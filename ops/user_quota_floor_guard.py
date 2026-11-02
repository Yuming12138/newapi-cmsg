#!/usr/bin/env python3
"""User quota floor guard for New API.

Keeps every active user above a configurable minimum wallet balance so a user
is never hard-blocked at zero after a balance sync or a long consumption run.

The floor is expressed in CNY and converted with New API's own constants
(quota_per_usd and usd_exchange_rate). Only users below the floor are touched;
users above it are never modified.

Caveat: this is a refill floor, not a hard cap. A user can consume down to zero
and be refilled again on the next tick. Tune the timer interval accordingly.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from typing import Any

DEFAULT_CONFIG = {
    "docker": "/usr/bin/docker",
    "database": {"container": "new-api-postgres"},
    "floor_yuan": 10.0,
    "quota_per_usd": 500000,
    "usd_exchange_rate": 7.3,
    "include_status": [1],
    "exclude_user_ids": [],
    "redis": {"container": "new-api-redis"},
    "invalidate_redis_cache": True,
    "state_path": "/opt/new-api/ops/user_quota_floor_guard_state.json",
}


def load_config(path: str | None) -> dict[str, Any]:
    cfg = dict(DEFAULT_CONFIG)
    if path:
        raw = json.loads(Path(path).read_text(encoding="utf-8"))
        for key, value in raw.items():
            if key == "database" and isinstance(value, dict):
                cfg["database"] = {**cfg["database"], **value}
            else:
                cfg[key] = value
    return cfg


def floor_quota(cfg: dict[str, Any]) -> int:
    usd = float(cfg["floor_yuan"]) / float(cfg["usd_exchange_rate"])
    return int(round(usd * float(cfg["quota_per_usd"])))


def psql(cfg: dict[str, Any], sql: str, capture: bool = False) -> str:
    db = cfg["database"]
    cmd = [
        cfg["docker"], "exec", db["container"],
        "psql", "-U", db.get("user", "postgres"), "-d", db.get("database", "new-api"),
        "-v", "ON_ERROR_STOP=1",
    ]
    cmd += ["-t", "-A", "-c", sql] if capture else ["-c", sql]
    proc = subprocess.run(cmd, check=False, capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip())
    return proc.stdout.strip()


def build_where(cfg: dict[str, Any]) -> str:
    clauses = ["deleted_at is null", f"quota < {floor_quota(cfg)}"]
    statuses = [int(x) for x in cfg.get("include_status") or []]
    if statuses:
        clauses.append("status in (" + ",".join(str(x) for x in statuses) + ")")
    excluded = [int(x) for x in cfg.get("exclude_user_ids") or []]
    if excluded:
        clauses.append("id not in (" + ",".join(str(x) for x in excluded) + ")")
    return " and ".join(clauses)


def redis_command(cfg: dict[str, Any], command: str) -> str:
    """Run a redis-cli command inside the configured redis container.

    New API caches users under user:<id>; a direct DB write is overwritten by
    the next request unless that key is dropped.
    """
    redis_cfg = cfg.get("redis") or {}
    container = redis_cfg.get("container") or "new-api-redis"
    cmd = [
        cfg["docker"], "exec", container, "sh", "-c",
        f'redis-cli -a "$REDIS_PASSWORD" --no-auth-warning {command}',
    ]
    proc = subprocess.run(cmd, check=False, capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip())
    return proc.stdout.strip()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    cfg = load_config(args.config)
    quota = floor_quota(cfg)
    where = build_where(cfg)

    preview = psql(cfg, f"select id, username, quota from users where {where} order by id", capture=True)
    rows = [line for line in preview.splitlines() if line.strip()]
    print(f"user_quota_floor_guard: floor_yuan={cfg['floor_yuan']} quota={quota} matched={len(rows)}")
    for line in rows:
        print("  ", line)

    if args.dry_run or not rows:
        return 0

    psql(cfg, f"update users set quota = {quota} where {where}")
    ids = [line.split("|")[0] for line in rows]
    if ids and cfg.get("invalidate_redis_cache", True):
        redis_command(cfg, "DEL " + " ".join(f"user:{user_id}" for user_id in ids))
    Path(cfg["state_path"]).write_text(
        json.dumps(
            {
                "applied_at": datetime.now().isoformat(timespec="seconds"),
                "floor_yuan": cfg["floor_yuan"],
                "floor_quota": quota,
                "topped_up": ids,
            },
            ensure_ascii=False,
        ),
        encoding="utf-8",
    )
    print(f"user_quota_floor_guard: topped up {len(rows)} user(s) to {quota} (cache invalidated: {bool(ids)})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
