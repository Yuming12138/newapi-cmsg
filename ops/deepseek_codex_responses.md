# DeepSeek 原生 Responses 与 Codex 工具续接

## 2026-10-14 修复

台式机 Codex `0.162.0-alpha.17.2` 的新会话
`01a135bc-204a-7bd0-8fb5-47a3da071c9a` 在第二次请求报错：

```text
The `reasoning_text` in the thinking mode must be passed back to the API.
```

渠道 11 实际将 `gpt-6.1-sol` 映射到 `deepseek-flash`，使用原生
`/responses`，请求保持 `reasoning.effort=max`。上游请求 ID 为
`979c84d6-6ad7-4556-a8b0-530cd3c5cd2b`；查 New API 日志应同时按
错误内容或线程标识检索，不能认为上游 ID 等于本站的 `logs.request_id`。

### 工具结果与助手说明的顺序

Codex 可能把同一助手输出中的工具调用先交给运行器，然后保存调用前说明：

```text
reasoning? → custom_tool_call → assistant preamble → custom_tool_call_output
```

旧的相邻配对逻辑把结果提前，但把说明留在末尾，生成：

```text
reasoning? → function_call → function_call_output → assistant preamble
```

真实 DeepSeek API 对照测试确认：即便原始 `reasoning_text` 完整保留，最后
这个助手说明仍会触发 400；同样内容改成下面的顺序则成功：

```text
reasoning? → assistant preamble → function_call → function_call_output
```

修复把调用与结果之间的调用前说明移到调用之前，并保留调用 ID、说明内容和
思考原文。不跨用户消息、新思考块或已取得的工具结果搬动说明。

### 同轮并行调用不能拆成多个助手轮次

台式机旧对话 `01a01ffc-a35d-74b0-a2af-f129717c48ed` 于同日再次报错，
上游请求 ID 为 `b93ca167-a5da-4289-a170-2a842300d841`。生产日志确认
此次使用用户 2 的 `desktop` 令牌、渠道 11；之前管理员 `admin` 令牌的
鹈鹕测试成功不代表旧对话中的所有工具组合已被覆盖。

成功响应含完整原始思考、助手说明及两个并行调用，但旧配对逻辑把历史改为：

```text
reasoning → assistant preamble → call 1 → result 1 → call 2 → result 2
```

直接重放该响应的原始思考及两个调用，真实上游仍返回相同 400；保持以下
原生顺序则返回 200：

```text
reasoning → assistant preamble → call 1 → call 2 → result 1 → result 2
```

修复保留同轮整个调用批次在结果之前，按 `call_id` 配对并保留结果完成顺序；
不合并已经被工具结果、新思考或用户消息分隔的调用轮次。没有关闭思考、
伪造思考或重复思考来绕过上游验证。回归覆盖两个及三个并行调用、逆序结果、
说明搬动、连续工具轮次和重复规范化。

### 命名空间中的动态工具

新版 Codex 的 `input.additional_tools.tools` 包含 `namespace`，例如
`functions.exec`、`functions.wait`。旧转换器跳过 namespace，实际发给上游的
工具列表可能为空，模型可能只输出 DSML 文本或长时间思考，无法正常创建文件。

原生 DeepSeek 请求现在将这些声明提升并展开为函数名，例如 `functions_exec`。
返回流中的调用恢复为 Codex 名称：自定义工具为 `exec`，普通函数为
`name=wait, namespace=functions`。下一轮历史再次转换为上游的函数名称，
`call_id` 保持不变。完整响应和流式 item 都执行同样的恢复。

## 2026-10-15 修复：工具轮次缺少 reasoning_text 的 400

用户报告「上一个对话是 gemini，下一个对话是 deepseek」时报错：

```text
The `reasoning_text` in the thinking mode must be passed back to the API.
```

渠道 11 将 `gpt-5.6-sol` 映射到 `deepseek-flash`（白名单模型，走原生
`/responses`）。用本地 dump 代理（渠道 base_url 临时指向透传代理，抓取
New API 实际上游请求）加真实上游对照实验确认：**带 `tools` 且 thinking
开启时，DeepSeek 要求每个助手轮次都回传 reasoning_text**，以下形态全部
返回 400（`deepseek-flash` 与 `deepseek-v4-pro` 均复现）：

