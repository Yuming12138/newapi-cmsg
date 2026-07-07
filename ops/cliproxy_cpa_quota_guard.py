#!/usr/bin/env python3
"""Runtime quota guard for the New API CLIProxyAPI Codex channel.

The channel consumes a shared CPA Codex account whose upstream quota is reported
as rolling 5h and 7d windows. New API only understands channel balance/status,
so this script polls CPA's management API, reads the upstream Codex wham usage
payload, and toggles the New API channel when the shared account drops below the
configured low-watermark thresholds.
"""

from __future__ import annotations

import base64
import hashlib
import json
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

STATUS_ENABLED = 1
STATUS_MANUALLY_DISABLED = 2
STATUS_AUTO_DISABLED = 3

WINDOW_5H_SECONDS = 5 * 60 * 60
WINDOW_7D_SECONDS = 7 * 24 * 60 * 60

DEFAULT_CONFIG = {
    "docker": "/usr/bin/docker",
    "database": {"container": "new-api-postgres"},
    "channel_id": 12,
    "env_path": "/opt/new-api/ops/cliproxy_cpa_quota_guard.env",
    "state_path": "/opt/new-api/ops/cliproxy_cpa_quota_guard_state.json",
    "cpa_base_url": "http://127.0.0.1:8317",
    "wham_usage_url": "https://chatgpt.com/backend-api/wham/usage",
    "timeout_sec": 30,
    "enabled": True,
    "min_remaining_percent_5h": 30.0,
    "min_remaining_percent_7d": 20.0,
    "fail_closed_after_consecutive_failures": 3,
    "balance_units_per_percent": 1.0,
    "personal_plan_keywords": ["plus"],
    "protected_plan_keywords": ["pro"],
    "default_account_bucket": "protected",
    "account_bucket_overrides": {},
}

OPTION_CONFIG_MAP = {
    "cliproxy_cpa_quota_guard.enabled": ("enabled", "bool"),
    "cliproxy_cpa_quota_guard.min_remaining_percent_5h": ("min_remaining_percent_5h", "float"),
    "cliproxy_cpa_quota_guard.min_remaining_percent_7d": ("min_remaining_percent_7d", "float"),
}


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
    Path(tmp).replace(path)


def deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    out = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = deep_merge(out[key], value)
        else:
            out[key] = value
    return out


