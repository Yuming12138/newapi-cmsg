package management

import (
	"net/http"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	authScheduleStateAvailable         = "available"
	authScheduleStateQuota5hExhausted  = "quota_5h_exhausted"
	authScheduleStateQuota7dExhausted  = "quota_7d_exhausted"
	authScheduleStateAuthInvalid       = "auth_invalid"
	authScheduleStateCooldown          = "cooldown"
	authScheduleStateProtectedReserve  = "protected_reserve"
	authScheduleStateManualDisabled    = "manual_disabled"
	authScheduleStateUnknown           = "unknown"
	authScheduleReasonManualDisabled   = "manual_disabled"
	authScheduleReasonAuthInvalid      = "auth_invalid"
	authScheduleReasonCooldown         = "cooldown"
	authScheduleReasonProtectedReserve = "protected_reserve"
	authScheduleReasonQuota5hExhausted = "quota_5h_exhausted"
	authScheduleReasonQuota7dExhausted = "quota_7d_exhausted"
	authScheduleReasonUnavailable      = "unavailable"
	authScheduleReasonStatusError      = "status_error"
	authScheduleReasonStatusUnknown    = "status_unknown"
	authScheduleReasonAvailable        = "available"
)

type authScheduleStateInfo struct {
	State       string
	Reason      string
	Retryable   bool
	ResetAt     time.Time
	LastError   *coreauth.Error
	Schedulable bool
}

func deriveAuthScheduleState(auth *coreauth.Auth, now time.Time) authScheduleStateInfo {
	if now.IsZero() {
		now = time.Now()
	}
	if auth == nil {
		return authScheduleStateInfo{State: authScheduleStateUnknown, Reason: authScheduleReasonStatusUnknown}
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return authScheduleStateInfo{State: authScheduleStateManualDisabled, Reason: authScheduleReasonManualDisabled, LastError: auth.LastError}
	}
	if isAuthInvalidError(auth.LastError) {
		return authScheduleStateInfo{State: authScheduleStateAuthInvalid, Reason: authScheduleReasonAuthInvalid, LastError: auth.LastError}
	}
	if state, ok := quotaScheduleState(auth.Quota, auth.NextRetryAfter, auth.LastError, now); ok {
		return state
	}
	if auth.NextRetryAfter.After(now) {
		return authScheduleStateInfo{
			State:       authScheduleStateCooldown,
			Reason:      authScheduleReasonCooldown,
			Retryable:   true,
			ResetAt:     auth.NextRetryAfter,
			LastError:   auth.LastError,
			Schedulable: false,
		}
	}
	if state, ok := modelScheduleState(auth.ModelStates, now); ok {
		return state
	}
	if auth.Unavailable {
		return authScheduleStateInfo{State: authScheduleStateUnknown, Reason: authScheduleReasonUnavailable, LastError: auth.LastError}
	}
	if auth.Status == coreauth.StatusUnknown || auth.Status == "" {
		return authScheduleStateInfo{State: authScheduleStateUnknown, Reason: authScheduleReasonStatusUnknown, LastError: auth.LastError}
	}
	return authScheduleStateInfo{
		State:       authScheduleStateAvailable,
		Reason:      authScheduleReasonAvailable,
		Retryable:   false,
		LastError:   auth.LastError,
		Schedulable: true,
	}
}

func quotaScheduleState(quota coreauth.QuotaState, fallbackReset time.Time, lastError *coreauth.Error, now time.Time) (authScheduleStateInfo, bool) {
	if !quota.Exceeded && strings.TrimSpace(quota.Reason) == "" {
		return authScheduleStateInfo{}, false
	}
	state, reason := classifyQuotaScheduleReason(quota.Reason)
	resetAt := quota.NextRecoverAt
	if resetAt.IsZero() {
		resetAt = fallbackReset
	}
	return authScheduleStateInfo{
		State:       state,
		Reason:      reason,
		Retryable:   !resetAt.IsZero() && resetAt.After(now),
		ResetAt:     resetAt,
		LastError:   lastError,
		Schedulable: false,
	}, true
}

func modelScheduleState(states map[string]*coreauth.ModelState, now time.Time) (authScheduleStateInfo, bool) {
	for _, state := range states {
		if state == nil {
			continue
		}
		if isAuthInvalidError(state.LastError) {
			return authScheduleStateInfo{State: authScheduleStateAuthInvalid, Reason: authScheduleReasonAuthInvalid, LastError: state.LastError}, true
		}
		if result, ok := quotaScheduleState(state.Quota, state.NextRetryAfter, state.LastError, now); ok {
			return result, true
		}
		if state.Unavailable && state.NextRetryAfter.After(now) {
			return authScheduleStateInfo{
				State:       authScheduleStateCooldown,
				Reason:      authScheduleReasonCooldown,
				Retryable:   true,
				ResetAt:     state.NextRetryAfter,
				LastError:   state.LastError,
				Schedulable: false,
			}, true
		}
	}
	return authScheduleStateInfo{}, false
}

func classifyQuotaScheduleReason(reason string) (string, string) {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(normalized, "protected") || strings.Contains(normalized, "reserve"):
		return authScheduleStateProtectedReserve, authScheduleReasonProtectedReserve
	case strings.Contains(normalized, "7d") || strings.Contains(normalized, "week") || strings.Contains(normalized, "weekly") || strings.Contains(normalized, "周"):
		return authScheduleStateQuota7dExhausted, authScheduleReasonQuota7dExhausted
	case strings.Contains(normalized, "5h") || strings.Contains(normalized, "5 h") || strings.Contains(normalized, "five"):
		return authScheduleStateQuota5hExhausted, authScheduleReasonQuota5hExhausted
	default:
		return authScheduleStateCooldown, authScheduleReasonCooldown
	}
}

func isAuthInvalidError(err *coreauth.Error) bool {
	if err == nil {
		return false
	}
	if err.HTTPStatus == http.StatusUnauthorized || err.HTTPStatus == http.StatusForbidden {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(err.Message + " " + err.Code))
	return strings.Contains(message, "unauthorized") ||
		strings.Contains(message, "forbidden") ||
		strings.Contains(message, "invalid_grant") ||
		strings.Contains(message, "invalid token") ||
		strings.Contains(message, "invalid auth")
}
