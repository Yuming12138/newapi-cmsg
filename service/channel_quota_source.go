package service

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const channelQuotaSourceInfoKey = "quota_source"

type usageBalanceQuota struct {
	Limit     interface{} `json:"limit"`
	Used      interface{} `json:"used"`
	Remaining interface{} `json:"remaining"`
	Unit      string      `json:"unit"`
	ResetAt   string      `json:"reset_at"`
}

type usageBalanceRateLimit struct {
	Window    string      `json:"window"`
	Limit     interface{} `json:"limit"`
	Used      interface{} `json:"used"`
	Remaining interface{} `json:"remaining"`
	ResetAt   string      `json:"reset_at"`
}

type usageBalanceResult struct {
	Balance  float64
	Response usageBalanceResponse
}

type newAPIBalanceResult struct {
	Balance float64
	Quota   int64
	Status  newAPIStatusBalanceResponse
}

func buildQuotaSource(sourceType string, unit string, balance float64, windows []map[string]interface{}, spendable bool, status string, statusReason string, reservePolicy map[string]interface{}, updatedAt int64, rawSource map[string]interface{}) map[string]interface{} {
	if unit == "" {
		unit = "USD"
	}
	if status == "" {
		if spendable {
			status = "available"
		} else {
			status = "exhausted"
		}
	}
	result := map[string]interface{}{
		"source_type":   sourceType,
		"unit":          unit,
		"balance":       roundFloat(math.Max(balance, 0), 6),
		"windows":       windows,
		"spendable":     spendable,
		"status":        status,
		"status_reason": statusReason,
		"updated_at":    updatedAt,
	}
	if reservePolicy != nil {
		result["reserve_policy"] = reservePolicy
	}
	if rawSource != nil {
		result["raw_source"] = rawSource
	}
	return result
}

func quotaSourceWindow(name string, unit string, remaining float64, limit float64, used float64, resetAt string) map[string]interface{} {
	window := map[string]interface{}{
		"name":      name,
		"unit":      unit,
		"remaining": roundFloat(math.Max(remaining, 0), 6),
	}
	if limit > 0 {
		window["limit"] = roundFloat(limit, 6)
		window["remaining_percent"] = roundFloat(math.Max(0, remaining)/limit*100, 4)
	}
	if used >= 0 {
		window["used"] = roundFloat(math.Max(used, 0), 6)
	}
	if strings.TrimSpace(resetAt) != "" {
		window["reset_at"] = strings.TrimSpace(resetAt)
	}
	return window
}

func buildASXSQuotaSource(usage asxsUsageResult, remainingUSD float64, nowTs int64) map[string]interface{} {
	unit := strings.TrimSpace(usage.Unit)
	if unit == "" {
		unit = "USD"
	}
	window := quotaSourceWindow("1d", unit, remainingUSD, usage.TotalUSD, usage.UsedUSD, "")
	if usage.ResetInfo != "" {
		window["reset_info"] = usage.ResetInfo
	}
	return buildQuotaSource(
		"daily_subscription_usd",
		unit,
		remainingUSD,
		[]map[string]interface{}{window},
		remainingUSD > 0,
		quotaSourceAvailabilityStatus(remainingUSD > 0),
		budgetReason(remainingUSD > 0),
		nil,
		nowTs,
		map[string]interface{}{
			"source":    "asxs_usage",
			"plan_name": usage.PlanName,
			"raw_items": usage.RawItems,
		},
	)
}

func buildInternalQuotaLedgerSource(mode string, limitUSD float64, usedQuota int64, quotaPerUSD float64, remainingUSD float64, nowTs int64, reason string) map[string]interface{} {
	usedUSD := 0.0
	if quotaPerUSD > 0 {
		usedUSD = float64(usedQuota) / quotaPerUSD
	}
	return buildQuotaSource(
		"internal_quota_ledger",
		"USD",
		remainingUSD,
		[]map[string]interface{}{quotaSourceWindow(mode, "USD", remainingUSD, limitUSD, usedUSD, "")},
		remainingUSD > 0,
		quotaSourceAvailabilityStatus(remainingUSD > 0),
		reason,
		nil,
		nowTs,
		map[string]interface{}{"source": "channel_budget_guard", "mode": mode},
	)
}

func ParseUsageBalanceQuotaSource(raw []byte, nowTs int64) (float64, map[string]interface{}, error) {
	result, err := parseUsageBalanceResult(raw)
	if err != nil {
		return 0, nil, err
	}
	return result.Balance, buildUsageBalanceQuotaSource(result.Response, result.Balance, nowTs), nil
}

