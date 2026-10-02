package service

import (
	"context"
	"math"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// ProviderAccountBalance is an official account balance, not a daily quota.
// A nil balance means no account has supplied a usable balance yet.
type ProviderAccountBalance struct {
	Balance            *float64                 `json:"balance"`
	Currency           string                   `json:"currency"`
	UpdatedAt          int64                    `json:"updated_at"`
	ChannelCount       int                      `json:"channel_count"`
	AccountCount       int                      `json:"account_count"`
	SyncedAccountCount int                      `json:"synced_account_count"`
	Partial            bool                     `json:"partial"`
	Subscription       *KimiSubscriptionBalance `json:"subscription,omitempty"`
}

type DashboardProviderBalances struct {
	ASXS     ASXSAccountDailyBalance `json:"asxs"`
	Kimi     ProviderAccountBalance  `json:"kimi"`
	DeepSeek ProviderAccountBalance  `json:"deepseek"`
}

func GetDashboardProviderBalances(ctx context.Context) (DashboardProviderBalances, error) {
	var channels []*model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "key", "type", "base_url", "group", "status", "balance", "balance_updated_time", "models", "model_mapping", "other_info").
		Where("status = ?", common.ChannelStatusEnabled).
		Find(&channels).Error; err != nil {
		return DashboardProviderBalances{}, err
	}
	balances := summarizeDashboardProviderBalances(channels)
	asxs, err := fetchASXSAccountDailyBalance(ctx)
	if err != nil {
		balances.ASXS = unavailableASXSAccountDailyBalance()
	} else {
		balances.ASXS = asxs
	}
	return balances, nil
}

func summarizeDashboardProviderBalances(channels []*model.Channel) DashboardProviderBalances {
	deepSeekSetting := currentDeepSeekBalanceSetting()
	kimi := summarizeProviderAccountBalance(channels, isOfficialKimiChannel)
	for _, channel := range channels {
		if channel == nil || channel.Status != common.ChannelStatusEnabled || !IsKimiCPAChannel(channel) {
			continue
		}
		kimi.ChannelCount++
		snapshot := kimiSubscriptionSnapshot(channel)
		if snapshot == nil {
			snapshot = &KimiSubscriptionBalance{Source: kimiCPASubscriptionInfoKey, ChannelCount: 1, Partial: true}
		}
		if kimi.Subscription == nil || snapshot.UpdatedAt > kimi.Subscription.UpdatedAt {
			kimi.Subscription = snapshot
		}
	}
	return DashboardProviderBalances{
		ASXS: ASXSAccountDailyBalance{Currency: "USD"},
		Kimi: kimi,
		DeepSeek: summarizeProviderAccountBalance(channels, func(channel *model.Channel) bool {
			return isDeepSeekBalanceChannel(channel, deepSeekSetting)
		}),
	}
}

func isOfficialKimiChannel(channel *model.Channel) bool {
	if channel == nil || (channel.Type != constant.ChannelTypeOpenAI && channel.Type != constant.ChannelTypeMoonshot) {
		return false
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[channel.Type]
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && strings.EqualFold(parsed.Hostname(), "api.moonshot.cn")
}

func summarizeProviderAccountBalance(channels []*model.Channel, matches func(*model.Channel) bool) ProviderAccountBalance {
	summary := ProviderAccountBalance{Currency: "CNY"}
	accounts := make(map[string]*model.Channel)
	for _, channel := range channels {
		if channel == nil || channel.Status != common.ChannelStatusEnabled || !matches(channel) {
			continue
		}
		summary.ChannelCount++
		key := strings.TrimSpace(channel.Key)
		if key == "" {
			summary.Partial = true
			continue
		}
		if previous, exists := accounts[key]; !exists || channel.BalanceUpdatedTime > previous.BalanceUpdatedTime {
			accounts[key] = channel
		}
	}

	summary.AccountCount = len(accounts)
	var totalCNY float64
	for _, channel := range accounts {
		if channel.BalanceUpdatedTime <= 0 || math.IsNaN(channel.Balance) || math.IsInf(channel.Balance, 0) {
			summary.Partial = true
			continue
		}
		summary.SyncedAccountCount++
		totalCNY += channel.Balance * ratio_setting.USD2RMB
		if summary.UpdatedAt == 0 || channel.BalanceUpdatedTime < summary.UpdatedAt {
			summary.UpdatedAt = channel.BalanceUpdatedTime
		}
	}
	if summary.SyncedAccountCount < summary.AccountCount {
		summary.Partial = true
	}
	if summary.SyncedAccountCount > 0 && !summary.Partial {
		balance := math.Round(totalCNY*100) / 100
		summary.Balance = &balance
	}
	return summary
}
