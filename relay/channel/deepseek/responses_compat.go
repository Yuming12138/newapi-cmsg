package deepseek

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const responsesTurnStateKey = "deepseek_responses_turn_state"

type responsesTurnState struct {
	ResponseID         string
	PreviousResponseID string
	Request            dto.OpenAIResponsesRequest
	ChatPayload        map[string]any
	BaseItems          []map[string]any
	InputItems         []map[string]any
	ToolMap            map[string]toolRestore
	InternalWebSearch  bool
}

type pendingToolCall struct {
	CallID string `json:"call_id"`
	Name   string `json:"name,omitempty"`
	Type   string `json:"type,omitempty"`
}

type toolRestore struct {
	Type      string
	Namespace string
	Name      string
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func convertResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	responseID := "resp_" + common.GetUUID()
	baseItems := make([]map[string]any, 0)
	pending := make(map[string]pendingToolCall)

	if request.PreviousResponseID != "" {
		session, err := model.GetResponsesChatSession(request.PreviousResponseID)
		if err != nil {
			return nil, err
		}
		if session == nil {
			return nil, fmt.Errorf("previous_response_id not found: %s", request.PreviousResponseID)
		}
		if session.UserId != 0 && info.UserId != 0 && session.UserId != info.UserId {
			return nil, fmt.Errorf("previous_response_id not found: %s", request.PreviousResponseID)
		}
		baseItems, err = decodeResponseItems(session.Items)
		if err != nil {
			return nil, fmt.Errorf("decode responses session items: %w", err)
		}
		pending, err = decodePendingToolCalls(session.PendingToolCalls)
		if err != nil {
			return nil, fmt.Errorf("decode responses session pending tool calls: %w", err)
		}
	}

	inputItems, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return nil, err
	}
	satisfiedCallIDs, err := validateResponsesInput(pending, inputItems)
	if err != nil {
		return nil, err
	}
	for _, callID := range satisfiedCallIDs {
		delete(pending, callID)
	}

	allItems := make([]map[string]any, 0, len(baseItems)+len(inputItems))
	allItems = append(allItems, baseItems...)
	allItems = append(allItems, inputItems...)
	messages := responseItemsToChatMessages(allItems)
	if request.PreviousResponseID == "" {
		if instructions := rawMessageText(request.Instructions); instructions != "" {
			messages = append([]dto.Message{{Role: "system", Content: instructions}}, messages...)
		}
	}

	tools, toolMap := responsesToolsToChat(request.GetToolsMap())
	payload := map[string]any{
		"model":    info.UpstreamModelName,
		"messages": messages,
		"stream":   false,
	}
	if request.MaxOutputTokens != nil {
		payload["max_completion_tokens"] = *request.MaxOutputTokens
	}
	if request.Temperature != nil {
		payload["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		payload["top_p"] = *request.TopP
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	if toolChoice := rawMessageText(request.ToolChoice); toolChoice == "auto" {
		payload["tool_choice"] = "auto"
	}
	if request.Reasoning != nil && request.Reasoning.Effort != "" {
		payload["reasoning"] = map[string]any{"effort": request.Reasoning.Effort}
	}

	if err := applyDeepSeekV4ThinkingPayload(info, payload); err != nil {
		return nil, err
	}
	chatPayload := filterChatPayload(payload)

	state := &responsesTurnState{
		ResponseID:         responseID,
		PreviousResponseID: request.PreviousResponseID,
		Request:            request,
		ChatPayload:        chatPayload,
		BaseItems:          baseItems,
		InputItems:         inputItems,
		ToolMap:            toolMap,
		InternalWebSearch:  hasInternalWebSearchTool(toolMap),
	}
	c.Set(responsesTurnStateKey, state)
	info.FinalRequestRelayFormat = types.RelayFormatOpenAI

	return chatPayload, nil
}

func handleResponsesChatResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	state, ok := getResponsesTurnState(c)
	if !ok {
		return nil, types.NewOpenAIError(errors.New("missing deepseek responses turn state"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var chatResponse dto.OpenAITextResponse
	if err := common.Unmarshal(responseBody, &chatResponse); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := chatResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if state.InternalWebSearch {
		var loopErr *types.NewAPIError
		chatResponse, loopErr = runDeepSeekInternalWebSearchLoop(c, info, state, &chatResponse)
		if loopErr != nil {
			return nil, loopErr
		}
	}

	responsesResponse, outputItems, usage, err := buildResponsesResponse(c, info, state, &chatResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if err := commitResponsesSession(info, state, outputItems); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeUpdateDataError, http.StatusInternalServerError)
	}

	if info.IsStream {
		if err := writeResponsesStream(c, responsesResponse); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		return usage, nil
	}

	jsonData, err := common.Marshal(responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	service.IOCopyBytesGracefully(c, resp, jsonData)
	return usage, nil
}

func getResponsesTurnState(c *gin.Context) (*responsesTurnState, bool) {
	value, ok := c.Get(responsesTurnStateKey)
	if !ok {
		return nil, false
	}
	state, ok := value.(*responsesTurnState)
	return state, ok && state != nil
}

var responsesChatRequestParams = map[string]bool{
	"model":                 true,
	"messages":              true,
	"stream":                true,
	"temperature":           true,
	"top_p":                 true,
	"max_completion_tokens": true,
	"frequency_penalty":     true,
	"presence_penalty":      true,
	"response_format":       true,
	"stop":                  true,
	"tools":                 true,
	"tool_choice":           true,
	"thinking":              true,
}

func filterChatPayload(payload map[string]any) map[string]any {
	filtered := make(map[string]any)
	for key, value := range payload {
		if responsesChatRequestParams[key] {
			filtered[key] = value
		}
	}
	if _, ok := filtered["thinking"]; !ok {
		if thinking := thinkingFromPayload(payload); thinking != nil {
			filtered["thinking"] = thinking
		}
	}
	if toolChoice, ok := filtered["tool_choice"]; ok && common.Interface2String(toolChoice) != "auto" {
		delete(filtered, "tool_choice")
	}
	return filtered
}

func thinkingFromPayload(payload map[string]any) map[string]string {
	effort := common.Interface2String(payload["reasoning_effort"])
	if effort == "" {
		if reasoning, ok := payload["reasoning"].(map[string]any); ok {
			effort = common.Interface2String(reasoning["effort"])
		}
	}
	if effort == "" {
		return nil
	}
	if effort == "none" || effort == "minimal" {
		return map[string]string{"type": "disabled"}
	}
	return map[string]string{"type": "enabled"}
}

func applyDeepSeekV4ThinkingPayload(info *relaycommon.RelayInfo, payload map[string]any) error {
	chatRequest := &dto.GeneralOpenAIRequest{
		Model: common.Interface2String(payload["model"]),
	}
	if err := applyDeepSeekV4OpenAIThinkingSuffix(info, chatRequest); err != nil {
		return err
	}
	payload["model"] = chatRequest.Model
	if len(chatRequest.THINKING) > 0 {
		var thinking map[string]any
		if err := common.Unmarshal(chatRequest.THINKING, &thinking); err != nil {
			return err
		}
		payload["thinking"] = thinking
	}
	if chatRequest.ReasoningEffort != "" {
		payload["reasoning_effort"] = chatRequest.ReasoningEffort
	}
	return nil
}

func normalizeResponsesInput(input json.RawMessage) ([]map[string]any, error) {
	if len(strings.TrimSpace(string(input))) == 0 || common.GetJsonType(input) == "null" {
		return nil, nil
	}

	switch common.GetJsonType(input) {
	case "string":
		var text string
		if err := common.Unmarshal(input, &text); err != nil {
			return nil, err
		}
		return []map[string]any{{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		}}, nil
	case "object":
		var item map[string]any
		if err := common.Unmarshal(input, &item); err != nil {
			return nil, err
		}
		return []map[string]any{item}, nil
	case "array":
		var items []map[string]any
		if err := common.Unmarshal(input, &items); err == nil {
			return items, nil
		}
		var rawItems []any
		if err := common.Unmarshal(input, &rawItems); err != nil {
			return nil, err
		}
		items = make([]map[string]any, 0, len(rawItems))
		for _, rawItem := range rawItems {
			if item, ok := rawItem.(map[string]any); ok {
				items = append(items, item)
			}
		}
		return items, nil
	default:
		return []map[string]any{{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": string(input)}},
		}}, nil
	}
}

func validateResponsesInput(pending map[string]pendingToolCall, inputItems []map[string]any) ([]string, error) {
	if len(pending) == 0 {
		for _, item := range inputItems {
			if isResponsesToolOutput(item) {
				return nil, fmt.Errorf("tool output references unknown call_id: %s", toolCallID(item))
			}
		}
		return nil, nil
	}

	unresolved := make(map[string]bool, len(pending))
	for callID := range pending {
		unresolved[callID] = true
	}
	satisfied := make([]string, 0)
	for _, item := range inputItems {
		if !isResponsesToolOutput(item) {
			continue
		}
		callID := toolCallID(item)
		if callID == "" {
			return nil, errors.New("tool output item is missing call_id")
		}
		if _, ok := pending[callID]; !ok {
			return nil, fmt.Errorf("tool output references unknown call_id: %s", callID)
		}
		if !unresolved[callID] {
			return nil, fmt.Errorf("tool output references an already satisfied call_id: %s", callID)
		}
		unresolved[callID] = false
		satisfied = append(satisfied, callID)
	}

	missing := make([]string, 0)
	for callID, stillPending := range unresolved {
		if stillPending {
			missing = append(missing, callID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("pending tool calls must be satisfied before continuing: %s", strings.Join(missing, ", "))
	}
	return satisfied, nil
}

func responseItemsToChatMessages(items []map[string]any) []dto.Message {
	messages := make([]dto.Message, 0, len(items))
	pendingToolCalls := make([]map[string]any, 0)

	flushToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		msg := dto.Message{Role: "assistant", Content: nil}
		msg.SetToolCalls(pendingToolCalls)
		messages = append(messages, msg)
		pendingToolCalls = nil
	}

	for _, item := range items {
		if isResponsesToolCall(item) {
			if toolCall := responseToolCallToChat(item); toolCall != nil {
				pendingToolCalls = append(pendingToolCalls, toolCall)
			}
			continue
		}
		flushToolCalls()
		if msg, ok := responseItemToChatMessage(item); ok {
			messages = append(messages, msg)
		}
	}
	flushToolCalls()
	return messages
}

func responseItemToChatMessage(item map[string]any) (dto.Message, bool) {
	itemType := common.Interface2String(item["type"])
	if isResponsesToolOutput(item) {
		return dto.Message{
			Role:       "tool",
			ToolCallId: toolCallID(item),
			Content:    outputToChatContent(item["output"]),
		}, true
	}
	if itemType != "message" && itemType != "" {
		return dto.Message{}, false
	}
	role := common.Interface2String(item["role"])
	return dto.Message{
		Role:    normalizeDeepSeekChatRole(role),
		Content: contentToChatContent(item["content"]),
	}, true
}

func normalizeDeepSeekChatRole(role string) string {
	switch strings.TrimSpace(role) {
	case "":
		return "user"
	case "developer":
		return "system"
	default:
		return role
	}
}

func responseToolCallToChat(item map[string]any) map[string]any {
	name := common.Interface2String(item["name"])
	if namespace := common.Interface2String(item["namespace"]); namespace != "" {
		name = namespacedToolName(namespace, name)
	}
	if name == "" {
		name = "unknown_tool"
	}
	return map[string]any{
		"id":   toolCallID(item),
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": responseItemArgumentsToChat(item),
		},
	}
}

func responseItemArgumentsToChat(item map[string]any) string {
	if common.Interface2String(item["type"]) == "custom_tool_call" {
		input := common.Interface2String(item["input"])
		data, err := common.Marshal(map[string]string{"input": input})
		if err != nil {
			return fmt.Sprintf(`{"input":%q}`, input)
		}
		return string(data)
	}
	arguments := item["arguments"]
	if text, ok := arguments.(string); ok {
		return text
	}
	if arguments == nil {
		return ""
	}
	data, err := common.Marshal(arguments)
	if err != nil {
		return common.Interface2String(arguments)
	}
	return string(data)
}

func contentToChatContent(content any) any {
	if content == nil {
		return ""
	}
	if text, ok := content.(string); ok {
		return text
	}
	items, ok := content.([]any)
	if !ok {
		return common.Interface2String(content)
	}
	parts := make([]map[string]any, 0, len(items))
	textParts := make([]string, 0)
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := common.Interface2String(item["type"])
		switch itemType {
		case "input_text", "output_text", "text":
			text := common.Interface2String(item["text"])
			if text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
				textParts = append(textParts, text)
			}
		case "input_image", "image_url":
			imageURL := imageURLFromItem(item)
			if imageURL != "" {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
			}
		}
	}
	for _, part := range parts {
		if common.Interface2String(part["type"]) == "image_url" {
			return parts
		}
	}
	return strings.Join(textParts, "\n")
}

func outputToChatContent(output any) any {
	if output == nil {
		return ""
	}
	if _, ok := output.([]any); ok {
		content := contentToChatContent(output)
		if common.Interface2String(content) != "" {
			return content
		}
	}
	if text, ok := output.(string); ok {
		return text
	}
	data, err := common.Marshal(output)
	if err != nil {
		return common.Interface2String(output)
	}
	return string(data)
}

func imageURLFromItem(item map[string]any) string {
	imageURL := item["image_url"]
	if imageURL == nil {
		imageURL = item["url"]
	}
	switch value := imageURL.(type) {
	case string:
		return value
	case map[string]any:
		return common.Interface2String(value["url"])
	default:
		return ""
	}
}

func responsesToolsToChat(tools []map[string]any) ([]map[string]any, map[string]toolRestore) {
	converted := make([]map[string]any, 0, len(tools))
	toolMap := make(map[string]toolRestore)
	webSearchAdded := false
	for _, tool := range tools {
		toolType := common.Interface2String(tool["type"])
		switch toolType {
		case "web_search", "web_search_preview":
			if !webSearchAdded && isActiveResponsesWebSearchTool(tool) {
				chatTool := deepSeekDoWebSearchTool(tool)
				converted = append(converted, chatTool)
				toolMap[deepSeekDoWebSearchToolName] = toolRestore{Type: "internal_web_search", Name: deepSeekDoWebSearchToolName}
				webSearchAdded = true
			}
			continue
		case "function":
			chatTool := responseFunctionToolToChat(tool)
			converted = append(converted, chatTool)
			if name := chatFunctionName(chatTool); name != "" {
				toolMap[name] = toolRestore{Type: "function", Name: name}
			}
		case "namespace":
			namespace := common.Interface2String(tool["name"])
			children, _ := tool["tools"].([]any)
			for _, rawChild := range children {
				child, ok := rawChild.(map[string]any)
				if !ok || common.Interface2String(child["type"]) != "function" {
					continue
				}
				childName := common.Interface2String(child["name"])
				flatName := namespacedToolName(namespace, childName)
				chatTool := responseFunctionToolToChat(map[string]any{
					"type":        "function",
					"name":        flatName,
					"description": child["description"],
					"parameters":  child["parameters"],
				})
				converted = append(converted, chatTool)
				toolMap[flatName] = toolRestore{Type: "namespace_function", Namespace: namespace, Name: childName}
			}
		case "file_search", "image_generation", "computer_use_preview":
			continue
		default:
			name := common.Interface2String(tool["name"])
			if name == "" {
				name = toolType
			}
			if name == "" {
				name = "unknown_tool"
			}
			converted = append(converted, customToolToChat(name, common.Interface2String(tool["description"])))
			toolMap[name] = toolRestore{Type: "custom", Name: name}
		}
	}
	return converted, toolMap
}

func responseFunctionToolToChat(tool map[string]any) map[string]any {
	if function, ok := tool["function"].(map[string]any); ok {
		parameters := function["parameters"]
		if parameters == nil {
			parameters = function["input_schema"]
		}
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        common.Interface2String(function["name"]),
				"description": common.Interface2String(function["description"]),
				"parameters":  sanitizeJSONSchema(parameters),
			},
		}
	}
	parameters := tool["parameters"]
	if parameters == nil {
		parameters = tool["input_schema"]
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        common.Interface2String(tool["name"]),
			"description": common.Interface2String(tool["description"]),
			"parameters":  sanitizeJSONSchema(parameters),
		},
	}
}

