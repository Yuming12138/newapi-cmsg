package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApplyQuotaProtectionFallbackStoresModelAndGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	block := &service.ChannelQuotaProtectionBlock{
		ChannelID:     12,
		Group:         "cliproxy-codex",
		AllowedModels: []string{"gpt-5.6-luna"},
	}

	require.True(t, applyQuotaProtectionFallback(ctx, "gpt-5.6-sol", block))
	require.Equal(t, "gpt-5.6-luna", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackModel))
	require.Equal(t, "cliproxy-codex", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackGroup))
}

func TestApplyQuotaProtectionFallbackDoesNotRemapAllowedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	block := &service.ChannelQuotaProtectionBlock{
		ChannelID:     12,
		Group:         "cliproxy-codex",
		AllowedModels: []string{"gpt-5.6-luna"},
	}

	require.False(t, applyQuotaProtectionFallback(ctx, "gpt-5.6-luna", block))
	_, exists := common.GetContextKey(ctx, constant.ContextKeyQuotaProtectionFallbackModel)
	require.False(t, exists)
}

func TestPendingQuotaProtectionFallbackActivatesReserveOnlyAfterRouteFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	setPendingQuotaProtectionFallback(ctx, "gpt-5.6-terra", "asxs-gpt56-direct", "gpt-5.6-luna", "cliproxy-codex")

	require.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyQuotaProtectionPendingFallback))
	require.Equal(t, "gpt-5.6-terra", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionPendingModel))
	require.Equal(t, "asxs-gpt56-direct", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionPendingGroup))
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackModel))

	require.True(t, activatePendingQuotaProtectionFallback(ctx))
	require.False(t, common.GetContextKeyBool(ctx, constant.ContextKeyQuotaProtectionPendingFallback))
	require.Equal(t, "gpt-5.6-luna", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackModel))
	require.Equal(t, "cliproxy-codex", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackGroup))
	require.False(t, activatePendingQuotaProtectionFallback(ctx))
}

func TestSolTerraModelDetection(t *testing.T) {
	require.True(t, isSolTerraModel("gpt-5.6-sol"))
	require.True(t, isSolTerraModel("gpt-5.6-terra-openai-compact"))
	require.False(t, isSolTerraModel("gpt-5.6-luna"))
}
