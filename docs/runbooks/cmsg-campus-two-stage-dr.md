# CMSG 校园两级灾备切换 Runbook

本文适用于以下固定拓扑：

- 公网节点：`cmsg-root`，WireGuard `10.203.66.1`；
- 校园节点：`fuyao`，WireGuard `10.203.66.2`；
- 公网入口：`api.cmsg666.xyz`；校园入口：`campus-api.cmsg666.xyz`；
- 公网 PostgreSQL/Redis 正常态为主，校园为异步 standby/replica；
- 校园 New API 保持 `NODE_TYPE=slave`，全局后台任务仍由公网 master 节点执行；
- 不启用 Cloudflare Load Balancer，不自动改 DNS，不执行无围栏自动提升。

## 两级模式

### L1：快速卸载模型流量，数据面仍在阿里云

链路为：

```text
用户 -> campus-api -> 校园 New API -> 校园 CPA/Mihomo -> AI 上游
                         |-> WireGuard -> 阿里云 PostgreSQL/Redis
```

该模式避免模型请求和流式响应占用阿里云公网带宽，但每个请求仍会跨站访问数据库与 Redis。只有以下条件持续满足时才允许使用：

- WireGuard 无持续丢包，TCP/5432 与 TCP/6379 无连接错误；
- PostgreSQL `SELECT 1`、Redis `PING` 的 p95 不超过各自健康基线的 3 倍；
- 建议绝对上限：PostgreSQL p95 150 ms、Redis p95 100 ms，错误率低于 1%；
- PostgreSQL standby 正常 streaming，Redis replica link 为 `up`；
- New API、CPA、Mihomo 及 Tunnel 均健康。

L1 不等于完整灾备，`cmsg_lb_eligibility.json` 必须保持 `ready=false`。建议使用独立的跨站数据面探测展示 L1 可用性，不复用完整 DR 健康端点。

当前演练中校园仍保持 `NODE_TYPE=slave`。进入 L2 且公网 New API 被围栏后，全局后台任务会暂停；这不会阻止普通 API 请求和结算写入，但不适合作为长期运行状态。若 L2 需要持续运行，必须在确认公网进程完全停止后，单独授权将校园 New API 提升为唯一 task master，并在回切前恢复为 slave，禁止两端同时运行 master 任务。

### L2：校园完整接管本地数据面

当 WireGuard/数据库 RTT 持续恶化、连接超时，或阿里云断网时，进入 L2。顺序必须是：

```text
关闭 eligibility
  -> 停止校园远端数据模式 New API
  -> 围栏/停止公网 New API
  -> 确认 WAL 和 Redis offset 追平
  -> 提升校园 PostgreSQL/Redis
  -> 校园 New API 改连本地服务
  -> 写入验证
  -> 原子打开 eligibility
```

若阿里云完全不可达，无法确认最终 WAL/offset 时，只能按最后可见的复制状态评估 RPO；不得把异步复制描述成零数据损失。

## 固定 Compose 命令

校园所有业务操作必须使用同一项目名和完整覆盖文件，避免生成重复项目或容器名冲突：

```bash
cd /home/gmchen/cmsg-campus
docker compose -p cmsg-campus \
  -f compose.yaml \
  -f compose.home.yaml \
  -f compose.replication.yaml \
  -f compose.failover.yaml \
  config -q
```

不要修改或混入隔离测试容器 `cmsg-campus-new-api`、`cmsg-campus-postgres`、`cmsg-campus-redis`。

## L2 切换前检查

1. 确认校园 New API 与公网运行二进制 SHA256 一致，容器均 healthy。
2. 确认公网 PostgreSQL：`pg_is_in_recovery=false`，校园复制连接 `streaming`，WAL lag 为 0。
3. 确认校园 PostgreSQL：`pg_is_in_recovery=true`，receive/replay LSN 相同。
4. 确认公网 Redis：`role=master`，校园 replica `online`、lag 0；校园 Redis：`role=slave`、link `up`、offset 与公网一致。
5. 确认校园复制网络子网已加入 `pg_hba.conf`，认证方法为 `scram-sha-256`；不得使用 `trust`。
6. 确认校园 `postgres-standby`、`redis-standby` 没有公网端口映射。
7. 用 `cmsg_dr_guard.py rewrite-env --dry-run` 验证只会把 `10.203.66.1` 改为 `postgres-standby` / `redis-standby`，不输出 DSN 或密码。
8. 先将 eligibility 关闭：

