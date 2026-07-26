#!/usr/bin/env python3
"""Runtime quota guard for the New API CLIProxyAPI Codex channel.

The channel consumes a shared CPA Codex account whose upstream quota is normally
reported as rolling 5h and 7d windows. The 5h window can temporarily disappear,
so the guard falls back to the required weekly window until it returns. New API
only understands channel balance/status, so this script polls CPA's management
API and maps the available windows to the channel state.
"""

from __future__ import annotations

import base64
import datetime as dt
import hashlib
import json
import math
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

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
    "min_remaining_percent_5h": 15.0,
    "min_remaining_percent_7d": 15.0,
    "fail_closed_after_consecutive_failures": 3,
    "balance_units_per_percent": 1.0,
    "dynamic_daily_budget_enabled": False,
    "timezone": "Asia/Shanghai",
    "quota_reset_increase_threshold_percent": 10.0,
    "quota_reset_near_full_percent": 90.0,
    "quota_reset_near_full_min_increase_percent": 5.0,
    "quota_reset_confirmation_count": 2,
    "personal_plan_keywords": ["plus"],
    "protected_plan_keywords": ["pro"],
    "default_account_bucket": "protected",
    "account_bucket_overrides": {},
    "prefer_cpa_quota_health_endpoint": True,
    "auto_reconcile_runtime_quota": True,
    "auto_reconcile_confirmation_count": 2,
    "auto_reconcile_reset_tolerance_sec": 60,
    "reset_credit_grace_enabled": False,
    "reset_credit_release_before_sec": 24 * 60 * 60,
    "reset_credit_auto_consume_before_sec": 10 * 60,
    "reset_credit_auto_consume_remaining_percent": 1.0,
    "reset_credit_retry_interval_sec": 60,
    "reset_credit_confirmation_timeout_sec": 5 * 60,
    "reset_credit_plan_keywords": ["pro"],
    "quota_feature": "",
    "quota_feature_limit_name": "",
    "quota_feature_plan_keywords": [],
    "quota_feature_min_remaining_percent": 0.0,
}

