package service

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func TestParseASXSUsageSelectsDailyUSD(t *testing.T) {
	raw := []byte(`[
		{"planName":"monthly","unit":"USD","isValid":true,"total":300,"remaining":250,"used":50},
		{"planName":"每日$45 · 日额度","unit":"USD","isValid":true,"total":"45","remaining":"12.5"}
	]`)
	got, err := parseASXSUsage(raw)
	if err != nil {
		t.Fatalf("parseASXSUsage() error = %v", err)
	}
	if got.PlanName != "每日$45 · 日额度" || got.TotalUSD != 45 || got.RemainingUSD != 12.5 || got.UsedUSD != 32.5 {
		t.Fatalf("parseASXSUsage() = %+v", got)
	}
}

func TestInGuardMinuteWindow(t *testing.T) {
	start, err := parseGuardHHMM("09:00")
	if err != nil {
		t.Fatal(err)
	}
	end, err := parseGuardHHMM("18:00")
	if err != nil {
		t.Fatal(err)
	}
	if !inGuardMinuteWindow(9*60, start, end) || !inGuardMinuteWindow(17*60+59, start, end) {
		t.Fatalf("expected daytime values to be restricted")
	}
	if inGuardMinuteWindow(18*60, start, end) || inGuardMinuteWindow(8*60+59, start, end) {
		t.Fatalf("expected outside values to be unlocked")
	}
}

func TestFilterUserQuotaGuardUsers(t *testing.T) {
	cfg := &operation_setting.UserQuotaGuardSetting{
		AutoManage:     true,
		IncludeRoles:   []int{common.RoleCommonUser},
		IncludeGroups:  []string{"asxs"},
		IncludeUserIDs: []int{99},
		ExcludeUserIDs: []int{3},
	}
	users := []*model.User{
		{Id: 1, Role: common.RoleRootUser, Group: "asxs"},
		{Id: 2, Role: common.RoleCommonUser, Group: "asxs"},
		{Id: 3, Role: common.RoleCommonUser, Group: "asxs"},
		{Id: 4, Role: common.RoleCommonUser, Group: "mimo"},
		{Id: 99, Role: common.RoleRootUser, Group: "default"},
	}
	got := filterUserQuotaGuardUsers(cfg, users)
	if len(got) != 2 || got[0].Id != 2 || got[1].Id != 99 {
		t.Fatalf("filterUserQuotaGuardUsers() got ids %+v", got)
	}
}

func TestInitialUserQuotaGuardStateRestricted(t *testing.T) {
	cfg := &operation_setting.UserQuotaGuardSetting{
		DaytimeBaseUSD:  50,
		PerUserExtraUSD: map[string]float64{"2": 10},
	}
	state := initialUserQuotaGuardState(cfg, userQuotaPhase{DateKey: "2026-05-14", Phase: "restricted"}, 2, 500000, 0, "static")
	if state.Phase != "restricted" || state.Date != "2026-05-14" || state.AppliedRestrictedGrantQuota != 30000000 || state.AppliedExtraUSD != 10 {
		t.Fatalf("initialUserQuotaGuardState() = %+v", state)
	}
}

func TestInitialUserQuotaGuardStateUnlockedUsesResolvedPoolQuota(t *testing.T) {
	cfg := &operation_setting.UserQuotaGuardSetting{}
	state := initialUserQuotaGuardState(cfg, userQuotaPhase{DateKey: "2026-05-14", Phase: "unlocked"}, 2, 500000, 123.45, "asxs_channel_pool")
	if state.Phase != "unlocked" || state.Date != "2026-05-14" || state.AppliedUnlockedQuota != 61725000 || state.AppliedUnlockedUSD != 123.45 || state.UnlockedQuotaSource != "asxs_channel_pool" {
		t.Fatalf("initialUserQuotaGuardState() = %+v", state)
	}
}

