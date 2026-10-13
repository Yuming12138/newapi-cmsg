package service

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func finitePoolAmount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// The stored DeepSeek balance is already USD-equivalent. Reuse it unchanged;
// CNY conversion belongs to UpdateDeepSeekBalance, not pool aggregation.
func deepSeekDailyQuotaPoolGroup(channels []*model.Channel) (DailyQuotaPoolGroup, bool) {
	group := DailyQuotaPoolGroup{Source: "deepseek_wallet", Group: "deepseek"}
	cfg := currentDeepSeekBalanceSetting()
	accounts := make(map[string]*model.Channel)
	for _, channel := range channels {
		if channel == nil || channel.Status != common.ChannelStatusEnabled || !isDeepSeekBalanceChannel(channel, cfg) {
			continue
		}
		group.ChannelCount++
		key := strings.TrimSpace(channel.Key)
		if key == "" {
			group.Partial = true
			continue
		}
		if previous, exists := accounts[key]; !exists || channel.BalanceUpdatedTime > previous.BalanceUpdatedTime {
			accounts[key] = channel
		}
	}
	group.AccountCount = len(accounts)
	for _, channel := range accounts {
		if channel.BalanceUpdatedTime <= 0 || !finitePoolAmount(channel.Balance) {
			group.Partial = true
			continue
		}
		group.RemainingUSD += math.Max(channel.Balance, 0)
		if group.UpdatedAt == 0 || channel.BalanceUpdatedTime < group.UpdatedAt {
			group.UpdatedAt = channel.BalanceUpdatedTime
		}
	}
	group.TotalUSD = group.RemainingUSD
	group.Available = group.RemainingUSD > 0
	return group, group.ChannelCount > 0
}

type geminiPoolAccount struct {
	account   GeminiCPAAccount
	updatedAt int64
}

func geminiDailyQuotaPoolGroup(channels []*model.Channel) (DailyQuotaPoolGroup, bool) {
	group := DailyQuotaPoolGroup{
		Source: "gemini_cpa_subscription_estimate", Group: "gemini", Estimated: true,
		EstimationBasis: "observed_token_cost_per_quota_percent",
		EstimateRates:   make(map[string]float64),
	}
	accounts := make(map[string]geminiPoolAccount)
	for _, channel := range channels {
		if channel == nil || channel.Status != common.ChannelStatusEnabled || !IsGeminiCPAChannel(channel) {
			continue
		}
		group.ChannelCount++
		raw, ok := channel.GetOtherInfo()[geminiCPAQuotaInfoKey]
		if !ok {
			group.Partial = true
			continue
		}
		data, err := common.Marshal(raw)
		var snapshot GeminiCPAQuotaBalance
		if err != nil || common.Unmarshal(data, &snapshot) != nil || snapshot.UpdatedAt <= 0 {
			group.Partial = true
			continue
		}
		group.Partial = group.Partial || snapshot.Partial || snapshot.Error != "" || len(snapshot.Accounts) == 0
		for _, account := range snapshot.Accounts {
			id := strings.TrimSpace(account.ID)
			if id == "" {
				group.Partial = true
				continue
			}
			// Deduplicate individual accounts, including partially overlapping
			// channel inventories. Model aliases sharing a quota are not accounts.
			if previous, exists := accounts[id]; !exists || snapshot.UpdatedAt > previous.updatedAt {
				accounts[id] = geminiPoolAccount{account: account, updatedAt: snapshot.UpdatedAt}
			}
		}
	}
	group.AccountCount = len(accounts)
	rates := operation_setting.GetGeminiQuotaEstimateSetting().USDPerPercent
	for _, observed := range accounts {
		account := observed.account
		if account.Error != "" {
			group.Partial = true
			continue
		}
		if account.RuntimeUnavailable {
			continue
		}
		rate := rates[strings.ToLower(strings.TrimSpace(account.Tier))]
		percent := geminiCPAAccountRemaining(account.Models)
		if percent == nil {
			percent = account.RemainingPercent
		}
		if !finitePoolAmount(rate) || rate <= 0 || percent == nil || !finitePoolAmount(*percent) || *percent < 0 || *percent > 100 {
			group.Partial = true
			continue
		}
		group.EstimateRates[account.Tier] = rate
		group.TotalUSD += 100 * rate
		group.UsedUSD += (100 - *percent) * rate
		group.RemainingUSD += *percent * rate
		if group.UpdatedAt == 0 || observed.updatedAt < group.UpdatedAt {
			group.UpdatedAt = observed.updatedAt
		}
	}
	group.Available = group.RemainingUSD > 0
	return group, group.ChannelCount > 0
}
