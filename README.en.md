<div align="center">

<img src="./web/default/public/logo.png" alt="new-api logo" width="120">

<h1>new-api-cmsg</h1>

<p><strong>CMSG's production-oriented unified AI gateway</strong></p>

<p>
  <a href="./README.md">简体中文</a>
  ·
  <strong>English</strong>
</p>

<p>
  <a href="./LICENSE">AGPL-3.0 License</a>
  ·
  <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a>
  ·
  <a href="https://github.com/router-for-me/CLIProxyAPI">CLIProxyAPI</a>
</p>

</div>

This repository is CMSG's production fork of <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a> and embeds CLIProxyAPI/CPA under <code>cliproxyapi/</code>.

## Project scope

<code>new-api-cmsg</code> brings multiple AI providers, account pools, and protocols behind one operable gateway. In addition to the user, token, group, metering, and channel management inherited from New API, CMSG maintains quota-aware scheduling, CPA account pools, Codex/Claude/Gemini/DeepSeek/Kimi routing and protocol adapters, channel capability tests, Codex Radar, server-side long-context protection, and incremental Responses session persistence.

The repository is designed to:

- provide a consistent entry point for Codex, Claude Code, OpenAI SDKs, and compatible clients;
- turn shared quotas, subscriptions, and usage-based balances into explainable and recoverable channel policies;
- preserve model mapping, reasoning, tool-call, and streaming semantics across New API and CLIProxyAPI;
- give operators evidence for diagnosing channels, accounts, networks, quotas, and protocol conversion;
- show channel test outputs and public benchmark scores to help operators compare models and channels;
- keep source development, build artifacts, and production runtimes clearly separated.

## Request path

```text
Codex / Claude Code / OpenAI-compatible clients
                              |
                              v
        New API: auth, groups, metering, billing and routing
                              |
                              v
     channel guards, quota refresh, aliases and fallback policies
                              |
                 +------------+-------------+
                 |                          |
                 v                          v
      CLIProxyAPI / CPA pools       Direct provider channels
                 |                          |
                 v                          v
    OpenAI / Claude / Gemini / Kimi    DeepSeek / Kimi / others
```

The current production entry point is <a href="https://api.cmsg666.xyz/">api.cmsg666.xyz</a>, served by the public runtime on <code>cmsg-root</code>.

## CMSG-maintained capabilities

| Area                           | Capabilities                                                                                                                                                                                         |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unified protocol entry         | OpenAI Responses, Chat Completions, Claude Messages, Gemini, and common OpenAI-compatible requests                                                                                                   |
| Groups and metering            | Differentiated groups, usage metering, daily subscription quotas, shared balances, and usage-based upstreams                                                                                         |
| Quota-aware routing            | Balance refresh, low-cost preference, fallback channels, automatic disable/recovery, temporary restriction lifts with a selectable expiry, and billing against the effective upstream model          |
| CPA account pools and quotas   | Codex/Claude/Kimi/Antigravity account pools, model-level quotas, subscription windows, reset times, active-account deduplication, cooldown and retries                                               |
| Codex protocol compatibility   | Native DeepSeek Responses plaintext reasoning replay, cross-provider history compatibility, Kimi tool adapters, custom and namespaced tools, and assistant-preamble/tool-result ordering             |
| Responses session storage      | Per-turn deltas, periodic and branch checkpoints, batched recovery, byte-bounded LRU, single-flight, limited concurrent rebuilds, and SHA-256 externalization for large attachments                  |
| Reliability                    | Pre-first-byte retries, CLI affinity retention on transient failures, per-host HTTP/2 pools, metric-flush circuit breaking, Mihomo node failover, and streaming disconnect cleanup                   |
| Gemini performance and context | Tool schema caching, linear request assembly, optimized streaming event conversion, final request budget checks, oversized tool-output trimming, and bounded retries for explicit token-limit errors |
| Channel capability tests       | Current canary covers channels 1, 12, and 27; model and reasoning-effort selection, prompts, answers, latency, failure reasons, and animated SVG previews                                            |
| Codex Radar                    | Public scores for four Sol/Astra model families, task coverage and cost/latency statistics, failed-point retries, complete Redis snapshots, and automatic frontend recovery                          |
| Observability                  | Performance metrics grouped by effective upstream model, channel usage, custom time windows, request paths, CPA scheduling, reasoning effort, stream state, and network probes                       |
| User self-service              | Codex desktop/terminal setup, upstream balance overview, and temporary quota requests for low-balance users                                                                                          |

Use the running instance's model list and channel configuration to find available models and groups.

## Capability updates since 2026-10-14

