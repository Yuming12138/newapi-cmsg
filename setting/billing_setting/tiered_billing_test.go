package billing_setting

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
)

func TestDefaultGPT56AndGPT6TieredPricing(t *testing.T) {
	tests := []struct {
		model              string
		standardInput      float64
		standardCached     float64
		standardCacheWrite float64
		standardOutput     float64
		longInput          float64
		longCached         float64
		longCacheWrite     float64
		longOutput         float64
	}{
		{model: "gpt-6-luna", standardInput: 0.10, standardCached: 0.01, standardCacheWrite: 0.125, standardOutput: 0.50, longInput: 0.20, longCached: 0.02, longCacheWrite: 0.25, longOutput: 0.75},
		{model: "gpt-6-sol", standardInput: 2.00, standardCached: 0.20, standardCacheWrite: 2.50, standardOutput: 10.00, longInput: 4.00, longCached: 0.40, longCacheWrite: 5.00, longOutput: 15.00},
		{model: "gpt-6.1-sol", standardInput: 2.00, standardCached: 0.10, standardCacheWrite: 2.50, standardOutput: 10.00, longInput: 4.00, longCached: 0.20, longCacheWrite: 5.00, longOutput: 15.00},
		{model: "gpt-5.6-luna", standardInput: 0.20, standardCached: 0.02, standardCacheWrite: 0.25, standardOutput: 1.20, longInput: 0.40, longCached: 0.04, longCacheWrite: 0.50, longOutput: 1.80},
		{model: "gpt-5.6-terra", standardInput: 2.00, standardCached: 0.20, standardCacheWrite: 2.50, standardOutput: 12.00, longInput: 4.00, longCached: 0.40, longCacheWrite: 5.00, longOutput: 18.00},
		{model: "gpt-5.6-sol", standardInput: 4.00, standardCached: 0.40, standardCacheWrite: 5.00, standardOutput: 20.00, longInput: 8.00, longCached: 0.80, longCacheWrite: 10.00, longOutput: 30.00},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if mode := defaultBillingMode[tt.model]; mode != BillingModeTieredExpr {
				t.Fatalf("billing mode = %q, want %q", mode, BillingModeTieredExpr)
			}
			expr, ok := defaultBillingExpr[tt.model]
			if !ok {
				t.Fatalf("missing default billing expression for %s", tt.model)
			}
			if err := SmokeTestExpr(expr); err != nil {
				t.Fatalf("billing expression smoke test failed: %v", err)
			}

			assertTierPrice(t, expr, 272000, "standard", billingexpr.TokenParams{P: 1}, tt.standardInput)
			assertTierPrice(t, expr, 272000, "standard", billingexpr.TokenParams{CR: 1}, tt.standardCached)
			assertTierPrice(t, expr, 272000, "standard", billingexpr.TokenParams{CC: 1}, tt.standardCacheWrite)
			assertTierPrice(t, expr, 272000, "standard", billingexpr.TokenParams{C: 1}, tt.standardOutput)

			assertTierPrice(t, expr, 272001, "long_context", billingexpr.TokenParams{P: 1}, tt.longInput)
			assertTierPrice(t, expr, 272001, "long_context", billingexpr.TokenParams{CR: 1}, tt.longCached)
			assertTierPrice(t, expr, 272001, "long_context", billingexpr.TokenParams{CC: 1}, tt.longCacheWrite)
			assertTierPrice(t, expr, 272001, "long_context", billingexpr.TokenParams{C: 1}, tt.longOutput)
		})
	}
}

func TestDeepSeekOfficialPeakAndOffPeakPricing(t *testing.T) {
	beijing := time.FixedZone("CST", 8*60*60)
	periods := []struct {
		name string
		at   time.Time
		peak bool
	}{
		{"weekday_peak", time.Date(2026, 9, 29, 9, 0, 0, 0, beijing), true},
		{"lunch_offpeak", time.Date(2026, 9, 29, 12, 0, 0, 0, beijing), false},
		{"afternoon_peak", time.Date(2026, 9, 29, 14, 0, 0, 0, beijing), true},
		{"evening_offpeak", time.Date(2026, 9, 29, 18, 0, 0, 0, beijing), false},
		{"national_holiday", time.Date(2026, 10, 1, 10, 0, 0, 0, beijing), false},
		{"makeup_saturday", time.Date(2026, 10, 10, 10, 0, 0, 0, beijing), false},
		{"unknown_calendar_year", time.Date(2027, 1, 4, 10, 0, 0, 0, beijing), false},
	}
	models := []struct {
		name   string
		prices [3]float64 // off-peak CNY/1M: input, cache hit, output
	}{
		{"deepseek-flash", [3]float64{1, 0.02, 4}},
		{"deepseek-v4-pro", [3]float64{4.5, 0.15, 13.5}},
	}
	for _, model := range models {
		expr := defaultBillingExpr[model.name]
		if defaultBillingMode[model.name] != BillingModeTieredExpr {
			t.Fatalf("%s is not using tiered pricing", model.name)
		}
		if err := SmokeTestExpr(expr); err != nil {
			t.Fatalf("%s expression: %v", model.name, err)
		}
		for _, period := range periods {
			t.Run(model.name+"/"+period.name, func(t *testing.T) {
				wantTier := "offpeak"
				multiplier := 1.0
				if period.peak {
					wantTier = "peak"
					multiplier = 2
				}
				for i, params := range []billingexpr.TokenParams{
					{P: 1_000_000}, {CR: 1_000_000}, {C: 1_000_000},
				} {
					usd, trace, err := billingexpr.RunExprWithRequest(expr, params, billingexpr.RequestInput{At: period.at})
					if err != nil {
						t.Fatal(err)
					}
					if got, want := usd/1_000_000*7.3, model.prices[i]*multiplier; math.Abs(got-want) > 1e-9 {
						t.Fatalf("price[%d] = %.12f CNY, want %.12f", i, got, want)
					}
					if trace.MatchedTier != wantTier {
						t.Fatalf("tier = %q, want %q", trace.MatchedTier, wantTier)
					}
				}
			})
		}
	}
	for alias, canonical := range map[string]string{
		"deepseek-v4-flash":   "deepseek-flash",
		"deepseek-v4-pro[1m]": "deepseek-v4-pro",
	} {
		if defaultBillingMode[alias] != BillingModeTieredExpr || defaultBillingExpr[alias] != defaultBillingExpr[canonical] {
			t.Fatalf("%s does not inherit %s pricing", alias, canonical)
		}
	}
}

