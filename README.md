# new-api-cmsg

CMSG 生产环境使用的 `new-api` 定制仓库。

本仓库基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 维护，保留 upstream `new-api` 的项目来源、许可证和必要 attribution；本 README 只描述 CMSG 自用分支的开发、部署和定制功能。

## 分支定位

| 分支 | 定位 | 规则 |
|------|------|------|
| `dev/cmsg` | 默认分支、日常开发与生产集成分支 | 所有新功能、修复、上线构建都以它为准 |
| `main` | 服务器初始基线快照 | 只用于历史对照和回溯，不作为日常开发入口 |
| `feature/*` / `fix/*` | 短期开发分支 | 完成验证后合回 `dev/cmsg` |

推荐工作流：

```bash
git clone git@github.com:Yuming12138/newapi-cmsg.git
cd newapi-cmsg
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c feature/your-change
```

完成修改后：

```bash
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git merge --no-ff feature/your-change
git push origin dev/cmsg
```

## CMSG 定制功能

- **分组与余额策略**：支持 `asxs`、`cliproxy-codex`、`cliproxy-codex-pool` 等分组的差异化额度管理，包括“计量但不扣费”、夜间共享余额、订阅制每日额度和余额制上游的混合展示。
- **上游渠道守卫**：为 asxs、LingDang、zz1、OneToken、CPA/CLIProxyAPI 等上游维护余额刷新、可用性检测、自动禁用与恢复策略，避免低价渠道可用时误打高价渠道。
- **CLIProxyAPI 集成**：仓库内包含 `cliproxyapi/`，用于 Codex/Claude 等官方网页账号池中转；已加入 HTTP/2 per-host 连接池、坏连接驱逐、流式首包前重试等稳定性优化。
- **Codex 使用文档**：内置面向桌面端 Codex 和终端版 Codex 的配置说明、示例 `config.toml`、示例 `auth.json`、自定义文档入口和 Chat/Open WebUI 入口。
- **渠道后台增强**：补充 CPA 5h/7d 配额、刷新时间、余额百分比、渠道 tooltip 等管理视图，方便管理员判断账号池剩余额度和下次刷新窗口。
- **额度申请流程**：用户余额不足时可以申请临时额度，支持第一次自动审批、后续进入管理员审核。
- **生产发布约束**：所有代码修改必须先进 Git 分支并推送远端；生产服务器只接收构建产物和配置变更。

## 目录说明

| 路径 | 说明 |
|------|------|
| `web/default/` | 当前主要前端，CMSG UI 修改优先放这里 |
| `web/classic/` | 旧版前端，除非明确需要兼容，否则不作为主要修改目标 |
| `ops/` | 余额刷新、渠道守卫、额度管理等生产辅助脚本 |
| `cliproxyapi/` | CMSG 使用的 CLIProxyAPI/CPA 中转站源码 |
| `docs/proposals/` | 重要架构调整和上线方案记录 |
| `AGENTS.md` | 本仓库开发、分支、构建和生产安全规则 |

## 构建与部署规则

`cmsg-root` 是低资源生产服务器，不能当构建机使用。

允许在服务器上做：

- 接收 WSL 构建好的 `new-api` 或 `cli-proxy-api` 二进制；
- 更新 `/opt/new-api`、`/opt/cliproxyapi` 下的配置；
- 切换 release、重启容器、查看日志和健康检查；
- 做轻量数据库或运行态排查。

不要在服务器上做：

- `bun install`、`bun run build`、`vite build`、`rsbuild build`；
- 大范围 `go build`、Docker build/buildx、镜像重建；
- 把服务器当 Git 源码开发目录直接改代码。

默认开发和构建位置：

```bash
/home/gmchen/work/new-api-cmsg
```

Windows 访问路径：

```text
\\wsl.localhost\Ubuntu-24.04\home\gmchen\work\new-api-cmsg
```

## 上线前检查

常用检查：

```bash
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git branch -r --no-merged origin/dev/cmsg
git status --short
```

如果有未合并分支，先查看：

```bash
git log --oneline origin/dev/cmsg..origin/<branch-name>
```

确认不需要合并后再构建或上线。

## Upstream 与许可证

本仓库是 CMSG 面向生产环境维护的定制分支，不是 upstream 通用发行版文档。

- Upstream: [QuantumNous/new-api](https://github.com/QuantumNous/new-api)
- License: [GNU Affero General Public License v3.0](./LICENSE)
- `new-api` 基于 [One API](https://github.com/songquanpeng/one-api) 发展而来。

Modified versions that present a user interface must preserve a visible link to the original project: <https://github.com/QuantumNous/new-api>.

This repository preserves the upstream attribution notice required by the upstream project: `Frontend design and development by New API contributors.`
