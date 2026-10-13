package deepseek

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestNormalizeNativeResponsesToolsPromotesCodexAdditionalTools(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Tools: json.RawMessage(`[{"type":"custom","name":"apply_patch","description":"Patch a file"}]`),
		Input: json.RawMessage(`[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"custom","name":"exec","description":"Run code","format":{"type":"grammar"}},
				{"type":"function","name":"wait","description":"Wait","parameters":{"type":"object","properties":{"cell_id":{"type":"string"}}}},
				{"type":"namespace","name":"collaboration","tools":[]}
			]},
			{"type":"message","role":"user","content":"Draw a pelican"}
		]`),
	}

	withTools, err := normalizeNativeResponsesToolsForUpstream(request)
	require.NoError(t, err)
	var tools []map[string]any
	require.NoError(t, common.Unmarshal(withTools.Tools, &tools))
	require.Len(t, tools, 3)
	require.Equal(t, []string{"apply_patch", "exec", "wait"}, []string{
		tools[0]["name"].(string), tools[1]["name"].(string), tools[2]["name"].(string),
	})
	for _, tool := range tools {
		require.Equal(t, "function", tool["type"])
	}
	require.Equal(t, "string", tools[1]["parameters"].(map[string]any)["properties"].(map[string]any)["input"].(map[string]any)["type"])

	upstream, err := normalizeNativeResponsesInputForUpstream(withTools)
	require.NoError(t, err)
	items, err := normalizeResponsesInput(upstream.Input)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "message", items[0]["type"])
}

func TestNativeResponsesCodexNamespaceToolsRoundTrip(t *testing.T) {
	for _, placement := range []string{"additional_tools", "top_level"} {
		t.Run(placement, func(t *testing.T) {
			declared := []map[string]any{{"type": "namespace", "name": "functions", "tools": []any{
				map[string]any{"type": "custom", "name": "exec", "description": "Run code"},
				map[string]any{"type": "function", "name": "wait", "parameters": map[string]any{"type": "object"}},
			}}}
			items := []map[string]any{
				{"type": "custom_tool_call", "call_id": "call_exec", "name": "exec", "input": "text(1)"},
				{"type": "custom_tool_call_output", "call_id": "call_exec", "output": "1"},
				{"type": "function_call", "call_id": "call_wait", "name": "wait", "namespace": "functions", "arguments": "{}"},
				{"type": "function_call_output", "call_id": "call_wait", "output": "done"},
			}
			request := dto.OpenAIResponsesRequest{Model: "deepseek-flash"}
			var err error
			if placement == "additional_tools" {
				items = append([]map[string]any{{"type": "additional_tools", "tools": declared}}, items...)
			} else {
				request.Tools, err = common.Marshal(declared)
				require.NoError(t, err)
			}
			request.Input, err = common.Marshal(items)
			require.NoError(t, err)
			converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, testRelayInfo("deepseek-flash"), request)
			require.NoError(t, err)
			got := converted.(dto.OpenAIResponsesRequest)
			var tools []map[string]any
			require.NoError(t, common.Unmarshal(got.Tools, &tools))
			require.Len(t, tools, 2)
			require.Equal(t, "functions_exec", tools[0]["name"])
			require.Equal(t, "functions_wait", tools[1]["name"])
			upstream, err := normalizeResponsesInput(got.Input)
			require.NoError(t, err)
			require.Len(t, upstream, 4)
			require.Equal(t, "functions_exec", upstream[0]["name"])
			require.Equal(t, "functions_wait", upstream[2]["name"])
			require.NotContains(t, upstream[2], "namespace")
			require.Equal(t, "call_exec", upstream[0]["call_id"])
			require.JSONEq(t, `{"input":"text(1)"}`, upstream[0]["arguments"].(string))
			toolMap := buildNativeResponsesToolMap(declared)
			outputs, changed := convertNativeResponsesOutput([]dto.ResponsesOutput{
				{Type: "custom_tool_call", Name: "functions_exec", CallId: "call_exec", Input: rawString("text(1)")},
				{Type: "function_call", Name: "functions_wait", CallId: "call_wait", Arguments: rawString("{}")},
			}, toolMap)
			require.True(t, changed)
			require.Equal(t, "exec", outputs[0].Name)
			require.Empty(t, outputs[0].Namespace)
			require.Equal(t, "wait", outputs[1].Name)
			require.Equal(t, "functions", outputs[1].Namespace)
			require.Equal(t, "call_exec", outputs[0].CallId)
		})
	}
}

func TestNativeResponsesStreamRestoresNamespaceToolNames(t *testing.T) {
	useNativeReasoningCache(t)
	tools := []map[string]any{{"type": "namespace", "name": "functions", "tools": []any{
		map[string]any{"type": "custom", "name": "exec"},
		map[string]any{"type": "function", "name": "wait"},
	}}}
	info := nativeReasoningTestInfo()
	body := nativeReasoningTestStream(t, info, []map[string]any{
		{"type": "response.output_item.done", "item": nativeReasoningTestOutput("rs_1", "original text")},
		{"type": "response.output_item.added", "item": dto.ResponsesOutput{Type: "custom_tool_call", Name: "functions_exec", CallId: "call_exec", ID: "ctc_1"}},
		{"type": "response.output_item.done", "item": dto.ResponsesOutput{Type: "custom_tool_call", Name: "functions_exec", CallId: "call_exec", ID: "ctc_1", Input: rawString("text(1)")}},
		{"type": "response.output_item.done", "item": dto.ResponsesOutput{Type: "function_call", Name: "functions_wait", CallId: "call_wait", ID: "fc_1", Arguments: rawString("{}")}},
	}, tools)
	require.Contains(t, body, `"name":"exec"`)
	require.Contains(t, body, `"name":"wait"`)
	require.Contains(t, body, `"namespace":"functions"`)
	require.NotContains(t, body, `"name":"functions_exec"`)
	got, err := restoreNativeResponsesReasoning(info, nativeReasoningTestRequest(t, map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call_exec"}))
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Equal(t, "original text", nativeReasoningTestText(t, items[0]))
}
