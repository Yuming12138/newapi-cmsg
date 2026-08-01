#!/usr/bin/env python3
"""Synchronize the unified DeepSeek channel balance for New API."""

from __future__ import annotations

import json
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


@dataclass
class DB:
    docker: str = "/usr/bin/docker"
    container: str = "new-api-postgres"
    user: str = "newapi"
    database: str = "new-api"

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


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def normalize_url(value: Any) -> str:
    return str(value or "").strip().rstrip("/").lower()


def group_contains(group_value: Any, target: str) -> bool:
    return target in [part.strip() for part in str(group_value or "").split(",")]


def fetch_channels(db: DB) -> list[dict[str, Any]]:
    raw = db.psql(
        """
select coalesce(json_agg(row_to_json(t)), '[]'::json)
from (
  select id, name, key, type, base_url, "group", status
  from channels
  where type = 43
  order by id
) t;
""",
        capture=True,
    )
    rows = json.loads(raw or "[]")
    return [
        row
        for row in rows
        if normalize_url(row.get("base_url")) == "https://api.deepseek.com"
        and group_contains(row.get("group"), "deepseek")
    ]


def fetch_balance(api_key: str, timeout: int = 15) -> float:
    req = urllib.request.Request(
        "https://api.deepseek.com/user/balance",
        headers={
            "Authorization": f"Bearer {api_key}",
            "Accept": "application/json",
            "User-Agent": "new-api-deepseek-balance-guard/1.0",
        },
        method="GET",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = getattr(resp, "status", 0)
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        body = exc.read(240).decode("utf-8", "replace")
        raise RuntimeError(f"deepseek balance http {exc.code}: {body}") from exc
    if status != 200:
        raise RuntimeError(f"deepseek balance http {status}")
    data = json.loads(raw.decode("utf-8"))
    if not data.get("is_available", False):
        raise RuntimeError("deepseek balance is unavailable")
    for item in data.get("balance_infos", []):
        if item.get("currency") == "CNY":
            return float(item.get("total_balance") or 0)
    raise RuntimeError("deepseek CNY balance not found")


def update_channel(db: DB, channel_id: int, balance_cny: float, now_ts: int) -> None:
    remark = "DeepSeek 统一渠道；支持 Chat Completions、Responses 与 Messages；余额单位 CNY，余额自动同步自 /user/balance"
    db.psql(
        "update channels set "
        f"balance = {balance_cny:.6f}, "
        f"balance_updated_time = {now_ts}, "
        f"remark = {sql_literal(remark)} "
        f"where id = {int(channel_id)};"
    )


def main() -> int:
    db = DB()
    now_ts = int(time.time())
    channels = fetch_channels(db)
    if not channels:
        print("no deepseek channels found")
        return 0
    for channel in channels:
        cid = int(channel["id"])
        name = channel.get("name") or str(cid)
        try:
            balance = fetch_balance(str(channel.get("key") or ""))
            update_channel(db, cid, balance, now_ts)
            print(f"channel {cid} {name}: balance CNY {balance:.2f}")
        except Exception as exc:
            print(f"channel {cid} {name}: balance sync failed: {exc}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"deepseek_balance_guard error: {exc}", file=sys.stderr)
        raise SystemExit(1)
