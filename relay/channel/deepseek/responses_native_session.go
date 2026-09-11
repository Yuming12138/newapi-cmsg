package deepseek

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const nativeResponsesRequestKey = "deepseek_native_responses_request"

// prepareNativeResponsesRequest converts OpenAI's stateful continuation into
// the complete input transcript required by DeepSeek's stateless Responses API.
func prepareNativeResponsesRequest(info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	previousResponseID := strings.TrimSpace(request.PreviousResponseID)
	if previousResponseID == "" {
		return request, nil
	}

	session, err := model.GetResponsesChatSession(previousResponseID)
	if err != nil {
		return request, err
	}
	if session == nil {
		return request, fmt.Errorf("previous_response_id not found: %s", previousResponseID)
	}
	if info != nil && session.UserId != 0 && info.UserId != 0 && session.UserId != info.UserId {
		return request, fmt.Errorf("previous_response_id not found: %s", previousResponseID)
	}

	baseItems, err := decodeResponseItems(session.Items)
	if err != nil {
		return request, fmt.Errorf("decode native responses session items: %w", err)
	}
	pending, err := decodePendingToolCalls(session.PendingToolCalls)
	if err != nil {
		return request, fmt.Errorf("decode native responses session pending tool calls: %w", err)
	}
	return replayNativeResponsesRequest(request, baseItems, pending)
}

func replayNativeResponsesRequest(request dto.OpenAIResponsesRequest, baseItems []map[string]any, pending map[string]pendingToolCall) (dto.OpenAIResponsesRequest, error) {
	inputItems, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return request, err
	}
	if _, err := validateResponsesInput(pending, inputItems); err != nil {
		return request, err
	}

	allItems := mergeNativeResponsesItems(baseItems, inputItems)
	input, err := common.Marshal(allItems)
	if err != nil {
		return request, err
	}
	request.Input = input
	request.PreviousResponseID = ""
	// The original instructions are already stored as the first system item in
	// the replay transcript. Sending them again would duplicate the system turn.
	request.Instructions = nil
	return request, nil
}

func mergeNativeResponsesItems(baseItems, inputItems []map[string]any) []map[string]any {
	allItems := make([]map[string]any, 0, len(baseItems)+len(inputItems))
	seen := make(map[string]struct{})
	appendItem := func(item map[string]any) {
		key := nativeResponsesItemKey(item)
		if key != "" {
			if _, exists := seen[key]; exists {
				return
			}
			seen[key] = struct{}{}
		}
		allItems = append(allItems, item)
	}
	for _, item := range baseItems {
		appendItem(item)
	}
	for _, item := range inputItems {
		appendItem(item)
	}
	return allItems
}

func nativeResponsesItemKey(item map[string]any) string {
	itemType := common.Interface2String(item["type"])
	if id := common.Interface2String(item["id"]); id != "" {
		return itemType + "|id|" + id
	}
	if callID := common.Interface2String(item["call_id"]); callID != "" {
		switch itemType {
		case "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output":
			return itemType + "|call|" + callID
		}
	}
	return ""
}

// DeepSeek only supports apply_patch as a custom tool. Codex custom tools such
// as exec are exposed to the client as custom calls, but their transcript must
// be represented as function call pairs when replayed to DeepSeek.
func normalizeNativeResponsesInputForUpstream(request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	inputItems, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return request, err
	}
	changed := false
	functionCallIDs := make(map[string]struct{})
	for _, item := range inputItems {
		itemType := common.Interface2String(item["type"])
		if itemType == "function_call" {
			if callID := toolCallID(item); callID != "" {
				functionCallIDs[callID] = struct{}{}
			}
			continue
		}
		if itemType != "custom_tool_call" {
			continue
		}
		if common.Interface2String(item["name"]) == "apply_patch" {
			continue
		}
		callID := toolCallID(item)
		if callID == "" {
			continue
		}
		arguments, err := common.Marshal(map[string]any{"input": item["input"]})
		if err != nil {
			return request, err
		}
		item["type"] = "function_call"
		item["arguments"] = string(arguments)
		delete(item, "input")
		functionCallIDs[callID] = struct{}{}
		changed = true
	}
	for _, item := range inputItems {
		if common.Interface2String(item["type"]) != "custom_tool_call_output" {
			continue
		}
		if _, ok := functionCallIDs[toolCallID(item)]; ok {
			item["type"] = "function_call_output"
			changed = true
		}
	}
	if !changed {
		return request, nil
	}
	input, err := common.Marshal(inputItems)
	if err != nil {
		return request, err
	}
	request.Input = input
	return request, nil
}

