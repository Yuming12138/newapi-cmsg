package service

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type SharedFallbackQuotaState int

const (
	SharedFallbackQuotaUnknown SharedFallbackQuotaState = iota
	SharedFallbackQuotaSpendable
	SharedFallbackQuotaExhausted
)

type ModelGroupRoute struct {
	PreferredGroup string
	FallbackGroup  string
}

func ResolveModelGroupRoute(userGroup, sourceGroup, modelName string) (ModelGroupRoute, bool) {
	cfg := operation_setting.GetModelGroupRouteSetting()
	if cfg == nil || !cfg.Enabled {
		return ModelGroupRoute{}, false
	}

	normalizedUserGroup := setting.NormalizeUserIdentityGroup(userGroup)
	if !containsFold(cfg.UserGroups, normalizedUserGroup) || !containsFold(cfg.SourceGroups, sourceGroup) {
		return ModelGroupRoute{}, false
	}

	modelName = strings.TrimSuffix(strings.TrimSpace(modelName), ratio_setting.CompactModelSuffix)
	if !hasPrefixFold(cfg.ModelPrefixes, modelName) {
		return ModelGroupRoute{}, false
	}

	preferredGroup := strings.TrimSpace(cfg.PreferredGroup)
	fallbackGroup := strings.TrimSpace(cfg.FallbackGroup)
	if preferredGroup == "" || fallbackGroup == "" || strings.EqualFold(preferredGroup, fallbackGroup) {
		return ModelGroupRoute{}, false
	}
	return ModelGroupRoute{PreferredGroup: preferredGroup, FallbackGroup: fallbackGroup}, true
}

// GetSharedFallbackQuotaState reads the configured quota source from the
// channel cache. Unknown or stale data deliberately leaves the fallback path
// enabled; only an authoritative exhausted state may bypass it.
func GetSharedFallbackQuotaState(now time.Time) SharedFallbackQuotaState {
	cfg := operation_setting.GetModelGroupRouteSetting()
	if cfg == nil || cfg.FallbackQuotaSourceChannelID <= 0 {
		return SharedFallbackQuotaUnknown
	}

	channel, err := model.CacheGetChannel(cfg.FallbackQuotaSourceChannelID)
	if err != nil || channel == nil {
		return SharedFallbackQuotaUnknown
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	quotaSource, ok := otherInfo[channelQuotaSourceInfoKey].(map[string]interface{})
	if !ok {
		return SharedFallbackQuotaUnknown
	}

	updatedAt, ok := guardObjectInt64(quotaSource, "updated_at")
	if !ok || updatedAt <= 0 {
		return SharedFallbackQuotaUnknown
	}
	maxAge := cfg.FallbackQuotaSourceMaxAgeSeconds
	age := now.Unix() - updatedAt
	if maxAge > 0 && (age > maxAge || age < -maxAge) {
		return SharedFallbackQuotaUnknown
	}

	status, ok := quotaSource["status"].(string)
	if !ok {
		return SharedFallbackQuotaUnknown
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "unknown" || status == "error" || status == "unavailable" {
		return SharedFallbackQuotaUnknown
	}
	spendable, hasSpendable := guardObjectBool(quotaSource, "spendable")
	balance, hasBalance := guardObjectFloat(quotaSource, "balance")
	if !hasSpendable || !hasBalance {
		return SharedFallbackQuotaUnknown
	}
	if status == "quota_exhausted" || !spendable || balance <= 0 {
		return SharedFallbackQuotaExhausted
	}
	if status == "available" && spendable && balance > 0 {
		return SharedFallbackQuotaSpendable
	}
	return SharedFallbackQuotaUnknown
}

// GetModelGroupRouteModels returns preferred-group models that can be routed from sourceGroup.
func GetModelGroupRouteModels(userGroup, sourceGroup string) []string {
	cfg := operation_setting.GetModelGroupRouteSetting()
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	preferredGroup := strings.TrimSpace(cfg.PreferredGroup)
	if preferredGroup == "" {
		return nil
	}

	routedModels := make([]string, 0)
	seen := make(map[string]struct{})
	for _, modelName := range model.GetGroupEnabledModels(preferredGroup) {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		route, ok := ResolveModelGroupRoute(userGroup, sourceGroup, modelName)
		if !ok || !strings.EqualFold(route.PreferredGroup, preferredGroup) {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		routedModels = append(routedModels, modelName)
	}
	return routedModels
}

func containsFold(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func hasPrefixFold(prefixes []string, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range prefixes {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix != "" && strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
