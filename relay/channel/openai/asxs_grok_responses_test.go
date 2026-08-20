package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNormalizeASXSGrokResponsesNamespaceTools(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 25}}
	request := dto.OpenAIResponsesRequest{
		Tools:      []byte(`[{"type":"namespace","name":"shell","tools":[{"type":"function","name":"exec","description":"run","parameters":{"type":"object"}},{"type":"web_search"}]},{"type":"function","name":"lookup","parameters":{"type":"object"}}]`),
		Input:      []byte(`[{"type":"function_call","namespace":"shell","name":"exec","call_id":"call_1","arguments":"{}"}]`),
		ToolChoice: []byte(`{"type":"function","namespace":"shell","name":"exec"}`),
	}

	normalized, err := normalizeASXSGrokResponsesRequest(c, info, request)
	require.NoError(t, err)
	var tools []map[string]any
	require.NoError(t, common.Unmarshal(normalized.Tools, &tools))
	require.Len(t, tools, 2)
	require.Equal(t, "shell__exec", tools[0]["name"])
	require.Equal(t, "function", tools[0]["type"])
	require.Equal(t, "lookup", tools[1]["name"])
	require.JSONEq(t, `[{"type":"function_call","name":"shell__exec","call_id":"call_1","arguments":"{}"}]`, string(normalized.Input))
	require.JSONEq(t, `{"type":"function","name":"shell__exec"}`, string(normalized.ToolChoice))
}

func TestNormalizeASXSGrokResponsesLiteAdditionalTools(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelId:         25,
		ChannelBaseUrl:    "https://api.asxs.top",
		UpstreamModelName: "grok-4.6",
	}}
	request := dto.OpenAIResponsesRequest{
		Tools: []byte(`[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`),
		Input: []byte(`[{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"mcp__exa","tools":[{"type":"function","name":"search","parameters":{"type":"object"}},{"type":"custom","name":"exec"},{"type":"custom","name":"apply_patch"}]}]},{"type":"function_call","namespace":"mcp__exa","name":"search","call_id":"call_1","arguments":"{}"},{"type":"message","role":"user","content":"use Exa"}]`),
	}

	normalized, err := normalizeASXSGrokResponsesRequest(c, info, request)
	require.NoError(t, err)
	require.NotContains(t, string(normalized.Input), "additional_tools")
	require.JSONEq(t, `[{"type":"function_call","name":"mcp__exa__search","call_id":"call_1","arguments":"{}"},{"type":"message","role":"user","content":"use Exa"}]`, string(normalized.Input))

	var tools []map[string]any
	require.NoError(t, common.Unmarshal(normalized.Tools, &tools))
	require.Len(t, tools, 3)
	require.Equal(t, "lookup", tools[0]["name"])
	require.Equal(t, "mcp__exa__search", tools[1]["name"])
	require.Equal(t, "mcp__exa__exec", tools[2]["name"])
	require.Equal(t, "function", tools[2]["type"])
	require.Equal(t, map[string]any{"type": "object", "properties": map[string]any{}}, tools[2]["parameters"])

	refs := asxSGrokToolNamespaceMap(c)
	require.Equal(t, asxsGrokToolRef{Namespace: "mcp__exa", Name: "search"}, refs["mcp__exa__search"])
	require.Equal(t, asxsGrokToolRef{Namespace: "mcp__exa", Name: "exec"}, refs["mcp__exa__exec"])
}

func TestNormalizeASXSGrokResponsesFallsBackForGrok46ToolRequests(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelId:         25,
		UpstreamModelName: "grok-4.6",
	}}
	request := dto.OpenAIResponsesRequest{
		Model: "grok-4.6",
		Tools: []byte(`[{"type":"function","name":"shell__exec","description":"run","parameters":{"type":"object"}}]`),
	}

	normalized, err := normalizeASXSGrokResponsesRequest(c, info, request)
	require.NoError(t, err)
	require.Equal(t, asxSGrokToolFallbackModel, normalized.Model)
	fallback, ok := c.Get(asxSGrokToolFallbackModelKey)
	require.True(t, ok)
	require.Equal(t, asxSGrokToolFallbackModel, fallback)
}

