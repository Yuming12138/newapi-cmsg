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

<code>new-api-cmsg</code> 把多种 AI 上游、账号池和协议统一到一个可运营的网关中。除了 upstream New API 提供的用户、令牌、分组、计量和渠道管理能力，CMSG 还维护了额度感知调度、CPA 账号池、Codex/Claude/Gemini/DeepSeek/Kimi 路由与协议适配、渠道能力测试、Codex Radar、图片工作台和服务端长上下文保护。

本仓库主要解决以下问题：

- 为 Codex、Claude Code、OpenAI SDK 等兼容客户端提供一致的入口；
- 将共享额度、订阅额度和按量余额转化为可解释、可恢复的渠道策略；
- 在 New API 与 CLIProxyAPI 两层保留模型映射、reasoning、工具调用和流式响应语义；
- 让管理员能够定位渠道、账号、网络、配额和协议转换问题；
- 展示渠道测试输出与外部公开评分，帮助管理员比较模型和渠道表现；
- 将源码开发、构建产物和生产运行环境明确分离。

## 请求链路

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

当前生产入口为 <a href="https://api.cmsg666.xyz/">api.cmsg666.xyz</a>，由 <code>cmsg-root</code> 上的公网运行实例提供服务。

## CMSG 维护能力

| 领域 | 能力 |
| --- | --- |
| 统一协议入口 | OpenAI Responses、Chat Completions、Claude Messages、Gemini 及常见 OpenAI-compatible 请求 |
| 分组与计量 | 差异化分组、管理员计量但不扣费、订阅制每日额度、共享余额及按量上游的混合展示 |
| 额度感知调度 | 余额刷新、低价优先、高价兜底、渠道自动禁用与恢复、可指定到期时间的临时解除限制、模型别名按真实上游模型结算 |
| CPA 账号池与额度 | Codex/Claude/Kimi/Antigravity 账号池、模型级额度、订阅窗口、重置时间、有效账号去重、冷却与重试 |
| Codex 协议兼容 | DeepSeek 原生 Responses 思考原文续接、跨提供方历史兼容、Kimi 工具适配、自定义与命名空间工具、助手说明与工具结果顺序处理 |
| 稳定性 | 首包前重试、HTTP/2 per-host 连接池、Mihomo 节点故障转移、流式断连清理 |
| Gemini 性能与上下文 | Tool Schema 缓存、请求线性拼装、流式事件转换优化、发送前预算校验、超大工具输出裁剪和明确 token 超限后的有限重试 |
| 渠道能力测试 | 当前灰度支持渠道 1、12、27，可选模型与思考强度，展示测试题、回答、耗时、失败原因和 SVG 动画预览 |
| Codex Radar | 同步四个 Sol/Astra 模型家族的公开评分，展示覆盖题数与成本/耗时统计，提供失败重试、Redis 完整快照和前端自动恢复 |
| 图片工作台 | 文生图、图生图/编辑、1 至 4 张参考图、可选蒙版及对应 API |
| 可观测性 | 渠道用量、自定义时间窗口、请求链路、CPA 调度、reasoning effort、流式状态和网络探针 |
| 账户与安全 | 新注册用户默认归入 <code>cmsg</code> 身份分组、注册口令、Turnstile、安全会话 Cookie、已注销用户的管理员硬删除清理和 SSRF 防护 |
| 用户自助 | Codex 桌面端/终端配置说明、上游余额概览、低余额临时额度申请 |

具体可用模型、分组和上游由运行时配置、账号状态与渠道策略共同决定；仓库中的能力说明不等同于对所有模型持续可用的承诺。

## 渠道测试与评分参考

### 本站渠道能力测试

管理员可以在渠道页选择模型和思考强度，向指定渠道发起鹈鹕骑自行车 SVG 测试。测试使用该渠道的实际配置与模型映射，不通过其他渠道兜底。面板展示题目、回答、请求模型、上游报告模型、耗时和失败原因；SVG 经安全清洗后提供动画预览，结果面板保留到用户主动关闭。