OPTION_CONFIG_MAP = {
    "cliproxy_cpa_quota_guard.enabled": ("enabled", "bool"),
    "cliproxy_cpa_quota_guard.min_remaining_percent_5h": ("min_remaining_percent_5h", "float"),
    "cliproxy_cpa_quota_guard.min_remaining_percent_7d": ("min_remaining_percent_7d", "float"),
    "cliproxy_cpa_quota_guard.dynamic_daily_budget_enabled": ("dynamic_daily_budget_enabled", "bool"),
    "cliproxy_cpa_quota_guard.quota_reset_increase_threshold_percent": ("quota_reset_increase_threshold_percent", "float"),
    "cliproxy_cpa_quota_guard.reset_credit_grace_enabled": ("reset_credit_grace_enabled", "bool"),
    "cliproxy_cpa_quota_guard.reset_credit_release_before_sec": ("reset_credit_release_before_sec", "float"),
    "cliproxy_cpa_quota_guard.reset_credit_auto_consume_before_sec": ("reset_credit_auto_consume_before_sec", "float"),
    "cliproxy_cpa_quota_guard.reset_credit_auto_consume_remaining_percent": ("reset_credit_auto_consume_remaining_percent", "float"),
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


def quota_feature_plan_selected(config: dict[str, Any], plan_type: str) -> bool:
    if not str(config.get("quota_feature") or "").strip():
        return True
    keywords = string_list(config.get("quota_feature_plan_keywords"))
    if not keywords:
        return True
    normalized = str(plan_type or "").strip().lower()
    return any(keyword in normalized for keyword in keywords)


def quota_feature_plan_is_known(plan_type: str) -> bool:
    return str(plan_type or "").strip().lower() not in {"", "unknown"}


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
    quota_feature_mode = bool(str(config.get("quota_feature") or "").strip())

    for auth_entry in entries:
        auth_index, _, account_id_hash = account_identity(auth_entry)
        bucket = classify_account_bucket(config, auth_entry, None, account_id_hash, auth_index)
        plan_type = plan_type_from_entry(auth_entry)
        base_account = {
            "auth_index": auth_index,
            "account_id_hash": account_id_hash,
            "account_label": account_label_from_entry(auth_entry),
            "plan_type": plan_type,
            "bucket": bucket,
            "disabled": bool(auth_entry.get("disabled")),
            "unavailable": bool(auth_entry.get("unavailable")),
            "reset_credits_available": None,
        }
        if bool(auth_entry.get("disabled")):
            accounts.append({
                **base_account,
                "ok": False,
                "schedulable": False,
                "skipped": True,
                "reason": "auth_disabled",
            })
            continue
        if (
            quota_feature_mode
            and quota_feature_plan_is_known(plan_type)
            and not quota_feature_plan_selected(config, plan_type)
        ):
            accounts.append({
                **base_account,
                "ok": False,
                "schedulable": False,
                "skipped": True,
                "reason": "quota_feature_plan_not_selected",
            })
            continue
        try:
            usage = call_wham_usage_for_auth(config, base_url, headers, timeout, auth_entry)
            resolved_plan_type = plan_type_from_entry(auth_entry, usage)
            if quota_feature_mode and not quota_feature_plan_selected(config, resolved_plan_type):
                accounts.append({
                    **base_account,
                    "plan_type": resolved_plan_type,
                    "ok": False,
                    "schedulable": False,
                    "skipped": True,
                    "reason": "quota_feature_plan_not_selected",
                })
                continue
            if quota_feature_mode:
                account = evaluate_quota_feature_account(config, auth_entry, usage)
            else:
                account = evaluate_account_quota(config, auth_entry, usage)
            accounts.append(account)
            successful += 1
        except Exception as exc:
            reason = "auth_unavailable" if bool(auth_entry.get("unavailable")) else "quota_probe_failed"
            accounts.append({
                **base_account,
                "ok": False,
                "schedulable": False,
                "skipped": bool(auth_entry.get("unavailable")),
                "reason": reason,
                "error": str(exc)[:180],
            })

    if successful == 0 and accounts and all(item.get("skipped") for item in accounts):
        return accounts
    if successful == 0:
        errors = [str(item.get("error") or item.get("reason") or "unknown") for item in accounts]
        raise RuntimeError("wham_usage_all_accounts_failed: " + "; ".join(errors[:3]))
    return accounts


def cpa_quota_health_query(config: dict[str, Any]) -> str:
    params = {
        "enabled": "true" if bool_value(config.get("enabled"), True) else "false",
        "min_remaining_percent_5h": str(clamp_percent(config.get("min_remaining_percent_5h"), 30.0)),
        "min_remaining_percent_7d": str(clamp_percent(config.get("min_remaining_percent_7d"), 20.0)),
        "balance_units_per_percent": str(float(config.get("balance_units_per_percent") or 1.0)),
    }
    return urllib.parse.urlencode(params)


def call_cpa_quota_health(config: dict[str, Any], env: dict[str, str]) -> dict[str, Any]:
    timeout = int(config.get("timeout_sec") or 30)
    base_url = str(config.get("cpa_base_url") or "").rstrip("/")
    if not base_url:
        raise RuntimeError("empty_cpa_base_url")
    headers = management_headers(env, base_url)
    if not headers.get("Authorization") and not headers.get("X-Management-Key"):
        raise RuntimeError("missing_cpa_management_credentials")

    payload = request_json(base_url + "/v0/management/quota-health?" + cpa_quota_health_query(config), headers, timeout)
    if payload.get("guard_mode") != "bucket_low_watermark":
        raise RuntimeError("cpa_quota_health_invalid_payload")
    if "ok" not in payload or "accounts" not in payload:
        raise RuntimeError("cpa_quota_health_incomplete_payload")
    payload["quota_health_source"] = "cpa_management"
    return payload


def call_quota_health(config: dict[str, Any], env: dict[str, str]) -> dict[str, Any]:
    quota_feature = str(config.get("quota_feature") or "").strip()
    if quota_feature:
        accounts = call_wham_usages(config, env)
        result = evaluate_quota(config, accounts)
        result.update({
            "guard_mode": "model_quota",
            "quota_feature": quota_feature,
            "quota_feature_limit_name": str(config.get("quota_feature_limit_name") or "").strip(),
            "quota_feature_min_remaining_percent": clamp_percent(
                config.get("quota_feature_min_remaining_percent"),
                0.0,
            ),
            "quota_health_source": "python_guard_feature",
        })
        return result

    if bool_value(config.get("prefer_cpa_quota_health_endpoint"), True):
        try:
            return call_cpa_quota_health(config, env)
        except Exception as exc:
            fallback_note = str(exc)[:180]
    else:
        fallback_note = ""

    accounts = call_wham_usages(config, env)
    result = evaluate_quota(config, accounts)
    result["quota_health_source"] = "python_guard_fallback"
    if fallback_note:
        result["quota_health_endpoint_error"] = fallback_note
    return result


def quota_reconcile_candidate(config: dict[str, Any], account: dict[str, Any], now: int) -> dict[str, Any] | None:
    if not bool_value(config.get("auto_reconcile_runtime_quota"), True):
        return None
    if bool(account.get("disabled")) or not bool(account.get("runtime_unavailable")):
        return None
    if not bool(account.get("runtime_quota_exceeded")):
        return None

    auth_index = str(account.get("auth_index") or "").strip()
    runtime_reset_at = int(number(account.get("runtime_reset_at")) or 0)
    windows = account.get("windows")
    if not auth_index or runtime_reset_at <= 0 or not isinstance(windows, dict):
        return None

    weekly = windows.get("7d")
    if not isinstance(weekly, dict):
        return None
    weekly_remaining = number(weekly.get("remaining_percent"))
    if weekly_remaining is None or weekly_remaining <= 0.000001:
        return None

    five_hour = windows.get("5h")
    if isinstance(five_hour, dict):
        five_hour_remaining = number(five_hour.get("remaining_percent"))
        if five_hour_remaining is None or five_hour_remaining <= 0.000001:
            return None

    selected_name = "7d"
    selected_window = weekly
    if isinstance(five_hour, dict) and runtime_reset_at-now <= WINDOW_5H_SECONDS + 15 * 60:
        selected_name = "5h"
        selected_window = five_hour

    upstream_reset_at = int(number(selected_window.get("reset_at")) or 0)
    if upstream_reset_at <= 0:
        return None
    tolerance = max(0, int(config.get("auto_reconcile_reset_tolerance_sec") or 60))
    return {
        "auth_index": auth_index,
        "window": selected_name,
        "window_key": f"{selected_name}:{upstream_reset_at}",
        "upstream_reset_at": upstream_reset_at,
        "runtime_reset_at": runtime_reset_at,
        "window_advanced": upstream_reset_at > runtime_reset_at + tolerance,
    }


def auto_reconcile_runtime_quota(
    config: dict[str, Any],
    env: dict[str, str],
    result: dict[str, Any],
    state: dict[str, Any],
    now: int | None = None,
) -> dict[str, Any]:
    summary: dict[str, Any] = {"reset_count": 0, "pending_count": 0, "error_count": 0}
    if not bool_value(config.get("auto_reconcile_runtime_quota"), True):
        return summary

    base_url = str(config.get("cpa_base_url") or "").rstrip("/")
    if not base_url:
        return summary
    headers = management_headers(env, base_url)
    if not headers.get("Authorization") and not headers.get("X-Management-Key"):
        return summary

    now_ts = int(time.time()) if now is None else int(now)
    confirmation_count = max(1, int(config.get("auto_reconcile_confirmation_count") or 2))
    reconcile_state = state.setdefault("quota_auto_reconcile", {})
    account_states = reconcile_state.setdefault("accounts", {})

    for raw_account in result.get("accounts") or []:
        if not isinstance(raw_account, dict):
            continue
        candidate = quota_reconcile_candidate(config, raw_account, now_ts)
        if candidate is None:
            continue

        auth_index = candidate["auth_index"]
        window_key = candidate["window_key"]
        account_state = account_states.setdefault(auth_index, {})
        if account_state.get("last_reset_window") == window_key:
            account_state["candidate_count"] = 0
            account_state["candidate_window"] = None
            continue

        if account_state.get("candidate_window") == window_key:
            candidate_count = int(account_state.get("candidate_count") or 0) + 1
        else:
            candidate_count = 1
        account_state["candidate_window"] = window_key
        account_state["candidate_count"] = candidate_count
        account_state["last_observed_at"] = now_ts

        required = 1 if candidate["window_advanced"] else confirmation_count
        if candidate_count < required:
            summary["pending_count"] += 1
            continue

        try:
            request_json(
                base_url + "/v0/management/reset-quota",
                headers,
                int(config.get("timeout_sec") or 30),
                {"auth_index": auth_index},
            )
        except Exception as exc:
            account_state["last_error"] = str(exc)[:180]
            account_state["last_error_at"] = now_ts
            summary["error_count"] += 1
            continue

        account_state["last_reset_window"] = window_key
        account_state["last_reset_at"] = now_ts
        account_state["last_error"] = None
        account_state["candidate_window"] = None
        account_state["candidate_count"] = 0
        summary["reset_count"] += 1

    reconcile_state["updated_at"] = now_ts
    return summary


def number(value: Any) -> float | None:
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        try:
            return float(value)
        except ValueError:
            return None
    return None


def rate_limit_windows(
    rate_limit: dict[str, Any],
    *,
    require_weekly: bool,
) -> dict[str, dict[str, Any]]:
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
    if not out:
        raise RuntimeError("missing_quota_window")
    if require_weekly and "7d" not in out:
        raise RuntimeError("missing_required_7d_quota_window")
    return out


def quota_windows(usage: dict[str, Any]) -> dict[str, dict[str, Any]]:
    rate_limit = first_non_empty(usage.get("rate_limit"), usage.get("rateLimit"))
    if not isinstance(rate_limit, dict):
        raise RuntimeError("missing_rate_limit")
    return rate_limit_windows(rate_limit, require_weekly=True)


def quota_feature_rate_limit(config: dict[str, Any], usage: dict[str, Any]) -> tuple[str, str, dict[str, Any]]:
    feature = str(config.get("quota_feature") or "").strip()
    limit_name = str(config.get("quota_feature_limit_name") or "").strip()
    raw_limits = first_non_empty(usage.get("additional_rate_limits"), usage.get("additionalRateLimits"))
    if not isinstance(raw_limits, list):
        raise RuntimeError("missing_additional_rate_limits")

    normalized_feature = feature.lower()
    normalized_name = limit_name.lower()
    for raw in raw_limits:
        if not isinstance(raw, dict):
            continue
        item_feature = str(first_non_empty(raw.get("metered_feature"), raw.get("meteredFeature")) or "").strip()
        item_name = str(first_non_empty(raw.get("limit_name"), raw.get("limitName")) or "").strip()
        if normalized_feature and item_feature.lower() != normalized_feature:
            continue
        if not normalized_feature and normalized_name and item_name.lower() != normalized_name:
            continue
        rate_limit = first_non_empty(raw.get("rate_limit"), raw.get("rateLimit"))
        if not isinstance(rate_limit, dict):
            raise RuntimeError("quota_feature_missing_rate_limit")
        return item_feature or feature, item_name or limit_name, rate_limit
    raise RuntimeError("quota_feature_not_available")


def account_window_remaining(windows: dict[str, dict[str, Any]], key: str) -> float:
    value = number(windows.get(key, {}).get("remaining_percent"))
    if value is None:
        raise RuntimeError(f"missing_{key}_remaining_percent")
    return max(0.0, min(100.0, value))


def exhausted_quota_window(windows: dict[str, dict[str, Any]]) -> str | None:
    for key in ("7d", "5h"):
        remaining = number(windows.get(key, {}).get("remaining_percent"))
        if remaining is not None and remaining <= 0.000001:
            return key
    return None


def account_unschedulable_reason(
    disabled: bool,
    runtime_unavailable: bool,
    windows: dict[str, dict[str, Any]],
) -> tuple[str | None, str | None]:
    if disabled:
        return "auth_disabled", None
    exhausted = exhausted_quota_window(windows)
    if exhausted:
        return f"quota_{exhausted}_exhausted", exhausted
    if runtime_unavailable:
        return "auth_unavailable", None
    return None, None


def evaluate_account_quota(config: dict[str, Any], auth_entry: dict[str, Any], usage: dict[str, Any]) -> dict[str, Any]:
    windows = quota_windows(usage)
    threshold_5h = clamp_percent(config.get("min_remaining_percent_5h"), 30.0)
    threshold_7d = clamp_percent(config.get("min_remaining_percent_7d"), 20.0)
    remaining_7d = account_window_remaining(windows, "7d")
    remaining_values = [remaining_7d]
    headroom_values = [remaining_7d - threshold_7d]
    if "5h" in windows:
        remaining_5h = account_window_remaining(windows, "5h")
        remaining_values.append(remaining_5h)
        headroom_values.append(remaining_5h - threshold_5h)
    guard_auth = usage.get("_guard_auth") if isinstance(usage.get("_guard_auth"), dict) else {}
    auth_index = str(guard_auth.get("auth_index") or "").strip()
    account_id_hash = str(guard_auth.get("account_id_hash") or "").strip()
    bucket = classify_account_bucket(config, auth_entry, usage, account_id_hash, auth_index)
    can_exhaust = bucket == "personal"
    raw_remaining = min(remaining_values)
    minimum_headroom = min(headroom_values)
    protected_headroom = max(0.0, minimum_headroom)
    visible_remaining = raw_remaining
    protected_reserve_warning = not can_exhaust and protected_headroom <= 0.000001
    units_per_percent = float(config.get("balance_units_per_percent") or 1.0)
    balance_units = raw_remaining * units_per_percent
    usable_balance_units = visible_remaining * units_per_percent
    disabled = bool(auth_entry.get("disabled"))
    runtime_unavailable = bool(auth_entry.get("unavailable"))
    reason, exhausted_window = account_unschedulable_reason(
        disabled,
        runtime_unavailable,
        windows,
    )
    schedulable = reason is None
    return {
        "ok": True,
        "schedulable": schedulable,
        "auth_index": auth_index,
        "account_id_hash": account_id_hash,
        "account_label": account_label_from_entry(auth_entry),
        "bucket": bucket,
        "can_exhaust": can_exhaust,
        "min_remaining_percent_5h": threshold_5h,
        "min_remaining_percent_7d": threshold_7d,
        "remaining_headroom_percent": round(minimum_headroom, 6),
        "protected_reserve_warning": protected_reserve_warning,
        "remaining_share_percent": round(visible_remaining, 6),
        "raw_remaining_percent": round(raw_remaining, 6),
        "balance_units": round(balance_units, 6),
        "usable_balance_units": round(usable_balance_units, 6),
        "plan_type": usage.get("plan_type") or usage.get("planType") or guard_auth.get("plan_type_hint"),
        "disabled": disabled,
        "unavailable": runtime_unavailable,
        "runtime_unavailable": runtime_unavailable,
        "reason": reason,
        "reset_credits_available": reset_credits_available(usage),
        "windows": windows,
        "rate_limit_reached_type": usage.get("rate_limit_reached_type"),
        "quota_exhausted_window": exhausted_window,
    }


def evaluate_quota_feature_account(
    config: dict[str, Any],
    auth_entry: dict[str, Any],
    usage: dict[str, Any],
) -> dict[str, Any]:
    feature, limit_name, rate_limit = quota_feature_rate_limit(config, usage)
    windows = rate_limit_windows(rate_limit, require_weekly=False)
    remaining_values = [
        float(value)
        for value in (
            number(window.get("remaining_percent"))
            for window in windows.values()
            if isinstance(window, dict)
        )
        if value is not None
    ]
    if not remaining_values:
        raise RuntimeError("quota_feature_missing_remaining_percent")

    threshold = clamp_percent(config.get("quota_feature_min_remaining_percent"), 0.0)
    raw_remaining = max(0.0, min(100.0, min(remaining_values)))
    allowed = bool_value(first_non_empty(rate_limit.get("allowed"), True), True)
    limit_reached = bool_value(first_non_empty(rate_limit.get("limit_reached"), rate_limit.get("limitReached")), False)
    disabled = bool(auth_entry.get("disabled"))
    runtime_unavailable = bool(auth_entry.get("unavailable"))
    quota_available = allowed and not limit_reached and raw_remaining > threshold + 0.000001
    reason: str | None = None
    if disabled:
        reason = "auth_disabled"
    elif runtime_unavailable:
        reason = "auth_unavailable"
    elif not quota_available:
        reason = "quota_feature_exhausted" if raw_remaining <= 0.000001 or limit_reached or not allowed else "quota_feature_low_watermark"

    guard_auth = usage.get("_guard_auth") if isinstance(usage.get("_guard_auth"), dict) else {}
    auth_index = str(guard_auth.get("auth_index") or "").strip()
    account_id_hash = str(guard_auth.get("account_id_hash") or "").strip()
    units_per_percent = max(0.0, float(number(config.get("balance_units_per_percent")) or 1.0))
    usable_remaining = raw_remaining if reason is None else 0.0
    return {
        "ok": True,
        "schedulable": reason is None,
        "auth_index": auth_index,
        "account_id_hash": account_id_hash,
        "account_label": account_label_from_entry(auth_entry),
        "bucket": "personal",
        "can_exhaust": True,
        "quota_feature": feature,
        "quota_feature_limit_name": limit_name,
        "quota_feature_min_remaining_percent": threshold,
        "remaining_share_percent": round(usable_remaining, 6),
        "raw_remaining_percent": round(raw_remaining, 6),
        "balance_units": round(raw_remaining * units_per_percent, 6),
        "usable_balance_units": round(usable_remaining * units_per_percent, 6),
        "plan_type": usage.get("plan_type") or usage.get("planType") or guard_auth.get("plan_type_hint"),
        "disabled": disabled,
        "unavailable": runtime_unavailable,
        "runtime_unavailable": runtime_unavailable,
        "reason": reason,
        "windows": windows,
        "quota_exhausted_window": exhausted_quota_window(windows),
        "allowed": allowed,
        "limit_reached": limit_reached,
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
        "schedulable",
        "runtime_unavailable",
        "quota_exhausted_window",
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
        "protected_reserve_warning",
        "reset_credits_available",
        "windows",
        "quota_feature",
        "quota_feature_limit_name",
        "quota_feature_min_remaining_percent",
        "allowed",
        "limit_reached",
    ]
    return {key: account.get(key) for key in keys if key in account}


def bucket_summary(config: dict[str, Any], bucket_key: str, accounts: list[dict[str, Any]]) -> dict[str, Any]:
    summary = empty_bucket(bucket_key, config)
    summary["account_count"] = len(accounts)
    ok_accounts = [account for account in accounts if account.get("ok")]
    schedulable_accounts = [
        account for account in ok_accounts if account.get("schedulable")
    ]
    summary["available_account_count"] = len(schedulable_accounts)
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
    schedulable_accounts = [
        account for account in ok_accounts if account.get("schedulable")
    ]
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
        "available_account_count": len(schedulable_accounts),
        "remaining_share_percent": usable_balance_units,
        "balance_units": usable_balance_units,
        "usable_balance_units": usable_balance_units,
        "total_balance_units": total_balance_units,
        "buckets": buckets,
        "accounts": accounts,
        "windows": windows,
    }


