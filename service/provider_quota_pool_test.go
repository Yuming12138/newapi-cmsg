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
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testGeminiPoolRate(t *testing.T) {
	t.Helper()
	cfg := operation_setting.GetGeminiQuotaEstimateSetting()
	original := cfg.USDPerPercent
	cfg.USDPerPercent = map[string]float64{"g1-pro-tier": 0.1}
	t.Cleanup(func() { cfg.USDPerPercent = original })
}

func testGeminiPoolChannel(t *testing.T, id int, updatedAt int64, accounts []GeminiCPAAccount) *model.Channel {
	t.Helper()
	channel := testGeminiCPAChannel()
	channel.Id, channel.Status, channel.Group = id, common.ChannelStatusEnabled, "gemini,asxs"
	raw, err := common.Marshal(map[string]any{geminiCPAQuotaInfoKey: GeminiCPAQuotaBalance{
		Source: geminiCPAQuotaInfoKey, UpdatedAt: updatedAt, Accounts: accounts,
	}})
	require.NoError(t, err)
	channel.OtherInfo = string(raw)
	return channel
}

func testGeminiPoolAccount(id string, percent float64) GeminiCPAAccount {
	return GeminiCPAAccount{ID: id, Tier: "g1-pro-tier", RemainingPercent: &percent, Models: []GeminiCPAModelQuota{
		{Model: "gemini-3-flash", RemainingPercent: &percent},
		{Model: "gemini-3.8-flash-tiered", RemainingPercent: &percent},
	}}
}

func testDeepSeekPoolChannel(id int, key string, usd float64, updatedAt int64) *model.Channel {
	baseURL := "https://api.deepseek.com"
	return &model.Channel{Id: id, Key: key, Type: constant.ChannelTypeDeepSeek, BaseURL: &baseURL,
		Group: "deepseek", Status: common.ChannelStatusEnabled, Balance: usd, BalanceUpdatedTime: updatedAt}
}

func TestDailyQuotaPoolCombinesProvidersWithoutDoubleCounting(t *testing.T) {
	testGeminiPoolRate(t)
	asxs := ChannelBudgetPoolSummary{Group: "asxs", ChannelCount: 2, AvailableChannelCount: 2,
		TotalUSD: 60, UsedUSD: 10, RemainingUSD: 50, UpdatedAt: 50}
	old := testGeminiPoolChannel(t, 35, 100, []GeminiCPAAccount{
		testGeminiPoolAccount("account-a", 80), testGeminiPoolAccount("account-b", 60),
	})
	newer := testGeminiPoolChannel(t, 36, 200, []GeminiCPAAccount{
		testGeminiPoolAccount("account-a", 50), testGeminiPoolAccount("account-c", 25),
	})
	disabled := testGeminiPoolChannel(t, 37, 300, []GeminiCPAAccount{testGeminiPoolAccount("account-a", 100)})
	disabled.Status = common.ChannelStatusManuallyDisabled
	dsOld := testDeepSeekPoolChannel(11, "same-key", 80, 100)
	dsNew := testDeepSeekPoolChannel(16, "same-key", 20, 200)
	dsDisabled := testDeepSeekPoolChannel(17, "same-key", 300, 300)
	dsDisabled.Status = common.ChannelStatusManuallyDisabled
	// Legacy metadata copied into the Gemini channel must not add a Codex pool.
	info := old.GetOtherInfo()
	info["cliproxy_cpa_quota_guard"] = map[string]any{"health": map[string]any{"ok": true,
		"dynamic_daily_budget": map[string]any{"applied": true, "daily_limit_percent": 100, "remaining_today_percent": 100}}}
	raw, err := common.Marshal(info)
	require.NoError(t, err)
	old.OtherInfo = string(raw)
	got := summarizeDailyQuotaPool(asxs, true, []*model.Channel{old, newer, disabled, dsOld, dsNew, dsDisabled}, 500000)
	// ASXS once ($50), three distinct Gemini accounts ($13.50), one wallet ($20).
	require.InDelta(t, 83.5, got.RemainingUSD, 0.000001)
	require.InDelta(t, 110, got.TotalUSD, 0.000001)
	require.InDelta(t, 26.5, got.UsedUSD, 0.000001)
	require.Equal(t, int64(41750000), got.RemainingQuota)
	require.Equal(t, "asxs,deepseek,gemini", got.Group)
	require.Len(t, got.GroupBreakdown, 3)
	require.True(t, got.Estimated)
	require.False(t, got.Partial)
	require.Equal(t, 3, got.GroupBreakdown[1].AccountCount)
	require.Equal(t, 1, got.GroupBreakdown[2].AccountCount)
	public, err := common.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(public), "same-key")
	require.NotContains(t, string(public), "account-a")
}

