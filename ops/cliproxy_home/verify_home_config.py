#!/usr/bin/env python3
"""Verify non-secret campus transport settings stored in Home."""

from __future__ import annotations

import argparse
from pathlib import Path
import urllib.error
import urllib.request

import yaml


def read_env_value(path: Path, key: str) -> str:
    values: list[str] = []
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        name, value = line.split("=", 1)
        if name.strip() == key:
            values.append(value.strip())
    if len(values) != 1 or not values[0]:
        raise ValueError(f"{key} must appear exactly once and be non-empty")
    return values[0]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--management-env", required=True, type=Path)
    parser.add_argument("--url", default="http://127.0.0.1:8327")
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    key = read_env_value(args.management_env, "HOME_MANAGEMENT_KEY")
    request = urllib.request.Request(args.url.rstrip("/") + "/v0/management/config.yaml")
    request.add_header("X-Management-Key", key)
    try:
        with urllib.request.urlopen(request, timeout=args.timeout) as response:
            payload = yaml.safe_load(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"Home config verification failed with HTTP {exc.code}") from None
    if not isinstance(payload, dict):
        raise ValueError("Home config response must be a YAML mapping")

    proxy_url = payload.get("proxy-url")
    if proxy_url != "http://mihomo:7890":
        raise RuntimeError("Home proxy-url is not the verified campus Mihomo endpoint")
    recovery = payload.get("proxy-route-recovery")
    streaming = payload.get("streaming")
    if not isinstance(recovery, dict) or not recovery:
        raise RuntimeError("Home proxy-route-recovery protection is missing")
    if not isinstance(streaming, dict) or not streaming:
        raise RuntimeError("Home streaming protection is missing")

    print("proxy_url=http://mihomo:7890")
    print("proxy_route_recovery_present=yes")
    print("streaming_present=yes")
    print("streaming_keepalive_seconds=" + str(streaming.get("keepalive-seconds", "missing")))
    print("streaming_bootstrap_retries=" + str(streaming.get("bootstrap-retries", "missing")))
    print("request_retry=" + str(payload.get("request-retry", "missing")))
    print("max_retry_credentials=" + str(payload.get("max-retry-credentials", "missing")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
