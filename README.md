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

本仓库是 CMSG 基于 <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a> 维护的生产定制分支，并在 <code>cliproxyapi/</code> 中集成 CLIProxyAPI/CPA。

## 项目定位

<code>new-api-cmsg</code> 把多种 AI 上游、账号池和协议统一到一个可运营的网关中。除了 upstream New API 提供的用户、令牌、分组、计量和渠道管理能力，CMSG 还维护了额度感知调度、CPA 账号池、Codex/Claude/Gemini/DeepSeek/Kimi 路由与协议适配、渠道能力测试、Codex Radar、服务端长上下文保护和 Responses 会话增量持久化。

本仓库主要解决以下问题：

- 为 Codex、Claude Code、OpenAI SDK 等兼容客户端提供一致的入口；
- 将共享额度、订阅额度和按量余额转化为可解释、可恢复的渠道策略；
- 在 New API 与 CLIProxyAPI 两层保留模型映射、reasoning、工具调用和流式响应语义；
- 让管理员能够定位渠道、账号、网络、配额和协议转换问题；
- 展示渠道测试输出与外部公开评分，帮助管理员比较模型和渠道表现；
- 将源码开发、构建产物和生产运行环境明确分离。

## 请求链路

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

当前生产入口为 <a href="https://api.cmsg666.xyz/">api.cmsg666.xyz</a>，由 <code>cmsg-root</code> 上的公网运行实例提供服务。

## CMSG 维护能力

| 领域                | 能力                                                                                                                    |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| 统一协议入口        | OpenAI Responses、Chat Completions、Claude Messages、Gemini 及常见 OpenAI-compatible 请求                               |
| 分组与计量          | 差异化分组、用量计量、订阅制每日额度、共享余额及按量上游的混合展示                                                      |
| 额度感知调度        | 余额刷新、低价优先、高价兜底、渠道自动禁用与恢复、可指定到期时间的临时解除限制、模型别名按真实上游模型结算              |
| CPA 账号池与额度    | Codex/Claude/Kimi/Antigravity 账号池、模型级额度、订阅窗口、重置时间、有效账号去重、冷却与重试                          |
| Codex 协议兼容      | DeepSeek 原生 Responses 思考原文续接、跨提供方历史兼容、Kimi 工具适配、自定义与命名空间工具、助手说明与工具结果顺序处理 |
| Responses 会话存储  | 每轮增量、周期与分支检查点、批量恢复、字节上限 LRU、single-flight、受限并发重建及 SHA-256 大附件外置                   |
| 稳定性              | 首包前重试、瞬时故障保留 CLI 会话亲和、HTTP/2 per-host 连接池、指标刷盘熔断、Mihomo 节点故障转移、流式断连清理            |
| Gemini 性能与上下文 | Tool Schema 缓存、请求线性拼装、流式事件转换优化、发送前预算校验、超大工具输出裁剪和明确 token 超限后的有限重试         |
| 渠道能力测试        | 当前灰度支持渠道 1、12、27，可选模型与思考强度，展示测试题、回答、耗时、失败原因和 SVG 动画预览                         |
| Codex Radar         | 同步四个 Sol/Astra 模型家族的公开评分，展示覆盖题数与成本/耗时统计，提供失败重试、Redis 完整快照和前端自动恢复          |
| 可观测性            | 按映射后上游模型归组的性能指标、渠道用量、自定义时间窗口、请求链路、CPA 调度、reasoning effort、流式状态和网络探针       |
| 用户自助            | Codex 桌面端/终端配置说明、上游余额概览、低余额临时额度申请                                                             |
| 上游积分与折算      | cli2api 账号 credits 汇总、渠道余额刷新、按参考模型单价折算进每日额度池、仪表盘积分口径展示                               |

可用模型与分组以运行实例返回的模型列表和渠道配置为准。

## 2026-10-14 以来的能力更新

