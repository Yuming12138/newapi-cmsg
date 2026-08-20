#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import io
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any


MODULE_PATH = Path(__file__).with_name("asxs_auto_daily_reset.py")
SPEC = importlib.util.spec_from_file_location("asxs_auto_daily_reset", MODULE_PATH)
assert SPEC and SPEC.loader
guard = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = guard
SPEC.loader.exec_module(guard)


SUBSCRIPTION_ID = "subscription-secret-id"
NOW = 1_800_000_000.0


def billing_payload(
    *,
    usage: float = 99.0,
    eligible: bool = True,
    limit_reached: bool = False,
    remaining_days: float = 10.0,
    allowed: bool = True,
) -> dict[str, Any]:
    return {
        "dailyReset": {
            "supported": True,
            "allowed": allowed,
            "usedToday": 2,
            "dailyLimit": 4,
            "usageThresholdPercent": 90,
            "minRemainingDays": 2,
            "targets": [
                {
                    "subscriptionId": SUBSCRIPTION_ID,
                    "currentUsagePercent": usage,
                    "remainingDays": remaining_days,
                    "eligible": eligible,
                    "limitReached": limit_reached,
                    "usedToday": 2,
                    "dailyLimit": 4,
                }
            ],
        }
    }


class FakeClient:
    def __init__(self, before: dict[str, Any], after: dict[str, Any] | None = None) -> None:
        self.before = before
        self.after = after or billing_payload(usage=0.0, eligible=False)
        self.billing_calls = 0
        self.reset_calls: list[str] = []
        self.token_rotated = False

    def billing_state(self) -> dict[str, Any]:
        self.billing_calls += 1
        return self.before if self.billing_calls == 1 else self.after

    def reset_subscription(self, subscription_id: str) -> dict[str, Any]:
        self.reset_calls.append(subscription_id)
        return {"ok": True}


class SelectionTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config["enabled"] = True
        self.state = {"version": 1, "targets": {}}

    def test_selects_provider_eligible_target(self) -> None:
        candidate, summary = guard.select_candidate(
            self.config, billing_payload(), self.state, NOW
        )

        self.assertIsNotNone(candidate)
        assert candidate is not None
        self.assertEqual(guard.hash_target(SUBSCRIPTION_ID), candidate.target_hash)
        self.assertEqual("candidate_selected", summary["reason"])

    def test_local_threshold_is_not_weaker_than_provider(self) -> None:
        self.config["minimum_usage_percent"] = 96.0
        candidate, summary = guard.select_candidate(
            self.config, billing_payload(usage=95.0), self.state, NOW
        )

        self.assertIsNone(candidate)
        self.assertEqual(1, summary["threshold_rejected_count"])

    def test_default_threshold_triggers_only_at_99_percent(self) -> None:
        candidate, summary = guard.select_candidate(
            self.config, billing_payload(usage=98.99), self.state, NOW
        )

        self.assertIsNone(candidate)
        self.assertEqual(99.0, summary["threshold_percent"])
        self.assertEqual(1, summary["threshold_rejected_count"])

        candidate, summary = guard.select_candidate(
            self.config, billing_payload(usage=99.0), self.state, NOW
        )

        self.assertIsNotNone(candidate)
        self.assertEqual(99.0, summary["threshold_percent"])

    def test_limit_reached_is_rejected(self) -> None:
        candidate, summary = guard.select_candidate(
            self.config, billing_payload(limit_reached=True), self.state, NOW
        )

        self.assertIsNone(candidate)
        self.assertEqual(1, summary["limit_reached_count"])

    def test_duration_floor_applies_after_one_day_deduction(self) -> None:
        candidate, summary = guard.select_candidate(
            self.config, billing_payload(remaining_days=2.5), self.state, NOW
        )

        self.assertIsNone(candidate)
        self.assertEqual(1, summary["duration_rejected_count"])

    def test_cooldown_prevents_duplicate_after_uncertain_request(self) -> None:
        target_hash = guard.hash_target(SUBSCRIPTION_ID)
        self.state["targets"][target_hash] = {"last_attempt_unix": NOW - 60}

        candidate, summary = guard.select_candidate(
            self.config, billing_payload(), self.state, NOW
        )

        self.assertIsNone(candidate)
        self.assertEqual(1, summary["cooldown_count"])

    def test_global_provider_denial_fails_closed(self) -> None:
        candidate, summary = guard.select_candidate(
            self.config, billing_payload(allowed=False), self.state, NOW
        )

        self.assertIsNone(candidate)
        self.assertEqual("provider_not_allowed", summary["reason"])


