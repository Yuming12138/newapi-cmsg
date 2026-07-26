# CMSG 开发说明

本文档记录 `new-api-cmsg` 的开发、分支、构建和部署约定。仓库首页 README 只展示项目定位和功能概览。

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

## 目录说明

| 路径 | 说明 |
|------|------|
| `web/default/` | 当前主要前端，CMSG UI 修改优先放这里 |
| `web/classic/` | 旧版前端，除非明确需要兼容，否则不作为主要修改目标 |
| `ops/` | 余额刷新、渠道守卫、额度管理等生产辅助脚本 |
| `cliproxyapi/` | CMSG 使用的 CLIProxyAPI/CPA 中转站源码 |
| `docs/proposals/` | 重要架构调整和上线方案记录 |
| `AGENTS.md` | 本仓库开发、分支、构建和生产安全规则 |

## 双上游与 CLIProxyAPI subtree

`new-api-cmsg` 是唯一需要提交、推送和用于生产构建的 Git 仓库。它同时跟踪两个上游：

| Remote | 上游 | 更新范围 |
|--------|------|----------|
| `upstream` | `https://github.com/QuantumNous/new-api.git` | 仓库根目录的 New API 代码 |
| `cliproxy-upstream` | `https://github.com/router-for-me/CLIProxyAPI.git` | `cliproxyapi/` subtree |

首次克隆后配置 CPA 上游：

```bash
git remote add cliproxy-upstream https://github.com/router-for-me/CLIProxyAPI.git
git config remote.cliproxy-upstream.tagOpt --no-tags
git fetch --no-tags cliproxy-upstream main
```

`cliproxyapi/` 是外层仓库直接跟踪的 subtree，不应包含独立 `.git`。不要在该目录内执行 `git pull`、`git merge`、`git rebase`、`git reset`、`git checkout`、`git clean` 或单独提交。

同步 CPA 上游时使用独立分支：

```bash
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c sync/cliproxy-vX.Y.Z
git fetch --no-tags cliproxy-upstream main
git subtree pull \
  --prefix=cliproxyapi \
  cliproxy-upstream main \
  --squash \
  -m "sync(cliproxy): update upstream to vX.Y.Z"
```

解决与 CMSG 定制代码的冲突后，在 `cliproxyapi/` 内完成测试和构建，再将同步分支合回 `dev/cmsg`。同步提交应记录上游 tag 和完整 commit，便于以后审计。

New API 上游继续使用普通外层 Git 工作流同步；不要用 subtree 命令更新仓库根目录。

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

## 生产发布原则

- `dev/cmsg` 是唯一日常集成源。
- 生产构建从 WSL 或专用构建机产出。
- `cmsg-root:/opt/new-api` 和 `cmsg-root:/opt/cliproxyapi` 只作为运行环境。
- 服务器上的 Git 历史、源码目录和临时文件不能作为后续开发基线。
- 上线后需要验证容器状态、健康检查和相关功能页面或 API。

## 上游同步记录

### 2026-07-26：New API 与 CPA 稳定性批次

本轮采用逐项审查和手工移植，不直接整体合并上游主分支。审查基线：

| 上游 | 审查 commit |
|------|-------------|
| `QuantumNous/new-api` | `3e1e728279884d83358811aec00980dd55f6ad4e` |
| `router-for-me/CLIProxyAPI` | `42a00a2a6521b867c27f7ad096d08699db8e6d19` |

本轮累计移植 **16 个逻辑改进**，由两个集成批次合入 `dev/cmsg`：

| 集成 commit | 内容 |
|-------------|------|
| `14264dc43` | 第一批 12 项 New API 管理端、relay 与 CPA Codex 输入/工具调用防护 |
| `6324679ed` | 第二批 4 项剩余可靠性改进 |

第二批包含：

1. 无限额度 API Key 仍显示实际已用额度。
2. New API 代理 URL 校验、规范化客户端缓存和定向失效。
3. CPA token accounting v2，避免缓存 token 与 reasoning token 重叠计算。
4. 工具转换后没有可用工具时，CPA 不再发送 `tool_choice` 和 `parallel_tool_calls`。

完成的整体验证：

- New API：`go test ./...`
- 默认前端：`cd web/default && bun run build:check`
- CPA：`cd cliproxyapi && go test ./...`
- CPA：`cd cliproxyapi && go build -o test-output ./cmd/server`