def guard_timezone(config: dict[str, Any]) -> dt.tzinfo:
    name = str(config.get("timezone") or "Asia/Shanghai").strip()
    try:
        return ZoneInfo(name)
    except ZoneInfoNotFoundError:
        return dt.timezone.utc


def protected_weekly_budget_snapshot(result: dict[str, Any], now: int) -> dict[str, Any] | None:
    accounts = result.get("accounts")
    if not isinstance(accounts, list):
        return None

    protected: list[dict[str, Any]] = []
    for account in accounts:
        if not isinstance(account, dict) or not account.get("ok") or account.get("bucket") != "protected":
            continue
        windows = account.get("windows")
        weekly = windows.get("7d") if isinstance(windows, dict) else None
        remaining = number(weekly.get("remaining_percent")) if isinstance(weekly, dict) else None
        if remaining is None:
            continue
        protected.append(account)
    if not protected:
        return None

    remaining_total = 0.0
    weekly_remaining_values: list[float] = []
    five_hour_remaining_values: list[float] = []
    reset_after_values: list[float] = []
    reset_at_values: list[float] = []
    identities: list[str] = []
    for account in protected:
        weekly = account["windows"]["7d"]
        remaining_total += max(0.0, float(number(weekly.get("remaining_percent")) or 0.0))
        weekly_remaining_values.append(max(0.0, float(number(weekly.get("remaining_percent")) or 0.0)))
        five_hour = account["windows"].get("5h")
        five_hour_remaining = number(five_hour.get("remaining_percent")) if isinstance(five_hour, dict) else None
        if five_hour_remaining is not None:
            five_hour_remaining_values.append(max(0.0, float(five_hour_remaining)))
        reset_after = number(weekly.get("reset_after_seconds"))
        reset_at = number(weekly.get("reset_at"))
        if reset_after is not None and reset_after >= 0:
            reset_after_values.append(reset_after)
        if reset_at is not None and reset_at > 0:
            reset_at_values.append(reset_at)
        identity = str(first_non_empty(account.get("account_id_hash"), account.get("auth_index"), "unknown"))
        identities.append(identity)

    reset_at = int(min(reset_at_values)) if reset_at_values else 0
    if reset_after_values:
        reset_after_seconds = max(0, int(min(reset_after_values)))
    elif reset_at > 0:
        reset_after_seconds = max(0, reset_at - now)
    else:
        reset_after_seconds = WINDOW_7D_SECONDS
    days_remaining = max(1, min(7, int(math.ceil(max(reset_after_seconds, 1) / 86_400))))
    signature = hashlib.sha256("|".join(sorted(identities)).encode("utf-8")).hexdigest()[:16]
    return {
        "account_count": len(protected),
        "account_signature": signature,
        "remaining_percent": round(remaining_total, 6),
        "minimum_remaining_percent_7d": round(min(weekly_remaining_values), 6),
        "minimum_remaining_percent_5h": round(min(five_hour_remaining_values), 6) if five_hour_remaining_values else None,
        "reset_at": reset_at,
        "reset_after_seconds": reset_after_seconds,
        "days_remaining": days_remaining,
    }


