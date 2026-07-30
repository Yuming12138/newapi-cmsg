#!/usr/bin/env python3
"""Low-impact network quality probe for CMSG CPA routing.

The probe is intentionally conservative: one small download sample, a few HEAD
requests, one delay sample per configured Mihomo node, and short timeouts. It
records JSON lines for later correlation with relay errors without generating
enough traffic to compete with production requests.
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
from pathlib import Path
from typing import Any


DEFAULT_CONFIG: dict[str, Any] = {
    "enabled": True,
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
    "mihomo_group": "OpenAI稳定",
    "mihomo_delay_url": "https://chatgpt.com/backend-api/codex/responses",
    "mihomo_nodes": [
        "🇯🇵 日本1 (移动>电信>联通)",
        "🇸🇬 新加坡1 (移动联通>电信)",
        "🇺🇸 美国1",
    ],
    "mihomo_delay_timeout_ms": 8000,
    "docker_logs": {
        "enabled": True,
        "since": "6m",
        "tail": 600,
        "patterns": {
            "channel_error": "channel error",
            "upstream_error": "upstream error",
            "protocol_error": "PROTOCOL_ERROR",
            "unexpected_eof": "unexpected EOF",
            "usage_limit": "usage limit|429",
            "success_consume": "record consume log",
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
        values["error"] = stderr.strip().splitlines()[-1][:240]
    return values


def read_nstat() -> dict[str, int]:
    keys = {
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


def delta_counters(current: dict[str, int], previous: dict[str, Any]) -> dict[str, int]:
    old = previous.get("nstat") if isinstance(previous, dict) else None
    if not isinstance(old, dict):
        return {}
    delta: dict[str, int] = {}
    for key, value in current.items():
        try:
            delta[key] = value - int(old.get(key, value))
        except Exception:
            pass
    return delta


def docker_inspect_pid(docker: str, container: str) -> str:
    code, stdout, stderr, _ = run_cmd([docker, "inspect", "-f", "{{.State.Pid}}", container], 5)
    if code != 0:
        raise RuntimeError(stderr.strip() or stdout.strip() or f"failed to inspect {container}")
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
    code, stdout, stderr, elapsed = mihomo_get(config, f"/proxies/{encoded_group}")
    result["group_ok"] = code == 0
    result["group_elapsed_sec"] = round(elapsed, 3)
    if code == 0:
        try:
            group = json.loads(stdout)
            result["selected"] = group.get("now")
            result["type"] = group.get("type")
        except Exception as exc:
            result["group_error"] = str(exc)
    else:
        result["group_error"] = (stderr or stdout).strip()[-240:]
    delay_url = urllib.parse.quote(str(config["mihomo_delay_url"]), safe="")
    for node in config.get("mihomo_nodes", []):
        node_name = str(node)
        encoded_node = urllib.parse.quote(node_name, safe="")
        path = f"/proxies/{encoded_node}/delay?timeout={int(config['mihomo_delay_timeout_ms'])}&url={delay_url}"
        code, stdout, stderr, elapsed = mihomo_get(config, path)
        item: dict[str, Any] = {"name": node_name, "ok": code == 0, "elapsed_sec": round(elapsed, 3)}
        if code == 0:
            try:
                item.update(json.loads(stdout))
            except Exception:
                item["raw"] = stdout.strip()[:240]
        else:
            item["error"] = (stderr or stdout).strip().splitlines()[-1][:240] if (stderr or stdout).strip() else "failed"
        result["nodes"].append(item)
    return result


def docker_log_counts(config: dict[str, Any]) -> dict[str, int]:
    log_cfg = config.get("docker_logs", {})
    if not log_cfg.get("enabled", True):
        return {}
    cmd = [
        str(config["docker"]),
        "logs",
        "--since",
        str(log_cfg.get("since", "6m")),
        "--tail",
        str(int(log_cfg.get("tail", 600))),
        str(config["new_api_container"]),
    ]
    code, stdout, stderr, _ = run_cmd(cmd, 10)
    text = stdout + "\n" + stderr
    patterns = log_cfg.get("patterns", {})
    counts: dict[str, int] = {}
    for name, pattern in patterns.items():
        counts[str(name)] = len(re.findall(str(pattern), text, flags=re.IGNORECASE))
    return counts


def probe(config: dict[str, Any]) -> dict[str, Any]:
    state_path = Path(str(config["state_path"]))
    previous = load_json(state_path, {})
    now = dt.datetime.now(dt.timezone.utc).astimezone().isoformat()
    nstat = read_nstat()
    data: dict[str, Any] = {
        "ts": now,
        "nstat_delta": delta_counters(nstat, previous),
        "head": [],
    }
    for url in config.get("head_urls", []):
        data["head"].append(curl_metrics(str(url), config, head=True))
    speed_cfg = config.get("speed_test", {})
    if speed_cfg.get("enabled", True):
        data["speed_test"] = curl_metrics(str(speed_cfg["url"]), config, head=False)
        data["speed_test"]["configured_bytes"] = int(speed_cfg.get("bytes", 0))
    data["mihomo"] = mihomo_snapshot(config)
    data["new_api_log_counts"] = docker_log_counts(config)
    save_json_atomic(state_path, {"ts": now, "nstat": nstat})
    return data


def append_jsonl(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, sort_keys=True)
        f.write("\n")


def main(argv: list[str]) -> int:
    config_path = Path(argv[1]) if len(argv) > 1 else Path("/opt/new-api/ops/cmsg_network_probe.json")
    config = DEFAULT_CONFIG
    if config_path.exists():
        config = deep_merge(DEFAULT_CONFIG, load_json(config_path, {}))
    if not config.get("enabled", True):
        return 0
    data = probe(config)
    append_jsonl(Path(str(config["output_path"])), data)
    print(json.dumps(data, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
