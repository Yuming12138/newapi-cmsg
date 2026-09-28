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
