# CMSG 开发与部署说明

当前维护使用 Windows 工作副本，从 `dev/cmsg` 构建产物并发布到公网运行节点。
本文前半部分是现行流程，末尾的上游同步记录保留各批次的历史决定和验证结果。

## 环境与职责

| 对象 | 路径或入口 | 职责 |
| --- | --- | --- |
| 源码工作副本 | `F:\WorkBuddy\2026-10-06-16-58-21\src\newapi-cmsg` | 日常开发、本地测试与构建 |
| 集成分支 | `dev/cmsg` | 已验证修改与生产构建源 |
| New API 运行目录 | `cmsg-root:/opt/new-api` | 接收产物、切换 release、运行服务 |
| CPA 运行目录 | `cmsg-root:/opt/cliproxyapi` | CPA 服务及其独立发布 |
| 公网入口 | `https://api.cmsg666.xyz` | 客户端 API 与管理界面 |

只维护上述 Windows 工作副本；旧 WSL 仓库保留为历史副本。CI 或专用构建机
也可从同一集成 commit 产出部署文件。校园 New API 站点已停用，校园相关手册
属于历史资料；CPA/Home 服务单独核实，不能据此推断它们的运行状态。

`cmsg-root` 是低资源生产机，负责接收已构建产物、运行配置、轻量排查和服务
切换。Go/Bun/Docker 构建、安装依赖和源码开发在本地或构建机完成。发布前
重新确认目标 Compose 文件、服务名、端口和二进制挂载，以实际运行结构为准。

## 分支与工作副本

| 分支 | 定位 |
| --- | --- |
| `dev/cmsg` | 默认分支、日常集成和生产构建源 |
| `main` | 历史基线，用于对照和回溯 |
| `feature/*`、`fix/*`、`docs/*`、`sync/*` | 短期工作分支，验证后合入 `dev/cmsg` |

开始任务先读 [AGENTS.md](AGENTS.md)，检查分支、commit、remotes 和工作树。
有未提交修改时先识别归属，保留无关修改。下列 Git 命令可在 PowerShell
逐行执行；只在工作树允许切换时更新分支：

~~~text
git status --short --branch
git fetch --all --prune
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c docs/your-change
~~~

完成验证后，提交工作分支，再合入 `dev/cmsg`：

~~~text
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git merge --no-ff docs/your-change
git push origin dev/cmsg
~~~

发布前检查 `git branch -r --no-merged origin/dev/cmsg`。结果用于识别相关遗漏，
而非要求合入所有实验分支。生产构建使用干净、已推送的 `dev/cmsg` commit；
紧急发布其他命名分支需要明确授权。

## 目录与验证

| 路径 | 说明 |
| --- | --- |
| `web/default/` | 唯一维护的前端，局部规范见其 `AGENTS.md` |
| `cliproxyapi/` | 外层仓库跟踪的 CPA subtree，含独立 Go module |
| `ops/` | 余额刷新、渠道守卫、额度与运行辅助脚本 |
| `docs/`、`docs/proposals/` | 功能语义、运行手册与架构方案 |
| `pkg/billingexpr/expr.md` | 修改表达式计费前必读 |
| `AGENTS.md` | 项目契约与生产安全约束 |

按变更范围验证，详情见根目录及前端 `AGENTS.md`：

- 文档：Markdown 格式、相对链接、`git diff --check`。
- New API：相关包测试；后端发布前运行根目录 `go test ./...`。
- CPA：在 `cliproxyapi/` 中运行相关测试，CPA 发布前运行其 `go test ./...`。
- 前端：相关回归测试与 `bun run build:check`，按变更检查格式与 lint。
- 涉及并发、计费、数据库或流式转换时，补充对应回归验证。

## 双上游与 CPA subtree

唯一提交和推送仓库为 `Yuming12138/newapi-cmsg`。外层 New API 与 CPA 分别
参考以下上游；remote 名称是约定，首次使用先检查 `git remote -v`：

