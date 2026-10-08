package deepseek

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/samber/hot"
)

const (
	nativeReasoningNamespace = "new-api:deepseek:reasoning:v1:"
	nativeReasoningTTL       = 24 * time.Hour
	nativeReasoningMaxBytes  = 1 << 20
	nativeReasoningTimeout   = 500 * time.Millisecond
	nativeReasoningCapacity  = 32
)

var nativeReasoningMemory = sync.OnceValue(func() *hot.HotCache[string, string] {
	// Redis retains continuity across restarts; the small LRU avoids a Redis
	// round trip for each item in a long stateless Codex transcript.
	return hot.NewHotCache[string, string](hot.LRU, nativeReasoningCapacity).
		WithTTL(nativeReasoningTTL).WithJanitor().Build()
})

func nativeReasoningKey(info *relaycommon.RelayInfo, itemKey string) string {
	if info == nil || info.UserId <= 0 || info.ChannelMeta == nil || info.ChannelId <= 0 || itemKey == "" {
		return ""
	}
	thread := ""
	// Codex sends both thread-id and session-id. Prefer the stable thread ID;
	// retain the other names for older clients and compatible callers.
	for _, name := range []string{"thread-id", "x-codex-thread-id", "session-id", "session_id"} {
		for key, value := range info.RequestHeaders {
			if strings.EqualFold(key, name) {
				thread = strings.TrimSpace(value)
				break
			}
		}
		if thread != "" {
			break
		}
	}
	if thread == "" {
		// A bare call_1 is not globally unique. Unknown conversation identity
		// must not recover another thread's reasoning, even for the same token.
		return ""
	}
	// A caller's thread hint is never an authorization boundary on its own.
	// Bind it to the authenticated user/token and exact upstream route.
	credential := sha256.Sum256([]byte(info.ApiKey))
	scope, err := common.Marshal([]any{info.UserId, info.TokenId, info.ChannelId,
		info.ChannelBaseUrl, info.UpstreamModelName, fmt.Sprintf("%x", credential), thread, itemKey})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s%x", nativeReasoningNamespace, sha256.Sum256(scope))
}

func nativeReasoningAnchor(item map[string]any) string {
	switch common.Interface2String(item["type"]) {
	case "reasoning":
		if id := common.Interface2String(item["id"]); id != "" {
			return "reasoning|" + id
		}
	case "message":
		if common.Interface2String(item["role"]) == "assistant" {
			if id := common.Interface2String(item["id"]); id != "" {
				return "message|" + id
			}
		}
	case "function_call", "custom_tool_call":
		if id := toolCallID(item); id != "" {
			return "call|" + id
		}
	}
	return ""
}

func hasNativeReasoningText(item map[string]any) bool {
	for _, part := range mapSlice(item["content"]) {
		switch common.Interface2String(part["type"]) {
		case "reasoning_text", "text":
			if strings.TrimSpace(common.Interface2String(part["text"])) != "" {
				return true
			}
		}
	}
	return false
}

// nativeReasoningWriter retains only the current output's reasoning group, not
// every completed item in a long stream. Only actual upstream plaintext enters
// the cache; summaries and encrypted blobs never become raw reasoning.
type nativeReasoningWriter struct {
	info       *relaycommon.RelayInfo
	reasoning  []map[string]any
	anchorSeen bool
	textID     string
	textParts  []dto.ResponsesOutputContent
}

func rememberNativeResponsesReasoning(info *relaycommon.RelayInfo, outputs []dto.ResponsesOutput) error {
	writer := nativeReasoningWriter{info: info}
	return writer.remember(outputs)
}

func (w *nativeReasoningWriter) remember(outputs []dto.ResponsesOutput) error {
	if nativeReasoningKey(w.info, "scope") == "" {
		return nil
	}
	records := make(map[string]string)
	for _, output := range outputs {
		item, err := nativeResponsesOutputItem(output)
		if err != nil {
			return err
		}
		// Message/call session replay does not need IDs, but restoration does.
		if output.ID != "" {
			item["id"] = output.ID
		}
		if output.Type == "reasoning" {
			if !hasNativeReasoningText(item) {
				continue
			}
			normalizeNativeReasoningText(item)
			if w.anchorSeen {
				w.reasoning = nil
				w.anchorSeen = false
			}
			replaced := false
			for index, existing := range w.reasoning {
				if output.ID != "" && common.Interface2String(existing["id"]) == output.ID {
					w.reasoning[index] = item
					replaced = true
					break
				}
			}
			if !replaced {
				w.reasoning = append(w.reasoning, item)
			}
			if key := nativeReasoningKey(w.info, nativeReasoningAnchor(item)); key != "" {
				data, err := marshalNativeReasoningRecord([]map[string]any{item})
				if err != nil {
					return err
				}
				records[key] = string(data)
			}
			continue
		}
		if key := nativeReasoningKey(w.info, nativeReasoningAnchor(item)); key != "" && len(w.reasoning) > 0 {
			data, err := marshalNativeReasoningRecord(w.reasoning)
			if err != nil {
				return err
			}
			records[key] = string(data)
			w.anchorSeen = true
		}
	}
	return storeNativeReasoningRecords(records)
}

