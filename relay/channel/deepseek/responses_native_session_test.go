package deepseek

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestReplayNativeResponsesRequestPairsToolOutputAndDropsPreviousID(t *testing.T) {
	baseItems := []map[string]any{
		{"type": "message", "role": "user", "content": "inspect the repository"},
		{"type": "custom_tool_call", "call_id": "call_1", "name": "exec", "input": "text(1)"},
	}
	pending := map[string]pendingToolCall{
		"call_1": {CallID: "call_1", Name: "exec", Type: "custom_tool_call"},
	}
	request := dto.OpenAIResponsesRequest{
		Model:              "deepseek-v4-flash",
		PreviousResponseID: "resp_1",
		Instructions:       json.RawMessage(`"do not repeat this"`),
		Input: json.RawMessage(`[
			{"type":"custom_tool_call_output","call_id":"call_1","output":"1"}
		]`),
	}

	got, err := replayNativeResponsesRequest(request, baseItems, pending)

	require.NoError(t, err)
	require.Empty(t, got.PreviousResponseID)
	require.Empty(t, got.Instructions)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, "custom_tool_call", items[1]["type"])
	require.Equal(t, "call_1", items[1]["call_id"])
	require.Equal(t, "custom_tool_call_output", items[2]["type"])
	require.Equal(t, "call_1", items[2]["call_id"])
}

func TestReplayNativeResponsesRequestRejectsUnknownToolOutput(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		PreviousResponseID: "resp_1",
		Input:              json.RawMessage(`[{"type":"function_call_output","call_id":"call_unknown","output":"nope"}]`),
	}

	_, err := replayNativeResponsesRequest(request, nil, map[string]pendingToolCall{
		"call_expected": {CallID: "call_expected", Name: "lookup", Type: "function_call"},
	})

	require.ErrorContains(t, err, "unknown call_id: call_unknown")
}

func TestReplayNativeResponsesRequestDeduplicatesFullTranscript(t *testing.T) {
	baseItems := []map[string]any{
		{"type": "custom_tool_call", "call_id": "call_1", "name": "exec", "input": "text(1)"},
	}
	request := dto.OpenAIResponsesRequest{
		PreviousResponseID: "resp_1",
		Input: json.RawMessage(`[
			{"type":"custom_tool_call","call_id":"call_1","name":"exec","input":"text(1)"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"1"}
		]`),
	}

	got, err := replayNativeResponsesRequest(request, baseItems, map[string]pendingToolCall{
		"call_1": {CallID: "call_1", Name: "exec", Type: "custom_tool_call"},
	})

	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "custom_tool_call", items[0]["type"])
	require.Equal(t, "custom_tool_call_output", items[1]["type"])
}

func TestNormalizeNativeResponsesInputConvertsUnsupportedCustomToolPair(t *testing.T) {
	request := dto.OpenAIResponsesRequest{Input: json.RawMessage(`[
		{"type":"custom_tool_call","call_id":"call_exec","name":"exec","input":"text(true)"},
		{"type":"custom_tool_call_output","call_id":"call_exec","output":"ok"},
		{"type":"custom_tool_call","call_id":"call_patch","name":"apply_patch","input":"*** Begin Patch"},
		{"type":"custom_tool_call_output","call_id":"call_patch","output":"Done"}
	]`)}

	got, err := normalizeNativeResponsesInputForUpstream(request)

	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Equal(t, "function_call", items[0]["type"])
	require.JSONEq(t, `{"input":"text(true)"}`, items[0]["arguments"].(string))
	require.NotContains(t, items[0], "input")
	require.Equal(t, "function_call_output", items[1]["type"])
	require.Equal(t, "custom_tool_call", items[2]["type"])
	require.Equal(t, "custom_tool_call_output", items[3]["type"])
}

func TestNormalizeNativeResponsesInputLeavesUnchangedRequestShape(t *testing.T) {
	request := dto.OpenAIResponsesRequest{Input: json.RawMessage(`"inspect the repository"`)}

	got, err := normalizeNativeResponsesInputForUpstream(request)

	require.NoError(t, err)
	require.Equal(t, string(request.Input), string(got.Input))
}

func TestBuildNativeResponsesSessionStateStoresInstructionsAndPendingTool(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Input:        json.RawMessage(`"inspect the repository"`),
		Instructions: json.RawMessage(`"be concise"`),
	}
	outputs := []dto.ResponsesOutput{
		{
			Type:    "message",
			ID:      "msg_1",
			Role:    "assistant",
			Status:  "completed",
			Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "I will inspect it."}},
		},
		{
			Type:   "custom_tool_call",
			ID:     "call_1",
			CallId: "call_1",
			Name:   "exec",
			Input:  rawString("text(1)"),
		},
	}

	items, pending, err := buildNativeResponsesSessionState(request, outputs)

	require.NoError(t, err)
	require.Len(t, items, 4)
	require.Equal(t, "system", items[0]["role"])
	require.Equal(t, "user", items[1]["role"])
	require.Equal(t, "message", items[2]["type"])
	require.Equal(t, "custom_tool_call", items[3]["type"])
	require.Equal(t, "call_1", items[3]["call_id"])
	require.Equal(t, pendingToolCall{CallID: "call_1", Name: "exec", Type: "custom_tool_call"}, pending["call_1"])
}
