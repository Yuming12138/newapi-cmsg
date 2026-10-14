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

> [!IMPORTANT]
> This repository is CMSG's production fork of <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a> and embeds CLIProxyAPI/CPA under <code>cliproxyapi/</code>. It is not the general upstream distribution. This README describes the CMSG-specific scope, maintained capabilities, and development boundaries.

## Project scope

<code>new-api-cmsg</code> brings multiple AI providers, account pools, and protocols behind one operable gateway. In addition to the user, token, group, metering, and channel management inherited from New API, CMSG maintains quota-aware scheduling, CPA account pools, Codex/Claude/Gemini/DeepSeek/Kimi routing and protocol adapters, channel capability tests, Codex Radar, image workflows, and server-side long-context protection.

The repository is designed to:

- provide a consistent entry point for Codex, Claude Code, OpenAI SDKs, and compatible clients;
- turn shared quotas, subscriptions, and usage-based balances into explainable and recoverable channel policies;
- preserve model mapping, reasoning, tool-call, and streaming semantics across New API and CLIProxyAPI;
- give operators evidence for diagnosing channels, accounts, networks, quotas, and protocol conversion;
- show channel test outputs and public benchmark scores to help operators compare models and channels;
- keep source development, build artifacts, and production runtimes clearly separated.

## Request path

~~~text
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
~~~

The current production entry point is <a href="https://api.cmsg666.xyz/">api.cmsg666.xyz</a>, served by the public runtime on <code>cmsg-root</code>.

## CMSG-maintained capabilities

| Area | Capabilities |
| --- | --- |
| Unified protocol entry | OpenAI Responses, Chat Completions, Claude Messages, Gemini, and common OpenAI-compatible requests |
| Groups and metering | Differentiated groups, metered-but-free administrator usage, daily subscription quotas, shared balances, and usage-based upstreams |
| Quota-aware routing | Balance refresh, low-cost preference, fallback channels, automatic disable/recovery, temporary restriction lifts with a selectable expiry, and billing against the effective upstream model |
| CPA account pools and quotas | Codex/Claude/Kimi/Antigravity account pools, model-level quotas, subscription windows, reset times, active-account deduplication, cooldown and retries |
| Codex protocol compatibility | Native DeepSeek Responses plaintext reasoning replay, cross-provider history compatibility, Kimi tool adapters, custom and namespaced tools, and assistant-preamble/tool-result ordering |
| Reliability | Pre-first-byte retries, per-host HTTP/2 pools, Mihomo node failover, and streaming disconnect cleanup |
| Gemini performance and context | Tool schema caching, linear request assembly, optimized streaming event conversion, final request budget checks, oversized tool-output trimming, and bounded retries for explicit token-limit errors |
| Channel capability tests | Current canary covers channels 1, 12, and 27; model and reasoning-effort selection, prompts, answers, latency, failure reasons, and animated SVG previews |
| Codex Radar | Public scores for four Sol/Astra model families, task coverage and cost/latency statistics, failed-point retries, complete Redis snapshots, and automatic frontend recovery |
| Image workspace | Text-to-image, image generation/editing, one to four references, optional masks, and matching APIs |
| Observability | Channel usage, custom time windows, request paths, CPA scheduling, reasoning effort, stream state, and network probes |
| Accounts and security | New registrations default to the <code>cmsg</code> identity group; registration passphrases, Turnstile, secure session cookies, administrator purge of soft-deleted users, and SSRF defenses |
| User self-service | Codex desktop/terminal setup, upstream balance overview, and temporary quota requests for low-balance users |

Available models, groups, and providers depend on runtime configuration, account state, and channel policy. Repository capabilities are not a promise that every model is continuously available.

## Channel tests and benchmark references

### Tests against this gateway's channels

Administrators can select a model and reasoning effort on the channel page and request a pelican-riding-a-bicycle SVG from that channel. The test uses the channel's actual configuration and model mapping without falling back to another channel. The panel shows the prompt, answer, requested model, upstream-reported model, latency, and failure reason. SVG output is sanitized before animated preview, and results remain open until the user closes the panel.

The current test endpoint is limited to channels 1, 12, and 27. A pass primarily means a complete SVG was detected, not that the model received a comprehensive capability score. One output cannot establish model identity or degradation. Tests make actual model requests and may consume upstream quota.

### Codex Radar public scores

The dashboard synchronizes RadarBench v2 summaries from <a href="https://codexradar.com/">Codex Radar</a> for <code>gpt-5.6-sol</code>, <code>gpt-6-sol</code>, <code>gpt-6.1-sol</code>, and <code>gpt-6-astra</code>. Each model has six efforts: <code>low</code>, <code>medium</code>, <code>high</code>, <code>xhigh</code>, <code>max</code>, and <code>ultra</code>, for 24 points in total. Luna is not shown in this panel.

These are external public scores, not measurements of this gateway's channels. Cost fields report upstream API-equivalent statistics, not gateway billing prices. Missing statistics remain unknown. When collection fails, a complete snapshot up to 24 hours old may be used with its original acquisition time and a cached-data marker. Redis-enabled instances can recover snapshots across restarts. See <a href="./docs/codex-radar.md">Radar data and failure protection</a>.

Tests and scores support manual comparison and diagnosis. They do not currently adjust channel priorities automatically or guarantee accurate detection of model degradation.

## Quota panels and protocol safeguards

