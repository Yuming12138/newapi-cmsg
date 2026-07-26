# 移植上游无状态会话与会话控制（31d70fca3 后端部分）

状态：已排期，未开工（2026-07-26 决定）
上游基线：`QuantumNous/new-api` `31d70fca3`（2026-07-20，#6329）及其后续修复 `172114422`

## 目标

用上游的无状态 token + 服务端会话记录方案替换现有 session cookie 方案，获得：

- **会话吊销**：用户/管理员可查看并踢掉任意登录会话（`GET/DELETE /api/user/self/sessions`、`revoke-others`）。
- **分布式强制下线**：`user_auth_cache` 的 auth version + fence 机制，改密/封禁后所有实例上的缓存凭据立即失效。
- **刷新轮转**：refresh token 按次轮转并带宽限期（`RotateUserSessionRefresh`），降低凭据泄露窗口。
- **CSRF 加固**：携带 cookie 的 auth 端点加 `SessionCookieOriginGuard`。

明确不做：不跟随同一提交的前端搬迁与 classic 删除（见 DEVELOPMENT.md 前端同步策略）。

## 上游架构速览

| 组件 | 内容 |
|------|------|
| `model/user_session.go`（870 行） | UserSession 表 + Redis 缓存、活跃会话计数/列表、deny fence、刷新轮转 |
| `model/user_auth_cache.go` | 每用户 auth version，`BumpUserAuthVersion` 触发全实例凭据失效 |
| `model/auth_flow.go`、`external_identity_claim.go` | OAuth 流程 state token 化 |
| `controller/auth_session.go`（189 行） | `/auth/refresh`、`/auth/logout`、会话管理端点 |
| `middleware/auth.go` 重写 | `authenticateDashboardRequest` / `classifyDashboardCredential`：bearer + 会话双通道 |
| `middleware/auth_origin.go` | Origin 守卫 |
| 前端 | auth-store 负责 token 刷新循环（在上游新 `web/` 布局中，CMSG 需自行适配） |

后端共约 76 个文件，其中约半数为新增测试。

## 分批方案

**Phase 0 — 差异盘点（无行为变化）**
盘点 CMSG 在 auth 路径上的既有定制与上游的冲突面：
`security/session-cookie-and-user-hardening-cmsg` 批次改过的 `common/session_cookie.go`、
passkey 流程、CMSG 实际启用的 OAuth 提供方（GitHub/微信/Telegram 哪些在用）、
`0565e6267`（仅 401 视为会话过期）是否已移植。产出冲突清单。

**Phase 1 — 数据层（可先行合并，无行为变化）**
移植 `UserSession` 表 + 迁移、`user_auth_cache.go`、`auth_flow.go`、
`external_identity_claim.go` 及配套测试。此阶段只建表建缓存，不接管认证。

**Phase 2 — 后端切换（一次性切换，选窗口期）**
移植 middleware/auth 重写、`controller/auth_session.go`、`auth_origin.go`、路由变更。
上游为直接替换而非双轨兼容，**上线即全体已登录用户登出一次**。

**Phase 3 — 前端适配（CMSG `web/default` 自行实现）**
登录/登出流对接新端点、token 刷新循环、401 处理。会话管理页（查看/踢会话）可后置。
参考上游 `web/src/stores/auth-store.ts`、`web/src/features/auth/api.ts`（路径映射见 DEVELOPMENT.md）。

**Phase 4 — 跟进项**
移植 `172114422`（限流或刷新失败时保持登录态；依赖本重构）。
观察期后清理旧 session cookie 遗留代码。

## 风险与回滚

- **强制登出**：Phase 2 上线时机避开使用高峰；提前通知实验室用户。
- **OAuth 回归**：CMSG 若启用微信/Telegram 之外的流程需逐一回归；未启用的提供方可降低测试面。
- **与 CMSG 安全批次的冲突**：`session_cookie.go` 双方都改过，Phase 0 必须先出冲突清单再动手。
- **回滚**：Phase 2 前打 release 快照；新方案数据层（Phase 1）与旧方案共存，回滚只需回退二进制，不需要回滚数据库（UserSession 表闲置无害）。

## 验证要求

- `go test ./...` 全绿（上游自带 auth_flow/auth_session/user_session 等约 15 个新测试文件）。
- 手工回归：密码登录、登出、改密后其他会话失效、管理员封禁立即生效、启用中的 OAuth 提供方各走一遍。
- 生产验证：登录后跨实例请求（如有多实例）、Redis 故障降级路径。
