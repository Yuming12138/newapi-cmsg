package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAbortWithOpenAIQuotaMessageReturnsStructured429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	abortWithOpenAiQuotaMessage(
		ctx,
		"渠道今日保护预算已耗尽，预计于 2026-08-06 00:00:00 CST 恢复，请稍后重试",
		types.ErrorCodeChannelDailyProtectedBudgetExhausted,
		1_800_028_800,
		120,
	)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "120", recorder.Header().Get("Retry-After"))
	var payload map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	errorPayload, ok := payload["error"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "quota_protection_error", errorPayload["type"])
	require.Equal(t, "channel_daily_protected_budget_exhausted", errorPayload["code"])
	require.Equal(t, float64(1_800_028_800), errorPayload["retry_at"])
	require.Equal(t, float64(120), errorPayload["retry_after_seconds"])
}
