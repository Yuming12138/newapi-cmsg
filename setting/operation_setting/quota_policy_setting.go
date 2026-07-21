package operation_setting

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
)

type QuotaPolicySetting struct {
	// ChargedGroups is a comma/space separated allowlist of groups that should
	// charge wallet/subscription quota. Other groups are metered only. It is
	// retained for backward compatibility when no user-aware policy is set.
	ChargedGroups string `json:"charged_groups"`
	// DefaultAction is applied when no user-aware rule matches. Supported
	// values are "charged" and "metered_only".
	DefaultAction string `json:"default_action"`
	// Rules are evaluated in order. The first valid matching rule wins.
	Rules []QuotaPolicyRule `json:"rules"`
}

type QuotaPolicyRule struct {
	UserGroups  []string `json:"user_groups"`
	UsingGroups []string `json:"using_groups"`
	Action      string   `json:"action"`
}

var quotaPolicySetting = QuotaPolicySetting{
	ChargedGroups: "asxs",
}

var (
	quotaPolicySettingUpdateMu sync.Mutex
	quotaPolicySnapshot        atomic.Pointer[QuotaPolicySetting]
)

type quotaPolicySettingConfig QuotaPolicySetting

func (s *QuotaPolicySetting) UpdateConfigFromMap(configMap map[string]string) error {
	quotaPolicySettingUpdateMu.Lock()
	defer quotaPolicySettingUpdateMu.Unlock()

	if err := config.UpdateConfigFromMap((*quotaPolicySettingConfig)(s), configMap); err != nil {
		return err
	}
	if s == &quotaPolicySetting {
		publishQuotaPolicySnapshot(s)
	}
	return nil
}

func publishQuotaPolicySnapshot(setting *QuotaPolicySetting) {
	snapshot := *setting
	snapshot.Rules = make([]QuotaPolicyRule, len(setting.Rules))
	for i, rule := range setting.Rules {
		snapshot.Rules[i] = rule
		snapshot.Rules[i].UserGroups = append([]string(nil), rule.UserGroups...)
		snapshot.Rules[i].UsingGroups = append([]string(nil), rule.UsingGroups...)
	}
	quotaPolicySnapshot.Store(&snapshot)
}

func init() {
	publishQuotaPolicySnapshot(&quotaPolicySetting)
	config.GlobalConfig.Register("quota_policy_setting", &quotaPolicySetting)
}

func GetQuotaPolicySetting() *QuotaPolicySetting {
	return quotaPolicySnapshot.Load()
}

func IsQuotaChargedGroup(group string) bool {
	group = strings.TrimSpace(group)
	if group == "" {
		return true
	}

	setting := GetQuotaPolicySetting()
	if setting == nil {
		return true
	}
	raw := strings.TrimSpace(setting.ChargedGroups)
	if raw == "" {
		return true
	}

	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	}) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		switch strings.ToLower(item) {
		case "*", "all":
			return true
		case "none":
			return false
		}
		if item == group {
			return true
		}
	}
	return false
}

// HasUserGroupQuotaPolicy reports whether the two-dimensional policy is
// configured. When false, callers should get the legacy ChargedGroups
// behavior through IsQuotaChargedForUser.
func HasUserGroupQuotaPolicy() bool {
	setting := GetQuotaPolicySetting()
	return setting != nil && (strings.TrimSpace(setting.DefaultAction) != "" || len(setting.Rules) > 0)
}

// IsQuotaChargedForUser determines whether wallet/subscription and token quota
// should be charged for a user-group/using-group pair. User-aware rules are
// opt-in so existing deployments keep their legacy ChargedGroups behavior.
func IsQuotaChargedForUser(userGroup, usingGroup string) bool {
	setting := GetQuotaPolicySetting()
	if setting == nil {
		return true
	}
	if strings.TrimSpace(setting.DefaultAction) == "" && len(setting.Rules) == 0 {
		return IsQuotaChargedGroup(usingGroup)
	}

	for _, rule := range setting.Rules {
		charged, valid := quotaPolicyActionCharged(rule.Action)
		if !valid {
			continue
		}
		if quotaPolicyUserGroupMatches(rule.UserGroups, userGroup) && quotaPolicyGroupMatches(rule.UsingGroups, usingGroup) {
			return charged
		}
	}

	if charged, valid := quotaPolicyActionCharged(setting.DefaultAction); valid {
		return charged
	}

	// Invalid or incomplete user-aware policies fail closed.
	return true
}

func quotaPolicyActionCharged(action string) (charged bool, valid bool) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "charged":
		return true, true
	case "metered_only":
		return false, true
	default:
		return true, false
	}
}

func quotaPolicyGroupMatches(configuredGroups []string, group string) bool {
	group = strings.TrimSpace(group)
	if group == "" || len(configuredGroups) == 0 {
		return false
	}

	for _, configuredGroup := range configuredGroups {
		configuredGroup = strings.TrimSpace(configuredGroup)
		if configuredGroup == "" {
			continue
		}
		switch strings.ToLower(configuredGroup) {
		case "*", "all":
			return true
		}
		if configuredGroup == group {
			return true
		}
	}
	return false
}

func quotaPolicyUserGroupMatches(configuredGroups []string, userGroup string) bool {
	for _, groupAlias := range setting.UserIdentityGroupAliases(userGroup) {
		if quotaPolicyGroupMatches(configuredGroups, groupAlias) {
			return true
		}
	}
	return false
}
