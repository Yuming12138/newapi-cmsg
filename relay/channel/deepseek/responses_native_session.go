package deepseek

import (
	"errors"
	"fmt"
	"sort"
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
		// Codex echoes the reasoning it received back into the next request
		// while the replayed transcript already carries the upstream copy. The
		// id-based key never matches those two (one side has an item id, the
		// other does not), so every tool round used to store the same reasoning
		// twice and the prompt doubled in size. Adjacent identical reasoning
		// items carry no extra information, and dropping one still leaves a
		// reasoning item in front of the assistant group DeepSeek requires.
		if len(allItems) > 0 && isAdjacentReasoningDuplicate(allItems[len(allItems)-1], item) {
			return
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

// dedupInlineNativeResponsesInput applies the adjacent-reasoning de-duplication
// to a request that carries its whole transcript inline. Codex Desktop does not
// send previous_response_id, so the replay merge never runs and the client's own
// echo of the reasoning it received would otherwise reach DeepSeek twice.
func dedupInlineNativeResponsesInput(request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	if len(request.Input) == 0 || common.GetJsonType(request.Input) != "array" {
		return request, nil
	}
	inputItems, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return request, err
	}
	deduped := mergeNativeResponsesItems(nil, inputItems)
	if len(deduped) == len(inputItems) {
		return request, nil
	}
	encoded, err := common.Marshal(deduped)
	if err != nil {
		return request, err
	}
	request.Input = encoded
	return request, nil
}

func isAdjacentReasoningDuplicate(previous, next map[string]any) bool {
	nextText, ok := reasoningText(next)
	if !ok {
		return false
	}
	previousText, ok := reasoningText(previous)
	if !ok {
		return false
	}
	return nextText == previousText
}

// reasoningText reports whether the item is a reasoning item and returns its
// concatenated text. Only the text is compared because the upstream copy
// carries nothing but {type, content} while the client copy adds id, role and
// annotations.
func reasoningText(item map[string]any) (string, bool) {
	if common.Interface2String(item["type"]) != "reasoning" {
		return "", false
	}
	var b strings.Builder
	content, ok := item["content"].([]any)
	if !ok {
		return "", true
	}
	for _, part := range content {
		entry, ok := part.(map[string]any)
		if !ok {
			continue
		}
		b.WriteString(common.Interface2String(entry["text"]))
		b.WriteByte('\x00')
	}
	return b.String(), true
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

// DeepSeek advertises function tools. Convert Codex custom call history to
// matching function call pairs before replaying the stateless transcript.
func normalizeNativeResponsesInputForUpstream(request dto.OpenAIResponsesRequest, originalTools ...map[string]nativeResponseTool) (dto.OpenAIResponsesRequest, error) {
	inputItems, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return request, err
	}
	changed := false
	filtered := make([]map[string]any, 0, len(inputItems))
	for _, item := range inputItems {
		if common.Interface2String(item["type"]) == "additional_tools" {
			changed = true
			continue
		}
		filtered = append(filtered, item)
	}
	inputItems = filtered
	toolMap := buildNativeResponsesToolMap(request.GetToolsMap())
	appendNativeResponsesInputTools(toolMap, request.Input)
	if len(originalTools) > 0 && originalTools[0] != nil {
		toolMap = originalTools[0]
	}
	functionCallIDs := make(map[string]struct{})
	for _, item := range inputItems {
		itemType := common.Interface2String(item["type"])
		if isResponsesToolCall(item) {
			name := nativeResponsesToolNameForUpstream(item, toolMap)
			if name != common.Interface2String(item["name"]) {
				item["name"] = name
				changed = true
			}
			if _, exists := item["namespace"]; exists {
				delete(item, "namespace")
				changed = true
			}
		}
		if itemType == "function_call" {
			if callID := toolCallID(item); callID != "" {
				functionCallIDs[callID] = struct{}{}
			}
			continue
		}
		if itemType != "custom_tool_call" {
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
	inputItems, reordered := pairNativeResponsesToolOutputs(inputItems)
	changed = changed || reordered
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

// DeepSeek's stateless Responses API requires the calls in one assistant turn
// before that turn's results. Interleaving parallel calls and results creates a
// new assistant turn without its reasoning_text. Codex may also place a preamble
// between calls and results; keep it before the generating turn's call batch.
func pairNativeResponsesToolOutputs(items []map[string]any) ([]map[string]any, bool) {
	outputsByCall := make(map[string][]int)
	for index, item := range items {
		if isResponsesToolOutput(item) {
			if callID := toolCallID(item); callID != "" {
				outputsByCall[callID] = append(outputsByCall[callID], index)
			}
		}
	}

	commentaryBeforeCall := make(map[int][]int)
	relocated := make([]bool, len(items))
	for index, item := range items {
		if !isResponsesToolCall(item) {
			continue
		}
		for _, outputIndex := range outputsByCall[toolCallID(item)] {
			if outputIndex <= index {
				continue
			}
			for between := index + 1; between < outputIndex; between++ {
				candidate := items[between]
				// Do not move commentary across a user turn, new thinking, or
				// a result already available when that commentary was generated.
				if common.Interface2String(candidate["type"]) == "reasoning" ||
					isResponsesToolOutput(candidate) ||
					common.Interface2String(candidate["role"]) == "user" {
					break
				}
				if common.Interface2String(candidate["role"]) == "assistant" &&
					!isResponsesToolCall(candidate) && !relocated[between] {
					commentaryBeforeCall[index] = append(commentaryBeforeCall[index], between)
					relocated[between] = true
				}
			}
			break
		}
	}

	paired := make([]bool, len(items))
	order := make([]int, 0, len(items))
	for index, item := range items {
		if paired[index] || relocated[index] {
			continue
		}
		if !isResponsesToolCall(item) {
			order = append(order, index)
			continue
		}
		// Consecutive calls (ignoring relocated preambles) belong to the
		// same assistant output. Keep the entire batch before any result.
		callIndexes := []int{index}
		for next := index + 1; next < len(items); next++ {
			if relocated[next] {
				continue
			}
			if paired[next] || !isResponsesToolCall(items[next]) {
				break
			}
			callIndexes = append(callIndexes, next)
		}
		for _, callIndex := range callIndexes {
			order = append(order, commentaryBeforeCall[callIndex]...)
		}
		order = append(order, callIndexes...)
		outputIndexes := make([]int, 0, len(callIndexes))
		for _, callIndex := range callIndexes {
			paired[callIndex] = true
			for _, outputIndex := range outputsByCall[toolCallID(items[callIndex])] {
				if outputIndex <= callIndex || paired[outputIndex] {
					continue
				}
				outputIndexes = append(outputIndexes, outputIndex)
				paired[outputIndex] = true
				break
			}
		}
		// Concurrent tools can finish in a different order from their calls.
		// Retain result order and associate each result by its unchanged ID.
		sort.Ints(outputIndexes)
		order = append(order, outputIndexes...)
	}
	ordered := make([]map[string]any, 0, len(items))
	changed := false
	for index, originalIndex := range order {
		ordered = append(ordered, items[originalIndex])
		changed = changed || index != originalIndex
	}
	return ordered, changed
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
		ConversationId:   relaycommon.ResponsesConversationID(info),
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
