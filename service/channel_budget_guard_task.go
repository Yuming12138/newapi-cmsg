package service

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	channelBudgetGuardStateOptionKey = "channel_budget_guard_state"
	channelBudgetGuardDefaultRemark  = "余额自动同步自 asxs /api/usage"
)

type channelBudgetGuardState struct {
	Version  int                                       `json:"version"`
	Channels map[string]channelBudgetGuardChannelState `json:"channels"`
}

type channelBudgetGuardChannelState struct {
	LastResetDate   string `json:"last_reset_date,omitempty"`
	DisabledByGuard bool   `json:"disabled_by_guard"`
}

type asxsUsageResult struct {
	PlanName     string
	TotalUSD     float64
	UsedUSD      float64
	RemainingUSD float64
	Unit         string
	ResetInfo    string
	RawItems     int
}

type asxsUsageItem struct {
	PlanName  string      `json:"planName"`
	Unit      string      `json:"unit"`
	IsValid   *bool       `json:"isValid"`
	Total     interface{} `json:"total"`
	Remaining interface{} `json:"remaining"`
	Used      interface{} `json:"used"`
	Extra     string      `json:"extra"`
}

type usageBalanceResponse struct {
	Balance        interface{}             `json:"balance"`
	Remaining      interface{}             `json:"remaining"`
	TotalAvailable interface{}             `json:"total_available"`
	Unit           string                  `json:"unit"`
	Mode           string                  `json:"mode"`
	IsValid        *bool                   `json:"isValid"`
	PlanName       string                  `json:"planName"`
	Quota          usageBalanceQuota       `json:"quota"`
	RateLimits     []usageBalanceRateLimit `json:"rate_limits"`
}

type newAPIUserSelfBalanceResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Quota int64 `json:"quota"`
	} `json:"data"`
}

type newAPIStatusBalanceResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		QuotaPerUnit               float64 `json:"quota_per_unit"`
		QuotaDisplayType           string  `json:"quota_display_type"`
		CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
		USDExchangeRate            float64 `json:"usd_exchange_rate"`
	} `json:"data"`
}

type ChannelBudgetPoolSummary struct {
	Source                string  `json:"source"`
	Group                 string  `json:"group"`
	ChannelCount          int     `json:"channel_count"`
	AvailableChannelCount int     `json:"available_channel_count"`
	BalanceFallbackCount  int     `json:"balance_fallback_count,omitempty"`
	FailedChannelCount    int     `json:"failed_channel_count"`
	TotalUSD              float64 `json:"total_usd"`
	UsedUSD               float64 `json:"used_usd"`
	RemainingUSD          float64 `json:"remaining_usd"`
	RemainingQuota        int64   `json:"remaining_quota"`
	QuotaPerUSD           float64 `json:"quota_per_usd"`
	UpdatedAt             int64   `json:"updated_at"`
	Refreshed             bool    `json:"refreshed"`
	Partial               bool    `json:"partial"`
}

var (
	channelBudgetGuardOnce    sync.Once
	channelBudgetGuardRunning atomic.Bool
)

func StartChannelBudgetGuardTask() {
	channelBudgetGuardOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			cfg := operation_setting.GetChannelBudgetGuardSetting()
			logger.LogInfo(context.Background(), fmt.Sprintf("channel budget guard task started: enabled=%t tick=%s", cfg.Enabled, channelBudgetGuardInterval(cfg)))
			runChannelBudgetGuardOnce()
			for {
				time.Sleep(channelBudgetGuardInterval(operation_setting.GetChannelBudgetGuardSetting()))
				runChannelBudgetGuardOnce()
			}
		})
	})
}

func channelBudgetGuardInterval(cfg *operation_setting.ChannelBudgetGuardSetting) time.Duration {
	if cfg == nil || cfg.TickIntervalMinutes < 1 {
		return time.Minute
	}
	return time.Duration(cfg.TickIntervalMinutes) * time.Minute
}

func channelBudgetGuardQuotaPerUSD(cfg *operation_setting.ChannelBudgetGuardSetting) float64 {
	if cfg == nil || cfg.QuotaPerUSD <= 0 {
		return 500000
	}
	return float64(cfg.QuotaPerUSD)
}

func channelBudgetGuardTimeout(cfg *operation_setting.ChannelBudgetGuardSetting, channelCfg operation_setting.ChannelBudgetGuardChannelSetting) time.Duration {
	seconds := 0
	if channelCfg.UsageTimeoutSecond > 0 {
		seconds = channelCfg.UsageTimeoutSecond
	} else if cfg != nil && cfg.UsageTimeoutSecond > 0 {
		seconds = cfg.UsageTimeoutSecond
	} else {
		seconds = 20
	}
	return time.Duration(seconds) * time.Second
}

func runChannelBudgetGuardOnce() {
	if !channelBudgetGuardRunning.CompareAndSwap(false, true) {
		return
	}
	defer channelBudgetGuardRunning.Store(false)

	ctx := context.Background()
	cfg := operation_setting.GetChannelBudgetGuardSetting()
	if cfg == nil || !cfg.Enabled {
		return
	}

	channels, err := fetchChannelBudgetGuardChannels()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("channel budget guard: query channels failed: %v", err))
		return
	}
	managed := resolveChannelBudgetGuardChannels(cfg, channels)
	if len(managed) == 0 {
		if common.DebugEnabled {
			logger.LogDebug(ctx, "channel budget guard: no managed channels")
		}
		return
	}

	state := loadChannelBudgetGuardState()
	quotaPerUSD := channelBudgetGuardQuotaPerUSD(cfg)
	today := guardDateString(cfg.Timezone)
	nowTs := common.GetTimestamp()
	stateChanged := false
	cacheRefreshNeeded := false
	updated := 0
	failed := 0

	for _, item := range managed {
		channel := item.channel
		channelCfg := item.config
		stateKey := strconv.Itoa(channel.Id)
		stateEntry, stateExists := state.Channels[stateKey]
		if !stateExists {
			stateEntry.LastResetDate = today
			state.Channels[stateKey] = stateEntry
			stateChanged = true
		}
		newState, channelUpdated, statusChanged, err := applyChannelBudgetGuard(ctx, cfg, channel, channelCfg, stateEntry, today, nowTs, quotaPerUSD)
		if err != nil {
			failed++
			logger.LogWarn(ctx, fmt.Sprintf("channel budget guard: channel_id=%d name=%s failed: %v", channel.Id, channel.Name, err))
			continue
		}
		if newState != stateEntry {
			state.Channels[stateKey] = newState
			stateChanged = true
		}
		if channelUpdated {
			updated++
		}
		if statusChanged {
			cacheRefreshNeeded = true
		}
	}

	asxsManaged := filterASXSChannelBudgetGuardChannels(managed)
	failed += refreshUsageBalanceFallbackChannels(ctx, discoverASXSBalanceFallbackChannels(cfg, channels, asxsManaged), channelBudgetGuardTimeout(cfg, operation_setting.ChannelBudgetGuardChannelSetting{}))

	if stateChanged {
		if err := saveJSONOption(channelBudgetGuardStateOptionKey, state); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel budget guard: save state failed: %v", err))
		}
	}
	if cacheRefreshNeeded {
		model.InitChannelCache()
	}
	if failed > 0 || cacheRefreshNeeded {
		logger.LogInfo(ctx, fmt.Sprintf("channel budget guard: managed=%d updated=%d failed=%d", len(managed), updated, failed))
	} else if common.DebugEnabled {
		logger.LogDebug(ctx, "channel budget guard: managed=%d updated=%d failed=%d", len(managed), updated, failed)
	}
}