func TestUpdateCliproxyCPAQuotaGuardBalanceUsesGuardSnapshot(t *testing.T) {
	channel := &model.Channel{
		Id:      12,
		Name:    "cliproxy-codex-pool",
		Balance: 0,
		OtherInfo: `{
			"cliproxy_cpa_quota_guard": {
				"managed": true,
				"health": {
					"ok": true,
					"balance_units": 31,
					"remaining_share_percent": 31,
					"share_limit_percent": 50,
					"windows": {
						"5h": {"used_percent": 15, "remaining_percent": 85, "reset_after_seconds": 5916},
						"7d": {"used_percent": 19, "remaining_percent": 81, "reset_after_seconds": 500089}
					}
				}
			}
		}`,
	}
	balance, handled, err := UpdateCliproxyCPAQuotaGuardBalance(channel)
	if err != nil {
		t.Fatalf("UpdateCliproxyCPAQuotaGuardBalance() error = %v", err)
	}
	if !handled || balance != 31 || channel.Balance != 31 {
		t.Fatalf("balance=%v handled=%v channel.Balance=%v", balance, handled, channel.Balance)
	}
}

func TestASXSChannelBudgetPoolIncludesXMAPIGroupFallback(t *testing.T) {
	asxsBaseURL := "https://api.asxs.top"
	xmapiBaseURL := "https://code.xmapi.cc"
	cfg := &operation_setting.ChannelBudgetGuardSetting{
		QuotaPerUSD: 500000,
		AutoDiscovery: operation_setting.ChannelBudgetGuardAutoDiscoverySetting{
			Enabled: true,
			ASXS: operation_setting.ChannelBudgetGuardASXSDiscoverySetting{
				Enabled:     true,
				ChannelType: 1,
				Group:       "asxs",
				BaseURL:     asxsBaseURL,
			},
		},
	}
	channels := []*model.Channel{
		{Id: 1, Type: 1, Name: "asxs-cgm", Status: common.ChannelStatusAutoDisabled, BaseURL: &asxsBaseURL, Group: "asxs", Balance: 0, UsedQuota: 45000000},
		{Id: 9, Type: 1, Name: "xmapi-cgm", Status: common.ChannelStatusEnabled, BaseURL: &xmapiBaseURL, Group: "asxs", Balance: 33.66070657, UsedQuota: 44802267, BalanceUpdatedTime: 1780484135},
	}
	managed := filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels))
	fallbacks := discoverASXSBalanceFallbackChannels(cfg, channels, managed)
	if len(managed) != 1 || managed[0].channel.Id != 1 {
		t.Fatalf("managed = %+v", managed)
	}
	if len(fallbacks) != 1 || fallbacks[0].Id != 9 {
		t.Fatalf("fallbacks = %+v", fallbacks)
	}
	summary := summarizeASXSChannelBudgetPool(cfg, managed, fallbacks, 0, false)
	if summary.ChannelCount != 2 || summary.AvailableChannelCount != 1 || summary.BalanceFallbackCount != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}
	if math.Abs(summary.RemainingUSD-33.660707) > 0.000001 || summary.RemainingQuota != 16830354 {
		t.Fatalf("summary quota = %+v", summary)
	}
}

func TestASXSChannelBudgetPoolIncludesConfiguredBalanceFallbackChannel(t *testing.T) {
	asxsBaseURL := "https://api.asxs.top"
	ainxBaseURL := "https://www.ainx.chat"
	cfg := &operation_setting.ChannelBudgetGuardSetting{
		QuotaPerUSD: 500000,
		AutoDiscovery: operation_setting.ChannelBudgetGuardAutoDiscoverySetting{
			Enabled: true,
			ASXS: operation_setting.ChannelBudgetGuardASXSDiscoverySetting{
				Enabled:                   true,
				ChannelType:               1,
				Group:                     "asxs",
				BaseURL:                   asxsBaseURL,
				BalanceFallbackChannelIDs: []int{13},
			},
		},
	}
	channels := []*model.Channel{
		{Id: 1, Type: 1, Name: "asxs-cgm", Status: common.ChannelStatusAutoDisabled, BaseURL: &asxsBaseURL, Group: "asxs", Balance: 0},
		{Id: 13, Type: 1, Name: "ainx", Status: common.ChannelStatusEnabled, BaseURL: &ainxBaseURL, Group: "asxs", Balance: 999.9649, BalanceUpdatedTime: 1781105380},
		{Id: 14, Type: 1, Name: "ainx-other", Status: common.ChannelStatusEnabled, BaseURL: &ainxBaseURL, Group: "asxs", Balance: 111},
	}
	managed := filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels))
	fallbacks := discoverASXSBalanceFallbackChannels(cfg, channels, managed)
	if len(managed) != 1 || managed[0].channel.Id != 1 {
		t.Fatalf("managed = %+v", managed)
	}
	if len(fallbacks) != 1 || fallbacks[0].Id != 13 {
		t.Fatalf("fallbacks = %+v", fallbacks)
	}
	summary := summarizeASXSChannelBudgetPool(cfg, managed, fallbacks, 0, false)
	if summary.ChannelCount != 2 || summary.AvailableChannelCount != 1 || summary.BalanceFallbackCount != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}
	if math.Abs(summary.RemainingUSD-999.9649) > 0.000001 || summary.RemainingQuota != 499982450 {
		t.Fatalf("summary quota = %+v", summary)
	}
}

