package model

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// IsChannelAutoDisabledByBudgetGuard reports whether an auto-disabled channel
// carries the explicit metadata written by the channel budget guard.  Other
// auto-disabled states (upstream failures, exhausted keys, etc.) remain
// unavailable to administrator requests.
func IsChannelAutoDisabledByBudgetGuard(channel *Channel) bool {
	if channel == nil || channel.Status != common.ChannelStatusAutoDisabled {
		return false
	}
	otherInfo := map[string]interface{}{}
	if strings.TrimSpace(channel.OtherInfo) != "" {
		if err := common.UnmarshalJsonStr(channel.OtherInfo, &otherInfo); err != nil {
			return false
		}
	}
	// A status reason written by the ordinary channel status path is the
	// freshest owner of the disabled state.  A non-budget reason (for example
	// an upstream authentication failure or a manual key disable) must never be
	// overridden by stale quota metadata.  The CPA guard normally has no
	// top-level status_reason, so an absent value is intentionally allowed below.
	reason, reasonOK := otherInfo["status_reason"].(string)
	if reasonOK && strings.TrimSpace(reason) != "" && !isChannelBudgetExhaustedReason(reason) {
		// A later status writer (manual action, upstream authentication failure,
		// or exhausted keys) owns the current reason.  Its explicit reason must
		// win over any stale budget metadata left in the JSON object.
		return false
	}

	if budgetInfo, ok := otherInfo["budget_guard"].(map[string]interface{}); ok {
		if isLegacyBudgetGuardMarker(otherInfo, budgetInfo) {
			return true
		}
	}

	// The CPA quota guard stores its authoritative result under a different
	// namespace.  Keep this path separate from the legacy marker so an
	// unrelated auto-disabled channel can never be admitted merely because it
	// happens to contain a quota_source object.
	return isCliproxyCPABudgetGuardMarker(otherInfo)
}

func isLegacyBudgetGuardMarker(otherInfo, budgetInfo map[string]interface{}) bool {
	if otherInfo == nil || budgetInfo == nil {
		return false
	}
	reason, reasonOK := otherInfo["status_reason"].(string)
	if !reasonOK || !isChannelBudgetExhaustedReason(reason) {
		return false
	}
	// Require the positive transition marker.  A false marker means the
	// budget guard observed the channel but did not disable it.  Older guard
	// versions wrote the transition reason before persisting this flag, so a
	// legacy false marker is accepted only when the nested reason is also the
	// explicit exhaustion value; unrelated auto-disabled states remain denied.
	disabled, hasDisabled := budgetInfo["disabled_by_guard"].(bool)
	if !hasDisabled || !disabled {
		nestedReason, reasonOK := budgetInfo["reason"].(string)
		if !reasonOK || strings.ToLower(strings.TrimSpace(nestedReason)) != "budget_exhausted" {
			return false
		}
	}
	// If a later status update changed status_time after the guard metadata was
	// refreshed, the budget marker is stale even if the old reason survived.
	if statusTime, statusOK := channelInfoInt64(otherInfo["status_time"]); statusOK {
		if updatedAt, updatedOK := channelInfoInt64(budgetInfo["updated_at"]); updatedOK && statusTime > updatedAt {
			return false
		}
	}
	return true
}

