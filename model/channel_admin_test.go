package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetRandomSatisfiedChannelForRequestPathAdminBypassesLocalChannelGates(t *testing.T) {
	truncateTables(t)

	priorities := []int64{30, 20, 10}
	channels := []*Channel{
		{Id: 901, Name: "auto-disabled", Key: "test", Group: "admin-group", Models: "admin-model", Status: common.ChannelStatusAutoDisabled, Priority: &priorities[0]},
		{Id: 902, Name: "manual-disabled", Key: "test", Group: "admin-group", Models: "admin-model", Status: common.ChannelStatusManuallyDisabled, Priority: &priorities[1]},
		{Id: 903, Name: "ability-disabled", Key: "test", Group: "admin-group", Models: "admin-model", Status: common.ChannelStatusEnabled, Priority: &priorities[2]},
	}
	require.NoError(t, DB.Create(&channels).Error)
	abilities := []*Ability{
		{Group: "admin-group", Model: "admin-model", ChannelId: 901, Enabled: false, Priority: &priorities[0]},
		{Group: "admin-group", Model: "admin-model", ChannelId: 902, Enabled: false, Priority: &priorities[1]},
		{Group: "admin-group", Model: "admin-model", ChannelId: 903, Enabled: false, Priority: &priorities[2]},
	}
	require.NoError(t, DB.Create(&abilities).Error)

	// A scheduler cooldown is a normal-user routing guard, not an administrator
	// permission boundary. The admin selector must still consider this channel.
	require.NoError(t, MarkChannelTemporarilyUnschedulable(901, time.Minute, ChannelTemporaryUnschedulable{Reason: "rate_limit"}))
	t.Cleanup(func() { ClearChannelTemporarilyUnschedulable(901) })

	for _, wantID := range []int{901, 902, 903} {
		excluded := map[int]struct{}{}
		for _, id := range []int{901, 902, 903} {
			if id != wantID {
				excluded[id] = struct{}{}
			}
		}
		selected, err := GetRandomSatisfiedChannelForRequestPathAdmin("admin-group", "admin-model", 0, "", excluded)
		require.NoError(t, err)
		require.NotNil(t, selected)
		require.Equal(t, wantID, selected.Id)
	}
}

func TestIsChannelAvailableForAdminGroupModelIgnoresLocalChannelGates(t *testing.T) {
	truncateTables(t)

	priority := int64(1)
	channels := []*Channel{
		{Id: 911, Name: "auto", Key: "test", Status: common.ChannelStatusAutoDisabled, Priority: &priority},
		{Id: 912, Name: "manual", Key: "test", Status: common.ChannelStatusManuallyDisabled, Priority: &priority},
		{Id: 913, Name: "ability-disabled", Key: "test", Status: common.ChannelStatusEnabled, Priority: &priority},
	}
	require.NoError(t, DB.Create(&channels).Error)
	abilities := []*Ability{
		{Group: "admin-group", Model: "admin-model", ChannelId: 911, Enabled: false, Priority: &priority},
		{Group: "admin-group", Model: "admin-model", ChannelId: 912, Enabled: false, Priority: &priority},
		{Group: "admin-group", Model: "admin-model", ChannelId: 913, Enabled: false, Priority: &priority},
	}
	require.NoError(t, DB.Create(&abilities).Error)

	require.True(t, IsChannelAvailableForAdminGroupModel(channels[0], "admin-group", "admin-model"))
	require.True(t, IsChannelAvailableForAdminGroupModel(channels[1], "admin-group", "admin-model"))
	require.True(t, IsChannelAvailableForAdminGroupModel(channels[2], "admin-group", "admin-model"))
	require.False(t, IsChannelAvailableForAdminGroupModel(channels[0], "other-group", "admin-model"))
}

func TestIsChannelAutoDisabledByBudgetGuardRejectsStaleOrNegativeMarkers(t *testing.T) {
	budgetMarker := func(reason string, disabled bool, statusTime, updatedAt int64) string {
		raw, err := common.Marshal(map[string]interface{}{
			"status_reason": reason,
			"status_time":   statusTime,
			"budget_guard": map[string]interface{}{
				"disabled_by_guard": disabled,
				"updated_at":        updatedAt,
			},
		})
		require.NoError(t, err)
		return string(raw)
	}

	require.True(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: budgetMarker("channel_budget_exhausted: daily limit", true, 100, 100),
	}))
	require.False(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: budgetMarker("channel_budget_exhausted: daily limit", false, 100, 100),
	}))
	require.False(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: budgetMarker("upstream authentication failed", true, 200, 100),
	}))
	require.False(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: budgetMarker("channel_budget_exhausted: daily limit", true, 200, 100),
	}))
}

