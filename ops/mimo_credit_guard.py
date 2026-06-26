#!/usr/bin/env python3
"""Local MiMo Token Plan credit estimator for New API.

MiMo Token Plan balance is measured in Credits, not USD. The provider API key
can invoke models but does not expose plan balance. This guard estimates the
remaining plan Credits from New API logs after a manually confirmed baseline.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import tempfile
import time
from dataclasses import dataclass
from datetime import datetime, time as dtime
from pathlib import Path
from typing import Any

try:
    from zoneinfo import ZoneInfo
except Exception:  # pragma: no cover
    ZoneInfo = None  # type: ignore


@dataclass
class DB:
    docker: str
    container: str
    user: str
    database: str

    def psql(self, sql: str, capture: bool = True) -> str:
        cmd = [
            self.docker,
            "exec",
            self.container,
            "psql",
            "-U",
            self.user,
            "-d",
            self.database,
            "-v",
            "ON_ERROR_STOP=1",
        ]
        cmd += ["-t", "-A", "-c", sql] if capture else ["-c", sql]
        proc = subprocess.run(cmd, text=True, capture_output=True)
        if proc.returncode != 0:
            raise RuntimeError(proc.stderr.strip() or proc.stdout.strip())
        return proc.stdout.strip()


def load_json(path: Path, default: Any) -> Any:
    if not path.exists():
        return default
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def save_json_atomic(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=str(path.parent), delete=False) as f:
        json.dump(data, f, ensure_ascii=False, indent=2, sort_keys=True)
        f.write("\n")
        tmp = f.name
    os.replace(tmp, path)


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def local_dt(epoch: int, tz_name: str) -> datetime:
    if ZoneInfo is not None:
        try:
            return datetime.fromtimestamp(int(epoch), ZoneInfo(tz_name))
        except Exception:
            pass
    return datetime.fromtimestamp(int(epoch))


def parse_hhmm(value: str) -> dtime:
    hour, minute = value.split(":", 1)
    return dtime(int(hour), int(minute))


def in_window(dt: datetime, start: dtime, end: dtime) -> bool:
    current = dt.time()
    if start < end:
        return start <= current < end
    return current >= start or current < end


def normalize_model(name: str) -> str:
    return (name or "").split("[", 1)[0].strip().lower()


def as_int(value: Any) -> int:
    try:
        return int(float(value or 0))
    except Exception:
        return 0


def format_credits(value: int) -> str:
    value = int(value)
    if abs(value) >= 1_000_000:
        return f"{value / 1_000_000:.2f}M"
    if abs(value) >= 1_000:
        return f"{value / 1_000:.2f}K"
    return str(value)


def fetch_logs(db: DB, channel_id: int, baseline_log_id: int) -> list[dict[str, Any]]:
    sql = f"""