// isCliproxyCPABudgetGuardMarker recognizes the metadata emitted by
// ops/cliproxy_cpa_quota_guard.py.  Its result is deliberately conservative:
// only a successful probe that explicitly reports quota exhaustion is allowed.
// Probe/authentication failures, disabled credentials, and stale observations
// remain unavailable even for administrators.
func isCliproxyCPABudgetGuardMarker(otherInfo map[string]interface{}) bool {
	guardInfo, ok := channelInfoMap(otherInfo["cliproxy_cpa_quota_guard"])
	if !ok || !channelInfoBoolValue(guardInfo, "managed", false) {
		return false
	}
	if desiredEnabled, exists := channelInfoBool(guardInfo, "desired_enabled"); !exists || desiredEnabled {
		return false
	}
	if statusTime, statusOK := channelInfoInt64(otherInfo["status_time"]); statusOK {
		if updatedAt, updatedOK := channelInfoInt64(guardInfo["updated_at"]); updatedOK && statusTime > updatedAt {
			return false
		}
	}
	if stale, exists := channelInfoBool(guardInfo, "quota_observation_stale"); exists && stale {
		return false
	}

	health, ok := channelInfoMap(guardInfo["health"])
	if !ok {
		return false
	}
	if healthy, exists := channelInfoBool(health, "ok"); !exists || !healthy {
		return false
	}
	if failClosed, exists := channelInfoBool(health, "fail_closed"); exists && failClosed {
		return false
	}
	quotaOK, exists := channelInfoBool(health, "quota_ok")
	if !exists {
		quotaOK, exists = channelInfoBool(health, "within_share")
	}
	if !exists || quotaOK {
		return false
	}

	// If the probe returned account details, there must be at least one
	// credential that is not explicitly disabled/unavailable.  This prevents
	// an all-auth-disabled result (which otherwise looks like zero quota) from
	// being mistaken for a budget exhaustion.
	if accounts, exists := channelInfoSlice(health["accounts"]); exists && len(accounts) > 0 {
		activeCredential := false
		for _, rawAccount := range accounts {
			account, accountOK := channelInfoMap(rawAccount)
			if !accountOK {
				continue
			}
			if boolValue, exists := channelInfoBool(account, "disabled"); exists && boolValue {
				continue
			}
			if boolValue, exists := channelInfoBool(account, "unavailable"); exists && boolValue {
				continue
			}
			accountReason, _ := account["reason"].(string)
			if isCliproxyCPAAuthReason(accountReason) {
				continue
			}
			activeCredential = true
			break
		}
		if !activeCredential {
			return false
		}
	}

	healthReason, _ := health["reason"].(string)
	quotaSource, _ := channelInfoMap(otherInfo["quota_source"])
	quotaSourceReason, _ := quotaSource["status_reason"].(string)
	quotaSourceStatus, _ := quotaSource["status"].(string)
	block, _ := channelInfoMap(health["quota_block"])
	if isCliproxyCPABudgetReason(healthReason) ||
		isCliproxyCPABudgetReason(quotaSourceReason) ||
		isCliproxyCPABudgetStatus(quotaSourceStatus) ||
		isCliproxyCPABudgetBlock(block) {
		return true
	}
	return false
}

func channelInfoMap(value interface{}) (map[string]interface{}, bool) {
	mapValue, ok := value.(map[string]interface{})
	return mapValue, ok && mapValue != nil
}

func channelInfoSlice(value interface{}) ([]interface{}, bool) {
	slice, ok := value.([]interface{})
	return slice, ok
}

func channelInfoBool(values map[string]interface{}, key string) (bool, bool) {
	if values == nil {
		return false, false
	}
	value, exists := values[key]
	if !exists {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	case float64:
		return typed != 0, true
	case float32:
		return typed != 0, true
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	default:
		return false, false
	}
}

func channelInfoBoolValue(values map[string]interface{}, key string, fallback bool) bool {
	value, ok := channelInfoBool(values, key)
	if !ok {
		return fallback
	}
	return value
}

func isCliproxyCPAAuthReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return reason == "auth_disabled" || reason == "auth_unavailable" || reason == "management_auth_failure"
}

func isCliproxyCPABudgetReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch reason {
	case "quota_low_watermark_reached",
		"quota_7d_exhausted",
		"quota_5h_exhausted",
		"quota_feature_exhausted",
		"quota_feature_low_watermark",
		"dynamic_daily_budget_exhausted",
		"protected_reserve_reached":
		return true
	default:
		return false
	}
}

func isCliproxyCPABudgetStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "quota_exhausted" || status == "quota_7d_exhausted" || status == "quota_5h_exhausted"
}

func isCliproxyCPABudgetBlock(block map[string]interface{}) bool {
	if block == nil {
		return false
	}
	kind, _ := block["kind"].(string)
	code, _ := block["code"].(string)
	kind = strings.ToLower(strings.TrimSpace(kind))
	code = strings.ToLower(strings.TrimSpace(code))
	if kind == "daily_protected_budget" || kind == "protected_reserve" {
		return true
	}
	return strings.HasPrefix(code, "channel_") && strings.Contains(code, "budget")
}

func channelInfoInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// isChannelBudgetExhaustedReason accepts only the status marker emitted when
// the budget guard itself disables a channel.  A substring check is unsafe:
// older budget metadata can remain beside a later upstream/manual failure.
func isChannelBudgetExhaustedReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return strings.HasPrefix(reason, "channel_budget_exhausted:")
}

// IsChannelAvailableForAdminGroupModel reports whether a channel has an
// explicitly configured ability for a group/model pair and may therefore be
// used by an administrator.  Normal enabled channels still require an
// enabled ability; a channel auto-disabled by the budget guard is the one
// deliberate exception, because the guard's false ability flag is what keeps
// ordinary traffic away.  Manual/upstream/unknown disabled states are never
// admitted.
func IsChannelAvailableForAdminGroupModel(channel *Channel, group string, modelName string) bool {
	if channel == nil || strings.TrimSpace(group) == "" || strings.TrimSpace(modelName) == "" {
		return false
	}
	if channel.Status != common.ChannelStatusEnabled && !IsChannelAutoDisabledByBudgetGuard(channel) {
		return false
	}
	if DB == nil {
		return false
	}

	models := []string{modelName}
	if normalized := ratio_setting.FormatMatchingModelName(modelName); normalized != "" && normalized != modelName {
		models = append(models, normalized)
	}
	query := DB.Model(&Ability{}).Where(commonGroupCol+" = ? AND channel_id = ? AND model IN ?", group, channel.Id, models)
	if channel.Status == common.ChannelStatusEnabled {
		query = query.Where("enabled = ?", true)
	}
	var count int64
	return query.Count(&count).Error == nil && count > 0
}

