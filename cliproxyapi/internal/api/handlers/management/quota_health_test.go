package management

import (
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

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

func TestEvaluateQuotaHealthAccountPlusWeeklyExhausted(t *testing.T) {
	account, err := (&Handler{}).evaluateQuotaHealthAccount(defaultQuotaHealthTestConfig(), &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}, quotaHealthTestUsage(10, 100))
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
	}, quotaHealthTestUsage(4.75, 22.0))
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
	if account["usable_balance_units"] != 58.0 {
		t.Fatalf("usable_balance_units = %#v, want 58", account["usable_balance_units"])
	}
}

func TestEvaluateQuotaHealthAggregatesBuckets(t *testing.T) {
	cfg := defaultQuotaHealthTestConfig()
	h := &Handler{}
	plus, errPlus := h.evaluateQuotaHealthAccount(cfg, &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}, quotaHealthTestUsage(100, 100))
	if errPlus != nil {
		t.Fatalf("plus evaluate error = %v", errPlus)
	}
	pro, errPro := h.evaluateQuotaHealthAccount(cfg, &coreauth.Auth{
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "pro"},
	}, quotaHealthTestUsage(4.75, 22.0))
	if errPro != nil {
		t.Fatalf("pro evaluate error = %v", errPro)
	}

	result := evaluateQuotaHealth(cfg, []map[string]any{plus, pro})
	if result["ok"] != true || result["quota_ok"] != true {
		t.Fatalf("result = %#v", result)
	}
	if result["usable_balance_units"] != 58.0 {
		t.Fatalf("usable_balance_units = %#v, want 58", result["usable_balance_units"])
	}
	if result["available_account_count"] != 1 {
		t.Fatalf("available_account_count = %#v, want 1", result["available_account_count"])
	}
	windows, ok := result["windows"].(map[string]any)
	if !ok || windows["5h"] == nil || windows["7d"] == nil {
		t.Fatalf("windows = %#v, want 5h and 7d aggregate entries", result["windows"])
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
