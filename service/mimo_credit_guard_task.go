package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

const mimoCreditGuardStateOptionKey = "mimo_credit_guard_state"

type mimoCreditGuardState struct {
	Version               int                      `json:"version"`
	UpdatedAt             int64                    `json:"updated_at"`
	ChannelID             int                      `json:"channel_id"`
	ChannelIDs            []int                    `json:"channel_ids,omitempty"`
	BaselineLogID         int                      `json:"baseline_log_id"`
	InitialUsedCredits    int64                    `json:"initial_used_credits"`
	IncrementalCredits    int64                    `json:"incremental_credits"`
	UsedCredits           int64                    `json:"used_credits"`
	RemainingCredits      int64                    `json:"remaining_credits"`
	PlanTotalCredits      int64                    `json:"plan_total_credits"`
	ExpiresAt             string                   `json:"expires_at,omitempty"`
	LogCountAfterBaseline int                      `json:"log_count_after_baseline"`
	LastLogID             int                      `json:"last_log_id"`
	LastItems             []mimoCreditEstimateItem `json:"last_items,omitempty"`
}

type mimoCreditEstimateItem struct {
	ID        int     `json:"id"`
	CreatedAt int64   `json:"created_at"`
	Model     string  `json:"model"`
	Tokens    int64   `json:"tokens"`
	ModelRate float64 `json:"model_rate"`
	TimeRate  float64 `json:"time_rate"`
	Credits   int64   `json:"credits"`
}

var (
	mimoCreditGuardOnce    sync.Once
	mimoCreditGuardRunning atomic.Bool
)

func StartMimoCreditGuardTask() {
	mimoCreditGuardOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			cfg := operation_setting.GetMimoCreditSetting()
			logger.LogInfo(context.Background(), fmt.Sprintf("mimo credit guard task started: enabled=%t tick=%s channel_id=%d baseline_log_id=%d", cfg.Enabled, mimoCreditGuardInterval(cfg), cfg.ChannelID, cfg.BaselineLogID))
			runMimoCreditGuardOnce()
			for {
				time.Sleep(mimoCreditGuardInterval(operation_setting.GetMimoCreditSetting()))
				runMimoCreditGuardOnce()
			}
		})
	})
}

func mimoCreditGuardInterval(cfg *operation_setting.MimoCreditSetting) time.Duration {
	if cfg == nil || cfg.TickIntervalMinutes < 1 {
		return 5 * time.Minute
	}
	return time.Duration(cfg.TickIntervalMinutes) * time.Minute
}

func runMimoCreditGuardOnce() {
	if !mimoCreditGuardRunning.CompareAndSwap(false, true) {
		return
	}
	defer mimoCreditGuardRunning.Store(false)

	ctx := context.Background()
	cfg := operation_setting.GetMimoCreditSetting()
	if cfg == nil || !cfg.Enabled {
		return
	}
	if cfg.ChannelID <= 0 {
		logger.LogWarn(ctx, "mimo credit guard: channel_id is not configured")
		return
	}
	if cfg.PlanTotalCredits <= 0 {
		logger.LogWarn(ctx, "mimo credit guard: plan_total_credits must be positive")
		return
	}

	channelIDs := mimoCreditPlanChannelIDs(cfg, nil)
	rows, err := fetchMimoCreditLogs(channelIDs, cfg.BaselineLogID)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("mimo credit guard: query logs failed: %v", err))
		return
	}
	report, err := buildMimoCreditReportForChannels(cfg, channelIDs, rows, common.GetTimestamp())
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("mimo credit guard: build report failed: %v", err))
		return
	}

	if cfg.Display.UpdateChannel {
		if err := updateMimoCreditChannels(cfg, report, channelIDs); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("mimo credit guard: update channel_id=%d failed: %v", cfg.ChannelID, err))
			return
		}
	}
	if err := saveJSONOption(mimoCreditGuardStateOptionKey, report); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("mimo credit guard: save state failed: %v", err))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("mimo credit guard: channel_id=%d remaining=%d used=%d incremental=%d logs=%d", report.ChannelID, report.RemainingCredits, report.UsedCredits, report.IncrementalCredits, report.LogCountAfterBaseline))
}

