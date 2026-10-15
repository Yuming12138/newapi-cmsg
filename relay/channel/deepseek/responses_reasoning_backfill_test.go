package deepseek

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func backfillTestRequest(t *testing.T, items ...map[string]any) dto.OpenAIResponsesRequest {
	t.Helper()
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	return dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Input: raw}
}

func reasoningItemTypes(t *testing.T, request dto.OpenAIResponsesRequest) []string {
	t.Helper()
	items, err := normalizeResponsesInput(request.Input)
	require.NoError(t, err)
	types := make([]string, 0, len(items))
	for _, item := range items {
		types = append(types, itemTypeOf(item))
	}
	return types
}

func itemTypeOf(item map[string]any) string {
	if t, ok := item["type"].(string); ok {
		return t
	}
	return ""
}

// Each shape below was verified against api.deepseek.com on 2026-10-15: without
// backfill the request returns 400 "The `reasoning_text` in the thinking mode
// must be passed back to the API."; with a placeholder reasoning item ahead of
// the assistant content it returns 200.
func TestBackfillMissingNativeReasoning_ToolCallWithoutReasoning(t *testing.T) {
	request := backfillTestRequest(t,
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "t1"}}},
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "hello"}}},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "ls"}}},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "shell", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "files"},
	)
	got, changed, err := backfillMissingNativeReasoning(request)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"message", "reasoning", "message", "message", "reasoning", "function_call", "function_call_output"}, reasoningItemTypes(t, got))
}

func TestBackfillMissingNativeReasoning_MessageAfterToolResult(t *testing.T) {
	request := backfillTestRequest(t,
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "t1"}}},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "shell", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "files"},
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}},
	)
	got, changed, err := backfillMissingNativeReasoning(request)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"message", "reasoning", "function_call", "function_call_output", "reasoning", "message"}, reasoningItemTypes(t, got))
}

func TestBackfillMissingNativeReasoning_NewToolBatchAfterResult(t *testing.T) {
	request := backfillTestRequest(t,
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "t1"}}},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "shell", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "files"},
		map[string]any{"type": "function_call", "call_id": "c2", "name": "shell", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c2", "output": "files"},
	)
	got, changed, err := backfillMissingNativeReasoning(request)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"message", "reasoning", "function_call", "function_call_output", "reasoning", "function_call", "function_call_output"}, reasoningItemTypes(t, got))
}

func TestBackfillMissingNativeReasoning_CustomToolAfterResult(t *testing.T) {
	request := backfillTestRequest(t,
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "t1"}}},
		map[string]any{"type": "custom_tool_call", "call_id": "c1", "name": "apply_patch", "input": "patch"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "ok"},
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}},
	)
	got, changed, err := backfillMissingNativeReasoning(request)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"message", "reasoning", "custom_tool_call", "custom_tool_call_output", "reasoning", "message"}, reasoningItemTypes(t, got))
}

// A plain user turn followed by an assistant message without reasoning is
// accepted by DeepSeek (verified 200), so it must stay untouched.
func TestBackfillMissingNativeReasoning_PlainAssistantMessageUnchanged(t *testing.T) {
	request := backfillTestRequest(t,
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "hello"}}},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "ls"}}},
		map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "t"}}},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "shell", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "files"},
	)
	got, changed, err := backfillMissingNativeReasoning(request)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, request.Input, got.Input)
}

func TestBackfillMissingNativeReasoning_AlreadyHasReasoning(t *testing.T) {
	request := backfillTestRequest(t,
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "t1"}}},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "shell", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "files"},
	)
	got, changed, err := backfillMissingNativeReasoning(request)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, request.Input, got.Input)
}

// The full cross-provider pipeline: a gemini-CPA reasoning item (summary only)
// replayed by the client, followed by a reasoning-less tool turn and a final
// assistant message. normalizeForeignResponsesReasoning converts the summary to
// reasoning_text and backfill covers the two turns DeepSeek would reject.
func TestBackfillMissingNativeReasoning_CrossProviderHistory(t *testing.T) {
	request := backfillTestRequest(t,
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"type": "reasoning", "id": "rs_gemini", "encrypted_content": "",
			"summary": []any{map[string]any{"type": "summary_text", "text": "gemini thought"}}},
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "hello"}}},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "ls"}}},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "shell", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "files"},
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}},
	)
	foreign, counts, err := normalizeForeignResponsesReasoning(request)
	require.NoError(t, err)
	require.True(t, counts.Summary > 0)
	got, changed, err := backfillMissingNativeReasoning(foreign)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"message", "reasoning", "message", "message", "reasoning", "function_call", "function_call_output", "reasoning", "message"}, reasoningItemTypes(t, got))
}
