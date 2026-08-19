package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetSharedFallbackQuotaState(t *testing.T) {
	cfg := operation_setting.GetModelGroupRouteSetting()
	originalCfg := *cfg
	cfg.FallbackQuotaSourceChannelID = 1
	cfg.FallbackQuotaSourceMaxAgeSeconds = 300
	t.Cleanup(func() { *cfg = originalCfg })

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:shared-fallback-quota?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalDB != nil {
			model.InitChannelCache()
		}
	})

	now := time.Unix(1_800_000_000, 0)
	quotaSource := func(status string, spendable bool, balance float64, updatedAt int64) string {
		raw, marshalErr := common.Marshal(map[string]interface{}{
			"quota_source": map[string]interface{}{
				"status": status, "spendable": spendable, "balance": balance, "updated_at": updatedAt,
			},
		})
		require.NoError(t, marshalErr)
		return string(raw)
	}

	tests := []struct {
		name      string
		otherInfo string
		want      SharedFallbackQuotaState
	}{
		{name: "fresh spendable balance", otherInfo: quotaSource("available", true, 12.5, now.Unix()), want: SharedFallbackQuotaSpendable},
		{name: "fresh exhausted balance", otherInfo: quotaSource("quota_exhausted", false, 0, now.Unix()), want: SharedFallbackQuotaExhausted},
		{name: "failed probe is unknown", otherInfo: quotaSource("unknown", false, 0, now.Unix()), want: SharedFallbackQuotaUnknown},
		{name: "stale exhaustion is unknown", otherInfo: quotaSource("quota_exhausted", false, 0, now.Add(-301*time.Second).Unix()), want: SharedFallbackQuotaUnknown},
		{name: "missing quota source is unknown", otherInfo: "{}", want: SharedFallbackQuotaUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, db.Exec("DELETE FROM channels").Error)
			channel := model.Channel{Id: 1, Name: "asxs-shared-quota", Status: common.ChannelStatusEnabled, OtherInfo: test.otherInfo}
			require.NoError(t, db.Create(&channel).Error)
			model.InitChannelCache()
			require.Equal(t, test.want, GetSharedFallbackQuotaState(now))
		})
	}
}

func TestResolveModelGroupRoute(t *testing.T) {
	cfg := operation_setting.GetModelGroupRouteSetting()
	original := *cfg
	cfg.Enabled = true
	cfg.UserGroups = []string{"cmsg"}
	cfg.SourceGroups = []string{"asxs", "cmsg"}
	cfg.ModelPrefixes = []string{"gpt-5.6-sol", "gpt-image"}
	cfg.PreferredGroup = "cliproxy-codex"
	cfg.FallbackGroup = "asxs"
	t.Cleanup(func() { *cfg = original })

	tests := []struct {
		name        string
		userGroup   string
		sourceGroup string
		model       string
		matched     bool
	}{
		{name: "canonical cmsg", userGroup: "cmsg", sourceGroup: "asxs", model: "gpt-5.6-sol", matched: true},
		{name: "legacy asxs identity", userGroup: "asxs", sourceGroup: "asxs", model: "gpt-5.6-sol", matched: true},
		{name: "compact model", userGroup: "asxs", sourceGroup: "asxs", model: "gpt-5.6-sol-openai-compact", matched: true},
		{name: "identity group fallback", userGroup: "cmsg", sourceGroup: "cmsg", model: "gpt-5.6-sol", matched: true},
		{name: "image model", userGroup: "cmsg", sourceGroup: "asxs", model: "gpt-image-2", matched: true},
		{name: "terra stays in source group", userGroup: "cmsg", sourceGroup: "asxs", model: "gpt-5.6-terra", matched: false},
		{name: "luna stays in source group", userGroup: "cmsg", sourceGroup: "asxs", model: "gpt-5.6-luna", matched: false},
		{name: "explicit real 5.6 group", userGroup: "cmsg", sourceGroup: "asxs-gpt56", model: "gpt-5.6-sol", matched: false},
		{name: "direct 5.5 request", userGroup: "cmsg", sourceGroup: "asxs", model: "gpt-5.5", matched: false},
		{name: "outside user group", userGroup: "default", sourceGroup: "asxs", model: "gpt-5.6-sol", matched: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, ok := ResolveModelGroupRoute(tt.userGroup, tt.sourceGroup, tt.model)
			require.Equal(t, tt.matched, ok)
			if ok {
				require.Equal(t, "cliproxy-codex", route.PreferredGroup)
				require.Equal(t, "asxs", route.FallbackGroup)
			}
		})
	}
}

