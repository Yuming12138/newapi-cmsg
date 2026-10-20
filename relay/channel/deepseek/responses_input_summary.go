package deepseek

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
)

// summarizeNativeResponsesInput renders a compact histogram of an incoming
// Responses payload, splitting reasoning items into real-text and placeholder
// ones. It exists to diagnose premature end-of-turn behaviour on long Codex
// threads: DeepSeek requires reasoning_text to be replayed, and a transcript
// made of blank reasoning makes the model stop mid-task.
func summarizeNativeResponsesInput(input json.RawMessage) string {
	if len(strings.TrimSpace(string(input))) == 0 {
		return "empty"
	}
	parsed := gjson.ParseBytes(input)
	if !parsed.IsArray() {
		return common.GetJsonType(input)
	}
	counts := make(map[string]int)
	reasoningReal := 0
	reasoningBlank := 0
	for _, item := range parsed.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "" {
			itemType = "?"
		}
		counts[itemType]++
		if itemType == "reasoning" {
			text := strings.TrimSpace(collectResponsesReasoningText(item))
			if text == "" {
				reasoningBlank++
			} else {
				reasoningReal++
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	parts = append(parts, fmt.Sprintf("reasoning_real=%d", reasoningReal), fmt.Sprintf("reasoning_blank=%d", reasoningBlank))
	return strings.Join(parts, ",")
}

func collectResponsesReasoningText(item gjson.Result) string {
	var sb strings.Builder
	for _, part := range item.Get("content").Array() {
		sb.WriteString(part.Get("text").String())
	}
	return sb.String()
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
