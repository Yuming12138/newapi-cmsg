package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	codexQuotaHealthUsageURL        = "https://chatgpt.com/backend-api/wham/usage"
	codexQuotaHealthResetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	quotaHealthWindow5h             = 5 * 60 * 60
	quotaHealthWindow7d             = 7 * 24 * 60 * 60
)

type quotaHealthConfig struct {
	Enabled                bool
	MinRemainingPercent5h  float64
	MinRemainingPercent7d  float64
	BalanceUnitsPerPercent float64
}

// GetQuotaHealth reports Codex rolling-window quota health for management callers.
func (h *Handler) GetQuotaHealth(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	cfg := quotaHealthConfigFromQuery(c)
	auths := h.authManager.List()
	accounts := make([]map[string]any, 0)
	successful := 0

	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			continue
		}
		auth.EnsureIndex()
		base := h.quotaHealthBaseAccount(auth, cfg, nil)
		if auth.Disabled {
			base["ok"] = false
			base["schedulable"] = false
			base["skipped"] = true
			base["reason"] = "auth_disabled"
			base["state"] = authScheduleStateManualDisabled
			base["retryable"] = false
			accounts = append(accounts, base)
			continue
		}

		usage, errUsage := h.fetchCodexWhamUsage(c.Request.Context(), auth)
		if errUsage != nil {
			stateInfo := deriveAuthScheduleState(auth, time.Now())
			base["ok"] = false
			base["schedulable"] = false
			base["skipped"] = auth.Unavailable
			if auth.Unavailable {
				base["reason"] = "auth_unavailable"
			} else {
				base["reason"] = "quota_probe_failed"
			}
			base["error"] = trimQuotaHealthError(errUsage)
			base["state"] = stateInfo.State
			base["retryable"] = stateInfo.Retryable
			if !stateInfo.ResetAt.IsZero() {
				base["reset_at"] = stateInfo.ResetAt.Unix()
			}
			accounts = append(accounts, base)
			continue
		}

		resetCredits, errResetCredits := h.fetchCodexResetCredits(c.Request.Context(), auth)
		account, errEvaluate := h.evaluateQuotaHealthAccount(cfg, auth, usage, resetCredits)
		if errEvaluate != nil {
			base["ok"] = false
			base["schedulable"] = false
			base["skipped"] = false
			base["reason"] = "quota_probe_failed"
			base["error"] = trimQuotaHealthError(errEvaluate)
			base["state"] = authScheduleStateUnknown
			accounts = append(accounts, base)
			continue
		}
		if errResetCredits != nil {
			account["reset_credits_error"] = trimQuotaHealthError(errResetCredits)
		}
		accounts = append(accounts, account)
		successful++
	}

	if len(accounts) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"ok":           false,
			"within_share": false,
			"quota_ok":     false,
			"reason":       "codex_auth_not_found",
			"error":        "codex_auth_not_found",
			"accounts":     []any{},
		})
		return
	}

	if successful == 0 && !quotaHealthAllIntentionallySkipped(accounts) {
		c.JSON(http.StatusOK, quotaHealthProbeFailedResult(accounts))
		return
	}

	c.JSON(http.StatusOK, evaluateQuotaHealth(cfg, accounts))
}

func quotaHealthConfigFromQuery(c *gin.Context) quotaHealthConfig {
	return quotaHealthConfig{
		Enabled:                quotaHealthBoolQuery(c, "enabled", true),
		MinRemainingPercent5h:  quotaHealthPercentQuery(c, "min_remaining_percent_5h", 30),
		MinRemainingPercent7d:  quotaHealthPercentQuery(c, "min_remaining_percent_7d", 20),
		BalanceUnitsPerPercent: quotaHealthFloatQuery(c, "balance_units_per_percent", 1),
	}
}

func quotaHealthBoolQuery(c *gin.Context, key string, fallback bool) bool {
	if c == nil {
		return fallback
	}
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func quotaHealthPercentQuery(c *gin.Context, key string, fallback float64) float64 {
	return clampQuotaHealthPercent(quotaHealthFloatQuery(c, key, fallback), fallback)
}

func quotaHealthFloatQuery(c *gin.Context, key string, fallback float64) float64 {
	if c == nil {
		return fallback
	}
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback
	}
	parsed, errParse := strconv.ParseFloat(raw, 64)
	if errParse != nil {
		return fallback
	}
	return parsed
}

