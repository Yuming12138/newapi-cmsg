package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func registrationLimitTestRouter(t *testing.T, useRedis bool) (*gin.Engine, *miniredis.Miniredis) {
	t.Helper()
	oldRedisEnabled, oldRDB := common.RedisEnabled, common.RDB
	oldLimit, oldDuration := common.RegistrationRateLimitNum, common.RegistrationRateLimitDuration
	common.RedisEnabled = useRedis
	common.RegistrationRateLimitNum, common.RegistrationRateLimitDuration = 5, 600
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = oldRedisEnabled, oldRDB
		common.RegistrationRateLimitNum, common.RegistrationRateLimitDuration = oldLimit, oldDuration
	})
	var server *miniredis.Miniredis
	if useRedis {
		server = miniredis.RunT(t)
		common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
		t.Cleanup(func() { _ = common.RDB.Close() })
	}
	router := gin.New()
	require.NoError(t, common.ConfigureTrustedProxies(router, "172.18.0.1/32"))
	router.POST("/api/user/register", RegistrationRateLimit(), func(c *gin.Context) {
		// Failed code validation must still consume a registration attempt.
		c.Status(http.StatusForbidden)
	})
	router.POST("/api/user/login", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router, server
}

func registrationLimitRequest(router *gin.Engine, path, peer, forwarded string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = peer
	req.Header.Set("X-Forwarded-For", forwarded)
	req.Header.Set("X-Real-IP", forwarded)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestRegistrationRateLimitCountsFailedAttemptsAndIgnoresSpoofedIPs(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", useRedis), func(t *testing.T) {
			router, _ := registrationLimitTestRouter(t, useRedis)
			for i := 0; i < 5; i++ {
				resp := registrationLimitRequest(router, "/api/user/register", "198.51.100.10:1234", fmt.Sprintf("10.0.0.%d", i+1))
				require.Equal(t, http.StatusForbidden, resp.Code)
			}
			resp := registrationLimitRequest(router, "/api/user/register", "198.51.100.10:1234", "10.0.0.99")
			require.Equal(t, http.StatusTooManyRequests, resp.Code)
			retryAfter, err := strconv.Atoi(resp.Header().Get("Retry-After"))
			require.NoError(t, err)
			require.Positive(t, retryAfter)
			require.LessOrEqual(t, retryAfter, 600)
			require.Equal(t, http.StatusForbidden, registrationLimitRequest(router, "/api/user/register", "198.51.100.11:1234", "10.0.0.99").Code)
			require.Equal(t, http.StatusNoContent, registrationLimitRequest(router, "/api/user/login", "198.51.100.10:1234", "10.0.0.99").Code)
		})
	}
}

func TestRegistrationRateLimitKeepsConcurrentAttemptsBounded(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", useRedis), func(t *testing.T) {
			router, _ := registrationLimitTestRouter(t, useRedis)
			var accepted, limited, other atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					resp := registrationLimitRequest(router, "/api/user/register", "172.18.0.1:1234", fmt.Sprintf("10.0.0.%d, 198.51.100.10", i+1))
					switch resp.Code {
					case http.StatusForbidden:
						accepted.Add(1)
					case http.StatusTooManyRequests:
						limited.Add(1)
					default:
						other.Add(1)
					}
				}(i)
			}
			wg.Wait()
			require.EqualValues(t, 5, accepted.Load())
			require.EqualValues(t, 27, limited.Load())
			require.Zero(t, other.Load())
		})
	}
}

func TestRegistrationRateLimitRedisExpiryAndFailure(t *testing.T) {
	router, server := registrationLimitTestRouter(t, true)
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusForbidden, registrationLimitRequest(router, "/api/user/register", "198.51.100.10:1234", "").Code)
	}
	key := "rateLimit:REGISTER:198.51.100.10"
	require.Equal(t, 600*time.Second, server.TTL(key))
	server.FastForward(601 * time.Second)
	require.Equal(t, http.StatusForbidden, registrationLimitRequest(router, "/api/user/register", "198.51.100.10:1234", "").Code)
	server.Close()
	require.Equal(t, http.StatusServiceUnavailable, registrationLimitRequest(router, "/api/user/register", "198.51.100.10:1234", "").Code)
}

func TestRegistrationRateLimitRedisSlidingWindow(t *testing.T) {
	_, _ = registrationLimitTestRouter(t, true)
	ctx := context.Background()
	key := "rateLimit:REGISTER:sliding-window-test"
	for _, now := range []int64{100000, 200000, 300000, 400000, 500000} {
		result, err := registrationRateLimitScript.Run(ctx, common.RDB, []string{key}, now, 600000, 5).Int64Slice()
		require.NoError(t, err)
		require.EqualValues(t, 1, result[0])
	}
	result, err := registrationRateLimitScript.Run(ctx, common.RDB, []string{key}, 699999, 600000, 5).Int64Slice()
	require.NoError(t, err)
	require.Equal(t, []int64{0, 1}, result)
	result, err = registrationRateLimitScript.Run(ctx, common.RDB, []string{key}, 700000, 600000, 5).Int64Slice()
	require.NoError(t, err)
	require.EqualValues(t, 1, result[0])
	result, err = registrationRateLimitScript.Run(ctx, common.RDB, []string{key}, 700001, 600000, 5).Int64Slice()
	require.NoError(t, err)
	require.Equal(t, []int64{0, 99999}, result)
	require.EqualValues(t, 5, common.RDB.LLen(ctx, key).Val())
}
