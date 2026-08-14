package operation_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type UserQuotaGuardApproval struct {
	ExtraUSD  float64 `json:"extra_usd"`
	Note      string  `json:"note,omitempty"`
	UpdatedAt int64   `json:"updated_at,omitempty"`
}

type UserQuotaGuardSetting struct {
	Enabled             bool                                         `json:"enabled"`
	TickIntervalMinutes int                                          `json:"tick_interval_minutes"`
	QuotaPerUSD         int                                          `json:"quota_per_usd"`
	Timezone            string                                       `json:"timezone"`
	RestrictedStart     string                                       `json:"restricted_start"`
	RestrictedEnd       string                                       `json:"restricted_end"`
	DaytimeBaseUSD      float64                                      `json:"daytime_base_usd"`
	UnlockedQuotaUSD    float64                                      `json:"unlocked_quota_usd"`
	UnlockedQuotaSource string                                       `json:"unlocked_quota_source"`
	AutoManage          bool                                         `json:"auto_manage"`
	IncludeRoles        []int                                        `json:"include_roles"`
	IncludeGroups       []string                                     `json:"include_groups"`
	IncludeUserIDs      []int                                        `json:"include_user_ids"`
	ExcludeUserIDs      []int                                        `json:"exclude_user_ids"`
	PerUserBaseUSD      map[string]float64                           `json:"per_user_base_usd"`
	PerUserExtraUSD     map[string]float64                           `json:"per_user_extra_usd"`
	DailyApprovals      map[string]map[string]UserQuotaGuardApproval `json:"daily_approvals"`
}

var userQuotaGuardSetting = UserQuotaGuardSetting{
	Enabled:             true,
	TickIntervalMinutes: 1,
	QuotaPerUSD:         500000,
	Timezone:            "Asia/Shanghai",
	RestrictedStart:     "09:00",
	RestrictedEnd:       "18:00",
	DaytimeBaseUSD:      50,
	UnlockedQuotaUSD:    405,
	UnlockedQuotaSource: "asxs_channel_pool",
	AutoManage:          true,
	IncludeRoles:        []int{common.RoleCommonUser},
	IncludeGroups:       []string{"asxs"},
	IncludeUserIDs:      []int{},
	ExcludeUserIDs:      []int{1},
	PerUserBaseUSD:      map[string]float64{},
	PerUserExtraUSD:     map[string]float64{},
	DailyApprovals:      map[string]map[string]UserQuotaGuardApproval{},
}

func init() {
	config.GlobalConfig.Register("user_quota_guard_setting", &userQuotaGuardSetting)
}

func GetUserQuotaGuardSetting() *UserQuotaGuardSetting {
	return &userQuotaGuardSetting
}