func (h *Handler) quotaHealthBaseAccount(auth *coreauth.Auth, cfg quotaHealthConfig, usage map[string]any) map[string]any {
	authIndex, accountID, accountIDHash := quotaHealthAccountIdentity(auth)
	planType := quotaHealthPlanType(auth, usage)
	bucket := quotaHealthClassifyBucket(auth, usage, planType)
	canExhaust := bucket == "personal"
	runtimeState := deriveAuthScheduleState(auth, time.Now())
	base := map[string]any{
		"auth_index":              authIndex,
		"account_id_hash":         accountIDHash,
		"account_label":           quotaHealthAccountLabel(auth),
		"plan_type":               planType,
		"bucket":                  bucket,
		"can_exhaust":             canExhaust,
		"disabled":                auth != nil && auth.Disabled,
		"unavailable":             auth != nil && auth.Unavailable,
		"runtime_unavailable":     auth != nil && auth.Unavailable,
		"runtime_state":           runtimeState.State,
		"runtime_reason":          runtimeState.Reason,
		"runtime_retryable":       runtimeState.Retryable,
		"runtime_schedulable":     runtimeState.Schedulable,
		"runtime_reset_at":        nil,
		"runtime_quota_exceeded":  authRuntimeQuotaExceeded(auth),
		"reset_credits_available": nil,
	}
	if !runtimeState.ResetAt.IsZero() {
		base["runtime_reset_at"] = runtimeState.ResetAt.Unix()
	}
	if !canExhaust {
		base["min_remaining_percent_5h"] = cfg.MinRemainingPercent5h
		base["min_remaining_percent_7d"] = cfg.MinRemainingPercent7d
	}
	if accountID == "" {
		base["account_id_hash"] = ""
	}
	return base
}

func authRuntimeQuotaExceeded(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	if quotaReasonIsExhaustion(auth.Quota) {
		return true
	}
	for _, state := range auth.ModelStates {
		if state != nil && quotaReasonIsExhaustion(state.Quota) {
			return true
		}
	}
	return false
}

func quotaReasonIsExhaustion(quota coreauth.QuotaState) bool {
	if !quota.Exceeded {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(quota.Reason))
	return normalized == "quota" ||
		strings.Contains(normalized, "5h") ||
		strings.Contains(normalized, "7d") ||
		strings.Contains(normalized, "week") ||
		strings.Contains(normalized, "weekly")
}

func (h *Handler) fetchCodexWhamUsage(ctx context.Context, auth *coreauth.Auth) (map[string]any, error) {
	return h.fetchCodexWhamJSON(ctx, auth, codexQuotaHealthUsageURL, "wham_usage")
}

func (h *Handler) fetchCodexResetCredits(ctx context.Context, auth *coreauth.Auth) (map[string]any, error) {
	return h.fetchCodexWhamJSON(ctx, auth, codexQuotaHealthResetCreditsURL, "reset_credits")
}

func (h *Handler) fetchCodexWhamJSON(ctx context.Context, auth *coreauth.Auth, endpoint string, label string) (map[string]any, error) {
	if auth == nil {
		return nil, fmt.Errorf("auth is required")
	}
	accountID := codexAccountID(auth)
	if accountID == "" {
		return nil, fmt.Errorf("missing_account_id")
	}
	token, errToken := h.resolveTokenForAuth(ctx, auth)
	if errToken != nil {
		return nil, fmt.Errorf("auth token refresh failed: %w", errToken)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("auth token not found")
	}

	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if errReq != nil {
		return nil, errReq
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Chatgpt-Account-Id", accountID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", "Codex Desktop")
	req.Header.Set("OAI-Product-Sku", "CODEX")
	req.Header.Set("User-Agent", codexResetCreditUserAgent)

	client := &http.Client{Transport: h.apiCallTransport(auth)}
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, errDo
	}
	defer func() { _ = resp.Body.Close() }()

	body, errRead := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if errRead != nil {
		return nil, errRead
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s_http_%d", label, resp.StatusCode)
	}

	var payload map[string]any
	if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil {
		return nil, fmt.Errorf("%s_invalid_json: %w", label, errUnmarshal)
	}
	if payload == nil {
		return nil, fmt.Errorf("%s_payload_not_object", label)
	}
	return payload, nil
}