func buildUsageBalanceQuotaSource(response usageBalanceResponse, balance float64, nowTs int64) map[string]interface{} {
	unit := strings.TrimSpace(response.Unit)
	if unit == "" {
		unit = "USD"
	}
	windows := make([]map[string]interface{}, 0, len(response.RateLimits)+1)
	if remaining, ok := interfaceToFloat64(response.Quota.Remaining); ok {
		limit, _ := interfaceToFloat64(response.Quota.Limit)
		used, okUsed := interfaceToFloat64(response.Quota.Used)
		if !okUsed && limit > 0 {
			used = math.Max(limit-remaining, 0)
		}
		windowUnit := defaultString(response.Quota.Unit, unit)
		windows = append(windows, quotaSourceWindow("period", windowUnit, remaining, limit, used, response.Quota.ResetAt))
	}
	for _, rateLimit := range response.RateLimits {
		remaining, ok := interfaceToFloat64(rateLimit.Remaining)
		if !ok {
			continue
		}
		limit, _ := interfaceToFloat64(rateLimit.Limit)
		used, okUsed := interfaceToFloat64(rateLimit.Used)
		if !okUsed && limit > 0 {
			used = math.Max(limit-remaining, 0)
		}
		name := strings.TrimSpace(rateLimit.Window)
		if name == "" {
			name = "rate_limit"
		}
		windows = append(windows, quotaSourceWindow(name, unit, remaining, limit, used, rateLimit.ResetAt))
	}
	sourceType := "stored_value_usd"
	if len(windows) > 0 && len(response.RateLimits) > 0 {
		sourceType = "period_cap_with_daily_limit"
	}
	return buildQuotaSource(
		sourceType,
		unit,
		balance,
		windows,
		balance > 0,
		quotaSourceAvailabilityStatus(balance > 0),
		budgetReason(balance > 0),
		nil,
		nowTs,
		map[string]interface{}{
			"source":    "usage_balance",
			"mode":      response.Mode,
			"plan_name": response.PlanName,
		},
	)
}

func BuildNewAPIStoredValueQuotaSource(balance float64, quota int64, quotaPerUnit float64, displayType string, nowTs int64) map[string]interface{} {
	return buildQuotaSource(
		"stored_value_usd",
		"USD",
		balance,
		nil,
		balance > 0,
		quotaSourceAvailabilityStatus(balance > 0),
		budgetReason(balance > 0),
		nil,
		nowTs,
		map[string]interface{}{
			"source":             "new_api_user_self",
			"quota":              quota,
			"quota_per_unit":     quotaPerUnit,
			"quota_display_type": displayType,
		},
	)
}

func buildNewAPIBalanceQuotaSource(result newAPIBalanceResult, nowTs int64) map[string]interface{} {
	return BuildNewAPIStoredValueQuotaSource(
		result.Balance,
		result.Quota,
		result.Status.Data.QuotaPerUnit,
		result.Status.Data.QuotaDisplayType,
		nowTs,
	)
}

func buildCliproxyCPAQuotaSource(health map[string]interface{}, balance float64, nowTs int64) map[string]interface{} {
	if health == nil {
		return nil
	}
	unit := "quota_unit"
	if rawUnit, ok := health["unit"].(string); ok && strings.TrimSpace(rawUnit) != "" {
		unit = strings.TrimSpace(rawUnit)
	}
	windows := buildCliproxyCPAQuotaWindows(health)
	reservePolicy := cliproxyCPAReservePolicy(health)
	spendable := balance > 0
	status, reason := cliproxyCPAQuotaSourceStatus(health, spendable)
	sourceType := "rolling_window_quota"
	if cliproxyCPAHasProtectedBucket(health) || len(reservePolicy) > 0 {
		sourceType = "shared_protected_rolling_quota"
	}
	return buildQuotaSource(
		sourceType,
		unit,
		balance,
		windows,
		spendable && status == "available",
		status,
		reason,
		reservePolicy,
		nowTs,
		map[string]interface{}{"source": "cliproxy_cpa_quota_guard"},
	)
}

