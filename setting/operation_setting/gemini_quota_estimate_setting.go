package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// Gemini subscription quota has no cash balance. Rates must be calibrated
// against observed token cost and quota consumption, not subscription price.
type GeminiQuotaEstimateSetting struct {
	USDPerPercent map[string]float64 `json:"usd_per_percent"`
}

var geminiQuotaEstimateSetting = GeminiQuotaEstimateSetting{
	// Calibrated on two g1-pro-tier accounts with gemini-3.8-flash-tiered
	// on 2026-10-14, using this site's mapped-model token prices. See
	// ops/unlocked_provider_quota_pool.md for measurements and limitations.
	USDPerPercent: map[string]float64{"g1-pro-tier": 0.079},
}

func init() {
	config.GlobalConfig.Register("gemini_quota_estimate_setting", &geminiQuotaEstimateSetting)
}

func GetGeminiQuotaEstimateSetting() *GeminiQuotaEstimateSetting {
	return &geminiQuotaEstimateSetting
}
