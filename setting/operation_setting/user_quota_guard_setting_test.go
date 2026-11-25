package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestUserQuotaGuardSettingPerUserBaseRoundTrip(t *testing.T) {
	var loaded UserQuotaGuardSetting
	manager := config.NewConfigManager()
	manager.Register("user_quota_guard_setting", &loaded)
	require.NoError(t, manager.LoadFromDB(map[string]string{
		"user_quota_guard_setting.daytime_base_usd":           "40",
		"user_quota_guard_setting.restricted_balance_cap_usd": "40",
		"user_quota_guard_setting.per_user_base_usd":          `{"3":10,"13":10}`,
	}))

	require.Equal(t, float64(40), loaded.DaytimeBaseUSD)
	require.Equal(t, float64(40), loaded.RestrictedBalanceCapUSD)
	require.Equal(t, map[string]float64{"3": 10, "13": 10}, loaded.PerUserBaseUSD)
}

func TestUserQuotaGuardSettingFixedDailyReload(t *testing.T) {
	cfg := UserQuotaGuardSetting{Enabled: true}
	manager := config.NewConfigManager()
	manager.Register("user_quota_guard_setting", &cfg)
	const key = "user_quota_guard_setting.per_user_fixed_daily_usd"
	require.NoError(t, manager.LoadFromDB(map[string]string{key: `{"13":10,"14":25}`}))
	amount, fixed := cfg.FixedDailyUSD(13)
	require.True(t, fixed)
	require.Equal(t, 10.0, amount)
	require.NoError(t, manager.LoadFromDB(map[string]string{key: `{"14":25}`}))
	_, fixed = cfg.FixedDailyUSD(13)
	require.False(t, fixed)
}
