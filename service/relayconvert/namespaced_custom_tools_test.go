package relayconvert

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex Desktop groups its tools into namespaces and declares most of them as
// freeform "custom" tools. Chat Completions only knows functions, so the
// namespace has to be flattened and the custom tool has to be given an argument
// schema carrying its freeform input.
func TestNamespacedCustomToolsFlattenToChatFunctions(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Tools: mustRawMessage(t, []any{
			map[string]any{
				"type": "namespace",
				"name": "functions",
				"tools": []any{
					map[string]any{
						"type":        "custom",
						"name":        "exec",
						"description": "run a command",
					},
					map[string]any{
						"type":       "function",
						"name":       "read_file",
						"parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
					},
				},
			},
		}),
	}

	chatReq, err := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chatReq.Tools, 2)

	assert.Equal(t, "function", chatReq.Tools[0].Type)
	assert.Equal(t, "exec", chatReq.Tools[0].Function.Name)
	parameters, _ := common.Marshal(chatReq.Tools[0].Function.Parameters)
	assert.Contains(t, string(parameters), `"input"`)

	assert.Equal(t, "function", chatReq.Tools[1].Type)
	assert.Equal(t, "read_file", chatReq.Tools[1].Function.Name)

	names := ResponsesRequestCustomToolNames(req)
	assert.Contains(t, names, "exec")
	assert.NotContains(t, names, "read_file")
}

// Codex Desktop ships its whole tool set in an "additional_tools" input item and
// leaves the top-level "tools" array empty, so a converter that only reads "tools"
// would relay a request with no tools at all.
func TestAdditionalToolsInputItemBecomesChatTools(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []any{
			map[string]any{
				"type":        "additional_tools",
				"id":          "at_1",
				"role":        "developer",
				"description": "",
				"tools": []any{
					map[string]any{
						"type": "namespace",
						"name": "functions",
						"tools": []any{
							map[string]any{"type": "custom", "name": "exec", "description": "run code"},
						},
					},
				},
			},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		}),
	}

	chatReq, err := ResponsesRequestToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chatReq.Tools, 1)
	assert.Equal(t, "exec", chatReq.Tools[0].Function.Name)
	// The declaration itself must not turn into a message.
	require.Len(t, chatReq.Messages, 1)
	assert.Contains(t, ResponsesRequestCustomToolNames(req), "exec")
}

func TestCustomToolCallRestoredAsCustomToolCall(t *testing.T) {
	chatResp := &dto.OpenAITextResponse{
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Message: dto.Message{
					Role:      "assistant",
					ToolCalls: mustRawMessage(t, []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "exec", "arguments": `{"input":"echo hi"}`}}}),
				},
				FinishReason: "tool_calls",
			},
		},
	}

	resp, _, err := ChatCompletionsResponseToResponsesResponseWithCustom(chatResp, "resp_1", map[string]struct{}{"exec": {}})
	require.NoError(t, err)
	require.Len(t, resp.Output, 1)
	assert.Equal(t, "custom_tool_call", resp.Output[0].Type)
	assert.Equal(t, "exec", resp.Output[0].Name)
	assert.Equal(t, "call_1", resp.Output[0].CallId)
	assert.Equal(t, `"echo hi"`, string(resp.Output[0].Input))

	// Without the declared custom tool the call stays a plain function call.
	plain, _, err := ChatCompletionsResponseToResponsesResponse(chatResp, "resp_1")
	require.NoError(t, err)
	require.Len(t, plain.Output, 1)
	assert.Equal(t, "function_call", plain.Output[0].Type)
}

func TestStreamCustomToolCallInputDeltas(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_1", "gpt-test")
	state.SetCustomToolNames(map[string]struct{}{"exec": {}})

	index := 0
	events, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{Index: &index, ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "exec"}},
					},
				},
			},
		},
	}, state)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	assert.Equal(t, "custom_tool_call", events[len(events)-1].Payload.Item.Type)

	// Partial JSON must not leak into the input stream.
	for _, fragment := range []string{`{"in`, `put":"ec`} {
		partial, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{
			Choices: []dto.ChatCompletionsStreamResponseChoice{
				{
					Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
						ToolCalls: []dto.ToolCallResponse{
							{Index: &index, Function: dto.FunctionResponse{Arguments: fragment}},
						},
					},
				},
			},
		}, state)
		require.NoError(t, err)
		for _, event := range partial {
			assert.NotEqual(t, "response.custom_tool_call_input.delta", event.Type)
		}
	}

	// The closing fragment completes the object, so the input is flushed at once.
	completedChunk, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{Index: &index, Function: dto.FunctionResponse{Arguments: `ho hi"}`}},
					},
				},
			},
		},
	}, state)
	require.NoError(t, err)
	var delta string
	for _, event := range completedChunk {
		if event.Type == "response.custom_tool_call_input.delta" {
			delta += event.Payload.Delta
		}
	}
	assert.Equal(t, "echo hi", delta)

	final := FinalizeChatCompletionsStreamToResponses(state)
	require.NotEmpty(t, final)
	completed := final[len(final)-1]
	require.NotNil(t, completed.Payload.Response)
	require.Len(t, completed.Payload.Response.Output, 1)
	output := completed.Payload.Response.Output[0]
	assert.Equal(t, "custom_tool_call", output.Type)
	assert.Equal(t, `"echo hi"`, string(output.Input))
}