def reset_credit_timestamp(value: Any) -> int | None:
    raw = str(value or "").strip()
    if not raw:
        return None
    if raw.endswith("Z"):
        raw = raw[:-1] + "+00:00"
    try:
        parsed = dt.datetime.fromisoformat(raw)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return int(parsed.timestamp())


def reset_credit_account_key(account: dict[str, Any]) -> str:
    return str(first_non_empty(account.get("account_id_hash"), account.get("auth_index")) or "").strip()


def reset_credit_plan_selected(config: dict[str, Any], account: dict[str, Any]) -> bool:
    plan = str(account.get("plan_type") or "").strip().lower()
    keywords = string_list(config.get("reset_credit_plan_keywords")) or ["pro"]
    return bool(plan) and any(keyword.lower() in plan for keyword in keywords)


def reset_credit_current(account: dict[str, Any]) -> dict[str, Any] | None:
    credits = account.get("reset_credits")
    if not isinstance(credits, list):
        return None
    candidates: list[dict[str, Any]] = []
    for raw_credit in credits:
        if not isinstance(raw_credit, dict):
            continue
        status = str(raw_credit.get("status") or "available").strip().lower()
        if status != "available":
            continue
        expires_at = str(raw_credit.get("expires_at") or "").strip()
        expires_ts = reset_credit_timestamp(expires_at)
        if expires_ts is None:
            continue
        reset_type = str(raw_credit.get("reset_type") or "codex_rate_limits").strip()
        id_suffix = str(raw_credit.get("id_suffix") or "").strip()
        credit_key = hashlib.sha256(
            f"{reset_type}|{id_suffix}|{expires_at}".encode("utf-8")
        ).hexdigest()[:24]
        candidates.append({
            "credit_key": credit_key,
            "expires_at": expires_at,
            "expires_ts": expires_ts,
            "reset_type": reset_type,
            "id_suffix": id_suffix,
        })
    if not candidates:
        return None
    return min(candidates, key=lambda item: int(item["expires_ts"]))


