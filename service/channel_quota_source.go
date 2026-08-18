package service

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	channelQuotaSourceInfoKey    = "quota_source"
	cliproxyCPAPlusUSDPerPercent = 0.77
	cliproxyCPAProUSDPerPercent  = 15.43
)

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

// EstimatedQuotaPoolSummary exposes the total and currently spendable shared
// balances derived from channel quota_source metadata. It keeps the legacy
// cliproxy percentage estimate while also including concrete sources such as
// ASXS daily quota.
type EstimatedQuotaPoolSummary struct {
	Source                string             `json:"source"`
	Group                 string             `json:"group"`
	ChannelCount          int                `json:"channel_count"`
	AvailableChannelCount int                `json:"available_channel_count"`
	FailedChannelCount    int                `json:"failed_channel_count"`
	EstimatedUSD          float64            `json:"estimated_usd"`
	UsableEstimatedUSD    float64            `json:"usable_estimated_usd"`
	RemainingQuota        int64              `json:"remaining_quota"`
	QuotaPerUSD           float64            `json:"quota_per_usd"`
	EstimateRates         map[string]float64 `json:"estimate_rates"`
	UpdatedAt             int64              `json:"updated_at"`
	Estimated             bool               `json:"estimated"`
	EstimationBasis       string             `json:"estimation_basis"`
	Partial               bool               `json:"partial"`
	GroupBreakdown        []QuotaPoolGroup   `json:"group_breakdown,omitempty"`
}

type QuotaPoolGroup struct {
	Group                 string  `json:"group"`
	ChannelCount          int     `json:"channel_count"`
	AvailableChannelCount int     `json:"available_channel_count"`
	FailedChannelCount    int     `json:"failed_channel_count"`
	EstimatedUSD          float64 `json:"estimated_usd"`
	UsableEstimatedUSD    float64 `json:"usable_estimated_usd"`
	RemainingQuota        int64   `json:"remaining_quota"`
	UpdatedAt             int64   `json:"updated_at"`
	Estimated             bool    `json:"estimated"`
	Partial               bool    `json:"partial"`
}

// DailyQuotaPoolSummary exposes only quota that can be spent during the
// current Asia/Shanghai guard day. Unlike EstimatedQuotaPoolSummary, it does
// not treat a CPA rolling-week balance as today's available balance.
type DailyQuotaPoolSummary struct {
	Source                string                `json:"source"`
	Group                 string                `json:"group"`
	ChannelCount          int                   `json:"channel_count"`
	AvailableChannelCount int                   `json:"available_channel_count"`
	FailedChannelCount    int                   `json:"failed_channel_count"`
	TotalUSD              float64               `json:"total_usd"`
	UsedUSD               float64               `json:"used_usd"`
	RemainingUSD          float64               `json:"remaining_usd"`
	TotalQuota            int64                 `json:"total_quota"`
	UsedQuota             int64                 `json:"used_quota"`
	RemainingQuota        int64                 `json:"remaining_quota"`
	QuotaPerUSD           float64               `json:"quota_per_usd"`
	UpdatedAt             int64                 `json:"updated_at"`
	Estimated             bool                  `json:"estimated"`
	Partial               bool                  `json:"partial"`
	GroupBreakdown        []DailyQuotaPoolGroup `json:"group_breakdown,omitempty"`
}

type DailyQuotaPoolGroup struct {
	Source                      string  `json:"source"`
	Group                       string  `json:"group"`
	ChannelCount                int     `json:"channel_count"`
	TotalUSD                    float64 `json:"total_usd"`
	UsedUSD                     float64 `json:"used_usd"`
	RemainingUSD                float64 `json:"remaining_usd"`
	RemainingQuota              int64   `json:"remaining_quota"`
	NormalTotalUSD              float64 `json:"normal_total_usd,omitempty"`
	NormalUsedUSD               float64 `json:"normal_used_usd,omitempty"`
	NormalRemainingUSD          float64 `json:"normal_remaining_usd,omitempty"`
	NormalRemainingQuota        int64   `json:"normal_remaining_quota,omitempty"`
	UpdatedAt                   int64   `json:"updated_at"`
	Available                   bool    `json:"available"`
	Estimated                   bool    `json:"estimated"`
	Partial                     bool    `json:"partial"`
	ReserveConfigured           bool    `json:"reserve_configured,omitempty"`
	ReserveActive               bool    `json:"reserve_active,omitempty"`
	ReserveRemainingUSD         float64 `json:"reserve_remaining_usd,omitempty"`
	ReservePercent              float64 `json:"reserve_percent,omitempty"`
	ReserveRemainingPercent     float64 `json:"reserve_remaining_percent,omitempty"`
	ReserveTotalUSD             float64 `json:"reserve_total_usd,omitempty"`
	ReserveBucketRemainingUSD   float64 `json:"reserve_bucket_remaining_usd,omitempty"`
	ReserveTotalQuota           int64   `json:"reserve_total_quota,omitempty"`
	ReserveBucketRemainingQuota int64   `json:"reserve_bucket_remaining_quota,omitempty"`
}