def load_env_values(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    if not path.exists():
        return values
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip('"').strip("'")
    return values


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


def bool_value(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0
    if isinstance(value, str):
        return value.strip().lower() in {"1", "true", "yes", "y", "on"}
    return default


def clamp_percent(value: Any, default: float) -> float:
    parsed = number(value)
    if parsed is None:
        parsed = default
    return max(0.0, min(100.0, float(parsed)))


def load_option_overrides(db: DB) -> dict[str, Any]:
    keys = ", ".join(sql_literal(key) for key in OPTION_CONFIG_MAP)
    sql = f"""
select key || chr(9) || value
from options
where key in ({keys});
"""
    output = db.psql(sql, capture=True)
    overrides: dict[str, Any] = {}
    for line in output.splitlines():
        if "\t" not in line:
            continue
        key, value = line.split("\t", 1)
        config_key, value_type = OPTION_CONFIG_MAP.get(key, ("", ""))
        if not config_key:
            continue
        if value_type == "bool":
            overrides[config_key] = bool_value(value)
        elif value_type == "float":
            parsed = number(value)
            if parsed is not None:
                overrides[config_key] = parsed
    return overrides


def fetch_channel(db: DB, channel_id: int) -> dict[str, Any]:
    sql = f"""
select coalesce(row_to_json(t)::text, '{{}}')
from (
  select id, name, status, balance, balance_updated_time, other_info
  from channels
  where id = {int(channel_id)}
) t;
"""
    data = json.loads(db.psql(sql, capture=True) or "{}")
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


def is_loopback_base_url(base_url: str) -> bool:
    try:
        hostname = urllib.parse.urlparse(base_url).hostname or ""
    except Exception:
        return False
    return hostname == "localhost" or hostname == "::1" or hostname.startswith("127.")


def management_headers(env: dict[str, str], base_url: str = "") -> dict[str, str]:
    headers = {"Accept": "application/json"}
    key = env.get("CPA_MANAGEMENT_KEY", "").strip()
    username = env.get("CPA_BASIC_USERNAME", "").strip()
    password = env.get("CPA_BASIC_PASSWORD", "")
    if username and password and not (key and is_loopback_base_url(base_url)):
        token = base64.b64encode(f"{username}:{password}".encode("utf-8")).decode("ascii")
        headers["Authorization"] = "Basic " + token
        if key:
            headers["X-Management-Key"] = key
    elif key:
        headers["Authorization"] = "Bearer " + key
    return headers


def request_json(url: str, headers: dict[str, str], timeout: int, data: Any | None = None) -> dict[str, Any]:
    body = None
    req_headers = dict(headers)
    method = "GET"
    if data is not None:
        body = json.dumps(data).encode("utf-8")
        req_headers["Content-Type"] = "application/json"
        method = "POST"
    req = urllib.request.Request(url, data=body, headers=req_headers, method=method)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read(1024 * 1024)
        payload = json.loads(raw.decode("utf-8"))
        if not isinstance(payload, dict):
            raise RuntimeError("payload_not_object")
        return payload


def parse_jwt_payload(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    if not isinstance(value, str):
        return {}
    raw = value.strip()
    parts = raw.split(".")
    if len(parts) < 2:
        return {}
    padded = parts[1] + "=" * ((4 - len(parts[1]) % 4) % 4)
    try:
        data = json.loads(base64.urlsafe_b64decode(padded.encode("ascii")))
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


def nested(data: dict[str, Any], *keys: str) -> Any:
    cur: Any = data
    for key in keys:
        if not isinstance(cur, dict):
            return None
        cur = cur.get(key)
    return cur


def first_non_empty(*values: Any) -> Any:
    for value in values:
        if value not in (None, ""):
            return value
    return None


def account_id_from_entry(entry: dict[str, Any]) -> str:
    claims = parse_jwt_payload(
        first_non_empty(entry.get("id_token"), nested(entry, "metadata", "id_token"), nested(entry, "attributes", "id_token"))
    )
    account_id = first_non_empty(
        entry.get("account_id"),
        entry.get("accountId"),
        nested(entry, "metadata", "account_id"),
        nested(entry, "metadata", "accountId"),
        nested(entry, "attributes", "account_id"),
        nested(entry, "attributes", "accountId"),
        claims.get("chatgpt_account_id"),
        nested(claims, "https://api.openai.com/auth", "chatgpt_account_id"),
    )
    return str(account_id or "").strip()


def plan_type_from_entry(entry: dict[str, Any], usage_payload: dict[str, Any] | None = None) -> str:
    claims = parse_jwt_payload(
        first_non_empty(entry.get("id_token"), nested(entry, "metadata", "id_token"), nested(entry, "attributes", "id_token"))
    )
    value = first_non_empty(
        usage_payload.get("plan_type") if usage_payload else None,
        usage_payload.get("planType") if usage_payload else None,
        entry.get("plan_type"),
        entry.get("planType"),
        nested(entry, "metadata", "plan_type"),
        nested(entry, "attributes", "plan_type"),
        claims.get("chatgpt_plan_type"),
        nested(claims, "https://api.openai.com/auth", "chatgpt_plan_type"),
    )
    return str(value or "unknown").strip() or "unknown"


def account_label_from_entry(entry: dict[str, Any]) -> str:
    value = first_non_empty(entry.get("label"), entry.get("name"), entry.get("email"), entry.get("account"))
    return str(value or "").strip()


def reset_credits_available(usage: dict[str, Any]) -> int | None:
    credits = first_non_empty(
        nested(usage, "rate_limit_reset_credits", "available_count"),
        nested(usage, "rateLimitResetCredits", "availableCount"),
    )
    parsed = number(credits)
    if parsed is None:
        return None
    return max(0, int(parsed))


def codex_auth_entries(auth_files_payload: dict[str, Any]) -> list[dict[str, Any]]:
    files = auth_files_payload.get("files")
    if not isinstance(files, list):
        raise RuntimeError("auth_files_missing_files")
    codex = [
        item
        for item in files
        if isinstance(item, dict) and str(item.get("provider") or item.get("type") or "").strip().lower() == "codex"
    ]
    if not codex:
        raise RuntimeError("codex_auth_not_found")
    return codex


def auth_is_unavailable(entry: dict[str, Any]) -> bool:
    return bool(entry.get("disabled") or entry.get("unavailable"))


def normalize_bucket(value: Any) -> str:
    raw = str(value or "").strip().lower().replace("-", "_")
    if raw in {"personal", "expendable", "plus", "owned", "own"}:
        return "personal"
    if raw in {"protected", "shared", "shared_pro", "pro", "reserved"}:
        return "protected"
    return ""


def string_list(value: Any) -> list[str]:
    if isinstance(value, list):
        return [str(item).strip().lower() for item in value if str(item).strip()]
    if isinstance(value, str):
        return [item.strip().lower() for item in value.split(",") if item.strip()]
    return []


def classify_account_bucket(
    config: dict[str, Any],
    auth_entry: dict[str, Any],
    usage_payload: dict[str, Any] | None,
    account_id_hash: str,
    auth_index: str,
) -> str:
    overrides = config.get("account_bucket_overrides")
    if not isinstance(overrides, dict):
        overrides = {}
    candidates = [
        account_id_hash,
        "hash:" + account_id_hash,
        "auth_index:" + auth_index,
        str(auth_index),
        str(first_non_empty(auth_entry.get("name"), auth_entry.get("label")) or "").strip(),
    ]
    for key in candidates:
        if key and key in overrides:
            bucket = normalize_bucket(overrides.get(key))
            if bucket:
                return bucket

    plan_type = plan_type_from_entry(auth_entry, usage_payload).lower()
    for keyword in string_list(config.get("personal_plan_keywords")):
        if keyword and keyword in plan_type:
            return "personal"
    for keyword in string_list(config.get("protected_plan_keywords")):
        if keyword and keyword in plan_type:
            return "protected"
    return normalize_bucket(config.get("default_account_bucket")) or "protected"


def account_identity(auth_entry: dict[str, Any]) -> tuple[str, str, str]:
    auth_index = str(first_non_empty(auth_entry.get("auth_index"), auth_entry.get("authIndex")) or "").strip()
    account_id = account_id_from_entry(auth_entry)
    account_id_hash = hashlib.sha256(account_id.encode("utf-8")).hexdigest()[:12] if account_id else ""
    return auth_index, account_id, account_id_hash


def call_wham_usage_for_auth(
    config: dict[str, Any],
    base_url: str,
    headers: dict[str, str],
    timeout: int,
    auth_entry: dict[str, Any],
) -> dict[str, Any]:
    auth_index, account_id, account_id_hash = account_identity(auth_entry)
    if not auth_index:
        raise RuntimeError("missing_auth_index")
    if not account_id:
        raise RuntimeError("missing_account_id")

    payload = {
        "auth_index": auth_index,
        "method": "GET",
        "url": str(config.get("wham_usage_url") or DEFAULT_CONFIG["wham_usage_url"]),
        "header": {
            "Authorization": "Bearer $TOKEN$",
            "Content-Type": "application/json",
            "User-Agent": "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal",
            "Chatgpt-Account-Id": account_id,
        },
    }
    response = request_json(base_url + "/v0/management/api-call", headers, timeout, payload)
    status = int(first_non_empty(response.get("status_code"), response.get("statusCode"), 0) or 0)
    body = response.get("body") or ""
    if status < 200 or status >= 300:
        raise RuntimeError(f"wham_usage_http_{status}")
    try:
        usage = json.loads(body) if isinstance(body, str) else body
    except Exception as exc:
        raise RuntimeError("wham_usage_invalid_json") from exc
    if not isinstance(usage, dict):
        raise RuntimeError("wham_usage_payload_not_object")
    usage["_guard_auth"] = {
        "auth_index": auth_index,
        "account_id_hash": hashlib.sha256(account_id.encode("utf-8")).hexdigest()[:12],
        "plan_type_hint": plan_type_from_entry(auth_entry, usage),
    }
    return usage


def call_wham_usages(config: dict[str, Any], env: dict[str, str]) -> list[dict[str, Any]]:
    timeout = int(config.get("timeout_sec") or 30)
    base_url = str(config.get("cpa_base_url") or "").rstrip("/")
    if not base_url:
        raise RuntimeError("empty_cpa_base_url")
    headers = management_headers(env, base_url)
    if not headers.get("Authorization") and not headers.get("X-Management-Key"):
        raise RuntimeError("missing_cpa_management_credentials")

    auth_payload = request_json(base_url + "/v0/management/auth-files", headers, timeout)
    entries = codex_auth_entries(auth_payload)
    accounts: list[dict[str, Any]] = []
    successful = 0

    for auth_entry in entries:
        auth_index, _, account_id_hash = account_identity(auth_entry)
        bucket = classify_account_bucket(config, auth_entry, None, account_id_hash, auth_index)
        base_account = {
            "auth_index": auth_index,
            "account_id_hash": account_id_hash,
            "account_label": account_label_from_entry(auth_entry),
            "plan_type": plan_type_from_entry(auth_entry),
            "bucket": bucket,
            "disabled": bool(auth_entry.get("disabled")),
            "unavailable": bool(auth_entry.get("unavailable")),
            "reset_credits_available": None,
        }
        if auth_is_unavailable(auth_entry):
            accounts.append({**base_account, "ok": False, "skipped": True, "reason": "auth_unavailable"})
            continue
        try:
            usage = call_wham_usage_for_auth(config, base_url, headers, timeout, auth_entry)
            account = evaluate_account_quota(config, auth_entry, usage)
            accounts.append(account)
            successful += 1
        except Exception as exc:
            accounts.append({**base_account, "ok": False, "error": str(exc)[:180]})

    if successful == 0 and accounts and all(item.get("skipped") for item in accounts):
        return accounts
    if successful == 0:
        errors = [str(item.get("error") or item.get("reason") or "unknown") for item in accounts]
        raise RuntimeError("wham_usage_all_accounts_failed: " + "; ".join(errors[:3]))
    return accounts


def number(value: Any) -> float | None:
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        try:
            return float(value)
        except ValueError:
            return None
    return None


def quota_windows(usage: dict[str, Any]) -> dict[str, dict[str, Any]]:
    rate_limit = first_non_empty(usage.get("rate_limit"), usage.get("rateLimit"))
    if not isinstance(rate_limit, dict):
        raise RuntimeError("missing_rate_limit")
    candidates = [
        first_non_empty(rate_limit.get("primary_window"), rate_limit.get("primaryWindow")),
        first_non_empty(rate_limit.get("secondary_window"), rate_limit.get("secondaryWindow")),
    ]
    out: dict[str, dict[str, Any]] = {}
    for fallback_name, item in zip(("5h", "7d"), candidates):
        if not isinstance(item, dict):
            continue
        duration = int(number(first_non_empty(item.get("limit_window_seconds"), item.get("limitWindowSeconds"))) or 0)
        name = fallback_name
        if duration == WINDOW_5H_SECONDS:
            name = "5h"
        elif duration == WINDOW_7D_SECONDS:
            name = "7d"
        used = number(first_non_empty(item.get("used_percent"), item.get("usedPercent")))
        reset_at = number(first_non_empty(item.get("reset_at"), item.get("resetAt")))
        reset_after = number(first_non_empty(item.get("reset_after_seconds"), item.get("resetAfterSeconds")))
        out[name] = {
            "duration_seconds": duration,
            "used_percent": used,
            "remaining_percent": None if used is None else max(0.0, min(100.0, 100.0 - used)),
            "reset_at": int(reset_at) if reset_at else None,
            "reset_after_seconds": int(reset_after) if reset_after else None,
        }
    if "5h" not in out or "7d" not in out:
        raise RuntimeError("missing_required_quota_windows")
    return out


def account_window_remaining(windows: dict[str, dict[str, Any]], key: str) -> float:
    value = number(windows.get(key, {}).get("remaining_percent"))
    if value is None:
        raise RuntimeError(f"missing_{key}_remaining_percent")
    return max(0.0, min(100.0, value))


def evaluate_account_quota(config: dict[str, Any], auth_entry: dict[str, Any], usage: dict[str, Any]) -> dict[str, Any]:
    windows = quota_windows(usage)
    threshold_5h = clamp_percent(config.get("min_remaining_percent_5h"), 30.0)
    threshold_7d = clamp_percent(config.get("min_remaining_percent_7d"), 20.0)
    remaining_5h = account_window_remaining(windows, "5h")
    remaining_7d = account_window_remaining(windows, "7d")
    headroom_5h = remaining_5h - threshold_5h
    headroom_7d = remaining_7d - threshold_7d
    guard_auth = usage.get("_guard_auth") if isinstance(usage.get("_guard_auth"), dict) else {}
    auth_index = str(guard_auth.get("auth_index") or "").strip()
    account_id_hash = str(guard_auth.get("account_id_hash") or "").strip()
    bucket = classify_account_bucket(config, auth_entry, usage, account_id_hash, auth_index)
    can_exhaust = bucket == "personal"
    raw_remaining = min(remaining_5h, remaining_7d)
    protected_headroom = max(0.0, min(headroom_5h, headroom_7d))
    visible_remaining = raw_remaining if can_exhaust else protected_headroom
    units_per_percent = float(config.get("balance_units_per_percent") or 1.0)
    balance_units = raw_remaining * units_per_percent
    usable_balance_units = visible_remaining * units_per_percent
    return {
        "ok": True,
        "auth_index": auth_index,
        "account_id_hash": account_id_hash,
        "account_label": account_label_from_entry(auth_entry),
        "bucket": bucket,
        "can_exhaust": can_exhaust,
        "min_remaining_percent_5h": threshold_5h,
        "min_remaining_percent_7d": threshold_7d,
        "remaining_headroom_percent": round(min(headroom_5h, headroom_7d), 6),
        "remaining_share_percent": round(visible_remaining, 6),
        "raw_remaining_percent": round(raw_remaining, 6),
        "balance_units": round(balance_units, 6),
        "usable_balance_units": round(usable_balance_units, 6),
        "plan_type": usage.get("plan_type") or usage.get("planType") or guard_auth.get("plan_type_hint"),
        "reset_credits_available": reset_credits_available(usage),
        "windows": windows,
        "rate_limit_reached_type": usage.get("rate_limit_reached_type"),
    }


def aggregate_windows(accounts: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    out: dict[str, dict[str, Any]] = {}
    for name in ("5h", "7d"):
        items = [
            account.get("windows", {}).get(name)
            for account in accounts
            if account.get("ok") and isinstance(account.get("windows"), dict) and isinstance(account.get("windows", {}).get(name), dict)
        ]
        if not items:
            continue
        remaining_values = [number(item.get("remaining_percent")) for item in items]
        used_values = [number(item.get("used_percent")) for item in items]
        reset_after_values = [number(item.get("reset_after_seconds")) for item in items]
        reset_at_values = [number(item.get("reset_at")) for item in items]
        remaining = [value for value in remaining_values if value is not None]
        used = [value for value in used_values if value is not None]
        reset_after = [value for value in reset_after_values if value is not None and value >= 0]
        reset_at = [value for value in reset_at_values if value is not None and value > 0]
        duration = number(first_non_empty(*[item.get("duration_seconds") for item in items]))
        out[name] = {
            "duration_seconds": int(duration) if duration else WINDOW_5H_SECONDS if name == "5h" else WINDOW_7D_SECONDS,
            "used_percent": round(sum(used) / len(used), 6) if used else None,
            "remaining_percent": round(sum(remaining) / len(remaining), 6) if remaining else None,
            "reset_after_seconds": int(min(reset_after)) if reset_after else None,
            "reset_at": int(min(reset_at)) if reset_at else None,
        }
    return out


def empty_bucket(key: str, config: dict[str, Any]) -> dict[str, Any]:
    can_exhaust = key == "personal"
    return {
        "bucket": key,
        "label": "个人池" if can_exhaust else "共享 Pro",
        "can_exhaust": can_exhaust,
        "account_count": 0,
        "available_account_count": 0,
        "balance_units": 0.0,
        "usable_balance_units": 0.0,
        "min_remaining_percent_5h": None if can_exhaust else clamp_percent(config.get("min_remaining_percent_5h"), 30.0),
        "min_remaining_percent_7d": None if can_exhaust else clamp_percent(config.get("min_remaining_percent_7d"), 20.0),
        "reset_credits_available": None,
        "accounts": [],
        "windows": {},
    }


def account_summary(account: dict[str, Any]) -> dict[str, Any]:
    keys = [
        "auth_index",
        "account_id_hash",
        "account_label",
        "plan_type",
        "bucket",
        "ok",
        "can_exhaust",
        "disabled",
        "unavailable",
        "skipped",
        "reason",
        "error",
        "balance_units",
        "usable_balance_units",
        "remaining_share_percent",
        "raw_remaining_percent",
        "reset_credits_available",
        "windows",
    ]
    return {key: account.get(key) for key in keys if key in account}


def bucket_summary(config: dict[str, Any], bucket_key: str, accounts: list[dict[str, Any]]) -> dict[str, Any]:
    summary = empty_bucket(bucket_key, config)
    summary["account_count"] = len(accounts)
    ok_accounts = [account for account in accounts if account.get("ok")]
    summary["available_account_count"] = len(ok_accounts)
    summary["balance_units"] = round(sum(float(account.get("balance_units") or 0) for account in ok_accounts), 6)
    summary["usable_balance_units"] = round(sum(float(account.get("usable_balance_units") or 0) for account in ok_accounts), 6)
    summary["remaining_share_percent"] = round(sum(float(account.get("remaining_share_percent") or 0) for account in ok_accounts), 6)
    summary["raw_remaining_percent"] = round(sum(float(account.get("raw_remaining_percent") or 0) for account in ok_accounts), 6)
    reset_values = [
        int(account.get("reset_credits_available"))
        for account in ok_accounts
        if account.get("reset_credits_available") is not None
    ]
    summary["reset_credits_available"] = sum(reset_values) if reset_values else None
    summary["accounts"] = [account_summary(account) for account in accounts]
    summary["windows"] = aggregate_windows(ok_accounts)
    plans: dict[str, int] = {}
    for account in accounts:
        plan = str(account.get("plan_type") or "unknown").strip() or "unknown"
        plans[plan] = plans.get(plan, 0) + 1
    summary["plans"] = plans
    return summary


def evaluate_quota(config: dict[str, Any], accounts: list[dict[str, Any]]) -> dict[str, Any]:
    enabled = bool_value(config.get("enabled"), True)
    buckets: dict[str, dict[str, Any]] = {}
    for key in ("personal", "protected"):
        bucket_accounts = [account for account in accounts if account.get("bucket") == key]
        if bucket_accounts:
            buckets[key] = bucket_summary(config, key, bucket_accounts)
    ok_accounts = [account for account in accounts if account.get("ok")]
    usable_balance_units = round(sum(float(account.get("usable_balance_units") or 0) for account in ok_accounts), 6)
    total_balance_units = round(sum(float(account.get("balance_units") or 0) for account in ok_accounts), 6)
    quota_ok = (not enabled) or usable_balance_units > 0
    windows = aggregate_windows(ok_accounts)
    return {
        "ok": True,
        "quota_ok": quota_ok,
        "within_share": quota_ok,
        "reason": "usable_balance_available" if quota_ok else "quota_low_watermark_reached",
        "guard_mode": "bucket_low_watermark",
        "enabled": enabled,
        "low_watermark_enabled": enabled,
        "min_remaining_percent_5h": clamp_percent(config.get("min_remaining_percent_5h"), 30.0),
        "min_remaining_percent_7d": clamp_percent(config.get("min_remaining_percent_7d"), 20.0),
        "account_count": len(accounts),
        "available_account_count": len(ok_accounts),
        "remaining_share_percent": usable_balance_units,
        "balance_units": usable_balance_units,
        "usable_balance_units": usable_balance_units,
        "total_balance_units": total_balance_units,
        "buckets": buckets,
        "accounts": accounts,
        "windows": windows,
    }


def apply_result(db: DB, channel: dict[str, Any], result: dict[str, Any], state: dict[str, Any]) -> str:
    now = int(time.time())
    cid = int(channel["id"])
    current_status = int(channel.get("status") or 0)
    manually_disabled = current_status == STATUS_MANUALLY_DISABLED
    other_info = parse_json_object(channel.get("other_info"))

    ok = bool(result.get("ok"))
    quota_ok = bool(result.get("quota_ok", result.get("within_share"))) if ok else False
    fail_closed = bool(result.get("fail_closed"))
    desired_enabled = ok and quota_ok

    guard_info = {
        "managed": True,
        "updated_at": now,
        "desired_enabled": desired_enabled,
        "manual_status_preserved": manually_disabled,
        "failure_count": int(state.get("failure_count") or 0),
        "health": result,
    }
    other_info["cliproxy_cpa_quota_guard"] = guard_info

    status: int | None = None
    abilities_enabled: bool | None = None
    balance_update: float | None = None

    if desired_enabled:
        balance_update = float(first_non_empty(result.get("usable_balance_units"), result.get("balance_units")) or 0)
        if not manually_disabled and current_status != STATUS_ENABLED:
            status = STATUS_ENABLED
            abilities_enabled = True
    elif ok or fail_closed:
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
    db.psql("\n".join(statements))

    name = channel.get("name") or f"channel-{cid}"
    if desired_enabled:
        return (
            f"{name}: enabled usable={float(result.get('usable_balance_units') or 0):.6f} "
            f"total={float(result.get('total_balance_units') or result.get('balance_units') or 0):.6f}"
        )
    if ok:
        return f"{name}: auto-disabled {result.get('reason')}"
    if fail_closed:
        return f"{name}: auto-disabled quota_probe_failed_closed"
    return f"{name}: unchanged quota_probe_failed"


def main() -> int:
    config_path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("/opt/new-api/ops/cliproxy_cpa_quota_guard.json")
    config = deep_merge(DEFAULT_CONFIG, load_json(config_path, {}))
    env = load_env_values(Path(str(config.get("env_path") or DEFAULT_CONFIG["env_path"])))
    state_path = Path(str(config.get("state_path") or DEFAULT_CONFIG["state_path"]))
    state = load_json(state_path, {})
    db = db_from_config(config)
    config = deep_merge(config, load_option_overrides(db))

    try:
        accounts = call_wham_usages(config, env)
        result = evaluate_quota(config, accounts)
        state["failure_count"] = 0
        state["last_success_at"] = int(time.time())
        state["last_error"] = None
    except (urllib.error.URLError, urllib.error.HTTPError, socket.timeout, TimeoutError, RuntimeError, json.JSONDecodeError) as exc:
        failure_count = int(state.get("failure_count") or 0) + 1
        state["failure_count"] = failure_count
        state["last_error"] = str(exc)[:180]
        state["last_failure_at"] = int(time.time())
        result = {
            "ok": False,
            "within_share": False,
            "reason": "quota_probe_failed",
            "error": str(exc)[:180],
            "fail_closed": failure_count >= int(config.get("fail_closed_after_consecutive_failures") or 3),
        }

    channel = fetch_channel(db, int(config.get("channel_id") or 12))
    message = apply_result(db, channel, result, state)
    save_json_atomic(state_path, state)
    print(message)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
