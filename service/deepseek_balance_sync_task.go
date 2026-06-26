package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
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

const (
	deepSeekBalanceDefaultTickInterval = 5 * time.Minute
	deepSeekBalanceDefaultTimeout      = 15 * time.Second
	deepSeekBalanceDefaultBaseURL      = "https://api.deepseek.com/anthropic"
	deepSeekBalanceDefaultGroup        = "deepseek-claude"
	deepSeekBalanceDefaultEndpoint     = "https://api.deepseek.com/user/balance"
	deepSeekBalanceRemark              = "DeepSeek Anthropic/Claude 兼容；base URL 自动拼接 /v1/messages；余额单位 CNY，余额自动同步自 /user/balance"
)

type deepSeekBalanceRuntimeSetting struct {
	Enabled      bool
	TickInterval time.Duration
	Timeout      time.Duration
	ChannelType  int
	BaseURL      string
	Group        string
	BalanceURL   string
}

type deepSeekBalanceInfo struct {
	Currency     string `json:"currency"`
	TotalBalance string `json:"total_balance"`
}

type deepSeekBalanceResponse struct {
	IsAvailable  bool                  `json:"is_available"`
	BalanceInfos []deepSeekBalanceInfo `json:"balance_infos"`
}

var (
	deepSeekBalanceSyncOnce    sync.Once
	deepSeekBalanceSyncRunning atomic.Bool
)

func currentDeepSeekBalanceSetting() deepSeekBalanceRuntimeSetting {
	runtimeSetting := deepSeekBalanceRuntimeSetting{
		Enabled:      true,
		TickInterval: deepSeekBalanceDefaultTickInterval,
		Timeout:      deepSeekBalanceDefaultTimeout,
		ChannelType:  constant.ChannelTypeAnthropic,
		BaseURL:      deepSeekBalanceDefaultBaseURL,
		Group:        deepSeekBalanceDefaultGroup,
		BalanceURL:   deepSeekBalanceDefaultEndpoint,
	}

	cfg := operation_setting.GetDeepSeekBalanceSetting()
	if cfg == nil {
		return runtimeSetting
	}

	runtimeSetting.Enabled = cfg.Enabled
	if cfg.TickIntervalMinutes > 0 {
		runtimeSetting.TickInterval = time.Duration(cfg.TickIntervalMinutes) * time.Minute
	}
	if cfg.TimeoutSeconds > 0 {
		runtimeSetting.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	if cfg.ChannelType > 0 {
		runtimeSetting.ChannelType = cfg.ChannelType
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		runtimeSetting.BaseURL = strings.TrimSpace(cfg.BaseURL)
	}
	if strings.TrimSpace(cfg.Group) != "" {
		runtimeSetting.Group = strings.TrimSpace(cfg.Group)
	}
	if strings.TrimSpace(cfg.BalanceURL) != "" {
		runtimeSetting.BalanceURL = strings.TrimSpace(cfg.BalanceURL)
	}
	return runtimeSetting
}

func StartDeepSeekBalanceSyncTask() {
	deepSeekBalanceSyncOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			ctx := context.Background()
			cfg := currentDeepSeekBalanceSetting()
			logger.LogInfo(ctx, fmt.Sprintf("deepseek balance sync task started: enabled=%t tick=%s timeout=%s base_url=%s group=%s", cfg.Enabled, cfg.TickInterval, cfg.Timeout, cfg.BaseURL, cfg.Group))

			runDeepSeekBalanceSyncOnce()
			for {
				time.Sleep(currentDeepSeekBalanceSetting().TickInterval)
				runDeepSeekBalanceSyncOnce()
			}
		})
	})
}

func normalizeDeepSeekBalanceURL(value string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
}

func splitNormalizedTokens(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, strings.ToLower(trimmed))
	}
	return result
}

func channelHasGroup(channelGroup string, targetGroup string) bool {
	target := strings.ToLower(strings.TrimSpace(targetGroup))
	if target == "" {
		return false
	}
	for _, group := range splitNormalizedTokens(channelGroup) {
		if group == target {
			return true
		}
	}
	return false
}

func isDeepSeekBalanceChannel(channel *model.Channel, cfg deepSeekBalanceRuntimeSetting) bool {
	if channel == nil {
		return false
	}
	if channel.Type != cfg.ChannelType {
		return false
	}
	if normalizeDeepSeekBalanceURL(channel.GetBaseURL()) != normalizeDeepSeekBalanceURL(cfg.BaseURL) {
		return false
	}
	return channelHasGroup(channel.Group, cfg.Group)
}