select coalesce(json_agg(row_to_json(t)), '[]'::json)
from (
  select id, created_at, model_name, prompt_tokens, completion_tokens, other
  from logs
  where channel_id = {int(channel_id)}
    and type = 2
    and id > {int(baseline_log_id)}
  order by id
) t;
"""
    raw = db.psql(sql, capture=True)
    return json.loads(raw or "[]")


def estimate_log_credits(row: dict[str, Any], config: dict[str, Any]) -> dict[str, Any]:
    other = {}
    if row.get("other"):
        try:
            other = json.loads(row["other"])
        except Exception:
            other = {}

    prompt_tokens = as_int(row.get("prompt_tokens"))
    completion_tokens = as_int(row.get("completion_tokens"))
    cache_read_tokens = as_int(other.get("cache_tokens"))
    cache_creation_tokens = as_int(other.get("cache_creation_tokens"))

    usage_cfg = config.get("usage", {})
    token_count = prompt_tokens + completion_tokens
    if usage_cfg.get("include_cache_read_tokens", True):
        token_count += int(cache_read_tokens * float(usage_cfg.get("cache_read_multiplier", 1)))
    if usage_cfg.get("include_cache_creation_tokens", True):
        token_count += int(cache_creation_tokens * float(usage_cfg.get("cache_creation_multiplier", 1)))

    model = normalize_model(str(row.get("model_name") or ""))
    rates = {normalize_model(k): float(v) for k, v in config.get("model_credit_rates", {}).items()}
    model_rate = float(rates.get(model, config.get("default_model_credit_rate", 1)))

    tz_name = config.get("timezone", "Asia/Shanghai")
    dt = local_dt(as_int(row.get("created_at")), tz_name)
    night_cfg = config.get("night_discount", {})
    time_rate = 1.0
    if night_cfg.get("enabled", True):
        start = parse_hhmm(str(night_cfg.get("start", "00:00")))
        end = parse_hhmm(str(night_cfg.get("end", "08:00")))
        if in_window(dt, start, end):
            time_rate = float(night_cfg.get("multiplier", 0.8))

    credits = int(round(token_count * model_rate * time_rate))
    return {
        "id": as_int(row.get("id")),
        "created_at": as_int(row.get("created_at")),
        "model": row.get("model_name") or "",
        "tokens": token_count,
        "model_rate": model_rate,
        "time_rate": time_rate,
        "credits": credits,
    }


def update_channel(db: DB, channel_id: int, remaining: int, used: int, total: int, config: dict[str, Any], dry_run: bool) -> None:
    display = config.get("display", {})
    expires_at = str(config.get("expires_at", ""))
    baseline_used = int(config.get("initial_used_credits", 0))
    remark = (
        "MiMo Token Plan Credits: "
        f"剩余 {format_credits(remaining)} / {format_credits(total)}，"
        f"已用 {format_credits(used)}；"
        f"基线已用 {format_credits(baseline_used)}；"
        f"到期 {expires_at}；单位是 Credits，不是 USD"
    )
    balance_value = remaining
    if display.get("balance_unit") == "million_credits":
        balance_value = remaining / 1_000_000
    sql = (
        "update channels set "
        f"balance = {balance_value:.6f}, "
        f"balance_updated_time = {int(time.time())}, "
        f"remark = {sql_literal(remark)} "
        f"where id = {int(channel_id)};"
    )
    if dry_run:
        print("[dry-run] " + sql)
        return
    db.psql(sql, capture=False)


def run(config_path: Path, state_path: Path, dry_run: bool) -> int:
    config = load_json(config_path, None)
    if not isinstance(config, dict):
        raise RuntimeError(f"invalid config: {config_path}")

    db_cfg = config.get("database", {})
    db = DB(
        docker=str(config.get("docker", "/usr/bin/docker")),
        container=str(db_cfg.get("container", "new-api-postgres")),
        user=str(db_cfg.get("user", "newapi")),
        database=str(db_cfg.get("database", "new-api")),
    )

    channel_id = int(config.get("channel_id", 3))
    baseline_log_id = int(config.get("baseline_log_id", 0))
    total = int(config.get("plan_total_credits", 0))
    initial_used = int(config.get("initial_used_credits", 0))

    rows = fetch_logs(db, channel_id, baseline_log_id)
    items = [estimate_log_credits(row, config) for row in rows]
    incremental = sum(item["credits"] for item in items)
    used = initial_used + incremental
    remaining = max(total - used, 0)

    report = {
        "version": 1,
        "updated_at": int(time.time()),
        "channel_id": channel_id,
        "baseline_log_id": baseline_log_id,
        "initial_used_credits": initial_used,
        "incremental_credits": incremental,
        "used_credits": used,
        "remaining_credits": remaining,
        "plan_total_credits": total,
        "expires_at": config.get("expires_at"),
        "log_count_after_baseline": len(rows),
        "last_log_id": max([baseline_log_id] + [item["id"] for item in items]),
        "last_items": items[-10:],
    }

    if config.get("display", {}).get("update_channel", True):
        update_channel(db, channel_id, remaining, used, total, config, dry_run)
    if not dry_run:
        save_json_atomic(state_path, report)

    print(
        "mimo credits: "
        f"remaining={remaining} ({format_credits(remaining)}), "
        f"used={used} ({format_credits(used)}), "
        f"incremental={incremental} ({format_credits(incremental)}), "
        f"logs={len(rows)}, baseline_log_id={baseline_log_id}"
    )
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="/home/wcy/new-api/ops/mimo_credit_config.json")
    parser.add_argument("--state", default="/home/wcy/new-api/ops/mimo_credit_state.json")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    return run(Path(args.config), Path(args.state), args.dry_run)


if __name__ == "__main__":
    raise SystemExit(main())
