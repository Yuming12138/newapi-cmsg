package service

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ReplayForeignResponsesSession rebuilds a complete transcript when a Responses
// request continues a session that another provider minted.
//
// DeepSeek and MiMo keep their conversation state in responses_chat_sessions,
// keyed by the response id that was returned to the client, because their
// Responses endpoints are stateless. When the client later sends that id to a
// different upstream on purpose - for example a DeepSeek-origin thread that the
// user switches to a GPT channel - the upstream can neither resolve the id nor
// validate the reasoning ciphertext bound to it, and answers
// "The upstream could not validate encrypted continuation context".
//
// Replaying the stored transcript turns the continuation into a plain stateless
// request, so an intentional cross-provider switch keeps the conversation
// instead of forcing the client to rebuild it. The boolean result reports
// whether the request was rewritten.
func ReplayForeignResponsesSession(info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, bool, error) {
	responseID := strings.TrimSpace(request.PreviousResponseID)
	if responseID == "" {
		return request, false, nil
	}
	session, err := model.GetResponsesChatSession(responseID)
	if err != nil {
		return request, false, err
	}
	if session == nil {
		// The upstream minted the id itself; leave the stateful call alone.
		return request, false, nil
	}
	if info != nil && session.UserId != 0 && info.UserId != 0 && session.UserId != info.UserId {
		// Sessions are per-user; never replay another account's transcript.
		return request, false, nil
	}
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
	replayed := buildForeignResponsesTranscript(baseItems, inputItems)
	encoded, err := common.Marshal(replayed)
	if err != nil {
		return request, false, err
	}
	request.Input = encoded
	request.PreviousResponseID = ""
	// The original instructions are already the first system item of the stored
	// transcript; sending them again would duplicate the system turn.
	request.Instructions = nil
	return request, true, nil
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

// buildForeignResponsesTranscript merges the stored transcript with the new
// turn and drops reasoning items: their ciphertext belongs to the provider that
// produced them, so the new upstream would reject the whole request with an
// encrypted-continuation or decryption error. Visible messages and tool calls
// carry everything the model needs to continue.
func buildForeignResponsesTranscript(baseItems, inputItems []map[string]any) []map[string]any {
	merged := make([]map[string]any, 0, len(baseItems)+len(inputItems))
	seen := make(map[string]struct{})
	appendItem := func(item map[string]any) {
		if item == nil {
			return
		}
		if strings.EqualFold(common.Interface2String(item["type"]), "reasoning") {
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
	if id := common.Interface2String(item["id"]); id != "" {
		return itemType + "|id|" + id
	}
	if callID := common.Interface2String(item["call_id"]); callID != "" {
		return itemType + "|call|" + callID
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