def reset_credit_quota_snapshot(account: dict[str, Any]) -> dict[str, Any]:
    windows = account.get("windows")
    if not isinstance(windows, dict):
        return {"minimum_remaining_percent": None, "reset_at": {}}
    remaining: list[float] = []
    reset_at: dict[str, int] = {}
    for window_name in ("5h", "7d"):
        window = windows.get(window_name)
        if not isinstance(window, dict):
            continue
        remaining_percent = number(window.get("remaining_percent"))
        if remaining_percent is not None:
            remaining.append(max(0.0, float(remaining_percent)))
        reset_timestamp = number(window.get("reset_at"))
        if reset_timestamp is not None and reset_timestamp > 0:
            reset_at[window_name] = int(reset_timestamp)
    return {
        "minimum_remaining_percent": min(remaining) if remaining else None,
        "reset_at": reset_at,
    }


def reset_credit_quota_refilled(config: dict[str, Any], previous: dict[str, Any], current: dict[str, Any]) -> bool:
    previous_reset_at = previous.get("reset_at") if isinstance(previous.get("reset_at"), dict) else {}
    current_reset_at = current.get("reset_at") if isinstance(current.get("reset_at"), dict) else {}
    for window_name, current_value in current_reset_at.items():
        previous_value = int(number(previous_reset_at.get(window_name)) or 0)
        if int(current_value) > previous_value:
            return True

    previous_remaining = number(previous.get("minimum_remaining_percent"))
    current_remaining = number(current.get("minimum_remaining_percent"))
    increase_threshold = max(
        0.0,
        float(number(config.get("quota_reset_increase_threshold_percent")) or 10.0),
    )
    return (
        previous_remaining is not None
        and current_remaining is not None
        and current_remaining - previous_remaining >= increase_threshold
    )


def reset_credit_redeem_request_id(account_key: str, credit_key: str) -> str:
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"https://cmsg666.xyz/cpa/reset-credit/{account_key}/{credit_key}"))


def consume_reset_credit(
    config: dict[str, Any],
    env: dict[str, str],
    auth_index: str,
    redeem_request_id: str,
) -> dict[str, Any]:
    auth_index = str(auth_index or "").strip()
    if not auth_index:
        raise RuntimeError("missing_auth_index")
    base_url = str(config.get("cpa_base_url") or "").rstrip("/")
    if not base_url:
        raise RuntimeError("empty_cpa_base_url")
    headers = management_headers(env, base_url)
    if not headers.get("Authorization") and not headers.get("X-Management-Key"):
        raise RuntimeError("missing_cpa_management_credentials")
    payload = request_json(
        base_url + "/v0/management/consume-codex-reset-credit",
        headers,
        int(config.get("timeout_sec") or 30),
        {
            "auth_index": auth_index,
            "redeem_request_id": redeem_request_id,
        },
    )
    if str(payload.get("status") or "").lower() != "ok":
        raise RuntimeError("reset_credit_consume_invalid_response")
    return payload