func TestASXSChannelBudgetPoolIncludesOneTokenFallback(t *testing.T) {
	asxsBaseURL := "https://api.asxs.top"
	oneTokenBaseURL := "https://api.onetokenpass.xyz"
	cfg := &operation_setting.ChannelBudgetGuardSetting{
		QuotaPerUSD: 500000,
		AutoDiscovery: operation_setting.ChannelBudgetGuardAutoDiscoverySetting{
			Enabled: true,
			ASXS: operation_setting.ChannelBudgetGuardASXSDiscoverySetting{
				Enabled:     true,
				ChannelType: 1,
				Group:       "asxs",
				BaseURL:     asxsBaseURL,
			},
		},
	}
	channels := []*model.Channel{
		{Id: 1, Type: 1, Name: "asxs-cgm", Status: common.ChannelStatusAutoDisabled, BaseURL: &asxsBaseURL, Group: "asxs", Balance: 0},
		{Id: 15, Type: 1, Name: "onetoken", Status: common.ChannelStatusEnabled, BaseURL: &oneTokenBaseURL, Group: "asxs", Balance: 24.9955396, BalanceUpdatedTime: 1781685406},
	}
	managed := filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels))
	fallbacks := discoverASXSBalanceFallbackChannels(cfg, channels, managed)
	if len(managed) != 1 || managed[0].channel.Id != 1 {
		t.Fatalf("managed = %+v", managed)
	}
	if len(fallbacks) != 1 || fallbacks[0].Id != 15 {
		t.Fatalf("fallbacks = %+v", fallbacks)
	}
	summary := summarizeASXSChannelBudgetPool(cfg, managed, fallbacks, 0, false)
	if summary.ChannelCount != 2 || summary.AvailableChannelCount != 1 || summary.BalanceFallbackCount != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}
	if math.Abs(summary.RemainingUSD-24.99554) > 0.000001 || summary.RemainingQuota != 12497770 {
		t.Fatalf("summary quota = %+v", summary)
	}
}

func TestASXSChannelBudgetPoolIncludesLingDangFallback(t *testing.T) {
	asxsBaseURL := "https://api.asxs.top"
	lingDangBaseURL := "https://www.qflowapi.com"
	cfg := &operation_setting.ChannelBudgetGuardSetting{
		QuotaPerUSD: 500000,
		AutoDiscovery: operation_setting.ChannelBudgetGuardAutoDiscoverySetting{
			Enabled: true,
			ASXS: operation_setting.ChannelBudgetGuardASXSDiscoverySetting{
				Enabled:     true,
				ChannelType: 1,
				Group:       "asxs",
				BaseURL:     asxsBaseURL,
			},
		},
	}
	channels := []*model.Channel{
		{Id: 1, Type: 1, Name: "asxs-cgm", Status: common.ChannelStatusAutoDisabled, BaseURL: &asxsBaseURL, Group: "asxs", Balance: 0},
		{Id: 20, Type: 1, Name: "LingDang", Status: common.ChannelStatusEnabled, BaseURL: &lingDangBaseURL, Group: "asxs", Balance: 27.1415485, BalanceUpdatedTime: 1782321700},
	}
	managed := filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels))
	fallbacks := discoverASXSBalanceFallbackChannels(cfg, channels, managed)
	if len(managed) != 1 || managed[0].channel.Id != 1 {
		t.Fatalf("managed = %+v", managed)
	}
	if len(fallbacks) != 1 || fallbacks[0].Id != 20 {
		t.Fatalf("fallbacks = %+v", fallbacks)
	}
	summary := summarizeASXSChannelBudgetPool(cfg, managed, fallbacks, 0, false)
	if summary.ChannelCount != 2 || summary.AvailableChannelCount != 1 || summary.BalanceFallbackCount != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}
	if math.Abs(summary.RemainingUSD-27.141549) > 0.000001 || summary.RemainingQuota != 13570775 {
		t.Fatalf("summary quota = %+v", summary)
	}
}

