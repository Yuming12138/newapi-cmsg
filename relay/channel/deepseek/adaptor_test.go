package deepseek

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func testRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "deepseek-v4-pro-max",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.deepseek.com",
			UpstreamModelName: "deepseek-v4-pro-max",
		},
	}
}

func TestConvertResponsesRequestBuildsDeepSeekChatPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := testRelayInfo()
	input := json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]`)
	request := dto.OpenAIResponsesRequest{
		Model:        "deepseek-v4-pro-max",
		Input:        input,
		Instructions: json.RawMessage(`"be concise"`),
	}

	converted, err := convertResponsesRequest(ctx, info, request)
	require.NoError(t, err)

	payload := converted.(map[string]any)
	require.Equal(t, "deepseek-v4-pro", payload["model"])
	require.Equal(t, false, payload["stream"])
	require.Equal(t, map[string]any{"type": "enabled"}, payload["thinking"])

	messages := payload["messages"].([]dto.Message)
	require.Len(t, messages, 2)
	require.Equal(t, "system", messages[0].Role)
	require.Equal(t, "be concise", messages[0].Content)
	require.Equal(t, "user", messages[1].Role)
	require.Equal(t, "hello", messages[1].Content)
}

func TestConvertResponsesRequestNormalizesDeveloperRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := testRelayInfo()
	input := json.RawMessage(`[
		{"type":"message","role":"developer","content":[{"type":"input_text","text":"follow codex policy"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
	]`)
	request := dto.OpenAIResponsesRequest{
		Model: "deepseek-v4-pro-max",
		Input: input,
	}

	converted, err := convertResponsesRequest(ctx, info, request)
	require.NoError(t, err)

	payload := converted.(map[string]any)
	messages := payload["messages"].([]dto.Message)
	require.Len(t, messages, 2)
	require.Equal(t, "system", messages[0].Role)
	require.Equal(t, "follow codex policy", messages[0].Content)
	require.Equal(t, "user", messages[1].Role)
	require.Equal(t, "hello", messages[1].Content)
}
