#!/usr/bin/env python3
"""Enforce per-channel budgets for a Docker Compose New API deployment.

The script is intentionally external to New API so it can work with the
official container image. It updates channel.used_quota/balance/status through
PostgreSQL and lets New API's normal cache sync pick up status changes.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

try:
    from zoneinfo import ZoneInfo
except Exception:  # pragma: no cover - Python 3.9+ has zoneinfo
    ZoneInfo = None  # type: ignore


STATUS_ENABLED = 1
STATUS_MANUALLY_DISABLED = 2
STATUS_AUTO_DISABLED = 3
OPS_DIR = Path(__file__).resolve().parent


@dataclass
class DB:
    docker: str
    container: str
    user: str
    database: str

    def psql(self, sql: str, capture: bool = False) -> str:
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
        if capture:
            cmd += ["-t", "-A", "-c", sql]
        else:
            cmd += ["-c", sql]
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
    with tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", dir=str(path.parent), delete=False
    ) as f:
        json.dump(data, f, ensure_ascii=False, indent=2, sort_keys=True)
        f.write("\n")
        tmp = f.name
    os.replace(tmp, path)


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def db_from_config(config: dict[str, Any]) -> DB:
    db_cfg = config.get("database", {})
    return DB(
        docker=str(config.get("docker", "/usr/bin/docker")),
        container=str(db_cfg.get("container", "new-api-postgres")),
        user=str(db_cfg.get("user", "newapi")),
        database=str(db_cfg.get("database", "new-api")),
    )


def int_ids(channels: list[dict[str, Any]]) -> list[int]:
    ids: list[int] = []
    for ch in channels:
        if not ch.get("enabled", True):
            continue
        cid = int(ch["id"])
        if cid not in ids:
            ids.append(cid)
    return ids


def fetch_channels(db: DB, ids: list[int]) -> dict[int, dict[str, Any]]:
    if not ids:
        return {}
    sql = f"""
select coalesce(json_agg(row_to_json(t)), '[]'::json)
from (
  select id, name, key, status, used_quota, balance, balance_updated_time, other_info
  from channels
  where id in ({",".join(str(i) for i in ids)})
  order by id
) t;
"""
    raw = db.psql(sql, capture=True)
    rows = json.loads(raw or "[]")
    return {int(row["id"]): row for row in rows}


def normalize_url(value: Any) -> str:
    return str(value or "").strip().rstrip("/").lower()


def group_contains(group_value: Any, target: str) -> bool:
    groups = [part.strip() for part in str(group_value or "").split(",")]
    return target in groups


def discover_asxs_channels(db: DB, config: dict[str, Any]) -> list[dict[str, Any]]:
    discovery = config.get("auto_discovery", {})
    if not discovery.get("enabled", False):
        return []
    asxs_cfg = discovery.get("asxs", {})
    if not asxs_cfg.get("enabled", True):
        return []

    channel_type = int(asxs_cfg.get("channel_type", 1))
    target_group = str(asxs_cfg.get("group", "asxs"))
    target_base_url = normalize_url(asxs_cfg.get("base_url", "https://api.asxs.top"))
    sql = f"""