func (h *Handler) evaluateQuotaHealthAccount(cfg quotaHealthConfig, auth *coreauth.Auth, usage map[string]any, resetCredits map[string]any) (map[string]any, error) {
	windows, errWindows := quotaHealthWindows(usage)
	if errWindows != nil {
		return nil, errWindows
	}
	remaining7d, errRemaining7d := quotaHealthWindowRemaining(windows, "7d")
	if errRemaining7d != nil {
		return nil, errRemaining7d
	}
	remainingValues := []float64{remaining7d}
	headroomValues := []float64{remaining7d - cfg.MinRemainingPercent7d}
	if _, has5h := windows["5h"]; has5h {
		remaining5h, errRemaining5h := quotaHealthWindowRemaining(windows, "5h")
		if errRemaining5h != nil {
			return nil, errRemaining5h
		}
		remainingValues = append(remainingValues, remaining5h)
		headroomValues = append(headroomValues, remaining5h-cfg.MinRemainingPercent5h)
	}

	account := h.quotaHealthBaseAccount(auth, cfg, usage)
	canExhaust, _ := account["can_exhaust"].(bool)
	minimumHeadroom := minSliceFloat64(headroomValues)
	rawRemaining := minSliceFloat64(remainingValues)
	protectedHeadroom := maxFloat64(0, minimumHeadroom)
	visibleRemaining := rawRemaining
	protectedReserveWarning := !canExhaust && protectedHeadroom <= 0.000001
	balanceUnits := rawRemaining * cfg.BalanceUnitsPerPercent
	usableBalanceUnits := visibleRemaining * cfg.BalanceUnitsPerPercent
	reason, exhaustedWindow := quotaHealthUnschedulableReason(auth, windows)
	schedulable := reason == ""

	account["ok"] = true
	account["schedulable"] = schedulable
	account["remaining_headroom_percent"] = roundQuotaHealth(minimumHeadroom)
	account["protected_reserve_warning"] = protectedReserveWarning
	account["remaining_share_percent"] = roundQuotaHealth(visibleRemaining)
	account["raw_remaining_percent"] = roundQuotaHealth(rawRemaining)
	account["balance_units"] = roundQuotaHealth(balanceUnits)
	account["usable_balance_units"] = roundQuotaHealth(usableBalanceUnits)
	account["reason"] = nil
	if reason != "" {
		account["reason"] = reason
	}
	account["reset_credits_available"] = quotaHealthResetCreditsAvailable(resetCredits, usage)
	if resetCreditItems, earliestExpiresAt := quotaHealthResetCredits(resetCredits); len(resetCreditItems) > 0 {
		account["reset_credits"] = resetCreditItems
		account["reset_credits_earliest_expires_at"] = earliestExpiresAt
	}
	if totalEarned, ok := quotaHealthNumber(quotaHealthFirstNonEmpty(resetCredits["total_earned_count"], resetCredits["totalEarnedCount"])); ok {
		account["reset_credits_total_earned"] = int(totalEarned)
	}
	account["windows"] = windows
	account["rate_limit_reached_type"] = quotaHealthFirstNonEmpty(usage["rate_limit_reached_type"], usage["rateLimitReachedType"])
	account["quota_exhausted_window"] = nil
	if exhaustedWindow != "" {
		account["quota_exhausted_window"] = exhaustedWindow
	}
	account["state"] = quotaHealthStateForAccount(reason)
	account["retryable"] = false
	if reason == "" {
		account["reason"] = nil
	}
	return account, nil
}

