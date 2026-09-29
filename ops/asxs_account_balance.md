# ASXS account subscription balance

The admin dashboard's ASXS card reads `GET /api/me/billing/state` with an ASXS
account session token. It totals each distinct active subscription's `daily`
`leftMicros` from `subscriptionWindows`, using the subscription's own `limits`
only when a runtime window is unavailable. The result is shown in USD. The
account cash balance (`balanceMicros`) is intentionally excluded. ASXS uses
5,000,000 `leftMicros` units per US dollar; the displayed sum is rounded to cents.

This display value is separate from channel `/api/usage` balances and the
channel budget guard. Multiple channel keys can report the same quota pool, so
their balances must not be added to calculate the account total.

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
