package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

func testGeminiCPAChannel() *model.Channel {
	baseURL := "http://cliproxy-api:8317"
	mapping := `{"gpt-6-sol":"gemini-3.8-flash-tiered","gemini-2.5-flash":"gemini-3-flash"}`
	return &model.Channel{
		Id: 35, Type: constant.ChannelTypeOpenAI, BaseURL: &baseURL, ModelMapping: &mapping,
		Models: "gpt-6-sol,gemini-2.5-flash,claude-sonnet-4-6",
	}
}

func TestGeminiCPAChannelClassificationAndModelMapping(t *testing.T) {
	channel := testGeminiCPAChannel()
	if !IsGeminiCPAChannel(channel) {
		t.Fatal("Gemini CPA channel was not detected")
	}
	names := geminiCPARelevantModels(channel)
	if strings.Join(names, ",") != "claude-sonnet-4-6,gemini-3-flash,gemini-3.8-flash-tiered" {
		t.Fatalf("unexpected mapped quota models: %v", names)
	}
	official := "https://generativelanguage.googleapis.com"
	channel.BaseURL = &official
	if IsGeminiCPAChannel(channel) || IsGeminiCPAChannel(nil) {
		t.Fatal("official or nil channel classified as CPA")
	}
}

func TestGeminiCPAQuotaProtobufZeroAndUnknown(t *testing.T) {
	raw := []byte(`{"models":{
		"gemini-pro":{"quotaInfo":{"remainingFraction":0.7049081,"resetTime":"2026-10-09T15:23:11Z"}},
		"claude-sonnet-4-6":{"quotaInfo":{"resetTime":"2026-10-13T05:34:49Z"}},
		"gemini-no-quota":{},
		"gemini-invalid":{"quotaInfo":{"remainingFraction":"NaN"}}
	}}`)
	quotas, err := parseGeminiCPAModelQuotas(raw, []string{
		"gemini-pro", "claude-sonnet-4-6", "gemini-no-quota", "gemini-missing", "gemini-invalid",
	})
	if err != nil || len(quotas) != 5 {
		t.Fatalf("quotas=%+v err=%v", quotas, err)
	}
	if quotas[0].RemainingPercent == nil || *quotas[0].RemainingPercent < 70.49 || quotas[0].ResetAt == 0 {
		t.Fatalf("Gemini quota=%+v", quotas[0])
	}
	if quotas[1].RemainingPercent == nil || *quotas[1].RemainingPercent != 0 || quotas[1].ResetAt == 0 {
		t.Fatalf("omitted protobuf fraction must mean zero: %+v", quotas[1])
	}
	for _, quota := range quotas[2:] {
		if quota.RemainingPercent != nil {
			t.Fatalf("unknown/invalid quota must stay unknown: %+v", quota)
		}
	}
	remaining := geminiCPAAccountRemaining(quotas)
	if remaining == nil || *remaining < 70.49 {
		t.Fatalf("Claude exhausted quota must not zero Gemini headline: %v", remaining)
	}
}

func TestGeminiCPAParseFloatRejectsNonFiniteAndPartialNumbers(t *testing.T) {
	for _, value := range []any{"NaN", "Inf", "0.5garbage", true, nil} {
		if _, ok := geminiCPAParseFloat(value); ok {
			t.Errorf("accepted invalid quota number: %v", value)
		}
	}
}

func TestGeminiCPAIdentityDeduplicatesAccount(t *testing.T) {
	first := geminiCPAAccountID("USER@example.test", "credential-1")
	second := geminiCPAAccountID("user@example.test", "credential-2")
	if first != second || strings.Contains(first, "@") {
		t.Fatal("same Google account should have one hashed identity")
	}
	if geminiCPAAccountID("", "credential-1") == geminiCPAAccountID("", "credential-2") {
		t.Fatal("different unknown accounts should remain distinct")
	}
}