func quotaHealthWindows(usage map[string]any) (map[string]map[string]any, error) {
	rateLimit := quotaHealthMap(quotaHealthFirstNonEmpty(usage["rate_limit"], usage["rateLimit"]))
	if rateLimit == nil {
		return nil, fmt.Errorf("missing_rate_limit")
	}
	candidates := []struct {
		fallbackName string
		raw          any
	}{
		{"5h", quotaHealthFirstNonEmpty(rateLimit["primary_window"], rateLimit["primaryWindow"])},
		{"7d", quotaHealthFirstNonEmpty(rateLimit["secondary_window"], rateLimit["secondaryWindow"])},
	}
	out := make(map[string]map[string]any)
	for _, candidate := range candidates {
		item := quotaHealthMap(candidate.raw)
		if item == nil {
			continue
		}
		duration := int(quotaHealthNumberOr(quotaHealthFirstNonEmpty(item["limit_window_seconds"], item["limitWindowSeconds"]), 0))
		name := candidate.fallbackName
		switch duration {
		case quotaHealthWindow5h:
			name = "5h"
		case quotaHealthWindow7d:
			name = "7d"
		}
		used, usedOK := quotaHealthNumber(quotaHealthFirstNonEmpty(item["used_percent"], item["usedPercent"]))
		resetAt, resetAtOK := quotaHealthNumber(quotaHealthFirstNonEmpty(item["reset_at"], item["resetAt"]))
		resetAfter, resetAfterOK := quotaHealthNumber(quotaHealthFirstNonEmpty(item["reset_after_seconds"], item["resetAfterSeconds"]))
		window := map[string]any{
			"duration_seconds":    duration,
			"used_percent":        nil,
			"remaining_percent":   nil,
			"reset_at":            nil,
			"reset_after_seconds": nil,
		}
		if usedOK {
			window["used_percent"] = used
			window["remaining_percent"] = clampQuotaHealthPercent(100-used, 0)
		}
		if resetAtOK && resetAt > 0 {
			window["reset_at"] = int64(resetAt)
		}
		if resetAfterOK && resetAfter >= 0 {
			window["reset_after_seconds"] = int64(resetAfter)
		}
		out[name] = window
	}
	if _, ok := out["7d"]; !ok {
		return nil, fmt.Errorf("missing_required_7d_quota_window")
	}
	return out, nil
}

func quotaHealthWindowRemaining(windows map[string]map[string]any, key string) (float64, error) {
	window := windows[key]
	if window == nil {
		return 0, fmt.Errorf("missing_%s_remaining_percent", key)
	}
	remaining, ok := quotaHealthNumber(window["remaining_percent"])
	if !ok {
		return 0, fmt.Errorf("missing_%s_remaining_percent", key)
	}
	return clampQuotaHealthPercent(remaining, 0), nil
}

func quotaHealthUnschedulableReason(auth *coreauth.Auth, windows map[string]map[string]any) (string, string) {
	if auth != nil && auth.Disabled {
		return "auth_disabled", ""
	}
	if exhausted := quotaHealthExhaustedWindow(windows); exhausted != "" {
		return "quota_" + exhausted + "_exhausted", exhausted
	}
	if auth != nil && auth.Unavailable {
		return "auth_unavailable", ""
	}
	return "", ""
}

func quotaHealthExhaustedWindow(windows map[string]map[string]any) string {
	for _, key := range []string{"7d", "5h"} {
		remaining, errRemaining := quotaHealthWindowRemaining(windows, key)
		if errRemaining == nil && remaining <= 0.000001 {
			return key
		}
	}
	return ""
}

func quotaHealthStateForAccount(reason string) string {
	switch reason {
	case "":
		return authScheduleStateAvailable
	case "quota_5h_exhausted":
		return authScheduleStateQuota5hExhausted
	case "quota_7d_exhausted":
		return authScheduleStateQuota7dExhausted
	case "protected_reserve_reached":
		return authScheduleStateProtectedReserve
	case "auth_disabled":
		return authScheduleStateManualDisabled
	default:
		return authScheduleStateUnknown
	}
}