| 主题 | 提交 | 更新 |
| --- | --- | --- |
| DeepSeek thinking 续接 | `59a8988` `d1b6e17` | 为 thinking + tools 请求中缺少 reasoning 的助手组补齐占位内容，覆盖工具轮和最终助手组，避免上游返回 `reasoning_text must be passed back`。 |
| Reasoning 去重 | `7626159` `df5e9a9` | 服务端会话回放和 Codex Desktop 内联历史都会删除相邻重复 reasoning，避免长上下文成倍增长。 |
| 跨渠道续接 | `26ed4fa` `3d9c84c` | DeepSeek 线程切到 GPT 渠道时重放本地 transcript，移除原提供方 reasoning 和 `previous_response_id`；GPT-5/GPT-6 等模型的请求清洗与诊断日志同步完善。 |
| 亲和稳定性 | `c3c6da9` | Codex/Claude CLI 会话遇到首包前超时或 5xx 时保留渠道亲和；限额、限流、鉴权等确定性错误仍正常清除亲和并故障转移。 |
| 性能与用量看板 | `06affc8` `4fe400b` `5d48a54` `f99ca9b` | 模型性能、渠道用量、渠道测试和异步任务统一按映射后的实际上游模型归组；指标刷盘连续失败后开熔断，避免拖垮转发主链路。 |
| 会话诊断 | `c944397` | 原生 Responses 入参记录脱敏类型直方图，便于定位长线程提前结束、reasoning 或工具条目异常。 |
| 运行资源边界 | `89d3086` | Responses 历史使用压缩 canonical 快照和 7 天清理；GORM 只记录参数化 SQL 形状；Docker 日志限制为 `20 MiB × 5`。 |
| 增量会话存储 | `37bf925` `ad41544` `fdcdbb8` | 引入改写感知增量、周期/分支检查点、批量恢复、LRU、single-flight、受限并发重建和大附件 SHA-256 外置；后续提交修复旧 reasoning 改写导致的逐轮完整检查点，并去除重复编码。 |
| Codex 工具续接修复 | `73e6f5d` `23821ad` `0116443` `9e0b899` `745c458` | 修复工具调用标记被当作纯文本吐出的问题：从 `additional_tools` 输入项读取工具声明、把 namespace 与 custom 工具展平为函数、system/developer 轮降级为 user 轮、`custom_tool_call_output` 转为 tool 消息（此前丢弃会导致模型看不到命令结果而反复重试）。 |
| 图片与 reasoning 兼容 | `ed53ffb` `36e74ad` | Responses→Chat 转换把字符串形式的 `input_image.image_url` 归一化为对象；同时记录 reasoning effort，供计费与诊断使用。 |
| cli2api 渠道余额 | `fe1e402` | Advanced Custom（type 59）渠道补齐余额刷新，调用上游 `/api/accounts` 汇总 ready 账号的 credits；仪表盘新增 `c2a` 字段，按积分口径展示，替代原先的 Gemini CPA 美元估算行。 |
| 上游积分折算 | `928df7a` | 新增 `c2a_credit_pool_setting`，把上游 credits 先按模型倍率折算成 token、再按站内参考模型单价折算成美元并入每日额度池；参考模型、倍率与输出占比均可配置。 |
| 推理档位映射 | `07ed900` | 新增 `advanced_custom.reasoning_effort_map`，按渠道改写发往上游的 reasoning 档位，解决上游把 `xhigh` 静默降级为 `high`（DeepSeek 系最高档为 `max`）的问题；默认关闭，不做改写。 |

## 渠道测试与评分参考

### 本站渠道能力测试

管理员可以在渠道页选择模型和思考强度，向指定渠道发起鹈鹕骑自行车 SVG 测试。请求固定到所选渠道，使用其实际配置与模型映射。面板展示题目、回答、请求模型、上游报告模型、耗时和失败原因；SVG 经安全清洗后提供动画预览，结果面板保留到用户主动关闭。

当前测试接口开放渠道 1、12、27，以检测到完整 SVG 作为通过条件。管理员可以直接查看图像和原始回答，比较不同渠道的输出。测试发起实际模型请求并使用上游额度。

### Codex Radar 公开评分