func UpdateChannelBudgetGuardBalance(ctx context.Context, channel *model.Channel) (float64, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if channel == nil {
		return 0, false, fmt.Errorf("channel is nil")
	}

	cfg := operation_setting.GetChannelBudgetGuardSetting()
	if cfg == nil {
		return 0, false, nil
	}
	managed := resolveChannelBudgetGuardChannels(cfg, []*model.Channel{channel})
	if len(managed) == 0 {
		return 0, false, nil
	}
	item := managed[0]
	source := strings.ToLower(strings.TrimSpace(defaultString(item.config.Source, "local")))
	if source != "asxs_usage" {
		return 0, false, nil
	}

	state := loadChannelBudgetGuardState()
	if state.Channels == nil {
		state.Channels = map[string]channelBudgetGuardChannelState{}
	}
	stateKey := strconv.Itoa(channel.Id)
	stateEntry := state.Channels[stateKey]
	if stateEntry.LastResetDate == "" {
		stateEntry.LastResetDate = guardDateString(cfg.Timezone)
	}

	nowTs := common.GetTimestamp()
	newState, _, statusChanged, err := applyChannelBudgetGuard(ctx, cfg, channel, item.config, stateEntry, guardDateString(cfg.Timezone), nowTs, channelBudgetGuardQuotaPerUSD(cfg))
	if err != nil {
		return 0, true, err
	}
	if previous, exists := state.Channels[stateKey]; !exists || previous != newState {
		state.Channels[stateKey] = newState
		if err := saveJSONOption(channelBudgetGuardStateOptionKey, state); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel budget guard: save state failed after manual balance refresh: %v", err))
		}
	}
	if statusChanged {
		model.InitChannelCache()
	}
	return channel.Balance, true, nil
}

func UpdateCliproxyCPAQuotaGuardBalance(channel *model.Channel) (float64, bool, error) {
	if channel == nil {
		return 0, false, fmt.Errorf("channel is nil")
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	guardInfo, ok := otherInfo["cliproxy_cpa_quota_guard"].(map[string]interface{})
	if !ok {
		return 0, false, nil
	}
	health, _ := guardInfo["health"].(map[string]interface{})
	if health == nil {
		return channel.Balance, true, nil
	}
	nowTs := common.GetTimestamp()
	if balance, ok := cliproxyCPAUsableBalance(health); ok {
		if err := UpdateChannelBalanceWithQuotaSource(channel, balance, buildCliproxyCPAQuotaSource(health, balance, nowTs), nowTs); err != nil {
			return 0, true, err
		}
		return balance, true, nil
	}
	if balance, ok := guardObjectFloat(health, "balance_units"); ok {
		if err := UpdateChannelBalanceWithQuotaSource(channel, balance, buildCliproxyCPAQuotaSource(health, balance, nowTs), nowTs); err != nil {
			return 0, true, err
		}
		return balance, true, nil
	}
	if balance, ok := guardObjectFloat(health, "remaining_share_percent"); ok {
		if err := UpdateChannelBalanceWithQuotaSource(channel, balance, buildCliproxyCPAQuotaSource(health, balance, nowTs), nowTs); err != nil {
			return 0, true, err
		}
		return balance, true, nil
	}
	return channel.Balance, true, nil
}

func cliproxyCPAUsableBalance(health map[string]interface{}) (float64, bool) {
	if balance, ok := guardObjectFloat(health, "usable_balance_units"); ok {
		return math.Max(0, balance), true
	}
	buckets, ok := health["buckets"].(map[string]interface{})
	if !ok || len(buckets) == 0 {
		return 0, false
	}
	total := 0.0
	found := false
	for _, raw := range buckets {
		bucket, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if balance, ok := guardObjectFloat(bucket, "usable_balance_units"); ok {
			total += math.Max(0, balance)
			found = true
			continue
		}
		if canExhaust, ok := guardObjectBool(bucket, "can_exhaust"); ok && canExhaust {
			if balance, ok := guardObjectFloat(bucket, "balance_units"); ok {
				total += math.Max(0, balance)
				found = true
			}
		}
	}
	if !found {
		return 0, false
	}
	return total, true
}

func GetASXSChannelBudgetPoolSnapshot(ctx context.Context) (ChannelBudgetPoolSummary, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := operation_setting.GetChannelBudgetGuardSetting()
	if cfg == nil || !cfg.Enabled {
		return ChannelBudgetPoolSummary{}, false, nil
	}
	channels, err := fetchChannelBudgetGuardChannels()
	if err != nil {
		return ChannelBudgetPoolSummary{}, true, err
	}
	managed := filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels))
	balanceFallbacks := discoverASXSBalanceFallbackChannels(cfg, channels, managed)
	if len(managed) == 0 && len(balanceFallbacks) == 0 {
		return ChannelBudgetPoolSummary{}, false, nil
	}
	return summarizeASXSChannelBudgetPool(cfg, managed, balanceFallbacks, 0, false), true, nil
}

func RefreshASXSChannelBudgetPoolSummary(ctx context.Context) (ChannelBudgetPoolSummary, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !channelBudgetGuardRunning.CompareAndSwap(false, true) {
		summary, handled, err := GetASXSChannelBudgetPoolSnapshot(ctx)
		if handled {
			summary.Partial = true
		}
		return summary, handled, err
	}
	defer channelBudgetGuardRunning.Store(false)
	return refreshASXSChannelBudgetPoolSummaryLocked(ctx)
}

