package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// C2ACreditPoolSetting converts cli2api upstream credits into USD so the
// Advanced Custom channels can join the daily quota pool.
//
// Conversion pipeline (2026-10-27, gmchen):
//  1. cli2api charges "credits" per model: credits = tokens/1000 * rate(model).
//     The flash tier rate (deepseek-v4.1-flash-sg) is x0.03, so
//     1 credit = 1000/rate = 33333 tokens.
//  2. Those tokens are then priced with this site's own model price for
//     ReferenceModel (default deepseek-flash), using the standard unit
//     (QuotaPerUnit = $0.002 per 1K tokens), i.e. usd_per_token =
//     model_ratio * 0.002 / 1000.
//  3. Completion tokens cost more; CompletionShare blends input/output:
//     blended = usd_per_token * ((1-share) + share*completion_ratio).
//
// Result: usd_per_credit = tokens_per_credit * blended.
type C2ACreditPoolSetting struct {
	Enabled             bool    `json:"enabled"`
	ReferenceModel      string  `json:"reference_model"`
	CreditsPerKiloToken float64 `json:"credits_per_kilo_token"`
	CompletionShare     float64 `json:"completion_share"`
}

var c2aCreditPoolSetting = C2ACreditPoolSetting{
	Enabled:             true,
	ReferenceModel:      "deepseek-flash",
	CreditsPerKiloToken: 0.03,
	// Codex traffic is prompt-heavy; assume 10% of tokens are completion.
	CompletionShare: 0.1,
}

func init() {
	config.GlobalConfig.Register("c2a_credit_pool_setting", &c2aCreditPoolSetting)
}

func GetC2ACreditPoolSetting() *C2ACreditPoolSetting {
	return &c2aCreditPoolSetting
}