func evaluateQuotaHealth(cfg quotaHealthConfig, accounts []map[string]any) map[string]any {
	okAccounts := quotaHealthOKAccounts(accounts)
	schedulableAccounts := quotaHealthSchedulableAccounts(okAccounts)
	usableBalance := roundQuotaHealth(sumQuotaHealthFloat(okAccounts, "usable_balance_units"))
	totalBalance := roundQuotaHealth(sumQuotaHealthFloat(okAccounts, "balance_units"))
	quotaOK := !cfg.Enabled || usableBalance > 0
	return map[string]any{
		"ok":                       true,
		"quota_ok":                 quotaOK,
		"within_share":             quotaOK,
		"reason":                   quotaHealthQuotaReason(quotaOK),
		"guard_mode":               "bucket_low_watermark",
		"enabled":                  cfg.Enabled,
		"low_watermark_enabled":    cfg.Enabled,
		"min_remaining_percent_5h": cfg.MinRemainingPercent5h,
		"min_remaining_percent_7d": cfg.MinRemainingPercent7d,
		"account_count":            len(accounts),
		"available_account_count":  len(schedulableAccounts),
		"remaining_share_percent":  usableBalance,
		"balance_units":            usableBalance,
		"usable_balance_units":     usableBalance,
		"total_balance_units":      totalBalance,
		"buckets":                  quotaHealthBuckets(cfg, accounts),
		"accounts":                 accounts,
		"windows":                  aggregateQuotaHealthWindows(okAccounts),
	}
}

func quotaHealthProbeFailedResult(accounts []map[string]any) map[string]any {
	errors := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if errText, ok := account["error"].(string); ok && errText != "" {
			errors = append(errors, errText)
			continue
		}
		if reason, ok := account["reason"].(string); ok && reason != "" {
			errors = append(errors, reason)
		}
	}
	if len(errors) > 3 {
		errors = errors[:3]
	}
	return map[string]any{
		"ok":            false,
		"within_share":  false,
		"quota_ok":      false,
		"reason":        "quota_probe_failed",
		"error":         strings.Join(errors, "; "),
		"guard_mode":    "bucket_low_watermark",
		"account_count": len(accounts),
		"accounts":      accounts,
		"buckets":       map[string]any{},
		"windows":       map[string]any{},
	}
}

func quotaHealthQuotaReason(quotaOK bool) string {
	if quotaOK {
		return "usable_balance_available"
	}
	return "quota_low_watermark_reached"
}

func quotaHealthBuckets(cfg quotaHealthConfig, accounts []map[string]any) map[string]any {
	out := make(map[string]any)
	for _, bucket := range []string{"personal", "protected"} {
		bucketAccounts := make([]map[string]any, 0)
		for _, account := range accounts {
			if value, _ := account["bucket"].(string); value == bucket {
				bucketAccounts = append(bucketAccounts, account)
			}
		}
		if len(bucketAccounts) > 0 {
			out[bucket] = quotaHealthBucketSummary(cfg, bucket, bucketAccounts)
		}
	}
	return out
}

func quotaHealthBucketSummary(cfg quotaHealthConfig, bucket string, accounts []map[string]any) map[string]any {
	canExhaust := bucket == "personal"
	okAccounts := quotaHealthOKAccounts(accounts)
	schedulableAccounts := quotaHealthSchedulableAccounts(okAccounts)
	summary := map[string]any{
		"bucket":                  bucket,
		"label":                   quotaHealthBucketLabel(bucket),
		"can_exhaust":             canExhaust,
		"account_count":           len(accounts),
		"available_account_count": len(schedulableAccounts),
		"balance_units":           roundQuotaHealth(sumQuotaHealthFloat(okAccounts, "balance_units")),
		"usable_balance_units":    roundQuotaHealth(sumQuotaHealthFloat(okAccounts, "usable_balance_units")),
		"remaining_share_percent": roundQuotaHealth(sumQuotaHealthFloat(okAccounts, "remaining_share_percent")),
		"raw_remaining_percent":   roundQuotaHealth(sumQuotaHealthFloat(okAccounts, "raw_remaining_percent")),
		"accounts":                quotaHealthAccountSummaries(accounts),
		"windows":                 aggregateQuotaHealthWindows(okAccounts),
		"plans":                   quotaHealthPlans(accounts),
	}
	if canExhaust {
		summary["min_remaining_percent_5h"] = nil
		summary["min_remaining_percent_7d"] = nil
	} else {
		summary["min_remaining_percent_5h"] = cfg.MinRemainingPercent5h
		summary["min_remaining_percent_7d"] = cfg.MinRemainingPercent7d
	}
	if resetCredits, ok := quotaHealthResetCreditsTotal(okAccounts); ok {
		summary["reset_credits_available"] = resetCredits
	} else {
		summary["reset_credits_available"] = nil
	}
	return summary
}