// Some upstream streams supply the original plaintext in text.done but omit
// content from output_item.done. Keep completed parts until a full item arrives.
func (w *nativeReasoningWriter) rememberTextDone(event dto.ResponsesStreamResponse) error {
	if event.ItemID == "" || event.Text == "" {
		return nil
	}
	index := 0
	if event.ContentIndex != nil {
		index = *event.ContentIndex
	}
	if index < 0 || index >= 128 {
		return nil
	}
	if w.textID != event.ItemID {
		w.textID = event.ItemID
		w.textParts = nil
	}
	for len(w.textParts) <= index {
		w.textParts = append(w.textParts, dto.ResponsesOutputContent{})
	}
	if len(event.Text) > nativeReasoningMaxBytes {
		w.textParts = nil
		return nil
	}
	w.textParts[index] = dto.ResponsesOutputContent{Type: "reasoning_text", Text: event.Text}
	size := 0
	for _, part := range w.textParts {
		size += len(part.Text)
	}
	if size > nativeReasoningMaxBytes {
		w.textParts = nil
		return nil
	}
	return w.remember([]dto.ResponsesOutput{{Type: "reasoning", ID: event.ItemID, Content: w.textParts}})
}

func storeNativeReasoningRecords(records map[string]string) error {
	if len(records) == 0 {
		return nil
	}
	redisOn := common.RedisEnabled && common.RDB != nil
	for key, value := range records {
		if len(value) > nativeReasoningMaxBytes {
			delete(records, key)
			continue
		}
		nativeReasoningMemory().SetWithTTL(key, value, nativeReasoningTTL)
	}
	if !redisOn || len(records) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeReasoningTimeout)
	defer cancel()
	pipe := common.RDB.Pipeline()
	defer pipe.Close()
	for key, value := range records {
		pipe.Set(ctx, key, value, nativeReasoningTTL)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func normalizeNativeReasoningText(item map[string]any) bool {
	changed := false
	for _, part := range mapSlice(item["content"]) {
		if part["type"] == "text" {
			part["type"] = "reasoning_text"
			changed = true
		}
	}
	return changed
}

type nativeReasoningRecord struct {
	Items     []map[string]any `json:"items"`
	ExpiresAt int64            `json:"expires_at"`
}

func marshalNativeReasoningRecord(items []map[string]any) ([]byte, error) {
	return common.Marshal(nativeReasoningRecord{Items: items, ExpiresAt: time.Now().Add(nativeReasoningTTL).UnixMilli()})
}

func decodeNativeReasoningRecord(raw string) ([]map[string]any, bool) {
	var record nativeReasoningRecord
	if len(raw) > nativeReasoningMaxBytes || common.UnmarshalJsonStr(raw, &record) != nil || len(record.Items) == 0 || record.ExpiresAt <= time.Now().UnixMilli() {
		return nil, false
	}
	originals := record.Items
	for _, original := range originals {
		if common.Interface2String(original["type"]) != "reasoning" || !hasNativeReasoningText(original) {
			return nil, false
		}
		normalizeNativeReasoningText(original)
	}
	return originals, true
}

func nativeReasoningIdentity(item map[string]any) string {
	if id := common.Interface2String(item["id"]); id != "" {
		return "id|" + id
	}
	// Without an item ID, only deduplicate identical plaintext within the
	// current turn. Explicit caller items are always retained.
	data, _ := common.Marshal(item["content"])
	return fmt.Sprintf("text|%x", sha256.Sum256(data))
}

func loadNativeReasoningRecords(keys []string) (map[string]string, error) {
	records := make(map[string]string, len(keys))
	missing := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if value, found, _ := nativeReasoningMemory().Get(key); found {
			records[key] = value
		} else {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 || !common.RedisEnabled || common.RDB == nil {
		return records, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeReasoningTimeout)
	defer cancel()
	values, err := common.RDB.MGet(ctx, missing...).Result()
	if err != nil {
		return records, err
	}
	for index, raw := range values {
		if value, ok := raw.(string); ok && len(value) <= nativeReasoningMaxBytes {
			records[missing[index]] = value
			nativeReasoningMemory().SetWithTTL(missing[index], value, nativeReasoningTTL)
		}
	}
	return records, nil
}

func restoreNativeResponsesReasoning(info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	items, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return request, err
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, nativeReasoningKey(info, nativeReasoningAnchor(item)))
	}
	records, cacheErr := loadNativeReasoningRecords(keys)
	if cacheErr != nil {
		// A cache outage must not discard otherwise usable caller history.
		logger.LogWarn(nil, "DeepSeek reasoning continuity cache unavailable")
	}
	restored := make([]map[string]any, 0, len(items))
	seenReasoning := make(map[string]bool)
	changed := false
	for _, item := range items {
		if common.Interface2String(item["role"]) == "user" || isResponsesToolOutput(item) {
			// IDs are unique output anchors and stay deduplicated across tool
			// results. ID-less text is only comparable within its current turn.
			for identity := range seenReasoning {
				if strings.HasPrefix(identity, "text|") {
					delete(seenReasoning, identity)
				}
			}
		}
		key := nativeReasoningKey(info, nativeReasoningAnchor(item))
		if raw, found := records[key]; found {
			if originals, valid := decodeNativeReasoningRecord(raw); valid {
				for _, original := range originals {
					identity := nativeReasoningIdentity(original)
					if seenReasoning[identity] {
						continue
					}
					restored = append(restored, original)
					seenReasoning[identity] = true
					changed = true
				}
				if common.Interface2String(item["type"]) == "reasoning" {
					changed = true
					continue
				}
			}
		}
		if common.Interface2String(item["type"]) == "reasoning" {
			// Codex may relabel genuine plain-text content as "text" on replay.
			// Keep every byte of the supplied text; do not use summary/encrypted_content.
			changed = normalizeNativeReasoningText(item) || changed
			if hasNativeReasoningText(item) {
				seenReasoning[nativeReasoningIdentity(item)] = true
			}
		}
		restored = append(restored, item)
	}
	if changed {
		request.Input, err = common.Marshal(restored)
	}
	return request, err
}
