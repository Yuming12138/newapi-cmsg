#!/usr/bin/env python3
"""Safely consume ASXS subscription daily resets from a server-side timer.

The ASXS native auto-reset switch can be disabled by the provider.  This
standalone guard evaluates the provider's current billing state and performs
at most one eligible reset per invocation.  Subscription identifiers are kept
in memory only; logs and persisted state contain truncated SHA-256 hashes.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import socket
import sys
import tempfile
import time
import urllib.error
import urllib.request
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterator, TextIO


DEFAULT_CONFIG: dict[str, Any] = {
    "enabled": False,
    "site_id": "aliyun",
    "allowed_hostnames": [],
    "base_url": "https://api.asxs.top",
    "state_endpoint": "/api/me/billing/state",
    "reset_endpoint": "/api/me/billing/daily-reset",
    "env_path": "/opt/new-api/ops/asxs_auto_daily_reset.env",
    "state_path": "/opt/new-api/ops/asxs_auto_daily_reset_state.json",
    "lock_path": "/run/lock/new-api-asxs-auto-daily-reset.lock",
    "timeout_sec": 20,
    "minimum_usage_percent": 99.0,
    "minimum_remaining_days_after_reset": 2,
    "target_cooldown_sec": 300,
    "max_resets_per_run": 1,
    "verify_after_reset": True,
    "state_retention_days": 30,
    "user_agent": "new-api-asxs-auto-daily-reset/1.0",
}

TOKEN_ENV_KEY = "ASXS_ACCESS_TOKEN"
PROXY_ENV_KEY = "ASXS_AUTO_RESET_PROXY"
STATE_VERSION = 1


class GuardError(RuntimeError):
    """Expected configuration, state, or provider error."""

    def __init__(self, code: str, *, status: int | None = None) -> None:
        super().__init__(code)
        self.code = code
        self.status = status


@dataclass(frozen=True)
class Candidate:
    subscription_id: str
    target_hash: str
    usage_percent: float
    threshold_percent: float
    remaining_days: float
    used_today: int | None
    daily_limit: int | None


def emit(event: str, **fields: Any) -> None:
    payload = {
        "event": event,
        "timestamp": datetime.now(timezone.utc).isoformat(),
        **fields,
    }
    print(json.dumps(payload, ensure_ascii=False, sort_keys=True), flush=True)


def deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    result = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(result.get(key), dict):
            result[key] = deep_merge(result[key], value)
        else:
            result[key] = value
    return result


def load_json(path: Path, default: Any) -> Any:
    if not path.exists():
        return default
    try:
        with path.open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise GuardError("invalid_json_file") from exc


def load_config(path: Path) -> dict[str, Any]:
    raw = load_json(path, {})
    if not isinstance(raw, dict):
        raise GuardError("config_not_object")
    return deep_merge(DEFAULT_CONFIG, raw)


def atomic_write_text(path: Path, content: str, mode: int = 0o600) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
        os.chmod(path, mode)
        directory_fd = os.open(path.parent, os.O_DIRECTORY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def save_json_atomic(path: Path, payload: Any) -> None:
    atomic_write_text(
        path,
        json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        0o600,
    )


def load_env_values(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    if not path.exists():
        return values
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise GuardError("env_unreadable") from exc
    for raw in lines:
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
            value = value[1:-1]
        values[key.strip()] = value
    return values


def replace_env_value_atomic(path: Path, key: str, value: str) -> None:
    if not value or any(character in value for character in ("\r", "\n", "\0")):
        raise GuardError("invalid_token_value")
    existing = path.read_text(encoding="utf-8").splitlines() if path.exists() else []
    output: list[str] = []
    replaced = False
    for raw in existing:
        stripped = raw.strip()
        if not stripped or stripped.startswith("#") or "=" not in stripped:
            output.append(raw)
            continue
        current_key = stripped.split("=", 1)[0].strip()
        if current_key == key:
            output.append(f"{key}={value}")
            replaced = True
        else:
            output.append(raw)
    if not replaced:
        output.append(f"{key}={value}")
    atomic_write_text(path, "\n".join(output) + "\n", 0o600)


def install_token_from_stream(env_path: Path, stream: TextIO) -> None:
    if stream.isatty():
        raise GuardError("token_stdin_required")
    token = stream.read().strip()
    if len(token) < 20:
        raise GuardError("token_too_short")
    replace_env_value_atomic(env_path, TOKEN_ENV_KEY, token)


def as_bool(value: Any, default: bool = False) -> bool:
    if value is None:
        return default
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0
    return str(value).strip().lower() in {"1", "true", "yes", "on", "enabled"}


def as_float(value: Any) -> float | None:
    if value is None or isinstance(value, bool):
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def as_int(value: Any) -> int | None:
    number = as_float(value)
    return int(number) if number is not None else None


def value_for(payload: dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in payload:
            return payload[key]
    return None


def parse_iso_timestamp(value: Any) -> float | None:
    if not value:
        return None
    try:
        parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.timestamp()


def hash_target(subscription_id: str) -> str:
    return hashlib.sha256(subscription_id.encode("utf-8")).hexdigest()[:20]


def remaining_days_for_target(target: dict[str, Any], now: float) -> float | None:
    direct = as_float(value_for(target, "remainingDays", "remaining_days"))
    if direct is not None:
        return direct
    expires_at = parse_iso_timestamp(value_for(target, "expiresAt", "expires_at"))
    if expires_at is None:
        return None
    return max(0.0, (expires_at - now) / 86_400.0)


def select_candidate(
    config: dict[str, Any],
    billing_state: dict[str, Any],
    persisted_state: dict[str, Any],
    now: float,
) -> tuple[Candidate | None, dict[str, Any]]:
    daily_reset = value_for(billing_state, "dailyReset", "daily_reset")
    if not isinstance(daily_reset, dict):
        return None, {"reason": "daily_reset_missing", "target_count": 0, "eligible_count": 0}

    targets = value_for(daily_reset, "targets")
    if not isinstance(targets, list):
        targets = []
    provider_threshold = as_float(
        value_for(daily_reset, "usageThresholdPercent", "usage_threshold_percent")
    )
    local_threshold = float(config.get("minimum_usage_percent") or 0.0)
    threshold = max(local_threshold, provider_threshold or 0.0)
    provider_min_days = as_float(
        value_for(daily_reset, "minRemainingDays", "min_remaining_days")
    )
    minimum_after_reset = float(config.get("minimum_remaining_days_after_reset") or 0.0)
    minimum_current_days = max(provider_min_days or 0.0, minimum_after_reset + 1.0)
    cooldown = max(0, int(config.get("target_cooldown_sec") or 0))
    target_states = persisted_state.get("targets")
    if not isinstance(target_states, dict):
        target_states = {}

    counters = {
        "target_count": len(targets),
        "provider_eligible_count": 0,
        "threshold_rejected_count": 0,
        "limit_reached_count": 0,
        "duration_rejected_count": 0,
        "cooldown_count": 0,
        "invalid_target_count": 0,
    }
    candidates: list[Candidate] = []
    supported = as_bool(value_for(daily_reset, "supported"), True)
    globally_allowed = as_bool(value_for(daily_reset, "allowed"), True)

    for raw_target in targets:
        if not isinstance(raw_target, dict):
            counters["invalid_target_count"] += 1
            continue
        subscription_id = str(
            value_for(raw_target, "subscriptionId", "subscription_id") or ""
        ).strip()
        if not subscription_id:
            counters["invalid_target_count"] += 1
            continue
        if not as_bool(value_for(raw_target, "eligible"), False):
            continue
        counters["provider_eligible_count"] += 1
        if as_bool(value_for(raw_target, "limitReached", "limit_reached"), False):
            counters["limit_reached_count"] += 1
            continue
        usage = as_float(
            value_for(raw_target, "currentUsagePercent", "current_usage_percent")
        )
        if usage is None or usage + 1e-9 < threshold:
            counters["threshold_rejected_count"] += 1
            continue
        remaining_days = remaining_days_for_target(raw_target, now)
        if remaining_days is None or remaining_days + 1e-9 < minimum_current_days:
            counters["duration_rejected_count"] += 1
            continue
        target_hash = hash_target(subscription_id)
        target_state = target_states.get(target_hash)
        last_attempt = (
            as_float(target_state.get("last_attempt_unix"))
            if isinstance(target_state, dict)
            else None
        )
        if last_attempt is not None and now - last_attempt < cooldown:
            counters["cooldown_count"] += 1
            continue
        candidates.append(
            Candidate(
                subscription_id=subscription_id,
                target_hash=target_hash,
                usage_percent=usage,
                threshold_percent=threshold,
                remaining_days=remaining_days,
                used_today=as_int(value_for(raw_target, "usedToday", "used_today")),
                daily_limit=as_int(value_for(raw_target, "dailyLimit", "daily_limit")),
            )
        )

    if not supported:
        reason = "provider_unsupported"
        selected = None
    elif not globally_allowed:
        reason = "provider_not_allowed"
        selected = None
    elif not candidates:
        reason = "no_safe_candidate"
        selected = None
    else:
        candidates.sort(key=lambda item: (-item.usage_percent, -item.remaining_days))
        selected = candidates[0]
        reason = "candidate_selected"

    return selected, {
        **counters,
        "reason": reason,
        "supported": supported,
        "globally_allowed": globally_allowed,
        "threshold_percent": threshold,
        "minimum_current_days": minimum_current_days,
        "safe_candidate_count": len(candidates),
        "provider_used_today": as_int(value_for(daily_reset, "usedToday", "used_today")),
        "provider_daily_limit": as_int(value_for(daily_reset, "dailyLimit", "daily_limit")),
    }


def prune_target_state(state: dict[str, Any], now: float, retention_days: int) -> None:
    targets = state.get("targets")
    if not isinstance(targets, dict):
        state["targets"] = {}
        return
    cutoff = now - max(1, retention_days) * 86_400
    for target_hash, details in list(targets.items()):
        if not isinstance(details, dict):
            del targets[target_hash]
            continue
        last_seen = max(
            as_float(details.get("last_attempt_unix")) or 0.0,
            as_float(details.get("last_success_unix")) or 0.0,
        )
        if last_seen and last_seen < cutoff:
            del targets[target_hash]


def update_observation(
    state: dict[str, Any], summary: dict[str, Any], now: float, result: str
) -> None:
    state["version"] = STATE_VERSION
    state["last_run_unix"] = now
    state["last_result"] = result
    state["last_observation"] = {
        key: value
        for key, value in summary.items()
        if key not in {"message", "subscription_id", "subscriptionId"}
    }


def mark_attempt(state: dict[str, Any], candidate: Candidate, now: float) -> None:
    targets = state.setdefault("targets", {})
    details = targets.setdefault(candidate.target_hash, {})
    details["last_attempt_unix"] = now
    details["attempt_count"] = int(details.get("attempt_count") or 0) + 1
    details["last_usage_percent"] = round(candidate.usage_percent, 6)


def mark_success(state: dict[str, Any], candidate: Candidate, now: float) -> None:
    targets = state.setdefault("targets", {})
    details = targets.setdefault(candidate.target_hash, {})
    details["last_success_unix"] = now
    details["success_count"] = int(details.get("success_count") or 0) + 1
    details.pop("last_error", None)


def mark_failure(state: dict[str, Any], candidate: Candidate, code: str) -> None:
    targets = state.setdefault("targets", {})
    details = targets.setdefault(candidate.target_hash, {})
    details["last_error"] = code


class ASXSClient:
    def __init__(
        self,
        config: dict[str, Any],
        env_path: Path,
        token: str,
        proxy: str = "",
    ) -> None:
        self.config = config
        self.env_path = env_path
        self.token = token
        self.proxy = proxy
        self.token_rotated = False

    def _url(self, endpoint: str) -> str:
        base_url = str(self.config.get("base_url") or "").strip().rstrip("/")
        if not base_url.startswith(("https://", "http://")):
            raise GuardError("invalid_base_url")
        return base_url + "/" + endpoint.lstrip("/")

    def _opener(self) -> urllib.request.OpenerDirector | None:
        if not self.proxy:
            return None
        return urllib.request.build_opener(
            urllib.request.ProxyHandler({"http": self.proxy, "https": self.proxy})
        )

    def _request(self, method: str, endpoint: str, body: Any = None) -> dict[str, Any]:
        encoded = None
        if body is not None:
            encoded = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        request = urllib.request.Request(
            self._url(endpoint),
            data=encoded,
            method=method,
            headers={
                "Authorization": "Bearer " + self.token,
                "Accept": "application/json",
                "Content-Type": "application/json",
                "User-Agent": str(self.config.get("user_agent") or DEFAULT_CONFIG["user_agent"]),
            },
        )
        timeout = max(1, int(self.config.get("timeout_sec") or 20))
        opener = self._opener()
        try:
            if opener is None:
                response_context = urllib.request.urlopen(request, timeout=timeout)
            else:
                response_context = opener.open(request, timeout=timeout)
            with response_context as response:
                status = int(getattr(response, "status", 0))
                raw = response.read()
                headers = {str(key).lower(): str(value) for key, value in response.headers.items()}
        except urllib.error.HTTPError as exc:
            raise GuardError(f"http_{exc.code}", status=int(exc.code)) from exc
        except urllib.error.URLError as exc:
            reason = getattr(exc, "reason", None)
            code = "timeout" if isinstance(reason, TimeoutError) else "network_error"
            raise GuardError(code) from exc
        except TimeoutError as exc:
            raise GuardError("timeout") from exc

        if not 200 <= status < 300:
            raise GuardError(f"http_{status}", status=status)
        new_token = headers.get("x-new-token", "").strip()
        if new_token and new_token != self.token:
            replace_env_value_atomic(self.env_path, TOKEN_ENV_KEY, new_token)
            self.token = new_token
            self.token_rotated = True
        if not raw:
            return {}
        try:
            payload = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise GuardError("invalid_json_response") from exc
        if not isinstance(payload, dict):
            raise GuardError("response_not_object")
        return payload

    def billing_state(self) -> dict[str, Any]:
        return self._request("GET", str(self.config.get("state_endpoint")))

    def reset_subscription(self, subscription_id: str) -> dict[str, Any]:
        return self._request(
            "POST",
            str(self.config.get("reset_endpoint")),
            {"subscriptionIds": [subscription_id]},
        )


def verify_reset(
    billing_state: dict[str, Any], subscription_id: str
) -> dict[str, Any]:
    daily_reset = value_for(billing_state, "dailyReset", "daily_reset")
    if not isinstance(daily_reset, dict):
        return {"verified": False, "reason": "daily_reset_missing"}
    targets = daily_reset.get("targets")
    if not isinstance(targets, list):
        targets = []
    for target in targets:
        if not isinstance(target, dict):
            continue
        current_id = str(value_for(target, "subscriptionId", "subscription_id") or "")
        if current_id != subscription_id:
            continue
        return {
            "verified": not as_bool(value_for(target, "eligible"), False),
            "usage_percent": as_float(
                value_for(target, "currentUsagePercent", "current_usage_percent")
            ),
            "used_today": as_int(value_for(target, "usedToday", "used_today")),
            "remaining_days": as_float(value_for(target, "remainingDays", "remaining_days")),
            "limit_reached": as_bool(
                value_for(target, "limitReached", "limit_reached"), False
            ),
        }
    return {"verified": True, "reason": "target_not_returned"}


@contextmanager
def acquire_lock(path: Path) -> Iterator[bool]:
    import fcntl

    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_CREAT | os.O_RDWR, 0o600)
    os.chmod(path, 0o600)
    acquired = False
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            acquired = True
        except BlockingIOError:
            acquired = False
        yield acquired
    finally:
        if acquired:
            fcntl.flock(descriptor, fcntl.LOCK_UN)
        os.close(descriptor)


def ensure_allowed_host(config: dict[str, Any]) -> None:
    allowed = config.get("allowed_hostnames")
    if not isinstance(allowed, list) or not allowed:
        return
    hostname = socket.gethostname().strip().lower()
    normalized = {str(item).strip().lower() for item in allowed if str(item).strip()}
    if hostname not in normalized:
        raise GuardError("hostname_not_allowed")


def run_cycle(
    config: dict[str, Any],
    state: dict[str, Any],
    state_path: Path,
    client: ASXSClient,
    *,
    now: float,
    dry_run: bool,
) -> int:
    billing_state = client.billing_state()
    candidate, summary = select_candidate(config, billing_state, state, now)
    update_observation(state, summary, now, "observed")
    prune_target_state(
        state,
        now,
        int(config.get("state_retention_days") or DEFAULT_CONFIG["state_retention_days"]),
    )
    emit(
        "asxs_auto_daily_reset_evaluation",
        site_id=str(config.get("site_id") or ""),
        dry_run=dry_run,
        enabled=as_bool(config.get("enabled"), False),
        token_rotated=client.token_rotated,
        **summary,
    )

    if candidate is None:
        state["last_result"] = summary["reason"]
        if not dry_run:
            save_json_atomic(state_path, state)
        return 0

    emit(
        "asxs_auto_daily_reset_candidate",
        target_hash=candidate.target_hash,
        usage_percent=round(candidate.usage_percent, 6),
        threshold_percent=round(candidate.threshold_percent, 6),
        remaining_days=round(candidate.remaining_days, 3),
        used_today=candidate.used_today,
        daily_limit=candidate.daily_limit,
        action="would_reset" if dry_run else "reset",
    )
    if dry_run:
        return 0

    if int(config.get("max_resets_per_run") or 0) != 1:
        raise GuardError("max_resets_per_run_must_equal_one")

    mark_attempt(state, candidate, now)
    state["last_result"] = "reset_in_progress"
    save_json_atomic(state_path, state)
    try:
        client.reset_subscription(candidate.subscription_id)
    except GuardError as exc:
        mark_failure(state, candidate, exc.code)
        state["last_result"] = "reset_failed"
        save_json_atomic(state_path, state)
        raise

    mark_success(state, candidate, now)
    state["last_result"] = "reset_succeeded"
    save_json_atomic(state_path, state)
    emit(
        "asxs_auto_daily_reset_success",
        target_hash=candidate.target_hash,
        token_rotated=client.token_rotated,
    )

    if as_bool(config.get("verify_after_reset"), True):
        try:
            verification = verify_reset(client.billing_state(), candidate.subscription_id)
            emit(
                "asxs_auto_daily_reset_verification",
                target_hash=candidate.target_hash,
                token_rotated=client.token_rotated,
                **verification,
            )
        except GuardError as exc:
            emit(
                "asxs_auto_daily_reset_verification",
                target_hash=candidate.target_hash,
                verified=False,
                reason=exc.code,
            )
    return 0


def run(args: argparse.Namespace) -> int:
    config_path = Path(args.config)
    config = load_config(config_path)
    env_path = Path(str(config.get("env_path") or DEFAULT_CONFIG["env_path"]))
    if args.install_token_stdin:
        install_token_from_stream(env_path, sys.stdin)
        emit("asxs_auto_daily_reset_token_installed", env_path=str(env_path), mode="0600")
        return 0

    enabled = as_bool(config.get("enabled"), False)
    if not enabled and not args.dry_run:
        emit(
            "asxs_auto_daily_reset_disabled",
            site_id=str(config.get("site_id") or ""),
            config_path=str(config_path),
        )
        return 0

    ensure_allowed_host(config)
    state_path = Path(args.state or str(config.get("state_path") or DEFAULT_CONFIG["state_path"]))
    lock_path = Path(str(config.get("lock_path") or DEFAULT_CONFIG["lock_path"]))
    with acquire_lock(lock_path) as acquired:
        if not acquired:
            emit("asxs_auto_daily_reset_busy", lock_path=str(lock_path))
            return 0
        env_values = load_env_values(env_path)
        token = str(env_values.get(TOKEN_ENV_KEY) or "").strip()
        if not token:
            raise GuardError("token_missing")
        proxy = str(env_values.get(PROXY_ENV_KEY) or config.get("proxy") or "").strip()
        state = load_json(state_path, {"version": STATE_VERSION, "targets": {}})
        if not isinstance(state, dict):
            raise GuardError("state_not_object")
        client = ASXSClient(config, env_path, token, proxy)
        return run_cycle(
            config,
            state,
            state_path,
            client,
            now=time.time(),
            dry_run=bool(args.dry_run),
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--config",
        default="/opt/new-api/ops/asxs_auto_daily_reset.json",
        help="JSON configuration path",
    )
    parser.add_argument("--state", default="", help="optional state path override")
    parser.add_argument("--dry-run", action="store_true", help="evaluate without resetting")
    parser.add_argument(
        "--install-token-stdin",
        action="store_true",
        help="atomically install the ASXS session token from stdin",
    )
    args = parser.parse_args()
    try:
        return run(args)
    except GuardError as exc:
        emit(
            "asxs_auto_daily_reset_error",
            error=exc.code,
            http_status=exc.status,
        )
        return 2
    except Exception as exc:  # Keep unexpected failures secret-safe.
        emit(
            "asxs_auto_daily_reset_error",
            error="unexpected_error",
            error_type=exc.__class__.__name__,
        )
        return 3


if __name__ == "__main__":
    raise SystemExit(main())