func TestASXSChannelBudgetPoolIncludesConfiguredNewAPIFallback(t *testing.T) {
	asxsBaseURL := "https://api.asxs.top"
	zz1BaseURL := "https://zz1cc.cc.cd"
	zz1Setting := dto.ChannelSettings{
		NewAPIBalanceAccessToken: "test-token",
		NewAPIBalanceUserID:      "3693",
	}
	zz1SettingRaw, err := common.Marshal(zz1Setting)
	if err != nil {
		t.Fatal(err)
	}
	zz1SettingString := string(zz1SettingRaw)
	cfg := &operation_setting.ChannelBudgetGuardSetting{
		QuotaPerUSD: 500000,
		AutoDiscovery: operation_setting.ChannelBudgetGuardAutoDiscoverySetting{
			Enabled: true,
			ASXS: operation_setting.ChannelBudgetGuardASXSDiscoverySetting{
				Enabled:     true,
				ChannelType: 1,
				Group:       "asxs",
				BaseURL:     asxsBaseURL,
			},
		},
	}
	channels := []*model.Channel{
		{Id: 1, Type: 1, Name: "asxs-cgm", Status: common.ChannelStatusAutoDisabled, BaseURL: &asxsBaseURL, Group: "asxs", Balance: 0},
		{Id: 16, Type: 1, Name: "zz1", Status: common.ChannelStatusEnabled, BaseURL: &zz1BaseURL, Group: "asxs", Balance: 123.456, BalanceUpdatedTime: 1781945400, Setting: &zz1SettingString},
	}
	managed := filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels))
	fallbacks := discoverASXSBalanceFallbackChannels(cfg, channels, managed)
	if len(fallbacks) != 1 || fallbacks[0].Id != 16 {
		t.Fatalf("fallbacks = %+v", fallbacks)
	}
	if !hasNewAPIBalanceFallbackSetting(fallbacks[0]) {
		t.Fatalf("expected new-api balance fallback setting")
	}
	summary := summarizeASXSChannelBudgetPool(cfg, managed, fallbacks, 0, false)
	if summary.ChannelCount != 2 || summary.AvailableChannelCount != 1 || summary.BalanceFallbackCount != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}
	if math.Abs(summary.RemainingUSD-123.456) > 0.000001 || summary.RemainingQuota != 61728000 {
		t.Fatalf("summary quota = %+v", summary)
	}
}

func TestParseUsageBalanceSelectsRemaining(t *testing.T) {
	raw := []byte(`{"remaining":"24.9955396","balance":1,"total_available":2,"isValid":true,"planName":"one-token","mode":"usage"}`)
	got, err := parseUsageBalance(raw)
	if err != nil {
		t.Fatalf("parseUsageBalance() error = %v", err)
	}
	if math.Abs(got-24.9955396) > 0.0000001 {
		t.Fatalf("parseUsageBalance() = %v", got)
	}
}