func UpdateDeepSeekAnthropicBalance(channel *model.Channel) (float64, error) {
	if channel == nil {
		return 0, fmt.Errorf("channel is nil")
	}

	cfg := currentDeepSeekBalanceSetting()
	proxyURL := channel.GetSetting().Proxy
	client, err := NewProxyHttpClient(proxyURL)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	balance, err := fetchDeepSeekBalance(ctx, client, channel, cfg.BalanceURL)
	if err != nil {
		return 0, err
	}

	channel.UpdateBalance(balance)
	channel.Balance = balance
	channel.BalanceUpdatedTime = common.GetTimestamp()
	if channel.Remark == nil || strings.TrimSpace(*channel.Remark) == "" {
		remark := deepSeekBalanceRemark
		if updateErr := model.DB.Model(channel).Select("remark").Updates(model.Channel{Remark: &remark}).Error; updateErr != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("deepseek balance sync: channel_id=%d remark update failed: %v", channel.Id, updateErr))
		} else {
			channel.Remark = &remark
		}
	}

	return balance, nil
}

func fetchDeepSeekBalance(ctx context.Context, client *http.Client, channel *model.Channel, balanceURL string) (float64, error) {
	if client == nil {
		return 0, fmt.Errorf("http client is nil")
	}
	if channel == nil {
		return 0, fmt.Errorf("channel is nil")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, balanceURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(channel.Key))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "new-api-deepseek-balance-sync/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		body := strings.TrimSpace(string(raw))
		if body != "" {
			return 0, fmt.Errorf("deepseek balance http %d: %s", resp.StatusCode, body)
		}
		return 0, fmt.Errorf("deepseek balance http %d", resp.StatusCode)
	}

	return parseDeepSeekBalanceResponse(raw)
}

func parseDeepSeekBalanceResponse(raw []byte) (float64, error) {
	var payload deepSeekBalanceResponse
	if err := common.Unmarshal(raw, &payload); err != nil {
		return 0, err
	}
	if !payload.IsAvailable {
		return 0, fmt.Errorf("deepseek balance is unavailable")
	}

	for _, info := range payload.BalanceInfos {
		if strings.EqualFold(strings.TrimSpace(info.Currency), "CNY") {
			balance, parseErr := parseDeepSeekBalanceValue(info.TotalBalance)
			if parseErr != nil {
				return 0, parseErr
			}
			return balance, nil
		}
	}

	return 0, fmt.Errorf("deepseek CNY balance not found")
}

func parseDeepSeekBalanceValue(value string) (float64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("deepseek balance is empty")
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func runDeepSeekBalanceSyncOnce() {
	if !deepSeekBalanceSyncRunning.CompareAndSwap(false, true) {
		return
	}
	defer deepSeekBalanceSyncRunning.Store(false)

	cfg := currentDeepSeekBalanceSetting()
	if !cfg.Enabled {
		return
	}

	ctx := context.Background()
	var channels []*model.Channel
	err := model.DB.
		Select("id", "name", "key", "type", "base_url", "group", "setting", "status", "remark").
		Where("type = ?", cfg.ChannelType).
		Order("id asc").
		Find(&channels).Error
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("deepseek balance sync query failed: %v", err))
		return
	}

	var updatedCount int
	var failedCount int
	for _, channel := range channels {
		if !isDeepSeekBalanceChannel(channel, cfg) {
			continue
		}
		if strings.TrimSpace(channel.Key) == "" {
			failedCount++
			logger.LogWarn(ctx, fmt.Sprintf("deepseek balance sync: channel_id=%d name=%s skipped because key is empty", channel.Id, channel.Name))
			continue
		}

		balance, err := UpdateDeepSeekAnthropicBalance(channel)
		if err != nil {
			failedCount++
			logger.LogWarn(ctx, fmt.Sprintf("deepseek balance sync: channel_id=%d name=%s failed: %v", channel.Id, channel.Name, err))
			continue
		}

		updatedCount++
		logger.LogInfo(ctx, fmt.Sprintf("deepseek balance sync: channel_id=%d name=%s balance_cny=%.2f", channel.Id, channel.Name, balance))
	}

	if common.DebugEnabled {
		logger.LogDebug(ctx, fmt.Sprintf("deepseek balance sync: scanned=%d updated=%d failed=%d", len(channels), updatedCount, failedCount))
	}
}
