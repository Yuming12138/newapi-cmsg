package deepseek

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

func TestResponsesUsagePropagatesCacheWriteToJSONAndCompletedEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buildRecorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(buildRecorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := testRelayInfo()
	state := &responsesTurnState{ResponseID: "resp_usage", Request: dto.OpenAIResponsesRequest{}}
	chatResponse := &dto.OpenAITextResponse{Usage: dto.Usage{
		PromptTokens:     3619,
		CompletionTokens: 36,
		TotalTokens:      3655,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         2921,
			CachedCreationTokens: 120,
			CacheWriteTokens:     3616,
			TextTokens:           3,
			ImageTokens:          4,
			AudioTokens:          5,
		},
	}}

	response, _, usage, err := buildResponsesResponse(ctx, info, state, chatResponse)
	require.NoError(t, err)
	require.NotNil(t, response.Usage)
	require.Same(t, usage, response.Usage)
	require.Equal(t, "deepseek_chat", usage.UsageSource)
	require.Equal(t, "openai", usage.UsageSemantic)
	require.NotNil(t, usage.InputTokensDetails)
	require.Equal(t, chatResponse.Usage.PromptTokensDetails, *usage.InputTokensDetails)

	wire, err := common.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(wire), `"cached_creation_tokens":120`)
	require.Contains(t, string(wire), `"cache_write_tokens":3616`)
	require.Contains(t, string(wire), `"text_tokens":3`)
	require.Contains(t, string(wire), `"image_tokens":4`)
	require.Contains(t, string(wire), `"audio_tokens":5`)

	streamRecorder := httptest.NewRecorder()
	streamCtx, _ := gin.CreateTestContext(streamRecorder)
	streamCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	require.NoError(t, writeResponsesStream(streamCtx, response))
	require.Contains(t, streamRecorder.Body.String(), "event: response.completed")
	require.Contains(t, streamRecorder.Body.String(), `"cache_write_tokens":3616`)
}
