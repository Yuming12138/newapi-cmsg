# new-api-cmsg

CMSG 面向生产环境维护的 `new-api` 定制仓库。

本仓库基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 维护，保留 upstream `new-api` 的项目来源、许可证和必要 attribution；本 README 只介绍项目定位和 CMSG 面向用户、管理员的定制能力，开发与部署约定统一记录在 `DEVELOPMENT.md`。

## CMSG 定制功能

- **分组、计量与计费策略**：支持 `asxs`、`cliproxy-codex`、`cliproxy-codex-pool` 等分组的差异化额度管理，包括“计量但不扣费”、夜间共享余额、订阅制每日额度和余额制上游的混合展示。
- **额度感知的渠道调度**：为 asxs、LingDang、zz1、OneToken、CPA/CLIProxyAPI 等上游维护余额刷新、低价优先、高价兜底、自动禁用与恢复策略；模型别名按实际映射的上游模型计费。
- **CPA 账号池与模型级额度**：展示账号的 5h/7d 配额、刷新时间和重置机会，区分 Plus/Pro 等账号策略，并对 GPT-5.3-Codex-Spark 等模型实施独立额度守卫。
- **Codex/Claude 路由与稳定性**：仓库内包含 `cliproxyapi/`，支持账号池、模型映射、工具调用和 reasoning 兼容；已加入 HTTP/2 per-host 连接池、旧代理路径退出、首包前重试和 Mihomo 节点故障转移。
- **图片生成与编辑工作台**：网页同时支持文生图和图生图/编辑，可上传 1-4 张参考图和可选蒙版；API 提供 `/v1/images/generations` 与 `/v1/images/edits`。
- **看板与请求可观测性**：支持按渠道统计用量和自定义时间窗口，并提供请求链路、CPA 调度审计、reasoning effort 与流式状态等诊断信息。
- **账户与注册安全**：支持注册口令、Turnstile、可选安全会话 Cookie、用户硬删除时清理关联凭据，以及用户可控网络请求的 SSRF 防护。
- **新手文档与用户自助**：内置桌面端和终端版 Codex 配置说明、示例 `config.toml` 与 `auth.json`、Chat/Open WebUI 入口；低余额用户可提交临时额度申请。

## 开发说明

分支定位、目录说明、构建部署规则和上线前检查见 [DEVELOPMENT.md](./DEVELOPMENT.md)。

## Upstream 与许可证

本仓库是 CMSG 面向生产环境维护的定制分支，不是 upstream 通用发行版文档。

- Upstream: [QuantumNous/new-api](https://github.com/QuantumNous/new-api)
- License: [GNU Affero General Public License v3.0](./LICENSE)
- `new-api` 基于 [One API](https://github.com/songquanpeng/one-api) 发展而来。

Modified versions that present a user interface must preserve a visible link to the original project: <https://github.com/QuantumNous/new-api>.

This repository preserves the upstream attribution notice required by the upstream project: `Frontend design and development by New API contributors.`
