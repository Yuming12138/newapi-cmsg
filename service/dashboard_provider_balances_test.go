package service

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSummarizeDashboardProviderBalances(t *testing.T) {
	kimiURL := "https://api.moonshot.cn"
	deepSeekURL := "https://api.deepseek.com"
	asxsURL := "https://api.itygk.sbs"
	channels := []*model.Channel{
		{Id: 1, Type: constant.ChannelTypeOpenAI, Key: "asxs-key", BaseURL: &asxsURL, Group: "asxs", Status: common.ChannelStatusEnabled, Balance: 50, BalanceUpdatedTime: 150},
		{Id: 33, Type: constant.ChannelTypeOpenAI, Key: "kimi-key", BaseURL: &kimiURL, Group: "kimi,asxs", Status: common.ChannelStatusEnabled, Balance: 24.98 / ratio_setting.USD2RMB, BalanceUpdatedTime: 200},
		{Id: 34, Type: constant.ChannelTypeOpenAI, Key: "kimi-key", BaseURL: &kimiURL, Group: "kimi", Status: common.ChannelStatusEnabled, Balance: 20 / ratio_setting.USD2RMB, BalanceUpdatedTime: 100},
		{Id: 11, Type: constant.ChannelTypeDeepSeek, Key: "deepseek-key", BaseURL: &deepSeekURL, Group: "deepseek", Status: common.ChannelStatusEnabled, Balance: 6.10 / ratio_setting.USD2RMB, BalanceUpdatedTime: 210},
	}

	got := summarizeDashboardProviderBalances(channels)
	if got.Kimi.Balance == nil || math.Abs(*got.Kimi.Balance-24.98) > 0.001 || got.Kimi.ChannelCount != 2 || got.Kimi.AccountCount != 1 || got.Kimi.UpdatedAt != 200 {
		t.Fatalf("Kimi balance summary = %+v, want one distinct account with CNY 24.98", got.Kimi)
	}
	if got.DeepSeek.Balance == nil || math.Abs(*got.DeepSeek.Balance-6.10) > 0.001 || got.DeepSeek.AccountCount != 1 {
		t.Fatalf("DeepSeek balance summary = %+v, want CNY 6.10", got.DeepSeek)
	}
}

func TestSummarizeProviderAccountBalanceDistinguishesMissingFromZero(t *testing.T) {
	kimiURL := "https://api.moonshot.cn"
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "kimi-key", BaseURL: &kimiURL, Status: common.ChannelStatusEnabled}
	missing := summarizeProviderAccountBalance([]*model.Channel{channel}, isOfficialKimiChannel)
	if missing.Balance != nil || !missing.Partial {
		t.Fatalf("missing balance = %+v, want unavailable and partial", missing)
	}
	channel.BalanceUpdatedTime = 100
	zero := summarizeProviderAccountBalance([]*model.Channel{channel}, isOfficialKimiChannel)
	if zero.Balance == nil || *zero.Balance != 0 || zero.Partial {
		t.Fatalf("zero balance = %+v, want available CNY zero", zero)
	}
	second := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "other-kimi-key", BaseURL: &kimiURL, Status: common.ChannelStatusEnabled}
	partial := summarizeProviderAccountBalance([]*model.Channel{channel, second}, isOfficialKimiChannel)
	if partial.Balance != nil || !partial.Partial || partial.AccountCount != 2 {
		t.Fatalf("partial balance = %+v, want no misleading total", partial)
	}
}

func TestDashboardASXSUsageBalanceUsesOnlyConfiguredSharedAllowance(t *testing.T) {
	for _, partial := range []bool{false, true} {
		pool := summarizeDailyQuotaPool(ChannelBudgetPoolSummary{
			Source: "asxs_usage", Group: "asxs", ChannelCount: 1,
			TotalUSD: 60, RemainingUSD: 57.231487, UpdatedAt: 100, Partial: partial,
		}, true, nil, 500000)
		pool.RemainingUSD = 183.56
		pool.UpdatedAt = 200
		pool.Partial = !partial
		pool.GroupBreakdown = append(pool.GroupBreakdown,
			DailyQuotaPoolGroup{Source: "cli2api_credits", RemainingUSD: 100, UpdatedAt: 200},
			DailyQuotaPoolGroup{Source: "gemini", RemainingUSD: 26.328513, UpdatedAt: 200})

		got, handled := dashboardASXSUsageBalance(pool)
		require.True(t, handled)
		require.Equal(t, "asxs_usage", got.Source)
		require.Equal(t, "USD", got.Currency)
		require.NotNil(t, got.Balance)
		require.Equal(t, 57.23, *got.Balance)
		require.Equal(t, int64(100), got.UpdatedAt)
		require.Equal(t, partial, got.Partial)
		require.Equal(t, 60.0, got.DailyLimitUSD)
		require.Zero(t, got.SubscriptionCount)
	}
}

