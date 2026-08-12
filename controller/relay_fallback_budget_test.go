package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFallbackBudgetTestContext(setting dto.ChannelSettings) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyChannelSetting, setting)
	return c
}

func TestApplyFallbackPreResponseBudgetCapsRetryToRemainingBudget(t *testing.T) {
	c := newFallbackBudgetTestContext(dto.ChannelSettings{
		PreResponseTimeoutSeconds:        120,
		FallbackPreResponseBudgetSeconds: 180,
	})

	require.Nil(t, applyFallbackPreResponseBudget(c))
	setting, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	require.True(t, ok)
	assert.Equal(t, 120, setting.PreResponseTimeoutSeconds)

	c.Set(fallbackPreResponseBudgetStartedAtKey, time.Now().Add(-80*time.Second))
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{
		PreResponseTimeoutSeconds:        120,
		FallbackPreResponseBudgetSeconds: 180,
	})

	require.Nil(t, applyFallbackPreResponseBudget(c))
	setting, ok = common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	require.True(t, ok)
	assert.InDelta(t, 100, setting.PreResponseTimeoutSeconds, 1)
}

func TestApplyFallbackPreResponseBudgetRejectsExhaustedRetry(t *testing.T) {
	c := newFallbackBudgetTestContext(dto.ChannelSettings{
		PreResponseTimeoutSeconds:        120,
		FallbackPreResponseBudgetSeconds: 180,
	})
	c.Set(fallbackPreResponseBudgetKey, 180*time.Second)
	c.Set(fallbackPreResponseBudgetStartedAtKey, time.Now().Add(-181*time.Second))

	err := applyFallbackPreResponseBudget(c)
	require.NotNil(t, err)
	assert.Equal(t, http.StatusGatewayTimeout, err.StatusCode)
	assert.Equal(t, types.ErrorCodeChannelResponseTimeExceeded, err.GetErrorCode())
}

func TestApplyFallbackPreResponseBudgetDisabledByDefault(t *testing.T) {
	c := newFallbackBudgetTestContext(dto.ChannelSettings{PreResponseTimeoutSeconds: 120})

	require.Nil(t, applyFallbackPreResponseBudget(c))
	setting, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	require.True(t, ok)
	assert.Equal(t, 120, setting.PreResponseTimeoutSeconds)
}