| 历史形态 | 结果 |
| --- | --- |
| `… user → function_call → function_call_output`（工具轮无 reasoning） | 400 |
| `… function_call → function_call_output → assistant message`（结果后文本轮无 reasoning） | 400 |
| `… function_call → function_call_output → function_call`（结果后新工具轮无 reasoning） | 400 |
| `user → assistant message`（普通文本轮无 reasoning） | 200 |
| `… reasoning → function_call → function_call_output`（工具轮有 reasoning） | 200 |
| `… function_call → function_call_output → 占位 reasoning → assistant message` | 200 |

`normalizeForeignResponsesReasoning` 只转换已存在的 reasoning item，不会
为缺失的轮次合成内容；gemini-CPA 的工具轮经常不带 reasoning item，客户端
原样回传即触发上游 400。

修复新增 `backfillMissingNativeReasoning`：对每个工具调用批次（其前无
reasoning 时）和工具结果后的助手消息（其前无 reasoning 时）插入占位
reasoning item（`reasoning_text` 为单个空格）。占位内容不满足
`hasNativeReasoningText`（TrimSpace 后为空），不会写入明文缓存；该函数在
会话存储之后运行，占位不会进入 `previous_response_id` 回放历史。

## 验证

```bash
go test ./relay/channel/deepseek ./relay/channel/openai ./relay ./service/relayconvert ./dto
```

覆盖调用前说明、原始思考恢复、并行工具、边界隔离、重复规范化、动态与顶层
命名空间、原生调用及流式返回名称恢复。真实上游还验证了相同文本不同排序的
成功与失败对照。

最终验收必须使用台式机上的 Codex、指定 `deepseek` 分组密钥及原始
`prompt.txt`，实际生成含鹈鹕骑自行车 SVG 动画的 HTML 文件，并核对生产使用
日志的渠道、上游模型、计费币种和完整流结束。只有文本成功或 CLI 返回零不足以
证明完成；修复前的鹈鹕任务曾返回零但没有生成任何文件。

密钥通过独立测试环境读取，不打印、不写入仓库或日志。隔离运行所用的临时
凭据在验收后删除，用户现有 Codex 配置与认证文件不改动。

## 2026-10-19 跨渠道续接（deepseek → gpt）

Codex 线程本身保持同一个 `previous_response_id`。该 ID 是 DeepSeek 原生
Responses 返回的 `response.ID`，同时也是 `responses_chat_sessions` 的主键。
用户把同一线程手动切到 GPT 渠道时，请求带着这个 ID 落到 OpenAI 兼容上游：

```text
stream disconnected before completion:
The upstream could not validate encrypted continuation context.
Rebuild the conversation context in the client.
```

上游既解析不了这个 ID，也校验不了与之绑定的 reasoning 密文，所以拒绝整轮。
客户端（Codex）无法按提示「自行重建上下文」，重建只能由中转侧完成。

修复分两层：

1. `service.ReplayForeignResponsesSession`（`relay/channel/openai/adaptor.go`
   在 `ConvertOpenAIResponsesRequest` 调用）：若 `previous_response_id` 命中
   本地会话表且属于当前用户，就把存储的 transcript 与新输入合并成完整
   `input`，清空 `previous_response_id` 与 `instructions`（原始指令已是
   transcript 的首个 system item）。这样切到 GPT 就是一次无状态请求。
2. 合并时丢弃全部 `reasoning` item（`buildForeignResponsesTranscript`）：
   密文属于原上游，新上游无法校验，且可见的 message/tool 调用足以续接。
   另加 `DropForeignReasoningItems` 兜底，处理客户端没有带
   `previous_response_id`、只在 `input` 内联回放历史的情况：GPT 系列模型下
   删除 `content[].type == "reasoning_text"`（DeepSeek 专有形制）的 reasoning
   item。

同时保留 `c3c6da9` 的亲和策略：`codex/claude cli trace` 这类 skip-retry 会话
规则只在 `upstream_timeout` / `upstream_unavailable` 时保留亲和，避免**非预期
逃逸**；用户主动切换模型/分组不受影响，走上面的重放路径。

已知边界：GPT 侧产生的 `response.ID` 不入会话表，若同一个 GPT 线程再次被
路由到另一把 CPA 密钥/渠道，仍可能触发同样的校验错误；需要时再补
「OpenAI 侧响应入库 + 重放」。

## 2026-10-21 Responses 会话增量存储

旧格式把同一会话的完整 transcript 快照在每个 `response.ID` 和 canonical
`history_*` 行各写一份。长会话会同时放大 PostgreSQL、WAL 和 Docker 日志
占用。增量格式改为：