def apply_reset_credit_grace(
    config: dict[str, Any],
    env: dict[str, str],
    result: dict[str, Any],
    state: dict[str, Any],
    now: int | None = None,
    allow_consume: bool = True,
) -> dict[str, Any]:
    now_ts = int(time.time()) if now is None else int(now)
    enabled = bool_value(config.get("reset_credit_grace_enabled"), False) and not str(
        config.get("quota_feature") or ""
    ).strip()
    summary: dict[str, Any] = {
        "enabled": enabled,
        "active": False,
        "active_account_count": 0,
        "confirmed_reset_count": 0,
        "manual_reset_count": 0,
        "auto_reset_count": 0,
        "expired_without_reset_count": 0,
        "consume_success_count": 0,
        "consume_error_count": 0,
        "updated_at": now_ts,
    }
    if not enabled or not result.get("ok"):
        result["reset_credit_grace"] = summary
        return result

    accounts = result.get("accounts")
    if not isinstance(accounts, list):
        result["reset_credit_grace"] = summary
        return result

    grace_state = state.setdefault("reset_credit_grace", {})
    account_states = grace_state.setdefault("accounts", {})
    history = grace_state.setdefault("history", [])
    credit_overrides: dict[str, dict[str, Any]] = {}
    current_accounts: dict[str, dict[str, Any]] = {}
    for account in accounts:
        if not isinstance(account, dict) or not account.get("ok") or not reset_credit_plan_selected(config, account):
            continue
        account_key = reset_credit_account_key(account)
        if account_key:
            current_accounts[account_key] = account

    confirmation_timeout = max(
        0,
        int(number(config.get("reset_credit_confirmation_timeout_sec")) or 300),
    )
    for account_key, account_state in list(account_states.items()):
        if not isinstance(account_state, dict) or account_state.get("status") not in {
            "active",
            "consume_error",
            "awaiting_confirmation",
        }:
            continue
        account = current_accounts.get(account_key)
        if account is None:
            continue
        current_credit = reset_credit_current(account)
        previous_credit_key = str(account_state.get("credit_key") or "")
        same_credit = current_credit is not None and current_credit.get("credit_key") == previous_credit_key
        expires_ts = int(number(account_state.get("expires_ts")) or 0)
        if same_credit and (
            now_ts < expires_ts
            or (
                account_state.get("auto_reset_succeeded_at")
                and now_ts <= expires_ts + confirmation_timeout
            )
        ):
            continue

        current_snapshot = reset_credit_quota_snapshot(account)
        previous_snapshot = account_state.get("quota_snapshot")
        if not isinstance(previous_snapshot, dict):
            previous_snapshot = {}
        quota_refilled = reset_credit_quota_refilled(config, previous_snapshot, current_snapshot)
        before_expiry = expires_ts > 0 and now_ts < expires_ts
        auto_attempted = int(account_state.get("auto_reset_attempt_count") or 0) > 0
        previous_available = number(account_state.get("baseline_available_count"))
        current_available = number(account.get("reset_credits_available"))
        credit_count_decreased = (
            previous_available is not None
            and current_available is not None
            and current_available < previous_available
        )
        reset_confirmed = (
            quota_refilled
            or (before_expiry and not same_credit and credit_count_decreased)
        )
        if not reset_confirmed and before_expiry:
            credit_overrides[account_key] = {
                "credit_key": previous_credit_key,
                "expires_at": account_state.get("expires_at"),
                "expires_ts": expires_ts,
                "reset_type": account_state.get("reset_type"),
                "id_suffix": "",
            }
            continue
        event = {
            "account_key": account_key,
            "credit_key": previous_credit_key,
            "expires_at": account_state.get("expires_at"),
            "completed_at": now_ts,
        }
        if reset_confirmed:
            reset_kind = "auto" if auto_attempted else "manual"
            account_state["status"] = "completed"
            account_state["reset_kind"] = reset_kind
            account_state["completed_at"] = now_ts
            summary["confirmed_reset_count"] += 1
            summary[f"{reset_kind}_reset_count"] += 1
            event["event"] = reset_kind + "_reset_confirmed"
        else:
            account_state["status"] = "expired"
            account_state["completed_at"] = now_ts
            summary["expired_without_reset_count"] += 1
            event["event"] = "expired_without_reset"
        history.append(event)

    release_before = max(0, int(number(config.get("reset_credit_release_before_sec")) or 86_400))
    auto_before = max(0, int(number(config.get("reset_credit_auto_consume_before_sec")) or 600))
    early_remaining = clamp_percent(config.get("reset_credit_auto_consume_remaining_percent"), 1.0)
    retry_interval = max(1, int(number(config.get("reset_credit_retry_interval_sec")) or 60))
    active_accounts: list[dict[str, Any]] = []

    for account_key, account in current_accounts.items():
        credit = credit_overrides.get(account_key) or reset_credit_current(account)
        if credit is None:
            continue
        seconds_until_expiry = int(credit["expires_ts"]) - now_ts
        if seconds_until_expiry <= 0 or seconds_until_expiry > release_before:
            continue

        account_state = account_states.get(account_key)
        if not isinstance(account_state, dict) or account_state.get("credit_key") != credit["credit_key"]:
            account_state = {
                "status": "active",
                "credit_key": credit["credit_key"],
                "expires_at": credit["expires_at"],
                "expires_ts": credit["expires_ts"],
                "reset_type": credit["reset_type"],
                "grace_started_at": now_ts,
                "redeem_request_id": reset_credit_redeem_request_id(account_key, str(credit["credit_key"])),
                "baseline_available_count": int(number(account.get("reset_credits_available")) or 0),
                "quota_snapshot": reset_credit_quota_snapshot(account),
                "auto_reset_attempt_count": 0,
            }
            account_states[account_key] = account_state

        if account_state.get("status") in {"completed", "expired"}:
            continue
        account_state["last_seen_at"] = now_ts
        account_state["seconds_until_expiry"] = seconds_until_expiry
        snapshot = reset_credit_quota_snapshot(account)
        minimum_remaining = number(snapshot.get("minimum_remaining_percent"))
        due_reason = ""
        if minimum_remaining is not None and minimum_remaining <= early_remaining + 0.000001:
            due_reason = "quota_near_exhaustion"
        elif seconds_until_expiry <= auto_before:
            due_reason = "credit_expiring"

        last_attempt_at = int(account_state.get("auto_reset_last_attempt_at") or 0)
        can_attempt = (
            allow_consume
            and due_reason != ""
            and not account_state.get("auto_reset_succeeded_at")
            and now_ts - last_attempt_at >= retry_interval
        )
        if can_attempt:
            account_state["auto_reset_last_attempt_at"] = now_ts
            account_state["auto_reset_attempt_count"] = int(account_state.get("auto_reset_attempt_count") or 0) + 1
            account_state["auto_reset_reason"] = due_reason
            try:
                consume_reset_credit(
                    config,
                    env,
                    str(account.get("auth_index") or "").strip(),
                    str(account_state["redeem_request_id"]),
                )
            except (urllib.error.URLError, urllib.error.HTTPError, socket.timeout, TimeoutError, RuntimeError) as exc:
                account_state["status"] = "consume_error"
                account_state["last_error"] = str(exc)[:180]
                account_state["last_error_at"] = now_ts
                summary["consume_error_count"] += 1
            else:
                account_state["status"] = "awaiting_confirmation"
                account_state["auto_reset_succeeded_at"] = now_ts
                account_state["last_error"] = None
                summary["consume_success_count"] += 1

        account["reset_credit_grace_active"] = True
        account["reset_credit_grace_expires_at"] = credit["expires_at"]
        account["reset_credit_auto_consume_at"] = dt.datetime.fromtimestamp(
            int(credit["expires_ts"]) - auto_before,
            dt.timezone.utc,
        ).isoformat().replace("+00:00", "Z")
        account["reset_credit_grace_state"] = account_state.get("status")
        active_accounts.append({
            "account_key": account_key,
            "plan_type": account.get("plan_type"),
            "expires_at": credit["expires_at"],
            "auto_consume_at": account["reset_credit_auto_consume_at"],
            "seconds_until_expiry": seconds_until_expiry,
            "minimum_remaining_percent": minimum_remaining,
            "state": account_state.get("status"),
            "auto_reset_reason": account_state.get("auto_reset_reason"),
        })

    history[:] = history[-50:]
    grace_state["updated_at"] = now_ts
    summary["active"] = bool(active_accounts)
    summary["active_account_count"] = len(active_accounts)
    if active_accounts:
        summary["accounts"] = active_accounts
        summary["earliest_expires_at"] = min(item["expires_at"] for item in active_accounts)
        summary["next_auto_reset_at"] = min(item["auto_consume_at"] for item in active_accounts)
        summary["limits_released"] = True
    else:
        summary["limits_released"] = False
    result["reset_credit_grace"] = summary
    return result