仪表盘同步 <a href="https://codexradar.com/">Codex Radar</a> 的 RadarBench v2 汇总数据，展示 <code>gpt-5.6-sol</code>、<code>gpt-6-sol</code>、<code>gpt-6.1-sol</code> 和 <code>gpt-6-astra</code>。每个模型包括 <code>low</code>、<code>medium</code>、<code>high</code>、<code>xhigh</code>、<code>max</code>、<code>ultra</code> 六档，共 24 个点位。

面板展示公开评分、覆盖题数、API 等价成本与耗时。缺失统计保持未知，采集失败时可以使用 24 小时内的完整缓存，并保留原采集时间与缓存标记；启用 Redis 的实例支持跨重启恢复。详见 <a href="./docs/codex-radar.md">雷达数据与失败保护说明</a>。

管理员可结合渠道测试输出和 Radar 评分，评估渠道表现并调整调度配置。

## 额度面板与协议保护

渠道页与仪表盘按美元、人民币和订阅额度百分比展示数据，缺失字段标记为未知。Codex、Kimi 与 Gemini CPA 使用各自的额度采集链路，展示上游提供的订阅窗口和重置时间。Gemini 按独立额度池汇总百分比，并保留各账号的重置时间。详见 <a href="./docs/gemini-cpa-quota.md">Gemini CPA 额度语义</a>。

cli2api（Advanced Custom）渠道的上游额度以 credits 计价，渠道余额与仪表盘均按积分原值展示；参与每日额度池时先按模型倍率换算成 token，再按站内参考模型单价折算成美元，换算参数见 `c2a_credit_pool_setting`，未配置折算时该渠道不计入池。

DeepSeek 原生 Responses 适配保留返回的思考原文，并按用户、令牌、渠道和线程隔离恢复缓存。GPT/Kimi 等跨提供方历史使用独立兼容策略。自定义、动态和命名空间工具在请求与返回流中保持调用标识和客户端名称。详见 <a href="./ops/deepseek_codex_responses.md">DeepSeek 与 Codex 工具续接说明</a>。

### Responses 会话增量存储

长会话按 `response ID` 保存独立增量，第 1 轮、每 20 轮和分支点保存完整检查点。增量格式记录父历史的基准长度、被上游改写的旧项索引和新追加项，兼容 DeepSeek 在新一轮重新规范化 reasoning 内容的情况。恢复时批量读取最多 24 轮增量，并校验消息数量、完整 items 哈希与 pending 工具调用哈希。

运行层提供按字节淘汰的进程内 LRU、same-ID single-flight、默认 2 路受限重建和 128 MiB 会话上限；超过 256 KiB 的文本、图片或工具输出按 SHA-256 外置到持久化 Blob 目录。清理任务按会话 head 水位整线程删除，避免删除仍活跃的线程。

存储模式支持 `legacy`、`shadow` 和 `incremental`。`shadow` 同时写入旧快照和增量表并比对重建结果，生产默认使用 `incremental`。配置项、灰度步骤和回滚方法见 <a href="./ops/deepseek_codex_responses.md#2026-10-21-responses-会话增量存储">Responses 增量存储运行说明</a>。

CPA 的 Antigravity/Gemini 路径在服务端估算上下文预算，超限时按规则裁剪历史或超大工具输出，并插入截断说明；裁剪会移除部分原文。最终请求仍超过预算则返回明确错误，上游明确报告输入 token 超限时，最多进行一次缩小请求后的重试。详见 <a href="./docs/antigravity-context-budget.md">长上下文保护说明</a>。

## 主要 API

| 用途             | 接口                                                                     |
| ---------------- | ------------------------------------------------------------------------ |
| Responses        | <code>POST /v1/responses</code>、<code>POST /v1/responses/compact</code> |
| Chat Completions | <code>POST /v1/chat/completions</code>                                   |
| Claude Messages  | <code>POST /v1/messages</code>                                           |
| 模型发现         | <code>GET /v1/models</code>                                              |

## 仓库结构

