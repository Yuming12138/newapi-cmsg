package mimo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func testRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "mimo-v2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://token-plan-cn.xiaomimimo.com/v1",
			UpstreamModelName: "mimo-v2.5-pro",
		},
	}
}

func TestGetRequestURL(t *testing.T) {
	adaptor := &Adaptor{}

	url, err := adaptor.GetRequestURL(testRelayInfo())
	require.NoError(t, err)
	require.Equal(t, "https://token-plan-cn.xiaomimimo.com/v1/chat/completions", url)

	info := testRelayInfo()
	info.ChannelBaseUrl = "https://api.xiaomimimo.com"
	url, err = adaptor.GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.xiaomimimo.com/v1/chat/completions", url)
}

func TestSetupRequestHeaderForResponsesForcesJSONAccept(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Accept", "text/event-stream")

	info := testRelayInfo()
	info.ApiKey = "test-key"
	info.IsStream = true
	info.RelayMode = relayconstant.RelayModeResponses
	header := http.Header{}

	err := (&Adaptor{}).SetupRequestHeader(ctx, &header, info)

	require.NoError(t, err)
	require.Equal(t, "Bearer test-key", header.Get("Authorization"))
	require.Equal(t, "application/json", header.Get("Accept"))
	require.Equal(t, "application/json", header.Get("Content-Type"))
}

func TestFilterChatPayloadMapsThinkingAndDropsUnsupportedFields(t *testing.T) {
	payload := map[string]any{
		"model":            "mimo-v2.5-pro",
		"messages":         []any{},
		"stream_options":   map[string]any{"include_usage": true},
		"reasoning_effort": "minimal",
		"tool_choice":      "required",
		"max_tokens":       100,
	}

	filtered := filterChatPayload(payload)
	require.NotContains(t, filtered, "stream_options")
	require.NotContains(t, filtered, "reasoning_effort")
	require.NotContains(t, filtered, "tool_choice")
	require.Equal(t, map[string]string{"type": "disabled"}, filtered["thinking"])
}

func TestConvertResponsesRequestBuildsChatPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := testRelayInfo()
	input := json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]`)
	tools := json.RawMessage(`[{"type":"custom","name":"local_shell","description":"run command"}]`)
	request := dto.OpenAIResponsesRequest{
		Model:     "mimo-v2.5-pro",
		Input:     input,
		Tools:     tools,
		Reasoning: &dto.Reasoning{Effort: "high"},
	}

	converted, err := convertResponsesRequest(ctx, info, request)
	require.NoError(t, err)
	payload := converted.(map[string]any)
	require.Equal(t, "mimo-v2.5-pro", payload["model"])
	require.Equal(t, false, payload["stream"])
	require.Equal(t, map[string]string{"type": "enabled"}, payload["thinking"])

	messages := payload["messages"].([]dto.Message)
	require.Len(t, messages, 1)
	require.Equal(t, "user", messages[0].Role)
	require.Equal(t, "hello", messages[0].Content)

	chatTools := payload["tools"].([]map[string]any)
	require.Len(t, chatTools, 1)
	require.Equal(t, "local_shell", chatTools[0]["function"].(map[string]any)["name"])
}

func TestConvertResponsesRequestSkipsWebSearchTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := testRelayInfo()
	input := json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]`)
	tools := json.RawMessage(`[{"type":"web_search"},{"type":"function","name":"local_shell","parameters":{"type":"object"}}]`)
	request := dto.OpenAIResponsesRequest{
		Model: "mimo-v2.5-pro",
		Input: input,
		Tools: tools,
	}

	converted, err := convertResponsesRequest(ctx, info, request)
	require.NoError(t, err)
	payload := converted.(map[string]any)

	chatTools := payload["tools"].([]map[string]any)
	require.Len(t, chatTools, 1)
	require.Equal(t, "local_shell", chatTools[0]["function"].(map[string]any)["name"])
}

func TestChatToolCallToResponsesOutputRestoresCustomTool(t *testing.T) {
	toolCall := chatToolCall{ID: "call_1"}
	toolCall.Function.Name = "local_shell"
	toolCall.Function.Arguments = `{"input":"ls -la"}`

	item, output := chatToolCallToResponsesOutput(toolCall, map[string]toolRestore{
		"local_shell": {Type: "custom", Name: "local_shell"},
	})

	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "ls -la", item["input"])
	require.Equal(t, "custom_tool_call", output.Type)
	require.Equal(t, "ls -la", common.JsonRawMessageToString(output.Input))
}

func TestValidateResponsesInputRequiresAllPendingToolOutputs(t *testing.T) {
	pending := map[string]pendingToolCall{
		"call_1": {CallID: "call_1"},
		"call_2": {CallID: "call_2"},
	}
	input := []map[string]any{{"type": "function_call_output", "call_id": "call_1", "output": "ok"}}

	_, err := validateResponsesInput(pending, input)
	require.ErrorContains(t, err, "call_2")
}
