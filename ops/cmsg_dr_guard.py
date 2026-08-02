#!/usr/bin/env python3
"""Secret-safe helpers for the CMSG campus disaster-recovery switch.

This tool deliberately does not start/stop containers or promote databases.
It only performs two auditable operations that are easy to get wrong in a
runbook:

* inspect or atomically rewrite only the host/port portions of New API's
  PostgreSQL and Redis URLs without printing credentials; and
* atomically open or close the campus LB eligibility gate, with a fresh,
  validated evidence file required before the gate can be opened.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import stat
import tempfile
import urllib.parse
from pathlib import Path
from typing import Any


SQL_KEY = "SQL_DSN"
REDIS_KEY = "REDIS_CONN_STRING"
SAFE_REASON = re.compile(r"^[a-z0-9][a-z0-9_.-]{0,79}$")
ENV_LINE = re.compile(
    r"^(?P<prefix>\s*(?:export\s+)?)(?P<key>[A-Za-z_][A-Za-z0-9_]*)=(?P<value>.*?)(?P<newline>\r?\n)?$"
)


class GuardError(ValueError):
    """Raised when a safety invariant is not satisfied."""


def utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


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


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, TypeError, ValueError) as exc:
        raise GuardError(f"invalid JSON evidence: {path}") from exc
    if not isinstance(value, dict):
        raise GuardError(f"JSON object required: {path}")
    return value


def unwrap_env_value(value: str) -> tuple[str, str]:
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        return value[1:-1], value[0]
    return value, ""


def wrap_env_value(value: str, quote: str) -> str:
    return f"{quote}{value}{quote}" if quote else value


def split_url(value: str, key: str) -> tuple[urllib.parse.SplitResult, str]:
    raw, quote = unwrap_env_value(value.strip())
    try:
        parsed = urllib.parse.urlsplit(raw)
        _ = parsed.port
    except ValueError as exc:
        raise GuardError(f"{key} is not a valid URL") from exc
    expected_schemes = {"postgres", "postgresql"} if key == SQL_KEY else {"redis", "rediss"}
    if parsed.scheme.lower() not in expected_schemes or not parsed.hostname:
        raise GuardError(f"{key} must use one of: {', '.join(sorted(expected_schemes))}")
    return parsed, quote


def safe_endpoint(value: str, key: str) -> dict[str, Any]:
    parsed, _ = split_url(value, key)
    default_port = 5432 if key == SQL_KEY else 6379
    return {
        "scheme": parsed.scheme.lower(),
        "host": parsed.hostname,
        "port": parsed.port or default_port,
    }


def replace_endpoint(value: str, key: str, host: str, port: int) -> str:
    parsed, quote = split_url(value, key)
    if not host or any(char.isspace() for char in host) or any(char in host for char in "/@"):
        raise GuardError(f"invalid target host for {key}")
    if not 1 <= port <= 65535:
        raise GuardError(f"invalid target port for {key}")
    userinfo = ""
    if "@" in parsed.netloc:
        userinfo = parsed.netloc.rsplit("@", 1)[0] + "@"
    rendered_host = f"[{host}]" if ":" in host and not host.startswith("[") else host
    updated = parsed._replace(netloc=f"{userinfo}{rendered_host}:{port}").geturl()
    return wrap_env_value(updated, quote)


def replace_url_password(value: str, key: str, password: str) -> str:
    parsed, quote = split_url(value, key)
    if key != REDIS_KEY:
        raise GuardError("password replacement is supported only for Redis")
    if not password or any(char in password for char in "\r\n\0"):
        raise GuardError("Redis password source is empty or invalid")
    if "@" not in parsed.netloc:
        raise GuardError("Redis URL does not contain user information")
    raw_userinfo, raw_endpoint = parsed.netloc.rsplit("@", 1)
    raw_username = raw_userinfo.split(":", 1)[0]
    encoded_password = urllib.parse.quote(password, safe="")
    updated = parsed._replace(netloc=f"{raw_username}:{encoded_password}@{raw_endpoint}").geturl()
    return wrap_env_value(updated, quote)


def read_protected_env_value(path: Path, key: str) -> str:
    try:
        file_stat = path.stat()
    except OSError as exc:
        raise GuardError(f"cannot stat protected env file: {path}") from exc
    if not stat.S_ISREG(file_stat.st_mode):
        raise GuardError(f"protected env path is not a regular file: {path}")
    if file_stat.st_mode & 0o077:
        raise GuardError(f"protected env file must not be group/world accessible: {path}")
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise GuardError(f"cannot read protected env file: {path}") from exc
    values: list[str] = []
    for line in lines:
        match = ENV_LINE.match(line)
        if match and match.group("key") == key:
            raw, _quote = unwrap_env_value(match.group("value").strip())
            values.append(raw)
    if len(values) != 1 or not values[0]:
        raise GuardError(f"protected env file must contain exactly one non-empty {key}")
    return values[0]


def read_env_lines(path: Path) -> tuple[list[str], dict[str, tuple[int, re.Match[str]]]]:
    try:
        lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    except OSError as exc:
        raise GuardError(f"cannot read env file: {path}") from exc
    found: dict[str, tuple[int, re.Match[str]]] = {}
    for index, line in enumerate(lines):
        match = ENV_LINE.match(line)
        if not match or match.group("key") not in {SQL_KEY, REDIS_KEY}:
            continue
        key = match.group("key")
        if key in found:
            raise GuardError(f"duplicate {key} in {path}")
        found[key] = (index, match)
    missing = sorted({SQL_KEY, REDIS_KEY} - set(found))
    if missing:
        raise GuardError(f"missing required env keys: {', '.join(missing)}")
    return lines, found


def atomic_write(path: Path, content: str, mode: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
        directory_fd = os.open(path.parent, os.O_DIRECTORY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def inspect_env(path: Path) -> dict[str, Any]:
    _lines, found = read_env_lines(path)
    return {
        "path": str(path),
        "endpoints": {
            key: safe_endpoint(match.group("value"), key)
            for key, (_index, match) in found.items()
        },
        "credentials_printed": False,
    }


def rewrite_env(
    path: Path,
    *,
    expected_sql_host: str,
    expected_redis_host: str,
    sql_host: str,
    sql_port: int,
    redis_host: str,
    redis_port: int,
    backup_dir: Path,
    dry_run: bool,
    redis_password_env_file: Path | None = None,
    redis_password_key: str = "REDIS_STANDBY_PASSWORD",
) -> dict[str, Any]:
    lines, found = read_env_lines(path)
    expected = {SQL_KEY: expected_sql_host, REDIS_KEY: expected_redis_host}
    targets = {SQL_KEY: (sql_host, sql_port), REDIS_KEY: (redis_host, redis_port)}
    redis_password = (
        read_protected_env_value(redis_password_env_file, redis_password_key)
        if redis_password_env_file is not None
        else None
    )
    before: dict[str, dict[str, Any]] = {}
    after: dict[str, dict[str, Any]] = {}
    for key, (_index, match) in found.items():
        endpoint = safe_endpoint(match.group("value"), key)
        before[key] = endpoint
        if endpoint["host"] != expected[key]:
            raise GuardError(
                f"{key} host fence failed: expected {expected[key]!r}, found {endpoint['host']!r}"
            )
        replacement = match.group("value")
        if key == REDIS_KEY and redis_password is not None:
            replacement = replace_url_password(replacement, key, redis_password)
        replacement = replace_endpoint(replacement, key, *targets[key])
        after[key] = safe_endpoint(replacement, key)

    if dry_run:
        return {
            "path": str(path),
            "dry_run": True,
            "before": before,
            "after": after,
            "redis_password_updated": redis_password is not None,
            "credentials_printed": False,
        }

    for key, (index, match) in found.items():
        replacement = match.group("value")
        if key == REDIS_KEY and redis_password is not None:
            replacement = replace_url_password(replacement, key, redis_password)
        replacement = replace_endpoint(replacement, key, *targets[key])
        lines[index] = (
            match.group("prefix")
            + key
            + "="
            + replacement
            + (match.group("newline") or "")
        )

    backup_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(backup_dir, 0o700)
    timestamp = utc_now().strftime("%Y%m%dT%H%M%S%fZ")
    backup = backup_dir / f"{path.name}.pre-dr-{timestamp}"
    shutil.copyfile(path, backup)
    os.chmod(backup, 0o600)
    atomic_write(path, "".join(lines), 0o600)
    return {
        "path": str(path),
        "dry_run": False,
        "backup": str(backup),
        "before": before,
        "after": after,
        "redis_password_updated": redis_password is not None,
        "credentials_printed": False,
    }


def validate_reason(reason: str) -> str:
    if not SAFE_REASON.fullmatch(reason):
        raise GuardError("reason must be 1-80 lowercase ASCII characters: a-z, 0-9, _, ., -")
    return reason


def validate_ready_evidence(
    evidence: dict[str, Any],
    *,
    max_age_sec: int,
    now: dt.datetime,
) -> dict[str, Any]:
    checked_at = parse_time(evidence.get("checked_at"))
    if checked_at is None:
        raise GuardError("evidence.checked_at is missing or invalid")
    age = max(0.0, (now - checked_at).total_seconds())
    if age > max_age_sec:
        raise GuardError(f"evidence is stale: age_sec={age:.3f}")
    if evidence.get("site_id") != "campus":
        raise GuardError("evidence.site_id must be campus")

    postgres = evidence.get("postgres")
    redis = evidence.get("redis")
    new_api = evidence.get("new_api")
    write_probe = evidence.get("write_probe")
    if not isinstance(postgres, dict) or postgres.get("in_recovery") is not False:
        raise GuardError("evidence requires promoted PostgreSQL")
    if not isinstance(redis, dict) or redis.get("role") != "master":
        raise GuardError("evidence requires promoted Redis")
    if not isinstance(new_api, dict) or new_api.get("healthy") is not True:
        raise GuardError("evidence requires healthy campus New API")
    if new_api.get("sql_host") != "postgres-standby":
        raise GuardError("evidence requires local PostgreSQL host postgres-standby")
    if new_api.get("redis_host") != "redis-standby":
        raise GuardError("evidence requires local Redis host redis-standby")
    if not isinstance(write_probe, dict) or write_probe.get("ok") is not True:
        raise GuardError("evidence requires a successful write probe")
    if int(write_probe.get("db_log_delta", 0) or 0) < 1:
        raise GuardError("write probe must produce at least one database log row")
    request_id = str(write_probe.get("request_id") or "").strip()
    if not request_id or len(request_id) > 160:
        raise GuardError("write probe request_id is missing or invalid")
    return {
        "checked_at": checked_at.isoformat(),
        "age_sec": round(age, 3),
        "request_id_present": True,
        "db_log_delta": int(write_probe["db_log_delta"]),
    }


def set_eligibility_blocked(path: Path, reason: str, *, now: dt.datetime) -> dict[str, Any]:
    value = {
        "schema_version": 1,
        "ready": False,
        "mode": "standby_or_transition",
        "reason": validate_reason(reason),
        "updated_at": now.isoformat(),
    }
    atomic_write(path, json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n", 0o600)
    return value


def set_eligibility_ready(
    path: Path,
    reason: str,
    evidence_path: Path,
    *,
    max_age_sec: int,
    now: dt.datetime,
    dry_run: bool,
) -> dict[str, Any]:
    evidence = load_json(evidence_path)
    summary = validate_ready_evidence(evidence, max_age_sec=max_age_sec, now=now)
    evidence_hash = hashlib.sha256(evidence_path.read_bytes()).hexdigest()
    value = {
        "schema_version": 1,
        "ready": True,
        "mode": "campus_local_promoted",
        "reason": validate_reason(reason),
        "updated_at": now.isoformat(),
        "evidence_checked_at": summary["checked_at"],
        "evidence_sha256": evidence_hash,
    }
    if not dry_run:
        atomic_write(path, json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n", 0o600)
    return {**value, "dry_run": dry_run, "evidence_summary": summary}


def parser() -> argparse.ArgumentParser:
    root = argparse.ArgumentParser(description=__doc__)
    commands = root.add_subparsers(dest="command", required=True)

    inspect_parser = commands.add_parser("inspect-env", help="print only DSN endpoint metadata")
    inspect_parser.add_argument("path", type=Path)

    rewrite_parser = commands.add_parser("rewrite-env", help="atomically rewrite DSN host/port only")
    rewrite_parser.add_argument("path", type=Path)
    rewrite_parser.add_argument("--expected-sql-host", required=True)
    rewrite_parser.add_argument("--expected-redis-host", required=True)
    rewrite_parser.add_argument("--sql-host", required=True)
    rewrite_parser.add_argument("--sql-port", type=int, default=5432)
    rewrite_parser.add_argument("--redis-host", required=True)
    rewrite_parser.add_argument("--redis-port", type=int, default=6379)
    rewrite_parser.add_argument("--redis-password-env-file", type=Path)
    rewrite_parser.add_argument("--redis-password-key", default="REDIS_STANDBY_PASSWORD")
    rewrite_parser.add_argument("--backup-dir", type=Path, required=True)
    rewrite_parser.add_argument("--dry-run", action="store_true")

    block_parser = commands.add_parser("eligibility-block", help="atomically close the campus gate")
    block_parser.add_argument("path", type=Path)
    block_parser.add_argument("--reason", required=True)

    ready_parser = commands.add_parser("eligibility-ready", help="open the gate after fresh evidence")
    ready_parser.add_argument("path", type=Path)
    ready_parser.add_argument("--reason", required=True)
    ready_parser.add_argument("--evidence", type=Path, required=True)
    ready_parser.add_argument("--max-age-sec", type=int, default=900)
    ready_parser.add_argument("--dry-run", action="store_true")
    return root


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    try:
        if args.command == "inspect-env":
            result = inspect_env(args.path)
        elif args.command == "rewrite-env":
            result = rewrite_env(
                args.path,
                expected_sql_host=args.expected_sql_host,
                expected_redis_host=args.expected_redis_host,
                sql_host=args.sql_host,
                sql_port=args.sql_port,
                redis_host=args.redis_host,
                redis_port=args.redis_port,
                redis_password_env_file=args.redis_password_env_file,
                redis_password_key=args.redis_password_key,
                backup_dir=args.backup_dir,
                dry_run=args.dry_run,
            )
        elif args.command == "eligibility-block":
            result = set_eligibility_blocked(args.path, args.reason, now=utc_now())
        elif args.command == "eligibility-ready":
            if args.max_age_sec < 1:
                raise GuardError("max-age-sec must be positive")
            result = set_eligibility_ready(
                args.path,
                args.reason,
                args.evidence,
                max_age_sec=args.max_age_sec,
                now=utc_now(),
                dry_run=args.dry_run,
            )
        else:
            raise AssertionError(args.command)
    except GuardError as exc:
        print(json.dumps({"ok": False, "error": str(exc)}, ensure_ascii=False, sort_keys=True))
        return 2
    print(json.dumps({"ok": True, **result}, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
