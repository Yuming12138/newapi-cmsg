package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type MimoCreditNightDiscountSetting struct {
	Enabled    bool    `json:"enabled"`
	Start      string  `json:"start"`
	End        string  `json:"end"`
	Multiplier float64 `json:"multiplier"`
}

type MimoCreditUsageSetting struct {
	IncludeCacheReadTokens     bool    `json:"include_cache_read_tokens"`
	CacheReadMultiplier        float64 `json:"cache_read_multiplier"`
	IncludeCacheCreationTokens bool    `json:"include_cache_creation_tokens"`
	CacheCreationMultiplier    float64 `json:"cache_creation_multiplier"`
}

type MimoCreditDisplaySetting struct {
	UpdateChannel bool   `json:"update_channel"`
	BalanceUnit   string `json:"balance_unit"`
}

type MimoCreditSetting struct {
	Enabled                bool                           `json:"enabled"`
	TickIntervalMinutes    int                            `json:"tick_interval_minutes"`
	ChannelID              int                            `json:"channel_id"`
	BaselineLogID          int                            `json:"baseline_log_id"`
	PlanTotalCredits       int64                          `json:"plan_total_credits"`
	InitialUsedCredits     int64                          `json:"initial_used_credits"`
	ExpiresAt              string                         `json:"expires_at"`
	Timezone               string                         `json:"timezone"`
	DefaultModelCreditRate float64                        `json:"default_model_credit_rate"`
	ModelCreditRates       map[string]float64             `json:"model_credit_rates"`
	NightDiscount          MimoCreditNightDiscountSetting `json:"night_discount"`
	Usage                  MimoCreditUsageSetting         `json:"usage"`
	Display                MimoCreditDisplaySetting       `json:"display"`
}

var mimoCreditSetting = MimoCreditSetting{
	Enabled:                true,
	TickIntervalMinutes:    5,
	ChannelID:              3,
	BaselineLogID:          25,
	PlanTotalCredits:       700000000,
	InitialUsedCredits:     26421349,
	ExpiresAt:              "2026-05-30",
	Timezone:               "Asia/Shanghai",
	DefaultModelCreditRate: 1,
	ModelCreditRates: map[string]float64{
		"mimo-v2-pro":               2,
		"mimo-v2-tts":               0,
		"mimo-v2.5":                 1,
		"mimo-v2.5-pro":             2,
		"mimo-v2.5-tts":             0,
		"mimo-v2.5-tts-voiceclone":  0,
		"mimo-v2.5-tts-voicedesign": 0,
		"mimo-v2-omni":              1,
	},
	NightDiscount: MimoCreditNightDiscountSetting{
		Enabled:    true,
		Start:      "00:00",
		End:        "08:00",
		Multiplier: 0.8,
	},
	Usage: MimoCreditUsageSetting{
		IncludeCacheReadTokens:     true,
		CacheReadMultiplier:        1,
		IncludeCacheCreationTokens: true,
		CacheCreationMultiplier:    1,
	},
	Display: MimoCreditDisplaySetting{
		UpdateChannel: true,
		BalanceUnit:   "credits",
	},
}

func init() {
	config.GlobalConfig.Register("mimo_credit_setting", &mimoCreditSetting)
}

func GetMimoCreditSetting() *MimoCreditSetting {
	return &mimoCreditSetting
}