func UpdateMimoCreditGuardBalance(ctx context.Context, channel *model.Channel) (float64, bool, error) {
	if channel == nil || !isMimoCreditManagedChannel(channel) {
		return 0, false, nil
	}

	cfg := operation_setting.GetMimoCreditSetting()
	if cfg == nil || !cfg.Enabled {
		return 0, true, fmt.Errorf("mimo credit guard is disabled")
	}
	if cfg.ChannelID <= 0 {
		return 0, true, fmt.Errorf("mimo credit guard channel_id is not configured")
	}
	if cfg.PlanTotalCredits <= 0 {
		return 0, true, fmt.Errorf("mimo credit guard plan_total_credits must be positive")
	}

	channelIDs := mimoCreditPlanChannelIDs(cfg, channel)
	rows, err := fetchMimoCreditLogs(channelIDs, cfg.BaselineLogID)
	if err != nil {
		return 0, true, err
	}
	report, err := buildMimoCreditReportForChannels(cfg, channelIDs, rows, common.GetTimestamp())
	if err != nil {
		return 0, true, err
	}
	if err := updateMimoCreditChannels(cfg, report, channelIDs); err != nil {
		return 0, true, err
	}
	if err := saveJSONOption(mimoCreditGuardStateOptionKey, report); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("mimo credit guard: save state failed after manual balance refresh: %v", err))
	}
	balance := mimoCreditReportBalance(cfg, report)
	logger.LogInfo(ctx, fmt.Sprintf("mimo credit manual refresh: channel_id=%d channels=%v remaining=%d used=%d logs=%d", channel.Id, channelIDs, report.RemainingCredits, report.UsedCredits, report.LogCountAfterBaseline))
	return balance, true, nil
}

func isMimoCreditManagedChannel(channel *model.Channel) bool {
	cfg := operation_setting.GetMimoCreditSetting()
	if cfg != nil && cfg.ChannelID > 0 && channel.Id == cfg.ChannelID {
		return true
	}
	if channel.Type == constant.ChannelTypeMimo {
		return true
	}

	group := strings.ToLower(strings.TrimSpace(channel.Group))
	models := strings.ToLower(channel.Models)
	name := strings.ToLower(channel.Name)
	baseURL := strings.ToLower(channel.GetBaseURL())
	return strings.Contains(group, "mimo") &&
		(strings.Contains(models, "mimo-") ||
			strings.Contains(name, "mimo") ||
			strings.Contains(baseURL, "xiaomimimo"))
}