func refreshASXSChannelBudgetPoolSummaryLocked(ctx context.Context) (ChannelBudgetPoolSummary, bool, error) {
	cfg := operation_setting.GetChannelBudgetGuardSetting()
	if cfg == nil || !cfg.Enabled {
		return ChannelBudgetPoolSummary{}, false, nil
	}

	channels, err := fetchChannelBudgetGuardChannels()
	if err != nil {
		return ChannelBudgetPoolSummary{}, true, err
	}
	managed := filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels))
	balanceFallbacks := discoverASXSBalanceFallbackChannels(cfg, channels, managed)
	if len(managed) == 0 && len(balanceFallbacks) == 0 {
		return ChannelBudgetPoolSummary{}, false, nil
	}

	state := loadChannelBudgetGuardState()
	quotaPerUSD := channelBudgetGuardQuotaPerUSD(cfg)
	today := guardDateString(cfg.Timezone)
	nowTs := common.GetTimestamp()
	stateChanged := false
	cacheRefreshNeeded := false
	failed := 0

	for _, item := range managed {
		channel := item.channel
		if channel == nil {
			continue
		}
		stateKey := strconv.Itoa(channel.Id)
		stateEntry, stateExists := state.Channels[stateKey]
		if !stateExists {
			stateEntry.LastResetDate = today
			state.Channels[stateKey] = stateEntry
			stateChanged = true
		}
		newState, _, statusChanged, err := applyChannelBudgetGuard(ctx, cfg, channel, item.config, stateEntry, today, nowTs, quotaPerUSD)
		if err != nil {
			failed++
			logger.LogWarn(ctx, fmt.Sprintf("asxs pool refresh: channel_id=%d name=%s failed: %v", channel.Id, channel.Name, err))
			continue
		}
		if newState != stateEntry {
			state.Channels[stateKey] = newState
			stateChanged = true
		}
		if statusChanged {
			cacheRefreshNeeded = true
		}
	}

	failed += refreshUsageBalanceFallbackChannels(ctx, balanceFallbacks, channelBudgetGuardTimeout(cfg, operation_setting.ChannelBudgetGuardChannelSetting{}))

	if stateChanged {
		if err := saveJSONOption(channelBudgetGuardStateOptionKey, state); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("asxs pool refresh: save state failed: %v", err))
		}
	}
	if cacheRefreshNeeded {
		model.InitChannelCache()
	}
	summary := summarizeASXSChannelBudgetPool(cfg, managed, balanceFallbacks, failed, true)
	if failed > 0 {
		summary.Partial = true
	}
	return summary, true, nil
}

func filterASXSChannelBudgetGuardChannels(managed []channelBudgetManagedChannel) []channelBudgetManagedChannel {
	result := make([]channelBudgetManagedChannel, 0, len(managed))
	for _, item := range managed {
		if isASXSChannelBudgetGuardItem(item) {
			result = append(result, item)
		}
	}
	return result
}

func isASXSChannelBudgetGuardItem(item channelBudgetManagedChannel) bool {
	return strings.EqualFold(strings.TrimSpace(defaultString(item.config.Source, "local")), "asxs_usage")
}

func summarizeASXSChannelBudgetPool(cfg *operation_setting.ChannelBudgetGuardSetting, managed []channelBudgetManagedChannel, balanceFallbacks []*model.Channel, failed int, refreshed bool) ChannelBudgetPoolSummary {
	quotaPerUSD := channelBudgetGuardQuotaPerUSD(cfg)
	group := "asxs"
	if cfg != nil && strings.TrimSpace(cfg.AutoDiscovery.ASXS.Group) != "" {
		group = strings.TrimSpace(cfg.AutoDiscovery.ASXS.Group)
	}
	summary := ChannelBudgetPoolSummary{
		Source:             "asxs_usage",
		Group:              group,
		FailedChannelCount: failed,
		QuotaPerUSD:        quotaPerUSD,
		Refreshed:          refreshed,
	}

	for _, item := range managed {
		addChannelBudgetPoolChannel(&summary, quotaPerUSD, item.channel)
	}
	for _, channel := range balanceFallbacks {
		before := summary.ChannelCount
		addChannelBudgetPoolChannel(&summary, quotaPerUSD, channel)
		if summary.ChannelCount > before {
			summary.BalanceFallbackCount++
		}
	}

	summary.TotalUSD = roundFloat(summary.TotalUSD, 6)
	summary.UsedUSD = roundFloat(summary.UsedUSD, 6)
	summary.RemainingUSD = roundFloat(summary.RemainingUSD, 6)
	summary.RemainingQuota = int64(math.Round(summary.RemainingUSD * quotaPerUSD))
	return summary
}

func addChannelBudgetPoolChannel(summary *ChannelBudgetPoolSummary, quotaPerUSD float64, channel *model.Channel) {
	if summary == nil || channel == nil {
		return
	}
	summary.ChannelCount++
	if channel.Status == common.ChannelStatusManuallyDisabled {
		return
	}

	remainingUSD := math.Max(channel.Balance, 0)
	usedUSD := math.Max(float64(channel.UsedQuota)/quotaPerUSD, 0)
	totalUSD := usedUSD + remainingUSD
	updatedAt := channel.BalanceUpdatedTime

	otherInfo := parseGuardObject(channel.OtherInfo)
	if budgetInfo, ok := otherInfo["budget_guard"].(map[string]interface{}); ok {
		if value, ok := guardObjectFloat(budgetInfo, "upstream_remaining_usd"); ok {
			remainingUSD = math.Max(value, 0)
		} else if value, ok := guardObjectFloat(budgetInfo, "remaining_usd"); ok {
			remainingUSD = math.Max(value, 0)
		}
		if value, ok := guardObjectFloat(budgetInfo, "upstream_used_usd"); ok {
			usedUSD = math.Max(value, 0)
		} else if value, ok := guardObjectFloat(budgetInfo, "used_usd"); ok {
			usedUSD = math.Max(value, 0)
		}
		if value, ok := guardObjectFloat(budgetInfo, "upstream_total_usd"); ok {
			totalUSD = math.Max(value, usedUSD+remainingUSD)
		} else if value, ok := guardObjectFloat(budgetInfo, "limit_usd"); ok {
			totalUSD = math.Max(value, usedUSD+remainingUSD)
		}
		if value, ok := guardObjectInt64(budgetInfo, "updated_at"); ok && value > updatedAt {
			updatedAt = value
		}
	}
	if quotaSource, ok := otherInfo[channelQuotaSourceInfoKey].(map[string]interface{}); ok {
		if value, ok := guardObjectFloat(quotaSource, "balance"); ok {
			remainingUSD = math.Max(value, 0)
		}
		if value, ok := guardObjectInt64(quotaSource, "updated_at"); ok && value > updatedAt {
			updatedAt = value
		}
	}

	if channel.Status == common.ChannelStatusEnabled && remainingUSD > 0 {
		summary.AvailableChannelCount++
	}
	if updatedAt > summary.UpdatedAt {
		summary.UpdatedAt = updatedAt
	}
	summary.TotalUSD += totalUSD
	summary.UsedUSD += usedUSD
	summary.RemainingUSD += remainingUSD
}