// GetRandomSatisfiedChannelForRequestPathAdmin selects a channel for an
// administrator request.  It is intentionally separate from the normal
// enabled-channel cache: budget protection disables an ability in that cache,
// while an administrator may still use the configured channel for measurement.
// Manual disables, unrelated auto-disables, temporary scheduler blocks,
// request-path incompatibility, and missing abilities are still respected.
func GetRandomSatisfiedChannelForRequestPathAdmin(group string, modelName string, retry int, requestPath string, excludedIDs ...map[int]struct{}) (*Channel, error) {
	if DB == nil || strings.TrimSpace(group) == "" || strings.TrimSpace(modelName) == "" {
		return nil, nil
	}

	models := []string{modelName}
	if normalized := ratio_setting.FormatMatchingModelName(modelName); normalized != "" && normalized != modelName {
		models = append(models, normalized)
	}
	var abilities []Ability
	if err := DB.Where(commonGroupCol+" = ? AND model IN ?", group, models).
		Order("priority DESC").Order("weight DESC").Find(&abilities).Error; err != nil {
		return nil, err
	}
	if len(abilities) == 0 {
		return nil, nil
	}

	channelIDs := make([]int, 0, len(abilities))
	seenIDs := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, seen := seenIDs[ability.ChannelId]; seen {
			continue
		}
		seenIDs[ability.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	channels, err := GetChannelsByIds(channelIDs)
	if err != nil {
		return nil, err
	}
	channelByID := make(map[int]*Channel, len(channels))
	for _, channel := range channels {
		if channel != nil {
			channelByID[channel.Id] = channel
		}
	}
	excluded := normalizeExcludedChannelIDs(excludedIDs)

	// A channel can have both an exact and a normalized ability row.  Keep the
	// highest-priority eligible row and do not present duplicates to the runtime
	// scorer.
	type candidate struct {
		channel  *Channel
		priority int64
	}
	candidatesByID := make(map[int]candidate, len(abilities))
	for _, ability := range abilities {
		channel := channelByID[ability.ChannelId]
		if channel == nil {
			continue
		}
		if _, blocked := excluded[channel.Id]; blocked {
			continue
		}
		if blocked, _ := IsChannelTemporarilyUnschedulable(channel.Id); blocked {
			continue
		}
		if !channelSupportsRequestPath(channel, requestPath) {
			continue
		}

		budgetAutoDisabled := IsChannelAutoDisabledByBudgetGuard(channel)
		switch channel.Status {
		case common.ChannelStatusEnabled:
			if !ability.Enabled {
				continue
			}
		case common.ChannelStatusAutoDisabled:
			if !budgetAutoDisabled {
				continue
			}
		case common.ChannelStatusManuallyDisabled:
			continue
		default:
			continue
		}

		priority := channel.GetPriority()
		if ability.Priority != nil {
			priority = *ability.Priority
		}
		if previous, exists := candidatesByID[channel.Id]; !exists || priority > previous.priority {
			candidatesByID[channel.Id] = candidate{channel: channel, priority: priority}
		}
	}
	if len(candidatesByID) == 0 {
		return nil, nil
	}

	prioritiesSet := make(map[int64]struct{}, len(candidatesByID))
	for _, item := range candidatesByID {
		prioritiesSet[item.priority] = struct{}{}
	}
	priorities := make([]int64, 0, len(prioritiesSet))
	for priority := range prioritiesSet {
		priorities = append(priorities, priority)
	}
	// The ability query is ordered, but deduplication above can change the
	// insertion order; sort explicitly to preserve normal retry semantics.
	for i := 0; i < len(priorities); i++ {
		for j := i + 1; j < len(priorities); j++ {
			if priorities[j] > priorities[i] {
				priorities[i], priorities[j] = priorities[j], priorities[i]
			}
		}
	}
	if retry < 0 {
		retry = 0
	}
	if retry >= len(priorities) {
		retry = len(priorities) - 1
	}
	for priorityIndex := retry; priorityIndex < len(priorities); priorityIndex++ {
		target := priorities[priorityIndex]
		pool := make([]*Channel, 0)
		for _, item := range candidatesByID {
			if item.priority == target {
				pool = append(pool, item.channel)
			}
		}
		if selected := selectChannelByRuntimeScore(pool); selected != nil {
			return selected, nil
		}
	}
	return nil, nil
}