func TestGeminiCPAManagementFetchHomeChainAndPartialAccount(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-Management-Key") != "test-key" {
			t.Error("missing management auth headers")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v0/management/auth-files" {
			_, _ = w.Write([]byte(`{"files":[
				{"provider":"antigravity","auth_index":"one","email":"user@example.test"},
				{"provider":"antigravity","auth_index":"duplicate","email":"USER@example.test"},
				{"provider":"antigravity","auth_index":"two","email":"other@example.test"},
				{"provider":"antigravity","auth_index":"disabled","disabled":true},
				{"provider":"kimi","auth_index":"wrong-provider"}
			]}`))
			return
		}
		if r.URL.Path != "/v0/management/api-call" {
			t.Fatalf("must not download OAuth credential files: %s", r.URL.Path)
		}
		calls++
		var payload struct {
			Index   string            `json:"auth_index"`
			URL     string            `json:"url"`
			Data    string            `json:"data"`
			Headers map[string]string `json:"header"`
		}
		if err := common.DecodeJson(r.Body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Headers["Authorization"] != "Bearer $TOKEN$" || payload.Headers["User-Agent"] == "" {
			t.Error("CPA must substitute upstream auth and use Antigravity UA")
		}
		status := 200
		body := `{"paidTier":{"id":"g1-pro-tier"},"cloudaicompanionProject":{"id":"test-project"}}`
		if strings.HasSuffix(payload.URL, ":fetchAvailableModels") {
			var data map[string]string
			_ = common.UnmarshalJsonStr(payload.Data, &data)
			if data["project"] != "test-project" {
				t.Error("must pass project from loadCodeAssist")
			}
			body = `{"models":{"gemini-3.8-flash-tiered":{"quotaInfo":{"remainingFraction":0.7}},"gemini-3-flash":{"quotaInfo":{"remainingFraction":0.7}},"claude-sonnet-4-6":{"quotaInfo":{"resetTime":"2026-10-13T05:34:49Z"}}}}`
			if payload.Index == "two" {
				status, body = 429, "private upstream error, must not persist"
			}
		}
		raw, _ := common.Marshal(map[string]any{"status_code": status, "body": body})
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	client := &geminiCPAManagementClient{baseURL: server.URL, key: "test-key", client: server.Client()}
	summary, err := client.fetch(context.Background(), testGeminiCPAChannel())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || summary.AccountCount != 2 || summary.CredentialCount != 3 || summary.AvailableAccountCount != 1 || !summary.Partial {
		t.Fatalf("calls=%d summary=%+v", calls, summary)
	}
	if summary.RemainingPercent == nil || *summary.RemainingPercent != 70 {
		t.Fatalf("partial observed quota must remain visible: %+v", summary)
	}
	serialized, _ := common.Marshal(summary)
	for _, sensitive := range []string{"user@example", "test-project", "test-key", "private upstream", "auth_index"} {
		if strings.Contains(string(serialized), sensitive) {
			t.Errorf("snapshot leaked %s", sensitive)
		}
	}
}

func TestGeminiCPAQuotaAverageAndIndependentPools(t *testing.T) {
	makeAccount := func(id string, value float64) GeminiCPAAccount {
		zero := 0.0
		return GeminiCPAAccount{ID: id, RemainingPercent: &value, Models: []GeminiCPAModelQuota{
			{Model: "gemini-pro", RemainingPercent: &value},
			{Model: "claude-sonnet", RemainingPercent: &zero},
		}}
	}
	summary := GeminiCPAQuotaBalance{Accounts: []GeminiCPAAccount{makeAccount("one", 70), makeAccount("two", 98)}}
	summarizeGeminiCPAQuota(&summary, []string{"gemini-pro", "claude-sonnet"})
	if summary.RemainingPercent == nil || *summary.RemainingPercent != 84 || summary.AvailableAccountCount != 2 {
		t.Fatalf("average/availability=%+v", summary)
	}
	if summary.Models[1].RemainingPercent == nil || *summary.Models[1].RemainingPercent != 0 {
		t.Fatalf("Claude pool=%+v", summary.Models[1])
	}
}

func TestGeminiCPAQuotaSnapshotDoesNotChangeDollarBalance(t *testing.T) {
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })
	channel := testGeminiCPAChannel()
	channel.Balance = 12.34
	channel.OtherInfo = `{"unrelated":{"keep":true}}`
	value := 84.0
	summary := GeminiCPAQuotaBalance{Source: geminiCPAQuotaInfoKey, UpdatedAt: 123, RemainingPercent: &value}
	if err := saveGeminiCPAQuotaSnapshot(channel, summary); err != nil {
		t.Fatal(err)
	}
	if channel.Balance != 12.34 || channel.BalanceUpdatedTime != 123 || channel.GetOtherInfo()["unrelated"] == nil {
		t.Fatalf("mutated unrelated channel data: %+v", channel)
	}
}

func TestGeminiCPARefreshFailureDoesNotReuseOldQuotaOrMutateCachedChannel(t *testing.T) {
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("secret upstream detail"))
	}))
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "test-key")
	if err := os.WriteFile(keyPath, []byte("fixture-management-key"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_CPA_MANAGEMENT_BASE_URL", server.URL)
	t.Setenv("GEMINI_CPA_MANAGEMENT_KEY_FILE", keyPath)
	channel := testGeminiCPAChannel()
	channel.Balance = 12.34
	channel.OtherInfo = `{"gemini_cpa_quota":{"remaining_percent":100}}`
	before := channel.OtherInfo
	balance, err := UpdateGeminiCPAQuotaBalance(context.Background(), channel)
	if err == nil || balance != 0 || channel.Balance != 12.34 || channel.OtherInfo != before {
		t.Fatalf("failure must not return old quota or mutate cached channel: balance=%v err=%v", balance, err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "fixture-management-key") {
		t.Fatalf("error must not expose upstream body or credentials: %v", err)
	}
}