func discoverASXSBalanceFallbackChannels(cfg *operation_setting.ChannelBudgetGuardSetting, channels []*model.Channel, managed []channelBudgetManagedChannel) []*model.Channel {
	if cfg == nil || !cfg.AutoDiscovery.Enabled || !cfg.AutoDiscovery.ASXS.Enabled {
		return nil
	}
	asxs := cfg.AutoDiscovery.ASXS
	group := defaultString(asxs.Group, "asxs")
	configuredFallbackIDs := channelBudgetGuardFallbackIDSet(asxs.BalanceFallbackChannelIDs)
	managedIDs := make(map[int]struct{}, len(managed))
	for _, item := range managed {
		if item.channel != nil {
			managedIDs[item.channel.Id] = struct{}{}
		}
	}

	result := make([]*model.Channel, 0)
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if _, exists := managedIDs[channel.Id]; exists {
			continue
		}
		if asxs.ChannelType > 0 && channel.Type != asxs.ChannelType {
			continue
		}
		if !guardGroupContains(channel.Group, group) {
			continue
		}
		_, configuredFallback := configuredFallbackIDs[channel.Id]
		if !configuredFallback && !isUsageBalanceFallbackBaseURL(channel.GetBaseURL()) && !hasNewAPIBalanceFallbackSetting(channel) {
			continue
		}
		result = append(result, channel)
	}
	return result
}

type channelBudgetManagedChannel struct {
	channel *model.Channel
	config  operation_setting.ChannelBudgetGuardChannelSetting
}

func fetchChannelBudgetGuardChannels() ([]*model.Channel, error) {
	var channels []*model.Channel
	err := model.DB.
		Select("id", "name", "key", "status", "used_quota", "balance", "balance_updated_time", "other_info", "type", "base_url", "group", "setting", "remark").
		Order("id asc").
		Find(&channels).Error
	return channels, err
}

func resolveChannelBudgetGuardChannels(cfg *operation_setting.ChannelBudgetGuardSetting, channels []*model.Channel) []channelBudgetManagedChannel {
	if cfg == nil {
		return nil
	}
	byID := make(map[int]channelBudgetManagedChannel)
	suppressed := make(map[int]struct{})
	channelByID := make(map[int]*model.Channel, len(channels))
	for _, channel := range channels {
		if channel != nil {
			channelByID[channel.Id] = channel
		}
	}

	for _, channelCfg := range cfg.Channels {
		if channelCfg.ID <= 0 {
			continue
		}
		if !channelCfg.Enabled {
			suppressed[channelCfg.ID] = struct{}{}
			continue
		}
		channel := channelByID[channelCfg.ID]
		if channel == nil {
			continue
		}
		if channelCfg.Mode == "" {
			channelCfg.Mode = "daily"
		}
		if channelCfg.Source == "" {
			channelCfg.Source = "local"
		}
		byID[channel.Id] = channelBudgetManagedChannel{channel: channel, config: channelCfg}
	}

	if cfg.AutoDiscovery.Enabled && cfg.AutoDiscovery.ASXS.Enabled {
		asxs := cfg.AutoDiscovery.ASXS
		for _, channel := range channels {
			if channel == nil {
				continue
			}
			if _, blocked := suppressed[channel.Id]; blocked {
				continue
			}
			if _, exists := byID[channel.Id]; exists {
				continue
			}
			if channel.Type != asxs.ChannelType {
				continue
			}
			if normalizeGuardURL(channel.GetBaseURL()) != normalizeGuardURL(asxs.BaseURL) {
				continue
			}
			if !guardGroupContains(channel.Group, asxs.Group) {
				continue
			}
			byID[channel.Id] = channelBudgetManagedChannel{
				channel: channel,
				config: operation_setting.ChannelBudgetGuardChannelSetting{
					ID:       channel.Id,
					Name:     channel.Name,
					Mode:     defaultString(asxs.Mode, "daily"),
					Source:   defaultString(asxs.Source, "asxs_usage"),
					UsageURL: defaultString(asxs.UsageURL, "https://api.asxs.top/api/usage"),
					LimitUSD: asxs.DefaultLimitUSD,
					Enabled:  true,
				},
			}
		}
	}

	result := make([]channelBudgetManagedChannel, 0, len(byID))
	for _, channel := range channels {
		if item, ok := byID[channel.Id]; ok {
			result = append(result, item)
		}
	}
	return result
}