暂缓项目：

- Playground `auto` 分组模型列表：有实际需求，但 CMSG 默认前端需要单独适配，不能直接 cherry-pick。
- 用户搜索防抖、充值金额可完全清空：低风险但不紧急，留待后续前端维护批次。
- Gemini 新图片模型注册：仅在准备开放对应模型时移植。
- CPA WebSocket、Multi-Agent V2、Claude 特定转换和 WebRTC 改动：当前现网路径未启用或回归风险较高，暂不合并。

### 2026-07-26 补充批次（sync/upstream-20260726-followup）

上游 HEAD 与本轮审查基线一致（无新提交），本批次移植主轮遗漏项并修复一处仓库损坏：

| 上游 commit | 内容 |
|-------------|------|
| `dfc0d6324` | 用户设置缓存加固：`updateUserCache` 只刷新非额度字段，避免覆盖原子额度缓存；`PUT /api/user/self` 加 CriticalRateLimit |
| `153d7f01a` | 客户端断连后停止写流：cleanup 前无条件 `wg.Wait`、断连立即关闭上游 body 停止计费、每次写加 write deadline |
| `4aa08f917` | Playground `auto` 分组模型列表（复评：实为纯后端改动）：`GetUserModels` 支持 `?group=` 参数，`auto` 展开为实际分组；新增 `service.GetGroupsEnabledModels` |

仓库修复：根 `.gitignore` 的裸 `data/` 规则误忽略了 `web/default/src/features/usage-logs/data/`，导致 5 个文件 import 的 schema 模块从未入库、`bun run build:check` 在 `dev/cmsg` 上失败。规则锚定为 `/data/`，并从上游原样恢复 `schema.ts`。

保留的 CMSG 定制：ListModels 的模型分组路由（`GetModelGroupRouteModels`）、Codex SSE header 透传、Claude 格式 ping、ping RecordPing 统计。未采纳上游 `ownerGroups` 重构。

验证：`go test ./...` 全绿；`cd web/default && bun run build:check` 通过（修复前失败）。

注意：`4aa08f917` 的前端部分（playground 传 `?group=auto`）在上游新 `web/` 布局中，CMSG 默认前端如需展示 auto 分组模型仍要单独适配；后端已就绪且向后兼容。

## 前端同步策略（2026-07-26 决定）

**`web/default/` 为 CMSG 自主维护的前端，不再跟随上游前端整体同步。**

背景与依据：

- 上游 `31d70fca3`（2026-07-20，#6329）将前端从 `web/default/` 整体迁至 `web/`（905 个纯改名 + 73 个 auth 相关内容修改），并**删除了整个 `web/classic/`**（424 文件）与 theme 切换机制。
- 更早的分歧才是主因：上游 7 月上旬的 design-system 大改版（±11 万行）CMSG 从未跟进，CMSG 自有前端定制已达 154 文件 / +1.5 万行。即使路径对齐，内容层面 cherry-pick 也无法应用。
- CMSG 前端事实上等于「上游 6 月底状态 + CMSG 定制」，7/20 的搬迁只是把分叉显性化。

执行规则：

1. 上游前端提交只作参考，有价值的修复以**手工移植**方式进入 `web/default/`；同类修复优先只取后端部分（参照 `4aa08f917` 的处理）。
2. 路径映射：上游 `web/src/<x>` ↔ CMSG `web/default/src/<x>`。搬迁 905/978 为纯改名，`src/` 内部结构两侧仍同构，移植时先做前缀替换再核对内容分歧。
3. `web/classic/` 与 theme 切换机制由 CMSG 独立保留（上游已删）。若确认生产无 `theme=classic` 用户，可另行评估退役。
4. 后端同步策略不变：继续逐项审查、手工移植上游后端提交。

关联暂缓项：上游 auth 重构（`31d70fca3` 后端部分：无状态 token、会话吊销、分布式强制下线，约 76 个后端文件，含 `controller/auth_session.go`、`model/user_session.go` 870 行等）作为独立安全项目排期移植，方案见 `docs/proposals/auth-stateless-session-port.md`。注意其上线会使全体已登录用户登出一次，需选窗口期；`172114422`（限流时保持登录态）依赖该重构，一并顺延。