func TestParseUsageBalanceSelectsDailyRateLimit(t *testing.T) {
	raw := []byte(`{
		"remaining": 1332.3462915,
		"unit": "USD",
		"mode": "quota_limited",
		"isValid": true,
		"quota": {"limit": 1350, "remaining": 1332.3462915, "used": 17.6537085, "unit": "USD"},
		"rate_limits": [
			{"window": "1d", "limit": 45, "remaining": 27.1415485, "used": 17.8584515, "reset_at": "2026-06-26T00:00:00+08:00"}
		]
	}`)
	got, err := parseUsageBalance(raw)
	if err != nil {
		t.Fatalf("parseUsageBalance() error = %v", err)
	}
	if math.Abs(got-27.1415485) > 0.0000001 {
		t.Fatalf("parseUsageBalance() = %v", got)
	}
}

func TestComputeNewAPIBalanceAmountUSD(t *testing.T) {
	got, err := computeNewAPIBalanceAmount(123456789, &newAPIStatusBalanceResponse{
		Data: struct {
			QuotaPerUnit               float64 `json:"quota_per_unit"`
			QuotaDisplayType           string  `json:"quota_display_type"`
			CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
			USDExchangeRate            float64 `json:"usd_exchange_rate"`
		}{
			QuotaPerUnit:     500000,
			QuotaDisplayType: "USD",
		},
	})
	if err != nil {
		t.Fatalf("computeNewAPIBalanceAmount() error = %v", err)
	}
	if math.Abs(got-246.913578) > 0.000001 {
		t.Fatalf("computeNewAPIBalanceAmount() = %v", got)
	}
}

func TestEstimateMimoLogCreditsUsesCacheAndNightDiscount(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &operation_setting.MimoCreditSetting{
		Timezone:               "Asia/Shanghai",
		DefaultModelCreditRate: 1,
		ModelCreditRates:       map[string]float64{"mimo-v2.5-pro": 2},
		NightDiscount: operation_setting.MimoCreditNightDiscountSetting{
			Enabled:    true,
			Start:      "00:00",
			End:        "08:00",
			Multiplier: 0.8,
		},
		Usage: operation_setting.MimoCreditUsageSetting{
			IncludeCacheReadTokens:     true,
			CacheReadMultiplier:        1,
			IncludeCacheCreationTokens: true,
			CacheCreationMultiplier:    1,
		},
	}
	row := model.Log{
		Id:               99,
		CreatedAt:        time.Date(2026, 5, 14, 1, 30, 0, 0, loc).Unix(),
		ModelName:        "mimo-v2.5-pro[cc]",
		PromptTokens:     100,
		CompletionTokens: 50,
		Other:            `{"cache_tokens":25,"cache_creation_tokens":25}`,
	}
	got, err := estimateMimoLogCredits(row, cfg)
	if err != nil {
		t.Fatalf("estimateMimoLogCredits() error = %v", err)
	}
	if got.Tokens != 200 || math.Abs(got.ModelRate-2) > 0.000001 || math.Abs(got.TimeRate-0.8) > 0.000001 || got.Credits != 320 {
		t.Fatalf("estimateMimoLogCredits() = %+v", got)
	}
}

func TestBuildMimoCreditReport(t *testing.T) {
	cfg := &operation_setting.MimoCreditSetting{
		ChannelID:              3,
		BaselineLogID:          25,
		InitialUsedCredits:     100,
		PlanTotalCredits:       1000,
		ExpiresAt:              "2026-05-30",
		DefaultModelCreditRate: 1,
		Timezone:               "Asia/Shanghai",
	}
	rows := []model.Log{
		{Id: 26, ModelName: "mimo-v2.5", PromptTokens: 10, CompletionTokens: 5},
		{Id: 27, ModelName: "unknown", PromptTokens: 20, CompletionTokens: 5},
	}
	got, err := buildMimoCreditReport(cfg, rows, 123)
	if err != nil {
		t.Fatalf("buildMimoCreditReport() error = %v", err)
	}
	if got.ChannelID != 3 || got.LastLogID != 27 || got.IncrementalCredits != 40 || got.UsedCredits != 140 || got.RemainingCredits != 860 || got.LogCountAfterBaseline != 2 {
		t.Fatalf("buildMimoCreditReport() = %+v", got)
	}
}
