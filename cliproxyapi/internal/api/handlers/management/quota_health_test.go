package management

import (
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestEvaluateQuotaHealthAccountExposesRuntimeQuotaState(t *testing.T) {
	next := time.Now().Add(6 * 24 * time.Hour).Truncate(time.Second)
	auth := &coreauth.Auth{
		Provider:    "codex",
		Status:      coreauth.StatusError,
		Unavailable: true,
		Attributes:  map[string]string{"plan_type": "plus"},
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "quota",
			NextRecoverAt: next,
		},
		NextRetryAfter: next,
	}

	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), auth, quotaHealthTestWeeklyOnlyUsage(0), nil)
	if err != nil {
		t.Fatalf("evaluateQuotaHealthAccount() error = %v", err)
	}
	if account["runtime_quota_exceeded"] != true {
		t.Fatalf("runtime_quota_exceeded = %#v, want true", account["runtime_quota_exceeded"])
	}
	if account["runtime_unavailable"] != true || account["runtime_schedulable"] != false {
		t.Fatalf("runtime availability = %#v", account)
	}
	if account["runtime_reason"] != authScheduleReasonCooldown {
		t.Fatalf("runtime_reason = %#v, want %q", account["runtime_reason"], authScheduleReasonCooldown)
	}
	if account["runtime_reset_at"] != next.Unix() {
		t.Fatalf("runtime_reset_at = %#v, want %d", account["runtime_reset_at"], next.Unix())
	}
	if account["schedulable"] != false || account["reason"] != "auth_unavailable" {
		t.Fatalf("effective schedule state = %#v", account)
	}
}

func TestEvaluateQuotaHealthAccountDoesNotTreatTransientCooldownAsQuota(t *testing.T) {
	next := time.Now().Add(time.Minute)
	auth := &coreauth.Auth{
		Provider:       "codex",
		Status:         coreauth.StatusError,
		Unavailable:    true,
		Attributes:     map[string]string{"plan_type": "plus"},
		NextRetryAfter: next,
	}

	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), auth, quotaHealthTestWeeklyOnlyUsage(0), nil)
	if err != nil {
		t.Fatalf("evaluateQuotaHealthAccount() error = %v", err)
	}
	if account["runtime_quota_exceeded"] != false {
		t.Fatalf("runtime_quota_exceeded = %#v, want false", account["runtime_quota_exceeded"])
	}
}

func TestQuotaHealthWindowsUsesDurationNames(t *testing.T) {
	usage := map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": quotaHealthWindow7d,
				"used_percent":         12.5,
			},
			"secondary_window": map[string]any{
				"limit_window_seconds": quotaHealthWindow5h,
				"used_percent":         75.0,
			},
		},
	}

	windows, err := quotaHealthWindows(usage)
	if err != nil {
		t.Fatalf("quotaHealthWindows() error = %v", err)
	}
	got5h, err5h := quotaHealthWindowRemaining(windows, "5h")
	if err5h != nil {
		t.Fatalf("5h remaining error = %v", err5h)
	}
	got7d, err7d := quotaHealthWindowRemaining(windows, "7d")
	if err7d != nil {
		t.Fatalf("7d remaining error = %v", err7d)
	}
	if got5h != 25.0 {
		t.Fatalf("5h remaining = %v, want 25", got5h)
	}
	if got7d != 87.5 {
		t.Fatalf("7d remaining = %v, want 87.5", got7d)
	}
}

func TestQuotaHealthWindowsAllowsWeeklyOnly(t *testing.T) {
	windows, err := quotaHealthWindows(quotaHealthTestWeeklyOnlyUsage(25))
	if err != nil {
		t.Fatalf("quotaHealthWindows() error = %v", err)
	}
	if _, ok := windows["5h"]; ok {
		t.Fatalf("windows = %#v, unexpected 5h window", windows)
	}
	got7d, err7d := quotaHealthWindowRemaining(windows, "7d")
	if err7d != nil {
		t.Fatalf("7d remaining error = %v", err7d)
	}
	if got7d != 75.0 {
		t.Fatalf("7d remaining = %v, want 75", got7d)
	}
}

func TestQuotaHealthWindowsStillRequiresWeeklyWindow(t *testing.T) {
	usage := map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": quotaHealthWindow5h,
				"used_percent":         10.0,
			},
		},
	}
	if _, err := quotaHealthWindows(usage); err == nil || err.Error() != "missing_required_7d_quota_window" {
		t.Fatalf("quotaHealthWindows() error = %v, want missing_required_7d_quota_window", err)
	}
}

