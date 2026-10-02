package service

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseASXSAccountDailyBalanceSumsDistinctActiveSubscriptions(t *testing.T) {
	now := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"balanceMicros":0,
		"subscription":{"id":"sixty","expiresAt":"2026-09-30T00:00:00Z","limits":[{"limitType":"daily","limitMicros":300000000,"leftMicros":300000000}]},
		"subscriptions":[
			{"id":"sixty","expiresAt":"2026-09-30T00:00:00Z","limits":[{"limitType":"daily","limitMicros":300000000,"leftMicros":300000000}]},
			{"id":"thirty-six","expiresAt":"2026-10-05T00:00:00Z","limits":[{"limitType":"daily","limitMicros":180000000,"leftMicros":180000000}]},
			{"id":"non-daily","limits":[{"limitType":"total","limitMicros":5000000,"leftMicros":5000000}]},
			{"id":"expired","expiresAt":"2026-09-28T00:00:00Z","limits":[{"limitType":"daily","limitMicros":50000000,"leftMicros":50000000}]}
		],
		"windows":[{"limitType":"daily","limitMicros":300000000,"leftMicros":216950000}],
		"subscriptionWindows":[
			{"subscriptionId":"sixty","windows":[{"limitType":"daily","limitMicros":300000000,"leftMicros":216950000}]},
			{"subscriptionId":"thirty-six","windows":[{"limitType":"daily","limitMicros":180000000,"leftMicros":180000000}]},
			{"subscriptionId":"non-daily","windows":[{"limitType":"total","limitMicros":5000000,"leftMicros":5000000}]}
		]
	}`)
	got, err := parseASXSAccountDailyBalance(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance == nil || math.Abs(*got.Balance-80.39) > 0.000001 || got.SubscriptionCount != 3 || got.Partial || got.Currency != "USD" || got.DailyLimitUSD != 97 {
		t.Fatalf("ASXS account balance = %+v, want $80.39 from two daily and one total subscription", got)
	}
}

func TestParseASXSAccountDailyBalanceUsesTotalSubscription(t *testing.T) {
	now := time.Date(2026, time.October, 2, 10, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"balanceMicros":0,
		"subscription":{"id":"two-hundred","expiresAt":"2026-11-01T12:17:14+08:00","limits":[{"limitType":"total","limitMicros":1000000000,"leftMicros":968112206}]},
		"subscriptions":[{"id":"two-hundred","expiresAt":"2026-11-01T12:17:14+08:00","limits":[{"limitType":"total","limitMicros":1000000000,"leftMicros":968112206}]}],
		"windows":[{"limitType":"total","limitMicros":1000000000,"leftMicros":968112206}],
		"subscriptionWindows":[{"subscriptionId":"two-hundred","windows":[{"limitType":"total","limitMicros":1000000000,"leftMicros":968112206}]}]
	}`)
	got, err := parseASXSAccountDailyBalance(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance == nil || math.Abs(*got.Balance-193.62) > 0.000001 || got.SubscriptionCount != 1 || got.Partial || got.DailyLimitUSD != 200 {
		t.Fatalf("ASXS total subscription balance = %+v, want $193.62 from a $200 total subscription", got)
	}
}

func TestParseASXSAccountDailyBalancePrefersDailyWindow(t *testing.T) {
	now := time.Date(2026, time.October, 2, 10, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"balanceMicros":0,
		"subscriptions":[{"id":"mixed","limits":[{"limitType":"daily","limitMicros":300000000,"leftMicros":250000000},{"limitType":"total","limitMicros":1000000000,"leftMicros":900000000}]}],
		"subscriptionWindows":[{"subscriptionId":"mixed","windows":[{"limitType":"total","limitMicros":1000000000,"leftMicros":900000000},{"limitType":"daily","limitMicros":300000000,"leftMicros":250000000}]}]
	}`)
	got, err := parseASXSAccountDailyBalance(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance == nil || math.Abs(*got.Balance-50) > 0.000001 || got.DailyLimitUSD != 60 {
		t.Fatalf("ASXS mixed windows balance = %+v, want the daily window only", got)
	}
}

func TestParseASXSAccountDailyBalanceFallsBackToStaticSubscriptionLimit(t *testing.T) {
	now := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"balanceMicros":0,
		"subscription":{"id":"sixty","limits":[{"limitType":"daily","limitMicros":300000000,"leftMicros":300000000}]},
		"subscriptions":[
			{"id":"thirty-six","limits":[{"limitType":"daily","limitMicros":180000000,"leftMicros":180000000}]}
		],
		"windows":[{"limitType":"daily","limitMicros":300000000,"leftMicros":216950000}]
	}`)
	got, err := parseASXSAccountDailyBalance(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance == nil || *got.Balance != 79.39 || !got.Partial || got.DailyLimitUSD != 96 {
		t.Fatalf("ASXS account daily balance = %+v, want $79.39 with static fallback marked partial", got)
	}
}

func TestParseASXSAccountDailyBalanceRejectsErrorPayload(t *testing.T) {
	got, err := parseASXSAccountDailyBalance([]byte(`{"error":"expired session"}`), time.Now())
	if err == nil || got.Balance != nil {
		t.Fatalf("incomplete account response = %+v, error=%v; want unavailable balance", got, err)
	}
}

func TestParseASXSAccountDailyBalanceKeepsExhaustedZero(t *testing.T) {
	raw := []byte(`{"balanceMicros":0,"subscriptions":[{"id":"exhausted","limits":[{"limitType":"daily","limitMicros":20000000,"leftMicros":0}]}]}`)
	got, err := parseASXSAccountDailyBalance(raw, time.Now())
	if err != nil || got.Balance == nil || *got.Balance != 0 {
		t.Fatalf("exhausted subscription = %+v, error=%v; want a real $0 balance", got, err)
	}
}

func TestASXSAccountTokenRotationPreservesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("old-token-with-adequate-length\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceASXSAccountToken(path, "new-token-with-adequate-length"); err != nil {
		t.Fatal(err)
	}
	token, err := readASXSAccountToken(path)
	if err != nil || token != "new-token-with-adequate-length" {
		t.Fatalf("read rotated token: present=%v, error=%v", token != "", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("rotated token file mode: info=%v, error=%v", info, err)
	}
}

func TestReadASXSAccountTokenRejectsPublicPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("private-token-with-adequate-length\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readASXSAccountToken(path); err == nil {
		t.Fatal("read token with public permissions succeeded")
	}
}

func TestASXSAccountGuardUsageUsesAggregateDailyBalance(t *testing.T) {
	balance := 46.73
	usage, err := asxsAccountGuardUsage(ASXSAccountDailyBalance{
		Balance: &balance, DailyLimitUSD: 118, SubscriptionCount: 4,
	})
	if err != nil || usage.TotalUSD != 118 || math.Abs(usage.UsedUSD-71.27) > 0.000001 || usage.RemainingUSD != balance || usage.RawItems != 4 {
		t.Fatalf("account guard usage = %+v, err=%v", usage, err)
	}
	if _, err := asxsAccountGuardUsage(ASXSAccountDailyBalance{}); err == nil {
		t.Fatal("missing account balance must not replace a channel balance")
	}
}
