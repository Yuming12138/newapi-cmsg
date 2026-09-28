package openai

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestNormalizeMoonshotResponsesRequestForCodexTools(t *testing.T) {
	tools, err := common.Marshal([]map[string]any{
		{"type": "custom", "name": "exec", "description": "Run code", "format": map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: SOURCE"}},
		{"type": "custom", "name": "apply_patch", "format": map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: SOURCE"}},
	})
	require.NoError(t, err)
	input, err := common.Marshal([]map[string]any{
		{"type": "reasoning", "encrypted_content": "foreign-provider"},
		{"type": "custom_tool_call", "id": "ctc_1", "call_id": "call_1", "name": "exec", "input": "print(1)"},
		{"type": "custom_tool_call_output", "call_id": "call_1", "output": "1"},
		{"type": "custom_tool_call", "call_id": "call_2", "name": "apply_patch", "input": "*** Begin Patch"},
	})
	require.NoError(t, err)
	request := dto.OpenAIResponsesRequest{
		Model: "kimi-k3", Tools: tools, Input: input,
		Reasoning: &dto.Reasoning{Effort: "medium", Summary: "auto"},
		Include:   []byte("[\"reasoning.encrypted_content\"]"),
		Store:     []byte("false"),
		Text:      []byte("{\"format\":{\"type\":\"text\"}}"),
	}

	converted, names, err := normalizeMoonshotResponsesRequest(request)
	require.NoError(t, err)
	require.Contains(t, names, "exec")
	require.Equal(t, "high", converted.Reasoning.Effort)
	require.Empty(t, converted.Reasoning.Summary)
	require.Nil(t, converted.Include)
	require.Nil(t, converted.Store)
	require.Nil(t, converted.Text)

	var gotTools []map[string]any
	require.NoError(t, common.Unmarshal(converted.Tools, &gotTools))
	require.Equal(t, "function", gotTools[0]["type"])
	require.Equal(t, "object", gotTools[0]["parameters"].(map[string]any)["type"])
	require.NotContains(t, gotTools[0], "format")
	require.Equal(t, "custom", gotTools[1]["type"])

	var gotInput []map[string]any
	require.NoError(t, common.Unmarshal(converted.Input, &gotInput))
	require.Len(t, gotInput, 3)
	require.Equal(t, "function_call", gotInput[0]["type"])
	require.Equal(t, "function_call_output", gotInput[1]["type"])
	require.Equal(t, "custom_tool_call", gotInput[2]["type"])
	var args map[string]string
	require.NoError(t, common.Unmarshal([]byte(gotInput[0]["arguments"].(string)), &args))
	require.Equal(t, "print(1)", args["input"])
}

func TestRewriteMoonshotFunctionCallForCustomClient(t *testing.T) {
	names := map[string]struct{}{"exec": {}}
	response, err := common.Marshal(map[string]any{
		"model":  "kimi-k3",
		"output": []map[string]any{{"type": "function_call", "id": "fc_123", "call_id": "call_123", "name": "exec", "arguments": "{\"input\":\"printf OK\"}"}},
		"usage":  map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
	require.NoError(t, err)
	rewritten, err := rewriteMoonshotResponseJSON(response, names)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, common.Unmarshal(rewritten, &got))
	item := got["output"].([]any)[0].(map[string]any)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "ctc_123", item["id"])
	require.Equal(t, "printf OK", item["input"])
	require.NotContains(t, item, "arguments")
}

func TestRewriteMoonshotStreamedFunctionCallForCustomClient(t *testing.T) {
	names := map[string]struct{}{"exec": {}}
	events := []map[string]any{
		{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{"type": "function_call", "id": "fc_123", "call_id": "call_123", "name": "exec", "arguments": ""}},
		{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc_123", "delta": "{\"input\":"},
		{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc_123", "delta": "\"printf OK\"}"},
		{"type": "response.function_call_arguments.done", "output_index": 1, "item_id": "fc_123", "arguments": "{\"input\":\"printf OK\"}"},
		{"type": "response.output_item.done", "output_index": 1, "item": map[string]any{"type": "function_call", "id": "fc_123", "call_id": "call_123", "name": "exec", "arguments": "{\"input\":\"printf OK\"}"}},
		{"type": "response.completed", "response": map[string]any{"output": []map[string]any{{"type": "function_call", "id": "fc_123", "call_id": "call_123", "name": "exec", "arguments": "{\"input\":\"printf OK\"}"}}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}}},
	}
	var upstream bytes.Buffer
	for i, event := range events {
		event["sequence_number"] = i
		frame, err := encodeMoonshotEvent(event)
		require.NoError(t, err)
		upstream.Write(frame)
	}
	source := io.NopCloser(strings.NewReader(upstream.String()))
	body := &moonshotResponsesBody{
		source: source, reader: bufio.NewReader(source),
		calls: make(map[int]*moonshotCallState), names: names,
	}
	rewritten, err := io.ReadAll(body)
	require.NoError(t, err)
	output := string(rewritten)
	require.Contains(t, output, "response.custom_tool_call_input.delta")
	require.Contains(t, output, "response.custom_tool_call_input.done")
	require.NotContains(t, output, "response.function_call_arguments.delta")
	require.Contains(t, output, "\"type\":\"custom_tool_call\"")
	require.Contains(t, output, "\"input\":\"printf OK\"")
	require.Contains(t, output, "\"input_tokens\":10")
	var sequence int
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var event map[string]any
		require.NoError(t, common.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
		require.Equal(t, float64(sequence), event["sequence_number"])
		sequence++
	}
	require.Equal(t, 5, sequence)
}

func TestMoonshotCustomToolResultRoundTrip(t *testing.T) {
	names := map[string]struct{}{"exec": {}}
	upstream := map[string]any{
		"type": "function_call", "id": "fc_123", "call_id": "call_123",
		"name": "exec", "arguments": "{\"input\":\"printf OK\"}",
	}
	require.True(t, rewriteMoonshotFunctionItem(upstream, names))
	tools, err := common.Marshal([]map[string]any{{"type": "custom", "name": "exec"}})
	require.NoError(t, err)
	input, err := common.Marshal([]map[string]any{
		upstream,
		{"type": "custom_tool_call_output", "call_id": "call_123", "output": "OK"},
	})
	require.NoError(t, err)
	converted, _, err := normalizeMoonshotResponsesRequest(dto.OpenAIResponsesRequest{
		Tools: tools, Input: input,
	})
	require.NoError(t, err)
	var items []map[string]any
	require.NoError(t, common.Unmarshal(converted.Input, &items))
	require.Equal(t, "function_call", items[0]["type"])
	require.Equal(t, "function_call_output", items[1]["type"])
	require.Equal(t, items[0]["call_id"], items[1]["call_id"])
	require.Equal(t, "OK", items[1]["output"])
	var args map[string]string
	require.NoError(t, common.Unmarshal([]byte(items[0]["arguments"].(string)), &args))
	require.Equal(t, "printf OK", args["input"])
}

func TestNormalizeMoonshotAdditionalCustomTools(t *testing.T) {
	input, err := common.Marshal([]map[string]any{
		{"type": "message", "role": "user", "content": "Use exec"},
		{"type": "additional_tools", "role": "developer", "tools": []map[string]any{
			{"type": "custom", "name": "exec", "description": "Run a command", "format": map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: SOURCE"}},
		}},
	})
	require.NoError(t, err)
	converted, names, err := normalizeMoonshotResponsesRequest(dto.OpenAIResponsesRequest{Model: "kimi-k3", Input: input})
	require.NoError(t, err)
	require.Contains(t, names, "exec")
	var items []map[string]any
	require.NoError(t, common.Unmarshal(converted.Input, &items))
	additional := items[1]["tools"].([]any)[0].(map[string]any)
	require.Equal(t, "function", additional["type"])
	require.Equal(t, "exec", additional["name"])
	require.NotContains(t, additional, "format")
	require.Equal(t, "object", additional["parameters"].(map[string]any)["type"])
}
