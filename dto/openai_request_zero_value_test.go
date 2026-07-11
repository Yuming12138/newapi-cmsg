package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeneralOpenAIRequestPreserveExplicitZeroValues(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-4.1",
		"stream":false,
		"max_tokens":0,
		"max_completion_tokens":0,
		"top_p":0,
		"top_k":0,
		"n":0,
		"frequency_penalty":0,
		"presence_penalty":0,
		"seed":0,
		"logprobs":false,
		"top_logprobs":0,
		"dimensions":0,
		"return_images":false,
		"return_related_questions":false
	}`)

	var req GeneralOpenAIRequest
	err := common.Unmarshal(raw, &req)
	require.NoError(t, err)

	encoded, err := common.Marshal(req)
	require.NoError(t, err)

	require.True(t, gjson.GetBytes(encoded, "stream").Exists())
	require.True(t, gjson.GetBytes(encoded, "max_tokens").Exists())
	require.True(t, gjson.GetBytes(encoded, "max_completion_tokens").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_p").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_k").Exists())
	require.True(t, gjson.GetBytes(encoded, "n").Exists())
	require.True(t, gjson.GetBytes(encoded, "frequency_penalty").Exists())
	require.True(t, gjson.GetBytes(encoded, "presence_penalty").Exists())
	require.True(t, gjson.GetBytes(encoded, "seed").Exists())
	require.True(t, gjson.GetBytes(encoded, "logprobs").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_logprobs").Exists())
	require.True(t, gjson.GetBytes(encoded, "dimensions").Exists())
	require.True(t, gjson.GetBytes(encoded, "return_images").Exists())
	require.True(t, gjson.GetBytes(encoded, "return_related_questions").Exists())
}

func TestOpenAIResponsesRequestPreserveExplicitZeroValues(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-4.1",
		"max_output_tokens":0,
		"max_tool_calls":0,
		"parallel_tool_calls":false,
		"service_tier":"",
		"stream":false,
		"top_p":0
	}`)

	var req OpenAIResponsesRequest
	err := common.Unmarshal(raw, &req)
	require.NoError(t, err)

	encoded, err := common.Marshal(req)
	require.NoError(t, err)

	require.True(t, gjson.GetBytes(encoded, "max_output_tokens").Exists())
	require.True(t, gjson.GetBytes(encoded, "max_tool_calls").Exists())
	require.True(t, gjson.GetBytes(encoded, "parallel_tool_calls").Exists())
	require.False(t, gjson.GetBytes(encoded, "parallel_tool_calls").Bool())
	require.True(t, gjson.GetBytes(encoded, "service_tier").Exists())
	require.Equal(t, "", gjson.GetBytes(encoded, "service_tier").String())
	require.True(t, gjson.GetBytes(encoded, "stream").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_p").Exists())
}

func TestOpenAIResponsesRequestPreservesCodexMetadataAndReasoningFields(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.6-sol",
		"client_metadata":{"session":"session-1"},
		"reasoning":{
			"effort":"high",
			"mode":"adaptive",
			"context":{"turn":"turn-1"}
		}
	}`)

	var req OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal(raw, &req))
	require.NotNil(t, req.Reasoning)

	encoded, err := common.Marshal(req)
	require.NoError(t, err)
	require.Equal(t, "session-1", gjson.GetBytes(encoded, "client_metadata.session").String())
	require.Equal(t, "adaptive", gjson.GetBytes(encoded, "reasoning.mode").String())
	require.Equal(t, "turn-1", gjson.GetBytes(encoded, "reasoning.context.turn").String())
}

func TestGeneralOpenAIRequestGetSystemRoleName(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "o1 uses developer", model: "o1", want: "developer"},
		{name: "o3 family uses developer", model: "o3-mini-high", want: "developer"},
		{name: "o4 family uses developer", model: "o4-mini", want: "developer"},
		{name: "o1 mini stays system", model: "o1-mini", want: "system"},
		{name: "o1 preview stays system", model: "o1-preview", want: "system"},
		{name: "gpt 5 uses developer", model: "gpt-5", want: "developer"},
		{name: "omni is not o series", model: "omni-moderation-latest", want: "system"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := GeneralOpenAIRequest{Model: tt.model}

			require.Equal(t, tt.want, req.GetSystemRoleName())
		})
	}
}