func customToolToChat(name, description string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": description,
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{
						"type":        "string",
						"description": "Freeform input for the custom tool.",
					},
				},
				"required":             []string{"input"},
				"additionalProperties": false,
			},
		},
	}
}

func xiaomiWebSearchToolToChat(tool map[string]any) map[string]any {
	if external, ok := tool["external_web_access"].(bool); ok && !external {
		return nil
	}
	converted := map[string]any{
		"type":         "web_search",
		"max_keyword":  3,
		"force_search": true,
		"limit":        1,
	}
	for _, key := range []string{"max_keyword", "limit"} {
		if value, ok := tool[key]; ok {
			converted[key] = value
		}
	}
	if value, ok := tool["force_search"]; ok {
		converted["force_search"] = value
	} else if value, ok := tool["forced_search"]; ok {
		converted["force_search"] = value
	}
	if location, ok := tool["user_location"].(map[string]any); ok {
		convertedLocation := make(map[string]any)
		for _, key := range []string{"type", "country", "region", "city"} {
			if value, ok := location[key]; ok {
				convertedLocation[key] = value
			}
		}
		if len(convertedLocation) > 0 {
			converted["user_location"] = convertedLocation
		}
	}
	return converted
}

func sanitizeJSONSchema(schema any) any {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	}
	sanitized, _ := sanitizeJSONSchemaWithNullable(schema)
	return sanitized
}

