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
