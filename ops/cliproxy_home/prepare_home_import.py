#!/usr/bin/env python3
"""Prepare an isolated CLIProxyAPIHome import config without printing secrets."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import tempfile

import yaml


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


def atomic_dump_yaml(path: Path, payload: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            yaml.safe_dump(payload, handle, sort_keys=False, allow_unicode=True)
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
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--management-env", required=True, type=Path)
    args = parser.parse_args()

    if args.input.resolve() == args.output.resolve():
        raise ValueError("input and output must be different files")

    payload = yaml.safe_load(args.input.read_text(encoding="utf-8"))
    if not isinstance(payload, dict):
        raise ValueError("input config must contain a YAML mapping")

    proxy_url = payload.get("proxy-url")
    if proxy_url != "http://mihomo:7890":
        raise ValueError("campus Home import requires the verified Mihomo proxy endpoint")

    management = payload.get("remote-management")
    if not isinstance(management, dict):
        management = {}
    management["allow-remote"] = True
    management["secret-key"] = read_env_value(args.management_env, "HOME_MANAGEMENT_KEY")
    payload["remote-management"] = management

    atomic_dump_yaml(args.output, payload)
    print(f"prepared Home import config at {args.output} (secret not shown)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