def apply_dynamic_daily_budget(
    config: dict[str, Any],
    result: dict[str, Any],
    state: dict[str, Any],
    now: int | None = None,
) -> dict[str, Any]:
    if not bool_value(config.get("dynamic_daily_budget_enabled"), False) or not result.get("ok"):
        return result

    reset_credit_grace = result.get("reset_credit_grace")
    if isinstance(reset_credit_grace, dict) and reset_credit_grace.get("active"):
        result["guard_mode"] = "bucket_reset_credit_grace"
        result["dynamic_daily_budget"] = {
            "enabled": True,
            "applied": False,
            "bypassed": True,
            "reason": "reset_credit_grace_active",
        }
        if result.get("quota_ok", result.get("within_share")):
            result["reason"] = "reset_credit_grace_active"
        return result

    now = int(time.time()) if now is None else int(now)
    snapshot = protected_weekly_budget_snapshot(result, now)
    if snapshot is None:
        result["dynamic_daily_budget"] = {"enabled": True, "applied": False, "reason": "protected_7d_window_unavailable"}
        return result

    day_key = dt.datetime.fromtimestamp(now, guard_timezone(config)).date().isoformat()
    reserve_per_account = clamp_percent(config.get("min_remaining_percent_7d"), 15.0)
    reserve_5h_per_account = clamp_percent(config.get("min_remaining_percent_5h"), 15.0)
    reserve_total = reserve_per_account * int(snapshot["account_count"])
    current_remaining = float(snapshot["remaining_percent"])
    budget_state = state.get("dynamic_daily_budget")
    if not isinstance(budget_state, dict):
        budget_state = {}

    previous_day = str(budget_state.get("day") or "")
    previous_signature = str(budget_state.get("account_signature") or "")
    previous_reset_at = int(budget_state.get("reset_at") or 0)
    previous_baseline = number(budget_state.get("baseline_remaining_percent"))
    low_watermark = number(budget_state.get("minimum_remaining_percent_seen"))
    if low_watermark is None:
        low_watermark = number(budget_state.get("last_remaining_percent"))
    if low_watermark is None:
        low_watermark = current_remaining
    low_watermark = min(float(low_watermark), current_remaining)

    increase_threshold = max(0.0, float(number(config.get("quota_reset_increase_threshold_percent")) or 10.0))
    near_full_threshold = clamp_percent(config.get("quota_reset_near_full_percent"), 90.0)
    near_full_min_increase = max(
        0.0,
        float(number(config.get("quota_reset_near_full_min_increase_percent")) or 5.0),
    )
    refill_increase = max(0.0, current_remaining - low_watermark)
    quota_refilled = refill_increase >= increase_threshold or (
        current_remaining >= near_full_threshold and refill_increase >= near_full_min_increase
    )
    explicit_runtime_reset = int(number(nested(result, "auto_reconcile", "reset_count")) or 0) > 0
    explicit_reset_credit = int(number(nested(result, "reset_credit_grace", "confirmed_reset_count")) or 0) > 0

    reset_reason = ""
    should_reset = False
    if previous_baseline is None:
        should_reset = True
        reset_reason = "baseline_missing"
    elif previous_day != day_key:
        should_reset = True
        reset_reason = "day_changed"
    elif explicit_reset_credit:
        should_reset = True
        reset_reason = "reset_credit_consumed"
    elif explicit_runtime_reset:
        should_reset = True
        reset_reason = "runtime_quota_reset"
    else:
        candidate_reasons: list[str] = []
        if previous_signature and previous_signature != snapshot["account_signature"]:
            candidate_reasons.append("account_signature_changed")
        if previous_reset_at and previous_reset_at != int(snapshot["reset_at"]):
            candidate_reasons.append("weekly_reset_at_changed")
        if quota_refilled:
            candidate_reasons.append("weekly_quota_refilled")

        if candidate_reasons:
            candidate_key = "|".join([
                ",".join(candidate_reasons),
                str(snapshot["account_signature"]),
                str(int(snapshot["reset_at"])),
            ])
            candidate = budget_state.get("reset_candidate")
            if not isinstance(candidate, dict) or candidate.get("key") != candidate_key:
                candidate = {"key": candidate_key, "count": 0, "first_observed_at": now}
            candidate["count"] = int(candidate.get("count") or 0) + 1
            candidate["last_observed_at"] = now
            candidate["reasons"] = candidate_reasons
            candidate["remaining_percent"] = round(current_remaining, 6)
            candidate["increase_from_low_watermark_percent"] = round(refill_increase, 6)
            budget_state["reset_candidate"] = candidate

            confirmation_count = max(1, int(number(config.get("quota_reset_confirmation_count")) or 2))
            if int(candidate["count"]) >= confirmation_count:
                should_reset = True
                reset_reason = "+".join(candidate_reasons)
        else:
            budget_state.pop("reset_candidate", None)

    if should_reset:
        baseline = current_remaining
        daily_limit = max(0.0, baseline - reserve_total) / int(snapshot["days_remaining"])
        budget_state = {
            "day": day_key,
            "account_signature": snapshot["account_signature"],
            "reset_at": int(snapshot["reset_at"]),
            "baseline_remaining_percent": round(baseline, 6),
            "daily_limit_percent": round(daily_limit, 6),
            "minimum_remaining_percent_seen": round(current_remaining, 6),
            "daily_exhausted": False,
            "baseline_reset_reason": reset_reason,
            "baseline_reset_at": now,
            "baseline_reset_previous_percent": round(float(previous_baseline), 6) if previous_baseline is not None else None,
        }
        low_watermark = current_remaining

    baseline = float(number(budget_state.get("baseline_remaining_percent")) or current_remaining)
    daily_limit = max(0.0, float(number(budget_state.get("daily_limit_percent")) or 0.0))
    consumed_today = max(0.0, baseline - current_remaining)
    calculated_remaining_today = max(0.0, daily_limit - consumed_today)
    minimum_remaining_7d = float(snapshot["minimum_remaining_percent_7d"])
    minimum_remaining_5h = number(snapshot.get("minimum_remaining_percent_5h"))
    hard_reserve_available = minimum_remaining_7d > reserve_per_account + 0.000001
    if minimum_remaining_5h is not None:
        hard_reserve_available = hard_reserve_available and minimum_remaining_5h > reserve_5h_per_account + 0.000001
    daily_exhausted = bool(budget_state.get("daily_exhausted"))
    daily_budget_available = not daily_exhausted and calculated_remaining_today > 0.000001
    if not daily_budget_available and not daily_exhausted and calculated_remaining_today <= 0.000001:
        daily_exhausted = True
        budget_state["daily_exhausted_at"] = now
    remaining_today = calculated_remaining_today if daily_budget_available else 0.0
    original_quota_ok = bool(result.get("quota_ok", result.get("within_share")))
    quota_ok = original_quota_ok and hard_reserve_available and daily_budget_available

    budget_state.update({
        "last_remaining_percent": round(current_remaining, 6),
        "minimum_remaining_percent_seen": round(min(low_watermark, current_remaining), 6),
        "last_updated_at": now,
        "days_remaining": int(snapshot["days_remaining"]),
        "reset_after_seconds": int(snapshot["reset_after_seconds"]),
        "reserve_percent": round(reserve_total, 6),
        "minimum_remaining_percent_7d": round(minimum_remaining_7d, 6),
        "minimum_remaining_percent_5h": round(minimum_remaining_5h, 6) if minimum_remaining_5h is not None else None,
        "consumed_today_percent": round(consumed_today, 6),
        "calculated_remaining_today_percent": round(calculated_remaining_today, 6),
        "remaining_today_percent": round(remaining_today, 6),
        "daily_exhausted": daily_exhausted,
        "quota_ok": quota_ok,
    })
    state["dynamic_daily_budget"] = budget_state

    units_per_percent = max(0.0, float(number(config.get("balance_units_per_percent")) or 1.0))
    visible_balance = remaining_today * units_per_percent if quota_ok else 0.0
    original_usable = max(0.0, float(number(result.get("usable_balance_units")) or 0.0))
    if original_usable > 0:
        visible_balance = min(visible_balance, original_usable)

    result["guard_mode"] = "bucket_dynamic_daily_budget"
    result["quota_ok"] = quota_ok
    result["within_share"] = quota_ok
    result["usable_balance_units"] = round(visible_balance, 6)
    result["remaining_share_percent"] = round(remaining_today if quota_ok else 0.0, 6)
    result["dynamic_daily_budget"] = {"enabled": True, "applied": True, **budget_state}
    if not original_quota_ok:
        return result
    if not hard_reserve_available:
        result["reason"] = "protected_reserve_reached"
    elif not daily_budget_available:
        result["reason"] = "dynamic_daily_budget_exhausted"
    else:
        result["reason"] = "dynamic_daily_budget_available"
    return result