func TestIsChannelAutoDisabledByBudgetGuardRecognizesCliproxyCPABudgetMetadata(t *testing.T) {
	buildInfo := func(health map[string]interface{}, source map[string]interface{}, statusTime int64) string {
		raw, err := common.Marshal(map[string]interface{}{
			"status_time": statusTime,
			"cliproxy_cpa_quota_guard": map[string]interface{}{
				"managed":                 true,
				"updated_at":              int64(100),
				"desired_enabled":         false,
				"quota_observation_stale": false,
				"health":                  health,
			},
			"quota_source": source,
		})
		require.NoError(t, err)
		return string(raw)
	}

	budgetHealth := map[string]interface{}{
		"ok":       true,
		"quota_ok": false,
		"reason":   "dynamic_daily_budget_exhausted",
		"accounts": []interface{}{
			map[string]interface{}{
				"ok":          true,
				"disabled":    false,
				"unavailable": false,
				"reason":      "quota_7d_exhausted",
			},
		},
	}
	budgetSource := map[string]interface{}{
		"status":        "quota_exhausted",
		"status_reason": "dynamic_daily_budget_exhausted",
		"spendable":     false,
	}
	require.True(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: buildInfo(budgetHealth, budgetSource, 100),
	}))

	// A management/auth failure must not be treated as a balance exhaustion.
	failureHealth := map[string]interface{}{
		"ok":          false,
		"quota_ok":    false,
		"fail_closed": true,
		"reason":      "quota_probe_failed",
	}
	require.False(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: buildInfo(failureHealth, budgetSource, 100),
	}))

	// If every credential is explicitly disabled, the zero balance is an auth
	// state rather than a budget state and must remain unavailable.
	disabledHealth := map[string]interface{}{
		"ok":       true,
		"quota_ok": false,
		"reason":   "quota_low_watermark_reached",
		"accounts": []interface{}{
			map[string]interface{}{
				"ok":       false,
				"disabled": true,
				"reason":   "auth_disabled",
			},
		},
	}
	require.False(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: buildInfo(disabledHealth, budgetSource, 100),
	}))

	// A newer status writer wins over the CPA marker, even if the old health
	// result still says that the quota was exhausted.
	require.False(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: buildInfo(budgetHealth, budgetSource, 101),
	}))

	// An explicit upstream/manual reason must always win over stale CPA data.
	withReason, err := common.Marshal(map[string]interface{}{
		"status_reason": "upstream authentication failed",
		"cliproxy_cpa_quota_guard": map[string]interface{}{
			"managed":         true,
			"updated_at":      int64(100),
			"desired_enabled": false,
			"health":          budgetHealth,
		},
	})
	require.NoError(t, err)
	require.False(t, IsChannelAutoDisabledByBudgetGuard(&Channel{
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: string(withReason),
	}))
}

func TestGetRandomSatisfiedChannelForRequestPathAdminUsesCliproxyBudgetMetadata(t *testing.T) {
	truncateTables(t)

	priority := int64(10)
	otherInfo, err := common.Marshal(map[string]interface{}{
		"cliproxy_cpa_quota_guard": map[string]interface{}{
			"managed":         true,
			"updated_at":      int64(100),
			"desired_enabled": false,
			"health": map[string]interface{}{
				"ok":       true,
				"quota_ok": false,
				"reason":   "quota_low_watermark_reached",
				"accounts": []interface{}{map[string]interface{}{"ok": true}},
			},
		},
		"quota_source": map[string]interface{}{
			"status":        "quota_exhausted",
			"status_reason": "quota_low_watermark_reached",
		},
	})
	require.NoError(t, err)
	channel := &Channel{
		Id:        921,
		Name:      "cliproxy-budget",
		Key:       "test",
		Group:     "cliproxy-codex",
		Models:    "gpt-5.6-luna",
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: string(otherInfo),
		Priority:  &priority,
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "cliproxy-codex",
		Model:     "gpt-5.6-luna",
		ChannelId: channel.Id,
		Enabled:   false,
		Priority:  &priority,
	}).Error)

	selected, err := GetRandomSatisfiedChannelForRequestPathAdmin("cliproxy-codex", "gpt-5.6-luna", 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, channel.Id, selected.Id)
}
