# 放开个人限额后的共享余额池

`user_quota_guard_setting.unlocked_quota_source=daily_quota_pool` 使用
`service.GetDailyQuotaPoolSnapshot` 的 `remaining_usd`。现有时段规则不变：

- 00:00–17:00：现有个人基础额度、逐用户覆盖值和追加审批。
- 17:00–24:00：共享池当前余额；现有任务每分钟同步用户 `quota`。

## 来源和单位

1. **ASXS**：继续使用账号订阅汇总产生的额度快照；共享渠道只计一次。
2. **原有 CPA 每日预算**：保留现有每日预算、储备和临时放开规则。
3. **Gemini CPA**：对启用渠道中的 Google 账号按哈希身份去重，选择该账号
   最新快照。模型别名共享同一额度，不能逐模型相加；独立 Claude/GPT-OSS
   额度不计入 Gemini。剩余美元估算为各可用账号
   `remaining_percent × USDPerPercent[tier]` 之和。
4. **DeepSeek**：直接采用已同步的 `Channel.Balance`。同步任务已将官方 CNY
   钱包除以站点汇率转为 USD，此处不再除一次。同一 key 的重复渠道选择
   最新快照。上游不提供跨不同 key 的钱包 ID，因此同一钱包的不同 key
   不应配置成独立预算来源。

停用渠道、不可用账号和未成功同步的钱包不贡献余额。缺失额度、未知
Google 套餐或缺失折算系数显示部分未同步，并排除未知金额。Gemini
继承的 Codex 日预算元数据不再作为另一笔余额加入。

## Gemini 实测折算系数

配置项：`gemini_quota_estimate_setting.usd_per_percent`，JSON 对象按套餐 ID
配置“一个百分点相当于多少站内 USD”。默认 `g1-pro-tier` 为 `0.079`。
没有测试依据的套餐没有默认估值。

2026-10-14 用同一 Google 账号的管理 API `auth_index` 固定凭据，查询
`loadCodeAssist` / `fetchAvailableModels`，再通过 `generateContent` 请求
`gemini-3.8-flash-tiered`。测试内容是合成文本，不包含用户数据。两账号
均为 `g1-pro-tier`，测试期间重置时间没有改变。

按当前站内模型价格：输入 $0.30/百万 token，输出（含思考 token）
$1.20/百万 token。不能用请求的 GPT 别名价格反推，也不能把订阅月费
当作一轮满额 API 价值。

初始累计小样本：

| 账号 | 输入 token | 输出及思考 token | 折算请求费用 | 百分点变化 | USD/百分点 |
| --- | ---: | ---: | ---: | ---: | ---: |
| A | 60,581 | 756 | $0.0190815 | 0.24134 | 约 $0.07906 |
| B | 40,319 | 504 | $0.0127005 | 0.16064 | 约 $0.07906 |

百分比接口会返回延迟/回跳读数。额外的大输入样本反推值约为
$0.053–$0.077/百分点，说明此转换是**当前模型价格下的经验估算**，
不是官方现金余额或所有模型/缓存比例的固定兑换关系。默认值按初始
两账号一致样本取约 $0.079；运营者可以通过上述配置调整。若用新的
模型、套餐或价格，应重新测量，避免宣称精确计价。

不要用单次旧读数与新读数相减。应保留同一账号、同一重置窗口的
累计 token 用量，确认额度变化已反映，并排除并发消费和恢复额度。
管理 key、渠道 key 和 OAuth token 不应写入日志、文档或提交。

## 展示和验证

- `/api/status.daily_quota_pool.group_breakdown` 返回每种来源的 USD 金额；
  Gemini 为 `gemini_cpa_subscription_estimate`，带 `estimated=true`。
- `/api/dashboard/provider-balances` 向已登录普通用户和管理员返回同一
  池快照。首页新增“总可用余额”及 Gemini 美元估算项，DeepSeek 官方
  钱包仍显示人民币。
- 可用池余额在白天也可查看，白天个人余额仍受原有限制。
- 后端测试覆盖重叠账号去重、停用渠道、未知/失败额度、CNY 不重复
  换算、真实 GORM 查询和限制/放开两种用户政策。

生产发布从最新 `dev/cmsg` 构建，保留最近三份程序版本与 Compose
快照，保留数据库备份。不要为验证晚上规则临时修改生产时间或额度。
