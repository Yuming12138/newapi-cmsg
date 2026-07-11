package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesRequestFromRequestMapsAllCompactionFields(t *testing.T) {
	parallelToolCalls := false
	serviceTier := "priority"
	compact := &dto.OpenAIResponsesCompactionRequest{
		Model:              "gpt-5.6-terra-openai-compact",
		Input:              []byte(`[{"role":"user","content":"hello"}]`),
		Instructions:       []byte(`"compact carefully"`),
		PreviousResponseID: "resp-1",
		Tools:              []byte(`[{"type":"function","name":"lookup"}]`),
		ParallelToolCalls:  &parallelToolCalls,
		Reasoning: &dto.Reasoning{
			Effort:  "high",
			Mode:    []byte(`"adaptive"`),
			Context: []byte(`{"turn":"turn-1"}`),
		},
		ServiceTier:    &serviceTier,
		PromptCacheKey: []byte(`"cache-1"`),
		Text:           []byte(`{"format":{"type":"text"}}`),
	}

	mapped, err := responsesRequestFromRequest(compact)
	require.NoError(t, err)
	mapped, err = common.DeepCopy(mapped)
	require.NoError(t, err)
	encoded, err := common.Marshal(mapped)
	require.NoError(t, err)

	for _, path := range []string{"tools", "parallel_tool_calls", "reasoning", "service_tier", "prompt_cache_key", "text"} {
		require.True(t, gjson.GetBytes(encoded, path).Exists(), "mapped field %q", path)
	}
	require.False(t, gjson.GetBytes(encoded, "parallel_tool_calls").Bool())
	require.Equal(t, "priority", gjson.GetBytes(encoded, "service_tier").String())
	require.Equal(t, "adaptive", gjson.GetBytes(encoded, "reasoning.mode").String())
}
