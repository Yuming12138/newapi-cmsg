# Campus CLIProxyAPIHome rollout

This directory contains the additive Compose layer and helpers for the isolated
campus test node. It is deliberately not a production-auth migration recipe.

## Safety boundary

- Keep `api.cmsg666.xyz`, the production New API database, the public CPA, and
  Cloudflare Load Balancer unchanged.
- Do not copy production auth files while the public CPA can refresh them.
- Use one JWT and one persistent certificate directory per CPA node.
- `cmsg-campus-home` is Docker-local discovery. Replace it with a routable
  WireGuard or equivalent overlay address before enrolling an off-host CPA.
- A second active Home requires a shared HA PostgreSQL database. SQLite is for
  this single active Home only.

## Runtime layout

Copy `compose.home.yaml` beside the campus `compose.yaml`, and copy
`cluster.sqlite.yaml` to `home/cluster.yaml`. The runtime layout is:

```text
/home/gmchen/cmsg-campus/
  bin/CLIProxyAPIHome-v1.0.64
  compose.yaml
  compose.home.yaml
  data/cpa-home-state/
  home/cluster.yaml
  home/entrypoint.sh
  home/data/
  home/logs/
  home/plugins/
  home/state/
  secrets/home-management.env
  secrets/cpa-home.env
```

`home-management.env` contains `HOME_MANAGEMENT_KEY`; `cpa-home.env` contains
`HOME_JWT`. Both files must be mode `0600`. The JWT is mounted as the CPA
working-directory `.env`, so it is not stored in the Compose service
environment or passed on the process command line.

The Home service reuses the already-cached `nginx:1.29-alpine` filesystem only
as a carrier for the verified static Home binary. Its Nginx entrypoint is
replaced and no Nginx process runs in the Home container.

## Import and activation

Generate the management key directly into its protected file without printing
it. Prepare an import-only copy of the existing campus CPA config with
`prepare_home_import.py`. The helper keeps the verified Mihomo proxy route and
sets a new Home management key; Home hashes that plaintext during import.

Use an empty, dedicated import auth directory during the campus-only rollout:

```bash
docker compose -f compose.yaml -f compose.home.yaml run --rm --no-deps \
  -v "$PWD/home/import/config.yaml:/CLIProxyAPIHome/import/config.yaml:ro" \
  -v "$PWD/home/import/auths:/CLIProxyAPIHome/import/auths:ro" \
  home \
  -config /CLIProxyAPIHome/import/config.yaml \
  -auth-dir /CLIProxyAPIHome/import/auths \
  -import

docker compose -f compose.yaml -f compose.home.yaml up -d home
python3 verify_home.py --management-env secrets/home-management.env --expect-cpa 0
python3 issue_home_jwt.py \
  --management-env secrets/home-management.env \
  --output secrets/cpa-home.env
docker compose -f compose.yaml -f compose.home.yaml up -d --no-deps cpa
python3 verify_home.py --management-env secrets/home-management.env --expect-cpa 1
```

After a successful import, remove the derived `home/import/config.yaml`; it can
be regenerated from the protected campus CPA config and management-key file.
Do not remove the Home database or either protected env file.

Use `verify_cpa_home.py` with the CPA container's backend-network URL to check
`/healthz` and `/v1/models`. It reads a client key from Home in memory and never
prints the key.

With the intentionally empty campus auth import, CPA `/healthz` should pass but
`/v1/models` is expected to remain unconfigured until a dedicated test OAuth
credential is added to Home. Do not treat that state as a successful upstream
model or streaming test.

Use `verify_home_config.py` to confirm that the imported campus config still
contains the Mihomo proxy route, proxy-route recovery, and streaming recovery
settings without printing API keys or management secrets.

Run `docker compose -f compose.yaml -f compose.home.yaml config -q` before every
activation. Do not run `docker compose config` without `-q` in a shared log,
because the base campus Compose currently contains interpolated secrets.

## Rollback

The base CPA config and empty campus auth directory remain mounted but are
ignored in Home mode. To roll back without deleting Home state:

```bash
docker compose -f compose.yaml up -d --no-deps --force-recreate cpa
docker stop cmsg-campus-home
```

Verify CPA `/healthz`, New API health, and the campus Tunnel after rollback.
Keep `home/data`, `data/cpa-home-state`, and both protected env files until the
rollback has been accepted. Never delete production auth material as part of
this rollback.

## Later production-auth migration

Schedule an explicit maintenance window. Fence or stop the old public CPA
before importing a consistent auth snapshot into Home, enroll public and campus
CPA nodes with different JWTs, verify only Home owns refresh, and only then
bring the shadow CPA online. Never allow the old file-backed CPA and Home to
refresh the same OAuth credentials concurrently.