func TestDashboardASXSUsageBalanceDistinguishesZeroFromUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining float64
		updatedAt int64
		available bool
	}{
		{"synced zero", 0, 100, true},
		{"unsynced zero", 0, 0, false},
		{"unsynced positive", 50, 0, false},
		{"negative", -1, 100, false},
		{"nan", math.NaN(), 100, false},
		{"infinite", math.Inf(1), 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, handled := dashboardASXSUsageBalance(DailyQuotaPoolSummary{GroupBreakdown: []DailyQuotaPoolGroup{{
				Source: "asxs_daily_subscription", balanceSource: "asxs_usage",
				RemainingUSD: tc.remaining, UpdatedAt: tc.updatedAt,
			}}})
			require.True(t, handled, "usage mode must not fall back to account billing")
			require.Equal(t, "asxs_usage", got.Source)
			if tc.available {
				require.NotNil(t, got.Balance)
				require.Zero(t, *got.Balance)
				require.False(t, got.Partial)
			} else {
				require.Nil(t, got.Balance)
				require.True(t, got.Partial)
			}
		})
	}
}

func TestDashboardASXSUsageBalancePreservesAccountMode(t *testing.T) {
	for _, source := range []string{"asxs_account", ""} {
		pool := summarizeDailyQuotaPool(ChannelBudgetPoolSummary{
			Source: source, ChannelCount: 1, RemainingUSD: 50, UpdatedAt: 100,
		}, true, nil, 500000)
		_, handled := dashboardASXSUsageBalance(pool)
		require.False(t, handled)
	}
	_, handled := dashboardASXSUsageBalance(DailyQuotaPoolSummary{})
	require.False(t, handled)
}

func TestGetDashboardProviderBalancesUsesUsageSnapshotWithoutAccountToken(t *testing.T) {
	originalDB, originalRedis := model.DB, common.RedisEnabled
	cfg := operation_setting.GetChannelBudgetGuardSetting()
	originalCfg := *cfg
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "dashboard.db")), &gorm.Config{})
	require.NoError(t, err)
	model.DB, common.RedisEnabled = db, false
	*cfg = operation_setting.ChannelBudgetGuardSetting{
		Enabled: true, QuotaPerUSD: 500000,
		Channels: []operation_setting.ChannelBudgetGuardChannelSetting{
			{ID: 1, Source: "asxs_usage", Enabled: true},
			{ID: 27, Enabled: false}, // The mirror is not a second allowance.
		},
		SharedBalancePools: []operation_setting.ChannelBudgetGuardSharedPool{{SourceChannelID: 1, MemberChannelIDs: []int{27}}},
	}
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = originalDB, originalRedis
		*cfg = originalCfg
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	t.Setenv("ASXS_ACCOUNT_TOKEN_FILE", filepath.Join(t.TempDir(), "missing-token"))
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	for _, id := range []int{1, 27} {
		require.NoError(t, db.Create(&model.Channel{
			Id: id, Type: constant.ChannelTypeOpenAI, Key: "test-only", Group: "asxs",
			Status: common.ChannelStatusEnabled, Balance: 57.231487, BalanceUpdatedTime: 100,
		}).Error)
	}

	for _, remaining := range []float64{57.231487, 0} {
		require.NoError(t, db.Model(&model.Channel{}).Where("id IN ?", []int{1, 27}).Update("balance", remaining).Error)
		got, err := GetDashboardProviderBalances(context.Background())
		require.NoError(t, err)
		require.Equal(t, "asxs_usage", got.ASXS.Source)
		require.NotNil(t, got.ASXS.Balance)
		require.Equal(t, math.Round(remaining*100)/100, *got.ASXS.Balance)
		require.Equal(t, int64(100), got.ASXS.UpdatedAt)
		require.False(t, got.ASXS.Partial)
		require.NotNil(t, got.DailyQuotaPool)
		require.Len(t, got.DailyQuotaPool.GroupBreakdown, 1)
		require.Equal(t, 1, got.DailyQuotaPool.GroupBreakdown[0].ChannelCount)
		require.InDelta(t, remaining, got.DailyQuotaPool.RemainingUSD, 0.000001)
	}
}
