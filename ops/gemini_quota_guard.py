#!/usr/bin/env python3
"""Gemini channel balance guard for New API (CLIProxyAPI Home source).

The New API gemini channel consumes antigravity accounts managed by Home. Home
tracks per-account quota windows (5h / weekly) for the "gemini" scope, but
nothing feeds those numbers into New API, so channels.balance stays empty and
the dashboard shows 0.

This guard reads Home's SQLite database directly (read-only), converts the
remaining gemini quota percentage into a USD estimate, and writes it to the
channel balance. It never overwrites the balance with 0 when Home has no data,
and it applies a configurable floor so an approximate conversion cannot make a
healthy channel look empty.

Run with --dry-run to inspect the computed value without writing.
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from typing import Any

DEFAULT_CONFIG = {
    "docker": "/usr/bin/docker",
    "database": {"container": "new-api-postgres"},
    "channel_id": 35,
    "home_db": "/opt/cliproxyapi-home/data/home.db",
    "provider": "antigravity",
    "window_match": "gemini",
    "usd_per_percent": 1.0,
    "min_balance_usd": 1.0,
    "manage_status": False,
    "state_path": "/opt/new-api/ops/gemini_quota_guard_state.json",
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


def read_home_windows(cfg: dict[str, Any]) -> list[dict[str, Any]]:
    db_path = cfg["home_db"]
    uri = f"file:{db_path}?mode=ro"
    con = sqlite3.connect(uri, uri=True, timeout=10)
    try:
        rows = con.execute(
            """
            SELECT a.label, a.disabled, a.status, q.window_id, q.used_ratio,
                   q.remaining_ratio, q.reset_at, q.observed_at
            FROM quota_window q
            JOIN auth a ON a.uuid = q.credential_id
            WHERE a.provider = ?
              AND q.window_id LIKE ?
            ORDER BY a.label, q.window_id
            """,
            (cfg["provider"], f"%{cfg['window_match']}%"),
        ).fetchall()
    finally:
        con.close()
    keys = ("label", "disabled", "status", "window_id", "used_ratio",
            "remaining_ratio", "reset_at", "observed_at")
    return [dict(zip(keys, row)) for row in rows]


def pick_windows(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Prefer the weekly window per account; fall back to any gemini window."""
    per_account: dict[str, dict[str, Any]] = {}
    for row in rows:
        label = row["label"]
        current = per_account.get(label)
        if current is None:
            per_account[label] = row
            continue
        if "weekly" in (row["window_id"] or "") and "weekly" not in (current["window_id"] or ""):
            per_account[label] = row
    return list(per_account.values())


def compute_balance(cfg: dict[str, Any], windows: list[dict[str, Any]]) -> tuple[float, list[dict[str, Any]]]:
    usable: list[dict[str, Any]] = []
    for row in windows:
        if int(row["disabled"] or 0) != 0:
            continue
        used = row["used_ratio"]
        if used is None:
            continue
        used = float(used)
        if used >= 1.0:
            continue
        usable.append({**row, "remaining_percent": max(0.0, (1.0 - used) * 100.0)})
    total = sum(item["remaining_percent"] for item in usable) * float(cfg["usd_per_percent"])
    total = round(total, 6)
    floor = float(cfg["min_balance_usd"])
    if usable and total < floor:
        total = floor
    return total, usable


def write_balance(cfg: dict[str, Any], balance: float) -> None:
    db = cfg["database"]
    sql = f"update channels set balance = {balance:.6f} where id = {int(cfg['channel_id'])};"
    cmd = [
        cfg["docker"], "exec", db["container"],
        "psql", "-U", db.get("user", "postgres"), "-d", db.get("database", "new-api"),
        "-v", "ON_ERROR_STOP=1", "-c", sql,
    ]
    proc = subprocess.run(cmd, check=False, capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip())


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    cfg = load_config(args.config)
    windows = pick_windows(read_home_windows(cfg))
    if not windows:
        print("gemini_quota_guard: no quota rows from Home; keeping channel balance unchanged")
        return 0

    balance, usable = compute_balance(cfg, windows)
    detail = ", ".join(
        f"{item['label'].split('@')[0]}={item['remaining_percent']:.2f}%"
        for item in sorted(usable, key=lambda x: -x["remaining_percent"])
    )
    print(f"gemini_quota_guard: usable={len(usable)} balance=${balance:.6f} ({detail or 'none'})")

    state = {
        "computed_at": datetime.now().isoformat(timespec="seconds"),
        "balance_usd": balance,
        "usable_accounts": [
            {"label": item["label"], "window": item["window_id"],
             "remaining_percent": round(item["remaining_percent"], 4),
             "reset_at": item["reset_at"]}
            for item in usable
        ],
    }
    state_path = Path(cfg["state_path"])
    if args.dry_run:
        print(json.dumps(state, ensure_ascii=False, indent=2))
        return 0

    write_balance(cfg, balance)
    state_path.write_text(json.dumps(state, ensure_ascii=False), encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
