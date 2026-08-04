#!/usr/bin/env python3
"""Low-impact, site-aware network quality probe for CMSG routing.

The probe is intentionally observational. It samples direct egress, Mihomo
node delay, host TCP counters, and incremental New API/CPA/Mihomo error logs.
It never changes routing or exposes raw log lines and credentials.
"""

from __future__ import annotations

import datetime as dt
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import urllib.parse
from collections import Counter
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo


DEFAULT_CONFIG: dict[str, Any] = {
    "enabled": True,
    "schema_version": 2,
    "site_id": "unknown",
    "site_role": "unknown",
    "timezone": "Asia/Shanghai",
    "state_path": "/opt/new-api/ops/cmsg_network_probe_state.json",
    "output_path": "/opt/new-api/logs/cmsg_network_probe.jsonl",
    "docker": "/usr/bin/docker",
    "mihomo_container": "mihomo",
    "new_api_container": "new-api",
    "mihomo_secret_file": "/opt/cliproxyapi/secrets/mihomo-controller",
    "connect_timeout_sec": 3,
    "max_time_sec": 8,
    "speed_test": {
        "enabled": True,
        "url": "https://speed.cloudflare.com/__down?bytes=65536",
        "bytes": 65536,
    },
    "head_urls": [
        "https://mirrors.tuna.tsinghua.edu.cn/",
        "https://www.cloudflare.com/",
        "https://raw.githubusercontent.com/Yuming12138/newapi-cmsg/dev/cmsg/README.md",
        "https://chatgpt.com/",
    ],
    # Direct chatgpt.com is expected to fail on many mainland routes. Keep it
    # as context, but do not use it as an origin-health signal.
    "diagnostic_only_head_hosts": ["chatgpt.com"],
    "mihomo_group": "OpenAI稳定",
    "mihomo_delay_url": "https://chatgpt.com/backend-api/codex/responses",
    "mihomo_nodes": [
        "🇯🇵 日本1 (移动>电信>联通)",
        "🇸🇬 新加坡1 (移动联通>电信)",
        "🇺🇸 美国1",
    ],
    "mihomo_delay_timeout_ms": 8000,
    "log_window": {
        "fallback_sec": 360,
        "max_lookback_sec": 900,
    },
    "new_api_logs": {
        "enabled": True,
        "container": "new-api",
        "tail": 3000,
        "patterns": {
            # Legacy v1 names remain available for existing analysis scripts.
            "channel_error": r"\bchannel error\b",
            "upstream_error": r"\bupstream error\b",
            "usage_limit": r"usage limit|status code:\s*429",
            "channel_error_total": r"\bchannel error\b",
            "network_5xx": r"channel error .*status code:\s*5\d\d",
            "quota_429": r"channel error .*status code:\s*429|usage limit has been reached",
            "upstream_request_failed": r"upstream error:\s*do request failed",
            "protocol_error": r"PROTOCOL_ERROR",
            "unexpected_eof": r"(?:channel|upstream) error .*unexpected EOF",
            "client_request_eof": r"Invalid request:.*unexpected EOF",
            "success_consume": r"record consume log",
        },
    },
    "cpa_logs": {
        "enabled": True,
        "path": "/opt/cliproxyapi/logs/main.log",
        "timezone": "UTC",
        "max_tail_bytes": 4 * 1024 * 1024,
        "patterns": {
            # Count the canonical shadow observation once per H2 failure.
            "h2_protocol_error": r"failure observed .*failure_class=h2_protocol_error",
            "terminal_protocol_error": r"stream execution failed .*PROTOCOL_ERROR",
            "unexpected_eof": r"stream execution failed .*unexpected EOF",
            "io_timeout": r"stream execution failed .*i/o timeout",
            "deadline_exceeded": r"stream execution failed .*context deadline exceeded",
            "connection_reset": r"stream execution failed .*connection reset",
        },
    },
    "mihomo_logs": {
        "enabled": True,
        "container": "mihomo",
        "tail": 3000,
        "patterns": {
            "connect_io_timeout": r"connect error:.*i/o timeout",
            "deadline_exceeded": r"context deadline exceeded",
            "connection_reset": r"connection reset",
            "dns_error": r"no such host|could not resolve",
        },
    },
}


def deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    out = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = deep_merge(out[key], value)
        else:
            out[key] = value
    return out


def normalize_config(override: dict[str, Any]) -> dict[str, Any]:
    config = deep_merge(DEFAULT_CONFIG, override)
    # Compatibility with the v1 config name.
    legacy_logs = override.get("docker_logs")
    if isinstance(legacy_logs, dict) and not isinstance(override.get("new_api_logs"), dict):
        config["new_api_logs"] = deep_merge(config["new_api_logs"], legacy_logs)
    return config


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


def run_cmd(cmd: list[str], timeout: float) -> tuple[int, str, str, float]:
    start = time.monotonic()
    try:
        proc = subprocess.run(cmd, text=True, capture_output=True, timeout=timeout)
        elapsed = time.monotonic() - start
        return proc.returncode, proc.stdout, proc.stderr, elapsed
    except subprocess.TimeoutExpired as exc:
        elapsed = time.monotonic() - start
        stdout = exc.stdout if isinstance(exc.stdout, str) else ""
        stderr = exc.stderr if isinstance(exc.stderr, str) else ""
        return 124, stdout, stderr or "timeout", elapsed


def safe_error_text(value: str) -> str:
    value = re.sub(r"sk-[A-Za-z0-9_-]+", "sk-<redacted>", value or "")
    value = re.sub(r"(?i)(authorization:\s*bearer\s+)[A-Za-z0-9._-]+", r"\1<redacted>", value)
    lines = [line.strip() for line in value.splitlines() if line.strip()]
    return (lines[-1] if lines else "unknown error")[:240]


def parse_iso_timestamp(value: Any) -> dt.datetime | None:
    if not isinstance(value, str) or not value.strip():
        return None
    raw = value.strip()
    if raw.endswith("Z"):
        raw = raw[:-1] + "+00:00"
    try:
        parsed = dt.datetime.fromisoformat(raw)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return parsed.astimezone(dt.timezone.utc)


def timezone_for_name(value: Any) -> dt.tzinfo:
    name = str(value or "UTC").strip()
    if name.upper() in {"UTC", "ETC/UTC", "GMT"}:
        return dt.timezone.utc
    return ZoneInfo(name)


def resolve_log_window(
    now_utc: dt.datetime,
    previous: dict[str, Any],
    config: dict[str, Any],
) -> tuple[dt.datetime, dt.datetime, dict[str, Any]]:
    window_cfg = config.get("log_window", {})
    fallback_sec = max(60, int(window_cfg.get("fallback_sec", 360)))
    max_lookback_sec = max(fallback_sec, int(window_cfg.get("max_lookback_sec", 900)))
    since = parse_iso_timestamp(previous.get("ts")) if isinstance(previous, dict) else None
    source = "state"
    truncated = False
    if since is None or since >= now_utc:
        since = now_utc - dt.timedelta(seconds=fallback_sec)
        source = "fallback"
    oldest = now_utc - dt.timedelta(seconds=max_lookback_sec)
    if since < oldest:
        since = oldest
        truncated = True
    seconds = max(0.0, (now_utc - since).total_seconds())
    return since, now_utc, {
        "since": since.isoformat(),
        "until": now_utc.isoformat(),
        "seconds": round(seconds, 3),
        "source": source,
        "truncated": truncated,
        "overlap_expected": False,
    }


def curl_metrics(url: str, config: dict[str, Any], *, head: bool, output: str = "/dev/null") -> dict[str, Any]:
    fmt = "http_code=%{http_code} remote_ip=%{remote_ip} namelookup=%{time_namelookup} connect=%{time_connect} tls=%{time_appconnect} ttfb=%{time_starttransfer} total=%{time_total} size=%{size_download} speed=%{speed_download}"
    cmd = [
        "/usr/bin/curl",
        "-sS",
        "-L",
        "--connect-timeout",
        str(config["connect_timeout_sec"]),
        "--max-time",
        str(config["max_time_sec"]),
        "-o",
        output,
        "-w",
        fmt,
    ]
    if head:
        cmd.append("-I")
    cmd.append(url)
    code, stdout, stderr, elapsed = run_cmd(cmd, float(config["max_time_sec"]) + 2)
    values: dict[str, Any] = {"url": url, "ok": code == 0, "rc": code, "elapsed_sec": round(elapsed, 3)}
    for item in stdout.strip().split():
        if "=" not in item:
            continue
        key, value = item.split("=", 1)
        if key in {"http_code", "size"}:
            try:
                values[key] = int(float(value))
            except ValueError:
                values[key] = value
        elif key in {"namelookup", "connect", "tls", "ttfb", "total", "speed"}:
            try:
                values[key] = round(float(value), 6)
            except ValueError:
                values[key] = value
        else:
            values[key] = value
    if stderr.strip():
        values["error"] = safe_error_text(stderr)
    return values