| Topic | Commits | Update |
| --- | --- | --- |
| DeepSeek thinking continuation | `59a8988` `d1b6e17` | Backfill placeholder reasoning for assistant groups that omit it in thinking + tools requests, covering tool turns and the final assistant group and preventing `reasoning_text must be passed back` failures. |
| Reasoning deduplication | `7626159` `df5e9a9` | Remove adjacent duplicate reasoning from both server-side session replay and Codex Desktop inline histories, preventing long-context growth from doubling. |
| Cross-channel continuation | `26ed4fa` `3d9c84c` | Replay a local transcript when a DeepSeek thread moves to a GPT channel, removing the original provider reasoning and `previous_response_id`; GPT-5/GPT-6 request sanitization and diagnostic logging were completed in the same batch. |
| Affinity stability | `c3c6da9` | Preserve channel affinity for Codex/Claude CLI sessions on pre-first-byte timeout or 5xx failures; deterministic quota, rate-limit, and authentication failures still clear affinity for failover. |
| Performance and usage panels | `06affc8` `4fe400b` `5d48a54` `f99ca9b` | Group model performance, channel usage, channel tests, and asynchronous tasks by the effective mapped upstream model; open a circuit after repeated metric-flush failures so observability cannot starve relay traffic. |
| Session diagnostics | `c944397` | Record a sanitized item-type histogram for native Responses inputs to diagnose long threads that end early or contain abnormal reasoning/tool items. |
| Runtime resource limits | `89d3086` | Use compressed canonical Responses snapshots with seven-day cleanup; emit parameterized SQL shapes from GORM; cap Docker logs at `20 MiB × 5`. |
| Incremental session storage | `37bf925` `ad41544` `fdcdbb8` | Add rewrite-aware deltas, periodic and branch checkpoints, batched recovery, LRU, single-flight, bounded concurrent rebuilds, and SHA-256 attachment externalization; follow-up commits fix full checkpoints caused by rewritten old reasoning and remove redundant encoding. |

## Channel tests and benchmark references

### Tests against this gateway's channels

Administrators can select a model and reasoning effort on the channel page and request a pelican-riding-a-bicycle SVG. Requests stay on the selected channel and use its actual configuration and model mapping. The panel shows the prompt, answer, requested model, upstream-reported model, latency, and failure reason. SVG output is sanitized before animated preview, and results remain open until the user closes the panel.

The current test endpoint supports channels 1, 12, and 27, with a complete SVG as its pass condition. Administrators can inspect the image and raw answer to compare channel outputs. Tests make actual model requests and use upstream quota.

### Codex Radar public scores

The dashboard synchronizes RadarBench v2 summaries from <a href="https://codexradar.com/">Codex Radar</a> for <code>gpt-5.6-sol</code>, <code>gpt-6-sol</code>, <code>gpt-6.1-sol</code>, and <code>gpt-6-astra</code>. Each model has six efforts: <code>low</code>, <code>medium</code>, <code>high</code>, <code>xhigh</code>, <code>max</code>, and <code>ultra</code>, for 24 points in total.

The panel shows public scores, task coverage, API-equivalent costs, and latency. Missing statistics remain unknown. When collection fails, a complete snapshot up to 24 hours old may be used with its original acquisition time and a cached-data marker. Redis-enabled instances can recover snapshots across restarts. See <a href="./docs/codex-radar.md">Radar data and failure protection</a>.

Administrators can use channel test outputs and Radar scores to evaluate channel performance and adjust routing configuration.

## Quota panels and protocol safeguards

Channel and dashboard panels show USD balances, CNY balances, and subscription quota percentages in their original units, with missing fields marked unknown. Codex, Kimi, and Gemini CPA use their own collection paths and display upstream-provided subscription windows and reset times. Gemini summarizes independent quota pools as percentages and retains per-account reset times. See <a href="./docs/gemini-cpa-quota.md">Gemini CPA quota semantics</a>.

The native DeepSeek Responses adapter preserves returned plaintext reasoning and isolates continuity caches by user, token, channel, and thread. Cross-provider GPT/Kimi histories use a separate compatibility strategy. Custom, dynamic, and namespaced tools retain call identifiers and client-facing names across requests and response streams. See <a href="./ops/deepseek_codex_responses.md">DeepSeek and Codex tool continuation</a>.

### Incremental Responses session storage

Long-running sessions store an independent delta per response ID, with full checkpoints at the first turn, every 20 turns, and branch points. A delta records the parent history length, indexes of items rewritten by the upstream, and newly appended items. This preserves recovery when DeepSeek normalizes earlier reasoning content on a later turn. Recovery reads at most 24 deltas in one bounded pass and validates item counts, full-history hashes, and pending tool-call hashes.

The runtime adds a byte-bounded in-process LRU, same-ID single-flight, a default two-slot rebuild limit, and a 128 MiB session ceiling. Text, images, and tool outputs larger than 256 KiB are externalized by SHA-256 into the persistent blob directory. Cleanup uses the conversation head watermark to remove whole expired threads without deleting active sessions.

Storage modes are `legacy`, `shadow`, and `incremental`. `shadow` writes both the legacy snapshot and incremental tables, then rebuilds for comparison; production uses `incremental`. Configuration, staged rollout, and rollback details are in <a href="./ops/deepseek_codex_responses.md#2026-10-21-responses-会话增量存储">Responses incremental-storage operations</a>.

