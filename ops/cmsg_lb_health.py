#!/usr/bin/env python3
"""Turn CMSG probe v2 history into a hysteretic LB health signal.

The program is deliberately observation-only: it evaluates local probe data
and can expose HTTP 200/503, but it never calls a Cloudflare API or changes
routing. Raw log lines and credentials are never included in its output.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import os
import re
import statistics
import subprocess
import sys
import tempfile
import threading
import urllib.parse
from collections import deque
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


DEFAULT_CONFIG: dict[str, Any] = {
    "schema_version": 1,
    "site_id": "unknown",
    "probe_path": "/opt/new-api/logs/cmsg_network_probe.jsonl",
    "state_path": "/var/lib/cmsg-lb-health/state.json",
    "lookback_sec": 1800,
    "freshness_sec": 720,
    "min_samples": 4,
    "max_records": 256,
    "required_log_sources": ["new_api", "cpa", "mihomo"],
    "required_head_hosts": [
        "mirrors.tuna.tsinghua.edu.cn",
        "www.cloudflare.com",
    ],
    "thresholds": {
        "min_log_source_ok_ratio": 0.8,
        "min_required_head_success_ratio": 0.8,
        "warn_required_head_success_ratio": 0.95,
        "min_speed_success_ratio": 0.75,
        "warn_speed_success_ratio": 0.95,
        "max_required_head_p95_ttfb_sec": 3.0,
        "warn_required_head_p95_ttfb_sec": 1.5,
        "max_speed_p95_ttfb_sec": 5.0,
        "warn_speed_p95_ttfb_sec": 2.5,
        "max_mihomo_best_p95_delay_ms": 1500.0,
        "warn_mihomo_best_p95_delay_ms": 800.0,
        "min_mihomo_success_ratio": 0.5,
        "max_tcp_retrans_percent": 5.0,
        "warn_tcp_retrans_percent": 2.0,
        "max_new_api_network_5xx": 5,
        "max_new_api_5xx_per_1000": 50.0,
        "max_cpa_transport_failures": 3,
        "max_mihomo_connect_failures": 5,
        "min_score": 70,
    },
    "hysteresis": {
        "fail_after": 2,
        "recover_after": 3,
        "recovery_cooldown_sec": 900,
    },
    "eligibility": {
        "required": False,
        "path": "",
        "max_age_sec": 0,
    },
    "runtime": {
        "docker": "/usr/bin/docker",
        "required_containers": [],
        "inspect_timeout_sec": 5,
    },
    "server": {
        "listen": "127.0.0.1",
        "port": 9120,
        "health_path": "/__cmsg_lb_health",
        "status_path": "/__cmsg_lb_status",
    },
}


def deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(merged.get(key), dict):
            merged[key] = deep_merge(merged[key], value)
        else:
            merged[key] = value
    return merged


def load_json(path: Path, default: Any) -> Any:
    if not path.is_file():
        return default
    try:
        with path.open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, json.JSONDecodeError, TypeError, ValueError):
        return default


def save_json_atomic(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        "w",
        encoding="utf-8",
        dir=str(path.parent),
        delete=False,
    ) as handle:
        json.dump(value, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")
        temporary = handle.name
    os.replace(temporary, path)


def parse_time(value: Any) -> dt.datetime | None:
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


def utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def percentile(values: list[float], fraction: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, math.ceil(len(ordered) * fraction) - 1))
    return round(ordered[index], 6)


def safe_reason(value: Any, fallback: str) -> str:
    cleaned = re.sub(r"[^a-z0-9_.-]+", "_", str(value or "").strip().lower()).strip("_")
    return (cleaned or fallback)[:80]


def read_probe_records(path: Path, max_records: int) -> tuple[list[dict[str, Any]], int]:
    limit = max(1, int(max_records))
    records: deque[dict[str, Any]] = deque(maxlen=limit)
    invalid = 0
    try:
        with path.open("rb") as handle:
            handle.seek(0, os.SEEK_END)
            position = handle.tell()
            raw = b""
            while position > 0 and raw.count(b"\n") <= limit:
                size = min(65536, position)
                position -= size
                handle.seek(position)
                raw = handle.read(size) + raw
        if position > 0:
            first_newline = raw.find(b"\n")
            raw = raw[first_newline + 1 :] if first_newline >= 0 else b""
        lines = raw.splitlines()[-limit:]
        for line in lines:
            try:
                value = json.loads(line.decode("utf-8", errors="replace"))
            except (json.JSONDecodeError, TypeError, ValueError):
                invalid += 1
                continue
            if not isinstance(value, dict) or parse_time(value.get("ts_utc")) is None:
                invalid += 1
                continue
            records.append(value)
    except OSError:
        return [], invalid
    return sorted(records, key=lambda item: parse_time(item.get("ts_utc")) or dt.datetime.min.replace(tzinfo=dt.timezone.utc)), invalid


def number(value: Any, default: float = 0.0) -> float:
    return float(value) if isinstance(value, (int, float)) and not isinstance(value, bool) else default


def integer(value: Any, default: int = 0) -> int:
    return int(value) if isinstance(value, (int, float)) and not isinstance(value, bool) else default


def http_ok(item: dict[str, Any]) -> bool:
    code = integer(item.get("http_code"))
    return bool(item.get("ok")) and 200 <= code < 400


def collect_metrics(records: list[dict[str, Any]], required_hosts: list[str]) -> dict[str, Any]:
    sample_count = len(records)
    required = {str(host).lower() for host in required_hosts if str(host).strip()}
    head_success = 0
    head_expected = sample_count * len(required)
    head_ttfb: list[float] = []
    optional_head_attempts = 0
    optional_head_success = 0
    speed_success = 0
    speed_ttfb: list[float] = []
    source_ok = {"new_api": 0, "cpa": 0, "mihomo": 0}
    new_api_5xx = 0
    quota_429 = 0
    success_consume = 0
    cpa_failures = 0
    mihomo_failures = 0
    network_error_windows = 0
    tcp_out = 0
    tcp_retrans = 0
    tcp_windows = 0
    mihomo_best_delays: list[float] = []

    for record in records:
        seen_required: set[str] = set()
        for item in record.get("head", []):
            if not isinstance(item, dict) or item.get("diagnostic_only"):
                continue
            host = (urllib.parse.urlparse(str(item.get("url", ""))).hostname or "").lower()
            if host in required and host not in seen_required:
                seen_required.add(host)
                if http_ok(item):
                    head_success += 1
                    if isinstance(item.get("ttfb"), (int, float)):
                        head_ttfb.append(float(item["ttfb"]))
            elif host not in required:
                optional_head_attempts += 1
                optional_head_success += int(http_ok(item))

        speed = record.get("speed_test")
        if isinstance(speed, dict) and http_ok(speed):
            speed_success += 1
            if isinstance(speed.get("ttfb"), (int, float)):
                speed_ttfb.append(float(speed["ttfb"]))

        logs = record.get("logs", {})
        if isinstance(logs, dict):
            for source in source_ok:
                if isinstance(logs.get(source), dict) and logs[source].get("ok") is True:
                    source_ok[source] += 1

        signals = record.get("signals", {})
        if isinstance(signals, dict):
            new_api_5xx += integer(signals.get("new_api_network_5xx"))
            quota_429 += integer(signals.get("new_api_quota_429"))
            cpa_failures += integer(signals.get("cpa_transport_failures"))
            mihomo_failures += integer(signals.get("mihomo_connect_failures"))
            network_error_windows += int(bool(signals.get("network_error_observed")))
        counts = record.get("new_api_log_counts", {})
        if isinstance(counts, dict):
            success_consume += integer(counts.get("success_consume"))

        delta = record.get("nstat_delta", {})
        if isinstance(delta, dict):
            sent = integer(delta.get("TcpOutSegs"))
            retrans = integer(delta.get("TcpRetransSegs"))
            if sent > 0:
                tcp_windows += 1
                tcp_out += sent
                tcp_retrans += max(0, retrans)

        mihomo = record.get("mihomo", {})
        nodes = mihomo.get("nodes", []) if isinstance(mihomo, dict) else []
        delays = [
            float(node["delay"])
            for node in nodes
            if isinstance(node, dict)
            and node.get("ok") is True
            and isinstance(node.get("delay"), (int, float))
        ]
        if delays:
            mihomo_best_delays.append(min(delays))

    transactions = success_consume + new_api_5xx
    return {
        "sample_count": sample_count,
        "required_head": {
            "expected": head_expected,
            "success": head_success,
            "success_ratio": round(head_success / head_expected, 6) if head_expected else None,
            "p95_ttfb_sec": percentile(head_ttfb, 0.95),
        },
        "optional_head": {
            "attempts": optional_head_attempts,
            "success_ratio": round(optional_head_success / optional_head_attempts, 6) if optional_head_attempts else None,
        },
        "speed_test": {
            "expected": sample_count,
            "success": speed_success,
            "success_ratio": round(speed_success / sample_count, 6) if sample_count else None,
            "p95_ttfb_sec": percentile(speed_ttfb, 0.95),
        },
        "log_source_ok_ratio": {
            source: round(count / sample_count, 6) if sample_count else 0.0
            for source, count in source_ok.items()
        },
        "errors": {
            "new_api_network_5xx": new_api_5xx,
            "new_api_quota_429": quota_429,
            "new_api_success_consume": success_consume,
            "new_api_5xx_per_1000": round(new_api_5xx * 1000.0 / transactions, 3) if transactions else 0.0,
            "cpa_transport_failures": cpa_failures,
            "mihomo_connect_failures": mihomo_failures,
            "network_error_windows": network_error_windows,
        },
        "tcp": {
            "windows": tcp_windows,
            "out_segments": tcp_out,
            "retrans_segments": tcp_retrans,
            "retrans_percent": round(tcp_retrans * 100.0 / tcp_out, 6) if tcp_out else None,
        },
        "mihomo": {
            "success_ratio": round(len(mihomo_best_delays) / sample_count, 6) if sample_count else 0.0,
            "best_p95_delay_ms": percentile(mihomo_best_delays, 0.95),
        },
    }


def evaluate_metrics(metrics: dict[str, Any], config: dict[str, Any]) -> tuple[bool, int, list[str], list[str]]:
    thresholds = config["thresholds"]
    critical: list[str] = []
    warnings: list[str] = []
    score = 100.0
    samples = integer(metrics.get("sample_count"))
    if samples < int(config["min_samples"]):
        critical.append("insufficient_probe_samples")
        score -= 50

    for source in config.get("required_log_sources", []):
        ratio = number(metrics.get("log_source_ok_ratio", {}).get(source))
        if ratio < number(thresholds["min_log_source_ok_ratio"]):
            critical.append(f"log_source_{safe_reason(source, 'unknown')}_unavailable")
            score -= 25

    head = metrics["required_head"]
    head_ratio = number(head.get("success_ratio"))
    if head_ratio < number(thresholds["min_required_head_success_ratio"]):
        critical.append("required_egress_unreachable")
        score -= 35
    elif head_ratio < number(thresholds["warn_required_head_success_ratio"]):
        warnings.append("required_egress_degraded")
        score -= 12
    head_p95 = head.get("p95_ttfb_sec")
    if not isinstance(head_p95, (int, float)):
        critical.append("required_egress_latency_missing")
        score -= 20
    elif float(head_p95) > number(thresholds["max_required_head_p95_ttfb_sec"]):
        critical.append("required_egress_ttfb_high")
        score -= 25
    elif float(head_p95) > number(thresholds["warn_required_head_p95_ttfb_sec"]):
        warnings.append("required_egress_ttfb_degraded")
        score -= 8

    speed = metrics["speed_test"]
    speed_ratio = number(speed.get("success_ratio"))
    if speed_ratio < number(thresholds["min_speed_success_ratio"]):
        critical.append("speed_probe_unreachable")
        score -= 25
    elif speed_ratio < number(thresholds["warn_speed_success_ratio"]):
        warnings.append("speed_probe_degraded")
        score -= 8
    speed_p95 = speed.get("p95_ttfb_sec")
    if isinstance(speed_p95, (int, float)):
        if float(speed_p95) > number(thresholds["max_speed_p95_ttfb_sec"]):
            critical.append("speed_probe_ttfb_high")
            score -= 20
        elif float(speed_p95) > number(thresholds["warn_speed_p95_ttfb_sec"]):
            warnings.append("speed_probe_ttfb_degraded")
            score -= 6

    errors = metrics["errors"]
    new_api_5xx = integer(errors.get("new_api_network_5xx"))
    new_api_rate = number(errors.get("new_api_5xx_per_1000"))
    if (
        new_api_5xx >= int(thresholds["max_new_api_network_5xx"])
        and new_api_rate >= number(thresholds["max_new_api_5xx_per_1000"])
    ):
        critical.append("new_api_network_5xx_high")
        score -= 30
    if integer(errors.get("cpa_transport_failures")) >= int(thresholds["max_cpa_transport_failures"]):
        critical.append("cpa_transport_failures_high")
        score -= 30
    if integer(errors.get("mihomo_connect_failures")) >= int(thresholds["max_mihomo_connect_failures"]):
        critical.append("mihomo_connect_failures_high")
        score -= 25

    tcp = metrics["tcp"]
    retrans = tcp.get("retrans_percent")
    if not isinstance(retrans, (int, float)):
        critical.append("tcp_retransmission_metric_missing")
        score -= 25
    elif float(retrans) >= number(thresholds["max_tcp_retrans_percent"]):
        critical.append("tcp_retransmission_high")
        score -= 35
    elif float(retrans) >= number(thresholds["warn_tcp_retrans_percent"]):
        warnings.append("tcp_retransmission_degraded")
        score -= 10

    mihomo = metrics["mihomo"]
    if number(mihomo.get("success_ratio")) < number(thresholds["min_mihomo_success_ratio"]):
        critical.append("mihomo_delay_probe_unreachable")
        score -= 25
    delay = mihomo.get("best_p95_delay_ms")
    if isinstance(delay, (int, float)):
        if float(delay) > number(thresholds["max_mihomo_best_p95_delay_ms"]):
            critical.append("mihomo_delay_high")
            score -= 20
        elif float(delay) > number(thresholds["warn_mihomo_best_p95_delay_ms"]):
            warnings.append("mihomo_delay_degraded")
            score -= 6

    score_value = max(0, min(100, int(round(score))))
    raw_healthy = not critical and score_value >= int(thresholds["min_score"])
    return raw_healthy, score_value, sorted(set(critical)), sorted(set(warnings))


def load_eligibility(config: dict[str, Any], now: dt.datetime) -> dict[str, Any]:
    eligibility = config.get("eligibility", {})
    if not eligibility.get("required", False):
        return {"required": False, "ready": True, "reason": "not_required"}
    path = Path(str(eligibility.get("path") or ""))
    value = load_json(path, None)
    if not isinstance(value, dict):
        return {"required": True, "ready": False, "reason": "eligibility_file_missing_or_invalid"}
    ready = value.get("ready") is True
    reason = safe_reason(value.get("reason"), "ready" if ready else "not_ready")
    max_age = max(0, int(eligibility.get("max_age_sec", 0) or 0))
    updated = parse_time(value.get("updated_at"))
    age = max(0.0, (now - updated).total_seconds()) if updated is not None else None
    if ready and max_age > 0 and (age is None or age > max_age):
        return {"required": True, "ready": False, "reason": "eligibility_stale", "age_sec": round(age, 3) if age is not None else None}
    return {
        "required": True,
        "ready": ready,
        "reason": reason,
        "age_sec": round(age, 3) if age is not None else None,
    }


def collect_runtime_health(config: dict[str, Any]) -> dict[str, Any]:
    runtime = config.get("runtime", {})
    docker = str(runtime.get("docker") or "/usr/bin/docker")
    timeout = max(1.0, float(runtime.get("inspect_timeout_sec", 5)))
    containers: dict[str, Any] = {}
    for configured_name in runtime.get("required_containers", []):
        name = str(configured_name).strip()
        if not name:
            continue
        try:
            process = subprocess.run(
                [docker, "inspect", "-f", "{{json .State}}", name],
                text=True,
                capture_output=True,
                timeout=timeout,
            )
        except (OSError, subprocess.TimeoutExpired):
            containers[name] = {"ready": False, "running": False, "health": "inspect_failed"}
            continue
        if process.returncode != 0:
            containers[name] = {"ready": False, "running": False, "health": "inspect_failed"}
            continue
        try:
            state = json.loads(process.stdout)
        except (json.JSONDecodeError, TypeError, ValueError):
            containers[name] = {"ready": False, "running": False, "health": "inspect_invalid"}
            continue
        running = state.get("Running") is True if isinstance(state, dict) else False
        health_value = state.get("Health") if isinstance(state, dict) else None
        health = health_value.get("Status") if isinstance(health_value, dict) else None
        ready = running and health in {None, "healthy"}
        containers[name] = {
            "ready": ready,
            "running": running,
            "health": str(health or "none"),
        }
    return {
        "ready": all(item["ready"] for item in containers.values()),
        "containers": containers,
    }


def apply_hysteresis(
    raw_healthy: bool,
    probe_id: str,
    state_path: Path,
    config: dict[str, Any],
    now: dt.datetime,
) -> dict[str, Any]:
    settings = config["hysteresis"]
    fail_after = max(1, int(settings["fail_after"]))
    recover_after = max(1, int(settings["recover_after"]))
    recovery_cooldown = max(0, int(settings["recovery_cooldown_sec"]))
    previous = load_json(state_path, {})
    valid = isinstance(previous, dict) and isinstance(previous.get("effective_healthy"), bool)
    if not valid:
        effective = raw_healthy
        changed_at = now
        consecutive_bad = 0
        consecutive_good = 0
    else:
        effective = bool(previous["effective_healthy"])
        changed_at = parse_time(previous.get("changed_at")) or now
        consecutive_bad = integer(previous.get("consecutive_bad"))
        consecutive_good = integer(previous.get("consecutive_good"))

    new_probe = previous.get("last_probe_id") != probe_id if valid else True
    if new_probe and valid:
        if effective:
            consecutive_good = 0
            consecutive_bad = consecutive_bad + 1 if not raw_healthy else 0
            if consecutive_bad >= fail_after:
                effective = False
                changed_at = now
                consecutive_bad = 0
        else:
            consecutive_bad = 0
            consecutive_good = consecutive_good + 1 if raw_healthy else 0
            state_age = max(0.0, (now - changed_at).total_seconds())
            if consecutive_good >= recover_after and state_age >= recovery_cooldown:
                effective = True
                changed_at = now
                consecutive_good = 0

    state = {
        "schema_version": 1,
        "effective_healthy": effective,
        "last_probe_id": probe_id,
        "last_raw_healthy": raw_healthy,
        "consecutive_bad": consecutive_bad,
        "consecutive_good": consecutive_good,
        "changed_at": changed_at.isoformat(),
        "updated_at": now.isoformat(),
    }
    save_json_atomic(state_path, state)
    return state


def evaluate(config: dict[str, Any], now: dt.datetime | None = None) -> dict[str, Any]:
    current = (now or utc_now()).astimezone(dt.timezone.utc)
    probe_path = Path(str(config["probe_path"]))
    records, invalid = read_probe_records(probe_path, int(config["max_records"]))
    latest_record = records[-1] if records else None
    latest_time = parse_time(latest_record.get("ts_utc")) if latest_record else None
    age = max(0.0, (current - latest_time).total_seconds()) if latest_time is not None else None
    fresh = age is not None and age <= float(config["freshness_sec"])
    cutoff = current - dt.timedelta(seconds=int(config["lookback_sec"]))
    window = [record for record in records if (parse_time(record.get("ts_utc")) or dt.datetime.min.replace(tzinfo=dt.timezone.utc)) >= cutoff]
    metrics = collect_metrics(window, [str(value) for value in config.get("required_head_hosts", [])])
    raw_healthy, score, critical, warnings = evaluate_metrics(metrics, config)
    if not fresh:
        critical.append("probe_missing" if latest_time is None else "probe_stale")
        raw_healthy = False
        score = min(score, 20)
    critical = sorted(set(critical))
    probe_id = latest_time.isoformat() if latest_time is not None else "missing"
    hysteresis = apply_hysteresis(
        raw_healthy,
        probe_id,
        Path(str(config["state_path"])),
        config,
        current,
    )
    # Freshness is a hard safety gate and must not wait for a new probe record.
    effective_network_healthy = bool(hysteresis["effective_healthy"]) and fresh
    eligibility = load_eligibility(config, current)
    runtime = collect_runtime_health(config)
    lb_healthy = effective_network_healthy and bool(eligibility["ready"]) and bool(runtime["ready"])
    reasons = list(critical)
    if not eligibility["ready"]:
        reasons.append(f"data_plane_{safe_reason(eligibility.get('reason'), 'not_ready')}")
    for name, item in runtime["containers"].items():
        if not item["ready"]:
            reasons.append(f"container_{safe_reason(name, 'unknown')}_unhealthy")
    return {
        "schema_version": 1,
        "site_id": str(config.get("site_id") or "unknown"),
        "checked_at": current.isoformat(),
        "latest_probe_at": latest_time.isoformat() if latest_time is not None else None,
        "probe_age_sec": round(age, 3) if age is not None else None,
        "probe_fresh": fresh,
        "window_sec": int(config["lookback_sec"]),
        "invalid_probe_records": invalid,
        "score": score,
        "raw_healthy": raw_healthy,
        "effective_network_healthy": effective_network_healthy,
        "lb_healthy": lb_healthy,
        "reasons": sorted(set(reasons)),
        "warnings": warnings,
        "metrics": metrics,
        "eligibility": eligibility,
        "runtime": runtime,
        "hysteresis": {
            "effective_healthy": bool(hysteresis["effective_healthy"]),
            "consecutive_bad": integer(hysteresis["consecutive_bad"]),
            "consecutive_good": integer(hysteresis["consecutive_good"]),
            "changed_at": hysteresis["changed_at"],
        },
        "observation_only": True,
    }


def public_payload(status: dict[str, Any]) -> dict[str, Any]:
    return {
        "schema_version": status["schema_version"],
        "site_id": status["site_id"],
        "checked_at": status["checked_at"],
        "latest_probe_at": status["latest_probe_at"],
        "probe_age_sec": status["probe_age_sec"],
        "score": status["score"],
        "healthy": status["lb_healthy"],
        "network_healthy": status["effective_network_healthy"],
        "eligibility_ready": status["eligibility"]["ready"],
        "runtime_ready": status["runtime"]["ready"],
        "reasons": status["reasons"],
        "warnings": status["warnings"],
        "observation_only": True,
    }


def load_config(path: Path) -> dict[str, Any]:
    override = load_json(path, None)
    if not isinstance(override, dict):
        raise ValueError(f"invalid LB health config: {path}")
    return deep_merge(DEFAULT_CONFIG, override)


def serve(config: dict[str, Any]) -> None:
    server_config = config["server"]
    health_path = str(server_config["health_path"])
    status_path = str(server_config["status_path"])
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        server_version = "CMSG-LB-Health/1"

        def log_message(self, _format: str, *_args: Any) -> None:
            return

        def do_HEAD(self) -> None:  # noqa: N802
            self._respond(head_only=True)

        def do_GET(self) -> None:  # noqa: N802
            self._respond(head_only=False)

        def _respond(self, *, head_only: bool) -> None:
            path = urllib.parse.urlsplit(self.path).path
            if path not in {health_path, status_path}:
                self.send_error(404)
                return
            with lock:
                status = evaluate(config)
            body_value = public_payload(status) if path == health_path else status
            body = json.dumps(body_value, ensure_ascii=False, sort_keys=True).encode("utf-8") + b"\n"
            code = 200 if path == status_path or status["lb_healthy"] else 503
            self.send_response(code)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            if not head_only:
                self.wfile.write(body)

    address = (str(server_config["listen"]), int(server_config["port"]))
    httpd = ThreadingHTTPServer(address, Handler)
    httpd.daemon_threads = True
    httpd.serve_forever()


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    evaluate_parser = subparsers.add_parser("evaluate", help="evaluate once and print sanitized JSON")
    evaluate_parser.add_argument("config", type=Path)
    serve_parser = subparsers.add_parser("serve", help="serve local HTTP health endpoints")
    serve_parser.add_argument("config", type=Path)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    config = load_config(args.config)
    if args.command == "evaluate":
        status = evaluate(config)
        print(json.dumps(status, ensure_ascii=False, sort_keys=True))
        return 0 if status["lb_healthy"] else 3
    if args.command == "serve":
        serve(config)
        return 0
    raise AssertionError(args.command)


if __name__ == "__main__":
    raise SystemExit(main())
