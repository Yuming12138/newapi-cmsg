# Gemini CPA channel quota details

Channel 35 is OpenAI-compatible at the CPA node, but its credentials are
Antigravity subscriptions owned by CLIProxyAPIHome. The node's local
`/v0/management/auth-files` inventory is empty in Home mode. Configure quota
collection to use the Home management endpoint instead of the relay node.

## Configuration

- `GEMINI_CPA_MANAGEMENT_BASE_URL`: Home/CPA management base URL. The verified
  public runtime uses `http://172.17.0.1:8327` from the New API container.
- `GEMINI_CPA_MANAGEMENT_KEY_FILE`: an existing management key delivered as a
  protected file; the deployed container uses
  `/run/secrets/gemini_cpa_management_key`.
- No OAuth files are downloaded, copied, or returned to the frontend.

The default base URL is the relay node for standalone CPA deployments; a Home
deployment must explicitly override it. The default key file is
`/data/ops/gemini-cpa-management-key`. Never place key values in source, output,
command arguments, or ordinary frontend/app configuration.

## Collection path

1. Read `GET /v0/management/auth-files` from the configured management endpoint.
2. Select enabled Antigravity credentials, deduplicating account emails by a
   non-reversible hash. If email is unavailable, hash the auth index.
3. Use `POST /v0/management/api-call` with `auth_index` and the placeholder
   `Bearer $TOKEN$`. Home supplies the token and the credential's proxy route.
4. Call `loadCodeAssist` for tier and project, then `fetchAvailableModels` with
   that project. Both endpoints use `cloudcode-pa.googleapis.com/v1internal:`.
5. Store only filtered, normalized quotas and hashed account identities under
   `other_info.gemini_cpa_quota`.

Manual balance refresh and the existing master-node five-minute sync task both
use this path. Concurrent refreshes for one channel are deduplicated. Only
management/quota requests are sent; no model generation request is made.

## Semantics and pitfalls

- Quotas are percentages, not USD. `Channel.Balance` and billing/user quotas
  are not changed, and this feature does not enable/disable a channel.
- The headline is the mean of successfully observed accounts' Gemini quota.
  Each account's headline uses its lowest known configured Gemini-model quota.
  It is not an estimate of dollar value or the sum of all percentages.
- Claude/GPT-OSS pools are independent and retain their own quotas/reset times.
  Exhausted Claude quota must not zero the Gemini headline.
- Google protobuf JSON omits zero scalar values. A present `quotaInfo` without
  `remainingFraction` is exhausted (0%); an absent model or quotaInfo is
  unknown. Do not show unknown as zero, full, or cached dollar balance.
- Model mappings are resolved before filtering; unsupported/non-returned
  models are displayed as "not provided".
- Disabled credentials are omitted. Failed active accounts remain identifiable
  as probe failures, with unknown current quota. A partial snapshot clearly
  labels percentages as observed values, not an authoritative full-pool total.
- Account-specific reset times must not be merged into one pooled reset time.
- The frontend uses the compact Kimi-style subscription layout, but collapses
  repeated model entries into Gemini, Claude, and GPT-OSS quota-pool rows. It
  does not list individual model names. Each account contributes the lowest
  known quota per family, and the pool headline averages those per-account
  values rather than double-counting model aliases. Different reset times are
  labeled as per-account resets instead of inventing a common reset timestamp.
- `fetchAvailableModels` does not supply named 5h/monthly windows or monthly
  quota. The UI must not infer period length from a next-reset timestamp or
  relabel Claude/GPT-OSS quota as monthly Gemini quota.
- Failure snapshots expose only controlled error categories/status codes.
  Tokens, emails, projects, upstream bodies, and response headers are not stored.

## Verification

Run the Gemini CPA Go and frontend helper tests, frontend typecheck/lint/build,
then verify the deployed manual refresh API and its persisted snapshot. Verify
`provider_currency=percent`, unchanged channel dollar balance/status/models,
active account counts, per-account quotas, and local/public release version.
Browser verification can be omitted when explicitly requested by the operator.
