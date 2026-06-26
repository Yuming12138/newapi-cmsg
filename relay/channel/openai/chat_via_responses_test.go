package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setResponsesStreamTestTimeout(t *testing.T) {
	t.Helper()
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
	})
}

func TestOaiResponsesToChatStreamHandlerClaudeKeepsMixedTextAndToolUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setResponsesStreamTestTimeout(t)

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_test","created_at":1778832821,"model":"gpt-5.5"}}`,
		`data: {"type":"response.output_item.added","item":{"id":"msg_1","type":"message","status":"in_progress","content":[],"role":"assistant"},"output_index":0}`,
		`data: {"type":"response.output_text.delta","delta":"I","item_id":"msg_1","output_index":0,"content_index":0}`,
		`data: {"type":"response.output_text.delta","delta":" will do it.","item_id":"msg_1","output_index":0,"content_index":0}`,
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","status":"in_progress","arguments":"","call_id":"call_1","name":"write_file"},"output_index":1}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"path\"","item_id":"fc_1","output_index":1}`,
		`data: {"type":"response.function_call_arguments.delta","delta":":\"hello.txt\",\"content\":\"CLAUDE_TOOL_OK\"}","item_id":"fc_1","output_index":1}`,
		`data: {"type":"response.function_call_arguments.done","arguments":"{\"path\":\"hello.txt\",\"content\":\"CLAUDE_TOOL_OK\"}","item_id":"fc_1","output_index":1}`,
		`data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","arguments":"{\"path\":\"hello.txt\",\"content\":\"CLAUDE_TOOL_OK\"}","call_id":"call_1","name":"write_file"},"output_index":1}`,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{
		RelayFormat:       types.RelayFormatClaude,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone},
		ChannelMeta:       &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.5"},
	}

	usage, newAPIError := OaiResponsesToChatStreamHandler(c, info, resp)

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	output := recorder.Body.String()
	require.Contains(t, output, `"type":"text"`)
	require.Contains(t, output, `"text":"I"`)
	require.Contains(t, output, `"text":" will do it."`)
	require.Contains(t, output, `"type":"tool_use"`)
	require.Contains(t, output, `"name":"write_file"`)
	require.Contains(t, output, `"type":"input_json_delta"`)
	require.Contains(t, output, `"partial_json":"{\"path\""`)
	require.Contains(t, output, `"stop_reason":"tool_use"`)
}

func TestOaiResponsesToChatStreamHandlerOpenAIKeepsLegacyTextOverToolBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setResponsesStreamTestTimeout(t)

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_test","created_at":1778832821,"model":"gpt-5.5"}}`,
		`data: {"type":"response.output_text.delta","delta":"I will do it.","item_id":"msg_1","output_index":0,"content_index":0}`,
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","status":"in_progress","arguments":"","call_id":"call_1","name":"write_file"},"output_index":1}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"path\":\"hello.txt\"}","item_id":"fc_1","output_index":1}`,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.5"},
	}

	_, newAPIError := OaiResponsesToChatStreamHandler(c, info, resp)

	require.Nil(t, newAPIError)
	output := recorder.Body.String()
	require.Contains(t, output, `I will do it.`)
	require.NotContains(t, output, `tool_calls`)
}
