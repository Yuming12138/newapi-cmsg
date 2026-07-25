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

func TestSummarizeEstimatedQuotaPoolIncludesASXSAndCliproxySources(t *testing.T) {
	channels := []*model.Channel{
		{
			Id:                 1,
			Name:               "asxs-cgm-1.2",
			Group:              "asxs",
			Status:             common.ChannelStatusEnabled,
			Balance:            12.5,
			BalanceUpdatedTime: 100,
			OtherInfo: `{
				"quota_source":{"balance":12.5,"spendable":true,"status":"available","updated_at":151,"raw_source":{"source":"asxs_usage"}}
			}`,
		},
		{
			Id:                 12,
			Name:               "cliproxy-codex-pool",
			Group:              "cliproxy-codex",
			Status:             common.ChannelStatusEnabled,
			Balance:            39,
			BalanceUpdatedTime: 200,
			OtherInfo: `{
				"cliproxy_cpa_quota_guard":{"managed":true,"updated_at":201,"health":{"ok":true,"usable_balance_units":39,"total_balance_units":59,"accounts":[
					{"ok":true,"plan_type":"plus","raw_remaining_percent":0,"remaining_share_percent":0},
					{"ok":true,"plan_type":"pro","raw_remaining_percent":58,"remaining_share_percent":58}
				]}},
				"quota_source":{"balance":39,"spendable":true,"status":"available","updated_at":202,"raw_source":{"source":"cliproxy_cpa_quota_guard"}}
			}`,
		},
		{
			Id:                 22,
			Name:               "cliproxy-disabled",
			Group:              "cliproxy-codex",
			Status:             common.ChannelStatusAutoDisabled,
			Balance:            10,
			BalanceUpdatedTime: 300,
			OtherInfo: `{
				"cliproxy_cpa_quota_guard":{"managed":true,"health":{"ok":false}},
				"quota_source":{"balance":10,"spendable":false,"status":"unknown","raw_source":{"source":"cliproxy_cpa_quota_guard"}}
			}`,
		},
	}

	got := summarizeEstimatedQuotaPool(channels, 500000)
	if got.Source != "unified_quota_source_pool" || got.Group != "asxs,cliproxy-codex" || !got.Estimated || got.EstimationBasis != "quota_source_balance_with_plan_weighted_estimates" {
		t.Fatalf("summarizeEstimatedQuotaPool() identity = %+v", got)
	}
	if got.ChannelCount != 3 || got.AvailableChannelCount != 2 || got.FailedChannelCount != 1 || !got.Partial {
		t.Fatalf("summarizeEstimatedQuotaPool() counts = %+v", got)
	}
	if got.EstimatedUSD != 907.44 || got.UsableEstimatedUSD != 907.44 || got.RemainingQuota != 453720000 || got.UpdatedAt != 300 {
		t.Fatalf("summarizeEstimatedQuotaPool() balance = %+v", got)
	}
	if len(got.GroupBreakdown) != 2 {
		t.Fatalf("summarizeEstimatedQuotaPool() group breakdown = %+v", got.GroupBreakdown)
	}
	asxsGroup := got.GroupBreakdown[0]
	if asxsGroup.Group != "asxs" || asxsGroup.ChannelCount != 1 || asxsGroup.AvailableChannelCount != 1 || asxsGroup.EstimatedUSD != 12.5 || asxsGroup.RemainingQuota != 6250000 || asxsGroup.Estimated {
		t.Fatalf("summarizeEstimatedQuotaPool() asxs group = %+v", asxsGroup)
	}
	cliproxyGroup := got.GroupBreakdown[1]
	if cliproxyGroup.Group != "cliproxy-codex" || cliproxyGroup.ChannelCount != 2 || cliproxyGroup.AvailableChannelCount != 1 || cliproxyGroup.FailedChannelCount != 1 || cliproxyGroup.EstimatedUSD != 894.94 || cliproxyGroup.RemainingQuota != 447470000 || !cliproxyGroup.Estimated || !cliproxyGroup.Partial {
		t.Fatalf("summarizeEstimatedQuotaPool() cliproxy group = %+v", cliproxyGroup)
	}
}

