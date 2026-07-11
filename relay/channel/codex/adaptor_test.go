package codex

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestConvertCompactResponsesRequestPreservesCodexFields(t *testing.T) {
	parallelToolCalls := false
	serviceTier := "priority"
	request := dto.OpenAIResponsesRequest{
		Model:             "gpt-5.6-sol-openai-compact",
		Tools:             []byte(`[{"type":"function","name":"lookup"}]`),
		ParallelToolCalls: &parallelToolCalls,
		Reasoning: &dto.Reasoning{
			Mode:    []byte(`"adaptive"`),
			Context: []byte(`{"turn":"turn-1"}`),
		},
		ServiceTier:    &serviceTier,
		PromptCacheKey: []byte(`"cache-1"`),
		Text:           []byte(`{"format":{"type":"text"}}`),
	}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeResponsesCompact,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	convertedAny, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	converted, ok := convertedAny.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)

	for _, path := range []string{"tools", "parallel_tool_calls", "reasoning", "service_tier", "prompt_cache_key", "text"} {
		require.True(t, gjson.GetBytes(encoded, path).Exists(), "converted field %q", path)
	}
	require.False(t, gjson.GetBytes(encoded, "parallel_tool_calls").Bool())
	require.Equal(t, "priority", gjson.GetBytes(encoded, "service_tier").String())
	require.Equal(t, "adaptive", gjson.GetBytes(encoded, "reasoning.mode").String())
	require.Equal(t, "", gjson.GetBytes(encoded, "instructions").String())
}