| Remote | 上游 | 范围 |
| --- | --- | --- |
| `upstream` | `https://github.com/QuantumNous/new-api.git` | New API 后端与可移植修复 |
| `cliproxy-upstream` | `https://github.com/router-for-me/CLIProxyAPI.git` | `cliproxyapi/` subtree |

需要同步且尚未配置相应 remote 时，再添加：

~~~text
git remote add upstream https://github.com/QuantumNous/new-api.git
git remote add cliproxy-upstream https://github.com/router-for-me/CLIProxyAPI.git
git config remote.cliproxy-upstream.tagOpt --no-tags
~~~

`cliproxyapi/` 内运行 Go 测试和构建，Git 提交与上游同步则在外层仓库执行。
该目录由外层直接跟踪，保持其没有独立 `.git`。

CPA 同步在独立分支完成，下面的 `main` 可替换为经过审查的 tag 或 commit。
命令保持单行，兼容 PowerShell：

~~~text
git switch dev/cmsg
git pull --ff-only origin dev/cmsg
git switch -c sync/cliproxy-vX.Y.Z
git fetch --no-tags cliproxy-upstream main
git subtree pull --prefix=cliproxyapi cliproxy-upstream main --squash -m "sync(cliproxy): update upstream to vX.Y.Z"
~~~

`cliproxyapi/` 自 2026-10-21 起为 pristine upstream v8（基线 `ce07d18`，
即 v8.0.37）：CMSG 定制已全部丢弃（ Mihomo 路由恢复、antigravity/gemini
预算防护、管理端点扩展、调度审计等，历史见 `dev/cmsg` 分支），集成暂在
`dev/cmsg-v8` 验证后合回 `dev/cmsg`。此后同步为纯上游跟进，正常不产生
冲突；审查上游 diff 与发布说明，在 `cliproxyapi/` 内完成 `go test ./...`
与生产二进制构建，再合回并记录上游 tag 和完整 commit。
New API 后端继续逐项审查和移植；前端修复手工移入 `web/default/`，不整体覆盖
CMSG 前端。历史同步记录可帮助定位既有取舍，下一轮仍须依据当前代码评估。

## Windows 本地构建

先核对本机 Go、Bun 与各模块要求，完成相关测试，再构建生产产物。
New API 通过 `go:embed` 嵌入 `web/default/dist`：**先完成前端构建，再编译
New API**。两步保持顺序，避免打包旧前端。

下面是 PowerShell 构建示例，只构建本地产物，不执行上传或重启。环境变量在
`finally` 中恢复；代码块中的路径为固定维护目录：

~~~powershell
$ErrorActionPreference = 'Stop'
$taskRepo = 'F:\WorkBuddy\2026-10-06-16-58-21\src\newapi-cmsg'
$taskSavedEnv = @{}
foreach ($taskName in @('CGO_ENABLED', 'GOOS', 'GOARCH', 'VITE_REACT_APP_VERSION')) {
    $taskSavedEnv[$taskName] = [Environment]::GetEnvironmentVariable($taskName, 'Process')
}

