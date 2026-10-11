# CPA Antigravity 长上下文服务端保护

适用路径：New API 渠道 35 → `cliproxy-api` → Google Antigravity / Gemini。
本次修复只改 CPA，不要求客户端修改模型目录或自动压缩参数。

## 源码中确认的漏洞

1. 旧 `EnsureAntigravityBudget` 在请求小于 1,800,000 字节时直接返回。字节数不是 Google tokenizer 的 token 数，这个快速返回不能作为上下文上限保证。
2. 文本估算使用 ASCII 3 字节/token，既不是官方分词结果，也没有针对代码、补丁等输入提供足够余量。
3. 历史工具结果压缩排除末轮；只剩当前工具调用/结果时，旧裁剪不处理大结果。
4. 旧裁剪后的最终估算即使仍超过预算，也会继续发送；首条消息之外的大文本、固定输入和结构化工具结果额外字段可能漏过。

以上是可由源码/回归测试复现的漏洞。没有把未经官方分词或原始请求验证的某个字符/token 比例当成这次生产错误的精确成因。

## 当前策略

| 环节 | 行为 |
| --- | --- |
| 小请求快速路径 | JSON 小于 256,000 字节，且小于当前预算的一半时不扫描；普通小请求零堆分配 |
| 本地估算 | ASCII 2 字节/token，非 ASCII 1.5 token/rune；包括系统指令、tools、输出 schema、完整工具调用/结果 |
| 常规预算 | 750,000 **估算** token，低于 Google 报错中的 1,048,576 上限；不声称估算等同官方分词 |
| 历史工具结果 | 只在整体超过预算时截取头尾各约 1,000 UTF-8 字节，插入明确截断说明 |
| 历史剪枝 | 删除旧会话前缀/匹配完整的旧 call-response 对，保留当前用户目标和活动工具对 |
| 活动工具结果 | 历史处理后仍超限时保留头尾各约 8,000 UTF-8 字节；保留工具 name/id/调用参数/签名 |
| 超大普通文本 | 必要时对所有可裁剪大文本分配剩余预算，保留头尾与说明，不限于首轮第一个 part |
| 最终发送校验 | 在 payload 配置、schema 清洗和签名补全之后再次校验实际 HTTP 请求体；仍超限则本地明确返回 `context_length_exceeded` |
| 上游分词差异 | 仅 HTTP 400 且明确包含 input token count 超限错误时，按**实际已发送请求体**再降预算，最多重试一次；重试必须确实缩小，普通 400/429 不触发 |

系统/开发者指令、tool schema、函数调用参数与 signed/thought parts 不会被偷偷改写。
单靠这些不可改写内容就超过预算时，网关拒绝发送。图像/音视频 token 数仍是估算，不能保证所有多模态极端输入都能自动修复。

截断/删除历史不是模型生成的语义摘要，会丢失被截断的中间内容。模型看到显式说明后应按需重新读取文件或获取日志；客户端原始历史不会被改写。

## 性能与测试

历史压缩先改每个 part/turn，再一次性替换 contents；剪枝对每轮估算一次、选择一次后缀，避免在每个候选剪点上反复复制整份大请求。

在 Windows 工作目录的 `cliproxyapi` 中执行：

```powershell
go test ./internal/translator/gemini/common ./internal/runtime/executor/helps ./internal/runtime/executor ./internal/translator/gemini/openai/responses
go test ./...
go test -run '^$' -bench 'BenchmarkEnsureAntigravityBudget' -benchmem -benchtime=5x ./internal/translator/gemini/common
go build -o test-output.exe ./cmd/server
```

覆盖：root/request.contents、末轮工具结果、结构化 response、额外大字段、UTF-8、配对 ID/签名、1.62MB 旧快速路径漏网、多个超大 text parts、不可裁剪输入本地拒绝、stream/non-stream/Pro 内部 SSE 路径的超限恢复、只重试一次、普通 400 不重试。

2026-10-12 验证时的环境限制：

- Windows 全量测试只有既有 `TestInstallPluginFromStoreInstallsDirectArtifact` 失败，其 fixture 仅注册 linux/amd64 和 darwin/arm64，未注册 windows/amd64；跳过这个测试后其余全量测试通过。未修改插件实现或该测试。
- Windows `CGO_ENABLED=0` 且未发现 C 编译器，故 `-race` 不能运行；普通测试及本地 server 构建已运行。
- 本机 i7-14700F 基准（仅作参考，不是生产延迟）：小请求 0 B/op、0 allocs/op；100 轮、约 3.2MB 工具历史约 29ms/次、22MB 分配。未在低配生产机做大上下文压力测试。

## 发布边界

在 `dev/cmsg` 集成后，**本地 Windows** 交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`。
上传独立 release，校验 SHA-256 和 ELF 静态链接信息；保留旧二进制回滚，再切换 `/opt/cliproxyapi/cli-proxy-api`，仅重启 `cliproxy-api`。
不编译生产服务器、不修改 Home/账号凭据/New API 分组或计费、不更改客户端目录。
重启前告知活动流可能断开，之后确认健康状态、内嵌 commit、运行中二进制校验和及轻量请求/日志。