def read_nstat() -> dict[str, int]:
    keys = {
        "TcpActiveOpens",
        "TcpPassiveOpens",
        "TcpAttemptFails",
        "TcpEstabResets",
        "TcpInSegs",
        "TcpOutSegs",
        "TcpRetransSegs",
        "TcpExtTCPTimeouts",
        "TcpExtTCPLostRetransmit",
        "TcpExtTCPFastRetrans",
        "IpInDiscards",
        "IpOutDiscards",
    }
    code, stdout, _, _ = run_cmd(["/usr/bin/nstat", "-az"], 5)
    if code != 0:
        return {}
    out: dict[str, int] = {}
    for line in stdout.splitlines():
        parts = line.split()
        if len(parts) >= 2 and parts[0] in keys:
            try:
                out[parts[0]] = int(float(parts[1]))
            except ValueError:
                pass
    return out


def delta_counters(current: dict[str, int], previous: dict[str, Any]) -> tuple[dict[str, int], list[str]]:
    old = previous.get("nstat") if isinstance(previous, dict) else None
    if not isinstance(old, dict):
        return {}, []
    delta: dict[str, int] = {}
    resets: list[str] = []
    for key, value in current.items():
        try:
            old_value = int(old.get(key, value))
        except Exception:
            continue
        if value < old_value:
            resets.append(key)
            continue
        delta[key] = value - old_value
    return delta, resets


def nstat_rates(delta: dict[str, int]) -> dict[str, float]:
    out: dict[str, float] = {}
    sent = int(delta.get("TcpOutSegs", 0))
    retrans = int(delta.get("TcpRetransSegs", 0))
    if sent > 0:
        out["tcp_retrans_rate"] = round(retrans / sent, 8)
        out["tcp_retrans_percent"] = round(retrans * 100.0 / sent, 6)
    connections = int(delta.get("TcpActiveOpens", 0)) + int(delta.get("TcpPassiveOpens", 0))
    if connections > 0:
        out["tcp_attempt_fail_per_1000_connections"] = round(
            int(delta.get("TcpAttemptFails", 0)) * 1000.0 / connections,
            3,
        )
        out["tcp_reset_per_1000_connections"] = round(
            int(delta.get("TcpEstabResets", 0)) * 1000.0 / connections,
            3,
        )
    return out


def docker_inspect_pid(docker: str, container: str) -> str:
    code, stdout, stderr, _ = run_cmd([docker, "inspect", "-f", "{{.State.Pid}}", container], 5)
    if code != 0:
        raise RuntimeError(safe_error_text(stderr or stdout or f"failed to inspect {container}"))
    return stdout.strip()


def mihomo_get(config: dict[str, Any], path: str) -> tuple[int, str, str, float]:
    docker = str(config["docker"])
    pid = docker_inspect_pid(docker, str(config["mihomo_container"]))
    secret = Path(str(config["mihomo_secret_file"])).read_text(encoding="utf-8").strip()
    cmd = [
        "/usr/bin/nsenter",
        "-t",
        pid,
        "-n",
        "/usr/bin/curl",
        "-fsS",
        "--connect-timeout",
        str(config["connect_timeout_sec"]),
        "--max-time",
        str(config["max_time_sec"]),
        "-H",
        f"Authorization: Bearer {secret}",
        f"http://127.0.0.1:9090{path}",
    ]
    return run_cmd(cmd, float(config["max_time_sec"]) + 2)