func mimoCreditPlanChannelIDs(cfg *operation_setting.MimoCreditSetting, target *model.Channel) []int {
	ids := make([]int, 0, 4)
	seen := make(map[int]struct{})
	addID := func(id int) {
		if id <= 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if cfg != nil {
		addID(cfg.ChannelID)
	}
	if target != nil {
		addID(target.Id)
	}

	var channels []model.Channel
	if err := model.DB.
		Select("id, type, name, base_url, models, \"group\"").
		Where("type = ? OR lower(\"group\") LIKE ? OR lower(models) LIKE ? OR lower(base_url) LIKE ? OR lower(name) LIKE ?",
			constant.ChannelTypeMimo,
			"%mimo%",
			"%mimo-%",
			"%xiaomimimo%",
			"%mimo%",
		).
		Find(&channels).Error; err != nil {
		return ids
	}
	for _, candidate := range channels {
		if isMimoCreditManagedChannel(&candidate) {
			addID(candidate.Id)
		}
	}
	return ids
}

func fetchMimoCreditLogs(channelIDs []int, baselineLogID int) ([]model.Log, error) {
	var rows []model.Log
	if len(channelIDs) == 0 {
		return rows, nil
	}
	err := model.LOG_DB.
		Select("id", "created_at", "model_name", "prompt_tokens", "completion_tokens", "other").
		Where("channel_id in ? and type = ? and id > ?", channelIDs, model.LogTypeConsume, baselineLogID).
		Order("id asc").
		Find(&rows).Error
	return rows, err
}

func buildMimoCreditReport(cfg *operation_setting.MimoCreditSetting, rows []model.Log, nowTs int64) (mimoCreditGuardState, error) {
	return buildMimoCreditReportForChannels(cfg, []int{cfg.ChannelID}, rows, nowTs)
}

func buildMimoCreditReportForChannels(cfg *operation_setting.MimoCreditSetting, channelIDs []int, rows []model.Log, nowTs int64) (mimoCreditGuardState, error) {
	items := make([]mimoCreditEstimateItem, 0, len(rows))
	incremental := int64(0)
	lastLogID := cfg.BaselineLogID
	for _, row := range rows {
		item, err := estimateMimoLogCredits(row, cfg)
		if err != nil {
			return mimoCreditGuardState{}, err
		}
		items = append(items, item)
		incremental += item.Credits
		if row.Id > lastLogID {
			lastLogID = row.Id
		}
	}

	used := cfg.InitialUsedCredits + incremental
	remaining := cfg.PlanTotalCredits - used
	if remaining < 0 {
		remaining = 0
	}
	if len(items) > 10 {
		items = items[len(items)-10:]
	}

	return mimoCreditGuardState{
		Version:               1,
		UpdatedAt:             nowTs,
		ChannelID:             cfg.ChannelID,
		ChannelIDs:            channelIDs,
		BaselineLogID:         cfg.BaselineLogID,
		InitialUsedCredits:    cfg.InitialUsedCredits,
		IncrementalCredits:    incremental,
		UsedCredits:           used,
		RemainingCredits:      remaining,
		PlanTotalCredits:      cfg.PlanTotalCredits,
		ExpiresAt:             cfg.ExpiresAt,
		LogCountAfterBaseline: len(rows),
		LastLogID:             lastLogID,
		LastItems:             items,
	}, nil
}

func estimateMimoLogCredits(row model.Log, cfg *operation_setting.MimoCreditSetting) (mimoCreditEstimateItem, error) {
	other := parseGuardObject(row.Other)
	tokens := int64(row.PromptTokens + row.CompletionTokens)
	if cfg.Usage.IncludeCacheReadTokens {
		tokens += int64(math.Round(float64(mimoCreditInt(other["cache_tokens"])) * nonZeroFloat(cfg.Usage.CacheReadMultiplier, 1)))
	}
	if cfg.Usage.IncludeCacheCreationTokens {
		tokens += int64(math.Round(float64(mimoCreditInt(other["cache_creation_tokens"])) * nonZeroFloat(cfg.Usage.CacheCreationMultiplier, 1)))
	}

	modelRate := mimoCreditModelRate(row.ModelName, cfg)
	timeRate, err := mimoCreditTimeRate(row.CreatedAt, cfg)
	if err != nil {
		return mimoCreditEstimateItem{}, err
	}
	credits := int64(math.Round(float64(tokens) * modelRate * timeRate))
	return mimoCreditEstimateItem{
		ID:        row.Id,
		CreatedAt: row.CreatedAt,
		Model:     row.ModelName,
		Tokens:    tokens,
		ModelRate: modelRate,
		TimeRate:  timeRate,
		Credits:   credits,
	}, nil
}

func updateMimoCreditChannel(cfg *operation_setting.MimoCreditSetting, report mimoCreditGuardState) error {
	return updateMimoCreditChannelByID(cfg.ChannelID, cfg, report)
}

func updateMimoCreditChannels(cfg *operation_setting.MimoCreditSetting, report mimoCreditGuardState, channelIDs []int) error {
	if len(channelIDs) == 0 {
		return updateMimoCreditChannel(cfg, report)
	}
	for _, channelID := range channelIDs {
		if err := updateMimoCreditChannelByID(channelID, cfg, report); err != nil {
			return err
		}
	}
	return nil
}

func updateMimoCreditChannelByID(channelID int, cfg *operation_setting.MimoCreditSetting, report mimoCreditGuardState) error {
	var channel model.Channel
	if err := model.DB.
		Select("id", "balance", "balance_updated_time", "other_info", "remark").
		Where("id = ?", channelID).
		First(&channel).Error; err != nil {
		return err
	}

	balance := mimoCreditReportBalance(cfg, report)
	remark := fmt.Sprintf(
		"MiMo Token Plan Credits: 剩余 %s / %s，已用 %s；基线已用 %s；到期 %s；单位是 Credits，不是 USD",
		formatMimoCredits(report.RemainingCredits),
		formatMimoCredits(report.PlanTotalCredits),
		formatMimoCredits(report.UsedCredits),
		formatMimoCredits(report.InitialUsedCredits),
		report.ExpiresAt,
	)
	otherInfo := parseGuardObject(channel.OtherInfo)
	otherInfo["mimo_credit_guard"] = map[string]interface{}{
		"managed":                  true,
		"remaining_credits":        report.RemainingCredits,
		"used_credits":             report.UsedCredits,
		"plan_total_credits":       report.PlanTotalCredits,
		"incremental_credits":      report.IncrementalCredits,
		"baseline_log_id":          report.BaselineLogID,
		"initial_used_credits":     report.InitialUsedCredits,
		"log_count_after_baseline": report.LogCountAfterBaseline,
		"last_log_id":              report.LastLogID,
		"channel_ids":              report.ChannelIDs,
		"expires_at":               report.ExpiresAt,
		"updated_at":               report.UpdatedAt,
		"unit":                     "credits",
	}
	rawOtherInfo, err := common.Marshal(otherInfo)
	if err != nil {
		return err
	}

	return model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Updates(map[string]interface{}{
		"balance":              balance,
		"balance_updated_time": report.UpdatedAt,
		"remark":               remark,
		"other_info":           string(rawOtherInfo),
	}).Error
}

func mimoCreditReportBalance(cfg *operation_setting.MimoCreditSetting, report mimoCreditGuardState) float64 {
	balance := float64(report.RemainingCredits)
	if strings.EqualFold(strings.TrimSpace(cfg.Display.BalanceUnit), "million_credits") {
		balance = float64(report.RemainingCredits) / 1_000_000
	}
	return balance
}

func mimoCreditModelRate(modelName string, cfg *operation_setting.MimoCreditSetting) float64 {
	modelKey := normalizeMimoModelName(modelName)
	rates := cfg.ModelCreditRates
	if rates != nil {
		for key, rate := range rates {
			if normalizeMimoModelName(key) == modelKey {
				return rate
			}
		}
	}
	return nonZeroFloat(cfg.DefaultModelCreditRate, 1)
}

func mimoCreditTimeRate(createdAt int64, cfg *operation_setting.MimoCreditSetting) (float64, error) {
	if !cfg.NightDiscount.Enabled {
		return 1, nil
	}
	start, err := parseGuardHHMM(defaultString(cfg.NightDiscount.Start, "00:00"))
	if err != nil {
		return 0, err
	}
	end, err := parseGuardHHMM(defaultString(cfg.NightDiscount.End, "08:00"))
	if err != nil {
		return 0, err
	}
	loc := time.Local
	if strings.TrimSpace(cfg.Timezone) != "" {
		loaded, loadErr := time.LoadLocation(cfg.Timezone)
		if loadErr == nil {
			loc = loaded
		}
	}
	current := time.Unix(createdAt, 0).In(loc)
	minute := current.Hour()*60 + current.Minute()
	if inGuardMinuteWindow(minute, start, end) {
		return nonZeroFloat(cfg.NightDiscount.Multiplier, 0.8), nil
	}
	return 1, nil
}

func normalizeMimoModelName(name string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(name, "[", 2)[0]))
}

func mimoCreditInt(value interface{}) int64 {
	switch v := value.(type) {
	case nil:
		return 0
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0
		}
		return int64(parsed)
	default:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(v)), 64)
		if err != nil {
			return 0
		}
		return int64(parsed)
	}
}

func formatMimoCredits(value int64) string {
	absValue := math.Abs(float64(value))
	if absValue >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(value)/1_000_000)
	}
	if absValue >= 1_000 {
		return fmt.Sprintf("%.2fK", float64(value)/1_000)
	}
	return strconv.FormatInt(value, 10)
}

func nonZeroFloat(value float64, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
}
