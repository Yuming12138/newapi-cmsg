# DeepSeek 历史思考续接与 OpenCodex 调研

核对日期：2026-10-08。开发目录以本次用户指定的 Windows
`F:\WorkBuddy\2026-10-06-16-58-21\src\newapi-cmsg` 为准。

## 结论

存在可以降低 `reasoning_text must be passed back` 400 的公开兼容方案，
但“请求能继续”和“原始思考完整恢复”是不同的验证目标。
没有查到可通用地无损转换 GPT 加密思考到 DeepSeek 明文思考的方案。
GPT 的 reasoning summary 也不是原始思考。

同一 DeepSeek 会话应该优先保存并恢复实际返回的明文内容；跨 GPT/Kimi
历史如果已经缺少原文，需要明确的兼容策略，不能声称无损。

## 本次 New API 改动的边界

- 只修改 DeepSeek 原生 Responses 适配，不修改渠道、计费、fallback 或思考强度。
- 保存实际上游 reasoning content；不从 summary/encrypted_content 生成原文。
- 接收客户端把真实明文重标为 `text` 的情况，规范成 `reasoning_text`，保持文字逐字不变。
- 按 reasoning item ID、assistant message ID 或 function/custom call ID 恢复丢失内容。
- 缓存作用域包含认证用户、token、渠道、上游 URL/模型/凭据摘要和客户端线程标识。
  优先使用 Codex 官方客户端发送的 `thread-id`，兼容 `x-codex-thread-id`、`session-id` 和 `session_id`；不以 prompt_cache_key 授权。
  没有线程标识时不做关联缓存恢复，避免不同会话复用 `call_1` 导致串历史。
- 内存 LRU 最多 32 条，每条序列化记录最多 1 MiB；Redis TTL 和记录硬过期时间为 24 小时。
  Redis 读写采用批量操作及 500 ms 超时，异常不删除已有调用者历史，也不记录原文。
- 流式输出在完整 reasoning/tool item 被转发前记住；支持 `reasoning_text.done` 中的完整文本。
  DSML 转成 custom tool 后，使用实际交给客户端的 call ID 建立关联。
- `previous_response_id` 继续使用已有数据库历史回放，不新增数据库字段。
- 无匹配缓存的外来摘要/加密内容保持原状；本次不启用占位或自动降级策略。

## 公开方案与证据强度

