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

<code>new-api-cmsg</code> brings multiple AI providers, account pools, and protocols behind one operable gateway. In addition to the user, token, group, metering, and channel management inherited from New API, CMSG maintains quota-aware scheduling, CPA account pools, Codex/Claude/Gemini routing, image workflows, request observability, and public/campus deployment support.

The repository is designed to:

- provide a consistent entry point for Codex, Claude Code, OpenAI SDKs, Open WebUI, and compatible clients;
- turn shared quotas, subscriptions, and usage-based balances into explainable and recoverable channel policies;
- preserve model mapping, reasoning, tool-call, and streaming semantics across New API and CLIProxyAPI;
- give operators evidence for diagnosing channels, accounts, networks, quotas, and protocol conversion;
- keep source development, build artifacts, and production runtimes clearly separated.

## Request path

~~~text
Codex / Claude Code / OpenAI-compatible clients / Open WebUI
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
      CLIProxyAPI / CPA pools       OpenAI-compatible channels
                 |
                 v
        OpenAI / Claude / Gemini and other configured upstreams
~~~

The public and campus sites are independently deployed instances. The repository provides shared build and runtime conventions, but the health, data role, or automatic failover eligibility of one site must not be inferred from the other.

## CMSG-maintained capabilities

| Area | Capabilities |
| --- | --- |
| Unified protocol entry | OpenAI Responses, Chat Completions, Claude Messages, Gemini, and common OpenAI-compatible requests |
| Groups and metering | Differentiated groups, metered-but-free usage, daily subscription quotas, shared balances, and usage-based upstreams |
| Quota-aware routing | Balance refresh, low-cost preference, fallback channels, automatic disable/recovery, and billing against the effective upstream model |
| CPA account pools | Codex/Claude account pools, model-level quotas, cooldown and retries, reasoning conversion, and tool-call compatibility |
| Reliability | Pre-first-byte retries, per-host HTTP/2 pools, Mihomo node failover, and streaming disconnect cleanup |
| Image workspace | Text-to-image, image generation/editing, one to four references, optional masks, and matching APIs |
| Observability | Channel usage, custom time windows, request paths, CPA scheduling, reasoning effort, stream state, and network probes |
| Accounts and security | Registration passphrases, Turnstile, secure session cookies, hard-delete cleanup, and SSRF defenses |
| User self-service | Codex desktop/terminal setup, Open WebUI access, and temporary quota requests for low-balance users |

Available models, groups, and providers depend on runtime configuration, account state, and channel policy. Repository capabilities are not a promise that every model is continuously available.

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

- Source development, frontend builds, and Go builds run in WSL or on a dedicated build host.
- Public and campus runtime nodes receive verified immutable artifacts; they are not Git development or heavy-build environments.
- <code>dev/cmsg</code> is the only daily integration and production-build source.
- Compose layout, data roles, channel eligibility, and failover state are verified independently for each site.
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
