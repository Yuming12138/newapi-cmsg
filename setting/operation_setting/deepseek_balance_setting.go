package operation_setting

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/config"
)

type DeepSeekBalanceSetting struct {
	Enabled             bool   `json:"enabled"`
	TickIntervalMinutes int    `json:"tick_interval_minutes"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	ChannelType         int    `json:"channel_type"`
	BaseURL             string `json:"base_url"`
	Group               string `json:"group"`
	BalanceURL          string `json:"balance_url"`
}

var deepSeekBalanceSetting = DeepSeekBalanceSetting{
	Enabled:             true,
	TickIntervalMinutes: 5,
	TimeoutSeconds:      15,
	ChannelType:         constant.ChannelTypeDeepSeek,
	BaseURL:             "https://api.deepseek.com",
	Group:               "deepseek",
	BalanceURL:          "https://api.deepseek.com/user/balance",
}

func init() {
	config.GlobalConfig.Register("deepseek_balance_setting", &deepSeekBalanceSetting)
}

func GetDeepSeekBalanceSetting() *DeepSeekBalanceSetting {
	return &deepSeekBalanceSetting
}