| 路径                                                                 | 说明                                       |
| -------------------------------------------------------------------- | ------------------------------------------ |
| <code>controller/</code>、<code>service/</code>、<code>model/</code> | New API 请求、业务与数据层                 |
| <code>relay/</code>、<code>middleware/</code>、<code>setting/</code> | 协议转换、分发、计费和运行设置             |
| <code>web/default/</code>                                            | CMSG 唯一维护的 React 前端                 |
| <code>cliproxyapi/</code>                                            | 作为 subtree 跟踪的 CLIProxyAPI/CPA 源码   |
| <code>ops/</code>                                                    | 余额刷新、渠道守卫、额度控制和观测脚本     |
| <code>docs/</code>                                                   | 雷达、额度、上下文保护等功能说明与运行手册 |
| <code>docs/proposals/</code>                                         | 重要架构与上线方案                         |
| <code>AGENTS.md</code>                                               | 代码、兼容性、分支与生产安全规则           |
| <code>DEVELOPMENT.md</code>                                          | 开发、上游同步、构建和部署约定             |

## 分支与开发

| 分支                           | 定位                                           |
| ------------------------------ | ---------------------------------------------- |
| <code>dev/cmsg</code>          | 默认分支、日常开发与生产集成源                 |
| <code>main</code>              | 历史服务器基线，仅用于对照                     |
| `feature/*`、`fix/*`、`docs/*` | 短期工作分支，验证后合回 <code>dev/cmsg</code> |

```bash
git clone git@github.com:Yuming12138/newapi-cmsg.git
cd newapi-cmsg
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c feature/your-change
```

开始修改前请完整阅读 <a href="./AGENTS.md">AGENTS.md</a> 和 <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a>。常用验证：

```bash
go test ./...
(cd web/default && bun run build:check)
(cd cliproxyapi && go test ./...)
(cd cliproxyapi && go build -o test-output ./cmd/server)
# 并发相关改动；需要 CGO 和 gcc/clang
CGO_ENABLED=1 go test -race ./model ./relay/channel/deepseek ./relay/channel/mimo
```

在 Windows 上，race 测试需要先安装 MinGW 或兼容的 LLVM 工具链，并让 `CC` 指向编译器。验证范围应与改动风险相匹配；仅文档变更不需要运行完整构建。

## 构建与部署边界

- 当前维护使用本地 Windows 工作副本，CI 或专用构建机也可产出部署文件；
- Windows 可使用 <code>CGO_ENABLED=0 GOOS=linux GOARCH=amd64</code> 交叉编译生产二进制，部署前核验版本、目标架构、静态链接与 SHA-256；
- 生产二进制使用 <code>CGO_ENABLED=0</code>；race 检测是本地验证步骤，使用单独的 CGO 构建，不影响发布产物；
- 公网节点负责运行服务，接收已验证的不可变产物；源码开发和重型构建在本地或构建机完成；
- <code>dev/cmsg</code> 是日常集成和生产构建的唯一源分支；
- 发布前核验目标实例的 Compose、数据角色和渠道配置；
- API Key、OAuth Token、数据库口令和代理凭据保存在受保护的运行配置中。

详细流程见 <a href="./DEVELOPMENT.md">DEVELOPMENT.md</a>。

## 使用与安全说明

- 使用者必须遵守所接入模型提供商的服务条款以及适用法律法规；
- 部署者需要自行负责访问控制、计费策略、数据保护、审计和上游授权；
- 对外提供生成式 AI 服务前，应确认当地监管、备案和内容安全要求。

## Upstream、署名与许可证

- New API upstream：<a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a>
- New API 源自：<a href="https://github.com/songquanpeng/one-api">One API</a>
- CLIProxyAPI upstream：<a href="https://github.com/router-for-me/CLIProxyAPI">router-for-me/CLIProxyAPI</a>
- License：<a href="./LICENSE">GNU Affero General Public License v3.0</a>

Modified versions that present a user interface must preserve a visible link to the original project: <https://github.com/QuantumNous/new-api>.

This repository preserves the upstream attribution notice: <code>Frontend design and development by New API contributors.</code>
