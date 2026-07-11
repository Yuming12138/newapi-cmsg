package codex

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestModelListIncludesGPT56AndCompactAliases(t *testing.T) {
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		require.Contains(t, ModelList, model)
		require.Contains(t, ModelList, ratio_setting.WithCompactModelSuffix(model))
	}

	seen := make(map[string]struct{}, len(ModelList))
	for _, model := range ModelList {
		_, duplicated := seen[model]
		require.False(t, duplicated, "duplicate Codex model %q", model)
		seen[model] = struct{}{}
	}
}