The CPA Antigravity/Gemini path estimates context budgets on the server. Over-budget requests may have history or oversized tool output trimmed with explicit truncation notices; trimming removes part of the original text. Requests that remain over budget receive a clear error. When the upstream explicitly reports an input-token limit, the executor retries at most once with a smaller request. See <a href="./docs/antigravity-context-budget.md">Long-context protection</a>.

## Primary APIs

| Purpose          | Endpoint                                                                 |
| ---------------- | ------------------------------------------------------------------------ |
| Responses        | <code>POST /v1/responses</code>, <code>POST /v1/responses/compact</code> |
| Chat Completions | <code>POST /v1/chat/completions</code>                                   |
| Claude Messages  | <code>POST /v1/messages</code>                                           |
| Model discovery  | <code>GET /v1/models</code>                                              |

## Repository layout

| Path                                                                 | Purpose                                                                                                 |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| <code>controller/</code>, <code>service/</code>, <code>model/</code> | New API request, business, and data layers                                                              |
| <code>relay/</code>, <code>middleware/</code>, <code>setting/</code> | Protocol conversion, distribution, billing, and runtime settings                                        |
| <code>web/default/</code>                                            | The only frontend maintained by CMSG                                                                    |
| <code>cliproxyapi/</code>                                            | CLIProxyAPI/CPA source tracked as a subtree                                                             |
| <code>ops/</code>                                                    | Balance refresh, channel guards, quota controls, and observation scripts                                |
| <code>docs/</code>                                                   | Feature documentation and runbooks for Radar, quotas, context protection, and other maintained features |
| <code>docs/proposals/</code>                                         | Architecture and rollout proposals                                                                      |
| <code>AGENTS.md</code>                                               | Coding, compatibility, branch, and production-safety rules                                              |
| <code>DEVELOPMENT.md</code>                                          | Development, upstream sync, build, and deployment conventions                                           |

## Branches and development

| Branch                         | Role                                                                         |
| ------------------------------ | ---------------------------------------------------------------------------- |
| <code>dev/cmsg</code>          | Default branch, daily integration, and production source                     |
| <code>main</code>              | Historical server baseline for comparison only                               |
| `feature/*`, `fix/*`, `docs/*` | Short-lived work branches merged into <code>dev/cmsg</code> after validation |

```bash
git clone git@github.com:Yuming12138/newapi-cmsg.git
cd newapi-cmsg
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c feature/your-change
```

Read <a href="./AGENTS.md">AGENTS.md</a> and <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a> completely before making changes. Common validation commands:

```bash
go test ./...
(cd web/default && bun run build:check)
(cd cliproxyapi && go test ./...)
(cd cliproxyapi && go build -o test-output ./cmd/server)
# Concurrency-sensitive changes; requires CGO and gcc/clang
CGO_ENABLED=1 go test -race ./model ./relay/channel/deepseek ./relay/channel/mimo
```

On Windows, race tests require MinGW or a compatible LLVM toolchain with `CC` pointing to the compiler. Validation should match the risk of the change. Documentation-only updates do not require a full build.

## Build and deployment boundaries

- Current maintenance uses a local Windows checkout. CI or a dedicated build host can also produce release artifacts.
- Windows can cross-compile production binaries with <code>CGO_ENABLED=0 GOOS=linux GOARCH=amd64</code>. Verify the embedded version, target architecture, static linkage, and SHA-256 before deployment.
- Production binaries use <code>CGO_ENABLED=0</code>. Race detection is a local validation step using a separate CGO build and does not affect release artifacts.
- Public nodes run the service and receive verified immutable artifacts. Source development and heavy builds run locally or on a build host.
- <code>dev/cmsg</code> is the only daily integration and production-build source.
- Verify the target instance's Compose layout, data roles, and channel configuration before release.
- Keep API keys, OAuth tokens, database passwords, and proxy credentials in protected runtime configuration.

See <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a> for the complete workflow.

## Usage and security

- Users must follow the terms of every connected model provider and all applicable laws and regulations.
- Operators are responsible for access control, billing policy, data protection, auditing, and upstream authorization.
- Before offering a generative AI service to others, confirm the applicable registration, regulatory, and content-safety requirements.

## Upstreams, attribution, and license

- New API upstream: <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a>
- New API evolved from: <a href="https://github.com/songquanpeng/one-api">One API</a>
- CLIProxyAPI upstream: <a href="https://github.com/router-for-me/CLIProxyAPI">router-for-me/CLIProxyAPI</a>
- License: <a href="./LICENSE">GNU Affero General Public License v3.0</a>

Modified versions that present a user interface must preserve a visible link to the original project: <https://github.com/QuantumNous/new-api>.

This repository preserves the upstream attribution notice: <code>Frontend design and development by New API contributors.</code>