def mihomo_snapshot(config: dict[str, Any]) -> dict[str, Any]:
    group_name = str(config["mihomo_group"])
    encoded_group = urllib.parse.quote(group_name, safe="")
    result: dict[str, Any] = {"group": group_name, "nodes": []}
    try:
        code, stdout, stderr, elapsed = mihomo_get(config, f"/proxies/{encoded_group}")
    except Exception as exc:
        return {"group": group_name, "nodes": [], "group_ok": False, "group_error": safe_error_text(str(exc))}
    result["group_ok"] = code == 0
    result["group_elapsed_sec"] = round(elapsed, 3)
    if code == 0:
        try:
            group = json.loads(stdout)
            result["selected"] = group.get("now")
            result["type"] = group.get("type")
        except Exception as exc:
            result["group_error"] = safe_error_text(str(exc))
    else:
        result["group_error"] = safe_error_text(stderr or stdout)
    delay_url = urllib.parse.quote(str(config["mihomo_delay_url"]), safe="")
    for node in config.get("mihomo_nodes", []):
        node_name = str(node)
        encoded_node = urllib.parse.quote(node_name, safe="")
        path = f"/proxies/{encoded_node}/delay?timeout={int(config['mihomo_delay_timeout_ms'])}&url={delay_url}"
        try:
            code, stdout, stderr, elapsed = mihomo_get(config, path)
        except Exception as exc:
            result["nodes"].append({"name": node_name, "ok": False, "error": safe_error_text(str(exc))})
            continue
        item: dict[str, Any] = {"name": node_name, "ok": code == 0, "elapsed_sec": round(elapsed, 3)}
        if code == 0:
            try:
                item.update(json.loads(stdout))
            except Exception:
                item["raw"] = stdout.strip()[:240]
        else:
            item["error"] = safe_error_text(stderr or stdout)
        result["nodes"].append(item)
    return result


def count_pattern_lines(lines: list[str], patterns: dict[str, Any]) -> dict[str, int]:
    compiled = {str(name): re.compile(str(pattern), flags=re.IGNORECASE) for name, pattern in patterns.items()}
    return {
        name: sum(1 for line in lines if pattern.search(line))
        for name, pattern in compiled.items()
    }


def collect_docker_log_snapshot(
    config: dict[str, Any],
    log_cfg: dict[str, Any],
    since: dt.datetime,
    until: dt.datetime,
) -> tuple[dict[str, Any], list[str]]:
    container = str(log_cfg.get("container") or "")
    result: dict[str, Any] = {
        "enabled": bool(log_cfg.get("enabled", True)),
        "source": "docker",
        "container": container,
        "ok": None,
        "counts": {},
    }
    if not result["enabled"]:
        return result, []
    if not container:
        result["error"] = "container is not configured"
        return result, []
    tail_limit = int(log_cfg.get("tail", 3000))
    cmd = [
        str(config["docker"]),
        "logs",
        "--since",
        since.isoformat(),
        "--until",
        until.isoformat(),
        "--tail",
        str(tail_limit),
        container,
    ]
    code, stdout, stderr, elapsed = run_cmd(cmd, 15)
    text = stdout + "\n" + stderr
    lines = [line for line in text.splitlines() if line.strip()]
    result["elapsed_sec"] = round(elapsed, 3)
    result["lines_scanned"] = len(lines)
    result["tail_limit"] = tail_limit
    result["tail_limit_reached"] = len(lines) >= tail_limit
    result["ok"] = code == 0
    result["counts"] = count_pattern_lines(lines, log_cfg.get("patterns", {}))
    if code != 0:
        result["error"] = safe_error_text(stderr or stdout)
    return result, lines


def read_tail_text(path: Path, max_bytes: int) -> tuple[str, bool]:
    size = path.stat().st_size
    max_bytes = max(4096, int(max_bytes))
    offset = max(0, size - max_bytes)
    with path.open("rb") as f:
        f.seek(offset)
        raw = f.read()
    if offset > 0:
        first_newline = raw.find(b"\n")
        raw = raw[first_newline + 1 :] if first_newline >= 0 else b""
    return raw.decode("utf-8", errors="replace"), offset > 0


CPA_TIMESTAMP_RE = re.compile(r"^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]")


