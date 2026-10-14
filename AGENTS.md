# AGENTS.md: new-api / CMSG repository conventions

## Scope and current environment

CMSG maintains this fork of QuantumNous/new-api with a CLIProxyAPI/CPA subtree.
The integration and production-source branch is `dev/cmsg`; `main` is a
historical baseline.

- Use the Windows checkout at
  `F:\WorkBuddy\2026-10-06-16-58-21\src\newapi-cmsg`. The old WSL checkout is
  no longer maintained.
- Read [DEVELOPMENT.md](DEVELOPMENT.md) for branch, upstream-sync, build, release,
  and rollback procedures. Read nested `AGENTS.md` files for the area being edited.
- `web/default/` is the only maintained frontend. Its local rules live in
  [web/default/AGENTS.md](web/default/AGENTS.md).
- `cliproxyapi/` is a separate Go module tracked by this outer Git repository.
  Make its commits and upstream merges through the outer repository.
- The campus New API site is retired. Campus runbooks are historical references;
  CPA/Home services have their own lifecycle and must be checked separately.

Apply repository conventions within the user's current request and higher-level
instructions. When an older skill, path, or runbook conflicts with the requested
environment, identify the source and use the verified current workflow. Keep
unrelated changes intact and inspect the working tree before editing.

## Code map

| Area | Location |
| --- | --- |
| HTTP handlers and business logic | `router/`, `controller/`, `service/` |
| Data access and caches | `model/`, `common/` |
| Provider routing and conversion | `relay/`, `middleware/`, `dto/`, `types/` |
| Pricing and runtime settings | `setting/`, `pkg/billingexpr/` |
| Frontend | `web/default/` |
| CPA executors and translators | `cliproxyapi/internal/` |
| Runtime helpers and feature runbooks | `ops/`, `docs/` |

Backend conventions use Gin and GORM; frontend scripts use Bun. Toolchain
versions come from the respective `go.mod` and `web/default/package.json`.

## Contracts to preserve

- **New API JSON:** use the wrappers in `common/json.go` (`Marshal`,
  `Unmarshal`, `UnmarshalJsonStr`, `DecodeJson`) for business-code serialization.
  Types such as `json.RawMessage` and `json.Number` may come from `encoding/json`.
  CPA follows its own module's existing JSON conventions.
- **Database portability:** support SQLite, MySQL, and PostgreSQL. Prefer GORM.
  In `model/`, use `lockForUpdate(tx)` for ordinary row locks: it locks on
  MySQL/PostgreSQL and skips unsupported `FOR UPDATE` syntax on SQLite.
  The legacy `gorm:query_option` pattern does not acquire locks in GORM v2.
  Raw SQL and migrations need explicit compatible paths for all supported
  databases; use the shared reserved-column and boolean helpers in
  `model/main.go`.
- **Relay DTOs:** optional scalar parameters that are parsed and forwarded use
  pointers with `omitempty`. Preserve absent versus explicit `0`/`false`.
- **Streaming:** when adding a provider, verify `StreamOptions` support and
  update `streamSupportedChannels` in `relay/common/relay_info.go` as appropriate.
- **Billing:** read [pkg/billingexpr/expr.md](pkg/billingexpr/expr.md) before
  changing expression pricing. Preserve quota accounting, model mapping, and
  group/channel policies outside the requested change.
- **Attribution:** preserve New API and QuantumNous identity, upstream links,
  copyright and license notices, module/package metadata, and the visible
  frontend attribution. Preserve CLIProxyAPI attribution as well.

## Validation

Use checks that exercise the changed behavior; keep regression tests beside the
affected code.

| Change | Checks |
| --- | --- |
| Documentation only | Markdown format, relative links, `git diff --check` |
| New API backend | Targeted Go tests; `go test ./...` before a backend release |
| CPA | Targeted tests inside `cliproxyapi/`; its own `go test ./...` before a CPA release |
| Frontend | Focused tests, TypeScript checks and `bun run build:check`; see its local rules |
| Concurrency, billing or DB behavior | Relevant race, quota and database regressions in addition to ordinary tests |

Report commands run, observed results, and any unverified behavior. A successful
status endpoint verifies service health; changed relay, billing or UI behavior
needs its own check.

## Production boundary

- Public runtime: `cmsg-root:/opt/new-api`, serving `https://api.cmsg666.xyz`.
  CPA runtime: `cmsg-root:/opt/cliproxyapi`. Verify actual Compose files and binary
  mounts before operating on either service.
- Build on Windows, CI, or a dedicated build host. Production receives verified
  static Linux AMD64 artifacts; heavy Go/Bun/Docker builds and dependency
  installation stay off `cmsg-root`.
- State the expected impact before a restart; active streams can disconnect.
  Recreate only the intended service. Domain changes, credential copying,
  database/cache role changes and failover require explicit scope.
- Keep secrets out of commands, output, Git, issues and examples. Do not read or
  print `.env`, OAuth/auth files, API keys, database passwords or proxy secrets.
  Inspect only required non-secret structure and sanitized metadata.
- After release verification, retain three releases total: the active release
  and two previous releases. Check live mounts before removing older artifacts.
