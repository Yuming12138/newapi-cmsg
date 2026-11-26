package openai

import (
	"bytes"
	"cmp"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const asxsResponsesContinuationKey = "asxs_responses_continuation"

type asxsResponsesHistory struct {
	id   string
	item map[int]string
}

func (history *asxsResponsesHistory) observe(data string, event dto.ResponsesStreamResponse) {
	switch event.Type {
	case "response.created":
		history.id = gjson.Get(data, "response.id").String()
	case dto.ResponsesOutputTypeItemDone:
		if event.OutputIndex != nil {
			if item := gjson.Get(data, "item"); item.IsObject() {
				switch item.Get("type").String() {
				case "reasoning", "compaction":
					return
				}
				if history.item == nil {
					history.item = make(map[int]string)
				}
				history.item[*event.OutputIndex] = item.Raw
			}
		}
	}
}

func (history *asxsResponsesHistory) complete(data string) (string, json.RawMessage) {
	responseID := cmp.Or(gjson.Get(data, "response.id").String(), history.id)
	output := gjson.Get(data, "response.output")
	if output.IsArray() && output.Raw != "[]" {
		return responseID, json.RawMessage(output.Raw)
	}
	indices := make([]int, 0, len(history.item))
	for idx := range history.item {
		indices = append(indices, idx)
	}
	slices.Sort(indices)
	items := make([]json.RawMessage, 0, len(indices))
	for _, idx := range indices {
		items = append(items, json.RawMessage(history.item[idx]))
	}
	raw, _ := common.Marshal(items)
	return responseID, raw
}

func asxsResponsesContinuation(c *gin.Context, info *relaycommon.RelayInfo) *service.ResponsesContinuation {
	if c == nil || !isASXSResponsesHistoryChannel(info, dto.OpenAIResponsesRequest{}) {
		return nil
	}
	value, _ := c.Get(asxsResponsesContinuationKey)
	turn, _ := value.(*service.ResponsesContinuation)
	return turn
}

func isASXSResponsesHistoryChannel(info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) bool {
	if info == nil || info.ChannelMeta == nil || !isOpenAIReasoningResponsesModel(request, info) {
		return false
	}
	if isASXSAPIBaseURL(info.ChannelBaseUrl) {
		return true
	}
	base, err := url.Parse(strings.TrimSpace(info.ChannelBaseUrl))
	return err == nil && strings.EqualFold(base.Hostname(), "api.hfjtc.cfd")
}

// ASXS validates replayed items by type. Chat-to-Responses bridges may emit
// the same content/role/image fields on every output item, including reasoning
// and tool calls for which those fields are invalid.
func normalizeASXSResponsesHistory(request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	input := bytes.TrimSpace(request.Input)
	if len(input) == 0 || input[0] != '[' {
		return request, nil
	}
	var items []json.RawMessage
	if err := common.Unmarshal(input, &items); err != nil {
		return request, err
	}
	changed := false
	kept := items[:0]
	for _, item := range items {
		parsed := gjson.ParseBytes(item)
		kind := parsed.Get("type").String()
		var remove []string
		var summary, content json.RawMessage
		switch kind {
		case "reasoning":
			id := parsed.Get("id").String()
			if !strings.HasPrefix(id, "rs_") || (parsed.Get("encrypted_content").String() == "" &&
				parsed.Get("content.#(type==\"reasoning_text\")").Exists()) {
				// A bridge's summary or another provider's plaintext thinking
				// cannot be used as native reasoning state on ASXS.
				changed = true
				continue
			}
			remove = []string{"content", "role", "quality", "size"}
			if !parsed.Get("summary").IsArray() {
				summary = []byte("[]")
			}
		case "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "compaction", "item_reference":
			remove = []string{"content", "role", "quality", "size"}
		case "message", "":
			remove = []string{"quality", "size"}
			if historyContent := parsed.Get("content"); historyContent.IsArray() {
				original := historyContent.Array()
				parts := make([]json.RawMessage, 0, len(original))
				for _, part := range original {
					switch part.Get("type").String() {
					case "reasoning_text", "summary_text":
						continue
					default:
						parts = append(parts, json.RawMessage(part.Raw))
					}
				}
				if len(parts) != len(original) {
					changed = true
					if len(parts) == 0 {
						continue
					}
					encoded, err := common.Marshal(parts)
					if err != nil {
						return request, err
					}
					content = encoded
				}
			}
		}
		if len(remove) == 0 {
			kept = append(kept, item)
			continue
		}
		fields := parsed.Map()
		needsRewrite := summary != nil || content != nil
		for _, field := range remove {
			_, exists := fields[field]
			needsRewrite = needsRewrite || exists
			delete(fields, field)
		}
		if needsRewrite {
			updated := make(map[string]json.RawMessage, len(fields))
			for name, value := range fields {
				updated[name] = json.RawMessage(value.Raw)
			}
			if summary != nil {
				updated["summary"] = summary
			}
			if content != nil {
				updated["content"] = content
			}
			encoded, err := common.Marshal(updated)
			if err != nil {
				return request, err
			}
			item = encoded
			changed = true
		}
		kept = append(kept, item)
	}
	if changed {
		updated, err := common.Marshal(kept)
		if err != nil {
			return request, err
		}
		request.Input = updated
	}
	return request, nil
}
