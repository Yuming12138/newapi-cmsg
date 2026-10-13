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

## 资源与失败保护

24 个点位由最多 4 个并发工作线程拉取。每个点位有 15 秒超时，整轮有 60 秒上限，响应大小（含解压后）限制为 1 MiB。

完整结果在内存缓存 10 分钟；并发页面请求通过 singleflight 合并为一次采集。任一点位失败或计分版本/题库不一致时，不用不完整结果覆盖缓存，最多使用 24 小时内的完整缓存并标记缓存数据。失败后至少间隔 1 分钟再向上游刷新，防止错误状态下重复采集。

浏览器取消请求不会取消其他用户正在共享的刷新；后台刷新仍受自身超时限制。

## 回归检查

```text
go test ./service -run TestCodexRadar -count=1
cd web/default
bun test src/features/dashboard/components/overview/__tests__/codex-radar-panel.test.tsx
bun run build:check
```

生产验证应通过已认证的 `/api/codex-radar/overview` 检查：24 个点位、四个模型家族、计分版本、非过期采集和缺失统计的空值语义。不能只验证 `/api/status` 后就宣称雷达功能正常。
