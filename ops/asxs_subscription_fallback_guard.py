#!/usr/bin/env python3
"""Layered fallback guard for ASXS subscription channels and zz1 cash channel.

Policy:
- ASXS 1.2x is the primary subscription channel and is disabled while its
  real health probe is unhealthy, then re-enabled after recovery.
- ASXS 5x is enabled only when ASXS 1.2x is unhealthy.
- zz1 spends real balance, so it is enabled only when both ASXS channels are
  unhealthy, or when ASXS shared daily remaining is below a configured floor.

The script intentionally lives outside New API and edits only channel status,
ability enabled flags, balance, and other_info through PostgreSQL. It never
prints provider keys or web access tokens.
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

STATUS_ENABLED = 1
STATUS_MANUALLY_DISABLED = 2
STATUS_AUTO_DISABLED = 3

DEFAULT_CONFIG = {
    "database": {"container": "new-api-postgres"},
    "docker": "/usr/bin/docker",
    "primary_channel_id": 1,
    "asxs_fallback_channel_id": 18,
    "cash_fallback_channel_id": 16,
    "redis_container": "new-api-redis",
    "health_model": "gpt-5.4-mini",
    "models_path": "/v1/models",
    "health_probe": {
        "enabled": True,
        "path": "/v1/chat/completions",
        "interval_seconds": 300,
        "failure_interval_seconds": 30,
        "prompt": "Return OK only.",
        "max_tokens": 4,
    },
    "asxs_usage_url": "https://api.asxs.top/api/usage",
    "probe_timeout_sec": 12,
    "fail_threshold": 3,
    "recover_threshold": 2,
    "fallback_hold_seconds": 300,
    "asxs_remaining_floor_usd": 30.0,
    "enable_cash_when_primary_unavailable": True,
    "enable_cash_when_asxs_low": True,
    "disable_primary_when_unavailable": True,
    "respect_manual_cash_enabled": True,
    "keep_asxs_fallback_standby_enabled": False,
    "sync_asxs_subscription_balance_channel_ids": [18],
    "status_monitor": {
        "enabled": True,
        "env_path": "/opt/new-api/ops/asxs_status_monitor.env",
        "dashboard_path": "/api/me/status/dashboard?period=1h",
        "primary_match": ["1.2"],
        "available_states": ["operational", "degraded"],
        "unavailable_states": ["error", "failed"],
        "max_age_seconds": 180,
    },
}


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
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=str(path.parent), delete=False) as f:
        json.dump(data, f, ensure_ascii=False, indent=2, sort_keys=True)
        f.write("\n")
        tmp = f.name
    os.replace(tmp, path)


def sql_literal(value: str) -> str:
    return "'" + str(value).replace("'", "''") + "'"


def deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    out = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = deep_merge(out[key], value)
        else:
            out[key] = value
    return out


def db_from_config(config: dict[str, Any]) -> DB:
    docker = str(config.get("docker", "/usr/bin/docker"))
    db_cfg = config.get("database", {})
    container = str(db_cfg.get("container", "new-api-postgres"))
    user = db_cfg.get("user")
    database = db_cfg.get("database")
    if not user:
        user = subprocess.check_output([docker, "exec", container, "printenv", "POSTGRES_USER"], text=True).strip()
    if not database:
        database = subprocess.check_output([docker, "exec", container, "printenv", "POSTGRES_DB"], text=True).strip()
    return DB(docker=docker, container=container, user=str(user), database=str(database))


def fetch_channels(db: DB, ids: list[int]) -> dict[int, dict[str, Any]]:
    sql = f"""
