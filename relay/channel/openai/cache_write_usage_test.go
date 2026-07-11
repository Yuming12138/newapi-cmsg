package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const cacheWriteUsageJSON = `"usage":{"input_tokens":3619,"output_tokens":36,"total_tokens":3655,"input_tokens_details":{"cached_tokens":2921,"cached_creation_tokens":120,"cache_write_tokens":3616}}`

func newCacheWriteResponseContext(t *testing.T, body, contentType string) (*gin.Context, *httptest.ResponseRecorder, *http.Response) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{contentType}},
	}
	return c, recorder, resp
}

func requireNativeCacheWriteUsage(t *testing.T, prompt, completion, cachedRead, cachedWrite int, usagePrompt, usageCompletion, usageRead, usageWrite int) {
	t.Helper()
	require.Equal(t, prompt, usagePrompt)
	require.Equal(t, completion, usageCompletion)
	require.Equal(t, cachedRead, usageRead)
	require.Equal(t, cachedWrite, usageWrite)
}

func TestOaiResponsesHandlerPropagatesCacheWriteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"id":"resp_1","object":"response","status":"completed",` + cacheWriteUsageJSON + `}`
	c, _, resp := newCacheWriteResponseContext(t, body, "application/json")

	usage, apiErr := OaiResponsesHandler(c, nil, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	requireNativeCacheWriteUsage(t, 3619, 36, 2921, 3616,
		usage.PromptTokens, usage.CompletionTokens,
		usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 120, usage.PromptTokensDetails.CachedCreationTokens)
}

func TestOaiResponsesStreamHandlerPropagatesCacheWriteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := "data: {\"type\":\"response.completed\",\"response\":{" + cacheWriteUsageJSON + "}}\n\ndata: [DONE]\n\n"
	c, _, resp := newCacheWriteResponseContext(t, body, "text/event-stream")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"}}

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	requireNativeCacheWriteUsage(t, 3619, 36, 2921, 3616,
		usage.PromptTokens, usage.CompletionTokens,
		usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 120, usage.PromptTokensDetails.CachedCreationTokens)
}

func TestOaiResponsesCompactionHandlerPropagatesCacheWriteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"id":"resp_compact_1","object":"response.compaction",` + cacheWriteUsageJSON + `}`
	c, _, resp := newCacheWriteResponseContext(t, body, "application/json")

	usage, apiErr := OaiResponsesCompactionHandler(c, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	requireNativeCacheWriteUsage(t, 3619, 36, 2921, 3616,
		usage.PromptTokens, usage.CompletionTokens,
		usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 120, usage.PromptTokensDetails.CachedCreationTokens)
}

func TestOaiChatToResponsesHandlerKeepsCacheWriteForSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-5.6-sol","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3619,"completion_tokens":36,"total_tokens":3655,"prompt_tokens_details":{"cached_tokens":2921,"cached_creation_tokens":120,"cache_write_tokens":3616}}}`
	c, recorder, resp := newCacheWriteResponseContext(t, body, "application/json")

	usage, apiErr := OaiChatToResponsesHandler(c, nil, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	requireNativeCacheWriteUsage(t, 3619, 36, 2921, 3616,
		usage.PromptTokens, usage.CompletionTokens,
		usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CacheWriteTokens)
	require.True(t, usage.HasNativeOpenAICacheWriteTokens())
	require.Contains(t, recorder.Body.String(), `"cache_write_tokens":3616`)
}

func TestOaiChatToResponsesStreamHandlerKeepsCacheWriteForSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-5.6-sol\",\"choices\":[],\"usage\":{\"prompt_tokens\":3619,\"completion_tokens\":36,\"total_tokens\":3655,\"prompt_tokens_details\":{\"cached_tokens\":2921,\"cached_creation_tokens\":120,\"cache_write_tokens\":3616}}}\n\ndata: [DONE]\n\n"
	c, recorder, resp := newCacheWriteResponseContext(t, body, "text/event-stream")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"}}

	usage, apiErr := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	requireNativeCacheWriteUsage(t, 3619, 36, 2921, 3616,
		usage.PromptTokens, usage.CompletionTokens,
		usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CacheWriteTokens)
	require.True(t, usage.HasNativeOpenAICacheWriteTokens())
	require.Contains(t, recorder.Body.String(), `"cache_write_tokens":3616`)
}
