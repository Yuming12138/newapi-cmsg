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

func TestConfigureSharedQuotaRouteFallbackBypassesExhaustedSharedPool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	route := service.ModelGroupRoute{PreferredGroup: "cliproxy-codex", FallbackGroup: "asxs-gpt56-direct"}
	block := &service.ChannelQuotaProtectionBlock{
		Group:         "cliproxy-codex",
		AllowedModels: []string{"gpt-5.6-luna"},
	}

	modelName, group := configureSharedQuotaRouteFallback(ctx, "gpt-5.6-sol", route, block, service.SharedFallbackQuotaExhausted)

	require.Equal(t, "gpt-5.6-luna", modelName)
	require.Equal(t, "cliproxy-codex", group)
	require.False(t, common.GetContextKeyBool(ctx, constant.ContextKeyQuotaProtectionPendingFallback))
	require.Equal(t, "gpt-5.6-luna", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackModel))
}

func TestConfigureSharedQuotaRouteFallbackKeepsDirectRouteForSpendableOrUnknownPool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	route := service.ModelGroupRoute{PreferredGroup: "cliproxy-codex", FallbackGroup: "asxs-gpt56-direct"}
	block := &service.ChannelQuotaProtectionBlock{
		Group:         "cliproxy-codex",
		AllowedModels: []string{"gpt-5.6-luna"},
	}

	for _, state := range []service.SharedFallbackQuotaState{service.SharedFallbackQuotaSpendable, service.SharedFallbackQuotaUnknown} {
		ctx, _ := gin.CreateTestContext(nil)
		modelName, group := configureSharedQuotaRouteFallback(ctx, "gpt-5.6-terra", route, block, state)

		require.Equal(t, "gpt-5.6-terra", modelName)
		require.Equal(t, "asxs-gpt56-direct", group)
		require.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyQuotaProtectionPendingFallback))
		require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackModel))
	}
}