select coalesce(json_agg(row_to_json(t)), '[]'::json)
from (
  select id, name, key, status, base_url, setting, models, test_model, priority, weight, other_info, balance
  from channels
  where id in ({','.join(str(i) for i in ids)})
  order by id
) t;
"""
    rows = json.loads(db.psql(sql, capture=True) or "[]")
    return {int(row["id"]): row for row in rows}


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


def normalize_base_url(value: Any) -> str:
    return str(value or "").strip().rstrip("/")


def proxy_for_urllib(proxy: str) -> urllib.request.OpenerDirector | None:
    proxy = str(proxy or "").strip()
    if not proxy:
        return None
    # Channel settings use http://mihomo:7890 inside Docker. This script runs on
    # the host, where the same proxy is published on 127.0.0.1:7890.
    proxy = proxy.replace("http://mihomo:", "http://127.0.0.1:")
    proxy = proxy.replace("https://mihomo:", "http://127.0.0.1:")
    return urllib.request.build_opener(urllib.request.ProxyHandler({"http": proxy, "https": proxy}))


def read_response(req: urllib.request.Request, timeout: int, proxy: str = "") -> tuple[int, bytes]:
    opener = proxy_for_urllib(proxy)
    if opener is None:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return int(getattr(resp, "status", 0)), resp.read()
    with opener.open(req, timeout=timeout) as resp:
        return int(getattr(resp, "status", 0)), resp.read()


def read_response_with_headers(
    req: urllib.request.Request, timeout: int, proxy: str = ""
) -> tuple[int, bytes, dict[str, str]]:
    opener = proxy_for_urllib(proxy)
    if opener is None:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return int(getattr(resp, "status", 0)), resp.read(), dict(resp.headers.items())
    with opener.open(req, timeout=timeout) as resp:
        return int(getattr(resp, "status", 0)), resp.read(), dict(resp.headers.items())


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


def replace_env_value_atomic(path: Path, key: str, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = path.read_text(encoding="utf-8").splitlines() if path.exists() else []
    rendered = f'{key}="{value}"'
    updated = False
    output: list[str] = []
    for raw in lines:
        stripped = raw.strip()
        if stripped.startswith("#") or "=" not in stripped:
            output.append(raw)
            continue
        current_key = stripped.split("=", 1)[0].strip()
        if current_key == key:
            output.append(rendered)
            updated = True
        else:
            output.append(raw)
    if not updated:
        output.append(rendered)

    mode = path.stat().st_mode & 0o777 if path.exists() else 0o600
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=str(path.parent), delete=False) as f:
        f.write("\n".join(output))
        f.write("\n")
        tmp = f.name
    os.chmod(tmp, mode)
    os.replace(tmp, path)


def parse_iso_time(value: Any) -> float | None:
    if not value:
        return None
    text = str(value).strip()
    try:
        dt = datetime.fromisoformat(text.replace("Z", "+00:00"))
    except Exception:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.timestamp()


def normalize_text(value: Any) -> str:
    return str(value or "").strip().lower()


def monitor_match_values(value: Any) -> list[str]:
    if isinstance(value, list):
        return [normalize_text(item) for item in value if normalize_text(item)]
    text = normalize_text(value)
    return [text] if text else []


def find_status_monitor_item(dashboard: dict[str, Any], match_values: list[str]) -> dict[str, Any] | None:
    if not match_values:
        return None
    for group in dashboard.get("groups") or []:
        if not isinstance(group, dict):
            continue
        group_name = normalize_text(group.get("groupName"))
        for item in group.get("items") or []:
            if not isinstance(item, dict):
                continue
            haystack = " ".join(
                normalize_text(part)
                for part in [
                    group_name,
                    item.get("name"),
                    item.get("channelName"),
                    item.get("model"),
                    item.get("targetType"),
                    item.get("requestFormat"),
                ]
            )
            if any(needle in haystack for needle in match_values):
                item_copy = dict(item)
                item_copy["_groupName"] = group.get("groupName")
                return item_copy
    return None


def summarize_status_monitor_item(
    item: dict[str, Any] | None,
    *,
    available_states: set[str],
    unavailable_states: set[str],
    max_age_seconds: int,
) -> dict[str, Any]:
    if not item:
        return {"found": False, "available": None, "reason": "item_not_found"}
    latest = item.get("latest") if isinstance(item.get("latest"), dict) else {}
    status = normalize_text(latest.get("status"))
    checked_at = latest.get("checkedAt")
    checked_ts = parse_iso_time(checked_at)
    age_seconds = round(time.time() - checked_ts, 3) if checked_ts else None
    stale = bool(max_age_seconds > 0 and age_seconds is not None and age_seconds > max_age_seconds)
    available: bool | None
    reason: str
    if stale:
        available = None
        reason = "stale_status_monitor"
    elif status in available_states:
        available = True
        reason = f"status_monitor_{status}"
    elif status in unavailable_states:
        available = False
        reason = f"status_monitor_{status}"
    else:
        available = None
        reason = f"status_monitor_unknown_{status or 'empty'}"

    availability = item.get("availability") if isinstance(item.get("availability"), dict) else {}
    request_success = item.get("requestSuccess") if isinstance(item.get("requestSuccess"), dict) else {}
    return {
        "found": True,
        "available": available,
        "reason": reason,
        "group_name": item.get("_groupName"),
        "name": item.get("name"),
        "target_type": item.get("targetType"),
        "request_format": item.get("requestFormat"),
        "model": item.get("model"),
        "channel_name": item.get("channelName"),
        "latest": {
            "status": status,
            "http_status_code": latest.get("httpStatusCode"),
            "latency_ms": latest.get("latencyMs"),
            "ttfb_ms": latest.get("ttfbMs"),
            "checked_at": checked_at,
            "age_seconds": age_seconds,
        },
        "availability": {
            "availability_pct": availability.get("availabilityPct"),
            "operational_count": availability.get("operationalCount"),
            "total_checks": availability.get("totalChecks"),
        },
        "request_success": {
            "enabled": request_success.get("enabled"),
            "success_rate_pct": request_success.get("successRatePct"),
            "success_count": request_success.get("successCount"),
            "error_count": request_success.get("errorCount"),
            "total_requests": request_success.get("totalRequests"),
            "no_data": request_success.get("noData"),
        },
    }


def fetch_status_monitor(config: dict[str, Any], timeout: int, dry_run: bool) -> dict[str, Any]:
    monitor_cfg = config.get("status_monitor", {})
    if not monitor_cfg or monitor_cfg.get("enabled") is False:
        return {"ok": False, "reason": "disabled"}

    env_path = Path(str(monitor_cfg.get("env_path") or ""))
    env_values = load_env_values(env_path)
    base_url = str(env_values.get("ASXS_STATUS_BASE_URL") or monitor_cfg.get("base_url") or "").strip().rstrip("/")
    token = str(env_values.get("ASXS_ACCESS_TOKEN") or "").strip()
    if not base_url:
        return {"ok": False, "reason": "empty_base_url"}
    if not token:
        return {"ok": False, "reason": "empty_token"}

    dashboard_path = str(monitor_cfg.get("dashboard_path") or "/api/me/status/dashboard?period=1h").strip()
    url = dashboard_path if dashboard_path.startswith(("http://", "https://")) else base_url + "/" + dashboard_path.lstrip("/")
    proxy = str(env_values.get("ASXS_STATUS_PROXY") or monitor_cfg.get("proxy") or "")
    req = urllib.request.Request(
        url,
        headers={
            "Authorization": "Bearer " + token,
            "Accept": "application/json",
            "Accept-Language": "zh-CN",
            "Content-Type": "application/json",
            "User-Agent": "new-api-asxs-subscription-fallback-guard/1.0",
        },
        method="GET",
    )
    try:
        status, raw, headers = read_response_with_headers(req, timeout=timeout, proxy=proxy)
    except urllib.error.HTTPError as exc:
        return {"ok": False, "status": exc.code, "reason": f"http_{exc.code}"}
    except Exception as exc:
        return {"ok": False, "reason": exc.__class__.__name__}
    if status != 200:
        return {"ok": False, "status": status, "reason": f"http_{status}"}
    try:
        dashboard = json.loads(raw.decode("utf-8"))
    except Exception:
        return {"ok": False, "status": status, "reason": "invalid_json"}
    if not isinstance(dashboard, dict):
        return {"ok": False, "status": status, "reason": "payload_not_object"}

    new_token = headers.get("X-New-Token") or headers.get("x-new-token")
    token_refreshed = False
    if new_token and new_token != token and not dry_run:
        replace_env_value_atomic(env_path, "ASXS_ACCESS_TOKEN", str(new_token))
        token_refreshed = True

    available_states = set(monitor_match_values(monitor_cfg.get("available_states") or ["operational", "degraded"]))
    unavailable_states = set(monitor_match_values(monitor_cfg.get("unavailable_states") or ["error", "failed"]))
    max_age_seconds = int(monitor_cfg.get("max_age_seconds") or 0)
    primary_item = find_status_monitor_item(dashboard, monitor_match_values(monitor_cfg.get("primary_match") or ["1.2"]))
    primary_summary = summarize_status_monitor_item(
        primary_item,
        available_states=available_states,
        unavailable_states=unavailable_states,
        max_age_seconds=max_age_seconds,
    )
    return {
        "ok": True,
        "status": status,
        "period": dashboard.get("period"),
        "generated_at": dashboard.get("generatedAt"),
        "last_updated": dashboard.get("lastUpdated"),
        "poll_interval_sec": dashboard.get("pollIntervalSec"),
        "overall_status": dashboard.get("overallStatus"),
        "summary_counts": dashboard.get("summaryCounts"),
        "token_refreshed": token_refreshed,
        "primary": primary_summary,
    }


def read_temp_unsched(docker: str, redis_container: str, channel_id: int) -> dict[str, Any]:
    key = f"new-api:channel_scheduler:temp_unsched:v1:{int(channel_id)}"
    cmd = f'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --raw get "{key}"'
    try:
        proc = subprocess.run(
            [docker, "exec", redis_container, "sh", "-lc", cmd],
            text=True,
            capture_output=True,
            timeout=5,
        )
    except Exception as exc:
        return {"blocked": False, "error": exc.__class__.__name__}
    if proc.returncode != 0:
        return {"blocked": False, "error": "redis_cli_failed"}
    raw = proc.stdout.strip()
    if not raw:
        return {"blocked": False}
    try:
        data = json.loads(raw)
    except Exception:
        return {"blocked": False, "error": "invalid_json"}
    until = int(data.get("until_unix") or 0)
    now = int(time.time())
    if until > 0 and now > until:
        return {"blocked": False, "expired_until_unix": until}
    return {
        "blocked": True,
        "until_unix": until,
        "reason": data.get("reason", ""),
        "status_code": data.get("status_code", 0),
        "error_code": data.get("error_code", ""),
    }


def probe_channel(channel: dict[str, Any], model: str, path: str, timeout: int) -> dict[str, Any]:
    key = str(channel.get("key") or "")
    base_url = normalize_base_url(channel.get("base_url"))
    setting = parse_json_object(channel.get("setting"))
    proxy = str(setting.get("proxy") or "")
    if not key:
        return {"ok": False, "reason": "empty_key"}
    if not base_url:
        return {"ok": False, "reason": "empty_base_url"}
    url = base_url + path
    req = urllib.request.Request(
        url,
        headers={
            "Authorization": "Bearer " + key,
            "Accept": "application/json",
            "User-Agent": "new-api-asxs-subscription-fallback-guard/1.0",
        },
        method="GET",
    )
    start = time.monotonic()
    try:
        status, raw = read_response(req, timeout=timeout, proxy=proxy)
        elapsed_ms = int((time.monotonic() - start) * 1000)
    except urllib.error.HTTPError as exc:
        body = exc.read(240).decode("utf-8", "replace")
        return {"ok": False, "status": exc.code, "reason": f"http_{exc.code}", "body": body[:120]}
    except (urllib.error.URLError, socket.timeout, TimeoutError) as exc:
        return {"ok": False, "reason": exc.__class__.__name__}
    except Exception as exc:
        return {"ok": False, "reason": exc.__class__.__name__}

    if status != 200:
        return {"ok": False, "status": status, "latency_ms": elapsed_ms, "reason": f"http_{status}"}
    try:
        data = json.loads(raw.decode("utf-8"))
    except Exception:
        return {"ok": False, "status": status, "latency_ms": elapsed_ms, "reason": "invalid_json"}
    models: list[str] = []
    if isinstance(data, dict) and isinstance(data.get("data"), list):
        for item in data["data"]:
            if isinstance(item, dict) and item.get("id"):
                models.append(str(item["id"]))
    if model and model not in models:
        return {"ok": False, "status": status, "latency_ms": elapsed_ms, "reason": "model_missing", "models": len(models)}
    return {"ok": True, "status": status, "latency_ms": elapsed_ms, "models": len(models)}


def parse_error_info(payload: Any) -> dict[str, Any]:
    if not isinstance(payload, dict):
        return {}
    error = payload.get("error")
    if isinstance(error, dict):
        out: dict[str, Any] = {}
        if error.get("code"):
            out["error_code"] = error.get("code")
        if error.get("type"):
            out["error_type"] = error.get("type")
        if error.get("message"):
            out["message"] = str(error.get("message"))[:180]
        return out
    return {}


def compact_usage(payload: Any) -> dict[str, Any] | None:
    if not isinstance(payload, dict):
        return None
    usage = payload.get("usage")
    if not isinstance(usage, dict):
        return None
    out: dict[str, Any] = {}
    for key in ("prompt_tokens", "completion_tokens", "total_tokens"):
        if key in usage:
            out[key] = usage.get(key)
    details = usage.get("prompt_tokens_details")
    if isinstance(details, dict) and "cached_tokens" in details:
        out["cached_tokens"] = details.get("cached_tokens")
    return out


def probe_channel_generation(channel: dict[str, Any], model: str, probe_cfg: dict[str, Any], timeout: int) -> dict[str, Any]:
    key = str(channel.get("key") or "")
    base_url = normalize_base_url(channel.get("base_url"))
    setting = parse_json_object(channel.get("setting"))
    proxy = str(setting.get("proxy") or "")
    if not key:
        return {"ok": False, "reason": "empty_key"}
    if not base_url:
        return {"ok": False, "reason": "empty_base_url"}

    path = str(probe_cfg.get("path") or "/v1/chat/completions")
    prompt = str(probe_cfg.get("prompt") or "Return OK only.")
    max_tokens = int(probe_cfg.get("max_tokens") or 4)
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
        "temperature": 0,
        "stream": False,
    }
    req = urllib.request.Request(
        base_url + path,
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Authorization": "Bearer " + key,
            "Accept": "application/json",
            "Content-Type": "application/json",
            "User-Agent": "new-api-asxs-subscription-fallback-guard/1.0",
        },
        method="POST",
    )
    start = time.monotonic()
    try:
        status, raw = read_response(req, timeout=timeout, proxy=proxy)
        elapsed_ms = int((time.monotonic() - start) * 1000)
    except urllib.error.HTTPError as exc:
        elapsed_ms = int((time.monotonic() - start) * 1000)
        raw = exc.read(512)
        try:
            payload = json.loads(raw.decode("utf-8"))
        except Exception:
            payload = {"raw": raw.decode("utf-8", "replace")[:180]}
        out = {"ok": False, "status": exc.code, "latency_ms": elapsed_ms, "reason": f"http_{exc.code}"}
        out.update(parse_error_info(payload))
        return out
    except (urllib.error.URLError, socket.timeout, TimeoutError) as exc:
        return {"ok": False, "reason": exc.__class__.__name__}
    except Exception as exc:
        return {"ok": False, "reason": exc.__class__.__name__}

    if status != 200:
        return {"ok": False, "status": status, "latency_ms": elapsed_ms, "reason": f"http_{status}"}
    try:
        data = json.loads(raw.decode("utf-8"))
    except Exception:
        return {"ok": False, "status": status, "latency_ms": elapsed_ms, "reason": "invalid_json"}
    if isinstance(data, dict) and data.get("error"):
        out = {"ok": False, "status": status, "latency_ms": elapsed_ms, "reason": "response_error"}
        out.update(parse_error_info(data))
        return out
    if not isinstance(data, dict) or not data.get("choices"):
        return {"ok": False, "status": status, "latency_ms": elapsed_ms, "reason": "missing_choices"}
    out = {"ok": True, "status": status, "latency_ms": elapsed_ms, "reason": "chat_completion_ok"}
    usage = compact_usage(data)
    if usage:
        out["usage"] = usage
    return out


def cached_generation_probe(
    state: dict[str, Any],
    cid: int,
    success_interval_seconds: int,
    failure_interval_seconds: int,
    now: int,
) -> dict[str, Any] | None:
    entry = state.setdefault("channels", {}).setdefault(state_key(cid), {})
    cached = entry.get("last_generation_probe")
    if not isinstance(cached, dict):
        return None
    checked_at = int(cached.get("checked_at") or 0)
    result = cached.get("result")
    if checked_at <= 0 or not isinstance(result, dict):
        return None
    age_seconds = now - checked_at
    interval_seconds = success_interval_seconds if result.get("ok") else failure_interval_seconds
    if interval_seconds <= 0:
        return None
    if age_seconds < 0 or age_seconds > interval_seconds:
        return None
    out = dict(result)
    out["cached"] = True
    out["cache_age_seconds"] = age_seconds
    return out


def probe_channel_health(
    channel: dict[str, Any],
    model: str,
    models_path: str,
    timeout: int,
    config: dict[str, Any],
    state: dict[str, Any],
    now: int,
) -> dict[str, Any]:
    models_probe = probe_channel(channel, model, models_path, timeout)
    if not models_probe.get("ok"):
        return models_probe

    probe_cfg = config.get("health_probe")
    if not isinstance(probe_cfg, dict):
        probe_cfg = {}
    if probe_cfg.get("enabled") is False:
        return models_probe

    cid = int(channel.get("id") or 0)
    interval_seconds = int(probe_cfg.get("interval_seconds") or 0)
    failure_interval_seconds = int(probe_cfg.get("failure_interval_seconds") or 0)
    generation_probe = cached_generation_probe(state, cid, interval_seconds, failure_interval_seconds, now)
    if generation_probe is None:
        generation_probe = probe_channel_generation(channel, model, probe_cfg, timeout)
        entry = state.setdefault("channels", {}).setdefault(state_key(cid), {})
        entry["last_generation_probe"] = {
            "checked_at": now,
            "result": generation_probe,
        }

    out = dict(generation_probe)
    out["models_status"] = models_probe.get("status")
    out["models_latency_ms"] = models_probe.get("latency_ms")
    out["models"] = models_probe.get("models")
    return out


def fetch_asxs_usage(primary: dict[str, Any], usage_url: str, timeout: int) -> dict[str, Any]:
    key = str(primary.get("key") or "")
    setting = parse_json_object(primary.get("setting"))
    proxy = str(setting.get("proxy") or "")
    if not key:
        return {"ok": False, "reason": "empty_key"}
    req = urllib.request.Request(
        usage_url,
        headers={
            "Authorization": "Bearer " + key,
            "Accept": "application/json",
            "User-Agent": "new-api-asxs-subscription-fallback-guard/1.0",
        },
        method="GET",
    )
    try:
        status, raw = read_response(req, timeout=timeout, proxy=proxy)
    except urllib.error.HTTPError as exc:
        return {"ok": False, "status": exc.code, "reason": f"http_{exc.code}"}
    except Exception as exc:
        return {"ok": False, "reason": exc.__class__.__name__}
    if status != 200:
        return {"ok": False, "status": status, "reason": f"http_{status}"}
    try:
        data = json.loads(raw.decode("utf-8"))
    except Exception:
        return {"ok": False, "status": status, "reason": "invalid_json"}
    if not isinstance(data, list):
        return {"ok": False, "status": status, "reason": "payload_not_list"}
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
        return {"ok": False, "status": status, "reason": "no_daily_usd"}
    selected = None
    for item in candidates:
        name = str(item.get("planName") or "")
        if "日" in name or "daily" in name.lower():
            selected = item
            break
    if selected is None:
        selected = candidates[0]
    total = float(selected.get("total") or 0)
    remaining = float(selected.get("remaining") or 0)
    used = selected.get("used")
    used_float = float(used) if used is not None else max(total - remaining, 0)
    return {
        "ok": True,
        "plan_name": selected.get("planName", ""),
        "total_usd": round(total, 6),
        "used_usd": round(used_float, 6),
        "remaining_usd": round(remaining, 6),
        "items": len(data),
    }


def update_channel(
    db: DB,
    cid: int,
    *,
    status: int | None,
    abilities_enabled: bool | None,
    other_info: dict[str, Any],
    balance: float | None,
    dry_run: bool,
) -> None:
    now = int(time.time())
    statements = ["begin;"]
    sets: list[str] = []
    if status is not None:
        sets.append(f"status = {int(status)}")
    if balance is not None:
        sets.append(f"balance = {float(balance):.6f}")
        sets.append(f"balance_updated_time = {now}")
    sets.append("other_info = " + sql_literal(json.dumps(other_info, ensure_ascii=False, sort_keys=True)))
    if sets:
        statements.append(f"update channels set {', '.join(sets)} where id = {int(cid)};")
    if abilities_enabled is not None:
        statements.append(f"update abilities set enabled = {'true' if abilities_enabled else 'false'} where channel_id = {int(cid)};")
    statements.append("commit;")
    sql = "\n".join(statements)
    if dry_run:
        print(f"[dry-run] would update channel {cid} status={status} abilities={abilities_enabled} balance={balance} at {now}")
        return
    db.psql(sql)


def merge_guard_info(row: dict[str, Any], info: dict[str, Any]) -> dict[str, Any]:
    other = parse_json_object(row.get("other_info"))
    other["subscription_fallback_guard"] = info
    return other


def previous_guard_info(row: dict[str, Any]) -> dict[str, Any]:
    info = parse_json_object(row.get("other_info")).get("subscription_fallback_guard")
    return info if isinstance(info, dict) else {}


def state_key(cid: int) -> str:
    return str(int(cid))


def bump_health(state: dict[str, Any], cid: int, ok: bool) -> tuple[int, int]:
    channels = state.setdefault("channels", {})
    entry = channels.setdefault(state_key(cid), {})
    if ok:
        entry["successes"] = int(entry.get("successes") or 0) + 1
        entry["failures"] = 0
    else:
        entry["failures"] = int(entry.get("failures") or 0) + 1
        entry["successes"] = 0
    return int(entry.get("successes") or 0), int(entry.get("failures") or 0)


def set_channel_state(
    db: DB,
    row: dict[str, Any],
    desired_enabled: bool,
    reason: str,
    health: dict[str, Any],
    dry_run: bool,
    balance: float | None = None,
) -> str:
    cid = int(row["id"])
    current = int(row.get("status") or 0)
    guard_info = {
        "managed": True,
        "desired_enabled": desired_enabled,
        "reason": reason,
        "updated_at": int(time.time()),
        "health": health,
    }
    other = merge_guard_info(row, guard_info)
    if desired_enabled:
        if current == STATUS_MANUALLY_DISABLED:
            update_channel(db, cid, status=None, abilities_enabled=None, other_info=other, balance=balance, dry_run=dry_run)
            return f"channel {cid} {row.get('name')}: left manually disabled"
        if current != STATUS_ENABLED:
            update_channel(db, cid, status=STATUS_ENABLED, abilities_enabled=True, other_info=other, balance=balance, dry_run=dry_run)
            return f"channel {cid} {row.get('name')}: enabled ({reason})"
        update_channel(db, cid, status=None, abilities_enabled=None, other_info=other, balance=balance, dry_run=dry_run)
        return f"channel {cid} {row.get('name')}: already enabled ({reason})"
    if current == STATUS_MANUALLY_DISABLED:
        update_channel(db, cid, status=None, abilities_enabled=None, other_info=other, balance=balance, dry_run=dry_run)
        return f"channel {cid} {row.get('name')}: left manually disabled ({reason})"
    if current != STATUS_AUTO_DISABLED:
        update_channel(db, cid, status=STATUS_AUTO_DISABLED, abilities_enabled=False, other_info=other, balance=balance, dry_run=dry_run)
        return f"channel {cid} {row.get('name')}: auto-disabled ({reason})"
    update_channel(db, cid, status=None, abilities_enabled=False, other_info=other, balance=balance, dry_run=dry_run)
    return f"channel {cid} {row.get('name')}: remains auto-disabled ({reason})"


def run(config_path: Path, state_path: Path, dry_run: bool) -> int:
    config = deep_merge(DEFAULT_CONFIG, load_json(config_path, {}))
    primary_id = int(config["primary_channel_id"])
    asxs5_id = int(config["asxs_fallback_channel_id"])
    cash_id = int(config["cash_fallback_channel_id"])
    ids = [primary_id, asxs5_id, cash_id]

    db = db_from_config(config)
    rows = fetch_channels(db, ids)
    missing = [cid for cid in ids if cid not in rows]
    if missing:
        raise RuntimeError(f"missing channels: {missing}")

    state = load_json(state_path, {"version": 1, "channels": {}})
    now = int(time.time())
    timeout = int(config.get("probe_timeout_sec", 12))
    model = str(config.get("health_model", "gpt-5.4-mini"))
    models_path = str(config.get("models_path", "/v1/models"))
    fail_threshold = int(config.get("fail_threshold", 3))
    recover_threshold = int(config.get("recover_threshold", 2))
    floor_usd = float(config.get("asxs_remaining_floor_usd", 30.0))

    primary_probe = probe_channel_health(rows[primary_id], model, models_path, timeout, config, state, now)
    asxs5_probe = probe_channel_health(rows[asxs5_id], model, models_path, timeout, config, state, now)
    cash_probe = probe_channel_health(rows[cash_id], model, models_path, timeout, config, state, now)
    usage = fetch_asxs_usage(rows[primary_id], str(config.get("asxs_usage_url")), timeout)
    status_monitor = fetch_status_monitor(config, timeout, dry_run)

    docker = str(config.get("docker", "/usr/bin/docker"))
    redis_container = str(config.get("redis_container", "new-api-redis"))
    primary_temp = read_temp_unsched(docker, redis_container, primary_id)
    asxs5_temp = read_temp_unsched(docker, redis_container, asxs5_id)
    cash_temp = read_temp_unsched(docker, redis_container, cash_id)

    primary_successes, primary_failures = bump_health(state, primary_id, bool(primary_probe.get("ok")))
    asxs5_successes, asxs5_failures = bump_health(state, asxs5_id, bool(asxs5_probe.get("ok")))
    cash_successes, cash_failures = bump_health(state, cash_id, bool(cash_probe.get("ok")))
    state["last_run_at"] = now
    state["last_primary_probe"] = {k: v for k, v in primary_probe.items() if k != "body"}
    state["last_asxs5_probe"] = {k: v for k, v in asxs5_probe.items() if k != "body"}
    state["last_cash_probe"] = {k: v for k, v in cash_probe.items() if k != "body"}
    state["last_temp_unsched"] = {
        "primary": primary_temp,
        "asxs5": asxs5_temp,
        "cash": cash_temp,
    }
    state["last_usage"] = usage
    state["last_status_monitor"] = status_monitor

    primary_probe_ok = bool(primary_probe.get("ok"))
    asxs5_probe_ok = bool(asxs5_probe.get("ok"))
    cash_probe_ok = bool(cash_probe.get("ok"))
    primary_considered_healthy = primary_probe_ok and primary_successes >= recover_threshold
    asxs5_considered_healthy = asxs5_probe_ok and asxs5_successes >= recover_threshold
    cash_considered_healthy = cash_probe_ok and cash_successes >= recover_threshold

    primary_temp_blocked = bool(primary_temp.get("blocked"))
    asxs5_temp_blocked = bool(asxs5_temp.get("blocked"))
    cash_temp_blocked = bool(cash_temp.get("blocked"))

    channels_state = state.setdefault("channels", {})
    primary_state = channels_state.setdefault(state_key(primary_id), {})
    if primary_temp_blocked:
        primary_state["last_temp_blocked_at"] = now
    last_primary_temp_blocked_at = int(primary_state.get("last_temp_blocked_at") or 0)
    fallback_hold_seconds = int(config.get("fallback_hold_seconds", 300))
    in_fallback_hold = bool(
        fallback_hold_seconds > 0
        and last_primary_temp_blocked_at > 0
        and now - last_primary_temp_blocked_at <= fallback_hold_seconds
    )

    primary_probe_available = primary_considered_healthy and not primary_temp_blocked
    asxs5_available = asxs5_considered_healthy and not asxs5_temp_blocked
    cash_available = cash_considered_healthy and not cash_temp_blocked
    primary_monitor = status_monitor.get("primary") if isinstance(status_monitor.get("primary"), dict) else {}
    primary_monitor_available = primary_monitor.get("available") if isinstance(primary_monitor, dict) else None
    if isinstance(primary_monitor_available, bool):
        primary_available = primary_monitor_available
        primary_available_source = "status_monitor"
    else:
        primary_available = primary_probe_available
        primary_available_source = "probe"

    remaining_usd = float(usage.get("remaining_usd") or 0) if usage.get("ok") else None
    asxs_low = remaining_usd is not None and remaining_usd <= floor_usd
    sync_balance_ids = {
        int(cid)
        for cid in config.get("sync_asxs_subscription_balance_channel_ids", [])
        if str(cid).strip().lstrip("-").isdigit()
    }

    primary_unavailable = not primary_available
    disable_primary_when_unavailable = bool(config.get("disable_primary_when_unavailable", True))
    desired_primary = not (disable_primary_when_unavailable and primary_unavailable)
    if desired_primary:
        primary_reason = "primary_subscription"
    elif primary_available_source == "status_monitor":
        primary_reason = "status_monitor_primary_unavailable"
    elif primary_temp_blocked:
        primary_reason = "primary_temp_unschedulable"
    else:
        primary_reason = "primary_unhealthy"
    standby_asxs5 = bool(config.get("keep_asxs_fallback_standby_enabled", True))
    asxs5_can_serve = bool(asxs5_probe.get("ok") or asxs5_available) and not asxs5_temp_blocked
    cash_can_serve = bool(cash_probe.get("ok") or cash_available) and not cash_temp_blocked
    desired_asxs5 = primary_unavailable and asxs5_can_serve and not asxs_low
    desired_cash = False
    cash_reason = "cash_balance_protected"
    cash_current_status = int(rows[cash_id].get("status") or 0)
    previous_cash_guard = previous_guard_info(rows[cash_id])
    previous_cash_guard_enabled = previous_cash_guard.get("desired_enabled")
    respect_manual_cash_enabled = bool(config.get("respect_manual_cash_enabled", True))
    cash_manually_enabled = (
        respect_manual_cash_enabled
        and cash_current_status == STATUS_ENABLED
        and previous_cash_guard_enabled is not True
    )
    if cash_manually_enabled:
        desired_cash = True
        cash_reason = "manual_cash_enabled"
    elif asxs_low and bool(config.get("enable_cash_when_asxs_low", True)) and cash_can_serve:
        desired_cash = True
        cash_reason = f"asxs_remaining_low_{remaining_usd:.4f}_usd"
    elif primary_unavailable and bool(config.get("enable_cash_when_primary_unavailable", True)) and cash_can_serve:
        desired_cash = True
        cash_reason = "primary_asxs_unavailable_cash_fallback"
    elif primary_unavailable and (not asxs5_available or not bool(asxs5_probe.get("ok"))) and cash_can_serve:
        desired_cash = True
        cash_reason = "both_asxs_paths_unhealthy_or_temp_blocked"
    elif (primary_unavailable or asxs_low) and not cash_can_serve:
        cash_reason = "cash_unhealthy_or_temp_blocked"

    health_payload = {
        "model": model,
        "primary": {"probe": primary_probe, "successes": primary_successes, "failures": primary_failures, "temp_unsched": primary_temp, "temp_blocked": primary_temp_blocked},
        "asxs5": {"probe": asxs5_probe, "successes": asxs5_successes, "failures": asxs5_failures, "temp_unsched": asxs5_temp, "temp_blocked": asxs5_temp_blocked},
        "cash": {"probe": cash_probe, "successes": cash_successes, "failures": cash_failures, "temp_unsched": cash_temp, "temp_blocked": cash_temp_blocked},
        "temp_unsched": {"primary": primary_temp, "asxs5": asxs5_temp, "cash": cash_temp},
        "usage": usage,
        "status_monitor": status_monitor,
        "primary_available": primary_available,
        "primary_available_source": primary_available_source,
        "primary_probe_available": primary_probe_available,
        "asxs5_available": asxs5_available,
        "cash_available": cash_available,
        "fail_threshold": fail_threshold,
        "recover_threshold": recover_threshold,
        "fallback_hold_seconds": fallback_hold_seconds,
        "keep_asxs_fallback_standby_enabled": standby_asxs5,
        "enable_cash_when_primary_unavailable": bool(config.get("enable_cash_when_primary_unavailable", True)),
        "disable_primary_when_unavailable": disable_primary_when_unavailable,
        "respect_manual_cash_enabled": respect_manual_cash_enabled,
        "cash_manually_enabled": cash_manually_enabled,
        "previous_cash_guard_enabled": previous_cash_guard_enabled,
        "in_fallback_hold": in_fallback_hold,
        "last_primary_temp_blocked_at": last_primary_temp_blocked_at,
        "asxs_remaining_floor_usd": floor_usd,
    }

    summaries = []
    summaries.append(set_channel_state(db, rows[primary_id], desired_primary, primary_reason, health_payload, dry_run))
    if desired_asxs5:
        if primary_available_source == "status_monitor":
            asxs5_reason = "status_monitor_primary_unavailable"
        elif primary_temp_blocked:
            asxs5_reason = "primary_temp_unschedulable"
        elif primary_unavailable:
            asxs5_reason = "primary_unhealthy_subscription_fallback"
        else:
            asxs5_reason = "primary_unavailable"
    elif asxs_low:
        asxs5_reason = f"asxs_remaining_low_{remaining_usd:.4f}_usd" if remaining_usd is not None else "asxs_remaining_low"
    elif primary_unavailable and not asxs5_can_serve:
        asxs5_reason = "primary_unavailable_asxs5_unhealthy_or_temp_blocked"
    elif primary_available_source == "status_monitor":
        asxs5_reason = "status_monitor_primary_available"
    else:
        asxs5_reason = "primary_healthy"
    asxs5_display_balance = remaining_usd if remaining_usd is not None and asxs5_id in sync_balance_ids else None
    summaries.append(set_channel_state(db, rows[asxs5_id], desired_asxs5, asxs5_reason, health_payload, dry_run, balance=asxs5_display_balance))
    summaries.append(set_channel_state(db, rows[cash_id], desired_cash, cash_reason, health_payload, dry_run))

    if not dry_run:
        save_json_atomic(state_path, state)
    for line in summaries:
        print(line)
    print(
        "summary: "
        f"primary_ok={primary_probe.get('ok')} primary_failures={primary_failures} primary_temp={primary_temp_blocked} "
        f"asxs5_ok={asxs5_probe.get('ok')} asxs5_failures={asxs5_failures} asxs5_temp={asxs5_temp_blocked} "
        f"cash_ok={cash_probe.get('ok')} cash_failures={cash_failures} cash_temp={cash_temp_blocked} "
        f"hold={in_fallback_hold} standby_asxs5={standby_asxs5} primary_source={primary_available_source} "
        f"monitor_primary={primary_monitor.get('reason') if isinstance(primary_monitor, dict) else None} "
        f"asxs_remaining={remaining_usd} desired_primary={desired_primary} desired_asxs5={desired_asxs5} desired_cash={desired_cash}"
    )
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="/opt/new-api/ops/asxs_subscription_fallback_guard.json")
    parser.add_argument("--state", default="/opt/new-api/ops/asxs_subscription_fallback_state.json")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    return run(Path(args.config), Path(args.state), args.dry_run)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
