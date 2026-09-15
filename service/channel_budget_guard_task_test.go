package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestApplySharedBalancePoolsRecoversFromPersistedMarker(t *testing.T) {
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:shared-balance-recovery?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	priority := int64(1)
	source := &model.Channel{Id: 1, Name: "source", Status: common.ChannelStatusEnabled, Balance: 300}
	member := &model.Channel{
		Id:      27,
		Name:    "member",
		Status:  common.ChannelStatusAutoDisabled,
		Balance: 0,
		OtherInfo: `{"shared_balance_guard":{"source_channel_id":1,"disabled_by_guard":true,"updated_at":10},` +
			`"status_reason":"channel_budget_exhausted: shared source channel 1","status_time":10}`,
	}
	require.NoError(t, db.Create(source).Error)
	require.NoError(t, db.Create(member).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "asxs-gpt56-direct", Model: "gpt-6-astra", ChannelId: 27, Enabled: false, Priority: &priority}).Error)

	state := channelBudgetGuardState{Version: 1, Channels: map[string]channelBudgetGuardChannelState{}}
	updated, statusChanged := applySharedBalancePools(&operation_setting.ChannelBudgetGuardSetting{
		SharedBalancePools: []operation_setting.ChannelBudgetGuardSharedPool{{SourceChannelID: 1, MemberChannelIDs: []int{27}}},
	}, []*model.Channel{source, member}, &state, 20)
	require.True(t, updated)
	require.True(t, statusChanged)

	var stored model.Channel
	require.NoError(t, db.First(&stored, 27).Error)
	require.Equal(t, common.ChannelStatusEnabled, stored.Status)
	require.Equal(t, 300.0, stored.Balance)
	info := parseGuardObject(stored.OtherInfo)
	shared, _ := info["shared_balance_guard"].(map[string]interface{})
	disabled, _ := guardObjectBool(shared, "disabled_by_guard")
	require.False(t, disabled)
	_, hasReason := info["status_reason"]
	require.False(t, hasReason)
	require.False(t, state.Channels["27"].DisabledByGuard)

	var ability model.Ability
	require.NoError(t, db.First(&ability, "channel_id = ?", 27).Error)
	require.True(t, ability.Enabled)
}

func TestApplySharedBalancePoolsDoesNotRecoverUnrelatedDisable(t *testing.T) {
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:shared-balance-unrelated?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	source := &model.Channel{Id: 1, Name: "source", Status: common.ChannelStatusEnabled, Balance: 300}
	member := &model.Channel{Id: 27, Name: "member", Status: common.ChannelStatusAutoDisabled, Balance: 0, OtherInfo: `{}`}
	require.NoError(t, db.Create(source).Error)
	require.NoError(t, db.Create(member).Error)
	state := channelBudgetGuardState{Version: 1, Channels: map[string]channelBudgetGuardChannelState{"27": {DisabledByGuard: true}}}

	updated, statusChanged := applySharedBalancePools(&operation_setting.ChannelBudgetGuardSetting{
		SharedBalancePools: []operation_setting.ChannelBudgetGuardSharedPool{{SourceChannelID: 1, MemberChannelIDs: []int{27}}},
	}, []*model.Channel{source, member}, &state, 20)
	require.False(t, updated)
	require.False(t, statusChanged)

	var stored model.Channel
	require.NoError(t, db.First(&stored, 27).Error)
	require.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	require.Equal(t, 0.0, stored.Balance)
}

func TestApplySharedBalancePoolsCleansMarkerAfterManualRecovery(t *testing.T) {
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:shared-balance-manual-recovery?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	source := &model.Channel{Id: 1, Name: "source", Status: common.ChannelStatusEnabled, Balance: 300}
	member := &model.Channel{
		Id:      27,
		Name:    "member",
		Status:  common.ChannelStatusEnabled,
		Balance: 0,
		OtherInfo: `{"shared_balance_guard":{"source_channel_id":1,"disabled_by_guard":true,"updated_at":10},` +
			`"status_reason":"channel_budget_exhausted: shared source channel 1","status_time":10}`,
	}
	require.NoError(t, db.Create(source).Error)
	require.NoError(t, db.Create(member).Error)
	state := channelBudgetGuardState{Version: 1, Channels: map[string]channelBudgetGuardChannelState{}}

	updated, statusChanged := applySharedBalancePools(&operation_setting.ChannelBudgetGuardSetting{
		SharedBalancePools: []operation_setting.ChannelBudgetGuardSharedPool{{SourceChannelID: 1, MemberChannelIDs: []int{27}}},
	}, []*model.Channel{source, member}, &state, 20)
	require.True(t, updated)
	require.False(t, statusChanged)

	var stored model.Channel
	require.NoError(t, db.First(&stored, 27).Error)
	require.Equal(t, common.ChannelStatusEnabled, stored.Status)
	require.Equal(t, 300.0, stored.Balance)
	info := parseGuardObject(stored.OtherInfo)
	shared, _ := info["shared_balance_guard"].(map[string]interface{})
	disabled, _ := guardObjectBool(shared, "disabled_by_guard")
	require.False(t, disabled)
	_, hasReason := info["status_reason"]
	require.False(t, hasReason)
}
