package service

import (
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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