func TestCacheGetRandomSatisfiedChannelUsesPreferredThenFallbackGroup(t *testing.T) {
	cfg := operation_setting.GetModelGroupRouteSetting()
	originalCfg := *cfg
	cfg.Enabled = true
	cfg.UserGroups = []string{"cmsg"}
	cfg.SourceGroups = []string{"asxs"}
	cfg.ModelPrefixes = []string{"gpt-5.6-sol", "gpt-image"}
	cfg.PreferredGroup = "cliproxy-codex"
	cfg.FallbackGroup = "asxs-gpt56-direct"
	t.Cleanup(func() { *cfg = originalCfg })

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:model-group-route?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalDB != nil {
			model.InitChannelCache()
		}
	})

	priority := int64(0)
	channels := []model.Channel{
		{Id: 27, Name: "asxs-gpt56-direct", Group: "asxs-gpt56-direct", Models: "gpt-5.6-sol", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
		{Id: 12, Name: "cliproxy-codex-pool", Group: "cliproxy-codex", Models: "gpt-5.6-sol", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
		{Id: 23, Name: "cliproxy-image-pool", Group: "cliproxy-codex", Models: "gpt-image-2", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
	}
	require.NoError(t, db.Create(&channels).Error)
	abilities := []model.Ability{
		{Group: "asxs-gpt56-direct", Model: "gpt-5.6-sol", ChannelId: 27, Enabled: true, Priority: &priority},
		{Group: "cliproxy-codex", Model: "gpt-5.6-sol", ChannelId: 12, Enabled: true, Priority: &priority},
		{Group: "cliproxy-codex", Model: "gpt-image-2", ChannelId: 23, Enabled: true, Priority: &priority},
	}
	require.NoError(t, db.Create(&abilities).Error)
	model.InitChannelCache()

	newContext := func() *gin.Context {
		ctx, _ := gin.CreateTestContext(nil)
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, "asxs")
		return ctx
	}

	preferredContext := newContext()
	channel, group, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: preferredContext, TokenGroup: "asxs", ModelName: "gpt-5.6-sol", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.Equal(t, 12, channel.Id)
	require.Equal(t, "cliproxy-codex", group)
	require.Equal(t, "cliproxy-codex", common.GetContextKeyString(preferredContext, constant.ContextKeyAutoGroup))

	imageContext := newContext()
	channel, group, err = CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: imageContext, TokenGroup: "asxs", ModelName: "gpt-image-2", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.Equal(t, 23, channel.Id)
	require.Equal(t, "cliproxy-codex", group)
	require.Equal(t, "cliproxy-codex", common.GetContextKeyString(imageContext, constant.ContextKeyAutoGroup))

	ExcludeChannelForRequest(preferredContext, 12, "test retry")
	channel, group, err = CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: preferredContext, TokenGroup: "asxs", ModelName: "gpt-5.6-sol", Retry: common.GetPointer(1),
	})
	require.NoError(t, err)
	require.Equal(t, 27, channel.Id)
	require.Empty(t, channel.GetModelMapping())
	require.Equal(t, "asxs-gpt56-direct", group)
	require.Equal(t, "asxs-gpt56-direct", common.GetContextKeyString(preferredContext, constant.ContextKeyAutoGroup))

	newRequestAfterRecovery := newContext()
	channel, group, err = CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: newRequestAfterRecovery, TokenGroup: "asxs", ModelName: "gpt-5.6-sol", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.Equal(t, 12, channel.Id)
	require.Equal(t, "cliproxy-codex", group)

	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 12).Update("status", common.ChannelStatusAutoDisabled).Error)
	model.InitChannelCache()
	fallbackContext := newContext()
	channel, group, err = CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: fallbackContext, TokenGroup: "asxs", ModelName: "gpt-5.6-sol", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.Equal(t, 27, channel.Id)
	require.Equal(t, "asxs-gpt56-direct", group)
	require.Equal(t, "asxs-gpt56-direct", common.GetContextKeyString(fallbackContext, constant.ContextKeyAutoGroup))
}

