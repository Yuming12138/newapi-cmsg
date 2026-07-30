#!/usr/bin/env python3
"""Probe CPA health and model listing using a Home-managed client key."""

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


def request_json(
    url: str,
    headers: dict[str, str],
    timeout: float,
    *,
    allow_http_error: bool = False,
) -> tuple[int, dict[str, object]]:
    request = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            status = response.status
            payload = json.load(response)
    except urllib.error.HTTPError as exc:
        if not allow_http_error:
            raise RuntimeError(f"request failed with HTTP {exc.code}: {url}") from None
        status = exc.code
        try:
            payload = json.load(exc)
        except (json.JSONDecodeError, UnicodeDecodeError):
            payload = {}
    if not isinstance(payload, dict):
        raise ValueError(f"request returned a non-object JSON response: {url}")
    return status, payload


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--management-env", required=True, type=Path)
    parser.add_argument("--home-url", default="http://127.0.0.1:8327")
    parser.add_argument("--cpa-url", required=True)
    parser.add_argument("--allow-no-auths", action="store_true")
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    management_key = read_env_value(args.management_env, "HOME_MANAGEMENT_KEY")
    _, key_payload = request_json(
        args.home_url.rstrip("/") + "/v0/management/api-keys",
        {"X-Management-Key": management_key},
        args.timeout,
    )
    api_keys = key_payload.get("api-keys")
    if not isinstance(api_keys, list):
        api_keys = []
    client_keys = [value for value in api_keys if isinstance(value, str) and value]
    if not client_keys:
        raise RuntimeError("Home has no client API key for the CPA model-list probe")

    _, auth_payload = request_json(
        args.home_url.rstrip("/") + "/v0/management/auth-files",
        {"X-Management-Key": management_key},
        args.timeout,
    )
    auth_items = auth_payload.get("files")
    auth_count = len(auth_items) if isinstance(auth_items, list) else 0

    health_status, health_payload = request_json(
        args.cpa_url.rstrip("/") + "/healthz",
        {},
        args.timeout,
    )
    if health_status != 200 or health_payload.get("status") != "ok":
        raise RuntimeError("CPA health response is not OK")

    model_status, model_payload = request_json(
        args.cpa_url.rstrip("/") + "/v1/models",
        {"Authorization": "Bearer " + client_keys[0]},
        args.timeout,
        allow_http_error=True,
    )
    model_items = model_payload.get("data")
    if not isinstance(model_items, list):
        model_items = []

    print(f"home_client_api_key_count={len(client_keys)}")
    print(f"home_auth_count={auth_count}")
    print(f"cpa_health_http={health_status}")
    print(f"cpa_models_http={model_status}")
    print(f"cpa_model_count={len(model_items)}")
    if model_status != 200:
        raw_error = model_payload.get("error")
        error_code = "unknown"
        if isinstance(raw_error, str):
            filtered = "".join(char for char in raw_error if char.isalnum() or char in "_.-")
            if filtered:
                error_code = filtered[:80]
        print(f"cpa_models_error_code={error_code}")
        if args.allow_no_auths and auth_count == 0 and model_status == 502:
            print("cpa_models_state=expected_unconfigured")
            return 0
        raise RuntimeError("CPA model listing did not return HTTP 200")
    print("cpa_models_state=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