class StateAndTokenTest(unittest.TestCase):
    def test_token_install_and_rotation_keep_mode_0600(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            env_path = Path(temporary) / "guard.env"
            guard.install_token_from_stream(env_path, io.StringIO("a" * 64 + "\n"))
            guard.replace_env_value_atomic(env_path, guard.TOKEN_ENV_KEY, "b" * 64)

            self.assertEqual("b" * 64, guard.load_env_values(env_path)[guard.TOKEN_ENV_KEY])
            self.assertEqual(0o600, env_path.stat().st_mode & 0o777)

    def test_persisted_state_never_contains_raw_subscription_id(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            state_path = Path(temporary) / "state.json"
            candidate = guard.Candidate(
                subscription_id=SUBSCRIPTION_ID,
                target_hash=guard.hash_target(SUBSCRIPTION_ID),
                usage_percent=95.0,
                threshold_percent=99.0,
                remaining_days=10.0,
                used_today=2,
                daily_limit=4,
            )
            state: dict[str, Any] = {"targets": {}}
            guard.mark_attempt(state, candidate, NOW)
            guard.mark_success(state, candidate, NOW)
            guard.save_json_atomic(state_path, state)

            raw = state_path.read_text(encoding="utf-8")
            self.assertNotIn(SUBSCRIPTION_ID, raw)
            self.assertIn(candidate.target_hash, raw)
            self.assertEqual(0o600, state_path.stat().st_mode & 0o777)


class OperatorControlTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config.update({"enabled": True, "site_id": "aliyun"})

    def write_control(self, path: Path, *, enabled: bool, site_id: str = "aliyun") -> None:
        path.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "site_id": site_id,
                    "enabled": enabled,
                    "updated_at": int(NOW),
                }
            ),
            encoding="utf-8",
        )

    def test_no_configured_control_path_preserves_legacy_config(self) -> None:
        self.assertIsNone(guard.operator_control_enabled(self.config))

    def test_control_file_can_enable_or_disable_reset(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "control.json"
            self.config["control_path"] = str(path)
            self.write_control(path, enabled=False)
            self.assertFalse(guard.operator_control_enabled(self.config))

            self.write_control(path, enabled=True)
            self.assertTrue(guard.operator_control_enabled(self.config))

    def test_configured_but_missing_control_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            self.config["control_path"] = str(Path(temporary) / "missing.json")
            with self.assertRaisesRegex(guard.GuardError, "operator_control_missing"):
                guard.operator_control_enabled(self.config)

    def test_control_for_another_site_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "control.json"
            self.config["control_path"] = str(path)
            self.write_control(path, enabled=True, site_id="campus")
            with self.assertRaisesRegex(guard.GuardError, "operator_control_site_mismatch"):
                guard.operator_control_enabled(self.config)

    def test_disabled_control_returns_before_loading_token(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            control_path = root / "control.json"
            config_path = root / "config.json"
            self.write_control(control_path, enabled=False)
            config_path.write_text(
                json.dumps(
                    {
                        "enabled": True,
                        "site_id": "aliyun",
                        "control_path": str(control_path),
                        "env_path": str(root / "missing.env"),
                    }
                ),
                encoding="utf-8",
            )
            args = type(
                "Args",
                (),
                {
                    "config": str(config_path),
                    "state": "",
                    "dry_run": False,
                    "install_token_stdin": False,
                },
            )()

            self.assertEqual(0, guard.run(args))


class RunCycleTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config.update({"enabled": True, "verify_after_reset": True})

    def test_dry_run_never_posts_or_writes_state(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            state_path = Path(temporary) / "state.json"
            client = FakeClient(billing_payload())

            result = guard.run_cycle(
                self.config,
                {"targets": {}},
                state_path,
                client,
                now=NOW,
                dry_run=True,
            )

            self.assertEqual(0, result)
            self.assertEqual([], client.reset_calls)
            self.assertFalse(state_path.exists())

    def test_active_cycle_posts_exact_subscription_and_verifies(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            state_path = Path(temporary) / "state.json"
            client = FakeClient(billing_payload())

            result = guard.run_cycle(
                self.config,
                {"targets": {}},
                state_path,
                client,
                now=NOW,
                dry_run=False,
            )

            self.assertEqual(0, result)
            self.assertEqual([SUBSCRIPTION_ID], client.reset_calls)
            persisted = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual("reset_succeeded", persisted["last_result"])
            self.assertNotIn(SUBSCRIPTION_ID, state_path.read_text(encoding="utf-8"))

    def test_client_request_body_matches_current_asxs_contract(self) -> None:
        class CaptureClient(guard.ASXSClient):
            def _request(self, method: str, endpoint: str, body: Any = None) -> dict[str, Any]:
                self.captured = (method, endpoint, body)
                return {}

        client = CaptureClient(self.config, Path("/tmp/unused.env"), "x" * 64)
        client.reset_subscription(SUBSCRIPTION_ID)

        self.assertEqual(
            (
                "POST",
                self.config["reset_endpoint"],
                {"subscriptionIds": [SUBSCRIPTION_ID]},
            ),
            client.captured,
        )


if __name__ == "__main__":
    unittest.main()
