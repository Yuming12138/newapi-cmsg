package service

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ResponsesContinuation retains the input of a successful turn so its response
// ID can later be used on a different upstream. It stores portable history,
// without a provider's opaque reasoning or compaction state.
type ResponsesContinuation struct {
	request       dto.OpenAIResponsesRequest
	parent        *model.ResponsesChatSession
	parentID      string
	upstreamScope string
}

func TrackResponsesContinuation(info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest, parent *model.ResponsesChatSession, parentID, upstreamScope string) *ResponsesContinuation {
	if info == nil || info.UserId <= 0 || info.ChannelMeta == nil || info.ChannelOtherSettings.DisableStore ||
		bytes.Equal(bytes.TrimSpace(request.Store), []byte("false")) ||
		(request.PreviousResponseID != "" && parent == nil) ||
		(len(bytes.TrimSpace(request.Conversation)) != 0 && common.GetJsonType(request.Conversation) != "null") {
		// An unknown provider-native ID or conversation may have additional
		// history upstream; recording a partial transcript would lose it.
		return nil
	}
	return &ResponsesContinuation{request: request, parent: parent, parentID: parentID, upstreamScope: upstreamScope}
}

func (turn *ResponsesContinuation) Save(info *relaycommon.RelayInfo, responseID string, output json.RawMessage) error {
	if turn == nil || info == nil || model.DB == nil || strings.TrimSpace(responseID) == "" || len(responseID) > 64 {
		return nil
	}
	input, err := normalizeResponsesReplayInput(turn.request.Input)
	if err != nil {
		return err
	}
	// A stateful continuation sends only the new turn to the native upstream.
	// In that case prepend the locally retained parent for future switches.
	var base []map[string]any
	if turn.parent != nil && turn.request.PreviousResponseID != "" {
		base, err = decodeResponsesSessionItems(turn.parent.Items)
		if err != nil {
			return err
		}
	}
	if instructions := common.JsonRawMessageToString(turn.request.Instructions); instructions != "" {
		item := map[string]any{"type": "message", "role": "system", "content": []any{
			map[string]any{"type": "input_text", "text": instructions},
		}}
		head := base
		if len(head) == 0 {
			head = input
		}
		if len(head) == 0 || common.Interface2String(head[0]["role"]) != "system" || responsesReplayMessageText(head[0]["content"]) != instructions {
			input = append([]map[string]any{item}, input...)
		}
	}
	var outputs []map[string]any
	if len(output) != 0 {
		if err := common.Unmarshal(output, &outputs); err != nil {
			return err
		}
	}
	items := mergePortableResponsesTranscripts(base, input)
	items = mergePortableResponsesTranscripts(items, outputs)
	if len(items) == 0 {
		return nil
	}
	pending := make(map[string]map[string]string)
	for _, item := range outputs {
		itemType := common.Interface2String(item["type"])
		callID := common.Interface2String(item["call_id"])
		if (itemType == "function_call" || itemType == "custom_tool_call") && callID != "" {
			pending[callID] = map[string]string{"call_id": callID, "name": common.Interface2String(item["name"]), "type": itemType}
		}
	}
	itemsJSON, err := common.Marshal(items)
	if err != nil {
		return err
	}
	pendingJSON, err := common.Marshal(pending)
	if err != nil {
		return err
	}
	conversationID := relaycommon.ResponsesConversationID(info)
	if conversationID == "" && turn.parent != nil && turn.parent.TokenId == info.TokenId {
		conversationID = turn.parent.ConversationId
	}
	return model.SaveResponsesChatSession(&model.ResponsesChatSession{
		ID: responseID, UserId: info.UserId, TokenId: info.TokenId, ConversationId: conversationID,
		ParentResponseId: turn.parentID, ModelName: info.OriginModelName, UpstreamScope: turn.upstreamScope,
		Items: string(itemsJSON), PendingToolCalls: string(pendingJSON),
	})
}
