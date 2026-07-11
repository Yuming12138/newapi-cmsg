package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesCompactionRequestMapsAllCodexFields(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.6-sol-openai-compact",
		"input":[{"role":"user","content":"hello"}],
		"instructions":"compact carefully",
		"previous_response_id":"resp-1",
		"tools":[{"type":"function","name":"lookup"}],
		"parallel_tool_calls":false,
		"reasoning":{
			"effort":"high",
			"mode":"adaptive",
			"context":{"turn":"turn-1"}
		},
		"service_tier":"",
		"prompt_cache_key":"",
		"text":{"format":{"type":"text"}}
	}`)

	var compact OpenAIResponsesCompactionRequest
	require.NoError(t, common.Unmarshal(raw, &compact))
	require.NotNil(t, compact.ParallelToolCalls)
	require.False(t, *compact.ParallelToolCalls)
	require.NotNil(t, compact.ServiceTier)
	require.Equal(t, "", *compact.ServiceTier)

	compactJSON, err := common.Marshal(compact)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(compactJSON, "parallel_tool_calls").Exists())
	require.False(t, gjson.GetBytes(compactJSON, "parallel_tool_calls").Bool())
	require.True(t, gjson.GetBytes(compactJSON, "service_tier").Exists())

	responses := compact.ToResponsesRequest()
	require.NotNil(t, responses)
	responsesJSON, err := common.Marshal(responses)
	require.NoError(t, err)

	for _, path := range []string{
		"tools",
		"parallel_tool_calls",
		"reasoning",
		"service_tier",
		"prompt_cache_key",
		"text",
	} {
		require.True(t, gjson.GetBytes(responsesJSON, path).Exists(), "mapped field %q", path)
	}
	require.False(t, gjson.GetBytes(responsesJSON, "parallel_tool_calls").Bool())
	require.Equal(t, "", gjson.GetBytes(responsesJSON, "service_tier").String())
	require.Equal(t, "adaptive", gjson.GetBytes(responsesJSON, "reasoning.mode").String())
	require.Equal(t, "turn-1", gjson.GetBytes(responsesJSON, "reasoning.context.turn").String())
	require.Equal(t, "lookup", gjson.GetBytes(responsesJSON, "tools.0.name").String())
	require.Equal(t, "text", gjson.GetBytes(responsesJSON, "text.format.type").String())
}

func TestOpenAIResponsesCompactionRequestOmitsAbsentOptionalScalars(t *testing.T) {
	compact := OpenAIResponsesCompactionRequest{Model: "gpt-5.6-luna-openai-compact"}
	encoded, err := common.Marshal(compact)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(encoded, "parallel_tool_calls").Exists())
	require.False(t, gjson.GetBytes(encoded, "service_tier").Exists())
	require.Nil(t, (*OpenAIResponsesCompactionRequest)(nil).ToResponsesRequest())
}