func GetDailyQuotaPoolSnapshot(ctx context.Context) (DailyQuotaPoolSummary, bool, error) {
	if model.DB == nil {
		return DailyQuotaPoolSummary{}, false, nil
	}
	asxs, asxsHandled, err := GetASXSChannelBudgetPoolSnapshot(ctx)
	if err != nil {
		return DailyQuotaPoolSummary{}, asxsHandled, err
	}
	var channels []*model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "name", "status", "balance", "balance_updated_time", "other_info", "group").
		Order("id asc").
		Find(&channels).Error; err != nil {
		return DailyQuotaPoolSummary{}, true, err
	}
	quotaPerUSD := common.QuotaPerUnit
	if asxs.QuotaPerUSD > 0 {
		quotaPerUSD = asxs.QuotaPerUSD
	}
	summary := summarizeDailyQuotaPool(asxs, asxsHandled, channels, quotaPerUSD)
	return summary, summary.ChannelCount > 0, nil
}

func summarizeDailyQuotaPool(asxs ChannelBudgetPoolSummary, asxsHandled bool, channels []*model.Channel, quotaPerUSD float64) DailyQuotaPoolSummary {
	if quotaPerUSD <= 0 {
		quotaPerUSD = common.QuotaPerUnit
	}
	summary := DailyQuotaPoolSummary{
		Source:      "daily_spendable_quota_pool",
		QuotaPerUSD: quotaPerUSD,
	}
	groups := map[string]struct{}{}
	if asxsHandled && asxs.ChannelCount > 0 {
		group := DailyQuotaPoolGroup{
			Source:       "asxs_daily_subscription",
			Group:        defaultString(asxs.Group, "asxs"),
			ChannelCount: asxs.ChannelCount,
			TotalUSD:     asxs.TotalUSD,
			UsedUSD:      asxs.UsedUSD,
			RemainingUSD: asxs.RemainingUSD,
			UpdatedAt:    asxs.UpdatedAt,
			Available:    asxs.AvailableChannelCount > 0 && asxs.RemainingUSD > 0,
			Partial:      asxs.Partial || asxs.FailedChannelCount > 0,
		}
		appendDailyQuotaPoolGroup(&summary, &group, quotaPerUSD)
		groups[group.Group] = struct{}{}
		summary.AvailableChannelCount += asxs.AvailableChannelCount
		summary.FailedChannelCount += asxs.FailedChannelCount
	}
	for _, channel := range channels {
		group, ok := cliproxyCPADailyQuotaPoolGroup(channel)
		if !ok {
			continue
		}
		appendDailyQuotaPoolGroup(&summary, &group, quotaPerUSD)
		groups[group.Group] = struct{}{}
		if group.Available {
			summary.AvailableChannelCount++
		}
		if group.Partial {
			summary.FailedChannelCount++
		}
	}
	summary.Group = joinQuotaPoolGroups(groups)
	summary.TotalUSD = roundFloat(summary.TotalUSD, 6)
	summary.UsedUSD = roundFloat(summary.UsedUSD, 6)
	summary.RemainingUSD = roundFloat(summary.RemainingUSD, 6)
	summary.TotalQuota = int64(math.Round(summary.TotalUSD * quotaPerUSD))
	summary.UsedQuota = int64(math.Round(summary.UsedUSD * quotaPerUSD))
	summary.RemainingQuota = int64(math.Round(summary.RemainingUSD * quotaPerUSD))
	summary.Partial = summary.FailedChannelCount > 0
	return summary
}

