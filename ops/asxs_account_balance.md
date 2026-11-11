# ASXS account subscription balance

The admin dashboard's ASXS card follows the channel budget guard's configured
balance source. With `asxs_usage`, it reuses the ASXS portion of the daily shared
pool snapshot, maintained through the configured `/api/usage` endpoint. The
card is labelled "Today's shared quota" and shows the same refresh time as the
pool. Channels 1 and 27 share that allowance and contribute it once. Loading
the dashboard adds no upstream requests or account-token dependency in this
mode. A synced zero remains a real zero; an unsynced balance stays unavailable.

With `asxs_account`, the card reads `GET /api/me/billing/state` with an ASXS
account session token. It totals one preferred window for each distinct active
subscription: `daily` from `subscriptionWindows` when present, otherwise
`total`; the subscription's own `limits` are used only when a runtime window is
unavailable. This prevents a subscription exposing both windows from being
counted twice, while still including a total-only subscription such as a $200
account plan. The result is shown in USD. The account cash balance
(`balanceMicros`) is intentionally excluded. ASXS uses 5,000,000 `leftMicros`
units per US dollar; the displayed sum is rounded to cents.

In account mode, multiple channel keys can report the same quota pool, so
their balances must not be added to calculate the account total. The following
token and proxy requirements apply to account mode.

Place the raw account token, without `Bearer` or an environment variable name,
as one line in `/opt/new-api/data/ops/asxs-account-token` on the production
host. The directory should be accessible only to the service operator, and the
file must be a regular file with mode `0600`. It is mounted at
`/data/ops/asxs-account-token` in the New API container. To use another path,
set `ASXS_ACCOUNT_TOKEN_FILE` in the container environment. Never commit or log
the token. The service atomically replaces this file when ASXS supplies an
`X-New-Token` response header.

The backend uses an ASXS managed channel's configured network proxy to reach
the account endpoint. If the token, proxy, or billing state is unavailable,
the ASXS card shows an unavailable balance while the other provider balances
continue loading. The endpoint is available only to administrators.
