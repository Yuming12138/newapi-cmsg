package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResolveModelGroupRoute(t *testing.T) {
	cfg := operation_setting.GetModelGroupRouteSetting()
	original := *cfg
	cfg.Enabled = true
	cfg.UserGroups = []string{"cmsg"}
	cfg.SourceGroups = []string{"asxs", "cmsg"}
	cfg.ModelPrefixes = []string{"gpt-5.6", "gpt-image"}
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
		{name: "legacy asxs identity", userGroup: "asxs", sourceGroup: "asxs", model: "gpt-5.6-terra", matched: true},
		{name: "compact model", userGroup: "asxs", sourceGroup: "asxs", model: "gpt-5.6-sol-openai-compact", matched: true},
		{name: "identity group fallback", userGroup: "cmsg", sourceGroup: "cmsg", model: "gpt-5.6-luna", matched: true},
		{name: "image model", userGroup: "cmsg", sourceGroup: "asxs", model: "gpt-image-2", matched: true},
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
	cfg.ModelPrefixes = []string{"gpt-5.6", "gpt-image"}
	cfg.PreferredGroup = "cliproxy-codex"
	cfg.FallbackGroup = "asxs"
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
		{Id: 1, Name: "asxs-cgm-1.2", Group: "asxs", Models: "gpt-5.6-sol", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
		{Id: 12, Name: "cliproxy-codex-pool", Group: "cliproxy-codex", Models: "gpt-5.6-sol", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
		{Id: 23, Name: "cliproxy-image-pool", Group: "cliproxy-codex", Models: "gpt-image-2", Status: common.ChannelStatusEnabled, Key: "test", Priority: &priority},
	}
	require.NoError(t, db.Create(&channels).Error)
	abilities := []model.Ability{
		{Group: "asxs", Model: "gpt-5.6-sol", ChannelId: 1, Enabled: true, Priority: &priority},
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
	require.Error(t, err)
	require.Nil(t, channel)
	require.Equal(t, "cliproxy-codex", group)

	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 12).Update("status", common.ChannelStatusAutoDisabled).Error)
	model.InitChannelCache()
	fallbackContext := newContext()
	channel, group, err = CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: fallbackContext, TokenGroup: "asxs", ModelName: "gpt-5.6-sol", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.Equal(t, 1, channel.Id)
	require.Equal(t, "asxs", group)
	require.Equal(t, "asxs", common.GetContextKeyString(fallbackContext, constant.ContextKeyAutoGroup))
}