def collect_cpa_log_snapshot(
    log_cfg: dict[str, Any],
    since: dt.datetime,
    until: dt.datetime,
) -> dict[str, Any]:
    path = Path(str(log_cfg.get("path") or ""))
    result: dict[str, Any] = {
        "enabled": bool(log_cfg.get("enabled", True)),
        "source": "file",
        "path": str(path),
        "ok": None,
        "counts": {},
        "failure_nodes": {},
    }
    if not result["enabled"]:
        return result
    if not path.is_file():
        result["error"] = "CPA log file is missing"
        return result
    try:
        log_timezone = timezone_for_name(log_cfg.get("timezone"))
        text, tail_truncated = read_tail_text(path, int(log_cfg.get("max_tail_bytes", 4 * 1024 * 1024)))
    except Exception as exc:
        result["error"] = safe_error_text(str(exc))
        return result
    lines: list[str] = []
    timestamped = 0
    earliest_timestamp: dt.datetime | None = None
    for line in text.splitlines():
        match = CPA_TIMESTAMP_RE.match(line)
        if not match:
            continue
        timestamped += 1
        try:
            parsed = dt.datetime.strptime(match.group(1), "%Y-%m-%d %H:%M:%S").replace(tzinfo=log_timezone)
        except ValueError:
            continue
        parsed_utc = parsed.astimezone(dt.timezone.utc)
        if earliest_timestamp is None or parsed_utc < earliest_timestamp:
            earliest_timestamp = parsed_utc
        if since <= parsed_utc <= until:
            lines.append(line)
    result["ok"] = True
    result["tail_read_truncated"] = tail_truncated
    result["window_truncated"] = bool(tail_truncated and earliest_timestamp is not None and earliest_timestamp > since)
    result["timestamped_lines_scanned"] = timestamped
    result["lines_in_window"] = len(lines)
    result["counts"] = count_pattern_lines(lines, log_cfg.get("patterns", {}))
    failure_nodes: Counter[str] = Counter()
    for line in lines:
        if "failure observed" not in line:
            continue
        match = re.search(r"selected_node=(.*?) selected_node_source=", line)
        if match and match.group(1).strip():
            failure_nodes[match.group(1).strip()] += 1
    result["failure_nodes"] = dict(sorted(failure_nodes.items()))
    return result


def add_mihomo_failure_dimensions(snapshot: dict[str, Any], lines: list[str]) -> None:
    endpoints: Counter[str] = Counter()
    groups: Counter[str] = Counter()
    for line in lines:
        if not re.search(r"i/o timeout|context deadline exceeded|connection reset", line, flags=re.IGNORECASE):
            continue
        endpoint = re.search(r"error:\s+([^\s:]+):\d+", line)
        if endpoint:
            endpoints[endpoint.group(1)] += 1
        group = re.search(r"\[TCP\] dial (.*?) \(match ", line)
        if group:
            groups[group.group(1)] += 1
    snapshot["failure_endpoints"] = dict(sorted(endpoints.items()))
    snapshot["failure_groups"] = dict(sorted(groups.items()))


def build_signal_summary(logs: dict[str, Any]) -> dict[str, int | bool]:
    new_api = logs.get("new_api", {}).get("counts", {})
    cpa = logs.get("cpa", {}).get("counts", {})
    mihomo = logs.get("mihomo", {}).get("counts", {})
    new_api_stream_failures = (
        int(new_api.get("protocol_error", 0))
        + int(new_api.get("unexpected_eof", 0))
    )
    protocol_failures = max(
        int(cpa.get("h2_protocol_error", 0)),
        int(cpa.get("terminal_protocol_error", 0)),
    )
    cpa_transport_failures = (
        protocol_failures
        + int(cpa.get("unexpected_eof", 0))
        + int(cpa.get("io_timeout", 0))
        + int(cpa.get("deadline_exceeded", 0))
        + int(cpa.get("connection_reset", 0))
    )
    upstream_transport_failures = max(cpa_transport_failures, new_api_stream_failures)
    mihomo_connect_failures = (
        int(mihomo.get("connect_io_timeout", 0))
        + int(mihomo.get("deadline_exceeded", 0))
        + int(mihomo.get("connection_reset", 0))
        + int(mihomo.get("dns_error", 0))
    )
    return {
        "observation_only": True,
        "new_api_network_5xx": int(new_api.get("network_5xx", 0)),
        "new_api_quota_429": int(new_api.get("quota_429", 0)),
        "new_api_stream_failures": new_api_stream_failures,
        "cpa_transport_failures": cpa_transport_failures,
        "upstream_transport_failures": upstream_transport_failures,
        "mihomo_connect_failures": mihomo_connect_failures,
        "network_error_observed": bool(
            int(new_api.get("network_5xx", 0))
            or upstream_transport_failures
            or mihomo_connect_failures
        ),
    }


