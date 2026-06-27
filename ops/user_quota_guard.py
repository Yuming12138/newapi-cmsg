#!/usr/bin/env python3
"""Time-window user quota guard for New API.

This is an external guard for the official New API image. It changes user.wallet
quota at phase boundaries:
- restricted window: set each managed user to base quota plus approved extras.
- unlocked window: set each managed user to a large quota and let channel budget
  guards enforce the upstream pool.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
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
    with tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", dir=str(path.parent), delete=False
    ) as f:
        json.dump(data, f, ensure_ascii=False, indent=2, sort_keys=True)
        f.write("\n")
        tmp = f.name
    os.replace(tmp, path)


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def local_now(config: dict[str, Any]) -> datetime:
    tz_name = config.get("timezone", "Asia/Shanghai")
    if ZoneInfo is not None:
        try:
            return datetime.now(ZoneInfo(tz_name))
        except Exception:
            pass
    return datetime.now()


def parse_hhmm(value: str) -> dtime:
    hour, minute = value.split(":", 1)
    return dtime(int(hour), int(minute))


def in_restricted_window(now: datetime, start: dtime, end: dtime) -> bool:
    current = now.time()
    if start < end:
        return start <= current < end
    return current >= start or current < end


def fetch_users(db: DB, config: dict[str, Any]) -> list[dict[str, Any]]:
    users_cfg = config.get("users", {})
    include_user_ids = [int(x) for x in users_cfg.get("include_user_ids", [])]
    exclude_user_ids = [int(x) for x in users_cfg.get("exclude_user_ids", [])]
    include_roles = [int(x) for x in users_cfg.get("include_roles", [1])]
    include_groups = [str(x) for x in users_cfg.get("include_groups", ["default"])]
    auto_manage = bool(users_cfg.get("auto_manage", True))

    conditions = ["deleted_at is null", "status = 1"]
    if auto_manage:
        if include_roles:
            conditions.append("role in (" + ",".join(str(x) for x in include_roles) + ")")
        if include_groups:
            group_parts = [
                f"(',' || \"group\" || ',') like {sql_literal('%,' + group + ',%')}"
                for group in include_groups
            ]
            conditions.append("(" + " or ".join(group_parts) + ")")
    elif include_user_ids:
        conditions.append("id in (" + ",".join(str(x) for x in include_user_ids) + ")")
    else:
        return []

    if include_user_ids and auto_manage:
        conditions.append(
            "(id in ("
            + ",".join(str(x) for x in include_user_ids)
            + ") or ("
            + conditions.pop()
            + "))"
        )
    if exclude_user_ids:
        conditions.append("id not in (" + ",".join(str(x) for x in exclude_user_ids) + ")")

    sql = f"""
