package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestGeminiQuotaEstimateSettingRoundTrip(t *testing.T) {
	var loaded GeminiQuotaEstimateSetting
	manager := config.NewConfigManager()
	manager.Register("gemini_quota_estimate_setting", &loaded)
	require.NoError(t, manager.LoadFromDB(map[string]string{
		"gemini_quota_estimate_setting.usd_per_percent": `{"g1-pro-tier":0.079}`,
	}))
	require.Equal(t, map[string]float64{"g1-pro-tier": 0.079}, loaded.USDPerPercent)
}
