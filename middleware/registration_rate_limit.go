package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

// The bounded list and expiry are updated atomically. Concurrent attempts must
// not all pass an independent LLEN check before any of them increments it.
var registrationRateLimitScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
if redis.call('LLEN', KEYS[1]) >= limit then
    local oldest = tonumber(redis.call('LINDEX', KEYS[1], -1))
    if oldest and now - oldest < window then
        return {0, window - (now - oldest)}
    end
end
redis.call('LPUSH', KEYS[1], now)
redis.call('LTRIM', KEYS[1], 0, limit - 1)
redis.call('PEXPIRE', KEYS[1], window)
return {1, 0}
`)

// RegistrationRateLimit is independent of the shared login/reset limiter. It
// counts every attempt, including requests rejected by later validation.
func RegistrationRateLimit() gin.HandlerFunc {
	limit, duration := common.RegistrationRateLimitNum, common.RegistrationRateLimitDuration
	if common.RedisEnabled {
		return func(c *gin.Context) {
			result, err := registrationRateLimitScript.Run(c.Request.Context(), common.RDB,
				[]string{"rateLimit:REGISTER:" + c.ClientIP()}, time.Now().UnixMilli(), duration*1000, limit).Int64Slice()
			if err != nil || len(result) != 2 {
				common.SysError("registration rate limiter unavailable")
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Registration temporarily unavailable"})
				return
			}
			if result[0] == 0 {
				registrationRateLimitExceeded(c, (result[1]+999)/1000)
			}
		}
	}
	limiter := new(common.InMemoryRateLimiter)
	limiter.Init(time.Duration(duration) * time.Second)
	return func(c *gin.Context) {
		if !limiter.Request(c.ClientIP(), limit, duration) {
			registrationRateLimitExceeded(c, duration)
		}
	}
}

func registrationRateLimitExceeded(c *gin.Context, retryAfter int64) {
	c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "Too many registration attempts"})
}
