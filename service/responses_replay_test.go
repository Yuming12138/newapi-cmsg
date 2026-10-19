package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func decodeJSON(data []byte, target any) error {
	return json.Unmarshal(data, target)
}

func TestBuildForeignResponsesTranscriptDropsReasoningAndKeepsToolCalls(t *testing.T) {
	baseItems := []map[string]any{
		{"type": "message", "role": "system", "content": []any{map[string]any{"type": "input_text", "text": "instructions"}}},
		{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "fix the bug"}}},
		{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "deepseek chain of thought"}}},
		{"type": "function_call", "call_id": "call_1", "name": "exec", "arguments": "{}"},
		{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
	}
	inputItems := []map[string]any{
		{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "now switch to gpt"}}},
	}

	merged := buildForeignResponsesTranscript(baseItems, inputItems)

	require.Len(t, merged, 5)
	for _, item := range merged {
		require.NotEqual(t, "reasoning", item["type"])
	}
	require.Equal(t, "system", merged[0]["role"])
	require.Equal(t, "now switch to gpt", merged[len(merged)-1]["content"].([]any)[0].(map[string]any)["text"])
}

func TestBuildForeignResponsesTranscriptDeduplicatesRepeatedItems(t *testing.T) {
	baseItems := []map[string]any{
		{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "exec", "arguments": "{}"},
	}
	inputItems := []map[string]any{
		{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "exec", "arguments": "{}"},
		{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
	}

	merged := buildForeignResponsesTranscript(baseItems, inputItems)

	require.Len(t, merged, 2)
	require.Equal(t, "function_call_output", merged[1]["type"])
}

func TestDropForeignReasoningItemsRemovesPlaintextReasoning(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: []byte(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"reasoning","content":[{"type":"reasoning_text","text":"deepseek thinking"}]},
			{"type":"reasoning","content":[{"type":"summary_text","text":"gpt summary"}],"encrypted_content":"gAAA"}
		]`),
	}

	updated, err := DropForeignReasoningItems(request)
	require.NoError(t, err)

	var items []map[string]any
	require.NoError(t, decodeJSON(updated.Input, &items))
	require.Len(t, items, 2)
	require.Equal(t, "message", items[0]["type"])
	require.Equal(t, "reasoning", items[1]["type"])
	require.Equal(t, "gAAA", items[1]["encrypted_content"])
}