func TestSummarizeEstimatedQuotaPoolSkipsModelQuotaPercent(t *testing.T) {
	channels := []*model.Channel{
		{
			Id:      1,
			Name:    "asxs-cgm-1.2",
			Group:   "asxs",
			Status:  common.ChannelStatusEnabled,
			Balance: 25,
			OtherInfo: `{
				"quota_source":{"source_type":"stored_value_usd","balance":25,"spendable":true,"status":"available"}
			}`,
		},
		{
			Id:      28,
			Name:    "cliproxy-codex-spark",
			Group:   "asxs",
			Status:  common.ChannelStatusEnabled,
			Balance: 100,
			OtherInfo: `{
				"cliproxy_cpa_quota_guard":{"managed":true,"health":{"ok":true,"quota_feature":"codex_bengalfox","usable_balance_units":100}},
				"quota_source":{"source_type":"model_quota_percent","unit":"percent","balance":100,"spendable":true,"status":"available","raw_source":{"source":"cliproxy_cpa_quota_guard","quota_feature":"codex_bengalfox"}}
			}`,
		},
	}

	got := summarizeEstimatedQuotaPool(channels, 500000)
	if got.ChannelCount != 1 || got.EstimatedUSD != 25 || got.UsableEstimatedUSD != 25 || got.RemainingQuota != 12500000 {
		t.Fatalf("summarizeEstimatedQuotaPool() = %+v", got)
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

func TestUpdateCliproxyCPAQuotaGuardBalancePrefersUsableBalance(t *testing.T) {
	channel := &model.Channel{
		Id:      12,
		Name:    "cliproxy-codex-pool",
		Balance: 0,
		OtherInfo: `{
			"cliproxy_cpa_quota_guard": {
				"managed": true,
				"health": {
					"ok": true,
					"balance_units": 2602.46,
					"usable_balance_units": 93,
					"total_balance_units": 2602.46
				}
			}
		}`,
	}
	balance, handled, err := UpdateCliproxyCPAQuotaGuardBalance(channel)
	if err != nil {
		t.Fatalf("UpdateCliproxyCPAQuotaGuardBalance() error = %v", err)
	}
	if !handled || balance != 93 || channel.Balance != 93 {
		t.Fatalf("balance=%v handled=%v channel.Balance=%v", balance, handled, channel.Balance)
	}
}

func TestUpdateCliproxyCPAQuotaGuardBalanceUsesBuckets(t *testing.T) {
	channel := &model.Channel{
		Id:      12,
		Name:    "cliproxy-codex-pool",
		Balance: 0,
		OtherInfo: `{
			"cliproxy_cpa_quota_guard": {
				"managed": true,
				"health": {
					"ok": true,
					"buckets": {
						"personal": {
							"can_exhaust": true,
							"balance_units": 42.5
						},
						"protected": {
							"can_exhaust": false,
							"balance_units": 2602.46,
							"usable_balance_units": 13.25
						}
					}
				}
			}
		}`,
	}
	balance, handled, err := UpdateCliproxyCPAQuotaGuardBalance(channel)
	if err != nil {
		t.Fatalf("UpdateCliproxyCPAQuotaGuardBalance() error = %v", err)
	}
	if !handled || math.Abs(balance-55.75) > 0.000001 || math.Abs(channel.Balance-55.75) > 0.000001 {
		t.Fatalf("balance=%v handled=%v channel.Balance=%v", balance, handled, channel.Balance)
	}
}

func TestUpdateCliproxyCPAQuotaGuardBalanceUsesModelQuotaPercent(t *testing.T) {
	channel := &model.Channel{
		Id:      28,
		Name:    "cliproxy-codex-spark",
		Balance: 0,
		OtherInfo: `{
			"cliproxy_cpa_quota_guard": {
				"managed": true,
				"health": {
					"ok": true,
					"quota_feature": "codex_bengalfox",
					"quota_feature_limit_name": "GPT-5.3-Codex-Spark",
					"usable_balance_units": 100,
					"windows": {"7d": {"remaining_percent": 100, "used_percent": 0}}
				}
			}
		}`,
	}

	balance, handled, err := UpdateCliproxyCPAQuotaGuardBalance(channel)
	if err != nil {
		t.Fatalf("UpdateCliproxyCPAQuotaGuardBalance() error = %v", err)
	}
	if !handled || balance != 100 || channel.Balance != 100 {
		t.Fatalf("balance=%v handled=%v channel.Balance=%v", balance, handled, channel.Balance)
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	source, ok := otherInfo[channelQuotaSourceInfoKey].(map[string]interface{})
	if !ok || source["source_type"] != "model_quota_percent" || source["unit"] != "percent" {
		t.Fatalf("quota_source = %#v", otherInfo[channelQuotaSourceInfoKey])
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

func TestASXSQuotaSourceDailySubscription(t *testing.T) {
	source := buildASXSQuotaSource(asxsUsageResult{
		PlanName:     "每日$45",
		TotalUSD:     45,
		UsedUSD:      10,
		RemainingUSD: 35,
		Unit:         "USD",
		ResetInfo:    "daily reset",
		RawItems:     2,
	}, 35, 1782321700)
	if source["source_type"] != "daily_subscription_usd" || source["status"] != "available" || source["spendable"] != true {
		t.Fatalf("source = %+v", source)
	}
	windows, ok := source["windows"].([]map[string]interface{})
	if !ok || len(windows) != 1 || windows[0]["name"] != "1d" || windows[0]["reset_info"] != "daily reset" {
		t.Fatalf("windows = %#v", source["windows"])
	}
}

func TestUsageBalanceQuotaSourcePeriodCapWithDailyLimit(t *testing.T) {
	raw := []byte(`{
		"remaining": 1332.3462915,
		"unit": "USD",
		"mode": "quota_limited",
		"planName": "LingDang",
		"isValid": true,
		"quota": {"limit": 1350, "remaining": 1332.3462915, "used": 17.6537085, "unit": "USD"},
		"rate_limits": [
			{"window": "1d", "limit": 45, "remaining": 27.1415485, "used": 17.8584515, "reset_at": "2026-06-26T00:00:00+08:00"}
		]
	}`)
	balance, source, err := ParseUsageBalanceQuotaSource(raw, 1782321700)
	if err != nil {
		t.Fatalf("ParseUsageBalanceQuotaSource() error = %v", err)
	}
	if math.Abs(balance-27.1415485) > 0.0000001 || source["source_type"] != "period_cap_with_daily_limit" {
		t.Fatalf("balance=%v source=%+v", balance, source)
	}
	windows, ok := source["windows"].([]map[string]interface{})
	if !ok || len(windows) != 2 || windows[0]["name"] != "period" || windows[1]["name"] != "1d" {
		t.Fatalf("windows = %#v", source["windows"])
	}
}

func TestNewAPIQuotaSourceStoredValue(t *testing.T) {
	source := BuildNewAPIStoredValueQuotaSource(999, 499500000, 500000, "USD", 1782321700)
	if source["source_type"] != "stored_value_usd" || source["balance"] != float64(999) || source["spendable"] != true {
		t.Fatalf("source = %+v", source)
	}
	raw, ok := source["raw_source"].(map[string]interface{})
	if !ok || raw["source"] != "new_api_user_self" || raw["quota"] != int64(499500000) {
		t.Fatalf("raw_source = %#v", source["raw_source"])
	}
}

func TestCliproxyCPAQuotaSourceProtectedReserveIsWarningOnly(t *testing.T) {
	health := map[string]interface{}{
		"ok": true,
		"windows": map[string]interface{}{
			"5h": map[string]interface{}{"remaining_percent": 29.9, "used_percent": 70.1, "reset_after_seconds": 120},
			"7d": map[string]interface{}{"remaining_percent": 40, "used_percent": 60, "reset_after_seconds": 600},
		},
		"buckets": map[string]interface{}{
			"protected": map[string]interface{}{
				"can_exhaust":              false,
				"usable_balance_units":     29.9,
				"min_remaining_percent_5h": 30,
				"min_remaining_percent_7d": 20,
			},
		},
	}
	source := buildCliproxyCPAQuotaSource(health, 29.9, 1782321700)
	if source["source_type"] != "shared_protected_rolling_quota" || source["status"] != "available" || source["spendable"] != true || source["balance"] != 29.9 {
		t.Fatalf("source = %+v", source)
	}
	policy, ok := source["reserve_policy"].(map[string]interface{})
	if !ok || policy["min_remaining_percent_5h"] != float64(30) {
		t.Fatalf("reserve_policy = %#v", source["reserve_policy"])
	}
}

func TestCliproxyCPAQuotaSourcePlusWeeklyExhausted(t *testing.T) {
	health := map[string]interface{}{
		"ok": true,
		"windows": map[string]interface{}{
			"5h": map[string]interface{}{"remaining_percent": 80, "used_percent": 20},
			"7d": map[string]interface{}{"remaining_percent": 0, "used_percent": 100},
		},
	}
	source := buildCliproxyCPAQuotaSource(health, 10, 1782321700)
	if source["source_type"] != "rolling_window_quota" || source["status"] != "quota_7d_exhausted" || source["spendable"] != false {
		t.Fatalf("source = %+v", source)
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