select coalesce(json_agg(row_to_json(t)), '[]'::json)
from (
  select id, name, type, base_url, "group"
  from channels
  where type = {channel_type}
  order by id
) t;
"""
    raw = db.psql(sql, capture=True)
    rows = json.loads(raw or "[]")
    discovered: list[dict[str, Any]] = []
    for row in rows:
        if normalize_url(row.get("base_url")) != target_base_url:
            continue
        if not group_contains(row.get("group"), target_group):
            continue
        discovered.append(
            {
                "id": int(row["id"]),
                "name": row.get("name") or str(row["id"]),
                "mode": asxs_cfg.get("mode", "daily"),
                "source": asxs_cfg.get("source", "asxs_usage"),
                "usage_url": asxs_cfg.get("usage_url", "https://api.asxs.top/api/usage"),
                "limit_usd": float(asxs_cfg.get("default_limit_usd", 1)),
                "enabled": True,
                "auto_discovered": True,
            }
        )
    return discovered


def resolve_channels_config(db: DB, config: dict[str, Any]) -> list[dict[str, Any]]:
    explicit = config.get("channels", [])
    by_id: dict[int, dict[str, Any]] = {}
    suppressed_ids: set[int] = set()

    for ch in explicit:
        cid = int(ch["id"])
        if ch.get("enabled", True):
            by_id[cid] = dict(ch)
        else:
            suppressed_ids.add(cid)

    for ch in discover_asxs_channels(db, config):
        cid = int(ch["id"])
        if cid in by_id or cid in suppressed_ids:
            continue
        by_id[cid] = ch

    return [by_id[cid] for cid in sorted(by_id)]


def update_channel(
    db: DB,
    cid: int,
    *,
    used_quota: int | None = None,
    balance: float | None = None,
    status: int | None = None,
    abilities_enabled: bool | None = None,
    other_info: dict[str, Any] | None = None,
    now_ts: int | None = None,
    dry_run: bool = False,
) -> None:
    now_ts = now_ts or int(time.time())
    sets: list[str] = []
    if used_quota is not None:
        sets.append(f"used_quota = {int(used_quota)}")
    if balance is not None:
        sets.append(f"balance = {float(balance):.6f}")
        sets.append(f"balance_updated_time = {now_ts}")
    if status is not None:
        sets.append(f"status = {int(status)}")
    if other_info is not None:
        sets.append("other_info = " + sql_literal(json.dumps(other_info, ensure_ascii=False)))
    if not sets and abilities_enabled is None:
        return

    statements: list[str] = ["begin;"]
    if sets:
        statements.append(f"update channels set {', '.join(sets)} where id = {cid};")
    if abilities_enabled is not None:
        enabled_sql = "true" if abilities_enabled else "false"
        statements.append(f"update abilities set enabled = {enabled_sql} where channel_id = {cid};")
    statements.append("commit;")
    sql = "\n".join(statements)
    if dry_run:
        print(f"[dry-run] channel {cid} SQL: {sql}")
        return
    db.psql(sql)


def parse_other_info(raw: Any) -> dict[str, Any]:
    if not raw:
        return {}
    if isinstance(raw, dict):
        return raw
    try:
        data = json.loads(str(raw))
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def today_string(config: dict[str, Any]) -> str:
    tz_name = config.get("timezone", "Asia/Shanghai")
    if ZoneInfo is not None:
        try:
            return datetime.now(ZoneInfo(tz_name)).date().isoformat()
        except Exception:
            pass
    return datetime.now().date().isoformat()


def build_budget_info(
    ch_cfg: dict[str, Any],
    mode: str,
    limit_usd: float,
    used_quota: int,
    quota_per_usd: float,
    remaining_usd: float,
    now_ts: int,
    reason: str,
    extra: dict[str, Any] | None = None,
) -> dict[str, Any]:
    info = {
        "managed": True,
        "mode": mode,
        "configured_name": ch_cfg.get("name", ""),
        "limit_usd": limit_usd,
        "used_usd": round(used_quota / quota_per_usd, 6),
        "remaining_usd": round(remaining_usd, 6),
        "updated_at": now_ts,
        "reason": reason,
    }
    if extra:
        info.update(extra)
    return info


def fetch_asxs_usage(api_key: str, usage_url: str, timeout: int = 20) -> dict[str, Any]:
    if not api_key:
        raise RuntimeError("channel key is empty")
    req = urllib.request.Request(
        usage_url,
        headers={
            "Authorization": f"Bearer {api_key}",
            "Accept": "application/json",
            "User-Agent": "new-api-channel-budget-guard/1.0",
        },
        method="GET",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = getattr(resp, "status", 0)
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        body = exc.read(240).decode("utf-8", "replace")
        raise RuntimeError(f"asxs usage http {exc.code}: {body}") from exc
    if status != 200:
        raise RuntimeError(f"asxs usage http {status}")
    try:
        data = json.loads(raw.decode("utf-8"))
    except Exception as exc:
        preview = raw[:160].decode("utf-8", "replace")
        raise RuntimeError(f"asxs usage invalid json: {preview}") from exc
    if not isinstance(data, list):
        raise RuntimeError("asxs usage payload is not a list")

    candidates: list[dict[str, Any]] = []
    for item in data:
        if not isinstance(item, dict):
            continue
        if item.get("unit") != "USD":
            continue
        if item.get("isValid") is False:
            continue
        if "total" in item and "remaining" in item:
            candidates.append(item)

    if not candidates:
        raise RuntimeError("asxs usage payload has no daily USD quota item")

    daily = None
    for item in candidates:
        name = str(item.get("planName", ""))
        if "日" in name or "daily" in name.lower():
            daily = item
            break
    if daily is None:
        daily = candidates[0]

    total = float(daily.get("total") or 0)
    remaining = float(daily.get("remaining") or 0)
    used_raw = daily.get("used")
    used = float(used_raw) if used_raw is not None else max(total - remaining, 0.0)
    return {
        "plan_name": daily.get("planName", ""),
        "total_usd": total,
        "used_usd": used,
        "remaining_usd": remaining,
        "unit": daily.get("unit", "USD"),
        "reset_info": daily.get("extra", ""),
        "raw_items": len(data),
    }


def init_state(config: dict[str, Any], state_path: Path) -> None:
    today = today_string(config)
    db = db_from_config(config)
    channels_cfg = resolve_channels_config(db, config)
    state = load_json(state_path, {"version": 1, "channels": {}})
    channels_state = state.setdefault("channels", {})
    for ch in channels_cfg:
        if not ch.get("enabled", True):
            continue
        cid = str(int(ch["id"]))
        entry = channels_state.setdefault(cid, {})
        if ch.get("mode", "daily") == "daily":
            entry.setdefault("last_reset_date", today)
        entry.setdefault("disabled_by_guard", False)
    save_json_atomic(state_path, state)
    print(f"initialized state at {state_path} for date {today}")


def run_guard(config_path: Path, state_path: Path, dry_run: bool, reset_now: bool) -> int:
    config = load_json(config_path, None)
    if not isinstance(config, dict):
        raise RuntimeError(f"invalid config: {config_path}")

    quota_per_usd = float(config.get("quota_per_usd", 500000))
    now_ts = int(time.time())
    today = today_string(config)

    db = db_from_config(config)
    channels_cfg = resolve_channels_config(db, config)
    rows = fetch_channels(db, int_ids(channels_cfg))
    state = load_json(state_path, {"version": 1, "channels": {}})
    channels_state = state.setdefault("channels", {})

    changed = False
    summaries: list[str] = []

    for ch_cfg in channels_cfg:
        cid = int(ch_cfg["id"])
        row = rows.get(cid)
        if row is None:
            summaries.append(f"channel {cid}: missing")
            continue

        mode = str(ch_cfg.get("mode", "daily")).strip().lower()
        if mode not in {"daily", "fixed"}:
            summaries.append(f"channel {cid}: unsupported mode {mode!r}")
            continue
        limit_usd = float(ch_cfg.get("limit_usd", ch_cfg.get("daily_usd", 0)))

        limit_quota = int(round(limit_usd * quota_per_usd))
        state_entry = channels_state.setdefault(str(cid), {})
        if mode == "daily" and "last_reset_date" not in state_entry:
            # First encounter is intentionally non-destructive. The next date
            # change will reset it, or use --reset-now for an immediate reset.
            state_entry["last_reset_date"] = today
            changed = True

        status = int(row.get("status") or 0)
        used_quota = int(row.get("used_quota") or 0)
        channel_name = row.get("name") or ch_cfg.get("name") or str(cid)
        source = str(ch_cfg.get("source", "local")).strip().lower()

        if limit_usd <= 0 and source != "asxs_usage":
            summaries.append(f"channel {cid}: no positive limit")
            continue

        if source == "asxs_usage":
            usage_url = str(ch_cfg.get("usage_url", "https://api.asxs.top/api/usage"))
            timeout = int(ch_cfg.get("usage_timeout_sec", config.get("usage_timeout_sec", 20)))
            try:
                upstream = fetch_asxs_usage(str(row.get("key") or ""), usage_url, timeout=timeout)
            except Exception as exc:
                summaries.append(f"channel {cid} {channel_name}: upstream usage sync failed: {exc}")
                continue

            upstream_total = float(upstream.get("total_usd") or 0)
            if upstream_total > 0:
                limit_usd = upstream_total
                limit_quota = int(round(limit_usd * quota_per_usd))
            upstream_used = max(float(upstream.get("used_usd") or 0), 0.0)
            upstream_remaining = max(float(upstream.get("remaining_usd") or 0), 0.0)
            used_quota = int(round(upstream_used * quota_per_usd))
            other_info = parse_other_info(row.get("other_info"))
            budget_extra = {
                "source": "asxs_usage",
                "upstream_plan_name": upstream.get("plan_name", ""),
                "upstream_reset_info": upstream.get("reset_info", ""),
                "upstream_total_usd": round(upstream_total, 6),
                "upstream_used_usd": round(upstream_used, 6),
                "upstream_remaining_usd": round(upstream_remaining, 6),
            }
            other_info["budget_guard"] = build_budget_info(
                ch_cfg,
                "upstream_daily",
                limit_usd,
                used_quota,
                quota_per_usd,
                upstream_remaining,
                now_ts,
                "within_budget" if upstream_remaining > 0 else "budget_exhausted",
                budget_extra,
            )

            if upstream_remaining <= 0:
                if status == STATUS_ENABLED:
                    update_channel(
                        db,
                        cid,
                        used_quota=used_quota,
                        balance=0,
                        status=STATUS_AUTO_DISABLED,
                        abilities_enabled=False,
                        other_info={
                            **other_info,
                            "status_reason": f"channel_budget_exhausted: upstream daily limit ${limit_usd:g}",
                            "status_time": now_ts,
                        },
                        now_ts=now_ts,
                        dry_run=dry_run,
                    )
                    state_entry["disabled_by_guard"] = True
                    changed = True
                    summaries.append(f"channel {cid} {channel_name}: disabled by upstream usage, used ${upstream_used:.4f} >= ${limit_usd:g}")
                else:
                    update_channel(
                        db,
                        cid,
                        used_quota=used_quota,
                        balance=0,
                        other_info=other_info,
                        now_ts=now_ts,
                        dry_run=dry_run,
                    )
                    summaries.append(f"channel {cid} {channel_name}: upstream exhausted, status={status}")
                continue

            if state_entry.get("disabled_by_guard", False) and status != STATUS_MANUALLY_DISABLED:
                update_channel(
                    db,
                    cid,
                    used_quota=used_quota,
                    balance=upstream_remaining,
                    status=STATUS_ENABLED,
                    abilities_enabled=True,
                    other_info=other_info,
                    now_ts=now_ts,
                    dry_run=dry_run,
                )
                state_entry["disabled_by_guard"] = False
                changed = True
                summaries.append(f"channel {cid} {channel_name}: re-enabled by upstream usage, remaining ${upstream_remaining:.4f}")
            else:
                update_channel(
                    db,
                    cid,
                    used_quota=used_quota,
                    balance=upstream_remaining,
                    other_info=other_info,
                    now_ts=now_ts,
                    dry_run=dry_run,
                )
                summaries.append(f"channel {cid} {channel_name}: upstream total ${limit_usd:.4f}, used ${upstream_used:.4f}, remaining ${upstream_remaining:.4f}, status={status}")
            continue

        should_reset = mode == "daily" and (
            reset_now or state_entry.get("last_reset_date") != today
        )
        if should_reset:
            other_info = parse_other_info(row.get("other_info"))
            other_info["budget_guard"] = build_budget_info(
                ch_cfg, mode, limit_usd, 0, quota_per_usd, limit_usd, now_ts, "daily_reset"
            )
            can_enable = status != STATUS_MANUALLY_DISABLED and state_entry.get(
                "disabled_by_guard", False
            )
            update_channel(
                db,
                cid,
                used_quota=0,
                balance=limit_usd,
                status=STATUS_ENABLED if can_enable else None,
                abilities_enabled=True if can_enable else None,
                other_info=other_info,
                now_ts=now_ts,
                dry_run=dry_run,
            )
            state_entry["last_reset_date"] = today
            state_entry["disabled_by_guard"] = False
            used_quota = 0
            status = STATUS_ENABLED if can_enable else status
            changed = True
            summaries.append(f"channel {cid} {channel_name}: reset daily budget to ${limit_usd:g}")

        remaining_quota = limit_quota - used_quota
        remaining_usd = max(remaining_quota / quota_per_usd, 0.0)
        other_info = parse_other_info(row.get("other_info"))
        other_info["budget_guard"] = build_budget_info(
            ch_cfg,
            mode,
            limit_usd,
            used_quota,
            quota_per_usd,
            remaining_usd,
            now_ts,
            "within_budget" if remaining_quota > 0 else "budget_exhausted",
        )

        if remaining_quota <= 0:
            if status == STATUS_ENABLED:
                update_channel(
                    db,
                    cid,
                    balance=0,
                    status=STATUS_AUTO_DISABLED,
                    abilities_enabled=False,
                    other_info={
                        **other_info,
                        "status_reason": f"channel_budget_exhausted: {mode} limit ${limit_usd:g}",
                        "status_time": now_ts,
                    },
                    now_ts=now_ts,
                    dry_run=dry_run,
                )
                state_entry["disabled_by_guard"] = True
                changed = True
                summaries.append(f"channel {cid} {channel_name}: disabled, used ${used_quota / quota_per_usd:.4f} >= ${limit_usd:g}")
            else:
                update_channel(
                    db,
                    cid,
                    balance=0,
                    other_info=other_info,
                    now_ts=now_ts,
                    dry_run=dry_run,
                )
                summaries.append(f"channel {cid} {channel_name}: exhausted, status={status}")
            continue

        if state_entry.get("disabled_by_guard", False) and status != STATUS_MANUALLY_DISABLED:
            update_channel(
                db,
                cid,
                balance=remaining_usd,
                status=STATUS_ENABLED,
                abilities_enabled=True,
                other_info=other_info,
                now_ts=now_ts,
                dry_run=dry_run,
            )
            state_entry["disabled_by_guard"] = False
            changed = True
            summaries.append(f"channel {cid} {channel_name}: re-enabled, remaining ${remaining_usd:.4f}")
        else:
            update_channel(
                db,
                cid,
                balance=remaining_usd,
                other_info=other_info,
                now_ts=now_ts,
                dry_run=dry_run,
            )
            summaries.append(f"channel {cid} {channel_name}: used ${used_quota / quota_per_usd:.4f}, remaining ${remaining_usd:.4f}, status={status}")

    if changed and not dry_run:
        save_json_atomic(state_path, state)
    elif not state_path.exists() and not dry_run:
        save_json_atomic(state_path, state)

    for line in summaries:
        print(line)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default=str(OPS_DIR / "channel_budgets.json"))
    parser.add_argument("--state", default=str(OPS_DIR / "channel_budget_state.json"))
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--init-state", action="store_true")
    parser.add_argument("--reset-now", action="store_true")
    args = parser.parse_args()

    config_path = Path(args.config)
    state_path = Path(args.state)
    if args.init_state:
        config = load_json(config_path, None)
        if not isinstance(config, dict):
            raise RuntimeError(f"invalid config: {config_path}")
        init_state(config, state_path)
        return 0
    return run_guard(config_path, state_path, args.dry_run, args.reset_now)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"channel_budget_guard error: {exc}", file=sys.stderr)
        raise SystemExit(1)