func sanitizeJSONSchemaWithNullable(schema any) (any, bool) {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return schema, false
	}

	if anyOf, ok := schemaMap["anyOf"].([]any); ok {
		nonNull := make([]any, 0, len(anyOf))
		nullable := false
		for _, option := range anyOf {
			if optionMap, ok := option.(map[string]any); ok && common.Interface2String(optionMap["type"]) == "null" {
				nullable = true
				continue
			}
			nonNull = append(nonNull, option)
		}
		if selected := selectXiaomiAnyOfSchema(nonNull); selected != nil {
			sanitizedSelected, childNullable := sanitizeJSONSchemaWithNullable(selected)
			merged := copyMapExcept(schemaMap, "anyOf", "type")
			if selectedMap, ok := sanitizedSelected.(map[string]any); ok {
				for key, value := range selectedMap {
					if _, exists := merged[key]; !exists {
						merged[key] = value
					}
				}
			}
			return merged, nullable || childNullable
		}
	}

	sanitized := copyMap(schemaMap)
	nullable := false
	if typeList, ok := sanitized["type"].([]any); ok {
		nonNullTypes := make([]any, 0, len(typeList))
		for _, item := range typeList {
			if common.Interface2String(item) == "null" {
				nullable = true
				continue
			}
			nonNullTypes = append(nonNullTypes, item)
		}
		if len(nonNullTypes) == 1 {
			sanitized["type"] = nonNullTypes[0]
		}
	}

	nullableProperties := make(map[string]bool)
	if properties, ok := sanitized["properties"].(map[string]any); ok {
		sanitizedProperties := make(map[string]any, len(properties))
		for name, child := range properties {
			sanitizedChild, childNullable := sanitizeJSONSchemaWithNullable(child)
			sanitizedProperties[name] = sanitizedChild
			if childNullable {
				nullableProperties[name] = true
			}
		}
		sanitized["properties"] = sanitizedProperties
	}
	if len(nullableProperties) > 0 {
		if required, ok := sanitized["required"].([]any); ok {
			filtered := make([]any, 0, len(required))
			for _, item := range required {
				name := common.Interface2String(item)
				if !nullableProperties[name] {
					filtered = append(filtered, item)
				}
			}
			sanitized["required"] = filtered
		}
	}
	if items, ok := sanitized["items"].(map[string]any); ok {
		sanitized["items"] = sanitizeJSONSchema(items)
	}
	if common.Interface2String(sanitized["type"]) == "object" {
		sanitized["additionalProperties"] = false
	}
	return sanitized, nullable
}