func quotaHealthBucketLabel(bucket string) string {
	if bucket == "personal" {
		return "个人池"
	}
	return "共享 Pro"
}

func quotaHealthAccountSummaries(accounts []map[string]any) []map[string]any {
	keys := []string{
		"auth_index",
		"account_id_hash",
		"account_label",
		"plan_type",
		"bucket",
		"ok",
		"schedulable",
		"state",
		"retryable",
		"runtime_unavailable",
		"runtime_state",
		"runtime_reason",
		"runtime_retryable",
		"runtime_schedulable",
		"runtime_reset_at",
		"runtime_quota_exceeded",
		"quota_exhausted_window",
		"can_exhaust",
		"disabled",
		"unavailable",
		"skipped",
		"reason",
		"error",
		"balance_units",
		"usable_balance_units",
		"remaining_share_percent",
		"raw_remaining_percent",
		"protected_reserve_warning",
		"reset_credits_available",
		"reset_credits_earliest_expires_at",
		"reset_credits_total_earned",
		"reset_credits",
		"reset_credits_error",
		"windows",
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		item := make(map[string]any)
		for _, key := range keys {
			if value, ok := account[key]; ok {
				item[key] = value
			}
		}
		out = append(out, item)
	}
	return out
}

func aggregateQuotaHealthWindows(accounts []map[string]any) map[string]any {
	out := make(map[string]any)
	for _, name := range []string{"5h", "7d"} {
		items := make([]map[string]any, 0)
		for _, account := range accounts {
			windows := quotaHealthMap(account["windows"])
			if windows == nil {
				continue
			}
			window := quotaHealthMap(windows[name])
			if window != nil {
				items = append(items, window)
			}
		}
		if len(items) == 0 {
			continue
		}
		out[name] = aggregateQuotaHealthWindow(name, items)
	}
	return out
}

func aggregateQuotaHealthWindow(name string, items []map[string]any) map[string]any {
	remaining := make([]float64, 0, len(items))
	used := make([]float64, 0, len(items))
	resetAfter := make([]float64, 0, len(items))
	resetAt := make([]float64, 0, len(items))
	duration := quotaHealthWindow5h
	if name == "7d" {
		duration = quotaHealthWindow7d
	}
	for _, item := range items {
		if value, ok := quotaHealthNumber(item["remaining_percent"]); ok {
			remaining = append(remaining, value)
		}
		if value, ok := quotaHealthNumber(item["used_percent"]); ok {
			used = append(used, value)
		}
		if value, ok := quotaHealthNumber(item["reset_after_seconds"]); ok && value >= 0 {
			resetAfter = append(resetAfter, value)
		}
		if value, ok := quotaHealthNumber(item["reset_at"]); ok && value > 0 {
			resetAt = append(resetAt, value)
		}
		if value, ok := quotaHealthNumber(item["duration_seconds"]); ok && value > 0 {
			duration = int(value)
		}
	}
	window := map[string]any{
		"duration_seconds":    duration,
		"used_percent":        nil,
		"remaining_percent":   nil,
		"reset_after_seconds": nil,
		"reset_at":            nil,
	}
	if len(used) > 0 {
		window["used_percent"] = roundQuotaHealth(avgFloat64(used))
	}
	if len(remaining) > 0 {
		window["remaining_percent"] = roundQuotaHealth(avgFloat64(remaining))
	}
	if len(resetAfter) > 0 {
		window["reset_after_seconds"] = int64(minSliceFloat64(resetAfter))
	}
	if len(resetAt) > 0 {
		window["reset_at"] = int64(minSliceFloat64(resetAt))
	}
	return window
}