def probe(config: dict[str, Any]) -> dict[str, Any]:
    probe_started = time.monotonic()
    state_path = Path(str(config["state_path"]))
    previous = load_json(state_path, {})
    now_utc = dt.datetime.now(dt.timezone.utc)
    try:
        display_timezone = timezone_for_name(config.get("timezone"))
    except Exception:
        display_timezone = dt.timezone.utc
    since, until, log_window = resolve_log_window(now_utc, previous, config)
    nstat = read_nstat()
    nstat_delta, nstat_resets = delta_counters(nstat, previous)
    data: dict[str, Any] = {
        "schema_version": int(config.get("schema_version", 2)),
        "site_id": str(config.get("site_id") or "unknown"),
        "site_role": str(config.get("site_role") or "unknown"),
        "ts": now_utc.astimezone(display_timezone).isoformat(),
        "ts_utc": now_utc.isoformat(),
        "nstat_delta": nstat_delta,
        "nstat_rates": nstat_rates(nstat_delta),
        "nstat_counter_resets": nstat_resets,
        "head": [],
    }
    diagnostic_hosts = {str(host).lower() for host in config.get("diagnostic_only_head_hosts", [])}
    for url in config.get("head_urls", []):
        item = curl_metrics(str(url), config, head=True)
        host = (urllib.parse.urlparse(str(url)).hostname or "").lower()
        item["diagnostic_only"] = host in diagnostic_hosts
        data["head"].append(item)
    speed_cfg = config.get("speed_test", {})
    if speed_cfg.get("enabled", True):
        data["speed_test"] = curl_metrics(str(speed_cfg["url"]), config, head=False)
        data["speed_test"]["configured_bytes"] = int(speed_cfg.get("bytes", 0))
    data["mihomo"] = mihomo_snapshot(config)

    new_api_cfg = dict(config.get("new_api_logs", {}))
    new_api_cfg.setdefault("container", str(config.get("new_api_container") or "new-api"))
    new_api_logs, _ = collect_docker_log_snapshot(config, new_api_cfg, since, until)
    cpa_logs = collect_cpa_log_snapshot(config.get("cpa_logs", {}), since, until)
    mihomo_log_cfg = dict(config.get("mihomo_logs", {}))
    mihomo_log_cfg.setdefault("container", str(config.get("mihomo_container") or "mihomo"))
    mihomo_logs, mihomo_lines = collect_docker_log_snapshot(config, mihomo_log_cfg, since, until)
    add_mihomo_failure_dimensions(mihomo_logs, mihomo_lines)
    logs = {
        "window": log_window,
        "new_api": new_api_logs,
        "cpa": cpa_logs,
        "mihomo": mihomo_logs,
    }
    data["logs"] = logs
    # Keep the v1 field for existing ad-hoc analysis scripts.
    data["new_api_log_counts"] = dict(new_api_logs.get("counts", {}))
    data["signals"] = build_signal_summary(logs)
    data["probe_elapsed_sec"] = round(time.monotonic() - probe_started, 3)
    save_json_atomic(
        state_path,
        {
            "schema_version": int(config.get("schema_version", 2)),
            "site_id": str(config.get("site_id") or "unknown"),
            "ts": now_utc.isoformat(),
            "nstat": nstat,
        },
    )
    return data


def append_jsonl(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, sort_keys=True)
        f.write("\n")


def main(argv: list[str]) -> int:
    config_path = Path(argv[1]) if len(argv) > 1 else Path("/opt/new-api/ops/cmsg_network_probe.json")
    override = load_json(config_path, {}) if config_path.exists() else {}
    if not isinstance(override, dict):
        raise ValueError("network probe config must be a JSON object")
    config = normalize_config(override)
    if not config.get("enabled", True):
        return 0
    data = probe(config)
    append_jsonl(Path(str(config["output_path"])), data)
    print(json.dumps(data, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