func appendDailyQuotaPoolGroup(summary *DailyQuotaPoolSummary, group *DailyQuotaPoolGroup, quotaPerUSD float64) {
	if summary == nil || group == nil {
		return
	}
	group.TotalUSD = roundFloat(math.Max(group.TotalUSD, 0), 6)
	group.UsedUSD = roundFloat(math.Max(group.UsedUSD, 0), 6)
	group.RemainingUSD = roundFloat(math.Max(group.RemainingUSD, 0), 6)
	if group.NormalTotalUSD == 0 && group.NormalUsedUSD == 0 && group.NormalRemainingUSD == 0 {
		group.NormalTotalUSD = group.TotalUSD
		group.NormalUsedUSD = group.UsedUSD
		group.NormalRemainingUSD = group.RemainingUSD
	}
	group.NormalTotalUSD = roundFloat(math.Max(group.NormalTotalUSD, 0), 6)
	group.NormalUsedUSD = roundFloat(math.Max(group.NormalUsedUSD, 0), 6)
	group.NormalRemainingUSD = roundFloat(math.Max(group.NormalRemainingUSD, 0), 6)
	group.RemainingQuota = int64(math.Round(group.RemainingUSD * quotaPerUSD))
	group.NormalRemainingQuota = int64(math.Round(group.NormalRemainingUSD * quotaPerUSD))
	group.ReserveTotalQuota = int64(math.Round(math.Max(group.ReserveTotalUSD, 0) * quotaPerUSD))
	group.ReserveBucketRemainingQuota = int64(math.Round(math.Max(group.ReserveBucketRemainingUSD, 0) * quotaPerUSD))
	summary.ChannelCount += group.ChannelCount
	summary.TotalUSD += group.TotalUSD
	summary.UsedUSD += group.UsedUSD
	summary.RemainingUSD += group.RemainingUSD
	if group.UpdatedAt > summary.UpdatedAt {
		summary.UpdatedAt = group.UpdatedAt
	}
	summary.Estimated = summary.Estimated || group.Estimated
	summary.GroupBreakdown = append(summary.GroupBreakdown, *group)
}

