#!/usr/bin/env python3
"""Read-only New API model/channel/price consistency check."""

from __future__ import annotations

import json
import os
import sys
import urllib.request
from typing import Any


def get_json(base: str, token: str, user_id: str, path: str):
    request = urllib.request.Request(
        base.rstrip("/") + path,
        headers={
            "Authorization": f"Bearer {token}",
            "New-Api-User": user_id,
            "Accept": "application/json",
        },
    )
    with urllib.request.urlopen(request, timeout=20) as response:
        return json.load(response)


def unwrap_items(payload: Any) -> list[dict[str, Any]]:
    """Accept both New API's flat and paginated management responses."""
    value = payload.get("data", payload) if isinstance(payload, dict) else payload
    if isinstance(value, dict):
        for key in ("items", "data", "list", "channels", "options"):
            if isinstance(value.get(key), list):
                value = value[key]
                break
    return [item for item in value if isinstance(item, dict)] if isinstance(value, list) else []


def channel_models(channel: dict[str, Any]) -> set[str]:
    raw = channel.get("models", "")
    if isinstance(raw, list):
        return {str(item).strip() for item in raw if str(item).strip()}
    return {item.strip() for item in str(raw).split(",") if item.strip()}


def main() -> int:
    base = os.environ.get("NEWAPI_BASE_URL", "https://api.cmsg666.xyz")
    token = os.environ.get("NEWAPI_ACCESS_TOKEN", "")
    user_id = os.environ.get("NEWAPI_USER_ID", "")
    if not token or not user_id:
        print("NEWAPI_ACCESS_TOKEN and NEWAPI_USER_ID are required", file=sys.stderr)
        return 2

    channels = unwrap_items(get_json(base, token, user_id, "/api/channel"))
    options = unwrap_items(get_json(base, token, user_id, "/api/option/"))
    prices = {}
    for option in options:
        if option.get("key") not in {"ModelRatio", "CompletionRatio", "ModelPrice"}:
            continue
        try:
            prices[option["key"]] = json.loads(option.get("value", "{}"))
        except (TypeError, json.JSONDecodeError):
            print(f"ERROR invalid {option.get('key')} JSON")
            return 1

    wanted = sorted({
        model.strip()
        for channel in channels
        for model in channel_models(channel)
        if model.strip()
    })
    for model in wanted:
        bound = [
            str(channel.get("id"))
            for channel in channels
            if model in channel_models(channel)
            and str(channel.get("status", "")).lower() in {"1", "enabled", "true"}
        ]
        ratio = prices.get("ModelRatio", {}).get(model)
        fixed = prices.get("ModelPrice", {}).get(model)
        if not bound:
            print(f"ERROR {model}: no enabled channel")
        elif ratio is None and fixed is None:
            print(f"ERROR {model}: no price")
        else:
            price_kind = "fixed" if fixed is not None else "ratio"
            print(f"OK {model}: channels={','.join(bound)} price={price_kind}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
