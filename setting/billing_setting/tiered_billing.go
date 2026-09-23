package billing_setting

import (
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

// Official GPT-5.6/GPT-6 Codex pricing uses a higher rate once the complete
// input context exceeds 272K tokens. Keep these defaults in the source tree so
// a fresh deployment and an existing deployment with an older/empty option
// value use the same billing contract. An explicitly stored per-model mode can
// still override the default (for example, setting a model back to "ratio").
var defaultBillingMode = map[string]string{
	"gpt-6-luna":    BillingModeTieredExpr,
	"gpt-6-sol":     BillingModeTieredExpr,
	"gpt-5.6-luna":  BillingModeTieredExpr,
	"gpt-5.6-terra": BillingModeTieredExpr,
	"gpt-5.6-sol":   BillingModeTieredExpr,
}

var defaultBillingExpr = map[string]string{
	"gpt-6-luna":    `len <= 272000 ? tier("standard", p * 0.10 + cr * 0.01 + cc * 0.125 + c * 0.50) : tier("long_context", p * 0.20 + cr * 0.02 + cc * 0.25 + c * 0.75)`,
	"gpt-6-sol":     `len <= 272000 ? tier("standard", p * 2.00 + cr * 0.20 + cc * 2.50 + c * 10.00) : tier("long_context", p * 4.00 + cr * 0.40 + cc * 5.00 + c * 15.00)`,
	"gpt-5.6-luna":  `len <= 272000 ? tier("standard", p * 0.20 + cr * 0.02 + cc * 0.25 + c * 1.20) : tier("long_context", p * 0.40 + cr * 0.04 + cc * 0.50 + c * 1.80)`,
	"gpt-5.6-terra": `len <= 272000 ? tier("standard", p * 2.00 + cr * 0.20 + cc * 2.50 + c * 12.00) : tier("long_context", p * 4.00 + cr * 0.40 + cc * 5.00 + c * 18.00)`,
	"gpt-5.6-sol":   `len <= 272000 ? tier("standard", p * 4.00 + cr * 0.40 + cc * 5.00 + c * 20.00) : tier("long_context", p * 8.00 + cr * 0.80 + cc * 10.00 + c * 30.00)`,
}

func cloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

var billingSetting = BillingSetting{
	BillingMode: cloneStringMap(defaultBillingMode),
	BillingExpr: cloneStringMap(defaultBillingExpr),
}

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// UpdateConfigFromMap merges persisted settings over the built-in official
// pricing defaults. Older databases may contain an empty or pre-GPT-6 map;
// replacing the defaults wholesale would silently revert the new models to
// ratio billing after restart.
func (s *BillingSetting) UpdateConfigFromMap(configMap map[string]string) error {
	if raw, ok := configMap[BillingModeField]; ok {
		incoming := make(map[string]string)
		if err := json.Unmarshal([]byte(raw), &incoming); err != nil {
			return fmt.Errorf("decode %s: %w", BillingModeField, err)
		}
		merged := cloneStringMap(defaultBillingMode)
		for key, value := range incoming {
			merged[key] = value
		}
		s.BillingMode = merged
	}
	if raw, ok := configMap[BillingExprField]; ok {
		incoming := make(map[string]string)
		if err := json.Unmarshal([]byte(raw), &incoming); err != nil {
			return fmt.Errorf("decode %s: %w", BillingExprField, err)
		}
		merged := cloneStringMap(defaultBillingExpr)
		for key, value := range incoming {
			merged[key] = value
		}
		s.BillingExpr = merged
	}
	return nil
}

// DefaultBillingExpressions returns a defensive copy for tests and migration
// tooling without exposing the mutable live configuration map.
func DefaultBillingExpressions() map[string]string {
	return cloneStringMap(defaultBillingExpr)
}

// DefaultBillingModes returns a defensive copy of the built-in tier selection.
func DefaultBillingModes() map[string]string {
	return cloneStringMap(defaultBillingMode)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

func GetBillingExpr(model string) (string, bool) {
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

func GetBillingModeCopy() map[string]string {
	return lo.Assign(billingSetting.BillingMode)
}

func GetBillingExprCopy() map[string]string {
	return lo.Assign(billingSetting.BillingExpr)
}

func GetPricingSyncData(base map[string]any) map[string]any {
	extra := make(map[string]any, 2)
	if modes := GetBillingModeCopy(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := GetBillingExprCopy(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}