```bash
python3 /home/gmchen/cmsg-campus/ops/cmsg_dr_guard.py eligibility-block \
  /home/gmchen/cmsg-campus/ops/cmsg_lb_eligibility.json \
  --reason failover_in_progress
```

## L2 围栏与追平

预计 `api.cmsg666.xyz` 会在公网 New API 停止后返回 503；既有流会中断。没有 Cloudflare LB/DNS 切换时，用户必须改用 `campus-api.cmsg666.xyz`，且校园入口要等本地数据面完成后才开放为完整 DR。

先停止校园仍连接远端数据面的 New API，再停止公网 New API：

```bash
ssh fuyao 'cd /home/gmchen/cmsg-campus && docker compose -p cmsg-campus \
  -f compose.yaml -f compose.home.yaml -f compose.replication.yaml -f compose.failover.yaml \
  stop new-api-failover'

ssh cmsg-root 'sudo -n bash -lc "cd /opt/new-api && docker compose -f docker-compose.prod.yml stop new-api"'
```

停止后重新读取两端 LSN 与 Redis offset。必须满足：

- 公网 `pg_current_wal_lsn()` 等于校园 `pg_last_wal_replay_lsn()`；
- 校园 `pg_last_wal_receive_lsn()` 等于 `pg_last_wal_replay_lsn()`；
- 公网 `master_repl_offset` 等于校园 `slave_repl_offset`；
- 公网 PostgreSQL 不再有 New API 普通 client backend，仅允许复制连接和本次管理查询。

任一条件不满足时停止切换并恢复两个 New API；此时尚未提升，可安全回到 L1/正常态。

## L2 提升（不可直接回滚点）

一旦执行以下命令，阿里云旧 PostgreSQL/Redis 就不得再以主库身份接受写入。恢复阿里云主用必须走“受控回切”，不能简单重启旧主库。

```bash
ssh fuyao 'sudo -n docker exec -u postgres cmsg-campus-postgres-standby \
  sh -lc '\''pg_ctl -D "$PGDATA" promote -w -t 60'\'''

ssh fuyao 'sudo -n docker exec cmsg-campus-redis-standby \
  sh -lc '\''REDISCLI_AUTH="$REDIS_STANDBY_PASSWORD" redis-cli --no-auth-warning REPLICAOF NO ONE'\'''
```

立即验证校园 PostgreSQL `pg_is_in_recovery=false`、Redis `role=master`。任一提升失败时保持两个 New API 停止，禁止尝试启动阿里云旧主写入。

## 校园 New API 改为本地数据面

先执行 dry-run，再执行原子替换。工具只改变 URL 的 host/port，保留用户名、密码、库名和查询参数，备份权限为 600：

```bash
python3 /home/gmchen/cmsg-campus/ops/cmsg_dr_guard.py rewrite-env \
  /home/gmchen/cmsg-campus/secrets/new-api-failover.env \
  --expected-sql-host 10.203.66.1 \
  --expected-redis-host 10.203.66.1 \
  --sql-host postgres-standby --sql-port 5432 \
  --redis-host redis-standby --redis-port 6379 \
  --backup-dir /home/gmchen/cmsg-campus/backups/dr-env \
  --dry-run

# 人工核对输出只包含 before/after host 与 port 后，去掉 --dry-run 再执行。
```

然后仅重建校园 failover New API：

```bash
cd /home/gmchen/cmsg-campus
docker compose -p cmsg-campus \
  -f compose.yaml -f compose.home.yaml -f compose.replication.yaml -f compose.failover.yaml \
  config -q
docker compose -p cmsg-campus \
  -f compose.yaml -f compose.home.yaml -f compose.replication.yaml -f compose.failover.yaml \
  up -d --no-deps --force-recreate new-api-failover
```

