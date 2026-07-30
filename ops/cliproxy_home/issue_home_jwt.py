#!/usr/bin/env python3
"""Issue one Home enrollment JWT and store it in a mode-0600 env file."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import tempfile
import urllib.error
import urllib.request


def read_env_value(path: Path, key: str) -> str:
    matches: list[str] = []
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        name, value = line.split("=", 1)
        if name.strip() == key:
            matches.append(value.strip())
    if len(matches) != 1 or not matches[0]:
        raise ValueError(f"{key} must appear exactly once and be non-empty")
    return matches[0]


def atomic_write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.exists():
        raise FileExistsError(f"refusing to replace existing output: {path}")
    fd, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary_name, path)
        os.chmod(path, 0o600)
    except BaseException:
        try:
            os.unlink(temporary_name)
        except FileNotFoundError:
            pass
        raise


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--management-env", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--url", default="http://127.0.0.1:8327")
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    management_key = read_env_value(args.management_env, "HOME_MANAGEMENT_KEY")
    endpoint = args.url.rstrip("/") + "/v0/management/certificates/clients"
    request = urllib.request.Request(endpoint, data=b"", method="POST")
    request.add_header("X-Management-Key", management_key)
    try:
        with urllib.request.urlopen(request, timeout=args.timeout) as response:
            payload = json.load(response)
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"Home certificate request failed with HTTP {exc.code}") from None

    token = payload.get("home_jwt") if isinstance(payload, dict) else None
    if not isinstance(token, str) or len(token.split(".")) != 3:
        raise ValueError("Home response did not contain a valid enrollment JWT")

    atomic_write(args.output, f"HOME_JWT={token}\n")
    print(f"stored one node-specific HOME_JWT at {args.output} (value not shown)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
