#!/usr/bin/env python3
"""Runtime balance/status guard for the LingDang qflowapi fallback channel.

The New API build can parse qflowapi /v1/usage when it returns 200, but the
upstream may return 402 while its team pool is busy. In that state we should
not keep a stale positive balance in the local router.
"""

from __future__ import annotations

import json
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

STATUS_ENABLED = 1
STATUS_MANUALLY_DISABLED = 2
STATUS_AUTO_DISABLED = 3

DEFAULT_CONFIG = {
    "docker": "/usr/bin/docker",
    "database": {"container": "new-api-postgres"},
    "channel_id": 20,
    "usage_path": "/v1/usage",
    "timeout_sec": 20,
}


def load_json(path: Path, default: Any) -> Any:
    if not path.exists():
        return default
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    out = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = deep_merge(out[key], value)
        else:
            out[key] = value
    return out


def sql_literal(value: Any) -> str:
    return "'" + str(value).replace("'", "''") + "'"


class DB:
    def __init__(self, docker: str, container: str, user: str, database: str) -> None:
        self.docker = docker
        self.container = container
        self.user = user
        self.database = database

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


def db_from_config(config: dict[str, Any]) -> DB:
    docker = str(config.get("docker") or "/usr/bin/docker")
    db_cfg = config.get("database") or {}
    container = str(db_cfg.get("container") or "new-api-postgres")
    user = db_cfg.get("user") or subprocess.check_output([docker, "exec", container, "printenv", "POSTGRES_USER"], text=True).strip()
    database = db_cfg.get("database") or subprocess.check_output([docker, "exec", container, "printenv", "POSTGRES_DB"], text=True).strip()
    return DB(docker=docker, container=container, user=str(user), database=str(database))


def fetch_channel(db: DB, channel_id: int) -> dict[str, Any]:
    sql = f"""
select coalesce(row_to_json(t)::text, '{{}}')
from (
  select id, name, key, status, base_url, setting, other_info
  from channels
  where id = {int(channel_id)}
) t;
"""
    row = db.psql(sql, capture=True)
    data = json.loads(row or "{}")
    if not data:
        raise RuntimeError(f"channel {channel_id} not found")
    return data