func TestGeminiPoolExcludesUnavailableInvalidAndUnknownTier(t *testing.T) {
	testGeminiPoolRate(t)
	zero := testGeminiPoolAccount("zero", 0)
	cooling := testGeminiPoolAccount("cooling", 100)
	cooling.RuntimeUnavailable = true
	failed := testGeminiPoolAccount("failed", 100)
	failed.Error = "quota lookup failed"
	unknown := testGeminiPoolAccount("unknown", 100)
	unknown.Tier = "unpriced-tier"
	invalid := testGeminiPoolAccount("invalid", math.Inf(1))
	// JSON rejects Inf; test invalid stored percentages with an out-of-range value.
	percent := 101.0
	invalid.RemainingPercent = &percent
	invalid.Models = nil
	channel := testGeminiPoolChannel(t, 35, 100, []GeminiCPAAccount{zero, cooling, failed, unknown, invalid})
	group, handled := geminiDailyQuotaPoolGroup([]*model.Channel{channel})
	require.True(t, handled)
	require.True(t, group.Partial)
	require.False(t, group.Available)
	require.Zero(t, group.RemainingUSD)
	require.InDelta(t, 10, group.UsedUSD, 0.000001)

	// The newest failed account snapshot supersedes the previous usable value.
	old := testGeminiPoolChannel(t, 36, 50, []GeminiCPAAccount{testGeminiPoolAccount("failed", 90)})
	group, _ = geminiDailyQuotaPoolGroup([]*model.Channel{old, channel})
	require.Zero(t, group.RemainingUSD)

	// An independent Claude pool must not increase the Gemini estimate.
	account := testGeminiPoolAccount("gemini", 80)
	low, full := 20.0, 100.0
	account.Models = append(account.Models, GeminiCPAModelQuota{Model: "gemini-pro-agent", RemainingPercent: &low},
		GeminiCPAModelQuota{Model: "claude-opus", RemainingPercent: &full})
	group, _ = geminiDailyQuotaPoolGroup([]*model.Channel{testGeminiPoolChannel(t, 38, 200, []GeminiCPAAccount{account})})
	require.InDelta(t, 2, group.RemainingUSD, 0.000001)
}

func TestDeepSeekPoolMissingZeroAndInvalidBalances(t *testing.T) {
	zero := testDeepSeekPoolChannel(11, "zero", 0, 100)
	group, handled := deepSeekDailyQuotaPoolGroup([]*model.Channel{zero})
	require.True(t, handled)
	require.False(t, group.Partial)
	require.Zero(t, group.RemainingUSD)
	missing := testDeepSeekPoolChannel(12, "missing", 99, 0)
	invalid := testDeepSeekPoolChannel(13, "invalid", math.NaN(), 200)
	noKey := testDeepSeekPoolChannel(14, "", 200, 200)
	group, _ = deepSeekDailyQuotaPoolGroup([]*model.Channel{zero, missing, invalid, noKey})
	require.True(t, group.Partial)
	require.Zero(t, group.RemainingUSD)
}

func TestUnlockedPoolIncludesGeminiAndDeepSeekWithRestrictedPolicyIntact(t *testing.T) {
	testGeminiPoolRate(t)
	originalDB, originalRedis := model.DB, common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "pool.db")), &gorm.Config{})
	require.NoError(t, err)
	model.DB, common.RedisEnabled = db, false
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = originalDB, originalRedis
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.User{}))
	gemini := testGeminiPoolChannel(t, 35, 100, []GeminiCPAAccount{testGeminiPoolAccount("account", 80)})
	require.NoError(t, db.Create(gemini).Error)
	require.NoError(t, db.Create(testDeepSeekPoolChannel(11, "key", 27.3, 200)).Error)
	cfg := &operation_setting.UserQuotaGuardSetting{DaytimeBaseUSD: 40, PerUserBaseUSD: map[string]float64{"3": 10},
		UnlockedQuotaSource: "daily_quota_pool", UnlockedQuotaUSD: 405}
	usd, source, err := resolveUserQuotaGuardUnlockedQuota(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, "daily_quota_pool", source)
	require.InDelta(t, 35.3, usd, 0.000001)
	user := &model.User{Id: 2, Username: "ordinary", AffCode: "test", Quota: 0}
	require.NoError(t, db.Create(user).Error)
	state, changed, err := applyUserQuotaGuardPolicy(cfg, user, userQuotaGuardUserState{}, false,
		userQuotaPhase{DateKey: "2026-10-14", Phase: "restricted"}, 500000, usd, source)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 20000000, user.Quota)
	require.Equal(t, 20000000, state.AppliedRestrictedGrantQuota)
	_, changed, err = applyUserQuotaGuardPolicy(cfg, user, state, true,
		userQuotaPhase{DateKey: "2026-10-14", Phase: "unlocked"}, 500000, usd, source)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 17650000, user.Quota)
	_, _, restrictedUSD := userQuotaRestrictedUSD(cfg, "2026-10-14", 3)
	require.Equal(t, 10.0, restrictedUSD)
}
