# new-api-cmsg

CMSG 生产环境使用的 `new-api` 定制仓库。

本仓库基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 维护，保留 upstream `new-api` 的项目来源、许可证和必要 attribution；本 README 只描述 CMSG 自用分支的开发、部署和定制功能。

## CMSG 定制功能

- **分组与余额策略**：支持 `asxs`、`cliproxy-codex`、`cliproxy-codex-pool` 等分组的差异化额度管理，包括“计量但不扣费”、夜间共享余额、订阅制每日额度和余额制上游的混合展示。
- **上游渠道守卫**：为 asxs、LingDang、zz1、OneToken、CPA/CLIProxyAPI 等上游维护余额刷新、可用性检测、自动禁用与恢复策略，避免低价渠道可用时误打高价渠道。
- **CLIProxyAPI 集成**：仓库内包含 `cliproxyapi/`，用于 Codex/Claude 等官方网页账号池中转；已加入 HTTP/2 per-host 连接池、坏连接驱逐、流式首包前重试等稳定性优化。
- **Codex 使用文档**：内置面向桌面端 Codex 和终端版 Codex 的配置说明、示例 `config.toml`、示例 `auth.json`、自定义文档入口和 Chat/Open WebUI 入口。
- **渠道后台增强**：补充 CPA 5h/7d 配额、刷新时间、余额百分比、渠道 tooltip 等管理视图，方便管理员判断账号池剩余额度和下次刷新窗口。
- **额度申请流程**：用户余额不足时可以申请临时额度，支持第一次自动审批、后续进入管理员审核。

## 开发说明

分支定位、目录说明、构建部署规则和上线前检查见 [DEVELOPMENT.md](./DEVELOPMENT.md)。

## Upstream 与许可证

本仓库是 CMSG 面向生产环境维护的定制分支，不是 upstream 通用发行版文档。

- Upstream: [QuantumNous/new-api](https://github.com/QuantumNous/new-api)
- License: [GNU Affero General Public License v3.0](./LICENSE)
- `new-api` 基于 [One API](https://github.com/songquanpeng/one-api) 发展而来。

Modified versions that present a user interface must preserve a visible link to the original project: <https://github.com/QuantumNous/new-api>.

This repository preserves the upstream attribution notice required by the upstream project: `Frontend design and development by New API contributors.`
