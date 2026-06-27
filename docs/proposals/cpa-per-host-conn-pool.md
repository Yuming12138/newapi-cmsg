# Proposal: Per-host connection pool for cpa `utlsRoundTripper`

| Field | Value |
|-------|-------|
| Status | **Implemented — Phase 5 canary ready** |
| Author | Codex |
| Date | 2026-06-27 |
| Related | PR #1 `feat(cpa): evict poisoned upstream conn on stream-level errors` |
| Implementation timing | Implemented for cmsg with default `poolSize = 1`; production should explicitly canary `utls-pool-size: 2` before raising further |

---

## 1. Background

`cliproxyapi/internal/runtime/executor/helps/utls_client.go` defines `utlsRoundTripper`, which proxies all requests to TLS-protected hosts (`chatgpt.com`, `api.anthropic.com`) through utls + HTTP/2. It currently caches **exactly one** `*http2.ClientConn` per host:

```go
type utlsRoundTripper struct {
    mu          sync.Mutex
    connections map[string]*http2.ClientConn   // ← host → 1 conn
    pending     map[string]*sync.Cond
    dialer      proxy.Dialer
}
```

PR #1 added eviction on stream-level errors (both `RoundTrip`-time and mid-body). That fixes the *re-poisoning* problem: a poisoned conn no longer lives forever. But it leaves three structural limits intact:

1. **In-flight requests on a poisoned conn die together** — eviction can save *future* requests but cannot rescue the streams currently riding the poisoned TCP+TLS session.
2. **Single-conn HTTP/2 throughput ceiling** — if ChatGPT enforces a low `SETTINGS_MAX_CONCURRENT_STREAMS` (the spec default is 100, but operators can lower it), `cpa` cannot exceed that ceiling per host even when traffic warrants it.
3. **Poisoning is binary** — one bad conn means 100% bad until the next request triggers eviction-then-dial, with a TLS handshake (~100–300 ms) on the critical path of the user-visible request.

This proposal grows the cached entity from "1 conn" to "a small selectable pool of up to N conns" (initial default `1`, see §4.4 for the staged default-bump). Displaced saturated conns can continue draining outside the selectable pool. All three limits relax: in-flight requests survive sibling-conn failure, throughput multiplies, and a poisoned conn only takes its own streams down.

## 2. Goals

- Maintain **up to N selectable** HTTP/2 connections per host (default **N = 1**, opt-in via explicit config). Detached draining conns are tracked separately and can temporarily raise the real live socket count.
- **Pool grows on demand**: the first request to a host dials `conn[0]`; subsequent requests reuse existing conns until all are saturated, then dial up to the cap.
- **Per-conn eviction**: on stream error, remove only the affected conn from the pool. Sibling conns continue serving traffic. **Evicted conn is explicitly `Close()`d** to release the underlying TLS+TCP socket.
- **Round-robin** selection across the pool's healthy conns to distribute stream load.
- **Request semantics preserved at `poolSize = 1`**: the user-visible request flow (which conn serves a given request, how long it takes, what errors propagate) is identical to today's `getOrCreateConnection`. **Connection lifecycle is strictly more active** than today, however: the displaced conn now receives `Shutdown(ctx)` + `Close()` backstop instead of being left to drift. This is intentional — today's behavior happens to be safe at `poolSize = 1` because the next request quickly replaces the orphan, but in the pool model an orphan would linger longer; explicit lifecycle management is required, not optional.

## 3. Non-goals

- Active health probing of idle connections via PINGs. (Possible follow-up; not needed if eviction works.)
- Per-conn or per-host rate limiting. (Cross-cutting; out of scope.)
- Connection age-out (forced rotation by time). Relies on existing eviction.
- Cross-host total-conn cap. (OS FD limits are the natural ceiling.)

## 4. Design

### 4.1 Data structures