func quotaHealthOKAccounts(accounts []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		if ok, _ := account["ok"].(bool); ok {
			out = append(out, account)
		}
	}
	return out
}

func quotaHealthSchedulableAccounts(accounts []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		if schedulable, _ := account["schedulable"].(bool); schedulable {
			out = append(out, account)
		}
	}
	return out
}

func quotaHealthAllIntentionallySkipped(accounts []map[string]any) bool {
	for _, account := range accounts {
		skipped, _ := account["skipped"].(bool)
		if !skipped {
			return false
		}
		if errText, _ := account["error"].(string); strings.TrimSpace(errText) != "" {
			return false
		}
	}
	return len(accounts) > 0
}

func quotaHealthPlans(accounts []map[string]any) map[string]int {
	out := make(map[string]int)
	for _, account := range accounts {
		plan := strings.TrimSpace(fmt.Sprint(account["plan_type"]))
		if plan == "" || plan == "<nil>" {
			plan = "unknown"
		}
		out[plan]++
	}
	return out
}

func quotaHealthResetCreditsTotal(accounts []map[string]any) (int, bool) {
	total := 0
	found := false
	for _, account := range accounts {
		value, ok := quotaHealthNumber(account["reset_credits_available"])
		if !ok {
			continue
		}
		total += int(value)
		found = true
	}
	return total, found
}

func sumQuotaHealthFloat(accounts []map[string]any, key string) float64 {
	var sum float64
	for _, account := range accounts {
		value, ok := quotaHealthNumber(account[key])
		if ok {
			sum += value
		}
	}
	return sum
}

func quotaHealthResetCreditsAvailable(resetCredits map[string]any, usage map[string]any) any {
	credits := quotaHealthFirstNonEmpty(
		resetCredits["available_count"],
		resetCredits["availableCount"],
		quotaHealthNested(usage, "rate_limit_reset_credits", "available_count"),
		quotaHealthNested(usage, "rateLimitResetCredits", "availableCount"),
	)
	value, ok := quotaHealthNumber(credits)
	if !ok {
		return nil
	}
	if value < 0 {
		return 0
	}
	return int(value)
}

func quotaHealthResetCredits(resetCredits map[string]any) ([]map[string]any, string) {
	rawCredits, _ := quotaHealthFirstNonEmpty(resetCredits["credits"], resetCredits["Credits"]).([]any)
	if len(rawCredits) == 0 {
		return nil, ""
	}
	credits := make([]map[string]any, 0, len(rawCredits))
	for _, raw := range rawCredits {
		credit := quotaHealthMap(raw)
		if credit == nil {
			continue
		}
		status := strings.TrimSpace(fmt.Sprint(quotaHealthFirstNonEmpty(credit["status"], credit["Status"])))
		if !strings.EqualFold(status, "available") {
			continue
		}
		expiresAt := strings.TrimSpace(fmt.Sprint(quotaHealthFirstNonEmpty(credit["expires_at"], credit["expiresAt"])))
		if expiresAt == "" || expiresAt == "<nil>" {
			continue
		}
		item := map[string]any{
			"status":     status,
			"expires_at": expiresAt,
		}
		if resetType := strings.TrimSpace(fmt.Sprint(quotaHealthFirstNonEmpty(credit["reset_type"], credit["resetType"]))); resetType != "" && resetType != "<nil>" {
			item["reset_type"] = resetType
		}
		if id := strings.TrimSpace(fmt.Sprint(quotaHealthFirstNonEmpty(credit["id"], credit["ID"]))); id != "" && id != "<nil>" {
			if len(id) > 8 {
				item["id_suffix"] = id[len(id)-8:]
			} else {
				item["id_suffix"] = id
			}
		}
		credits = append(credits, item)
	}
	if len(credits) == 0 {
		return nil, ""
	}
	sort.Slice(credits, func(i, j int) bool {
		return quotaHealthTimeSortKey(credits[i]["expires_at"]).Before(quotaHealthTimeSortKey(credits[j]["expires_at"]))
	})
	earliest, _ := credits[0]["expires_at"].(string)
	return credits, earliest
}

func quotaHealthTimeSortKey(value any) time.Time {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "<nil>" {
		return time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed
	}
	return time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
}