func cliproxyCPADailyQuotaPoolGroup(channel *model.Channel) (DailyQuotaPoolGroup, bool) {
	if channel == nil || cliproxyCPAChannelIsModelQuota(channel) {
		return DailyQuotaPoolGroup{}, false
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	guardInfo, ok := otherInfo["cliproxy_cpa_quota_guard"].(map[string]interface{})
	if !ok {
		return DailyQuotaPoolGroup{}, false
	}
	health, ok := guardInfo["health"].(map[string]interface{})
	if !ok {
		return DailyQuotaPoolGroup{}, false
	}
	daily, ok := health["dynamic_daily_budget"].(map[string]interface{})
	if !ok {
		return DailyQuotaPoolGroup{}, false
	}
	if applied, exists := guardObjectBool(daily, "applied"); exists && !applied {
		return DailyQuotaPoolGroup{}, false
	}
	limitPercent, okLimit := guardObjectFloat(daily, "daily_limit_percent")
	remainingPercent, okRemaining := guardObjectFloat(daily, "remaining_today_percent")
	if !okLimit || !okRemaining {
		return DailyQuotaPoolGroup{}, false
	}
	consumedPercent, okConsumed := guardObjectFloat(daily, "consumed_today_percent")
	if !okConsumed {
		consumedPercent = math.Max(limitPercent-remainingPercent, 0)
	}
	rate, okRate := cliproxyCPADailyUSDPerPercent(daily)
	if !okRate {
		return DailyQuotaPoolGroup{}, false
	}
	failed := false
	if healthy, exists := guardObjectBool(health, "ok"); exists && !healthy {
		failed = true
	}
	groupName := strings.TrimSpace(channel.Group)
	if groupName == "" {
		groupName = "cliproxy-codex"
	}
	updatedAt := channel.BalanceUpdatedTime
	if value, ok := guardObjectInt64(guardInfo, "updated_at"); ok && value > updatedAt {
		updatedAt = value
	}
	remainingPercent = math.Min(math.Max(remainingPercent, 0), math.Max(limitPercent, 0))
	consumedPercent = math.Min(math.Max(consumedPercent, 0), math.Max(limitPercent, 0))
	reserveActive, _ := guardObjectBool(daily, "model_reserve_active")
	modelReservePercent, _ := guardObjectFloat(daily, "model_reserve_percent")
	modelReserveRemainingPercent, _ := guardObjectFloat(daily, "model_reserve_remaining_percent")
	modelReservePercent = math.Max(modelReservePercent, 0)
	modelReserveRemainingPercent = math.Min(math.Max(modelReserveRemainingPercent, 0), modelReservePercent)
	// New guard output carries an explicit configured flag so a non-zero
	// percentage without a model allowlist is not shown as spendable reserve.
	// Older guard output has no flag; retain the percentage-based fallback for
	// compatibility with already stored channel metadata.
	reserveConfigured, hasReserveConfigured := guardObjectBool(daily, "model_reserve_configured")
	if !hasReserveConfigured {
		reserveConfigured = modelReservePercent > 0.000001
	}
	if !reserveConfigured {
		modelReservePercent = 0
		modelReserveRemainingPercent = 0
		reserveActive = false
	}
	normalTotalUSD := math.Max(limitPercent, 0) * rate
	normalUsedUSD := consumedPercent * rate
	normalRemainingUSD := remainingPercent * rate
	reserveTotalUSD := modelReservePercent * rate
	reserveBucketRemainingUSD := modelReserveRemainingPercent * rate
	reserveRemainingUSD := 0.0
	if reserveActive && channel.Status == common.ChannelStatusEnabled && !failed {
		reserveRemainingUnits, ok := guardObjectFloat(daily, "model_reserve_remaining_percent")
		if !ok {
			reserveRemainingUnits, ok = guardObjectFloat(health, "usable_balance_units")
		}
		if !ok {
			reserveRemainingUnits = math.Max(channel.Balance, 0)
		}
		reserveRemainingUSD = math.Max(reserveRemainingUnits, 0) * rate
	}
	return DailyQuotaPoolGroup{
		Source:                      "cliproxy_cpa_dynamic_daily_budget",
		Group:                       groupName,
		ChannelCount:                1,
		TotalUSD:                    normalTotalUSD,
		UsedUSD:                     normalUsedUSD,
		RemainingUSD:                normalRemainingUSD + reserveRemainingUSD,
		NormalTotalUSD:              normalTotalUSD,
		NormalUsedUSD:               normalUsedUSD,
		NormalRemainingUSD:          normalRemainingUSD,
		UpdatedAt:                   updatedAt,
		Available:                   channel.Status == common.ChannelStatusEnabled && !failed && (remainingPercent > 0 || reserveRemainingUSD > 0 || reserveBucketRemainingUSD > 0),
		Estimated:                   true,
		Partial:                     failed,
		ReserveConfigured:           reserveConfigured,
		ReserveActive:               reserveActive && reserveRemainingUSD > 0,
		ReserveRemainingUSD:         reserveRemainingUSD,
		ReservePercent:              modelReservePercent,
		ReserveRemainingPercent:     modelReserveRemainingPercent,
		ReserveTotalUSD:             reserveTotalUSD,
		ReserveBucketRemainingUSD:   reserveBucketRemainingUSD,
		ReserveTotalQuota:           int64(math.Round(reserveTotalUSD * common.QuotaPerUnit)),
		ReserveBucketRemainingQuota: int64(math.Round(reserveBucketRemainingUSD * common.QuotaPerUnit)),
	}, true
}

func cliproxyCPADailyUSDPerPercent(daily map[string]interface{}) (float64, bool) {
	plans, _ := daily["baseline_account_plans"].([]interface{})
	if len(plans) == 0 {
		plans, _ = daily["account_plans"].([]interface{})
	}
	if len(plans) == 0 {
		return 0, false
	}
	weightedRate := 0.0
	weightTotal := 0.0
	reserveTotal, _ := guardObjectFloat(daily, "reserve_percent")
	reservePerAccount := math.Max(reserveTotal, 0) / float64(len(plans))
	for _, raw := range plans {
		plan, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		planType, _ := plan["plan_type"].(string)
		remaining, okRemaining := guardObjectFloat(plan, "remaining_percent")
		days, okDays := guardObjectFloat(plan, "days_remaining")
		if !okRemaining || !okDays || days <= 0 {
			continue
		}
		weight := math.Max(remaining-reservePerAccount, 0) / days
		if weight <= 0 {
			continue
		}
		weightedRate += weight * cliproxyCPAUSDPerPercent(planType)
		weightTotal += weight
	}
	if weightTotal <= 0 {
		return 0, false
	}
	return weightedRate / weightTotal, true
}

func GetEstimatedQuotaPoolSnapshot(ctx context.Context) (EstimatedQuotaPoolSummary, bool, error) {
	if model.DB == nil {
		return EstimatedQuotaPoolSummary{}, false, nil
	}
	var channels []*model.Channel
	err := model.DB.WithContext(ctx).
		Select("id", "name", "status", "balance", "balance_updated_time", "other_info", "group").
		Order("id asc").
		Find(&channels).Error
	if err != nil {
		return EstimatedQuotaPoolSummary{}, false, err
	}
	summary := summarizeEstimatedQuotaPool(channels, common.QuotaPerUnit)
	return summary, summary.ChannelCount > 0, nil
}

func summarizeEstimatedQuotaPool(channels []*model.Channel, quotaPerUSD float64) EstimatedQuotaPoolSummary {
	if quotaPerUSD <= 0 {
		quotaPerUSD = common.QuotaPerUnit
	}
	summary := EstimatedQuotaPoolSummary{
		Source:      "unified_quota_source_pool",
		QuotaPerUSD: quotaPerUSD,
		EstimateRates: map[string]float64{
			"plus": cliproxyCPAPlusUSDPerPercent,
			"pro":  cliproxyCPAProUSDPerPercent,
		},
		EstimationBasis: "quota_source_balance",
	}
	groups := map[string]struct{}{}
	groupBreakdown := map[string]*QuotaPoolGroup{}
	for _, channel := range channels {
		balance, usableBalance, updatedAt, available, failed, estimated, ok := estimatedQuotaPoolChannelBalance(channel)
		if !ok {
			continue
		}
		summary.ChannelCount++
		group := strings.TrimSpace(channel.Group)
		if group == "" {
			group = "default"
		}
		groups[group] = struct{}{}
		groupSummary := groupBreakdown[group]
		if groupSummary == nil {
			groupSummary = &QuotaPoolGroup{Group: group}
			groupBreakdown[group] = groupSummary
		}
		groupSummary.ChannelCount++
		if available {
			summary.AvailableChannelCount++
			groupSummary.AvailableChannelCount++
		}
		if failed {
			summary.FailedChannelCount++
			groupSummary.FailedChannelCount++
		} else {
			summary.EstimatedUSD += balance
			summary.UsableEstimatedUSD += usableBalance
			groupSummary.EstimatedUSD += balance
			groupSummary.UsableEstimatedUSD += usableBalance
		}
		if estimated {
			summary.Estimated = true
			groupSummary.Estimated = true
		}
		if updatedAt > summary.UpdatedAt {
			summary.UpdatedAt = updatedAt
		}
		if updatedAt > groupSummary.UpdatedAt {
			groupSummary.UpdatedAt = updatedAt
		}
	}
	summary.Group = joinQuotaPoolGroups(groups)
	if summary.Estimated {
		summary.EstimationBasis = "quota_source_balance_with_plan_weighted_estimates"
	}
	summary.EstimatedUSD = roundFloat(summary.EstimatedUSD, 6)
	summary.UsableEstimatedUSD = roundFloat(summary.UsableEstimatedUSD, 6)
	summary.RemainingQuota = int64(math.Round(summary.EstimatedUSD * quotaPerUSD))
	summary.Partial = summary.FailedChannelCount > 0
	summary.GroupBreakdown = summarizeQuotaPoolGroups(groupBreakdown, quotaPerUSD)
	return summary
}

func summarizeQuotaPoolGroups(groups map[string]*QuotaPoolGroup, quotaPerUSD float64) []QuotaPoolGroup {
	if len(groups) == 0 {
		return nil
	}
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	result := make([]QuotaPoolGroup, 0, len(names))
	for _, group := range names {
		item := *groups[group]
		item.EstimatedUSD = roundFloat(item.EstimatedUSD, 6)
		item.UsableEstimatedUSD = roundFloat(item.UsableEstimatedUSD, 6)
		item.RemainingQuota = int64(math.Round(item.EstimatedUSD * quotaPerUSD))
		item.Partial = item.FailedChannelCount > 0
		result = append(result, item)
	}
	return result
}

func joinQuotaPoolGroups(groups map[string]struct{}) string {
	if len(groups) == 0 {
		return ""
	}
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func estimatedQuotaPoolChannelBalance(channel *model.Channel) (float64, float64, int64, bool, bool, bool, bool) {
	if cliproxyCPAChannelIsModelQuota(channel) {
		return 0, 0, 0, false, false, false, false
	}
	if balance, usableBalance, updatedAt, available, failed, ok := cliproxyCPAEstimatedChannelBalance(channel); ok {
		return balance, usableBalance, updatedAt, available, failed, true, true
	}
	balance, usableBalance, updatedAt, available, failed, ok := quotaSourceChannelBalance(channel)
	return balance, usableBalance, updatedAt, available, failed, false, ok
}

func cliproxyCPAChannelIsModelQuota(channel *model.Channel) bool {
	if channel == nil {
		return false
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	guardInfo, _ := otherInfo["cliproxy_cpa_quota_guard"].(map[string]interface{})
	quotaSource, _ := otherInfo[channelQuotaSourceInfoKey].(map[string]interface{})
	return cliproxyCPAIsModelQuota(guardInfo, quotaSource)
}

func quotaSourceChannelBalance(channel *model.Channel) (float64, float64, int64, bool, bool, bool) {
	if channel == nil {
		return 0, 0, 0, false, false, false
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	quotaSource, ok := otherInfo[channelQuotaSourceInfoKey].(map[string]interface{})
	if !ok {
		return 0, 0, 0, false, false, false
	}
	balance := math.Max(channel.Balance, 0)
	if value, ok := guardObjectFloat(quotaSource, "balance"); ok {
		balance = math.Max(value, 0)
	}
	updatedAt := channel.BalanceUpdatedTime
	if value, ok := guardObjectInt64(quotaSource, "updated_at"); ok && value > updatedAt {
		updatedAt = value
	}
	spendable := balance > 0
	if value, ok := guardObjectBool(quotaSource, "spendable"); ok {
		spendable = value
	}
	status := ""
	if value, ok := quotaSource["status"].(string); ok {
		status = strings.ToLower(strings.TrimSpace(value))
	}
	failed := status == "unknown" || status == "error" || status == "unavailable"
	available := spendable && !failed && channel.Status == common.ChannelStatusEnabled
	usableBalance := balance
	if !spendable {
		usableBalance = 0
	}
	return balance, usableBalance, updatedAt, available, failed, true
}

func cliproxyCPAEstimatedChannelBalance(channel *model.Channel) (float64, float64, int64, bool, bool, bool) {
	if channel == nil {
		return 0, 0, 0, false, false, false
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	guardInfo, hasGuard := otherInfo["cliproxy_cpa_quota_guard"].(map[string]interface{})
	quotaSource, hasQuotaSource := otherInfo[channelQuotaSourceInfoKey].(map[string]interface{})
	if cliproxyCPAIsModelQuota(guardInfo, quotaSource) {
		return 0, 0, 0, false, false, false
	}
	isCPA := hasGuard
	if rawSource, ok := quotaSource["raw_source"].(map[string]interface{}); ok {
		if source, ok := rawSource["source"].(string); ok && strings.EqualFold(strings.TrimSpace(source), "cliproxy_cpa_quota_guard") {
			isCPA = true
		}
	}
	if !isCPA {
		return 0, 0, 0, false, false, false
	}

	usableBalance := math.Max(channel.Balance, 0)
	displayBalance := usableBalance
	usableDisplayBalance := usableBalance
	updatedAt := channel.BalanceUpdatedTime
	spendable := usableBalance > 0
	failed := false
	if hasQuotaSource {
		if value, ok := guardObjectFloat(quotaSource, "balance"); ok {
			usableBalance = math.Max(value, 0)
			displayBalance = usableBalance
			usableDisplayBalance = usableBalance
		}
		if value, ok := guardObjectInt64(quotaSource, "updated_at"); ok && value > updatedAt {
			updatedAt = value
		}
		if value, ok := guardObjectBool(quotaSource, "spendable"); ok {
			spendable = value
		}
		if status, ok := quotaSource["status"].(string); ok && strings.EqualFold(strings.TrimSpace(status), "unknown") {
			failed = true
		}
	}
	if hasGuard {
		if value, ok := guardObjectInt64(guardInfo, "updated_at"); ok && value > updatedAt {
			updatedAt = value
		}
		if health, ok := guardInfo["health"].(map[string]interface{}); ok {
			if rawEstimate, usableEstimate, ok := cliproxyCPAPlanWeightedEstimate(health); ok {
				displayBalance = rawEstimate
				usableDisplayBalance = usableEstimate
			} else {
				if value, ok := guardObjectFloat(health, "total_balance_units"); ok {
					displayBalance = math.Max(value, 0)
				}
				if value, ok := guardObjectFloat(health, "usable_balance_units"); ok {
					usableDisplayBalance = math.Max(value, 0)
				}
			}
			if value, ok := guardObjectFloat(health, "usable_balance_units"); ok {
				usableBalance = math.Max(value, 0)
			}
			if value, exists := guardObjectBool(health, "ok"); exists && !value {
				failed = true
			}
		}
	}
	available := channel.Status == common.ChannelStatusEnabled && spendable && usableBalance > 0
	return displayBalance, usableDisplayBalance, updatedAt, available, failed, true
}

func cliproxyCPAIsModelQuota(guardInfo map[string]interface{}, quotaSource map[string]interface{}) bool {
	if sourceType, ok := quotaSource["source_type"].(string); ok && strings.EqualFold(strings.TrimSpace(sourceType), "model_quota_percent") {
		return true
	}
	if rawSource, ok := quotaSource["raw_source"].(map[string]interface{}); ok {
		if feature, ok := rawSource["quota_feature"].(string); ok && strings.TrimSpace(feature) != "" {
			return true
		}
	}
	health, _ := guardInfo["health"].(map[string]interface{})
	if feature, ok := health["quota_feature"].(string); ok && strings.TrimSpace(feature) != "" {
		return true
	}
	return false
}

func cliproxyCPAPlanWeightedEstimate(health map[string]interface{}) (float64, float64, bool) {
	accounts, ok := health["accounts"].([]interface{})
	if !ok || len(accounts) == 0 {
		return 0, 0, false
	}
	rawEstimate := 0.0
	usableEstimate := 0.0
	count := 0
	for _, item := range accounts {
		account, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if value, exists := guardObjectBool(account, "ok"); exists && !value {
			continue
		}
		rawRemaining, okRaw := guardObjectFloat(account, "raw_remaining_percent")
		usableRemaining, okUsable := guardObjectFloat(account, "remaining_share_percent")
		if !okRaw && !okUsable {
			continue
		}
		if !okRaw {
			rawRemaining = usableRemaining
		}
		if !okUsable {
			usableRemaining = rawRemaining
		}
		planType, _ := account["plan_type"].(string)
		rate := cliproxyCPAUSDPerPercent(planType)
		rawEstimate += math.Max(rawRemaining, 0) * rate
		usableEstimate += math.Max(usableRemaining, 0) * rate
		count++
	}
	if count == 0 {
		return 0, 0, false
	}
	return roundFloat(rawEstimate, 6), roundFloat(usableEstimate, 6), true
}

func cliproxyCPAUSDPerPercent(planType string) float64 {
	normalized := strings.ToLower(strings.TrimSpace(planType))
	switch {
	case strings.Contains(normalized, "pro"):
		return cliproxyCPAProUSDPerPercent
	case strings.Contains(normalized, "plus"):
		return cliproxyCPAPlusUSDPerPercent
	default:
		return 1
	}
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
	quotaFeature, _ := health["quota_feature"].(string)
	quotaFeature = strings.TrimSpace(quotaFeature)
	if quotaFeature != "" {
		sourceType = "model_quota_percent"
		unit = "percent"
		reservePolicy = nil
	} else if cliproxyCPAHasProtectedBucket(health) || len(reservePolicy) > 0 {
		sourceType = "shared_protected_rolling_quota"
	}
	rawSource := map[string]interface{}{"source": "cliproxy_cpa_quota_guard"}
	if quotaFeature != "" {
		rawSource["quota_feature"] = quotaFeature
		if limitName, ok := health["quota_feature_limit_name"].(string); ok && strings.TrimSpace(limitName) != "" {
			rawSource["quota_feature_limit_name"] = strings.TrimSpace(limitName)
		}
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
		rawSource,
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