```go
// utlsRoundTripper now holds a pool per host instead of a single conn.
type utlsRoundTripper struct {
    mu       sync.Mutex
    pools    map[string]*connPool      // host → pool
    pending  map[string]*sync.Cond     // unchanged
    dialer   proxy.Dialer
    poolSize int                       // max selectable conns per host; default 1 (Phase 1-5), raised to 4 after Phase 6 by follow-up PR (see §4.4)
}

// connPool holds the active connections for one host.
//
// IMPORTANT: poolSize bounds len(conns) — i.e. the number of *selectable*
// conns that can accept new requests. Conns that have been displaced by
// saturated replacement (§4.2 step 5) are NOT in conns; they live out
// their lives in a separate detached set tracked by `draining`. So:
//
//   real live sockets per host = len(conns) + draining.Load()
//
// draining has no hard cap because forcing a cap would either block the
// caller (against §4.2 guarantees), close healthy long streams (against
// AGENTS.md line 58), or both. Instead it is observable via the
// `draining_conns` gauge (see §4.4) so operators can detect a runaway.
type connPool struct {
    conns    []*http2.ClientConn  // mutated under utlsRoundTripper.mu; len <= poolSize
    idx      atomic.Uint32         // round-robin cursor; lock-free
    draining atomic.Int64          // metric source: detached conns currently inside drainConn
    drainWG  sync.WaitGroup        // optional shutdown/test waiter; Add before launching goroutine
}
```

### 4.2 Operations

#### `getOrCreateConnection(host, addr) (*http2.ClientConn, error)`