func assertTierPrice(t *testing.T, expr string, inputLength float64, wantTier string, params billingexpr.TokenParams, want float64) {
	t.Helper()
	params.Len = inputLength
	got, trace, err := billingexpr.RunExpr(expr, params)
	if err != nil {
		t.Fatalf("run billing expression: %v", err)
	}
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("price = %v, want %v", got, want)
	}
	if trace.MatchedTier != wantTier {
		t.Fatalf("matched tier = %q, want %q", trace.MatchedTier, wantTier)
	}
}

func TestBillingSettingPersistedValuesOverrideWithoutDroppingDefaults(t *testing.T) {
	settings := BillingSetting{
		BillingMode: cloneStringMap(defaultBillingMode),
		BillingExpr: cloneStringMap(defaultBillingExpr),
	}
	err := settings.UpdateConfigFromMap(map[string]string{
		BillingModeField: `{"gpt-6-sol":"ratio","custom-model":"tiered_expr"}`,
		BillingExprField: `{"custom-model":"tier(\"custom\", p * 1)"}`,
	})
	if err != nil {
		t.Fatalf("UpdateConfigFromMap() error = %v", err)
	}
	if got := settings.BillingMode["gpt-6-sol"]; got != BillingModeRatio {
		t.Fatalf("persisted override = %q, want %q", got, BillingModeRatio)
	}
	if got := settings.BillingMode["gpt-5.6-sol"]; got != BillingModeTieredExpr {
		t.Fatalf("default mode after merge = %q, want %q", got, BillingModeTieredExpr)
	}
	if got := settings.BillingExpr["custom-model"]; got != `tier("custom", p * 1)` {
		t.Fatalf("custom expression = %q", got)
	}
	if _, ok := settings.BillingExpr["gpt-6-luna"]; !ok {
		t.Fatal("default gpt-6-luna expression was dropped")
	}
}

func TestDefaultGeminiAndClaudeTieredPricing(t *testing.T) {
	tests := []struct {
		model              string
		standardInput      float64
		standardCached     float64
		standardCacheWrite float64
		standardOutput     float64
	}{
		{model: "gemini-3.8-flash", standardInput: 0.30, standardCached: 0.075, standardCacheWrite: 0, standardOutput: 1.20},
		{model: "gemini-3.7-flash", standardInput: 0.30, standardCached: 0.075, standardCacheWrite: 0, standardOutput: 1.20},
		{model: "gemini-3.5-flash-lite", standardInput: 0.30, standardCached: 0.075, standardCacheWrite: 0, standardOutput: 1.20},
		{model: "gemini-pro-agent", standardInput: 1.25, standardCached: 0.3125, standardCacheWrite: 0, standardOutput: 5.00},
		{model: "gemini-3.1-pro-low", standardInput: 1.25, standardCached: 0.3125, standardCacheWrite: 0, standardOutput: 5.00},
		{model: "claude-sonnet-5.5", standardInput: 3.00, standardCached: 0.30, standardCacheWrite: 3.75, standardOutput: 15.00},
		{model: "claude-opus-5.5", standardInput: 5.00, standardCached: 0.50, standardCacheWrite: 6.25, standardOutput: 25.00},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if mode := defaultBillingMode[tt.model]; mode != BillingModeTieredExpr {
				t.Fatalf("billing mode = %q, want %q", mode, BillingModeTieredExpr)
			}
			expr, ok := defaultBillingExpr[tt.model]
			if !ok {
				t.Fatalf("missing default billing expression for %s", tt.model)
			}
			if err := SmokeTestExpr(expr); err != nil {
				t.Fatalf("billing expression smoke test failed: %v", err)
			}

			assertTierPrice(t, expr, 1000, "standard", billingexpr.TokenParams{P: 1}, tt.standardInput)
			assertTierPrice(t, expr, 1000, "standard", billingexpr.TokenParams{CR: 1}, tt.standardCached)
			if tt.standardCacheWrite > 0 {
				assertTierPrice(t, expr, 1000, "standard", billingexpr.TokenParams{CC: 1}, tt.standardCacheWrite)
			}
			assertTierPrice(t, expr, 1000, "standard", billingexpr.TokenParams{C: 1}, tt.standardOutput)
		})
	}
}
