package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

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
