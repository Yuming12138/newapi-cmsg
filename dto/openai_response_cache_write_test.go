package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestInputTokenDetailsCacheCreationTokensTotal(t *testing.T) {
	tests := []struct {
		name    string
		details InputTokenDetails
		want    int
	}{
		{name: "empty", want: 0},
		{name: "legacy", details: InputTokenDetails{CachedCreationTokens: 120}, want: 120},
		{name: "native", details: InputTokenDetails{CacheWriteTokens: 140}, want: 140},
		{name: "aliases use maximum", details: InputTokenDetails{CachedCreationTokens: 120, CacheWriteTokens: 140}, want: 140},
		{name: "legacy maximum", details: InputTokenDetails{CachedCreationTokens: 160, CacheWriteTokens: 140}, want: 160},
		{name: "negative values clamp", details: InputTokenDetails{CachedCreationTokens: -10, CacheWriteTokens: -5}, want: 0},
		{name: "positive beats negative", details: InputTokenDetails{CachedCreationTokens: 25, CacheWriteTokens: -5}, want: 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.details.CacheCreationTokensTotal())
		})
	}
}

func TestUsageUnmarshalsNativeCacheWriteTokens(t *testing.T) {
	raw := []byte(`{
		"prompt_tokens":3619,
		"completion_tokens":36,
		"prompt_tokens_details":{"cached_tokens":2921,"cache_write_tokens":3616},
		"input_tokens":3619,
		"input_tokens_details":{"cached_tokens":2921,"cache_write_tokens":3616}
	}`)

	var usage Usage
	require.NoError(t, common.Unmarshal(raw, &usage))
	require.Equal(t, 3616, usage.PromptTokensDetails.CacheWriteTokens)
	require.NotNil(t, usage.InputTokensDetails)
	require.Equal(t, 3616, usage.InputTokensDetails.CacheWriteTokens)
}

func TestUsageHasNativeOpenAICacheWriteTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage *Usage
		want  bool
	}{
		{name: "nil usage", usage: nil, want: false},
		{name: "native openai", usage: &Usage{PromptTokensDetails: InputTokenDetails{CacheWriteTokens: 42}}, want: true},
		{name: "legacy only", usage: &Usage{PromptTokensDetails: InputTokenDetails{CachedCreationTokens: 42}}, want: false},
		{name: "anthropic semantic", usage: &Usage{UsageSemantic: "anthropic", PromptTokensDetails: InputTokenDetails{CacheWriteTokens: 42}}, want: false},
		{name: "anthropic source", usage: &Usage{UsageSemantic: "openai", UsageSource: "anthropic", PromptTokensDetails: InputTokenDetails{CacheWriteTokens: 42}}, want: false},
		{name: "non-positive native field", usage: &Usage{PromptTokensDetails: InputTokenDetails{CacheWriteTokens: -1}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.usage.HasNativeOpenAICacheWriteTokens())
		})
	}
}

func TestUsageInternalCacheExclusionIsNotSerialized(t *testing.T) {
	wire, err := common.Marshal(Usage{CacheReadWriteExclusionTokens: 123})
	require.NoError(t, err)
	require.NotContains(t, string(wire), "CacheReadWriteExclusionTokens")
	require.NotContains(t, string(wire), "cache_read_write_exclusion_tokens")
}
