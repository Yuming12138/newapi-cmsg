package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInitChannelCacheUsesOnlyEnabledAbilities(t *testing.T) {
	originalDB := DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalGroupMap := group2model2channels
	originalChannelMap := channelsIDM
	originalAdvancedCustomMap := channel2advancedCustomConfig
	t.Cleanup(func() {
		DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		group2model2channels = originalGroupMap
		channelsIDM = originalChannelMap
		channel2advancedCustomConfig = originalAdvancedCustomMap
	})

	db, err := gorm.Open(sqlite.Open("file:channel-cache-enabled-abilities?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	common.MemoryCacheEnabled = true

	priority := int64(10)
	weight := uint(100)
	channel := Channel{
		Id:       12,
		Name:     "cliproxy-codex-pool",
		Key:      "test",
		Status:   common.ChannelStatusEnabled,
		Group:    "cliproxy-codex",
		Models:   "gpt-5.6-luna,gpt-5.6-sol",
		Priority: &priority,
		Weight:   &weight,
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&Ability{
		Group: "cliproxy-codex", Model: "gpt-5.6-luna", ChannelId: 12, Enabled: true, Priority: &priority, Weight: 100,
	}).Error)
	require.NoError(t, db.Create(&Ability{
		Group: "cliproxy-codex", Model: "gpt-5.6-sol", ChannelId: 12, Enabled: false, Priority: &priority, Weight: 100,
	}).Error)

	InitChannelCache()

	require.True(t, IsChannelEnabledForGroupModel("cliproxy-codex", "gpt-5.6-luna", 12))
	require.False(t, IsChannelEnabledForGroupModel("cliproxy-codex", "gpt-5.6-sol", 12))
	selected, err := GetRandomSatisfiedChannel("cliproxy-codex", "gpt-5.6-sol", 0)
	require.NoError(t, err)
	require.Nil(t, selected)
}

func TestGetRandomSatisfiedChannelFallsBackToDatabaseWhenCacheIsStale(t *testing.T) {
	originalDB := DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalGroupMap := group2model2channels
	originalChannelMap := channelsIDM
	originalAdvancedCustomMap := channel2advancedCustomConfig
	t.Cleanup(func() {
		deadline := time.Now().Add(time.Second)
		for channelCacheRefreshInFlight.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		group2model2channels = originalGroupMap
		channelsIDM = originalChannelMap
		channel2advancedCustomConfig = originalAdvancedCustomMap
	})

	db, err := gorm.Open(sqlite.Open("file:channel-cache-stale-fallback?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	common.MemoryCacheEnabled = true

	priority := int64(10)
	weight := uint(100)
	channel := Channel{
		Id:       120,
		Name:     "stale-cache-channel",
		Key:      "test",
		Status:   common.ChannelStatusAutoDisabled,
		Group:    "stale-group",
		Models:   "stale-model",
		Priority: &priority,
		Weight:   &weight,
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&Ability{
		Group: "stale-group", Model: "stale-model", ChannelId: channel.Id, Enabled: false, Priority: &priority, Weight: 100,
	}).Error)
	InitChannelCache()

	// Simulate an out-of-band guard update after the last cache tick.
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusEnabled).Error)
	require.NoError(t, db.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", true).Error)

	selected, err := GetRandomSatisfiedChannelForRequestPath("stale-group", "stale-model", 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, channel.Id, selected.Id)
}