func TestNormalizeASXSGrokResponsesKeepsGrok46ForTextOnlyRequests(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelId:         25,
		UpstreamModelName: "grok-4.6",
	}}
	request := dto.OpenAIResponsesRequest{Model: "grok-4.6", Input: []byte(`[{"role":"user","content":"hello"}]`)}

	normalized, err := normalizeASXSGrokResponsesRequest(c, info, request)
	require.NoError(t, err)
	require.Equal(t, "grok-4.6", normalized.Model)
	_, ok := c.Get(asxSGrokToolFallbackModelKey)
	require.False(t, ok)
}

func TestNormalizeASXSGrokResponsesFallsBackForToolHistoryWithoutDefinitions(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelId:         25,
		UpstreamModelName: "grok-4.6",
	}}
	request := dto.OpenAIResponsesRequest{
		Model: "grok-4.6",
		Input: []byte(`[{"type":"function_call_output","call_id":"call_1","output":"ok"}]`),
	}

	normalized, err := normalizeASXSGrokResponsesRequest(c, info, request)
	require.NoError(t, err)
	require.Equal(t, asxSGrokToolFallbackModel, normalized.Model)
}

func TestNormalizeASXSGrokResponsesToolsOnlyForTarget(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	request := dto.OpenAIResponsesRequest{
		Tools: []byte(`[{"type":"namespace","name":"shell","tools":[{"type":"function","name":"exec"}]}]`),
	}

	fallbackInfo := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.asxs.top/v1",
		UpstreamModelName: "grok-4.6",
	}}
	normalized, err := normalizeASXSGrokResponsesRequest(c, fallbackInfo, request)
	require.NoError(t, err)
	require.NotContains(t, string(normalized.Tools), `"type":"namespace"`)

	nonTarget := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.asxs.top/v1",
		UpstreamModelName: "gpt-5.6-sol",
	}}
	unchanged, err := normalizeASXSGrokResponsesRequest(c, nonTarget, request)
	require.NoError(t, err)
	require.JSONEq(t, string(request.Tools), string(unchanged.Tools))
}

func TestNormalizeASXSGrokResponsesRejectsFlattenedNameCollision(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 25}}
	request := dto.OpenAIResponsesRequest{
		Tools: []byte(`[{"type":"function","name":"mcp__exa__search"},{"type":"namespace","name":"mcp__exa","tools":[{"type":"function","name":"search"}]}]`),
	}

	_, err := normalizeASXSGrokResponsesRequest(c, info, request)
	require.ErrorContains(t, err, `duplicate flattened tool name "mcp__exa__search"`)
}

func TestNamespacedToolNamePreservesQualifiedNames(t *testing.T) {
	require.Equal(t, "mcp__exa__search", namespacedToolName("mcp__exa", "search"))
	require.Equal(t, "mcp__exa__search", namespacedToolName("mcp__exa", "mcp__exa__search"))
	require.Equal(t, "collaboration__send_message", namespacedToolName("collaboration", "collaboration__send_message"))
}

func TestRestoreASXSGrokResponsesNamespaceToolCall(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set(asxSGrokToolNamespaceMapKey, map[string]asxsGrokToolRef{
		"shell__exec": {Namespace: "shell", Name: "exec"},
	})
	body := []byte(`{"id":"resp_1","output":[{"type":"function_call","name":"shell__exec","call_id":"call_1","arguments":"{}"}]}`)
	restored := restoreASXSGrokResponsesBody(c, body)
	require.JSONEq(t, `{"id":"resp_1","output":[{"type":"function_call","name":"exec","namespace":"shell","call_id":"call_1","arguments":"{}"}]}`, string(restored))

	stream := restoreASXSGrokStreamData(c, `{"type":"response.output_item.done","item":{"type":"function_call","name":"shell__exec","call_id":"call_1","arguments":"{}"}}`)
	require.JSONEq(t, `{"type":"response.output_item.done","item":{"type":"function_call","name":"exec","namespace":"shell","call_id":"call_1","arguments":"{}"}}`, stream)
}
