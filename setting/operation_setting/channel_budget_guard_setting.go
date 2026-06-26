package operation_setting

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/config"
)

type ChannelBudgetGuardChannelSetting struct {
	ID                 int     `json:"id"`
	Name               string  `json:"name"`
	Mode               string  `json:"mode"`
	Source             string  `json:"source"`
	UsageURL           string  `json:"usage_url"`
	UsageTimeoutSecond int     `json:"usage_timeout_sec"`
	LimitUSD           float64 `json:"limit_usd"`
	Enabled            bool    `json:"enabled"`
}

type ChannelBudgetGuardASXSDiscoverySetting struct {
	Enabled                   bool    `json:"enabled"`
	ChannelType               int     `json:"channel_type"`
	Group                     string  `json:"group"`
	BaseURL                   string  `json:"base_url"`
	Mode                      string  `json:"mode"`
	Source                    string  `json:"source"`
	UsageURL                  string  `json:"usage_url"`
	DefaultLimitUSD           float64 `json:"default_limit_usd"`
	BalanceFallbackChannelIDs []int   `json:"balance_fallback_channel_ids"`
}

type ChannelBudgetGuardAutoDiscoverySetting struct {
	Enabled bool                                   `json:"enabled"`
	ASXS    ChannelBudgetGuardASXSDiscoverySetting `json:"asxs"`
}

type ChannelBudgetGuardSetting struct {
	Enabled             bool                                   `json:"enabled"`
	TickIntervalMinutes int                                    `json:"tick_interval_minutes"`
	QuotaPerUSD         int                                    `json:"quota_per_usd"`
	Timezone            string                                 `json:"timezone"`
	UsageTimeoutSecond  int                                    `json:"usage_timeout_sec"`
	AutoDiscovery       ChannelBudgetGuardAutoDiscoverySetting `json:"auto_discovery"`
	Channels            []ChannelBudgetGuardChannelSetting     `json:"channels"`
}

var channelBudgetGuardSetting = ChannelBudgetGuardSetting{
	Enabled:             true,
	TickIntervalMinutes: 1,
	QuotaPerUSD:         500000,
	Timezone:            "Asia/Shanghai",
	UsageTimeoutSecond:  20,
	AutoDiscovery: ChannelBudgetGuardAutoDiscoverySetting{
		Enabled: true,
		ASXS: ChannelBudgetGuardASXSDiscoverySetting{
			Enabled:         true,
			ChannelType:     constant.ChannelTypeOpenAI,
			Group:           "asxs",
			BaseURL:         "https://api.asxs.top",
			Mode:            "daily",
			Source:          "asxs_usage",
			UsageURL:        "https://api.asxs.top/api/usage",
			DefaultLimitUSD: 1,
		},
	},
	Channels: []ChannelBudgetGuardChannelSetting{},
}

func init() {
	config.GlobalConfig.Register("channel_budget_guard_setting", &channelBudgetGuardSetting)
}

func GetChannelBudgetGuardSetting() *ChannelBudgetGuardSetting {
	return &channelBudgetGuardSetting
}
