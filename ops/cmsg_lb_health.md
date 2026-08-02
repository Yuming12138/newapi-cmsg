# CMSG LB 综合健康信号

`cmsg_lb_health.py` 将 `cmsg_network_probe.py` 的 v2 JSONL 历史转换为 Cloudflare Load Balancer Monitor 可读取的 HTTP 信号。它只观测并输出本机 `200` 或 `503`，不会调用 Cloudflare API、修改 DNS、创建 LB 或执行主备提升。

## 判定范围

默认使用最近 30 分钟、至少 4 个探测窗口，并检查：

- 探测记录不超过 12 分钟；
- New API、CPA、Mihomo 三类日志源可用；
- 清华镜像和 Cloudflare 两个非诊断目标的成功率与 p95 TTFB；
- Cloudflare speed probe 的成功率与 p95 TTFB；
- New API 网络 5xx、CPA HTTP/2/EOF/timeout、Mihomo 连接错误；
- TCP 总发送段数、总重传段数及聚合重传率；
- Mihomo 可用节点的最佳延迟；
- 配置中列出的 New API、CPA、Mihomo、PostgreSQL、Redis 容器状态；
- 可选的数据面接流资格文件。

New API 429/额度错误只记录，不会把网络判为故障。直连 `chatgpt.com` 保持 `diagnostic_only`，不参与健康判定。

## 迟滞

- 连续 2 个新探测窗口不健康后，状态切为不健康；
- 连续 3 个新探测窗口健康，且满足 15 分钟恢复冷却后，状态恢复；
- 同一个探测窗口被 Monitor 重复请求不会重复累计；
- 探测记录过期是硬故障，不等待迟滞。

## 数据面资格

`eligibility.required=true` 时，资格文件必须是 JSON：

```json
{
  "ready": false,
  "reason": "postgres_and_redis_standbys_not_promoted"
}
```

校园 PostgreSQL/Redis 仍是只读 standby/replica 时应保持 `ready=false`。只有完成受控提升、切换 New API 到本地数据层并验证写入后，才可原子更新为 `ready=true`。该文件不应包含密码、DSN 或 Token。

## 命令与端点

```bash
python3 ops/cmsg_lb_health.py evaluate ops/cmsg_lb_health.json
python3 ops/cmsg_lb_health.py serve ops/cmsg_lb_health.json
```

- `GET /__cmsg_lb_health`：健康返回 `200`，不健康返回 `503`；响应只含精简、脱敏字段。
- `GET /__cmsg_lb_status`：始终返回 `200` 和详细聚合指标，建议仅保留在 loopback，不通过公网 Nginx 暴露。

Cloudflare LB 应使用主池 `aliyun`、备用池 `campus` 的故障转移方式，而不是按瞬时延迟轮询。启用 LB、修改 `api.cmsg666.xyz` 或执行数据库提升均需单独授权。
