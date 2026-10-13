# 新用户默认归入 cmsg

从本次发布起，新注册用户的身份分组默认为 `cmsg`。

## 覆盖范围

- 用户名/密码注册（包括注册口令开启时的注册）：由注册控制器指定 `model.DefaultUserGroup`，忽略客户端提交的身份分组。
- 第三方注册：`User.Insert` 与 `User.InsertWithTx` 共用的 `prepareNewUserFields` 为未指定或全空白的分组填入 `cmsg`，覆盖内置和自定义 OAuth 注册入口。
- 管理员创建用户：未指定分组时使用 `cmsg`；管理员明确指定的分组仍保留。

## 不改变的行为

- 不批量修改已有用户；已有 OAuth 用户再次登录不会重新分组。
- 不修改 `NormalizeUserIdentityGroup` 的空值兼容规则和数据库字段默认值，避免影响历史账号、缓存读取或触发额外 schema 迁移。
- 不改变管理员角色、注册赠送额度、渠道分组或 API Token 的路由分组。这里的 `cmsg` 是用户身份分组，不等同于强制把 Token 的路由分组设置为 `cmsg`。

## 回归检查

```powershell
go test ./model ./controller -run 'Test(Register|AdminCreateUserDefaults|OAuthRegistration|InsertDefaults|InsertWithTxDefaults|UserCreationPreserves)' -count=5
```

测试覆盖密码注册不可覆盖分组、注册口令、管理员显式分组、空白分组、普通/事务创建、GitHub/Discord/OIDC/LinuxDO/自定义 OAuth，以及已有用户不重新分组。

Windows 全量检查发现三个与本次修改无关的既有失败：`TestASXSAutoDailyResetControlRoundTrip`（Unix 0600 权限断言）、`TestASXSAccountTokenRotationPreservesPrivateFile`（目录 fsync）和 `TestObserveChannelAffinityUsageCacheByRelayFormat_UnsupportedModeKeepsEmpty`（统计测试之间的键碰撞）。使用 Go overlay 恢复修改前的 `model/user.go` 后仍可复现；排除这三个测试后其余 New API 全量测试通过。本次未修改这些功能或测试。