func applyChannelBudgetGuard(ctx context.Context, cfg *operation_setting.ChannelBudgetGuardSetting, channel *model.Channel, channelCfg operation_setting.ChannelBudgetGuardChannelSetting, state channelBudgetGuardChannelState, today string, nowTs int64, quotaPerUSD float64) (channelBudgetGuardChannelState, bool, bool, error) {
	if channel == nil {
		return state, false, false, fmt.Errorf("channel is nil")
	}
	mode := strings.ToLower(strings.TrimSpace(defaultString(channelCfg.Mode, "daily")))
	source := strings.ToLower(strings.TrimSpace(defaultString(channelCfg.Source, "local")))
	if mode != "daily" && mode != "fixed" {
		return state, false, false, fmt.Errorf("unsupported mode %q", mode)
	}

	if source == "asxs_usage" {
		return applyASXSChannelBudgetGuard(ctx, cfg, channel, channelCfg, state, nowTs, quotaPerUSD)
	}

	limitUSD := channelCfg.LimitUSD
	if limitUSD <= 0 {
		return state, false, false, fmt.Errorf("non-positive limit")
	}
	limitQuota := int64(math.Round(limitUSD * quotaPerUSD))
	if mode == "daily" && state.LastResetDate == "" {
		state.LastResetDate = today
		return state, false, false, nil
	}
	statusChanged := false
	if mode == "daily" && state.LastResetDate != today {
		otherInfo := parseGuardObject(channel.OtherInfo)
		otherInfo["budget_guard"] = buildChannelBudgetInfo(channelCfg, mode, limitUSD, 0, quotaPerUSD, limitUSD, nowTs, "daily_reset", map[string]interface{}{
			"last_reset_date": today,
		})
		otherInfo[channelQuotaSourceInfoKey] = buildInternalQuotaLedgerSource(mode, limitUSD, 0, quotaPerUSD, limitUSD, nowTs, "daily_reset")
		updates := channelBudgetChannelUpdate{UsedQuota: int64Ptr(0), Balance: float64Ptr(limitUSD), OtherInfo: otherInfo}
		if state.DisabledByGuard && channel.Status != common.ChannelStatusManuallyDisabled {
			updates.Status = intPtr(common.ChannelStatusEnabled)
			updates.AbilitiesEnabled = boolPtr(true)
			statusChanged = true
		}
		if err := updateChannelBudgetGuardChannel(channel, updates, nowTs); err != nil {
			return state, false, false, err
		}
		state.LastResetDate = today
		state.DisabledByGuard = false
		channel.UsedQuota = 0
		channel.Status = common.ChannelStatusEnabled
	}

	remainingQuota := limitQuota - channel.UsedQuota
	remainingUSD := math.Max(float64(remainingQuota)/quotaPerUSD, 0)
	otherInfo := parseGuardObject(channel.OtherInfo)
	otherInfo["budget_guard"] = buildChannelBudgetInfo(channelCfg, mode, limitUSD, channel.UsedQuota, quotaPerUSD, remainingUSD, nowTs, budgetReason(remainingQuota > 0), map[string]interface{}{
		"last_reset_date":   state.LastResetDate,
		"disabled_by_guard": state.DisabledByGuard,
	})
	otherInfo[channelQuotaSourceInfoKey] = buildInternalQuotaLedgerSource(mode, limitUSD, channel.UsedQuota, quotaPerUSD, remainingUSD, nowTs, budgetReason(remainingQuota > 0))

	updates := channelBudgetChannelUpdate{Balance: float64Ptr(remainingUSD), OtherInfo: otherInfo}
	if remainingQuota <= 0 {
		updates.Balance = float64Ptr(0)
		if channel.Status == common.ChannelStatusEnabled {
			updates.Status = intPtr(common.ChannelStatusAutoDisabled)
			updates.AbilitiesEnabled = boolPtr(false)
			otherInfo["status_reason"] = fmt.Sprintf("channel_budget_exhausted: %s limit $%g", mode, limitUSD)
			otherInfo["status_time"] = nowTs
			state.DisabledByGuard = true
			statusChanged = true
		}
	} else if wasDisabledByChannelBudgetGuard(state, channel.OtherInfo) && channel.Status != common.ChannelStatusManuallyDisabled {
		updates.Status = intPtr(common.ChannelStatusEnabled)
		updates.AbilitiesEnabled = boolPtr(true)
		state.DisabledByGuard = false
		statusChanged = true
	}

	if err := updateChannelBudgetGuardChannel(channel, updates, nowTs); err != nil {
		return state, false, false, err
	}
	return state, true, statusChanged, nil
}

func applyASXSChannelBudgetGuard(ctx context.Context, cfg *operation_setting.ChannelBudgetGuardSetting, channel *model.Channel, channelCfg operation_setting.ChannelBudgetGuardChannelSetting, state channelBudgetGuardChannelState, nowTs int64, quotaPerUSD float64) (channelBudgetGuardChannelState, bool, bool, error) {
	usageURL := defaultString(channelCfg.UsageURL, "https://api.asxs.top/api/usage")
	usage, err := fetchASXSUsage(ctx, channel, usageURL, channelBudgetGuardTimeout(cfg, channelCfg))
	if err != nil {
		return state, false, false, err
	}
	limitUSD := channelCfg.LimitUSD
	if usage.TotalUSD > 0 {
		limitUSD = usage.TotalUSD
	}
	usedQuota := int64(math.Round(math.Max(usage.UsedUSD, 0) * quotaPerUSD))
	remainingUSD := math.Max(usage.RemainingUSD, 0)
	otherInfo := parseGuardObject(channel.OtherInfo)
	budgetExtra := map[string]interface{}{
		"source":                 "asxs_usage",
		"upstream_plan_name":     usage.PlanName,
		"upstream_reset_info":    usage.ResetInfo,
		"upstream_total_usd":     roundFloat(usage.TotalUSD, 6),
		"upstream_used_usd":      roundFloat(usage.UsedUSD, 6),
		"upstream_remaining_usd": roundFloat(remainingUSD, 6),
		"disabled_by_guard":      state.DisabledByGuard,
	}
	otherInfo["budget_guard"] = buildChannelBudgetInfo(channelCfg, "upstream_daily", limitUSD, usedQuota, quotaPerUSD, remainingUSD, nowTs, budgetReason(remainingUSD > 0), budgetExtra)
	otherInfo[channelQuotaSourceInfoKey] = buildASXSQuotaSource(usage, remainingUSD, nowTs)

	updates := channelBudgetChannelUpdate{UsedQuota: &usedQuota, Balance: float64Ptr(remainingUSD), OtherInfo: otherInfo}
	statusChanged := false
	if channel.Remark == nil || strings.TrimSpace(*channel.Remark) == "" {
		updates.Remark = stringPtr(channelBudgetGuardDefaultRemark)
	}

	if remainingUSD <= 0 {
		updates.Balance = float64Ptr(0)
		if channel.Status == common.ChannelStatusEnabled {
			updates.Status = intPtr(common.ChannelStatusAutoDisabled)
			updates.AbilitiesEnabled = boolPtr(false)
			otherInfo["status_reason"] = fmt.Sprintf("channel_budget_exhausted: upstream daily limit $%g", limitUSD)
			otherInfo["status_time"] = nowTs
			state.DisabledByGuard = true
			statusChanged = true
		}
	} else if wasDisabledByChannelBudgetGuard(state, channel.OtherInfo) && channel.Status != common.ChannelStatusManuallyDisabled {
		updates.Status = intPtr(common.ChannelStatusEnabled)
		updates.AbilitiesEnabled = boolPtr(true)
		state.DisabledByGuard = false
		statusChanged = true
	}

	if err := updateChannelBudgetGuardChannel(channel, updates, nowTs); err != nil {
		return state, false, false, err
	}
	if common.DebugEnabled {
		logger.LogDebug(ctx, "channel budget guard: channel_id=%d name=%s upstream_total=$%.4f used=$%.4f remaining=$%.4f status=%d", channel.Id, channel.Name, limitUSD, usage.UsedUSD, remainingUSD, channel.Status)
	}
	return state, true, statusChanged, nil
}

