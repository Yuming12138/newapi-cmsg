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
		"subscription":{"id":"sixty","expiresAt":"2026-09-30T00:00:00Z","limits":[{"limitType":"daily","limitMicros":60000000,"leftMicros":60000000}]},
		"subscriptions":[
			{"id":"sixty","expiresAt":"2026-09-30T00:00:00Z","limits":[{"limitType":"daily","limitMicros":60000000,"leftMicros":60000000}]},
			{"id":"thirty-six","expiresAt":"2026-10-05T00:00:00Z","limits":[{"limitType":"daily","limitMicros":36000000,"leftMicros":36000000}]},
			{"id":"non-daily","limits":[{"limitType":"total","limitMicros":1000000,"leftMicros":1000000}]},
			{"id":"expired","expiresAt":"2026-09-28T00:00:00Z","limits":[{"limitType":"daily","limitMicros":10000000,"leftMicros":10000000}]}
		],
		"windows":[{"limitType":"daily","limitMicros":60000000,"leftMicros":43390000}],
		"subscriptionWindows":[
			{"subscriptionId":"sixty","windows":[{"limitType":"daily","limitMicros":60000000,"leftMicros":43390000}]},
			{"subscriptionId":"thirty-six","windows":[{"limitType":"daily","limitMicros":36000000,"leftMicros":36000000}]}
		]
	}`)
	got, err := parseASXSAccountDailyBalance(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance == nil || math.Abs(*got.Balance-79.39) > 0.000001 || got.SubscriptionCount != 3 || got.Partial || got.Currency != "USD" {
		t.Fatalf("ASXS account daily balance = %+v, want $79.39 from two daily subscriptions", got)
	}
}

func TestParseASXSAccountDailyBalanceFallsBackToStaticSubscriptionLimit(t *testing.T) {
	now := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"balanceMicros":0,
		"subscription":{"id":"sixty","limits":[{"limitType":"daily","limitMicros":60000000,"leftMicros":60000000}]},
		"subscriptions":[
			{"id":"thirty-six","limits":[{"limitType":"daily","limitMicros":36000000,"leftMicros":36000000}]}
		],
		"windows":[{"limitType":"daily","limitMicros":60000000,"leftMicros":43390000}]
	}`)
	got, err := parseASXSAccountDailyBalance(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance == nil || *got.Balance != 79.39 || !got.Partial {
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
	raw := []byte(`{"balanceMicros":0,"subscriptions":[{"id":"exhausted","limits":[{"limitType":"daily","limitMicros":4000000,"leftMicros":0}]}]}`)
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
