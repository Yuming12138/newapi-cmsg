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
	require.Equal(t, "shell_exec", tools[0]["name"])
	require.Equal(t, "function", tools[0]["type"])
	require.Equal(t, "lookup", tools[1]["name"])
	require.JSONEq(t, `[{"type":"function_call","name":"shell_exec","call_id":"call_1","arguments":"{}"}]`, string(normalized.Input))
	require.JSONEq(t, `{"type":"function","name":"shell_exec"}`, string(normalized.ToolChoice))
}

func TestRestoreASXSGrokResponsesNamespaceToolCall(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set(asxSGrokToolNamespaceMapKey, map[string]asxsGrokToolRef{
		"shell_exec": {Namespace: "shell", Name: "exec"},
	})
	body := []byte(`{"id":"resp_1","output":[{"type":"function_call","name":"shell_exec","call_id":"call_1","arguments":"{}"}]}`)
	restored := restoreASXSGrokResponsesBody(c, body)
	require.JSONEq(t, `{"id":"resp_1","output":[{"type":"function_call","name":"exec","namespace":"shell","call_id":"call_1","arguments":"{}"}]}`, string(restored))

	stream := restoreASXSGrokStreamData(c, `{"type":"response.output_item.done","item":{"type":"function_call","name":"shell_exec","call_id":"call_1","arguments":"{}"}}`)
	require.JSONEq(t, `{"type":"response.output_item.done","item":{"type":"function_call","name":"exec","namespace":"shell","call_id":"call_1","arguments":"{}"}}`, stream)
}
