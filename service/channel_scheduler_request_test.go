package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandleChannelFailureClearsAffinityAndTemporarilyUnschedules(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	cache := getChannelAffinityCache()
	cacheKey := "channel-scheduler-test"
	require.NoError(t, cache.SetWithTTL(cacheKey, 9527, time.Minute))
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{cacheKey})
		model.ClearChannelTemporarilyUnschedulable(9527)
	})

	setChannelAffinityContext(ctx, channelAffinityMeta{
		CacheKey:   cache.FullKey(cacheKey),
		TTLSeconds: 60,
		RuleName:   "codex cli trace",
		SkipRetry:  true,
	})

	channelError := *types.NewChannelError(9527, 1, "test-channel", false, "", false)
	upstreamErr := types.NewOpenAIError(errors.New("quota exceeded for current account"), types.ErrorCodeBadResponseStatusCode, http.StatusForbidden)

	HandleChannelFailure(ctx, channelError, upstreamErr)

	excluded := GetExcludedChannelIDsForRequest(ctx)
	_, found := excluded[9527]
	require.True(t, found)
	require.False(t, ShouldSkipRetryAfterChannelAffinityFailure(ctx))

	_, cacheFound, err := cache.Get(cacheKey)
	require.NoError(t, err)
	require.False(t, cacheFound)

	blocked, state := model.IsChannelTemporarilyUnschedulable(9527)
	require.True(t, blocked)
	require.NotNil(t, state)
	require.Equal(t, http.StatusForbidden, state.StatusCode)
}