## 写入验证与 eligibility

验证必须同时满足：

- 校园 PostgreSQL 已提升，校园 Redis 为 master；
- 校园 New API healthy，脱敏检查显示 host 为 `postgres-standby` / `redis-standby`；
- `campus-api.cmsg666.xyz` 状态接口正常；
- 使用现有 Token 通过 `newapi exec-token` 发起一次最小、可计费的专用模型请求；禁止把 Token 放入命令或日志；
- 请求成功并取得 request ID；校园 PostgreSQL 中对应消费日志增加至少 1 行，Token 已用额度/用户额度变化符合该请求；
- 公网旧主库在围栏后没有新增消费日志。

将上述无敏感字段结果写入权限 600 的 evidence JSON：

```json
{
  "schema_version": 1,
  "site_id": "campus",
  "checked_at": "2026-08-02T12:00:00+00:00",
  "postgres": {"in_recovery": false},
  "redis": {"role": "master"},
  "new_api": {
    "healthy": true,
    "sql_host": "postgres-standby",
    "redis_host": "redis-standby"
  },
  "write_probe": {
    "ok": true,
    "request_id": "sanitized-request-id",
    "db_log_delta": 1
  }
}
```

先 dry-run，再原子打开 gate：

```bash
python3 /home/gmchen/cmsg-campus/ops/cmsg_dr_guard.py eligibility-ready \
  /home/gmchen/cmsg-campus/ops/cmsg_lb_eligibility.json \
  --reason campus_data_plane_promoted_and_write_verified \
  --evidence /home/gmchen/cmsg-campus/ops/cmsg_dr_evidence.json \
  --max-age-sec 900 \
  --dry-run

# 核对通过后去掉 --dry-run。
```

最后验证：

```bash
curl -fsS -o /dev/null -w '%{http_code}\n' \
  https://campus-api.cmsg666.xyz/__cmsg_lb_health
```

只有全部条件满足时应返回 200。本端点不启用 Cloudflare LB，也不修改 `api.cmsg666.xyz`。

## 提升前回滚与提升后回切

### 提升前

若 LSN/offset 未追平或预检失败：保持 eligibility=false，重新启动公网 New API 和校园 L1 New API。数据库角色未改变，不需要数据恢复。

### 提升后

提升后不存在“启动阿里云旧主”这种回滚。受控回切步骤为：

1. 保持阿里云 New API 停止，将阿里云 PostgreSQL 用 `pg_rewind` 或全量 `pg_basebackup` 重建为校园主库的 standby；
2. 仅在 WireGuard 地址临时发布校园 PostgreSQL/Redis，并用 UFW 只允许 `10.203.66.1`；
3. 将阿里云 Redis 配为校园 Redis 的 replica；
4. 等待阿里云 PostgreSQL WAL 与 Redis offset 追平；
5. 再次关闭 eligibility，停止校园 New API，等待最终追平并围栏校园写入；
6. 提升阿里云 PostgreSQL/Redis，恢复公网 New API 的本地连接并做写入验证；
7. 将校园重新初始化为阿里云 standby/replica，恢复 L1 配置，eligibility 保持 false；
8. 删除回切期间的校园 WG 数据端口映射和临时 UFW 规则。

回切涉及第二次数据库角色翻转和旧主数据目录重建，必须使用独立维护窗口与明确授权。

## Redis 凭据轮换

若任何 Redis URI 认证字段进入聊天、日志或命令输出，应视为已暴露。轮换必须在维护窗口内同时更新：

- 当前 Redis master 的 `requirepass`/ACL；
- replica 的 `masterauth` 与本地 `requirepass`；
- 公网与校园 New API 的 `REDIS_CONN_STRING`；
- healthcheck 和运维脚本使用的受保护环境文件。

新值只生成到 600 文件，不回显；两端配置完成并验证后再撤销旧凭据。不要在切换中途只改一端。