type channelBudgetChannelUpdate struct {
	UsedQuota        *int64
	Balance          *float64
	Status           *int
	AbilitiesEnabled *bool
	OtherInfo        map[string]interface{}
	Remark           *string
}

func updateChannelBudgetGuardChannel(channel *model.Channel, update channelBudgetChannelUpdate, nowTs int64) error {
	updates := make(map[string]interface{})
	if update.UsedQuota != nil {
		updates["used_quota"] = *update.UsedQuota
		channel.UsedQuota = *update.UsedQuota
	}
	if update.Balance != nil {
		updates["balance"] = *update.Balance
		updates["balance_updated_time"] = nowTs
		channel.Balance = *update.Balance
		channel.BalanceUpdatedTime = nowTs
	}
	if update.Status != nil {
		updates["status"] = *update.Status
		channel.Status = *update.Status
	}
	if update.OtherInfo != nil {
		raw, err := common.Marshal(update.OtherInfo)
		if err != nil {
			return err
		}
		updates["other_info"] = string(raw)
		channel.OtherInfo = string(raw)
	}
	if update.Remark != nil {
		updates["remark"] = update.Remark
		channel.Remark = update.Remark
	}
	if len(updates) > 0 {
		if err := model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
			return err
		}
	}
	if update.AbilitiesEnabled != nil {
		if err := model.UpdateAbilityStatus(channel.Id, *update.AbilitiesEnabled); err != nil {
			return err
		}
	}
	if update.Status != nil {
		model.CacheUpdateChannelStatus(channel.Id, *update.Status)
	}
	return nil
}

func fetchASXSUsage(ctx context.Context, channel *model.Channel, usageURL string, timeout time.Duration) (asxsUsageResult, error) {
	if strings.TrimSpace(channel.Key) == "" {
		return asxsUsageResult{}, fmt.Errorf("channel key is empty")
	}
	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return asxsUsageResult{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, usageURL, nil)
	if err != nil {
		return asxsUsageResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(channel.Key))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "new-api-channel-budget-guard/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return asxsUsageResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return asxsUsageResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		body := common.MaskSensitiveInfo(strings.TrimSpace(string(raw)))
		if body != "" {
			return asxsUsageResult{}, fmt.Errorf("asxs usage http %d: %s", resp.StatusCode, body)
		}
		return asxsUsageResult{}, fmt.Errorf("asxs usage http %d", resp.StatusCode)
	}
	return parseASXSUsage(raw)
}

func refreshUsageBalanceFallbackChannels(ctx context.Context, channels []*model.Channel, timeout time.Duration) int {
	failed := 0
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		var err error
		switch {
		case hasNewAPIBalanceFallbackSetting(channel):
			_, err = refreshNewAPIBalanceFallbackChannel(ctx, channel, timeout)
		case isUsageBalanceFallbackBaseURL(channel.GetBaseURL()):
			_, err = refreshUsageBalanceFallbackChannel(ctx, channel, timeout)
		default:
			failed++
			logger.LogWarn(ctx, fmt.Sprintf("asxs pool fallback refresh: channel_id=%d name=%s failed: unsupported balance source", channel.Id, channel.Name))
			continue
		}
		if err != nil {
			failed++
			logger.LogWarn(ctx, fmt.Sprintf("asxs pool fallback refresh: channel_id=%d name=%s failed: %v", channel.Id, channel.Name, err))
		}
	}
	return failed
}

func refreshUsageBalanceFallbackChannel(ctx context.Context, channel *model.Channel, timeout time.Duration) (float64, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	if baseURL == "" {
		return 0, fmt.Errorf("usage fallback base_url is empty")
	}
	result, err := fetchUsageBalance(ctx, channel, fmt.Sprintf("%s/v1/usage", baseURL), timeout)
	if err != nil {
		return 0, err
	}
	nowTs := common.GetTimestamp()
	quotaSource := buildUsageBalanceQuotaSource(result.Response, result.Balance, nowTs)
	if err := UpdateChannelBalanceWithQuotaSource(channel, result.Balance, quotaSource, nowTs); err != nil {
		return 0, err
	}
	return result.Balance, nil
}

func refreshNewAPIBalanceFallbackChannel(ctx context.Context, channel *model.Channel, timeout time.Duration) (float64, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	if baseURL == "" {
		return 0, fmt.Errorf("new-api fallback base_url is empty")
	}
	result, err := fetchNewAPIBalance(ctx, channel, baseURL, timeout)
	if err != nil {
		return 0, err
	}
	nowTs := common.GetTimestamp()
	quotaSource := buildNewAPIBalanceQuotaSource(result, nowTs)
	if err := UpdateChannelBalanceWithQuotaSource(channel, result.Balance, quotaSource, nowTs); err != nil {
		return 0, err
	}
	return result.Balance, nil
}

func fetchUsageBalance(ctx context.Context, channel *model.Channel, usageURL string, timeout time.Duration) (usageBalanceResult, error) {
	if strings.TrimSpace(channel.Key) == "" {
		return usageBalanceResult{}, fmt.Errorf("channel key is empty")
	}
	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return usageBalanceResult{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, usageURL, nil)
	if err != nil {
		return usageBalanceResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(channel.Key))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "new-api-channel-budget-guard/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return usageBalanceResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return usageBalanceResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		body := common.MaskSensitiveInfo(strings.TrimSpace(string(raw)))
		if body != "" {
			return usageBalanceResult{}, fmt.Errorf("usage balance http %d: %s", resp.StatusCode, body)
		}
		return usageBalanceResult{}, fmt.Errorf("usage balance http %d", resp.StatusCode)
	}
	return parseUsageBalanceResult(raw)
}

