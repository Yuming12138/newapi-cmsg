# CMSG Codex 雷达

## 数据源与展示范围

仪表盘读取 Codex Radar 官网的公开 RadarBench v2 汇总数据，不是在本站实时测试模型，也不代表本站渠道实际能力或扣费价格。

只展示 `gpt-6-astra`、`gpt-6.1-sol`、`gpt-6-sol`、`gpt-5.6-sol`。每组包括 `ultra`、`max`、`xhigh`、`high`、`medium`、`low` 六档，共 24 个点位。Luna、Terra 和其他旧模型不在此面板展示。

数据入口：

```text
GET https://codexradar.com/api/radar-bench-score?model={model}&effort={effort}&view=summary
User-Agent: new-api-cmsg codex-radar overview
X-Radar-Bench-Selection: user-selected-v1
```

不再读取 `/data/intelligence-efficiency.json`；旧数据文件的计分版本与当前接口不同，不能混用。

## 字段语义

- `score` 为官网返回的原始评分。内部校验 `score_version=radar-bench-v2-150`、`score_max=150`，页面只显示分数，不添加 `/150` 标签。
- `coverage` 为覆盖的不同题目数，`tasks` 为上游 `required_tasks`，当前题库包含 64 题。覆盖不足 40 题时显示“数据不足”。
- `graded_runs_count` 是判分题次，不等同于覆盖题数，更不等同于通过题数。
- 价格来自 `statistics.metrics.api_equivalent_usd.mean`，耗时来自 `statistics.metrics.elapsed_ms.mean`（转换为秒）。只使用有有效样本且均值非空的统计；缺失值保持 `null` 并在页面显示“—”，不能补成零或自行估价。
- `updated_at` 是上游最新判分日期桶；`fetched_at` 是本站实际同步时间。页面展示同步时间，避免将按日归档时间误当实时更新时间。

## 展示设计

四个模型家族使用独立且不受网站主题预设影响的语义配色：Astra 紫、6.1 Sol 橙金、6 Sol 青蓝、5.6 Sol 翡翠绿。深浅色分别选择高对比度文字色，并沿用本站旧版的细顶线、突出评分与右侧价格/耗时分栏。

评分条仅作为原始分数的辅助视觉，按上游 `score_max` 等比显示，不额外标注分母；没有分数或有效上限时不绘制填充。题次、覆盖题数与低覆盖警示仍按原字段展示。网格根据面板实际宽度切换 1/2/3/6 列，避免侧栏展开或小屏幕下挤压数据。

## 资源与失败保护

24 个点位由最多 4 个并发工作线程拉取。每个点位有 15 秒超时，整轮采集有 60 秒上限，响应大小（含解压后）限制为 1 MiB。仅失败点位在超时、连接中断、HTTP 408/429/5xx 时最多额外重试一次（默认等待 500ms），成功点位不重采，重试不增加工作线程。鉴权、格式错误、模型/评分版本/题库不匹配不重试。短 `Retry-After` 会被尊重；超过 5 秒的上游等待要求交由整轮退避处理，避免占用线程。

完整结果在内存缓存 10 分钟；并发页面请求通过 singleflight 合并为一次采集。任一点位失败或计分版本/题库不一致时，不用不完整结果覆盖缓存，最多使用 24 小时内的完整缓存并标记缓存数据。失败后至少间隔 1 分钟再向上游刷新，防止错误状态下重复采集；上游要求更长等待时取最长的 `Retry-After`。无可用缓存时返回 502 和 `Retry-After`（至少 60 秒）。

启用 Redis 时，在现有应用 Redis 中保存最后一次完整校验成功的原始汇总快照，键为 `cmsg:codex-radar:radar-bench-v2-150:four-models:v1`，TTL 为 24 小时，载荷上限 128 KiB。进程重启后的首次读取重建并校验完整 24 点、模型/档位顺序、评分版本、题库一致性及原采集时间；不会把恢复时间当成新采集时间。失败刷新既不覆盖快照也不延长其 TTL。Redis 读写最多占用 2 秒，失败退化为原内存缓存与实时采集，不影响成功数据的展示。没有配置 Redis 的实例仍使用内存缓存。

前端对临时网络/HTTP 错误最多自动重试两次，默认等待 65 秒、120 秒，并尊重更长的 `Retry-After`；鉴权和数据校验错误不自动重试。自动重试期间显示恢复提示，不重复弹全局错误提示，后台刷新失败也不清空已经展示的数据。正常刷新间隔为 10 分钟；后端返回缓存回退时，前端每 65 秒尝试恢复实时采集，并保持 React Query 默认的隐藏标签页暂停策略。

浏览器取消请求不会取消其他用户正在共享的刷新；后台刷新仍受自身超时限制。

## 回归检查

```text
go test ./service -run TestCodexRadar -count=1
go test ./controller -run TestCodexRadar -count=1
cd web/default
bun test src/features/dashboard/components/overview/__tests__/codex-radar-panel.test.tsx
bun test src/features/dashboard/lib/__tests__/codex-radar-query.test.ts
bun run build:check
```

生产验证应通过已认证的 `/api/codex-radar/overview` 检查：24 个点位、四个模型家族、计分版本、非过期采集和缺失统计的空值语义。不能只验证 `/api/status` 后就宣称雷达功能正常。

跨重启验证可本地编译 `./scripts/codex-radar-probe`，上传后在 New API 容器中运行（使用既有 Redis），确认第一轮完整采集写入快照，再以 `--no-network` 启动第二个独立进程，确认读取同一原采集时间且上游请求数为零；这不能替代已认证的生产接口验证。诊断不得输出连接串或凭据，不得在生产编译，不得制造用户或绕过接口认证。没有 Redis 的实例不使用此诊断程序。
