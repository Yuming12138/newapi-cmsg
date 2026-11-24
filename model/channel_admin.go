package model

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// IsChannelAutoDisabledByBudgetGuard reports whether an auto-disabled channel
// carries the explicit metadata written by the channel budget guard.
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