func TestEvaluateQuotaHealthAccountWeeklyOnly(t *testing.T) {
	h := &Handler{}
	plus, errPlus := h.evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}, quotaHealthTestWeeklyOnlyUsage(22), nil)
	if errPlus != nil {
		t.Fatalf("plus evaluate error = %v", errPlus)
	}
	if plus["raw_remaining_percent"] != 78.0 || plus["usable_balance_units"] != 78.0 {
		t.Fatalf("plus weekly-only balance = %#v", plus)
	}

	pro, errPro := h.evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "pro"},
	}, quotaHealthTestWeeklyOnlyUsage(22), nil)
	if errPro != nil {
		t.Fatalf("pro evaluate error = %v", errPro)
	}
	if pro["raw_remaining_percent"] != 78.0 || pro["usable_balance_units"] != 78.0 {
		t.Fatalf("pro weekly-only balance = %#v", pro)
	}
}

func TestEvaluateQuotaHealthAccountPlusWeeklyExhausted(t *testing.T) {
	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}, quotaHealthTestUsage(10, 100), nil)
	if err != nil {
		t.Fatalf("evaluateQuotaHealthAccount() error = %v", err)
	}
	if account["bucket"] != "personal" || account["can_exhaust"] != true {
		t.Fatalf("bucket fields = %#v", account)
	}
	if account["schedulable"] != false || account["reason"] != "quota_7d_exhausted" || account["state"] != authScheduleStateQuota7dExhausted {
		t.Fatalf("exhausted account = %#v", account)
	}
	if account["usable_balance_units"] != 0.0 {
		t.Fatalf("usable_balance_units = %#v, want 0", account["usable_balance_units"])
	}
}

func TestEvaluateQuotaHealthAccountProProtectedHeadroom(t *testing.T) {
	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "pro"},
	}, quotaHealthTestUsage(4.75, 22.0), nil)
	if err != nil {
		t.Fatalf("evaluateQuotaHealthAccount() error = %v", err)
	}
	if account["bucket"] != "protected" || account["can_exhaust"] != false {
		t.Fatalf("bucket fields = %#v", account)
	}
	if account["schedulable"] != true || account["state"] != authScheduleStateAvailable {
		t.Fatalf("schedulable fields = %#v", account)
	}
	if account["raw_remaining_percent"] != 78.0 {
		t.Fatalf("raw_remaining_percent = %#v, want 78", account["raw_remaining_percent"])
	}
	if account["usable_balance_units"] != 78.0 {
		t.Fatalf("usable_balance_units = %#v, want 78", account["usable_balance_units"])
	}
	if account["protected_reserve_warning"] != false {
		t.Fatalf("protected_reserve_warning = %#v, want false", account["protected_reserve_warning"])
	}
}

func TestEvaluateQuotaHealthAccountProReserveIsWarningOnly(t *testing.T) {
	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "pro"},
	}, quotaHealthTestUsage(85, 85), nil)
	if err != nil {
		t.Fatalf("evaluateQuotaHealthAccount() error = %v", err)
	}
	if account["schedulable"] != true || account["state"] != authScheduleStateAvailable || account["reason"] != nil {
		t.Fatalf("reserve warning account = %#v", account)
	}
	if account["raw_remaining_percent"] != 15.0 || account["usable_balance_units"] != 15.0 {
		t.Fatalf("reserve warning balance = %#v", account)
	}
	if account["protected_reserve_warning"] != true {
		t.Fatalf("protected_reserve_warning = %#v, want true", account["protected_reserve_warning"])
	}
}