| 来源 | 处理办法 | 已有证据 | 不足 |
| --- | --- | --- | --- |
| [Pydantic AI PR 5842](https://github.com/pydantic/pydantic-ai/pull/5842) | 给框架合成、没有模型思考的工具 assistant turn 补 `reasoning_content: ""` | 2026-06-11 合入；PR 记录了真实 DeepSeek API VCR 测试 | Chat Completions 合成历史场景，不能直接证明原生 Responses 或任意 GPT/Kimi 历史有效 |
| [Codex issue 42249](https://github.com/openai/codex/issues/42249) | 缺明文的 Responses reasoning item 补 `(thinking unavailable)` | 报告人称本地代理让自动化继续完成 | 用户实测报告，尚未证明无质量损失；也记录了 `content.type: text` 的重标问题 |
| [New API PR 6395](https://github.com/QuantumNous/new-api/pull/6395) | Responses→Chat 保留 reasoning/tool history | 有转换层回归测试；核对时 closed、未合入 | 包含 summary 映射；不是原生 DeepSeek Responses 修复，也不能恢复外国密文 |
| [Shelley PR 290](https://github.com/boldsoftware/shelley/pull/290) | 将空格占位限定到 DeepSeek | 作者报告占位导致可见空 thinking/工具标记错位的观察 | 核对时未合入；作者没有 DeepSeek 账号做实测 |
| [OpenCodex issue 5421](https://github.com/lidge-jun/opencodex/issues/5421) / [PR 5825](https://github.com/lidge-jun/opencodex/pull/5825) | 原生 Responses 优先保留 reasoning_text，缺失时补 summary_text，再缺失时补一个空格 | PR 已于 2026-09-25 合入；当前正式 release 源码和回归测试含此分支 | 无损性并未验证；通用 `text` 类型可能进入补位分支 |

## OpenCodex 当前实现

核对的是 [v2.80.0](https://github.com/lidge-jun/opencodex/releases/tag/v2.80.0)，
release API 指向的源码 commit：
`250f17afd8ff44c93c620d87c1f346ef56f64fb4`。

它是原版 Codex 前面的本地代理，不是替换 Codex 内核的分支：

```text
原版 Codex App/CLI → 本机 OpenCodex → DeepSeek
```

主要代码：

1. [DeepSeek registry](https://github.com/lidge-jun/opencodex/blob/250f17afd8ff44c93c620d87c1f346ef56f64fb4/src/providers/registry/entries-core.ts#L1199)
   声明 `/responses`、stateless、明文 reasoning 保留和工具结果相邻等能力。
2. [原生 Responses 清洗](https://github.com/lidge-jun/opencodex/blob/250f17afd8ff44c93c620d87c1f346ef56f64fb4/src/adapters/openai-responses/reasoning.ts#L14)
   `requirePlaintextReasoning` 开启时，只认非空 `reasoning_text`；缺失时从 `summary_text`
   构造 content，完全没有摘要时填 `" "`。
3. [原生序列化调用点](https://github.com/lidge-jun/opencodex/blob/250f17afd8ff44c93c620d87c1f346ef56f64fb4/src/adapters/openai-responses/passthrough.ts#L498)
   传入保存明文、丢弃外国密文、要求明文等选项。
4. [Chat 工具历史回放](https://github.com/lidge-jun/opencodex/blob/250f17afd8ff44c93c620d87c1f346ef56f64fb4/src/adapters/openai-chat/messages.ts#L256)
   优先用已有 thinking，其次按工具 call ID 读缓存，仍没有则补空格。
5. [Chat 回放缓存](https://github.com/lidge-jun/opencodex/blob/250f17afd8ff44c93c620d87c1f346ef56f64fb4/src/responses/reasoning-replay-cache.ts#L29)
   64 条 / 256 KiB / 1 小时，只存内存，并绑定线程、提供方、endpoint、adapter、模型与凭据。
   不应据此说原生 Responses 路由也必然使用这条 Chat 缓存恢复路径。
6. [入口到出口测试](https://github.com/lidge-jun/opencodex/blob/250f17afd8ff44c93c620d87c1f346ef56f64fb4/tests/providers/deepseek-inbound-wire.test.ts#L114)
   Codex Responses 输入选择 DeepSeek 原生 `/responses`；Chat/Anthropic 输入选择 Chat。
   其中 fetch 是 mock，不是本次我们做的真实上游测试。

### 本机隔离验证

从此 commit 取原样 sanitizer 函数，补上依赖常量/对象类型判断后，使用 Bun
执行六个无网络、无凭据的序列化断言，全部通过：

1. 已有真正的 `reasoning_text` 按字节保留。
2. 仅有摘要时，摘要被重新放入 `reasoning_text`。
3. 没有可用摘要时填一个空格。
4. 原文如果标为 `text` 而不是 `reasoning_text`，没有摘要时也变成空格。
5. 未启用特定提供方策略时不填占位。
6. 选项允许移除外国 reasoning ID 与 output-only status。

没有安装 OpenCodex，没有修改本地 Codex 配置/密钥，没有调用付费模型，也没有验证其
完整 App 工作流。正式使用前需要真实的多轮工具续接灰度，不能用这些断言替代。
第 4 项只验证此 sanitizer 的边界，不等于已经证明完整请求管线一定缺少其它类型规范化。

## Codex 客户端线程标识（2026-10-08 已验证）

核对 openai/codex 当前 main 分支源码（临时浅克隆，未安装、未改本地配置）：

1. `codex-rs/codex-api/src/requests/headers.rs` 的 `build_session_headers` 对每个
   Responses 请求发送 `session-id` 与 `thread-id` 两个头。
2. `codex-rs/core/src/client.rs` 中 `thread-id` 恒为会话 UUID；`session-id` 在普通线程取
   `prompt_cache_key`（可被用户配置的 prompt_cache_key 覆盖，多个会话可能相同），在子代理线程取会话 UUID。
   因此 New API 侧按 `thread-id` 优先、`session-id` 兜底的顺序识别线程。
3. `codex-rs/codex-api/src/endpoint/responses.rs` 确认 HTTP POST `/responses` 路径同样附带这两个头，
   不限于 websocket 传输。

## 对 CMSG 的建议

优先把可借鉴的小范围能力放到 New API 中，使现有用户不需要新增本地常驻代理。
第一阶段只保护真实 DeepSeek 原文与会话/渠道隔离。

如果本地 OpenCodex 连接的是 New API 的混合 `asxs` 分组，而不是直接连接 DeepSeek，
New API 内部 GPT/Kimi→DeepSeek fallback 对客户端代理可能不可见；它无法仅凭同一个
endpoint/模型名知道本次实际提供方。不能据直连 DeepSeek 的效果推断这种双代理链路已修好。

跨模型可以另设显式开关（例如严格 / 可用性兼容）：

- 严格：无原始明文时不冒充恢复；建议新会话或保留原提供方。
- 可用性兼容：优先实际原文，其次本方缓存，最后才采用经真实上游验证的摘要/占位策略；
  必须把“原文缺失、正在兼容续接”记录为可观察事件，不宣称无损。
- 若用户选择关闭 thinking 或重新构建新上下文，这是不同的策略，需单独同意，不能静默切换。

试运行矩阵应包括：全新 DeepSeek、多轮工具、并行工具、流式中断、GPT→DeepSeek、
Kimi→DeepSeek、返回原提供方、同账号不同线程复用工具 ID，以及长历史/缓存过期。

官方参考：[DeepSeek Responses](https://api-docs.deepseek.com/guides/responses_api)、
[DeepSeek thinking mode](https://api-docs.deepseek.com/guides/thinking_mode)、
[OpenAI reasoning](https://developers.openai.com/api/docs/guides/reasoning)。
OpenAI 官方说明 reasoning summaries 不是 raw reasoning，持久化的 reasoning 是 opaque，
即使模型家族之间也有兼容边界，不能据“API 形状相同”推断密文可跨提供方转换。
