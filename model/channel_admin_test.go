package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetRandomSatisfiedChannelForRequestPathAdminAllowsBudgetAutoDisabledOnly(t *testing.T) {
	truncateTables(t)

	priority := int64(10)
	budgetInfo, err := common.Marshal(map[string]interface{}{
		"status_reason": "channel_budget_exhausted: daily limit",
		"budget_guard": map[string]interface{}{
			"managed":           true,
			"reason":            "budget_exhausted",
			"disabled_by_guard": true,
		},
	})
	require.NoError(t, err)
	upstreamInfo, err := common.Marshal(map[string]interface{}{"status_reason": "upstream authentication failed"})
	require.NoError(t, err)

	channels := []*Channel{
		{Id: 901, Name: "budget", Key: "test", Group: "admin-group", Models: "admin-model", Status: common.ChannelStatusAutoDisabled, OtherInfo: string(budgetInfo), Priority: &priority},
		{Id: 902, Name: "upstream-failure", Key: "test", Group: "admin-group", Models: "admin-model", Status: common.ChannelStatusAutoDisabled, OtherInfo: string(upstreamInfo), Priority: &priority},
		{Id: 903, Name: "manual", Key: "test", Group: "admin-group", Models: "admin-model", Status: common.ChannelStatusManuallyDisabled, Priority: &priority},
	}
	require.NoError(t, DB.Create(&channels).Error)
	abilities := []*Ability{
		{Group: "admin-group", Model: "admin-model", ChannelId: 901, Enabled: false, Priority: &priority},
		{Group: "admin-group", Model: "admin-model", ChannelId: 902, Enabled: false, Priority: &priority},
		{Group: "admin-group", Model: "admin-model", ChannelId: 903, Enabled: false, Priority: &priority},
	}
	require.NoError(t, DB.Create(&abilities).Error)

	selected, err := GetRandomSatisfiedChannelForRequestPathAdmin("admin-group", "admin-model", 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 901, selected.Id)

	// Removing the budget marker must not turn an unrelated auto-disabled
	// channel into an administrator candidate.
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 901).Update("other_info", "{}").Error)
	selected, err = GetRandomSatisfiedChannelForRequestPathAdmin("admin-group", "admin-model", 0, "")
	require.NoError(t, err)
	require.Nil(t, selected)
}

func TestIsChannelAvailableForAdminGroupModelHonorsAbilityAndStatus(t *testing.T) {
	truncateTables(t)

	priority := int64(1)
	budgetInfo, err := common.Marshal(map[string]interface{}{
		"status_reason": "channel_budget_exhausted: daily limit",
		"budget_guard": map[string]interface{}{
			"reason":            "budget_exhausted",
			"disabled_by_guard": true,
		},
	})
	require.NoError(t, err)
	staleInfo, err := common.Marshal(map[string]interface{}{
		"budget_guard": map[string]interface{}{
			"reason":            "budget_exhausted",
			"disabled_by_guard": false,
		},
	})
	require.NoError(t, err)
	channels := []*Channel{
		{Id: 911, Name: "budget", Key: "test", Status: common.ChannelStatusAutoDisabled, OtherInfo: string(budgetInfo), Priority: &priority},
		{Id: 912, Name: "stale", Key: "test", Status: common.ChannelStatusAutoDisabled, OtherInfo: string(staleInfo), Priority: &priority},
		{Id: 913, Name: "manual", Key: "test", Status: common.ChannelStatusManuallyDisabled, Priority: &priority},
	}
	require.NoError(t, DB.Create(&channels).Error)
	abilities := []*Ability{
		{Group: "admin-group", Model: "admin-model", ChannelId: 911, Enabled: false, Priority: &priority},
		{Group: "admin-group", Model: "admin-model", ChannelId: 912, Enabled: false, Priority: &priority},
		{Group: "admin-group", Model: "admin-model", ChannelId: 913, Enabled: true, Priority: &priority},
	}
	require.NoError(t, DB.Create(&abilities).Error)

	require.True(t, IsChannelAvailableForAdminGroupModel(channels[0], "admin-group", "admin-model"))
	require.False(t, IsChannelAvailableForAdminGroupModel(channels[1], "admin-group", "admin-model"))
	require.False(t, IsChannelAvailableForAdminGroupModel(channels[2], "admin-group", "admin-model"))
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