select coalesce(json_agg(row_to_json(t)), '[]'::json)
from (
  select id, username, display_name, role, status, quota, used_quota, "group", remark
  from users
  where {' and '.join(conditions)}
  order by id
) t;
"""
    raw = db.psql(sql, capture=True)
    return json.loads(raw or "[]")


def quota_from_usd(usd: float, quota_per_usd: float) -> int:
    return int(round(float(usd) * quota_per_usd))


def approvals_for(config: dict[str, Any], approvals: dict[str, Any], date_key: str, user_id: int) -> float:
    users_cfg = config.get("users", {})
    user_map = users_cfg.get("per_user_extra_usd", {})
    extra = float(user_map.get(str(user_id), user_map.get(user_id, 0)) or 0)
    day = approvals.get(date_key, {}) if isinstance(approvals, dict) else {}
    if isinstance(day, dict):
        val = day.get(str(user_id), day.get(user_id, 0))
        if isinstance(val, dict):
            extra += float(val.get("extra_usd", 0) or 0)
        else:
            extra += float(val or 0)
    return extra


def update_user(
    db: DB,
    user_id: int,
    quota: int,
    remark: str,
    dry_run: bool,
) -> None:
    sql = (
        "update users set "
        f"quota = {int(quota)}, "
        f"remark = {sql_literal(remark)} "
        f"where id = {int(user_id)};"
    )
    if dry_run:
        print(f"[dry-run] user {user_id} SQL: {sql}")
        return
    db.psql(sql)


def approve_extra(config_path: Path, approvals_path: Path, user_id: int, usd: float, date_key: str | None, note: str) -> None:
    config = load_json(config_path, {})
    date_key = date_key or local_now(config).date().isoformat()
    approvals = load_json(approvals_path, {})
    day = approvals.setdefault(date_key, {})
    old = day.get(str(user_id), {})
    if not isinstance(old, dict):
        old = {"extra_usd": float(old or 0)}
    old["extra_usd"] = float(old.get("extra_usd", 0) or 0) + float(usd)
    if note:
        old["note"] = note
    old["updated_at"] = int(time.time())
    day[str(user_id)] = old
    save_json_atomic(approvals_path, approvals)
    print(f"approved user {user_id} extra ${usd:g} for {date_key}; total extra ${old['extra_usd']:g}")


def run_guard(config_path: Path, state_path: Path, approvals_path: Path, dry_run: bool, force_phase: str | None) -> int:
    config = load_json(config_path, None)
    if not isinstance(config, dict):
        raise RuntimeError(f"invalid config: {config_path}")
    approvals = load_json(approvals_path, {})
    state = load_json(state_path, {"version": 1, "users": {}})

    quota_per_usd = float(config.get("quota_per_usd", 500000))
    schedule = config.get("schedule", {})
    restricted_start = parse_hhmm(str(schedule.get("restricted_start", "09:00")))
    restricted_end = parse_hhmm(str(schedule.get("restricted_end", "18:00")))
    now = local_now(config)
    date_key = now.date().isoformat()
    phase = "restricted" if in_restricted_window(now, restricted_start, restricted_end) else "unlocked"
    if force_phase:
        phase = force_phase

    policy = config.get("policy", {})
    base_usd = float(policy.get("daytime_base_usd", 50))
    unlocked_usd = float(policy.get("unlocked_quota_usd", 405))

    db_cfg = config.get("database", {})
    db = DB(
        docker=str(config.get("docker", "/usr/bin/docker")),
        container=str(db_cfg.get("container", "new-api-postgres")),
        user=str(db_cfg.get("user", "newapi")),
        database=str(db_cfg.get("database", "new-api")),
    )
    users = fetch_users(db, config)
    state_users = state.setdefault("users", {})
    summaries: list[str] = []
    changed = False

    for user in users:
        uid = int(user["id"])
        username = user.get("username") or str(uid)
        current_quota = int(user.get("quota") or 0)
        user_state = state_users.setdefault(str(uid), {})
        last_phase = user_state.get("phase")
        last_date = user_state.get("date")
        applied_grant = int(user_state.get("applied_restricted_grant_quota", 0) or 0)

        if phase == "restricted":
            extra_usd = approvals_for(config, approvals, date_key, uid)
            target_usd = base_usd + extra_usd
            target_quota = quota_from_usd(target_usd, quota_per_usd)
            should_enter = last_phase != "restricted" or last_date != date_key
            if should_enter:
                remark = f"白天额度 ${target_usd:g}，含追加 ${extra_usd:g}；18:00 后解锁"
                update_user(db, uid, target_quota, remark, dry_run)
                user_state.update(
                    {
                        "date": date_key,
                        "phase": "restricted",
                        "applied_restricted_grant_quota": target_quota,
                        "applied_extra_usd": extra_usd,
                    }
                )
                changed = True
                summaries.append(f"user {uid} {username}: enter restricted, quota=${target_usd:g}")
                continue

            if target_quota != applied_grant:
                delta = target_quota - applied_grant
                new_quota = max(current_quota + delta, 0)
                remark = f"白天额度 ${target_usd:g}，含追加 ${extra_usd:g}；18:00 后解锁"
                update_user(db, uid, new_quota, remark, dry_run)
                user_state["applied_restricted_grant_quota"] = target_quota
                user_state["applied_extra_usd"] = extra_usd
                changed = True
                summaries.append(
                    f"user {uid} {username}: approval delta ${delta / quota_per_usd:.4f}, quota -> ${new_quota / quota_per_usd:.4f}"
                )
            else:
                summaries.append(f"user {uid} {username}: restricted, remaining=${current_quota / quota_per_usd:.4f}")
            continue

        target_quota = quota_from_usd(unlocked_usd, quota_per_usd)
        should_unlock = last_phase != "unlocked" or last_date != date_key
        if should_unlock:
            remark = f"非白天限额时段已解锁；总额度由 asxs 渠道池控制"
            update_user(db, uid, target_quota, remark, dry_run)
            user_state.update(
                {
                    "date": date_key,
                    "phase": "unlocked",
                    "applied_restricted_grant_quota": 0,
                    "applied_extra_usd": 0,
                }
            )
            changed = True
            summaries.append(f"user {uid} {username}: unlocked, quota=${unlocked_usd:g}")
        else:
            summaries.append(f"user {uid} {username}: unlocked, remaining=${current_quota / quota_per_usd:.4f}")

    if changed and not dry_run:
        save_json_atomic(state_path, state)
    elif not state_path.exists() and not dry_run:
        save_json_atomic(state_path, state)

    print(f"phase={phase} date={date_key} users={len(users)}")
    for line in summaries:
        print(line)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default=str(OPS_DIR / "user_quotas.json"))
    parser.add_argument("--state", default=str(OPS_DIR / "user_quota_state.json"))
    parser.add_argument("--approvals", default=str(OPS_DIR / "user_quota_approvals.json"))
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--force-phase", choices=["restricted", "unlocked"])
    parser.add_argument("--approve-user-id", type=int)
    parser.add_argument("--approve-usd", type=float, default=0)
    parser.add_argument("--approve-date")
    parser.add_argument("--approve-note", default="")
    args = parser.parse_args()

    config_path = Path(args.config)
    state_path = Path(args.state)
    approvals_path = Path(args.approvals)

    if args.approve_user_id:
        if args.approve_usd <= 0:
            raise RuntimeError("--approve-usd must be > 0")
        approve_extra(
            config_path,
            approvals_path,
            args.approve_user_id,
            args.approve_usd,
            args.approve_date,
            args.approve_note,
        )
        return run_guard(config_path, state_path, approvals_path, args.dry_run, None)

    return run_guard(config_path, state_path, approvals_path, args.dry_run, args.force_phase)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"user_quota_guard error: {exc}", file=sys.stderr)
        raise SystemExit(1)