func fetchNewAPIBalance(ctx context.Context, channel *model.Channel, baseURL string, timeout time.Duration) (newAPIBalanceResult, error) {
	accessToken, userID, err := newAPIBalanceFallbackCredentials(channel)
	if err != nil {
		return newAPIBalanceResult{}, err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+accessToken)
	headers.Set("New-Api-User", userID)
	headers.Set("Accept", "application/json")
	headers.Set("User-Agent", "new-api-channel-budget-guard/1.0")

	body, err := fetchJSONWithChannelProxy(ctx, channel, http.MethodGet, baseURL+"/api/user/self", headers, timeout)
	if err != nil {
		return newAPIBalanceResult{}, err
	}
	selfResp := newAPIUserSelfBalanceResponse{}
	if err := common.Unmarshal(body, &selfResp); err != nil {
		return newAPIBalanceResult{}, err
	}
	if !selfResp.Success {
		if strings.TrimSpace(selfResp.Message) != "" {
			return newAPIBalanceResult{}, fmt.Errorf("new-api user self failed: %s", selfResp.Message)
		}
		return newAPIBalanceResult{}, fmt.Errorf("new-api user self failed")
	}

	statusHeaders := http.Header{}
	statusHeaders.Set("Accept", "application/json")
	statusHeaders.Set("User-Agent", "new-api-channel-budget-guard/1.0")
	body, err = fetchJSONWithChannelProxy(ctx, channel, http.MethodGet, baseURL+"/api/status", statusHeaders, timeout)
	if err != nil {
		return newAPIBalanceResult{}, err
	}
	statusResp := newAPIStatusBalanceResponse{}
	if err := common.Unmarshal(body, &statusResp); err != nil {
		return newAPIBalanceResult{}, err
	}
	if statusResp.Success == false && strings.TrimSpace(statusResp.Message) != "" {
		return newAPIBalanceResult{}, fmt.Errorf("new-api status failed: %s", statusResp.Message)
	}
	balance, err := computeNewAPIBalanceAmount(selfResp.Data.Quota, &statusResp)
	if err != nil {
		return newAPIBalanceResult{}, err
	}
	return newAPIBalanceResult{Balance: balance, Quota: selfResp.Data.Quota, Status: statusResp}, nil
}

func fetchJSONWithChannelProxy(ctx context.Context, channel *model.Channel, method, url string, headers http.Header, timeout time.Duration) ([]byte, error) {
	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, url, nil)
	if err != nil {
		return nil, err
	}
	for key := range headers {
		req.Header.Set(key, headers.Get(key))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body := common.MaskSensitiveInfo(strings.TrimSpace(string(raw)))
		if body != "" {
			return nil, fmt.Errorf("http %d: %s", resp.StatusCode, body)
		}
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return raw, nil
}

func parseUsageBalance(raw []byte) (float64, error) {
	result, err := parseUsageBalanceResult(raw)
	if err != nil {
		return 0, err
	}
	return result.Balance, nil
}

func parseUsageBalanceResult(raw []byte) (usageBalanceResult, error) {
	response := usageBalanceResponse{}
	if err := common.Unmarshal(raw, &response); err != nil {
		return usageBalanceResult{}, err
	}
	if response.IsValid != nil && !*response.IsValid {
		return usageBalanceResult{}, fmt.Errorf("usage balance account is not valid, plan: %s, mode: %s", response.PlanName, response.Mode)
	}
	for _, rateLimit := range response.RateLimits {
		if strings.TrimSpace(rateLimit.Window) != "1d" {
			continue
		}
		if balance, ok := interfaceToFloat64(rateLimit.Remaining); ok {
			return usageBalanceResult{Balance: balance, Response: response}, nil
		}
	}
	if len(response.RateLimits) > 0 {
		if balance, ok := interfaceToFloat64(response.RateLimits[0].Remaining); ok {
			return usageBalanceResult{Balance: balance, Response: response}, nil
		}
	}
	for _, value := range []interface{}{response.Remaining, response.Balance, response.TotalAvailable} {
		if balance, ok := interfaceToFloat64(value); ok {
			return usageBalanceResult{Balance: balance, Response: response}, nil
		}
	}
	return usageBalanceResult{}, fmt.Errorf("usage balance response missing remaining balance")
}

func computeNewAPIBalanceAmount(quota int64, status *newAPIStatusBalanceResponse) (float64, error) {
	if status == nil {
		return 0, fmt.Errorf("new-api status response is nil")
	}
	quotaPerUnit := status.Data.QuotaPerUnit
	if quotaPerUnit <= 0 {
		return 0, fmt.Errorf("new-api quota_per_unit must be greater than 0")
	}
	amount := float64(quota)
	switch strings.ToUpper(strings.TrimSpace(status.Data.QuotaDisplayType)) {
	case operation_setting.QuotaDisplayTypeCNY:
		amount = amount / quotaPerUnit * status.Data.USDExchangeRate
	case operation_setting.QuotaDisplayTypeTokens:
	case operation_setting.QuotaDisplayTypeCustom:
		amount = amount / quotaPerUnit * status.Data.CustomCurrencyExchangeRate
	default:
		amount = amount / quotaPerUnit
	}
	return amount, nil
}

