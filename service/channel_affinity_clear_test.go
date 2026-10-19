package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newSessionAffinityTestContext(skipRetry bool) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setChannelAffinityContext(c, channelAffinityMeta{
		CacheKey:    "unit-test:key",
		RuleName:    "codex cli trace",
		SkipRetry:   skipRetry,
		UsingGroup:  "deepseek",
		ModelName:   "gpt-6.1-sol",
		RequestPath: "/v1/responses",
	})
	return c
}

func TestShouldClearChannelAffinityForRequest(t *testing.T) {
	oldAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() { common.AutomaticDisableChannelEnabled = oldAutoDisable }()

	transient := types.NewErrorWithStatusCode(
		fmt.Errorf("upstream hiccups"),
		types.ErrorCodeDoRequestFailed,
		http.StatusInternalServerError,
	)

	// CLI session rules keep their affinity across transient failures so the
	// retry does not escape to upstreams that cannot serve the continuation.
	c := newSessionAffinityTestContext(true)
	require.False(t, ShouldClearChannelAffinityForRequest(c, transient))

	// Without a session rule the legacy behaviour stays: clear and re-route.
	c = newSessionAffinityTestContext(false)
	require.True(t, ShouldClearChannelAffinityForRequest(c, transient))

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c = c2
	require.True(t, ShouldClearChannelAffinityForRequest(c, transient))

	// A disable-worthy failure always clears, otherwise the request would be
	// trapped on a broken channel.
	disableWorthy := types.NewErrorWithStatusCode(
		fmt.Errorf("invalid api key"),
		types.ErrorCodeChannelInvalidKey,
		http.StatusUnauthorized,
	)
	c = newSessionAffinityTestContext(true)
	require.True(t, ShouldClearChannelAffinityForRequest(c, disableWorthy))

	// Non-affinity-related errors never clear.
	require.False(t, ShouldClearChannelAffinityForRequest(nil, nil))
}