func selectXiaomiAnyOfSchema(options []any) map[string]any {
	for _, preferredType := range []string{"string", "integer", "number", "boolean", "object", "array"} {
		for _, option := range options {
			optionMap, ok := option.(map[string]any)
			if ok && common.Interface2String(optionMap["type"]) == preferredType {
				return optionMap
			}
		}
	}
	for _, option := range options {
		if optionMap, ok := option.(map[string]any); ok {
			return optionMap
		}
	}
	return nil
}

func buildResponsesResponse(c *gin.Context, info *relaycommon.RelayInfo, state *responsesTurnState, chatResponse *dto.OpenAITextResponse) (*dto.OpenAIResponsesResponse, []map[string]any, *dto.Usage, error) {
	outputItems := make([]map[string]any, 0)
	responseOutput := make([]dto.ResponsesOutput, 0)
	outputText := ""

	if len(chatResponse.Choices) > 0 {
		message := chatResponse.Choices[0].Message
		if text := message.StringContent(); text != "" {
			itemID := "msg_" + common.GetUUID()
			outputText = text
			outputItems = append(outputItems, map[string]any{
				"type":    "message",
				"id":      itemID,
				"status":  "completed",
				"role":    "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
			})
			responseOutput = append(responseOutput, dto.ResponsesOutput{
				Type:    "message",
				ID:      itemID,
				Status:  "completed",
				Role:    "assistant",
				Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: text, Annotations: []interface{}{}}},
			})
		}

		toolCalls := parseChatToolCalls(message.ToolCalls)
		for _, toolCall := range toolCalls {
			item, output := chatToolCallToResponsesOutput(toolCall, state.ToolMap)
			outputItems = append(outputItems, item)
			responseOutput = append(responseOutput, output)
		}
	}

	usage := normalizeResponsesUsage(c, info, chatResponse, outputText)
	response := &dto.OpenAIResponsesResponse{
		ID:                 state.ResponseID,
		Object:             "response",
		CreatedAt:          int(time.Now().Unix()),
		Status:             json.RawMessage(`"completed"`),
		Instructions:       rawOrNull(state.Request.Instructions),
		Model:              info.OriginModelName,
		Output:             responseOutput,
		PreviousResponseID: rawStringOrNull(state.PreviousResponseID),
		Reasoning:          state.Request.Reasoning,
		ToolChoice:         rawOrNull(state.Request.ToolChoice),
		Tools:              state.Request.GetToolsMap(),
		Truncation:         rawOrNull(state.Request.Truncation),
		Usage:              usage,
		User:               rawOrNull(state.Request.User),
		Metadata:           rawOrNull(state.Request.Metadata),
	}
	if state.Request.MaxOutputTokens != nil {
		response.MaxOutputTokens = int(*state.Request.MaxOutputTokens)
	}
	if state.Request.Temperature != nil {
		response.Temperature = *state.Request.Temperature
	}
	if state.Request.TopP != nil {
		response.TopP = *state.Request.TopP
	}
	if len(state.Request.ParallelToolCalls) > 0 {
		var parallel bool
		if err := common.Unmarshal(state.Request.ParallelToolCalls, &parallel); err == nil {
			response.ParallelToolCalls = parallel
		}
	}
	if len(state.Request.Store) > 0 {
		var store bool
		if err := common.Unmarshal(state.Request.Store, &store); err == nil {
			response.Store = store
		}
	}
	return response, outputItems, usage, nil
}