func TestEvaluateQuotaHealthAggregatesBuckets(t *testing.T) {
	cfg := defaultQuotaHealthTestConfig()
	h := &Handler{}
	plus, errPlus := h.evaluateQuotaHealthAccount(cfg, &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}, quotaHealthTestUsage(100, 100), nil)
	if errPlus != nil {
		t.Fatalf("plus evaluate error = %v", errPlus)
	}
	pro, errPro := h.evaluateQuotaHealthAccount(cfg, &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "pro"},
	}, quotaHealthTestUsage(4.75, 22.0), nil)
	if errPro != nil {
		t.Fatalf("pro evaluate error = %v", errPro)
	}

	result := evaluateQuotaHealth(cfg, []map[string]any{plus, pro})
	if result["ok"] != true || result["quota_ok"] != true {
		t.Fatalf("result = %#v", result)
	}
	if result["usable_balance_units"] != 78.0 {
		t.Fatalf("usable_balance_units = %#v, want 78", result["usable_balance_units"])
	}
	if result["available_account_count"] != 1 {
		t.Fatalf("available_account_count = %#v, want 1", result["available_account_count"])
	}
	windows, ok := result["windows"].(map[string]any)
	if !ok || windows["5h"] == nil || windows["7d"] == nil {
		t.Fatalf("windows = %#v, want 5h and 7d aggregate entries", result["windows"])
	}
}

func TestEvaluateQuotaHealthAccountResetCreditsUsesReadOnlyEndpointPayload(t *testing.T) {
	resetCredits := map[string]any{
		"available_count":    float64(2),
		"total_earned_count": float64(5),
		"credits": []any{
			map[string]any{
				"id":         "credit-later-abcdef12",
				"status":     "available",
				"reset_type": "codex_rate_limits",
				"expires_at": "2030-01-03T00:00:00Z",
			},
			map[string]any{
				"id":         "credit-earlier-12345678",
				"status":     "available",
				"reset_type": "codex_rate_limits",
				"expires_at": "2030-01-02T00:00:00Z",
			},
			map[string]any{
				"id":         "credit-redeemed-99999999",
				"status":     "redeemed",
				"reset_type": "codex_rate_limits",
				"expires_at": "2030-01-01T00:00:00Z",
			},
		},
	}

	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}, quotaHealthTestUsage(10, 10), resetCredits)
	if err != nil {
		t.Fatalf("evaluateQuotaHealthAccount() error = %v", err)
	}
	if got := account["reset_credits_available"]; got != 2 {
		t.Fatalf("reset_credits_available = %#v, want 2", got)
	}
	if got := account["reset_credits_total_earned"]; got != 5 {
		t.Fatalf("reset_credits_total_earned = %#v, want 5", got)
	}
	if got := account["reset_credits_earliest_expires_at"]; got != "2030-01-02T00:00:00Z" {
		t.Fatalf("reset_credits_earliest_expires_at = %#v", got)
	}
	credits, ok := account["reset_credits"].([]map[string]any)
	if !ok || len(credits) != 2 {
		t.Fatalf("reset_credits = %#v, want two available credits", account["reset_credits"])
	}
	if credits[0]["id_suffix"] != "12345678" || credits[1]["id_suffix"] != "abcdef12" {
		t.Fatalf("reset_credits sort/id suffix = %#v", credits)
	}
}

func TestEvaluateQuotaHealthAccountResetCreditsFallsBackToUsageCount(t *testing.T) {
	usage := quotaHealthTestUsage(10, 10)
	usage["rate_limit_reset_credits"] = map[string]any{"available_count": float64(3)}

	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}, usage, nil)
	if err != nil {
		t.Fatalf("evaluateQuotaHealthAccount() error = %v", err)
	}
	if got := account["reset_credits_available"]; got != 3 {
		t.Fatalf("reset_credits_available = %#v, want 3", got)
	}
	if _, ok := account["reset_credits_earliest_expires_at"]; ok {
		t.Fatalf("reset_credits_earliest_expires_at should be absent when only usage fallback is available")
	}
}

func defaultQuotaHealthTestConfig() quotaHealthConfig {
	return quotaHealthConfig{
		Enabled:                true,
		MinRemainingPercent5h:  30,
		MinRemainingPercent7d:  20,
		BalanceUnitsPerPercent: 1,
	}
}

func quotaHealthTestUsage(used5h float64, used7d float64) map[string]any {
	return map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": quotaHealthWindow5h,
				"used_percent":         used5h,
				"reset_at":             1800000000,
				"reset_after_seconds":  1200,
			},
			"secondary_window": map[string]any{
				"limit_window_seconds": quotaHealthWindow7d,
				"used_percent":         used7d,
				"reset_at":             1800100000,
				"reset_after_seconds":  86400,
			},
		},
	}
}

func quotaHealthTestWeeklyOnlyUsage(used7d float64) map[string]any {
	return map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": quotaHealthWindow7d,
				"used_percent":         used7d,
				"reset_at":             1800100000,
				"reset_after_seconds":  86400,
			},
		},
	}
}
