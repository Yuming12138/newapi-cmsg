package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestBuildClaudeUsageFromNativeOpenAICacheWrite(t *testing.T) {
	usage := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     3619,
		CompletionTokens: 36,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         2921,
			CachedCreationTokens: 120,
			CacheWriteTokens:     3616,
		},
	})

	require.NotNil(t, usage)
	require.Equal(t, 3, usage.InputTokens)
	require.Equal(t, 36, usage.OutputTokens)
	require.Equal(t, 2921, usage.CacheReadInputTokens)
	require.Equal(t, 3616, usage.CacheCreationInputTokens)
}

func TestBuildClaudeUsageKeepsLegacyCreationSemantics(t *testing.T) {
	usage := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     62,
		CompletionTokens: 10,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 50,
	})

	require.NotNil(t, usage)
	// Legacy/Claude-derived usage already reports text input separately.
	require.Equal(t, 62, usage.InputTokens)
	require.Equal(t, 30, usage.CacheReadInputTokens)
	require.Equal(t, 50, usage.CacheCreationInputTokens)
	require.NotNil(t, usage.CacheCreation)
	require.Equal(t, 50, usage.CacheCreation.Ephemeral5mInputTokens)
}

func TestBuildClaudeUsageDoesNotTreatAnthropicSourceAliasAsNativePrefix(t *testing.T) {
	usage := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     180,
		CompletionTokens: 20,
		UsageSemantic:    "openai",
		UsageSource:      "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
			CacheWriteTokens:     50,
		},
	})

	require.NotNil(t, usage)
	require.Equal(t, 180, usage.InputTokens)
	require.Equal(t, 30, usage.CacheReadInputTokens)
	require.Equal(t, 50, usage.CacheCreationInputTokens)
}

func TestBuildClaudeUsageUsesExactAggregateCacheExclusion(t *testing.T) {
	usage := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:                  2000,
		CompletionTokens:              20,
		CacheReadWriteExclusionTokens: 450,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         300,
			CachedCreationTokens: 350,
			CacheWriteTokens:     300,
		},
	})

	require.NotNil(t, usage)
	require.Equal(t, 1550, usage.InputTokens)
	require.Equal(t, 20, usage.OutputTokens)
	require.Equal(t, 300, usage.CacheReadInputTokens)
	require.Equal(t, 350, usage.CacheCreationInputTokens)
}

func TestBuildClaudeUsageDoesNotApplyExactExclusionToAnthropicUsage(t *testing.T) {
	tests := []struct {
		name     string
		semantic string
		source   string
	}{
		{name: "anthropic semantic", semantic: "anthropic"},
		{name: "anthropic source", semantic: "openai", source: "anthropic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
				PromptTokens:                  100,
				UsageSemantic:                 tt.semantic,
				UsageSource:                   tt.source,
				CacheReadWriteExclusionTokens: 80,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens:         30,
					CachedCreationTokens: 50,
					CacheWriteTokens:     50,
				},
			})

			require.NotNil(t, usage)
			require.Equal(t, 100, usage.InputTokens)
		})
	}
}
