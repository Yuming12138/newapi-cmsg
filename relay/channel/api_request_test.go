package channel

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	common2 "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHTTPAndFormDebugURLLoggingSanitizesSensitiveQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldDebugEnabled := common2.DebugEnabled
	oldErrorWriter := gin.DefaultErrorWriter
	var logOutput bytes.Buffer
	common2.DebugEnabled = true
	gin.DefaultErrorWriter = &logOutput
	t.Cleanup(func() {
		common2.DebugEnabled = oldDebugEnabled
		gin.DefaultErrorWriter = oldErrorWriter
	})

	for _, requestKind := range []string{"http", "form"} {
		t.Run(requestKind, func(t *testing.T) {
			logOutput.Reset()
			logUpstreamRequestURL(ctx, "https://example.test/v1/responses?key=debug-secret&model=gpt-5.6")

			got := logOutput.String()
			require.NotContains(t, got, "debug-secret")
			require.Contains(t, got, "model=gpt-5.6")
		})
	}
}

func TestNewWSSDialErrorSanitizesExplicitAndNestedURLs(t *testing.T) {
	fullRequestURL := "wss://example.test/v1/realtime?access_token=access-secret&model=gpt-5.6"
	cause := context.DeadlineExceeded
	dialErr := &url.Error{
		Op:  "dial",
		URL: fullRequestURL,
		Err: cause,
	}

	err := newWSSDialError(fullRequestURL, dialErr)

	require.NotContains(t, err.Error(), "access-secret")
	require.Contains(t, err.Error(), "model=gpt-5.6")
	require.ErrorIs(t, err, cause)

	var extracted *url.Error
	require.ErrorAs(t, err, &extracted)
	require.Same(t, dialErr, extracted)
	require.Contains(t, extracted.URL, "access-secret", "the original error identity must remain unchanged")
}

func TestNewDoRequestFailedErrorSanitizesLogAndKeepsErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	oldDebugEnabled := common2.DebugEnabled
	oldErrorWriter := gin.DefaultErrorWriter
	var logOutput bytes.Buffer
	common2.DebugEnabled = false
	gin.DefaultErrorWriter = &logOutput
	t.Cleanup(func() {
		common2.DebugEnabled = oldDebugEnabled
		gin.DefaultErrorWriter = oldErrorWriter
	})

	cause := errors.New("connection refused")
	original := &url.Error{
		Op:  http.MethodPost,
		URL: "https://example.test/v1/responses?token=token-secret&client_secret=client-secret&model=gpt-5.6",
		Err: cause,
	}

	apiErr := newDoRequestFailedError(ctx, original)

	gotLog := logOutput.String()
	require.NotContains(t, gotLog, "token-secret")
	require.NotContains(t, gotLog, "client-secret")
	require.Contains(t, gotLog, "model=gpt-5.6")
	require.Equal(t, types.ErrorCodeDoRequestFailed, apiErr.GetErrorCode())
	require.Equal(t, "upstream error: do request failed", apiErr.Error())
}

func TestNewDoRequestFailedErrorClassifiesActiveRequestTimeoutAsGatewayTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	original := &url.Error{
		Op:  http.MethodPost,
		URL: "https://example.test/v1/responses",
		Err: context.DeadlineExceeded,
	}

	apiErr := newDoRequestFailedError(ctx, original)

	require.Equal(t, http.StatusGatewayTimeout, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeChannelResponseTimeExceeded, apiErr.GetErrorCode())
	require.Equal(t, "upstream timeout while waiting for response headers", apiErr.Error())
}

func TestNewDoRequestFailedErrorDoesNotRelabelCanceledParentRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	apiErr := newDoRequestFailedError(ctx, context.DeadlineExceeded)

	require.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeDoRequestFailed, apiErr.GetErrorCode())
}

func TestApplyUpstreamRequestID(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyUpstreamRequestID(req, &relaycommon.RelayInfo{RequestId: " req-new-api-1 "})

	require.Equal(t, "req-new-api-1", req.Header.Get(common2.RequestIdKey))
}

func TestApplyUpstreamRequestIDSkipsEmpty(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyUpstreamRequestID(req, &relaycommon.RelayInfo{})

	require.Empty(t, req.Header.Get(common2.RequestIdKey))
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}