func TestQuotaProtectionTriesSharedFallbackBeforeLunaReserve(t *testing.T) {
	cfg := operation_setting.GetModelGroupRouteSetting()
	originalCfg := *cfg
	cfg.Enabled = true
	cfg.UserGroups = []string{"cmsg"}
	cfg.SourceGroups = []string{"asxs"}
	cfg.ModelPrefixes = []string{"gpt-5.6-sol", "gpt-5.6-terra"}
	cfg.PreferredGroup = "cliproxy-codex"
	cfg.FallbackGroup = "asxs-gpt56-direct"
	t.Cleanup(func() { *cfg = originalCfg })

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:quota-route-fallback?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalDB != nil {
			model.InitChannelCache()
		}
	})

	priority := int64(0)
	channels := []model.Channel{
		{Id: 12, Name: "cliproxy-codex-pool", Group: "cliproxy-codex", Models: "gpt-5.6-sol,gpt-5.6-terra,gpt-5.6-luna", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
		// Channel 27 intentionally has no quota_source. Its spendable balance is
		// shared with another upstream channel and is not modeled independently.
		{Id: 27, Name: "asxs-5x", Group: "asxs-gpt56-direct", Models: "gpt-5.6-sol,gpt-5.6-terra", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
	}
	require.NoError(t, db.Create(&channels).Error)
	abilities := []model.Ability{
		{Group: "cliproxy-codex", Model: "gpt-5.6-sol", ChannelId: 12, Enabled: true, Priority: &priority},
		{Group: "cliproxy-codex", Model: "gpt-5.6-terra", ChannelId: 12, Enabled: true, Priority: &priority},
		{Group: "cliproxy-codex", Model: "gpt-5.6-luna", ChannelId: 12, Enabled: true, Priority: &priority},
		{Group: "asxs-gpt56-direct", Model: "gpt-5.6-sol", ChannelId: 27, Enabled: true, Priority: &priority},
		{Group: "asxs-gpt56-direct", Model: "gpt-5.6-terra", ChannelId: 27, Enabled: true, Priority: &priority},
	}
	require.NoError(t, db.Create(&abilities).Error)
	model.InitChannelCache()

	for _, sourceModel := range []string{"gpt-5.6-sol", "gpt-5.6-terra"} {
		t.Run(sourceModel, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(nil)
			common.SetContextKey(ctx, constant.ContextKeyUserGroup, "cmsg")
			common.SetContextKey(ctx, constant.ContextKeyQuotaProtectionPendingFallback, true)
			common.SetContextKey(ctx, constant.ContextKeyQuotaProtectionPendingModel, sourceModel)
			common.SetContextKey(ctx, constant.ContextKeyQuotaProtectionPendingGroup, "asxs-gpt56-direct")
			common.SetContextKey(ctx, constant.ContextKeyQuotaProtectionPendingReserveModel, "gpt-5.6-luna")
			common.SetContextKey(ctx, constant.ContextKeyQuotaProtectionPendingReserveGroup, "cliproxy-codex")

			param := &RetryParam{Ctx: ctx, TokenGroup: "asxs-gpt56-direct", ModelName: sourceModel, Retry: common.GetPointer(0)}
			channel, group, err := CacheGetRandomSatisfiedChannel(param)
			require.NoError(t, err)
			require.Equal(t, 27, channel.Id)
			require.Equal(t, "asxs-gpt56-direct", group)
			require.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyQuotaProtectionPendingFallback))
			require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackModel))

			ExcludeChannelForRequest(ctx, 27, "shared upstream unavailable")
			param.SetRetry(1)
			channel, group, err = CacheGetRandomSatisfiedChannel(param)
			require.NoError(t, err)
			require.Equal(t, 12, channel.Id)
			require.Equal(t, "cliproxy-codex", group)
			require.False(t, common.GetContextKeyBool(ctx, constant.ContextKeyQuotaProtectionPendingFallback))
			require.Equal(t, "gpt-5.6-luna", common.GetContextKeyString(ctx, constant.ContextKeyQuotaProtectionFallbackModel))
		})
	}
}