func setNativeResponsesRequest(c *gin.Context, request dto.OpenAIResponsesRequest) {
	if c == nil {
		return
	}
	c.Set(nativeResponsesRequestKey, request)
}

func getNativeResponsesRequest(c *gin.Context) (dto.OpenAIResponsesRequest, bool) {
	if c == nil {
		return dto.OpenAIResponsesRequest{}, false
	}
	value, ok := c.Get(nativeResponsesRequestKey)
	if !ok {
		return dto.OpenAIResponsesRequest{}, false
	}
	request, ok := value.(dto.OpenAIResponsesRequest)
	return request, ok
}

func commitNativeResponsesSession(info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest, response *dto.OpenAIResponsesResponse) error {
	if response == nil || strings.TrimSpace(response.ID) == "" {
		return errors.New("native responses output is missing response id")
	}
	items, pending, err := buildNativeResponsesSessionState(request, response.Output)
	if err != nil {
		return err
	}
	itemsJSON, err := common.Marshal(items)
	if err != nil {
		return err
	}
	pendingJSON, err := common.Marshal(pending)
	if err != nil {
		return err
	}
	session := &model.ResponsesChatSession{
		ID:               response.ID,
		Items:            string(itemsJSON),
		PendingToolCalls: string(pendingJSON),
	}
	if info != nil {
		session.UserId = info.UserId
		session.TokenId = info.TokenId
		session.ModelName = info.OriginModelName
	}
	return model.SaveResponsesChatSession(session)
}

func buildNativeResponsesSessionState(request dto.OpenAIResponsesRequest, outputs []dto.ResponsesOutput) ([]map[string]any, map[string]pendingToolCall, error) {
	inputItems, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return nil, nil, err
	}
	items := make([]map[string]any, 0, len(inputItems)+len(outputs)+1)
	if instructions := rawMessageText(request.Instructions); instructions != "" {
		items = append(items, map[string]any{
			"type": "message",
			"role": "system",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": instructions,
			}},
		})
	}
	items = append(items, inputItems...)

	pending := make(map[string]pendingToolCall)
	for _, output := range outputs {
		item, err := nativeResponsesOutputItem(output)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, item)
		if !isResponsesToolCall(item) {
			continue
		}
		callID := toolCallID(item)
		if callID == "" {
			continue
		}
		pending[callID] = pendingToolCall{
			CallID: callID,
			Name:   common.Interface2String(item["name"]),
			Type:   common.Interface2String(item["type"]),
		}
	}
	return items, pending, nil
}

func nativeResponsesOutputItem(output dto.ResponsesOutput) (map[string]any, error) {
	callID := output.CallId
	if callID == "" {
		callID = output.ID
	}
	switch output.Type {
	case "message":
		item := map[string]any{
			"type":    "message",
			"role":    output.Role,
			"content": output.Content,
		}
		if item["role"] == "" {
			item["role"] = "assistant"
		}
		return item, nil
	case "function_call":
		item := map[string]any{
			"type":      "function_call",
			"call_id":   callID,
			"name":      output.Name,
			"arguments": common.JsonRawMessageToString(output.Arguments),
		}
		if output.Namespace != "" {
			item["namespace"] = output.Namespace
		}
		return item, nil
	case "custom_tool_call":
		input := any(common.JsonRawMessageToString(output.Input))
		if len(output.Input) > 0 {
			var decoded any
			if err := common.Unmarshal(output.Input, &decoded); err == nil {
				input = decoded
			}
		}
		return map[string]any{
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    output.Name,
			"input":   input,
		}, nil
	}
	data, err := common.Marshal(output)
	if err != nil {
		return nil, err
	}
	var item map[string]any
	if err := common.Unmarshal(data, &item); err != nil {
		return nil, err
	}
	return item, nil
}