func parseASXSUsage(raw []byte) (asxsUsageResult, error) {
	var items []asxsUsageItem
	if err := common.Unmarshal(raw, &items); err != nil {
		return asxsUsageResult{}, err
	}
	if len(items) == 0 {
		return asxsUsageResult{}, fmt.Errorf("asxs usage payload is empty")
	}
	candidates := make([]asxsUsageItem, 0, len(items))
	for _, item := range items {
		if item.Unit != "USD" {
			continue
		}
		if item.IsValid != nil && !*item.IsValid {
			continue
		}
		if item.Total == nil || item.Remaining == nil {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		return asxsUsageResult{}, fmt.Errorf("asxs usage payload has no daily USD quota item")
	}
	selected := candidates[0]
	for _, candidate := range candidates {
		name := strings.ToLower(candidate.PlanName)
		if strings.Contains(candidate.PlanName, "日") || strings.Contains(name, "daily") {
			selected = candidate
			break
		}
	}
	total, _ := interfaceToFloat64(selected.Total)
	remaining, _ := interfaceToFloat64(selected.Remaining)
	used, ok := interfaceToFloat64(selected.Used)
	if !ok {
		used = math.Max(total-remaining, 0)
	}
	return asxsUsageResult{
		PlanName:     selected.PlanName,
		TotalUSD:     total,
		UsedUSD:      used,
		RemainingUSD: remaining,
		Unit:         selected.Unit,
		ResetInfo:    selected.Extra,
		RawItems:     len(items),
	}, nil
}

func buildChannelBudgetInfo(channelCfg operation_setting.ChannelBudgetGuardChannelSetting, mode string, limitUSD float64, usedQuota int64, quotaPerUSD float64, remainingUSD float64, nowTs int64, reason string, extra map[string]interface{}) map[string]interface{} {
	info := map[string]interface{}{
		"managed":         true,
		"mode":            mode,
		"configured_name": channelCfg.Name,
		"limit_usd":       limitUSD,
		"used_usd":        roundFloat(float64(usedQuota)/quotaPerUSD, 6),
		"remaining_usd":   roundFloat(remainingUSD, 6),
		"updated_at":      nowTs,
		"reason":          reason,
	}
	for k, v := range extra {
		info[k] = v
	}
	return info
}

func loadChannelBudgetGuardState() channelBudgetGuardState {
	state := channelBudgetGuardState{Version: 1, Channels: map[string]channelBudgetGuardChannelState{}}
	if loadJSONOption(channelBudgetGuardStateOptionKey, &state) && state.Channels != nil {
		return state
	}
	state.Version = 1
	state.Channels = map[string]channelBudgetGuardChannelState{}
	return state
}

func wasDisabledByChannelBudgetGuard(state channelBudgetGuardChannelState, otherInfoRaw string) bool {
	if state.DisabledByGuard {
		return true
	}
	otherInfo := parseGuardObject(otherInfoRaw)
	budgetInfo, ok := otherInfo["budget_guard"].(map[string]interface{})
	if ok {
		if disabled, ok := budgetInfo["disabled_by_guard"].(bool); ok && disabled {
			return true
		}
		if reason, ok := budgetInfo["reason"].(string); ok && reason == "budget_exhausted" {
			return true
		}
	}
	if reason, ok := otherInfo["status_reason"].(string); ok && strings.Contains(reason, "channel_budget_exhausted") {
		return true
	}
	return false
}

func parseGuardObject(raw string) map[string]interface{} {
	result := map[string]interface{}{}
	if strings.TrimSpace(raw) == "" {
		return result
	}
	if err := common.UnmarshalJsonStr(raw, &result); err != nil {
		return map[string]interface{}{}
	}
	return result
}

func guardObjectFloat(values map[string]interface{}, key string) (float64, bool) {
	if values == nil {
		return 0, false
	}
	return interfaceToFloat64(values[key])
}

func guardObjectBool(values map[string]interface{}, key string) (bool, bool) {
	if values == nil {
		return false, false
	}
	switch value := values[key].(type) {
	case bool:
		return value, true
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(value))
		if trimmed == "" {
			return false, false
		}
		switch trimmed {
		case "1", "true", "yes", "y", "on":
			return true, true
		case "0", "false", "no", "n", "off":
			return false, true
		}
	case float64:
		return value != 0, true
	case float32:
		return value != 0, true
	case int:
		return value != 0, true
	case int64:
		return value != 0, true
	case int32:
		return value != 0, true
	}
	return false, false
}

func guardObjectInt64(values map[string]interface{}, key string) (int64, bool) {
	value, ok := guardObjectFloat(values, key)
	if !ok {
		return 0, false
	}
	return int64(value), true
}

func budgetReason(hasRemaining bool) string {
	if hasRemaining {
		return "within_budget"
	}
	return "budget_exhausted"
}

func interfaceToFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed, err == nil
	default:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(v)), 64)
		return parsed, err == nil
	}
}

func guardDateString(timezone string) string {
	return guardNow(timezone).Format("2006-01-02")
}

func guardNow(timezone string) time.Time {
	if strings.TrimSpace(timezone) == "" {
		return time.Now()
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Now()
	}
	return time.Now().In(loc)
}

func normalizeGuardURL(value string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
}

func guardGroupContains(groupValue string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return false
	}
	for _, group := range strings.Split(groupValue, ",") {
		if strings.ToLower(strings.TrimSpace(group)) == target {
			return true
		}
	}
	return false
}

func channelBudgetGuardFallbackIDSet(ids []int) map[int]struct{} {
	result := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			result[id] = struct{}{}
		}
	}
	return result
}

func isUsageBalanceFallbackBaseURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if (err != nil || parsed.Hostname() == "") && !strings.Contains(rawURL, "://") {
		parsed, err = url.Parse("https://" + rawURL)
	}
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "api.awnjkankwik.asia" ||
		host == "api.onetokenpass.xyz" ||
		host == "www.qflowapi.com" ||
		host == "qflowapi.com" ||
		host == "xmapi.cc" ||
		strings.HasSuffix(host, ".xmapi.cc") ||
		strings.HasSuffix(host, ".qflowapi.com")
}

func hasNewAPIBalanceFallbackSetting(channel *model.Channel) bool {
	if channel == nil {
		return false
	}
	setting := channel.GetSetting()
	return strings.TrimSpace(setting.NewAPIBalanceAccessToken) != "" ||
		strings.TrimSpace(setting.NewAPIBalanceAccessTokenEnv) != "" ||
		strings.TrimSpace(setting.NewAPIBalanceUserID) != "" ||
		strings.TrimSpace(setting.NewAPIBalanceUserIDEnv) != ""
}

func newAPIBalanceFallbackCredentials(channel *model.Channel) (string, string, error) {
	if channel == nil {
		return "", "", fmt.Errorf("channel is nil")
	}
	setting := channel.GetSetting()
	accessToken := resolveSettingSecret(setting.NewAPIBalanceAccessToken, setting.NewAPIBalanceAccessTokenEnv)
	userID := resolveSettingSecret(setting.NewAPIBalanceUserID, setting.NewAPIBalanceUserIDEnv)
	if accessToken == "" || userID == "" {
		return "", "", fmt.Errorf("new-api balance sync requires both access token and user id")
	}
	if _, err := strconv.Atoi(userID); err != nil {
		return "", "", fmt.Errorf("new-api balance sync user id must be numeric: %w", err)
	}
	return accessToken, userID, nil
}

func resolveSettingSecret(rawValue string, envName string) string {
	envName = strings.TrimSpace(envName)
	if envName != "" {
		return strings.TrimSpace(common.GetEnvOrDefaultString(envName, ""))
	}
	return strings.TrimSpace(rawValue)
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func roundFloat(value float64, places int) float64 {
	if places <= 0 {
		return math.Round(value)
	}
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func intPtr(value int) *int             { return &value }
func int64Ptr(value int64) *int64       { return &value }
func float64Ptr(value float64) *float64 { return &value }
func boolPtr(value bool) *bool          { return &value }
func stringPtr(value string) *string    { return &value }
