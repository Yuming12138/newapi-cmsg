#!/usr/bin/env python3
"""Verify Home management and print only non-secret topology counts."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import urllib.error
import urllib.request


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


def get_json(url: str, key: str, timeout: float) -> tuple[dict[str, object], dict[str, str]]:
    request = urllib.request.Request(url)
    request.add_header("X-Management-Key", key)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            payload = json.load(response)
            headers = {name.lower(): value for name, value in response.headers.items()}
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"Home verification failed with HTTP {exc.code}") from None
    if not isinstance(payload, dict):
        raise ValueError("Home returned a non-object JSON response")
    return payload, headers


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--management-env", required=True, type=Path)
    parser.add_argument("--url", default="http://127.0.0.1:8327")
    parser.add_argument("--expect-cpa", type=int)
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    key = read_env_value(args.management_env, "HOME_MANAGEMENT_KEY")
    base = args.url.rstrip("/") + "/v0/management"
    capabilities, headers = get_json(base + "/capabilities", key, args.timeout)
    topology, _ = get_json(base + "/topology", key, args.timeout)
    nodes, _ = get_json(base + "/nodes", key, args.timeout)

    summary = topology.get("summary")
    if not isinstance(summary, dict):
        summary = {}
    home_items = topology.get("homes")
    cpa_items = topology.get("cpas")
    node_items = nodes.get("nodes")
    home_count = int(summary.get("home_count", len(home_items) if isinstance(home_items, list) else 0))
    healthy_home_count = int(summary.get("healthy_home_count", 0))
    cpa_count = int(summary.get("cpa_count", len(cpa_items) if isinstance(cpa_items, list) else 0))
    healthy_cpa_count = int(summary.get("healthy_cpa_count", 0))
    node_count = len(node_items) if isinstance(node_items, list) else 0

    capability_data = capabilities.get("capabilities")
    capability_count = len(capability_data) if isinstance(capability_data, dict) else 0
    print("home_version=" + headers.get("x-cpa-home-version", "unknown"))
    print("home_commit=" + headers.get("x-cpa-home-commit", "unknown"))
    print(f"capability_count={capability_count}")
    print(f"home_count={home_count}")
    print(f"healthy_home_count={healthy_home_count}")
    print(f"cpa_count={cpa_count}")
    print(f"healthy_cpa_count={healthy_cpa_count}")
    print(f"stale_cpa_count={max(cpa_count - healthy_cpa_count, 0)}")
    print(f"live_node_count={node_count}")

    if home_count < 1 or healthy_home_count < 1:
        raise RuntimeError("Home topology is not healthy")
    if args.expect_cpa is not None:
        if healthy_cpa_count != args.expect_cpa or node_count != args.expect_cpa:
            raise RuntimeError("CPA topology count did not match the expected healthy count")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
