package common

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSanitizeURLForLogMasksSensitiveQueryValues(t *testing.T) {
	rawURL := "https://example.test/v1beta/models/gemini:streamGenerateContent?" +
		"key=key-value&ACCESS_TOKEN=access-value&token=token-value&secret=secret-value&" +
		"signature=exact-signature-value&session_token=session-value&" +
		"clientSecret=client-secret-value&X-Amz-Signature=signature-value&" +
		"signatureHint=signature-hint-value&alt=sse&model=gemini-2.5-flash"

	got := SanitizeURLForLog(rawURL)

	for _, secret := range []string{
		"key-value",
		"access-value",
		"token-value",
		"secret-value",
		"exact-signature-value",
		"session-value",
		"client-secret-value",
		"signature-value",
		"signature-hint-value",
	} {
		require.NotContains(t, got, secret)
	}

	parsedURL, err := url.Parse(got)
	require.NoError(t, err)
	query := parsedURL.Query()
	for _, key := range []string{
		"key",
		"ACCESS_TOKEN",
		"token",
		"secret",
		"signature",
		"session_token",
		"clientSecret",
		"X-Amz-Signature",
		"signatureHint",
	} {
		require.Equal(t, "***masked***", query.Get(key), "key=%s", key)
	}
	require.Equal(t, "sse", query.Get("alt"))
	require.Equal(t, "gemini-2.5-flash", query.Get("model"))
}

func TestSanitizeURLForLogKeepsOrdinaryQuery(t *testing.T) {
	rawURL := "wss://example.test/v1/realtime?api-version=2026-07-01&alt=sse&model=gpt-5.6"

	require.Equal(t, rawURL, SanitizeURLForLog(rawURL))
}

func TestSanitizeURLForLogMasksMalformedPercentQuery(t *testing.T) {
	rawURL := "https://admin:password@example.test/v1/responses?key=percent-secret%zz&model=gpt-5.6"

	got := SanitizeURLForLog(rawURL)

	require.NotContains(t, got, "admin")
	require.NotContains(t, got, "password")
	require.NotContains(t, got, "percent-secret")
	require.NotContains(t, got, "model=gpt-5.6", "malformed queries must be redacted as a whole")
	require.Equal(t, "***masked***", mustParseURL(t, got).Query().Get("_redacted_"))
}

func TestSanitizeURLForLogMasksSemicolonQuery(t *testing.T) {
	rawURL := "https://example.test/v1/responses?key=semicolon-secret;model=gpt-5.6&alt=sse"

	got := SanitizeURLForLog(rawURL)

	require.NotContains(t, got, "semicolon-secret")
	require.NotContains(t, got, "model=gpt-5.6")
	require.NotContains(t, got, "alt=sse")
	require.Equal(t, "***masked***", mustParseURL(t, got).Query().Get("_redacted_"))
}

func TestSanitizeURLForLogRemovesUserinfoAndKeepsOrdinaryQuery(t *testing.T) {
	rawURL := "https://admin:p%40ssword@example.test/v1/responses?model=gpt-5.6&alt=sse"

	got := SanitizeURLForLog(rawURL)

	require.NotContains(t, got, "admin")
	require.NotContains(t, got, "p%40ssword")
	parsedURL := mustParseURL(t, got)
	require.Nil(t, parsedURL.User)
	require.Equal(t, "example.test", parsedURL.Host)
	require.Equal(t, "gpt-5.6", parsedURL.Query().Get("model"))
	require.Equal(t, "sse", parsedURL.Query().Get("alt"))
}

func TestSanitizeURLErrorForLogPreservesWrappedIdentity(t *testing.T) {
	sentinel := errors.New("dial sentinel")
	originalURL := &url.Error{
		Op:  http.MethodPost,
		URL: "https://example.test/v1/responses?api_key=api-secret&model=gpt-5.6",
		Err: sentinel,
	}
	original := fmt.Errorf("outer request context: %w", originalURL)

	sanitized := SanitizeURLErrorForLog(original)

	require.NotContains(t, sanitized.Error(), "api-secret")
	require.Contains(t, sanitized.Error(), "outer request context")
	require.ErrorIs(t, sanitized, sentinel)

	var extracted *url.Error
	require.ErrorAs(t, sanitized, &extracted)
	require.Same(t, originalURL, extracted, "errors.As must retain the original error identity")
	require.Contains(t, extracted.URL, "api-secret", "the original error must not be mutated")
}

func TestSanitizeURLErrorForLogPreservesJoinedIdentity(t *testing.T) {
	requestSentinel := errors.New("request sentinel")
	joinedSentinel := errors.New("joined sentinel")
	originalURL := &url.Error{
		Op:  http.MethodPost,
		URL: "https://example.test/v1/responses?access_token=joined-secret",
		Err: requestSentinel,
	}
	original := errors.Join(fmt.Errorf("request branch: %w", originalURL), joinedSentinel)

	sanitized := SanitizeURLErrorForLog(original)

	require.NotContains(t, sanitized.Error(), "joined-secret")
	require.ErrorIs(t, sanitized, requestSentinel)
	require.ErrorIs(t, sanitized, joinedSentinel)

	var extracted *url.Error
	require.ErrorAs(t, sanitized, &extracted)
	require.Same(t, originalURL, extracted)
}

func TestSanitizeURLErrorForLogProxiesTimeout(t *testing.T) {
	original := &url.Error{
		Op:  http.MethodPost,
		URL: "https://example.test/v1/responses?token=timeout-secret",
		Err: context.DeadlineExceeded,
	}

	sanitized := SanitizeURLErrorForLog(original)

	require.NotContains(t, sanitized.Error(), "timeout-secret")
	timeoutErr, ok := sanitized.(interface{ Timeout() bool })
	require.True(t, ok)
	require.True(t, timeoutErr.Timeout())
	require.ErrorIs(t, sanitized, context.DeadlineExceeded)
}

func TestSanitizeURLErrorForLogKeepsNonURLError(t *testing.T) {
	original := errors.New("connection refused")

	require.Same(t, original, SanitizeURLErrorForLog(original))
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	require.NoError(t, err)
	return parsedURL
}

func TestValidateMultipartDirectNormalizesImageField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.NewReader(`{"model":"wan2.7-i2v","prompt":"animate","image":" https://example.com/first.png "}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	info := &RelayInfo{
		TaskRelayInfo: &TaskRelayInfo{},
	}

	taskErr := ValidateMultipartDirect(context, info)

	require.Nil(t, taskErr)
	storedReq, err := GetTaskRequest(context)
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.com/first.png"}, storedReq.Images)
	require.Equal(t, constant.TaskActionGenerate, info.Action)
}