func chatToolCallToResponsesOutput(toolCall chatToolCall, toolMap map[string]toolRestore) (map[string]any, dto.ResponsesOutput) {
	callID := toolCall.ID
	if callID == "" {
		callID = "call_" + common.GetUUID()
	}
	name := toolCall.Function.Name
	restore := toolMap[name]
	if restore.Type == "custom" {
		input := customInputFromArguments(toolCall.Function.Arguments)
		return map[string]any{
			"type":    "custom_tool_call",
			"id":      callID,
			"call_id": callID,
			"name":    name,
			"input":   input,
		}, dto.ResponsesOutput{Type: "custom_tool_call", ID: callID, CallId: callID, Name: name, Input: rawString(input)}
	}
	outputName := name
	namespace := ""
	if restore.Type == "namespace_function" {
		outputName = restore.Name
		namespace = restore.Namespace
	}
	item := map[string]any{
		"type":      "function_call",
		"id":        callID,
		"call_id":   callID,
		"name":      outputName,
		"arguments": toolCall.Function.Arguments,
	}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return item, dto.ResponsesOutput{Type: "function_call", ID: callID, CallId: callID, Name: outputName, Namespace: namespace, Arguments: rawString(toolCall.Function.Arguments)}
}

func normalizeResponsesUsage(c *gin.Context, info *relaycommon.RelayInfo, chatResponse *dto.OpenAITextResponse, outputText string) *dto.Usage {
	usage := chatResponse.Usage
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		if outputText != "" {
			usage = *service.ResponseText2Usage(c, outputText, info.UpstreamModelName, info.GetEstimatePromptTokens())
		}
	} else if usage.PromptTokens == 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	usage.InputTokens = usage.PromptTokens
	usage.OutputTokens = usage.CompletionTokens
	usage.UsageSource = "deepseek_chat"
	usage.UsageSemantic = "openai"
	return &usage
}

