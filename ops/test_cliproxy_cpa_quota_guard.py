#!/usr/bin/env python3

import importlib.util
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("cliproxy_cpa_quota_guard.py")
SPEC = importlib.util.spec_from_file_location("cliproxy_cpa_quota_guard", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
guard = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(guard)


def weekly_only_usage(used_percent: float, plan_type: str) -> dict:
    return {
        "plan_type": plan_type,
        "rate_limit": {
            "primary_window": {
                "limit_window_seconds": guard.WINDOW_7D_SECONDS,
                "used_percent": used_percent,
                "reset_at": 1_800_100_000,
                "reset_after_seconds": 86_400,
            }
        },
    }


def runtime_quota_account(runtime_reset_at: int, upstream_reset_at: int, *, quota: bool = True) -> dict:
    return {
        "auth_index": "auth-index-a",
        "plan_type": "plus",
        "disabled": False,
        "runtime_unavailable": True,
        "runtime_quota_exceeded": quota,
        "runtime_reset_at": runtime_reset_at,
        "windows": {
            "7d": {
                "remaining_percent": 100.0,
                "reset_at": upstream_reset_at,
            }
        },
    }


def dynamic_budget_result(remaining_percent: float, reset_at: int = 1_800_604_800, remaining_5h: float | None = None) -> dict:
    windows = {
        "7d": {
            "remaining_percent": remaining_percent,
            "reset_at": reset_at,
            "reset_after_seconds": 7 * 86_400,
        }
    }
    if remaining_5h is not None:
        windows["5h"] = {"remaining_percent": remaining_5h, "reset_after_seconds": guard.WINDOW_5H_SECONDS}
    return {
        "ok": True,
        "quota_ok": True,
        "within_share": True,
        "usable_balance_units": remaining_percent,
        "remaining_share_percent": remaining_percent,
        "reason": "usable_balance_available",
        "accounts": [
            {
                "ok": True,
                "bucket": "protected",
                "account_id_hash": "protected-account-a",
                "windows": windows,
            }
        ],
    }


class QuotaWindowCompatibilityTest(unittest.TestCase):
    def test_weekly_only_window_is_accepted(self) -> None:
        windows = guard.quota_windows(weekly_only_usage(25.0, "plus"))
        self.assertNotIn("5h", windows)
        self.assertEqual(75.0, guard.account_window_remaining(windows, "7d"))

    def test_weekly_window_remains_required(self) -> None:
        usage = {
            "rate_limit": {
                "primary_window": {
                    "limit_window_seconds": guard.WINDOW_5H_SECONDS,
                    "used_percent": 10.0,
                }
            }
        }
        with self.assertRaisesRegex(RuntimeError, "missing_required_7d_quota_window"):
            guard.quota_windows(usage)

    def test_weekly_only_balances_respect_account_bucket(self) -> None:
        config = dict(guard.DEFAULT_CONFIG)
        plus = guard.evaluate_account_quota(config, {}, weekly_only_usage(22.0, "plus"))
        pro = guard.evaluate_account_quota(config, {}, weekly_only_usage(22.0, "pro"))

        self.assertEqual(78.0, plus["raw_remaining_percent"])
        self.assertEqual(78.0, plus["usable_balance_units"])
        self.assertEqual(78.0, pro["raw_remaining_percent"])
        self.assertEqual(78.0, pro["usable_balance_units"])
        self.assertFalse(pro["protected_reserve_warning"])

    def test_protected_reserve_is_warning_only(self) -> None:
        config = dict(guard.DEFAULT_CONFIG)
        pro = guard.evaluate_account_quota(config, {}, weekly_only_usage(85.0, "pro"))

        self.assertEqual(15.0, pro["raw_remaining_percent"])
        self.assertEqual(15.0, pro["usable_balance_units"])
        self.assertTrue(pro["protected_reserve_warning"])
        self.assertTrue(pro["schedulable"])
        self.assertIsNone(pro["reason"])


class DynamicDailyBudgetTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config.update({
            "dynamic_daily_budget_enabled": True,
            "min_remaining_percent_5h": 15.0,
            "min_remaining_percent_7d": 15.0,
            "timezone": "UTC",
        })
        self.now = 1_800_000_000

    def test_daily_budget_reserves_fifteen_percent(self) -> None:
        state: dict = {}
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)

        budget = result["dynamic_daily_budget"]
        self.assertTrue(result["quota_ok"])
        self.assertAlmostEqual(85.0 / 7.0, budget["daily_limit_percent"], places=6)
        self.assertAlmostEqual(15.0, budget["reserve_percent"], places=6)
        self.assertAlmostEqual(85.0 / 7.0, result["usable_balance_units"], places=6)

    def test_daily_budget_exhaustion_disables_channel(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)

        self.assertFalse(result["quota_ok"])
        self.assertEqual("dynamic_daily_budget_exhausted", result["reason"])
        self.assertEqual(0.0, result["usable_balance_units"])

    def test_hard_reserve_stops_at_fifteen_percent(self) -> None:
        state: dict = {}
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(15.0), state, self.now)

        self.assertFalse(result["quota_ok"])
        self.assertEqual("protected_reserve_reached", result["reason"])

    def test_five_hour_reserve_uses_same_fifteen_percent_line(self) -> None:
        state: dict = {}
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(80.0, remaining_5h=15.0), state, self.now)

        self.assertFalse(result["quota_ok"])
        self.assertEqual("protected_reserve_reached", result["reason"])

    def test_quota_increase_rebuilds_budget(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(90.0), state, self.now + 60)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0, 1_801_209_600), state, self.now + 120)

        self.assertTrue(result["quota_ok"])
        self.assertEqual(100.0, result["dynamic_daily_budget"]["baseline_remaining_percent"])
        self.assertEqual(0.0, result["dynamic_daily_budget"]["consumed_today_percent"])


class RuntimeQuotaReconcileTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config["cpa_base_url"] = "http://127.0.0.1:8317"
        self.env = {"CPA_MANAGEMENT_KEY": "test-management-key"}

    def test_advanced_upstream_window_resets_immediately(self) -> None:
        state: dict = {}
        result = {
            "accounts": [runtime_quota_account(1_800_000_000, 1_800_100_000)],
        }
        with mock.patch.object(guard, "request_json", return_value={"status": "ok"}) as request:
            summary = guard.auto_reconcile_runtime_quota(self.config, self.env, result, state, now=1_799_000_000)

        self.assertEqual(1, summary["reset_count"])
        request.assert_called_once()
        self.assertEqual(
            "7d:1800100000",
            state["quota_auto_reconcile"]["accounts"]["auth-index-a"]["last_reset_window"],
        )

    def test_same_window_requires_confirmation_and_only_resets_once(self) -> None:
        state: dict = {}
        result = {
            "accounts": [runtime_quota_account(1_800_100_000, 1_800_100_000)],
        }
        with mock.patch.object(guard, "request_json", return_value={"status": "ok"}) as request:
            first = guard.auto_reconcile_runtime_quota(self.config, self.env, result, state, now=1_799_000_000)
            second = guard.auto_reconcile_runtime_quota(self.config, self.env, result, state, now=1_799_000_060)
            third = guard.auto_reconcile_runtime_quota(self.config, self.env, result, state, now=1_799_000_120)

        self.assertEqual(1, first["pending_count"])
        self.assertEqual(1, second["reset_count"])
        self.assertEqual(0, third["reset_count"])
        request.assert_called_once()

    def test_non_quota_runtime_cooldown_is_ignored(self) -> None:
        state: dict = {}
        result = {
            "accounts": [runtime_quota_account(1_800_000_000, 1_800_100_000, quota=False)],
        }
        with mock.patch.object(guard, "request_json", return_value={"status": "ok"}) as request:
            summary = guard.auto_reconcile_runtime_quota(self.config, self.env, result, state, now=1_799_000_000)

        self.assertEqual(0, summary["reset_count"])
        request.assert_not_called()


if __name__ == "__main__":
    unittest.main()
