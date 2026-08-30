package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCliproxyCPAQuotaForceUnlockBoundaryUsesEarliestResetBoundary(t *testing.T) {
	now := int64(1_800_000_000)
	health := map[string]interface{}{
		"dynamic_daily_budget": map[string]interface{}{
			"next_daily_budget_reset_at": now + 1_800,
			"effective_reset_at":         now + 3_600,
			"weekly_reset_at":            now + 7_200,
			"planning_signature":         "cycle-a",
		},
	}

	until, signature, err := cliproxyCPAQuotaForceUnlockBoundary(health, now)

	require.NoError(t, err)
	require.Equal(t, now+1_800, until)
	require.Equal(t, "cycle-a", signature)
}

func TestCliproxyCPAQuotaForceUnlockBoundaryRejectsStaleReset(t *testing.T) {
	now := int64(1_800_000_000)
	health := map[string]interface{}{
		"dynamic_daily_budget": map[string]interface{}{
			"effective_reset_at": now - 1,
			"weekly_reset_at":    now + int64(9*24*60*60),
		},
	}

	_, _, err := cliproxyCPAQuotaForceUnlockBoundary(health, now)

	require.Error(t, err)
}

func TestCliproxyCPAQuotaForceUnlockBoundaryRequiresCycleSignature(t *testing.T) {
	now := int64(1_800_000_000)
	health := map[string]interface{}{
		"dynamic_daily_budget": map[string]interface{}{
			"effective_reset_at": now + 3_600,
		},
	}

	_, _, err := cliproxyCPAQuotaForceUnlockBoundary(health, now)

	require.ErrorContains(t, err, "cycle signature is unavailable")
}

func TestCliproxyCPAQuotaForceUnlockTargetAcceptsCustomTimeWithinWindow(t *testing.T) {
	now := int64(1_800_000_000)
	requestedUntil := now + int64(3*time.Hour/time.Second)
	health := map[string]interface{}{
		"dynamic_daily_budget": map[string]interface{}{
			"planning_signature": "cycle-custom",
		},
	}

	until, signature, err := cliproxyCPAQuotaForceUnlockTarget(health, now, &requestedUntil)

	require.NoError(t, err)
	require.Equal(t, requestedUntil, until)
	require.Equal(t, "cycle-custom", signature)
}

func TestCliproxyCPAQuotaForceUnlockTargetRejectsCustomTimeOutsideWindow(t *testing.T) {
	now := int64(1_800_000_000)
	health := map[string]interface{}{
		"dynamic_daily_budget": map[string]interface{}{
			"planning_signature": "cycle-custom",
		},
	}

	tooSoon := now + int64(30*time.Second/time.Second)
	_, _, err := cliproxyCPAQuotaForceUnlockTarget(health, now, &tooSoon)
	require.ErrorContains(t, err, "at least 1 minute")

	tooLate := now + int64((cliproxyCPAQuotaForceUnlockMaxWindow+time.Second)/time.Second)
	_, _, err = cliproxyCPAQuotaForceUnlockTarget(health, now, &tooLate)
	require.ErrorContains(t, err, "more than 8 days")
}

func TestCliproxyCPAQuotaForceUnlockTargetRejectsPastCustomTime(t *testing.T) {
	now := int64(1_800_000_000)
	requestedUntil := now
	health := map[string]interface{}{
		"dynamic_daily_budget": map[string]interface{}{
			"planning_signature": "cycle-custom",
		},
	}

	_, _, err := cliproxyCPAQuotaForceUnlockTarget(health, now, &requestedUntil)
	require.ErrorContains(t, err, "must be in the future")
}

func TestCliproxyCPAQuotaForceUnlockEligibleRequiresSchedulableBalance(t *testing.T) {
	require.True(t, cliproxyCPAQuotaForceUnlockEligible(map[string]interface{}{
		"ok":                      true,
		"available_account_count": float64(1),
		"total_balance_units":     float64(11),
		"dynamic_daily_budget":    map[string]interface{}{"applied": true},
	}))
	require.False(t, cliproxyCPAQuotaForceUnlockEligible(map[string]interface{}{
		"ok":                      true,
		"available_account_count": float64(0),
		"total_balance_units":     float64(11),
		"dynamic_daily_budget":    map[string]interface{}{"applied": true},
	}))
	require.False(t, cliproxyCPAQuotaForceUnlockEligible(map[string]interface{}{
		"ok":                      false,
		"available_account_count": float64(1),
		"total_balance_units":     float64(11),
		"dynamic_daily_budget":    map[string]interface{}{"applied": true},
	}))
}

func TestForceUnlockCliproxyCPAQuotaGuardPersistsOverrideAndEnablesChannel(t *testing.T) {
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	db, err := gorm.Open(sqlite.Open("file:channel-quota-force-unlock?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Option{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	now := int64(1_800_000_000)
	otherInfo := common.MapToJsonStr(map[string]interface{}{
		"cliproxy_cpa_quota_guard": map[string]interface{}{
			"managed": true,
			"health": map[string]interface{}{
				"ok":                      true,
				"available_account_count": 1,
				"total_balance_units":     11,
				"dynamic_daily_budget": map[string]interface{}{
					"effective_reset_at": now + 3_600,
					"planning_signature": "cycle-a",
					"applied":            true,
				},
			},
		},
	})
	require.NoError(t, db.Create(&model.Channel{
		Id:        CliproxyCPAQuotaForceUnlockChannelID,
		Name:      "cliproxy-codex",
		Key:       "test-only",
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: otherInfo,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "cliproxy-codex", Model: "gpt-test", ChannelId: CliproxyCPAQuotaForceUnlockChannelID, Enabled: false,
	}).Error)

	result, err := ForceUnlockCliproxyCPAQuotaGuard(CliproxyCPAQuotaForceUnlockChannelID, 7, now)

	require.NoError(t, err)
	require.True(t, result.Active)
	require.Equal(t, now+3_600, result.Until)
	var channel model.Channel
	require.NoError(t, db.First(&channel, "id = ?", CliproxyCPAQuotaForceUnlockChannelID).Error)
	require.Equal(t, common.ChannelStatusEnabled, channel.Status)
	var ability model.Ability
	require.NoError(t, db.First(&ability, "channel_id = ?", CliproxyCPAQuotaForceUnlockChannelID).Error)
	require.True(t, ability.Enabled)
	var option model.Option
	require.NoError(t, db.First(&option, "key = ?", cliproxyCPAQuotaForceUnlockOptionKey).Error)
	require.Contains(t, option.Value, `"until":1800003600`)
	require.Contains(t, option.Value, `"cycle_signature":"cycle-a"`)

	cancelled, err := CancelCliproxyCPAQuotaGuardForceUnlock(CliproxyCPAQuotaForceUnlockChannelID, 7, now+30)
	require.NoError(t, err)
	require.False(t, cancelled.Active)
	require.NoError(t, db.First(&option, "key = ?", cliproxyCPAQuotaForceUnlockOptionKey).Error)
	require.Contains(t, option.Value, `"until":0`)
}

func TestForceUnlockCliproxyCPAQuotaGuardRejectsOtherChannel(t *testing.T) {
	_, err := ForceUnlockCliproxyCPAQuotaGuard(13, 7, 1_800_000_000)
	require.ErrorContains(t, err, "restricted to channel 12")
}