func commitResponsesSession(info *relaycommon.RelayInfo, state *responsesTurnState, outputItems []map[string]any) error {
	items := make([]map[string]any, 0, len(state.BaseItems)+len(state.InputItems)+len(outputItems))
	items = append(items, state.BaseItems...)
	items = append(items, state.InputItems...)
	items = append(items, outputItems...)

	pending := make(map[string]pendingToolCall)
	for _, item := range outputItems {
		if !isResponsesToolCall(item) {
			continue
		}
		callID := toolCallID(item)
		if callID == "" {
			continue
		}
		pending[callID] = pendingToolCall{CallID: callID, Name: common.Interface2String(item["name"]), Type: common.Interface2String(item["type"])}
	}

	itemsJSON, err := common.Marshal(items)
	if err != nil {
		return err
	}
	pendingJSON, err := common.Marshal(pending)
	if err != nil {
		return err
	}
	return model.SaveResponsesChatSession(&model.ResponsesChatSession{
		ID:               state.ResponseID,
		UserId:           info.UserId,
		TokenId:          info.TokenId,
		ModelName:        info.OriginModelName,
		Items:            string(itemsJSON),
		PendingToolCalls: string(pendingJSON),
	})
}

func writeResponsesStream(c *gin.Context, response *dto.OpenAIResponsesResponse) error {
	helper.SetEventStreamHeaders(c)
	if err := writeSSEEvent(c, "response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": response.ID}}); err != nil {
		return err
	}
	for index, item := range response.Output {
		addedItem := item
		if item.Type == "message" {
			addedItem.Content = make([]dto.ResponsesOutputContent, len(item.Content))
			for i, content := range item.Content {
				content.Text = ""
				addedItem.Content[i] = content
			}
		} else if item.Type == "function_call" {
			addedItem.Arguments = rawString("")
		} else if item.Type == "custom_tool_call" {
			addedItem.Input = rawString("")
		}
		if err := writeSSEEvent(c, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": index, "item": addedItem}); err != nil {
			return err
		}
		if item.Type == "message" {
			for _, content := range item.Content {
				if content.Type != "output_text" || content.Text == "" {
					continue
				}
				if err := writeSSEEvent(c, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": index, "item_id": item.ID, "delta": content.Text}); err != nil {
					return err
				}
			}
		} else if item.Type == "function_call" {
			if delta := item.ArgumentsString(); delta != "" {
				if err := writeSSEEvent(c, "response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "output_index": index, "item_id": item.ID, "call_id": item.CallId, "delta": delta}); err != nil {
					return err
				}
			}
		} else if item.Type == "custom_tool_call" {
			if delta := common.JsonRawMessageToString(item.Input); delta != "" {
				if err := writeSSEEvent(c, "response.custom_tool_call_input.delta", map[string]any{"type": "response.custom_tool_call_input.delta", "output_index": index, "item_id": item.ID, "call_id": item.CallId, "delta": delta}); err != nil {
					return err
				}
			}
		}
		if err := writeSSEEvent(c, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": index, "item": item}); err != nil {
			return err
		}
	}
	if err := writeSSEEvent(c, "response.completed", map[string]any{"type": "response.completed", "response": response}); err != nil {
		return err
	}
	helper.Done(c)
	return nil
}