Push-Location $taskRepo
try {
    $taskBranch = (& git branch --show-current).Trim()
    if ($LASTEXITCODE -ne 0 -or $taskBranch -ne 'dev/cmsg') {
        throw 'Build from the integrated dev/cmsg branch'
    }
    $taskDirty = @(& git status --porcelain)
    if ($LASTEXITCODE -ne 0 -or $taskDirty.Count -gt 0) {
        throw 'Commit or separate pending changes before building'
    }
    $taskCommit = (& git rev-parse --short=12 HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot resolve commit' }
    $taskRelease = "new-api-$taskCommit-$(Get-Date -Format 'yyyyMMddHHmmss')"
    $taskOut = Join-Path $env:LOCALAPPDATA "CMSG\releases\$taskRelease"
    New-Item -ItemType Directory -Path $taskOut -ErrorAction Stop | Out-Null
    $taskBinary = Join-Path $taskOut 'new-api'
    $env:VITE_REACT_APP_VERSION = $taskCommit

    Push-Location 'web/default'
    try {
        & bun run build:check
        if ($LASTEXITCODE -ne 0) { throw 'Frontend validation/build failed' }
    } finally {
        Pop-Location
    }

    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $taskLdFlags = "-s -w -X github.com/QuantumNous/new-api/common.Version=$taskCommit -extldflags '-static'"
    & go build -buildvcs=false -ldflags $taskLdFlags -o $taskBinary .
    if ($LASTEXITCODE -ne 0) { throw 'Backend build failed' }
    & go version -m $taskBinary
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read binary metadata' }
    Get-Item -LiteralPath $taskBinary | Select-Object FullName, Length
    Get-FileHash -LiteralPath $taskBinary -Algorithm SHA256
} finally {
    foreach ($taskName in $taskSavedEnv.Keys) {
        [Environment]::SetEnvironmentVariable($taskName, $taskSavedEnv[$taskName], 'Process')
    }
    Pop-Location
}
~~~

上传前核验版本、commit、ELF 64-bit AMD64 架构、静态链接、大小和 SHA-256。
`go version -m` 可查看构建参数，架构与静态链接还需用 ELF 检查工具核实；
Linux 上的 `file` 可用于接收后的独立验证。

CPA 在 `cliproxyapi/` 模块按其版本信息约定构建 `./cmd/server`，同样使用
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`。针对 Alpine 运行镜像尤其要核验
静态链接：带有缺失动态加载器的 ELF 会报 `no such file or directory`，
即使二进制文件本身存在。

仓库内 `scripts/cmsg-release-new-api.sh` 是 Bash 自动发布脚本，包含上传、
重启和清理；`scripts/cmsg-rollback-new-api.sh` 会执行回滚切换。运行前读
脚本并核对参数，确认具备 Bash 与相应传输工具；本机 PowerShell 构建和
SSH 上传可按本节与下节单独执行。

## 公网发布与回滚

每次发布先只读核对：

- 当前容器及状态、Compose 文件与服务名、实际二进制挂载源和目标；
- 现役 release、版本和校验值，已有可回滚产物与 Compose 备份；
- 目标实例的数据角色、端口和所改功能的验证方式。

New API 常用 Compose 路径为 `/opt/new-api/docker-compose.prod.yml`，
二进制单文件只读挂载到 `/new-api`。CPA 使用自己的 Compose 与挂载，
两种产物单独发布；CPAHome 也应单独核实来源、版本与二进制兼容性。

发布步骤：

1. 将产物上传到新的不可变目录，例如
   `/opt/new-api/releases/new-api-<commit>-<timestamp>/new-api`。
   Windows 使用已配置的 `ssh.exe`/`scp.exe`，先核验 SSH 目标和权限；
   上传后比较本地与远端 SHA-256。
2. 保存 Compose 备份和旧 release 路径，确认目标是可执行的二进制文件。
   只替换现有二进制挂载源，保持 `/new-api:ro`，避免把目录挂载到可执行路径。
3. 用 `docker compose -f docker-compose.prod.yml config --quiet` 验证配置。
   保持输出只包含所需结构，避免展开带凭据的配置或读取 `.env`。
4. 说明重启影响后，仅重新创建目标 New API 服务。在 `/opt/new-api` 中执行
   `sudo -n docker compose -f docker-compose.prod.yml up -d --no-deps --force-recreate new-api`。
   CPA 发布使用其已核验的对应命令，数据库与 Redis 保持运行。
5. 验证容器 `healthy`、真实挂载、内网
   `http://127.0.0.1:3000/api/status` 与公网
   `https://api.cmsg666.xyz/api/status` 的预期版本，并查看脱敏启动日志。
   改动涉及转发、流式、余额或 UI 时，另测相应功能。
6. 验收后保留 **3 个 release 总计：当前版本和此前 2 个版本**。清理前核实
   所有运行挂载和回滚依赖，按精确目录操作；保留对应 Compose 备份，清理已
   验证无用的上传临时目录。保留策略适用于单项服务的 release，历史脚本或
   手册中的其他数量应在执行前核对。

回滚时恢复旧二进制挂载源或对应 Compose 备份，重新创建同一个服务，再运行
同样的健康、版本与功能检查。发布记录包含新旧 release、commit、SHA-256、
备份位置、验证结果和中断影响，凭据继续保存在受保护的运行配置中。

## cli2api 上游服务

cli2api 是独立于 New API 的兼容网关，供渠道 36（Advanced Custom，type 59）反代。
Compose 位于 `/opt/cli2api/deploy/docker-compose.yml`，容器 `qoder-api-proxy`，
仅监听 `127.0.0.1:3010`，数据在 `/opt/cli2api/data`。

镜像 tag 必须保持为 `:latest`：宿主更新器只执行 pull 和重建，**不会改写 Compose
里的 tag**。若把 tag 固定成某个版本号，更新器即使拉到新镜像，重建后仍运行旧版本，
健康检查发现版本不符会判定更新失败并自动回滚（表现为「更新失败并被回滚：健康检查
未达到 vX.Y.Z」）。同理，手动升级时也要先改 tag 或用更新器，不要依赖 `up -d`
自动跟随。升级步骤：

1. 核对目标版本与当前版本：`curl -s http://127.0.0.1:3010/health`；
2. 面板「立即更新」或 `sudo docker pull ghcr.io/caigee-cmd/cli2api:latest`；
3. 需要固定版本回滚时，临时改回具体 tag 并重建，验证后仍改回 `:latest`；
4. 验证 `/health` 版本、`/api/accounts` 结构与真实请求。

宿主更新器（可选，面板「立即更新」依赖它）由 `deploy/install-updater.sh` 安装：
二进制在 `/usr/local/bin/cli2api-updater`，systemd 单元 `cli2api-updater.service`，
socket 为 `/run/cli2api-updater/updater.sock`，状态文件
`/var/lib/cli2api-updater/status.json`。安装完成后需强制重建一次容器。
状态文件可能残留历史失败记录，以 `GET /api/system/update` 的实时返回为准。

网络：本机直连 GitHub 与 ghcr.io 均超时，下载必须走本机代理
`http://127.0.0.1:7897`（mihomo 容器映射端口）。代理需要配置两处：

- 更新器：`/etc/systemd/system/cli2api-updater.service.d/proxy.conf`；
- Docker daemon：`/etc/systemd/system/docker.service.d/http-proxy.conf`，
  因 `docker pull` 由 daemon 执行，容器级环境变量无效。该项**需要重启
  docker**，会同时重启全部容器（约 30 秒），重启后 mihomo 自动恢复、代理端口
  随之可用。验证方法是拉取一个不存在的 tag：返回 `not found` 说明已连通，
  超时说明代理未生效。

cli2api 的 `/api/*` 控制台接口需要控制台密钥，渠道自带的客户端密钥不具权限；
New API 侧通过 `CLI2API_CONSOLE_KEY_FILE` 注入。

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
3. `web/classic/` 与 theme 切换机制已于 2026-07-26 退役删除（确认生产无 `theme=classic` 用户后执行，`cleanup/retire-classic-frontend` 分支）。数据库中残留的 `theme` option 为惰性数据，无需清理。
4. 后端同步策略不变：继续逐项审查、手工移植上游后端提交。

关联暂缓项：上游 auth 重构（`31d70fca3` 后端部分：无状态 token、会话吊销、分布式强制下线，约 76 个后端文件，含 `controller/auth_session.go`、`model/user_session.go` 870 行等）作为独立安全项目排期移植，方案见 `docs/proposals/auth-stateless-session-port.md`。注意其上线会使全体已登录用户登出一次，需选窗口期；`172114422`（限流时保持登录态）依赖该重构，一并顺延。