def quota_source_window(name: str, window: dict[str, Any]) -> dict[str, Any]:
    remaining = number(window.get("remaining_percent"))
    used = number(window.get("used_percent"))
    out: dict[str, Any] = {"name": name, "unit": "percent"}
    if remaining is not None:
        value = round(max(remaining, 0.0), 6)
        out["remaining"] = value
        out["remaining_percent"] = value
    if used is not None:
        value = round(max(used, 0.0), 6)
        out["used"] = value
        out["used_percent"] = value
    reset_after = number(window.get("reset_after_seconds"))
    if reset_after is not None:
        out["reset_after_seconds"] = int(reset_after)
    reset_at = number(window.get("reset_at"))
    if reset_at is not None and reset_at > 0:
        out["reset_at"] = int(reset_at)
    return out


def quota_source_windows(result: dict[str, Any]) -> list[dict[str, Any]]:
    windows = result.get("windows")
    if not isinstance(windows, dict):
        return []
    out: list[dict[str, Any]] = []
    for name in ("5h", "7d"):
        item = windows.get(name)
        if isinstance(item, dict):
            out.append(quota_source_window(name, item))
    for name, item in windows.items():
        if name in {"5h", "7d"} or not isinstance(item, dict):
            continue
        out.append(quota_source_window(str(name), item))
    return out


def quota_source_reserve_policy(result: dict[str, Any]) -> dict[str, Any] | None:
    policy: dict[str, Any] = {}
    min_5h = number(result.get("min_remaining_percent_5h"))
    min_7d = number(result.get("min_remaining_percent_7d"))
    if min_5h is not None:
        policy["min_remaining_percent_5h"] = round(min_5h, 6)
    if min_7d is not None:
        policy["min_remaining_percent_7d"] = round(min_7d, 6)
    buckets = result.get("buckets")
    if isinstance(buckets, dict):
        for bucket in buckets.values():
            if not isinstance(bucket, dict) or bucket.get("can_exhaust") is True:
                continue
            min_5h = number(bucket.get("min_remaining_percent_5h"))
            min_7d = number(bucket.get("min_remaining_percent_7d"))
            if min_5h is not None:
                policy["min_remaining_percent_5h"] = round(min_5h, 6)
            if min_7d is not None:
                policy["min_remaining_percent_7d"] = round(min_7d, 6)
    return policy or None


def quota_source_has_protected_bucket(result: dict[str, Any]) -> bool:
    buckets = result.get("buckets")
    if not isinstance(buckets, dict):
        return False
    for bucket in buckets.values():
        if isinstance(bucket, dict) and bucket.get("can_exhaust") is False:
            return True
    return False


def quota_source_window_remaining(result: dict[str, Any], name: str) -> float | None:
    windows = result.get("windows")
    if not isinstance(windows, dict):
        return None
    item = windows.get(name)
    if not isinstance(item, dict):
        return None
    remaining = number(item.get("remaining_percent"))
    return None if remaining is None else float(remaining)


def build_quota_source(result: dict[str, Any], balance: float, spendable: bool, now: int) -> dict[str, Any]:
    remaining_7d = quota_source_window_remaining(result, "7d")
    remaining_5h = quota_source_window_remaining(result, "5h")
    if not result.get("ok"):
        status = "unknown"
        reason = str(result.get("reason") or result.get("error") or "quota_probe_failed")
    elif remaining_7d is not None and remaining_7d <= 0.000001:
        status = "quota_7d_exhausted"
        reason = "quota_7d_exhausted"
    elif remaining_5h is not None and remaining_5h <= 0.000001:
        status = "quota_5h_exhausted"
        reason = "quota_5h_exhausted"
    elif not spendable:
        status = "quota_exhausted"
        reason = str(result.get("reason") or "no_spendable_balance")
    else:
        status = "available"
        reason = "within_budget"

    quota_feature = str(result.get("quota_feature") or "").strip()
    source_type = "model_quota_percent" if quota_feature else (
        "shared_protected_rolling_quota"
        if quota_source_has_protected_bucket(result) or quota_source_reserve_policy(result)
        else "rolling_window_quota"
    )
    raw_source: dict[str, Any] = {"source": "cliproxy_cpa_quota_guard"}
    if quota_feature:
        raw_source["quota_feature"] = quota_feature
        raw_source["quota_feature_limit_name"] = str(result.get("quota_feature_limit_name") or "").strip()
    return {
        "source_type": source_type,
        "unit": "percent" if quota_feature else "quota_unit",
        "balance": round(max(float(balance), 0.0), 6),
        "windows": quota_source_windows(result),
        "spendable": bool(spendable and status == "available"),
        "status": status,
        "status_reason": reason,
        "reserve_policy": quota_source_reserve_policy(result),
        "updated_at": now,
        "raw_source": raw_source,
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

    quota_source_balance = balance_update
    if quota_source_balance is None:
        quota_source_balance = float(first_non_empty(result.get("usable_balance_units"), result.get("balance_units")) or 0)
    other_info["quota_source"] = build_quota_source(result, quota_source_balance, desired_enabled, now)

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
    if str(config.get("quota_feature") or "").strip():
        config["dynamic_daily_budget_enabled"] = False
        config["auto_reconcile_runtime_quota"] = False
        config["reset_credit_grace_enabled"] = False

    try:
        result = call_quota_health(config, env)
        reconcile_summary = auto_reconcile_runtime_quota(config, env, result, state)
        if reconcile_summary["reset_count"] > 0:
            result = call_quota_health(config, env)
        if any(reconcile_summary.values()):
            result["auto_reconcile"] = reconcile_summary
        result = apply_reset_credit_grace(config, env, result, state)
        result = apply_dynamic_daily_budget(config, result, state)
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