def parse_json_object(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    if not value:
        return {}
    try:
        data = json.loads(str(value))
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


def number(value: Any) -> float | None:
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        try:
            return float(value)
        except ValueError:
            return None
    return None


def proxy_for_urllib(proxy: str) -> urllib.request.OpenerDirector:
    proxy = str(proxy or "").strip()
    if not proxy:
        return urllib.request.build_opener()
    proxy = proxy.replace("http://mihomo:", "http://127.0.0.1:")
    proxy = proxy.replace("https://mihomo:", "http://127.0.0.1:")
    return urllib.request.build_opener(urllib.request.ProxyHandler({"http": proxy, "https": proxy}))


def parse_usage_balance(data: dict[str, Any]) -> float:
    rate_limits = data.get("rate_limits")
    if isinstance(rate_limits, list):
        for item in rate_limits:
            if isinstance(item, dict) and str(item.get("window") or "").strip() == "1d" and item.get("remaining") is not None:
                return max(float(item["remaining"]), 0.0)
        for item in rate_limits:
            if isinstance(item, dict) and item.get("remaining") is not None:
                return max(float(item["remaining"]), 0.0)
    for key in ("remaining", "balance", "total_available"):
        if data.get(key) is not None:
            return max(float(data[key]), 0.0)
    quota = data.get("quota")
    if isinstance(quota, dict) and quota.get("remaining") is not None:
        return max(float(quota["remaining"]), 0.0)
    raise RuntimeError("usage response missing remaining balance")


def fetch_usage(channel: dict[str, Any], usage_path: str, timeout: int) -> dict[str, Any]:
    key = str(channel.get("key") or "").strip()
    base_url = str(channel.get("base_url") or "").strip().rstrip("/")
    if not key:
        return {"ok": False, "reason": "empty_key"}
    if not base_url:
        return {"ok": False, "reason": "empty_base_url"}
    setting = parse_json_object(channel.get("setting"))
    opener = proxy_for_urllib(str(setting.get("proxy") or ""))
    req = urllib.request.Request(
        base_url + usage_path,
        headers={
            "Authorization": "Bearer " + key,
            "Accept": "application/json",
            "User-Agent": "new-api-lingdang-balance-guard/1.0",
        },
        method="GET",
    )
    try:
        with opener.open(req, timeout=timeout) as resp:
            status = int(getattr(resp, "status", 0))
            raw = resp.read(65536)
    except urllib.error.HTTPError as exc:
        status = int(exc.code)
        raw = exc.read(65536)
    except (urllib.error.URLError, socket.timeout, TimeoutError) as exc:
        return {"ok": False, "reason": exc.__class__.__name__}
    except Exception as exc:
        return {"ok": False, "reason": exc.__class__.__name__}

    try:
        data = json.loads(raw.decode("utf-8"))
    except Exception:
        return {"ok": False, "status": status, "reason": "invalid_json"}

    if status != 200:
        error = data.get("error") if isinstance(data, dict) else None
        error_info = error if isinstance(error, dict) else {}
        return {
            "ok": False,
            "status": status,
            "reason": f"http_{status}",
            "error_code": error_info.get("code"),
            "error_type": error_info.get("type"),
            "message": str(error_info.get("message") or "")[:180],
        }
    if not isinstance(data, dict):
        return {"ok": False, "status": status, "reason": "payload_not_object"}
    try:
        balance = parse_usage_balance(data)
    except Exception as exc:
        return {"ok": False, "status": status, "reason": str(exc)[:180]}
    return {"ok": True, "status": status, "reason": "usage_balance_ok", "balance": round(balance, 6), "usage": data}


def quota_source_window(
    name: str,
    unit: str,
    remaining: float,
    limit: float | None,
    used: float | None,
    reset_at: str | None = None,
) -> dict[str, Any]:
    window: dict[str, Any] = {
        "name": name,
        "unit": unit,
        "remaining": round(max(remaining, 0.0), 6),
    }
    if limit is not None and limit > 0:
        window["limit"] = round(limit, 6)
        window["remaining_percent"] = round(max(remaining, 0.0) / limit * 100, 4)
    if used is not None:
        window["used"] = round(max(used, 0.0), 6)
    if reset_at:
        window["reset_at"] = reset_at
    return window


def build_usage_quota_source(result: dict[str, Any], balance: float, reason: str, now: int) -> dict[str, Any]:
    usage = result.get("usage")
    if not isinstance(usage, dict):
        usage = {}
    unit = str(usage.get("unit") or "USD").strip() or "USD"
    windows: list[dict[str, Any]] = []

    quota = usage.get("quota")
    if isinstance(quota, dict):
        remaining = number(quota.get("remaining"))
        if remaining is not None:
            limit = number(quota.get("limit"))
            used = number(quota.get("used"))
            if used is None and limit is not None:
                used = max(limit - remaining, 0.0)
            windows.append(
                quota_source_window(
                    "period",
                    str(quota.get("unit") or unit).strip() or unit,
                    remaining,
                    limit,
                    used,
                    str(quota.get("reset_at") or "").strip() or None,
                )
            )

    rate_limits = usage.get("rate_limits")
    if isinstance(rate_limits, list):
        for item in rate_limits:
            if not isinstance(item, dict):
                continue
            remaining = number(item.get("remaining"))
            if remaining is None:
                continue
            limit = number(item.get("limit"))
            used = number(item.get("used"))
            if used is None and limit is not None:
                used = max(limit - remaining, 0.0)
            name = str(item.get("window") or "rate_limit").strip() or "rate_limit"
            windows.append(
                quota_source_window(
                    name,
                    unit,
                    remaining,
                    limit,
                    used,
                    str(item.get("reset_at") or "").strip() or None,
                )
            )

    spendable = bool(result.get("ok")) and balance > 0
    source_type = "period_cap_with_daily_limit" if isinstance(rate_limits, list) and rate_limits else "stored_value_usd"
    return {
        "source_type": source_type,
        "unit": unit,
        "balance": round(max(balance, 0.0), 6),
        "windows": windows,
        "spendable": spendable,
        "status": "available" if spendable else "quota_exhausted" if result.get("ok") else "unknown",
        "status_reason": reason,
        "updated_at": now,
        "raw_source": {
            "source": "usage_balance",
            "mode": usage.get("mode"),
            "plan_name": usage.get("planName"),
        },
    }


def update_channel(db: DB, channel: dict[str, Any], result: dict[str, Any], dry_run: bool = False) -> str:
    now = int(time.time())
    cid = int(channel["id"])
    current_status = int(channel.get("status") or 0)
    other_info = parse_json_object(channel.get("other_info"))
    manually_disabled = current_status == STATUS_MANUALLY_DISABLED

    ok = bool(result.get("ok"))
    balance = float(result.get("balance") or 0) if ok else 0.0
    desired_enabled = ok and balance > 0
    if ok:
        reason = "usage_balance_ok" if balance > 0 else "usage_balance_zero"
    elif result.get("status") == 402 and result.get("error_code") == "insufficient_quota":
        reason = "upstream_usage_402_insufficient_quota"
    else:
        reason = "usage_balance_unavailable"

    guard_info = {
        "managed": True,
        "desired_enabled": desired_enabled,
        "reason": reason,
        "updated_at": now,
        "health": {k: v for k, v in result.items() if k != "body"},
    }
    if manually_disabled:
        guard_info["manual_status_preserved"] = True
    other_info["lingdang_balance_guard"] = guard_info
    other_info["quota_source"] = build_usage_quota_source(result, balance, reason, now)

    status: int | None = None
    abilities_enabled: bool | None = None
    balance_update: float | None = None
    if desired_enabled:
        balance_update = balance
        if not manually_disabled and current_status != STATUS_ENABLED:
            status = STATUS_ENABLED
            abilities_enabled = True
    elif reason == "upstream_usage_402_insufficient_quota" or (ok and balance <= 0):
        balance_update = 0.0
        if not manually_disabled and current_status != STATUS_AUTO_DISABLED:
            status = STATUS_AUTO_DISABLED
            abilities_enabled = False

    statements = ["begin;"]
    sets = ["other_info = " + sql_literal(json.dumps(other_info, ensure_ascii=False, sort_keys=True))]
    if balance_update is not None:
        sets.append(f"balance = {float(balance_update):.6f}")
        sets.append(f"balance_updated_time = {now}")
    if status is not None:
        sets.append(f"status = {int(status)}")
    statements.append(f"update channels set {', '.join(sets)} where id = {cid};")
    if abilities_enabled is not None:
        statements.append(f"update abilities set enabled = {'true' if abilities_enabled else 'false'} where channel_id = {cid};")
    statements.append("commit;")
    if dry_run:
        print("[dry-run] " + " ".join(statements))
    else:
        db.psql("\n".join(statements))

    if desired_enabled:
        return f"channel {cid} {channel.get('name')}: enabled balance={balance:.6f}"
    if reason == "upstream_usage_402_insufficient_quota":
        return f"channel {cid} {channel.get('name')}: auto-disabled balance=0 ({reason})"
    return f"channel {cid} {channel.get('name')}: unchanged ({reason})"


def main() -> int:
    config_path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("/opt/new-api/ops/lingdang_balance_guard.json")
    config = deep_merge(DEFAULT_CONFIG, load_json(config_path, {}))
    db = db_from_config(config)
    channel = fetch_channel(db, int(config["channel_id"]))
    result = fetch_usage(channel, str(config.get("usage_path") or "/v1/usage"), int(config.get("timeout_sec") or 20))
    print(update_channel(db, channel, result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
