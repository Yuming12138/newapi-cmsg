<div align="center">

<img src="./web/default/public/logo.png" alt="new-api logo" width="120">

<h1>new-api-cmsg</h1>

<p><strong>CMSG 面向生产环境维护的统一 AI 网关</strong></p>

<p>
  <strong>简体中文</strong>
  ·
  <a href="./README.en.md">English</a>
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
> 本仓库是 CMSG 基于 <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a> 维护的生产定制分支，并在 <code>cliproxyapi/</code> 中集成 CLIProxyAPI/CPA。它不是 upstream 的通用发行版；本文只描述 CMSG 的项目定位、已维护能力和开发边界。

## 项目定位

<code>new-api-cmsg</code> 把多种 AI 上游、账号池和协议统一到一个可运营的网关中。除了 upstream New API 提供的用户、令牌、分组、计量和渠道管理能力，CMSG 还维护了额度感知调度、CPA 账号池、Codex/Claude/Gemini 路由、图片工作台、请求可观测性以及公有/校园双站点部署支持。

本仓库主要解决以下问题：

- 为 Codex、Claude Code、OpenAI SDK、Open WebUI 等客户端提供一致的入口；
- 将共享额度、订阅额度和按量余额转化为可解释、可恢复的渠道策略；
- 在 New API 与 CLIProxyAPI 两层保留模型映射、reasoning、工具调用和流式响应语义；
- 让管理员能够定位渠道、账号、网络、配额和协议转换问题；
- 将源码开发、构建产物和生产运行环境明确分离。

## 请求链路

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

公网站点与校园站点是独立部署实例。仓库提供一致的构建和运行约定，但不把任一站点的健康、数据角色或自动故障切换状态推断到另一站点。

## CMSG 维护能力

| 领域 | 能力 |
| --- | --- |
| 统一协议入口 | OpenAI Responses、Chat Completions、Claude Messages、Gemini 及常见 OpenAI-compatible 请求 |
| 分组与计量 | 差异化分组、计量但不扣费、订阅制每日额度、共享余额及按量上游的混合展示 |
| 额度感知调度 | 余额刷新、低价优先、高价兜底、渠道自动禁用与恢复、模型别名按真实上游模型结算 |
| CPA 账号池 | Codex/Claude 账号池、模型级额度、冷却与重试、reasoning 和工具调用兼容 |
| 稳定性 | 首包前重试、HTTP/2 per-host 连接池、Mihomo 节点故障转移、流式断连清理 |
| 图片工作台 | 文生图、图生图/编辑、1–4 张参考图、可选蒙版及对应 API |
| 可观测性 | 渠道用量、自定义时间窗口、请求链路、CPA 调度、reasoning effort、流式状态和网络探针 |
| 账户与安全 | 注册口令、Turnstile、安全会话 Cookie、用户硬删除清理和 SSRF 防护 |
| 用户自助 | Codex 桌面端/终端配置说明、Open WebUI 入口、低余额临时额度申请 |

具体可用模型、分组和上游由运行时配置、账号状态与渠道策略共同决定；仓库中的能力说明不等同于对所有模型持续可用的承诺。

## 主要 API

| 用途 | 接口 |
| --- | --- |
| Responses | <code>POST /v1/responses</code>、<code>POST /v1/responses/compact</code> |
| Chat Completions | <code>POST /v1/chat/completions</code> |
| Claude Messages | <code>POST /v1/messages</code> |
| 图片生成与编辑 | <code>POST /v1/images/generations</code>、<code>POST /v1/images/edits</code> |
| 模型发现 | <code>GET /v1/models</code> |

生产环境应以客户端所属分组返回的模型列表和管理员渠道配置为准。

## 仓库结构

| 路径 | 说明 |
| --- | --- |
| <code>controller/</code>、<code>service/</code>、<code>model/</code> | New API 请求、业务与数据层 |
| <code>relay/</code>、<code>middleware/</code>、<code>setting/</code> | 协议转换、分发、计费和运行设置 |
| <code>web/default/</code> | CMSG 唯一维护的 React 前端 |
| <code>cliproxyapi/</code> | 作为 subtree 跟踪的 CLIProxyAPI/CPA 源码 |
| <code>ops/</code> | 余额刷新、渠道守卫、额度控制和观测脚本 |
| <code>docs/proposals/</code> | 重要架构与上线方案 |
| <code>AGENTS.md</code> | 代码、兼容性、分支与生产安全规则 |
| <code>DEVELOPMENT.md</code> | 开发、上游同步、构建和部署约定 |

## 分支与开发

| 分支 | 定位 |
| --- | --- |
| <code>dev/cmsg</code> | 默认分支、日常开发与生产集成源 |
| <code>main</code> | 历史服务器基线，仅用于对照 |
| <code>feature/*</code>、<code>fix/*</code>、<code>docs/*</code> | 短期工作分支，验证后合回 <code>dev/cmsg</code> |

~~~bash
git clone git@github.com:Yuming12138/newapi-cmsg.git
cd newapi-cmsg
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c feature/your-change
~~~

开始修改前请完整阅读 <a href="./AGENTS.md">AGENTS.md</a> 和 <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a>。常用验证：

~~~bash
go test ./...
(cd web/default && bun run build:check)
(cd cliproxyapi && go test ./...)
(cd cliproxyapi && go build -o test-output ./cmd/server)
~~~

验证范围应与改动风险相匹配；仅文档变更不需要运行完整构建。

## 构建与部署边界

- 源码开发、前端构建和 Go 二进制构建在 WSL 或专用构建机完成；
- 公网与校园运行节点只接收已验证的不可变产物，不作为 Git 开发或重型构建环境；
- <code>dev/cmsg</code> 是日常集成和生产构建的唯一源分支；
- 两个站点的 Compose、数据角色、渠道资格和故障切换状态必须分别核验；
- 不在仓库、日志、Issue 或示例中提交 API Key、OAuth Token、数据库口令或代理凭据。

详细流程见 <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a>。

## 使用与安全说明

- 使用者必须遵守所接入模型提供商的服务条款以及适用法律法规；
- 部署者需要自行负责访问控制、计费策略、数据保护、审计和上游授权；
- 本仓库的生产定制能力不构成稳定性、额度或技术支持承诺；
- 对外提供生成式 AI 服务前，应确认当地监管、备案和内容安全要求。

## Upstream、署名与许可证

- New API upstream：<a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a>
- New API 源自：<a href="https://github.com/songquanpeng/one-api">One API</a>
- CLIProxyAPI upstream：<a href="https://github.com/router-for-me/CLIProxyAPI">router-for-me/CLIProxyAPI</a>
- License：<a href="./LICENSE">GNU Affero General Public License v3.0</a>

Modified versions that present a user interface must preserve a visible link to the original project: <https://github.com/QuantumNous/new-api>.

This repository preserves the upstream attribution notice: <code>Frontend design and development by New API contributors.</code>