func quotaHealthAccountIdentity(auth *coreauth.Auth) (string, string, string) {
	if auth == nil {
		return "", "", ""
	}
	auth.EnsureIndex()
	accountID := codexAccountID(auth)
	accountIDHash := ""
	if accountID != "" {
		sum := sha256.Sum256([]byte(accountID))
		accountIDHash = hex.EncodeToString(sum[:])[:12]
	}
	return auth.Index, accountID, accountIDHash
}

func quotaHealthPlanType(auth *coreauth.Auth, usage map[string]any) string {
	if value := strings.TrimSpace(fmt.Sprint(quotaHealthFirstNonEmpty(usage["plan_type"], usage["planType"]))); value != "" && value != "<nil>" {
		return value
	}
	if auth != nil {
		if value := strings.TrimSpace(authAttribute(auth, "plan_type")); value != "" {
			return value
		}
		if auth.Metadata != nil {
			for _, key := range []string{"plan_type", "planType"} {
				if value, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
				}
			}
		}
		if claims := extractCodexIDTokenClaims(auth); claims != nil {
			if value, ok := claims["plan_type"].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return "unknown"
}

func quotaHealthClassifyBucket(auth *coreauth.Auth, usage map[string]any, planType string) string {
	if auth != nil {
		for _, key := range []string{"quota_bucket", "bucket", "account_bucket"} {
			if bucket := normalizeQuotaHealthBucket(authAttribute(auth, key)); bucket != "" {
				return bucket
			}
			if auth.Metadata != nil {
				if value, ok := auth.Metadata[key].(string); ok {
					if bucket := normalizeQuotaHealthBucket(value); bucket != "" {
						return bucket
					}
				}
			}
		}
	}
	if bucket := normalizeQuotaHealthBucket(fmt.Sprint(quotaHealthFirstNonEmpty(usage["bucket"], usage["account_bucket"], usage["accountBucket"]))); bucket != "" {
		return bucket
	}
	normalizedPlan := strings.ToLower(strings.TrimSpace(planType))
	if strings.Contains(normalizedPlan, "plus") {
		return "personal"
	}
	if strings.Contains(normalizedPlan, "pro") {
		return "protected"
	}
	return "protected"
}

func normalizeQuotaHealthBucket(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	raw = strings.ReplaceAll(raw, "-", "_")
	switch raw {
	case "personal", "expendable", "plus", "owned", "own":
		return "personal"
	case "protected", "shared", "shared_pro", "pro", "reserved":
		return "protected"
	default:
		return ""
	}
}

func quotaHealthAccountLabel(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, value := range []string{auth.Label, auth.FileName, authEmail(auth)} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	_, account := auth.AccountInfo()
	return strings.TrimSpace(account)
}

func quotaHealthNested(data map[string]any, keys ...string) any {
	var current any = data
	for _, key := range keys {
		item := quotaHealthMap(current)
		if item == nil {
			return nil
		}
		current = item[key]
	}
	return current
}

func quotaHealthFirstNonEmpty(values ...any) any {
	for _, value := range values {
		if value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		default:
			return value
		}
	}
	return nil
}

func quotaHealthMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case gin.H:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	case map[string]map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	default:
		return nil
	}
}

func quotaHealthNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		parsed, errParse := typed.Float64()
		return parsed, errParse == nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		parsed, errParse := strconv.ParseFloat(trimmed, 64)
		return parsed, errParse == nil
	default:
		return 0, false
	}
}

func quotaHealthNumberOr(value any, fallback float64) float64 {
	parsed, ok := quotaHealthNumber(value)
	if !ok {
		return fallback
	}
	return parsed
}

func clampQuotaHealthPercent(value float64, fallback float64) float64 {
	if value != value {
		value = fallback
	}
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func roundQuotaHealth(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}

func trimQuotaHealthError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	runes := []rune(text)
	if len(runes) > 180 {
		return string(runes[:180])
	}
	return text
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func avgFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func minSliceFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	out := values[0]
	for _, value := range values[1:] {
		if value < out {
			out = value
		}
	}
	return out
}