func buildCliproxyCPAQuotaWindows(health map[string]interface{}) []map[string]interface{} {
	windowsObj, _ := health["windows"].(map[string]interface{})
	if len(windowsObj) == 0 {
		return nil
	}
	order := []string{"5h", "7d"}
	result := make([]map[string]interface{}, 0, len(windowsObj))
	seen := map[string]struct{}{}
	appendWindow := func(name string, raw interface{}) {
		windowObj, ok := raw.(map[string]interface{})
		if !ok {
			return
		}
		window := map[string]interface{}{"name": name, "unit": "percent"}
		if value, ok := guardObjectFloat(windowObj, "remaining_percent"); ok {
			window["remaining_percent"] = roundFloat(math.Max(value, 0), 4)
			window["remaining"] = roundFloat(math.Max(value, 0), 4)
		}
		if value, ok := guardObjectFloat(windowObj, "used_percent"); ok {
			window["used_percent"] = roundFloat(math.Max(value, 0), 4)
			window["used"] = roundFloat(math.Max(value, 0), 4)
		}
		if value, ok := guardObjectFloat(windowObj, "reset_after_seconds"); ok {
			window["reset_after_seconds"] = int64(math.Round(value))
		}
		if value, ok := windowObj["reset_at"].(string); ok && strings.TrimSpace(value) != "" {
			window["reset_at"] = strings.TrimSpace(value)
		}
		result = append(result, window)
		seen[name] = struct{}{}
	}
	for _, name := range order {
		appendWindow(name, windowsObj[name])
	}
	for name, raw := range windowsObj {
		if _, ok := seen[name]; ok {
			continue
		}
		appendWindow(name, raw)
	}
	return result
}

func cliproxyCPAReservePolicy(health map[string]interface{}) map[string]interface{} {
	policy := map[string]interface{}{}
	buckets, _ := health["buckets"].(map[string]interface{})
	for _, raw := range buckets {
		bucket, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		canExhaust, okCanExhaust := guardObjectBool(bucket, "can_exhaust")
		if okCanExhaust && canExhaust {
			continue
		}
		if value, ok := guardObjectFloat(bucket, "min_remaining_percent_5h"); ok {
			policy["min_remaining_percent_5h"] = value
		}
		if value, ok := guardObjectFloat(bucket, "min_remaining_percent_7d"); ok {
			policy["min_remaining_percent_7d"] = value
		}
	}
	if len(policy) == 0 {
		return nil
	}
	return policy
}

func cliproxyCPAHasProtectedBucket(health map[string]interface{}) bool {
	buckets, _ := health["buckets"].(map[string]interface{})
	for _, raw := range buckets {
		bucket, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if canExhaust, ok := guardObjectBool(bucket, "can_exhaust"); ok && !canExhaust {
			return true
		}
	}
	return false
}

func cliproxyCPAQuotaSourceStatus(health map[string]interface{}, spendable bool) (string, string) {
	if ok, exists := guardObjectBool(health, "ok"); exists && !ok {
		return "unknown", "cpa_health_not_ok"
	}
	if cliproxyCPAProtectedReserveReached(health) {
		return "protected_reserve", "protected_reserve_reached"
	}
	if cliproxyCPAWindowRemainingPercent(health, "7d") <= 0 {
		return "quota_7d_exhausted", "quota_7d_exhausted"
	}
	if cliproxyCPAWindowRemainingPercent(health, "5h") <= 0 {
		return "quota_5h_exhausted", "quota_5h_exhausted"
	}
	if !spendable {
		return "quota_exhausted", "no_spendable_balance"
	}
	return "available", "within_budget"
}

func cliproxyCPAProtectedReserveReached(health map[string]interface{}) bool {
	buckets, _ := health["buckets"].(map[string]interface{})
	for _, raw := range buckets {
		bucket, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		canExhaust, okCanExhaust := guardObjectBool(bucket, "can_exhaust")
		if okCanExhaust && canExhaust {
			continue
		}
		if balance, ok := guardObjectFloat(bucket, "usable_balance_units"); ok && balance <= 0 {
			return true
		}
	}
	return false
}

func cliproxyCPAWindowRemainingPercent(health map[string]interface{}, name string) float64 {
	windows, _ := health["windows"].(map[string]interface{})
	window, _ := windows[name].(map[string]interface{})
	if value, ok := guardObjectFloat(window, "remaining_percent"); ok {
		return value
	}
	return math.Inf(1)
}

func quotaSourceAvailabilityStatus(spendable bool) string {
	if spendable {
		return "available"
	}
	return "quota_exhausted"
}

func UpdateChannelBalanceWithQuotaSource(channel *model.Channel, balance float64, quotaSource map[string]interface{}, nowTs int64) error {
	if channel == nil {
		return nil
	}
	if nowTs <= 0 {
		nowTs = common.GetTimestamp()
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	if quotaSource != nil {
		otherInfo[channelQuotaSourceInfoKey] = quotaSource
	}
	raw, err := common.Marshal(otherInfo)
	if err != nil {
		return err
	}
	channel.Balance = balance
	channel.BalanceUpdatedTime = nowTs
	channel.OtherInfo = string(raw)
	if model.DB == nil {
		return nil
	}
	updates := map[string]interface{}{
		"balance":              balance,
		"balance_updated_time": nowTs,
		"other_info":           string(raw),
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
		return err
	}
	return nil
}