当前测试接口只开放渠道 1、12、27。通过标记主要表示检测到完整 SVG，不是全面的模型能力评分，也不能单凭一次输出确定模型身份或是否降智。测试会发起实际模型请求，可能消耗上游额度。

### Codex Radar 公开评分

仪表盘同步 <a href="https://codexradar.com/">Codex Radar</a> 的 RadarBench v2 汇总数据，展示 <code>gpt-5.6-sol</code>、<code>gpt-6-sol</code>、<code>gpt-6.1-sol</code> 和 <code>gpt-6-astra</code>。每个模型包括 <code>low</code>、<code>medium</code>、<code>high</code>、<code>xhigh</code>、<code>max</code>、<code>ultra</code> 六档，共 24 个点位；Luna 不在此面板展示。

这些是外部公开评分，不是本站渠道的实测结果。成本字段是上游统计的 API 等价成本，不是本站扣费价格。缺失统计保持未知，采集失败时可以使用 24 小时内的完整缓存，并保留原采集时间与缓存标记；启用 Redis 的实例支持跨重启恢复。详见 <a href="./docs/codex-radar.md">雷达数据与失败保护说明</a>。

上述测试与评分用于人工比较和排查；当前没有据此自动调整渠道优先级，也不构成准确检测降智的保证。

## 额度面板与协议保护

渠道页与仪表盘区分美元余额、人民币余额和订阅额度百分比，不把未知数据当成零或满额。Codex、Kimi 与 Gemini CPA 使用各自的额度采集链路；订阅窗口和重置时间只展示上游实际提供的字段。Gemini 按独立额度池汇总，不从重置时间推断 5 小时或月度窗口，也不把百分比换算成美元。详见 <a href="./docs/gemini-cpa-quota.md">Gemini CPA 额度语义</a>。

DeepSeek 原生 Responses 适配优先保留实际返回的思考原文，并按用户、令牌、渠道和线程隔离恢复缓存。GPT/Kimi 等外来历史缺少原文时使用兼容策略；摘要或占位内容不等于原始思考，加密内容无法被无损还原。自定义、动态和命名空间工具在请求与返回流中保持调用标识和客户端名称。详见 <a href="./ops/deepseek_codex_responses.md">DeepSeek 与 Codex 工具续接说明</a>。

CPA 的 Antigravity/Gemini 路径在服务端检查上下文预算，超限时按规则裁剪历史或超大工具输出，并插入截断说明。最终请求仍超过预算则返回明确错误；上游明确报告输入 token 超限时，最多进行一次确实缩小请求的重试。这不是无损摘要，token 估算也不等同官方分词，不能保证所有多模态极端输入都自动修复。详见 <a href="./docs/antigravity-context-budget.md">长上下文保护说明</a>。

新注册用户默认属于 <code>cmsg</code> 身份分组；这不等于强制设置 API Token 的路由分组，也不会批量修改已有用户。详见 <a href="./docs/registration-default-cmsg.md">默认注册分组说明</a>。

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
| <code>docs/</code> | 雷达、额度、上下文保护、注册分组等功能说明与运行手册 |
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

- 当前维护使用本地 Windows 工作副本；源码开发与前端构建也可在 WSL、CI 或专用构建机完成，不要求依赖 WSL；
- Windows 可使用 <code>CGO_ENABLED=0 GOOS=linux GOARCH=amd64</code> 交叉编译生产二进制，部署前核验版本、目标架构、静态链接与 SHA-256；
- 公网运行节点只接收已验证的不可变产物，不作为 Git 开发或重型构建环境；
- <code>dev/cmsg</code> 是日常集成和生产构建的唯一源分支；
- 发布前核验目标实例的 Compose、数据角色和渠道配置，不以历史部署状态推断当前运行状态；
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
