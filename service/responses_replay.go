package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// PrepareResponsesContinuation loads local history for the requested parent.
// A native scope keeps the original ID when the upstream can resolve it.
// Chat Completions adapters pass an empty scope and always replay known IDs.
func PrepareResponsesContinuation(info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest, nativeScope string) (dto.OpenAIResponsesRequest, *model.ResponsesChatSession, bool, error) {
	responseID := strings.TrimSpace(request.PreviousResponseID)
	if responseID == "" {
		return request, nil, false, nil
	}
	session, err := model.GetResponsesChatSession(responseID)
	if err != nil {
		return request, nil, false, err
	}
	if session == nil {
		// The upstream minted the id itself; leave the stateful call alone.
		return request, nil, false, nil
	}
	if info == nil || info.UserId <= 0 || session.UserId != info.UserId {
		return request, nil, false, fmt.Errorf("previous_response_id not found")
	}
	// Native ASXS responses can still be continued upstream on the original
	// model and credential. Use our transcript only when changing upstreams.
	if nativeScope != "" && session.UpstreamScope == nativeScope {
		return request, session, false, nil
	}
	updated, replayed, err := replayStoredResponsesSession(request, session)
	return updated, session, replayed, err
}

// ResponsesUpstreamScope identifies native continuation state without storing
// the upstream credential. Distinct channels with the same endpoint and key
// can reuse the same native response ID.
func ResponsesUpstreamScope(info *relaycommon.RelayInfo, modelName string) string {
	if info == nil || info.ChannelMeta == nil || info.ChannelBaseUrl == "" || modelName == "" {
		return ""
	}
	value := fmt.Sprintf("%s\x00%s\x00%s", strings.TrimRight(info.ChannelBaseUrl, "/"), modelName, info.ApiKey)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func replayStoredResponsesSession(request dto.OpenAIResponsesRequest, session *model.ResponsesChatSession) (dto.OpenAIResponsesRequest, bool, error) {
	baseItems, err := decodeResponsesSessionItems(session.Items)
	if err != nil {
		return request, false, err
	}
	if len(baseItems) == 0 {
		return request, false, nil
	}
	inputItems, err := normalizeResponsesReplayInput(request.Input)
	if err != nil {
		return request, false, err
	}
	replayed := mergePortableResponsesTranscripts(baseItems, inputItems)
	encoded, err := common.Marshal(replayed)
	if err != nil {
		return request, false, err
	}
	request.Input = encoded
	request.PreviousResponseID = ""
	// Keep current instructions. Older providers already stored their own
	// instructions as messages, so avoid repeating an identical system turn.
	if len(replayed) != 0 {
		first := replayed[0]
		instructions := common.JsonRawMessageToString(request.Instructions)
		if common.Interface2String(first["role"]) == "system" && instructions != "" && instructions == responsesReplayMessageText(first["content"]) {
			request.Instructions = nil
		}
	}
	return request, true, nil
}

func responsesReplayMessageText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	var text strings.Builder
	if parts, ok := content.([]any); ok {
		for _, value := range parts {
			if part, ok := value.(map[string]any); ok && common.Interface2String(part["type"]) == "input_text" {
				text.WriteString(common.Interface2String(part["text"]))
			}
		}
	}
	return text.String()
}

func decodeResponsesSessionItems(data string) ([]map[string]any, error) {
	if strings.TrimSpace(data) == "" {
		return nil, nil
	}
	var items []map[string]any
	if err := common.UnmarshalJsonStr(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func normalizeResponsesReplayInput(input json.RawMessage) ([]map[string]any, error) {
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

// mergePortableResponsesTranscripts preserves visible history, tool calls and
// results. A different provider cannot reuse the previous provider's reasoning
// ciphertext or compaction state.
func mergePortableResponsesTranscripts(baseItems, inputItems []map[string]any) []map[string]any {
	merged := make([]map[string]any, 0, len(baseItems)+len(inputItems))
	seen := make(map[string]struct{})
	appendItem := func(item map[string]any) {
		if item == nil {
			return
		}
		switch common.Interface2String(item["type"]) {
		case "reasoning", "compaction", "item_reference":
			return
		}
		if key := responsesTranscriptItemKey(item); key != "" {
			if _, exists := seen[key]; exists {
				return
			}
			seen[key] = struct{}{}
		}
		merged = append(merged, item)
	}
	for _, item := range baseItems {
		appendItem(item)
	}
	for _, item := range inputItems {
		appendItem(item)
	}
	return merged
}

func responsesTranscriptItemKey(item map[string]any) string {
	itemType := common.Interface2String(item["type"])
	if callID := common.Interface2String(item["call_id"]); callID != "" {
		return itemType + "|call|" + callID
	}
	if id := common.Interface2String(item["id"]); id != "" {
		return itemType + "|id|" + id
	}
	return ""
}

// DropForeignReasoningItems removes reasoning items whose content uses
// DeepSeek's plaintext reasoning_text parts. GPT-family upstreams cannot
// validate those items and reject the request, and they carry no information
// the new provider can use. Requests without a replayable session id (for
// example a client that resent the whole history inline) are covered here.
func DropForeignReasoningItems(request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	if len(request.Input) == 0 {
		return request, nil
	}
	input := strings.TrimSpace(string(request.Input))
	if len(input) == 0 || input[0] != '[' {
		return request, nil
	}
	var items []map[string]any
	if err := common.Unmarshal(request.Input, &items); err != nil {
		return request, nil
	}
	filtered := make([]map[string]any, 0, len(items))
	changed := false
	for _, item := range items {
		if hasForeignPlaintextReasoning(item) {
			changed = true
			continue
		}
		filtered = append(filtered, item)
	}
	if !changed {
		return request, nil
	}
	encoded, err := common.Marshal(filtered)
	if err != nil {
		return request, err
	}
	request.Input = encoded
	return request, nil
}

func hasForeignPlaintextReasoning(item map[string]any) bool {
	if item == nil || !strings.EqualFold(common.Interface2String(item["type"]), "reasoning") {
		return false
	}
	parts, ok := item["content"].([]any)
	if !ok {
		return false
	}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if common.Interface2String(part["type"]) == "reasoning_text" {
			return true
		}
	}
	return false
}