func writeSSEEvent(c *gin.Context, event string, payload any) error {
	data, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := c.Writer.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))); err != nil {
		return err
	}
	return helper.FlushWriter(c)
}

func parseChatToolCalls(raw json.RawMessage) []chatToolCall {
	if len(strings.TrimSpace(string(raw))) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var toolCalls []chatToolCall
	if err := common.Unmarshal(raw, &toolCalls); err != nil {
		return nil
	}
	return toolCalls
}

func customInputFromArguments(arguments string) string {
	var parsed map[string]any
	if err := common.Unmarshal(common.StringToByteSlice(arguments), &parsed); err != nil {
		return arguments
	}
	if input, ok := parsed["input"]; ok {
		return common.Interface2String(input)
	}
	return arguments
}

func isResponsesToolCall(item map[string]any) bool {
	itemType := common.Interface2String(item["type"])
	return itemType == "function_call" || itemType == "custom_tool_call"
}

func isResponsesToolOutput(item map[string]any) bool {
	itemType := common.Interface2String(item["type"])
	return itemType == "function_call_output" || itemType == "custom_tool_call_output"
}

func toolCallID(item map[string]any) string {
	callID := common.Interface2String(item["call_id"])
	if callID != "" {
		return callID
	}
	return common.Interface2String(item["id"])
}