Channel and dashboard panels distinguish USD balances, CNY balances, and subscription quota percentages. Unknown data is not reported as zero or full quota. Codex, Kimi, and Gemini CPA use their own collection paths; subscription windows and reset times are shown only when the upstream supplies them. Gemini uses independent quota-pool summaries, without inferring five-hour or monthly windows from reset timestamps or converting percentages into dollars. See <a href="./docs/gemini-cpa-quota.md">Gemini CPA quota semantics</a>.

The native DeepSeek Responses adapter prioritizes actual returned plaintext reasoning and isolates continuity caches by user, token, channel, and thread. Foreign GPT/Kimi histories without plaintext use a compatibility strategy; summaries and placeholders are not original reasoning, and encrypted content cannot be recovered losslessly. Custom, dynamic, and namespaced tools retain call identifiers and client-facing names across requests and response streams. See <a href="./ops/deepseek_codex_responses.md">DeepSeek and Codex tool continuation</a>.

The CPA Antigravity/Gemini path checks context budgets on the server. Over-budget requests may have history or oversized tool output trimmed with explicit truncation notices. Requests that remain over budget receive a clear error. When the upstream explicitly reports an input-token limit, the executor retries at most once with a genuinely smaller request. This is not a lossless summary, token estimates are not official tokenization, and not every extreme multimodal request can be repaired automatically. See <a href="./docs/antigravity-context-budget.md">Long-context protection</a>.

New registrations default to the <code>cmsg</code> identity group. This does not force an API token's routing group or migrate existing users. See <a href="./docs/registration-default-cmsg.md">Default registration group</a>.

## Primary APIs

| Purpose | Endpoint |
| --- | --- |
| Responses | <code>POST /v1/responses</code>, <code>POST /v1/responses/compact</code> |
| Chat Completions | <code>POST /v1/chat/completions</code> |
| Claude Messages | <code>POST /v1/messages</code> |
| Image generation and editing | <code>POST /v1/images/generations</code>, <code>POST /v1/images/edits</code> |
| Model discovery | <code>GET /v1/models</code> |

In production, use the model list returned for the client's group and the operator's effective channel configuration as the source of truth.

## Repository layout

| Path | Purpose |
| --- | --- |
| <code>controller/</code>, <code>service/</code>, <code>model/</code> | New API request, business, and data layers |
| <code>relay/</code>, <code>middleware/</code>, <code>setting/</code> | Protocol conversion, distribution, billing, and runtime settings |
| <code>web/default/</code> | The only frontend maintained by CMSG |
| <code>cliproxyapi/</code> | CLIProxyAPI/CPA source tracked as a subtree |
| <code>ops/</code> | Balance refresh, channel guards, quota controls, and observation scripts |
| <code>docs/</code> | Feature documentation and runbooks for Radar, quotas, context protection, registration groups, and other maintained features |
| <code>docs/proposals/</code> | Architecture and rollout proposals |
| <code>AGENTS.md</code> | Coding, compatibility, branch, and production-safety rules |
| <code>DEVELOPMENT.md</code> | Development, upstream sync, build, and deployment conventions |

## Branches and development

| Branch | Role |
| --- | --- |
| <code>dev/cmsg</code> | Default branch, daily integration, and production source |
| <code>main</code> | Historical server baseline for comparison only |
| <code>feature/*</code>, <code>fix/*</code>, <code>docs/*</code> | Short-lived work branches merged into <code>dev/cmsg</code> after validation |

~~~bash
git clone git@github.com:Yuming12138/newapi-cmsg.git
cd newapi-cmsg
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c feature/your-change
~~~

Read <a href="./AGENTS.md">AGENTS.md</a> and <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a> completely before making changes. Common validation commands:

~~~bash
go test ./...
(cd web/default && bun run build:check)
(cd cliproxyapi && go test ./...)
(cd cliproxyapi && go build -o test-output ./cmd/server)
~~~

Validation should match the risk of the change. Documentation-only updates do not require a full build.

## Build and deployment boundaries

- Current maintenance uses a local Windows checkout. Source development and frontend builds can also run in WSL, CI, or on a dedicated build host; WSL is not required.
- Windows can cross-compile production binaries with <code>CGO_ENABLED=0 GOOS=linux GOARCH=amd64</code>. Verify the embedded version, target architecture, static linkage, and SHA-256 before deployment.
- Public runtime nodes receive verified immutable artifacts; they are not Git development or heavy-build environments.
- <code>dev/cmsg</code> is the only daily integration and production-build source.
- Verify the target instance's Compose layout, data roles, and channel configuration before release; do not infer current runtime state from historical deployments.
- API keys, OAuth tokens, database passwords, and proxy credentials must never be committed or pasted into logs, issues, or examples.

See <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a> for the complete workflow.

## Usage and security

- Users must follow the terms of every connected model provider and all applicable laws and regulations.
- Operators are responsible for access control, billing policy, data protection, auditing, and upstream authorization.
- The CMSG production customizations do not constitute a guarantee of stability, quota, or technical support.
- Before offering a generative AI service to others, confirm the applicable registration, regulatory, and content-safety requirements.

## Upstreams, attribution, and license

- New API upstream: <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a>
- New API evolved from: <a href="https://github.com/songquanpeng/one-api">One API</a>
- CLIProxyAPI upstream: <a href="https://github.com/router-for-me/CLIProxyAPI">router-for-me/CLIProxyAPI</a>
- License: <a href="./LICENSE">GNU Affero General Public License v3.0</a>

Modified versions that present a user interface must preserve a visible link to the original project: <https://github.com/QuantumNous/new-api>.

This repository preserves the upstream attribution notice: <code>Frontend design and development by New API contributors.</code>
