package operation_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

type QuotaPolicySetting struct {
	// ChargedGroups is a comma/space separated allowlist of groups that should
	// charge wallet/subscription quota. Other groups are metered only.
	ChargedGroups string `json:"charged_groups"`
}

var quotaPolicySetting = QuotaPolicySetting{
	ChargedGroups: "asxs",
}

func init() {
	config.GlobalConfig.Register("quota_policy_setting", &quotaPolicySetting)
}

func GetQuotaPolicySetting() *QuotaPolicySetting {
	return &quotaPolicySetting
}

func IsQuotaChargedGroup(group string) bool {
	group = strings.TrimSpace(group)
	if group == "" {
		return true
	}

	raw := strings.TrimSpace(quotaPolicySetting.ChargedGroups)
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