func chatFunctionName(tool map[string]any) string {
	function, _ := tool["function"].(map[string]any)
	return common.Interface2String(function["name"])
}

func namespacedToolName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	if strings.HasSuffix(namespace, "_") || strings.HasPrefix(name, "_") {
		return namespace + name
	}
	return namespace + "_" + name
}

func decodeResponseItems(data string) ([]map[string]any, error) {
	if strings.TrimSpace(data) == "" {
		return nil, nil
	}
	var items []map[string]any
	if err := common.UnmarshalJsonStr(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func decodePendingToolCalls(data string) (map[string]pendingToolCall, error) {
	pending := make(map[string]pendingToolCall)
	if strings.TrimSpace(data) == "" {
		return pending, nil
	}
	if err := common.UnmarshalJsonStr(data, &pending); err != nil {
		return nil, err
	}
	return pending, nil
}

func rawMessageText(raw json.RawMessage) string {
	return strings.TrimSpace(common.JsonRawMessageToString(raw))
}

func rawOrNull(raw json.RawMessage) json.RawMessage {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

func rawString(value string) json.RawMessage {
	data, err := common.Marshal(value)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return data
}

func rawStringOrNull(value string) json.RawMessage {
	if value == "" {
		return json.RawMessage("null")
	}
	return rawString(value)
}

func copyMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func copyMapExcept(input map[string]any, excluded ...string) map[string]any {
	excludedSet := make(map[string]bool, len(excluded))
	for _, key := range excluded {
		excludedSet[key] = true
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		if !excludedSet[key] {
			output[key] = value
		}
	}
	return output
}