| 表 | 内容 |
| --- | --- |
| `responses_chat_turns` | 每轮新增 items、父 response ID、待处理工具调用及完整性哈希 |
| `responses_chat_checkpoints` | 第 1 轮、每 20 轮和分支点的完整状态 |
| `responses_chat_heads` | 会话最新 response ID 和清理时间水位 |

恢复时先读一个检查点，再按 `conversation_id + depth` 批量取最多 24 轮增量，
在内存中校验消息数量、完整 items 哈希和 pending 工具调用哈希。单会话重建
通过 singleflight 合并相同读取，并受全局并发槽限制。

### 运行模式与回滚

`RESPONSES_SESSION_STORAGE_MODE` 控制写入路径：

| 值 | 行为 |
| --- | --- |
| `legacy` | 只写旧快照，作为紧急回滚模式 |
| `shadow` | 先写旧快照，再写增量表并重建比对；默认值 |
| `incremental` | 只写增量表，读取失败时回退旧快照 |

`shadow` 下增量写入或重建差异只计数和记录日志，旧快照仍是读写的正确性来源；
不会因为影子校验失败让请求报错。读取始终优先返回旧快照。`incremental` 下
新线程写入后立即进入内存 LRU；仍需从旧快照续接的历史会自动回退并缓存。

回滚时把模式改回 `shadow` 或 `legacy` 并重建 New API。不要删除新表或 Blob：
`shadow` 切回 `incremental` 需要这些记录；旧快照仍保留。

### 环境变量

| 变量 | 生产值 | 说明 |
| --- | --- | --- |
| `RESPONSES_SESSION_STORAGE_MODE` | `shadow` | 首次灰度只写；验收后改 `incremental` |
| `RESPONSES_SESSION_RETENTION_SECONDS` | `604800` | 线程保留 7 天，按 head 水位整线程清理 |
| `RESPONSES_SESSION_REBUILD_CONCURRENCY` | `2` | 全局重建并发，限制在 1–4 |
| `RESPONSES_SESSION_MAX_BYTES` | `134217728` | 单 items 或 pending 解压后上限，限制在 1 MiB–1 GiB |
| `RESPONSES_SESSION_CACHE_BYTES` | `50331648` | 进程内 LRU 总字节上限，默认 48 MiB |
| `RESPONSES_SESSION_BLOB_DIR` | `/data/responses_session_blobs` | 与 `/data` 一起持久化 |
| `RESPONSES_SESSION_BLOB_THRESHOLD_BYTES` | `262144` | 大于 256 KiB 的 JSON 字符串外置 |
| `RESPONSES_SESSION_BLOB_RETENTION_SECONDS` | `2592000` | 未访问 Blob 保留 30 天，默认值 |

### Blob 与清理

大字符串递归替换为带原长度和 SHA-256 的 `cmsg_blob_v1` 标记；文件按内容寻址
去重，权限 `0600`，读取时同时校验长度和哈希。文件创建失败时保留内联内容并
增加 `blob_errors`，不影响请求；数据库事务回滚可能留下未被引用的文件，由
30 天过期回收处理。

清理任务以 `responses_chat_heads.updated_at` 选择过期线程，在同一事务内删除
turn、checkpoint、旧快照和 head，并持有该会话的保存锁。仍活跃的线程不会因
旧 canonical 行时间较早而被误删。LRU 条目达到同样的 7 天保留期后自动失效。

### 灰度验收

1. 生产首次只设置 `RESPONSES_SESSION_STORAGE_MODE=shadow`。
2. 等待清理任务至少运行一轮，检查脱敏日志中的
   `shadow=N/N cache_hit_rate=... incremental_reads=... legacy_fallbacks=...`、
   `shadow_read_errors=0`、`shadow_write_errors=0`、`rebuild_active=x/2`。
3. 确认新表存在且正常增长；大附件对应 Blob 文件存在，数据库中没有完整原文。
4. 观察请求错误率、CPU、内存、数据库增长和恢复耗时。
5. 稳定后只改 `RESPONSES_SESSION_STORAGE_MODE=incremental` 并重建 New API，
   继续观察 `legacy_fallbacks`、`blob_errors` 和请求错误率。

测量增长时同时记录 1 小时前后的 PostgreSQL 数据目录、New API `/data` 和
容器 writable layer。不要把一次镜像构建或镜像拉取变化误判为会话表增长。