1. Acquire `t.mu`.
2. Look up `pool := t.pools[host]`; create empty pool if missing.
3. Walk the pool round-robin (`idx.Add(1) % len(conns)`); for each candidate call **`ReserveNewRequest()`** (atomic reserve-or-fail); on the first one that returns `true`, release the mutex and return that conn. The caller MUST `RoundTrip` immediately to consume the reservation. *(Not `CanTakeNewRequest()`: that is a read-only check that races under concurrent demand — two goroutines can both see "true" and both attempt to dispatch, with one losing to `errClientConnUnusable`. The pool then fails to grow even though it should. See [`http2.ClientConn.ReserveNewRequest`](https://cs.opensource.google/go/x/net/+/master:http2/transport.go) and the burst test in §8.)*
4. If **no** conn in the pool reserved successfully and the pool is **below `poolSize`**, dial a new conn (release the lock for the dial, reacquire to insert) and reserve on it before returning.
5. If the pool is **at capacity** (`len(pool.conns) == poolSize`) and no conn reserved (every selectable conn is saturated): **dial a replacement conn, reserve on it, and swap it in for the round-robin-selected slot. The displaced conn is detached from `pool.conns` and then handed to `t.startDrain(pool, displaced)` for graceful drain.** `startDrain` increments accounting before launching the goroutine, avoiding the classic `WaitGroup.Add`-after-`Wait` race in tests/shutdown code. `drainConn` calls `Shutdown(context.Background())` (unbounded — see footnote) and then `Close()` as a no-op backstop. **Never blocks the caller; never returns "pool full"; never interrupts a healthy in-flight stream, however long it runs.**

```go
func (t *utlsRoundTripper) startDrain(pool *connPool, conn *http2.ClientConn) {
    pool.draining.Add(1)
    pool.drainWG.Add(1)
    go t.drainConn(pool, conn)
}

func (t *utlsRoundTripper) drainConn(pool *connPool, conn *http2.ClientConn) {
    defer pool.draining.Add(-1)
    defer pool.drainWG.Done()
    // Per cliproxyapi/AGENTS.md (line 58): "do not introduce upstream
    // timeouts after a conn is established." A healthy Codex stream may
    // legitimately run for tens of minutes, and the only legitimate
    // sources of upstream deadlines are (a) the upstream server itself,
    // (b) the request originator's per-request context. cpa is a transit
    // proxy and must not invent its own ceiling.
    //
    // Shutdown sends GOAWAY, then blocks until every in-flight stream
    // completes on its own. With an unbounded ctx, Shutdown returns nil
    // only after all streams are finished AND the underlying conn is
    // already closed — Close() is then a no-op idempotent backstop.
    _ = conn.Shutdown(context.Background())
    _ = conn.Close()
}
```

> **What `poolSize` actually bounds.** `poolSize` limits `len(pool.conns)` — the count of *selectable* conns that can serve new requests. Displaced-and-draining conns continue to exist (with their healthy in-flight streams) outside the pool, and `len(conns) + draining count` is the real socket count for this host. There is no hard cap on `draining` for the same reason there is no `drainTimeout`: any cap would either block the caller or close a healthy long stream. Instead this is **operator-observable**: see §4.4 metric and §6 risk row. Under steady state and short streams, `draining` settles back to 0 quickly because `Shutdown(context.Background())` returns as soon as every in-flight stream finishes.

> **The two lifecycle paths.** `CanTakeNewRequest() == false` only means "no room for new streams"; it says nothing about whether existing streams are healthy. Calling `Close()` directly on a saturated-but-healthy conn would forcibly RST any long-output streams (e.g. a 30-minute Codex Pro task) that are still mid-body — exactly the *opposite* of this proposal's goal and explicitly forbidden by `cliproxyapi/AGENTS.md` line 58. So the rule is:
>
> - **Poisoned eviction** (stream RST observed, §4.2 next subsection): explicit `Close()` because the conn is already broken; in-flight streams are doomed anyway, and pre-empting them turns a stuck-then-timeout into a fast retry the upper layer can handle. This is consistent with AGENTS.md because the *upstream* (ChatGPT) already broke the conn — we are not introducing a new timeout, we are reacting to one.
> - **Saturated replacement** (this step): `drainConn` does `Shutdown(context.Background())` + a no-op `Close()` backstop. Unbounded ctx so that any healthy long stream — including streams legitimately exceeding human-tolerable durations — runs to its natural end before the socket is released.
>
> **Why no `drainTimeout` even as a safety net.** Earlier drafts proposed a 5-minute `drainTimeout` as protection against hung streams. Codex review correctly flagged that (a) this would cut healthy Codex Pro long tasks that exceed 5 min, and (b) it violates `cliproxyapi/AGENTS.md` line 58. Hung-stream detection is a separate concern that belongs in active conn-health monitoring (out of scope here); inventing an arbitrary drain ceiling just to feel safer would silently break valid use cases. If a hung stream ever does happen, the goroutine running `Shutdown` is a single blocked goroutine — small price, easy to diagnose with `runtime.Stack()`, and fixable by the proper health-monitoring follow-up rather than by an opaque timeout here.

> **Why no blocking variant in v1.** An earlier draft proposed `poolBlockWhenSaturated`, reusing `pending[host]` cond. Codex review correctly flagged that the existing cond signals only *dial completion*, not *stream-end* — so a blocking waiter would have no one to wake it on stream finish. Implementing stream-end signalling requires hooking HTTP/2 GOAWAY/RST events, which is out of scope here. Dial-replacement gives us bounded latency (one dial) without that complexity. A queue/block variant is a possible follow-up PR if cmsg ever finds replacement-dials too costly.

#### Mid-body / RoundTrip eviction

Reuses the helpers PR #1 added (`evictIfCurrent`, `evictOnReadErrBody`, the retry loop). The only change: instead of `delete(t.connections, host)`, evict the specific `*http2.ClientConn` from the pool **and close it**:

```go
func (t *utlsRoundTripper) evictConn(host string, conn *http2.ClientConn) {
    var removed bool
    t.mu.Lock()
    pool := t.pools[host]
    if pool != nil {
        for i, c := range pool.conns {
            if c == conn {
                pool.conns = append(pool.conns[:i], pool.conns[i+1:]...)
                removed = true
                break
            }
        }
    }
    t.mu.Unlock()

    // Close outside the lock. The http2 library will tear down any
    // in-flight streams on this conn and release the underlying TLS+TCP
    // socket. We are evicting precisely because we believe the conn is
    // poisoned by an upstream RST pattern; preemptively failing the
    // in-flight streams (which the user-level retry will recover) is
    // strictly better than letting them block on a known-bad conn until
    // server timeout. Done in a goroutine to keep eviction non-blocking.
    if removed {
        go func() { _ = conn.Close() }()
    }
}
```

Sibling conns remain healthy. The next request that needs more capacity dials a fresh conn to restore the pool.

> **Why `Close()` matters more in a pool.** PR #1 only `delete`d the map entry and let the orphaned conn drift to GC. At `poolSize = 1` the next request immediately dialled and overwrote the cache, so the orphan was short-lived. With a pool of N, an evicted slot might not be re-dialled for a while (sibling conns still serve traffic), so the orphan would linger and **leak FDs / TLS state** across many evictions. Explicit `Close()` is required, not optional, in the pool model.

### 4.3 Concurrency model

| Resource | Guard |
|----------|-------|
| `pools` map | `t.mu` (existing) |
| `pool.conns` slice | `t.mu` for **all** mutation; reads under `t.mu` for safety |
| `pool.idx` | `atomic.Uint32`, no lock |
| Dial syscall | Release `t.mu` during the network dial; reacquire to insert |

The release-then-reacquire pattern around dial is identical to the existing code (`createConnection` already runs outside the lock).

### 4.4 Configuration

One new optional config field, plumbed through the existing `cpa` config:

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `utls-pool-size` | `int` | `1` (Phase 1-5) → `4` (after Phase 6) | Max selectable conns per host. Detached draining conns are outside this cap and are internally tracked as `pool.draining`. Initially `1` so the refactor lands with request-visible parity to today; once Phase 6 validates `utls-pool-size: 4` in production for ≥ 1 week, a **separate follow-up PR** raises the repo default from `1` to `4`, citing the Phase 6 data as evidence. |

Omitting the config field leaves it at the current repo default. Phase 1-5 defaults to `1` to isolate refactor risk from new-behavior risk; the follow-on PR that raises the default to `4` is in scope **after** Phase 6 W3+ data confirms it is safe.

The blocking-on-saturation variant has been dropped from v1; see §4.2 footnote.

#### Observability

The implementation keeps internal counters on each pool. cpa currently has no Prometheus-style metrics exposition, so external metrics are a follow-up rather than part of this first implementation:

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `pool.conns` | internal | `host` | `len(pool.conns)` — selectable conns, bounded by `poolSize` |
| `pool.draining` | internal | `host` | Number of detached conns currently inside `drainConn`; if later exposed, operators should alert on sustained > 2 × poolSize, which suggests either a chatgpt outage causing constant saturation or genuinely stuck streams that need investigation |

## 5. Implementation phases

To keep risk low, each phase is independently shippable and reviewable.

### Phase 1 — Pure refactor (request-visible parity)

- Convert `connections map[string]*http2.ClientConn` → `pools map[string]*connPool`.
- Always behave as if `poolSize == 1`: only ever have 1 conn per pool, always pick `conns[0]`.
- All existing tests must still pass unchanged.

### Phase 2 — Configurable poolSize plumbing

- Add config schema and parser.
- Default `poolSize = 1` (request-visible parity preserved; lifecycle is the more-active Shutdown/Close path from §4.2).
- Document new fields in `cliproxyapi/config.example.yaml`.

### Phase 3 — Dial-on-demand growth + round-robin

- Implement the `getOrCreateConnection` logic from §4.2.
- New unit tests:
  - Pool grows from 0 to `poolSize` under concurrent demand.
  - Round-robin reaches every member.
  - Saturated-pool dial-replace path: when all conns are saturated and pool is at cap, the round-robin slot is replaced by a fresh dial AND the displaced conn is handed to `startDrain`.
  - `drainConn` runs in a goroutine and never blocks the caller (verified via timing assertion on the call site).
  - `drainConn` always invokes `Close()` after `Shutdown` returns, regardless of what `Shutdown` returns (mock the conn to inject nil and a synthetic error in two separate sub-tests; assert call order `Shutdown` → `Close`).
  - With a stuck mock stream, `drainConn`'s `Shutdown(context.Background())` call blocks indefinitely in its own goroutine, leaving the caller unaffected and the pool able to grow. (Intentional behavior per §4.2 footnote; the test asserts no spurious `Close()` happens while the stream is still nominally "in flight".)
  - **Concurrent-burst test**: fire `2 * poolSize` `RoundTrip` calls in parallel against a freshly-empty pool; assert the pool grows to exactly `poolSize` (not less), `errClientConnUnusable` never propagates to the caller, and every reservation that wins is matched 1:1 with a downstream `RoundTrip` (no orphan reservations). This is the test that breaks if `CanTakeNewRequest` is used instead of `ReserveNewRequest`.
  - `startDrain` increments `pool.draining` and `pool.drainWG` before launching the goroutine; `drainConn` decrements both on return. The gauge must never go negative, and `drainWG.Wait()` must not race with a late `Add`.

### Phase 4 — Per-conn eviction

- Replace `delete(t.connections, host)` calls with `evictConn(host, conn)`.
- New unit tests:
  - Stream error on `conn[i]` removes only `conn[i]` from the pool.
  - Sibling conns remain selectable for new requests.
  - Next request that exceeds remaining capacity dials a fresh conn.

### Phase 5 — Production canary at `poolSize = 1`

- Deploy with `poolSize = 1` for **at least 24 hours**.
- Expected outcome: error rate identical to PR #1's post-deploy rate.
- This validates the refactor preserves request-visible behavior when not opted in (note: connection lifecycle is strictly more active even at `poolSize = 1` — see §2 final goal — so this is *parity*, not a strict no-op).

### Phase 6 — Gradual rollout (cmsg validates; then a follow-up PR raises repo default)

| Week | cmsg deployed config | Repo default | Watch |
|------|---------------------|--------------|-------|
| W1 | (unset → `1`) | `1` | Phase-1 refactor parity; expect same metrics as PR #1 |
| W2 | `utls-pool-size: 2` | `1` | Error rate trending down; FD/socket count steady |
| W3+ | `utls-pool-size: 4` (if W2 clean) | `1` | Sustained low error rate; memory steady |
| W4+ | `utls-pool-size: 4` (unset, inherits default) | **`4`** (raised by follow-up PR) | Confirms unset-default users still see the validated behavior |

**Why the staged default-bump.** Landing this proposal directly at default `4` would conflate two distinct risks: (a) the refactor + new pool logic might have bugs, (b) `N = 4` specifically might trigger resource or upstream concurrency issues. Separating them via a 1-week canary at `utls-pool-size: 4` (W3) lets the Phase 6 follow-up PR cite concrete data when raising the default. The follow-up PR is a single-constant code change (the `poolSize` default in the struct initializer + config schema) plus the §4.4 documentation update; trivial to review and revert.

## 6. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Race conditions in pool mutation | Medium | Hard-to-reproduce stalls / panics | Heavy unit tests covering concurrent grow + evict; reuse existing `mu` |
| Orphaned conn goroutines | Low | Memory / FD drift | Two explicit lifecycle paths, never reliant on implicit reclamation: **poisoned eviction** → `Close()` (conn already broken, in-flight streams unrecoverable); **saturated replacement** → `startDrain(pool, conn)` accounting, then `drainConn(conn)` with `Shutdown(context.Background())` + idempotent `Close()` backstop. **No `drainTimeout`** — that would violate `cliproxyapi/AGENTS.md` line 58 and could cut healthy long-running Codex streams; see §4.2 footnote for the full rationale. If a stream genuinely hangs, the cost is one blocked goroutine (detectable via `runtime.Stack()`); the proper fix is conn-health monitoring as a follow-up, not an arbitrary local deadline. cpa uses a bare `http2.Transport{}` with no `IdleConnTimeout`, so we drive the lifecycle entirely from our side. |
| FD count exhausts limits | Low-Medium | `accept: too many open files` | Steady-state count is `(len(pool.conns) + draining)` × hosts × processes per process. Selectable side is bounded by `poolSize` (tiny). Draining side has no hard cap (see §4.2 footnote); under sustained chatgpt saturation + long streams it can grow. Mitigation: monitor `cpa_pool_draining` gauge; alert on sustained `> 2 × poolSize`; investigate (likely a chatgpt outage or genuinely stuck streams). Raising the process FD ulimit is a cheap stopgap. |
| Draining queue runaway | Low | Goroutine + FD growth from displaced conns whose streams never finish | Operator-visible via `cpa_pool_draining` gauge. We deliberately do NOT impose a hard cap (would either block the caller or close healthy long streams; both violate §4.2 contracts). Follow-up work — conn-health monitoring with active probing — can quarantine genuinely-hung conns out-of-band without an arbitrary local deadline. |
| ChatGPT poisons all N conns simultaneously | Medium | All streams to host fail | Out of scope; same failure mode as PR #1. PR #3 (if needed) could add per-conn cooldown |
| Request-semantics diff at `poolSize = 1` vs current | High | Silent regression risk | Phase 1 is a pure refactor; Phase 5 validates in production. **Note**: request semantics are identical to today, but connection lifecycle is strictly more active (today lets the orphan drift; v1 calls `Shutdown` + `Close` backstop). This is by design — see §2 final bullet for rationale. Tested at `poolSize = 1` for production parity on the request-visible surface. |

## 7. Rollback plan

- **Immediate**: revert mount in `docker-compose.prod.yml` to the previous binary, `docker compose up -d`. Identical to PR #1's rollback.
- **Config-level**: set `utls-pool-size: 1` in config and restart `cpa`. **This rolls back the *multi-conn* behavior only**; the conn lifecycle is still the more-active `Shutdown + Close` path (§4.2), not today's "orphan drifts on overwrite". So config-level rollback addresses bugs caused by `N > 1` (race, FD, draining queue growth), not bugs caused by the lifecycle change itself.
- **Source-level**: revert the merge commit on `dev/cmsg`, rebuild. Required if the lifecycle change itself is the regression (i.e. `Shutdown` interacting badly with utls, or any issue not gated on `poolSize > 1`).

## 8. Testing strategy

- **Unit** (target ≥ 90 % coverage of new code):
  - Pool grow / shrink scenarios
  - Round-robin distribution under concurrency (use `testing/synctest` or goroutine-based fuzz)
  - Eviction targeting the right conn
  - Saturated-pool dial-replace path (no blocking variant — see §4.2 footnote)
- **Integration** (against a fake HTTP/2 server in the same repo):
  - Server RSTs streams on `conn[i]`; assert sibling conns serve traffic
  - Server returns `GOAWAY`; assert clean reconnect
- **Load**: 50 concurrent requests; observe pool growth metric.
- **Production canary**: Phase 5 above, 24 h at `poolSize = 1`.

## 9. Decision points

### Resolved (in this draft)

1. ✅ **Default `poolSize`** — `1` for Phase 1-5, raised to `4` by a separate follow-up PR after Phase 6 W3+ validation. (Resolves codex P1 finding while still letting unset-config users get the validated behavior eventually.)
2. ✅ **Saturated-pool behavior** — Dial-replace the saturated round-robin slot; displaced conn handed to `startDrain(pool, conn)`, which accounts first and then runs `drainConn` for unbounded `Shutdown` + `Close` backstop. Request-visible parity with today; lifecycle is more active. No blocking variant, no immediate error, no `drainTimeout`. (Resolves codex P1/P2 on cond semantics, Shutdown deadline behaviour, and AGENTS.md upstream-no-timeout rule.)

### Resolved during implementation

3. ✅ **Config location** — the existing config has no `upstream` subtree; cmsg uses top-level `utls-pool-size` next to `proxy-url`.
4. ✅ **Timing** — cmsg chose to proceed because the stability upside is medium-high and the deployed default remains `1` unless canary config opts into a larger pool.

## 10. Out of scope (potential follow-up PRs)

- **Active PING-based health checks** on idle conns (so we evict *before* a user request hits a poisoned conn).
- **Per-conn cooldown / quarantine** after eviction (don't immediately redial the same target if the failure rate is high — back off).
- **Pool stats / metrics endpoint** (`/v0/management/pool-stats`) for observability.

## 11. References

- PR #1: `feat(cpa): evict poisoned upstream conn on stream-level errors`
- 2026-06-27 incident timeline (V-curve error pattern: 12:30 60 % → 12:51 restart → 13:00 0 % → 13:30 10 % creep)
- HTTP/2 spec — RFC 7540 §6.4 (RST_STREAM), §6.5.2 (`SETTINGS_MAX_CONCURRENT_STREAMS`)
- Go `golang.org/x/net/http2.Transport` semantics
