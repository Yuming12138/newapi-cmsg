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


def spark_usage(used_percent: float, *, allowed: bool = True, limit_reached: bool = False) -> dict:
    return {
        "plan_type": "pro",
        "additional_rate_limits": [
            {
                "limit_name": "GPT-5.3-Codex-Spark",
                "metered_feature": "codex_bengalfox",
                "rate_limit": {
                    "allowed": allowed,
                    "limit_reached": limit_reached,
                    "primary_window": {
                        "limit_window_seconds": guard.WINDOW_7D_SECONDS,
                        "used_percent": used_percent,
                        "reset_at": 1_805_600_367,
                        "reset_after_seconds": guard.WINDOW_7D_SECONDS - 825,
                    },
                    "secondary_window": None,
                },
            }
        ],
        "_guard_auth": {
            "auth_index": "pro-auth-index",
            "account_id_hash": "pro-account-hash",
            "plan_type_hint": "pro",
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


def reset_credit_result(
    now: int,
    expires_after: int,
    *,
    remaining_percent: float = 50.0,
    available_count: int = 1,
    include_credit: bool = True,
    reset_at: int = 1_800_604_800,
) -> dict:
    result = dynamic_budget_result(remaining_percent, reset_at=reset_at)
    account = result["accounts"][0]
    account.update({
        "auth_index": "pro-auth-index",
        "account_id_hash": "pro-account-hash",
        "plan_type": "pro",
        "can_exhaust": False,
        "schedulable": remaining_percent > 0,
        "reset_credits_available": available_count,
    })
    if include_credit:
        expires_at = guard.dt.datetime.fromtimestamp(
            now + expires_after,
            guard.dt.timezone.utc,
        ).isoformat().replace("+00:00", "Z")
        account["reset_credits"] = [{
            "status": "available",
            "reset_type": "codex_rate_limits",
            "id_suffix": "credit-a",
            "expires_at": expires_at,
        }]
        account["reset_credits_earliest_expires_at"] = expires_at
    return result


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


class QuotaFeatureTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config.update({
            "quota_feature": "codex_bengalfox",
            "quota_feature_limit_name": "GPT-5.3-Codex-Spark",
            "quota_feature_plan_keywords": ["pro"],
            "quota_feature_min_remaining_percent": 0.0,
            "balance_units_per_percent": 1.0,
        })

    def test_spark_quota_is_evaluated_independently(self) -> None:
        result = guard.evaluate_quota_feature_account(self.config, {}, spark_usage(0.0))

        self.assertTrue(result["schedulable"])
        self.assertEqual("codex_bengalfox", result["quota_feature"])
        self.assertEqual(100.0, result["raw_remaining_percent"])
        self.assertEqual(100.0, result["usable_balance_units"])
        self.assertEqual(guard.WINDOW_7D_SECONDS, result["windows"]["7d"]["duration_seconds"])

    def test_spark_quota_can_be_fully_exhausted(self) -> None:
        result = guard.evaluate_quota_feature_account(
            self.config,
            {},
            spark_usage(100.0, allowed=False, limit_reached=True),
        )

        self.assertFalse(result["schedulable"])
        self.assertEqual("quota_feature_exhausted", result["reason"])
        self.assertEqual(0.0, result["usable_balance_units"])

    def test_feature_plan_filter_skips_plus_accounts(self) -> None:
        self.assertTrue(guard.quota_feature_plan_selected(self.config, "pro"))
        self.assertFalse(guard.quota_feature_plan_selected(self.config, "plus"))

    def test_unknown_auth_plan_is_resolved_from_usage_before_filtering(self) -> None:
        entries = [
            {"provider": "codex", "auth_index": "pro-auth", "account_id": "pro-account"},
            {"provider": "codex", "auth_index": "plus-auth", "account_id": "plus-account"},
        ]
        plus_usage = weekly_only_usage(10.0, "plus")
        with (
            mock.patch.object(guard, "request_json", return_value={"files": entries}),
            mock.patch.object(
                guard,
                "call_wham_usage_for_auth",
                side_effect=[spark_usage(20.0), plus_usage],
            ) as usage_request,
        ):
            accounts = guard.call_wham_usages(
                {**self.config, "cpa_base_url": "http://127.0.0.1:8317"},
                {"CPA_MANAGEMENT_KEY": "test-management-key"},
            )

        self.assertEqual(2, usage_request.call_count)
        self.assertTrue(accounts[0]["ok"])
        self.assertEqual("pro", accounts[0]["plan_type"])
        self.assertTrue(accounts[1]["skipped"])
        self.assertEqual("plus", accounts[1]["plan_type"])
        self.assertEqual("quota_feature_plan_not_selected", accounts[1]["reason"])

    def test_feature_quota_source_uses_percent_units(self) -> None:
        account = guard.evaluate_quota_feature_account(self.config, {}, spark_usage(25.0))
        result = guard.evaluate_quota(self.config, [account])
        result.update({
            "quota_feature": "codex_bengalfox",
            "quota_feature_limit_name": "GPT-5.3-Codex-Spark",
        })

        source = guard.build_quota_source(result, 75.0, True, 1_800_000_000)

        self.assertEqual("model_quota_percent", source["source_type"])
        self.assertEqual("percent", source["unit"])
        self.assertEqual("codex_bengalfox", source["raw_source"]["quota_feature"])


class DynamicDailyBudgetTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config.update({
            "dynamic_daily_budget_enabled": True,
            "min_remaining_percent_5h": 15.0,
            "min_remaining_percent_7d": 15.0,
            "quota_reset_increase_threshold_percent": 10.0,
            "quota_reset_near_full_percent": 90.0,
            "quota_reset_near_full_min_increase_percent": 5.0,
            "quota_reset_confirmation_count": 2,
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

    def test_small_quota_increase_does_not_rebuild_or_reopen_budget(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        exhausted = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(88.0), state, self.now + 120)

        self.assertFalse(exhausted["quota_ok"])
        self.assertFalse(result["quota_ok"])
        self.assertEqual(100.0, result["dynamic_daily_budget"]["baseline_remaining_percent"])
        self.assertTrue(result["dynamic_daily_budget"]["daily_exhausted"])
        self.assertEqual(0.0, result["dynamic_daily_budget"]["remaining_today_percent"])

    def test_large_same_day_refill_requires_confirmation_then_rebuilds(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)
        pending = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(98.0), state, self.now + 120)
        pending_count = pending["dynamic_daily_budget"]["reset_candidate"]["count"]
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(98.0), state, self.now + 180)

        self.assertFalse(pending["quota_ok"])
        self.assertEqual(1, pending_count)
        self.assertTrue(result["quota_ok"])
        self.assertEqual(98.0, result["dynamic_daily_budget"]["baseline_remaining_percent"])
        self.assertEqual(0.0, result["dynamic_daily_budget"]["consumed_today_percent"])
        self.assertFalse(result["dynamic_daily_budget"]["daily_exhausted"])
        self.assertEqual("weekly_quota_refilled", result["dynamic_daily_budget"]["baseline_reset_reason"])

    def test_explicit_runtime_reset_rebuilds_immediately(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)
        reset_result = dynamic_budget_result(100.0)
        reset_result["auto_reconcile"] = {"reset_count": 1}
        result = guard.apply_dynamic_daily_budget(self.config, reset_result, state, self.now + 120)

        self.assertTrue(result["quota_ok"])
        self.assertEqual(100.0, result["dynamic_daily_budget"]["baseline_remaining_percent"])
        self.assertEqual(0.0, result["dynamic_daily_budget"]["consumed_today_percent"])
        self.assertEqual("runtime_quota_reset", result["dynamic_daily_budget"]["baseline_reset_reason"])


class ResetCreditGraceTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config.update({
            "dynamic_daily_budget_enabled": True,
            "reset_credit_grace_enabled": True,
            "reset_credit_release_before_sec": 24 * 60 * 60,
            "reset_credit_auto_consume_before_sec": 10 * 60,
            "reset_credit_auto_consume_remaining_percent": 1.0,
            "reset_credit_retry_interval_sec": 60,
            "timezone": "UTC",
            "cpa_base_url": "http://127.0.0.1:8317",
        })
        self.env = {"CPA_MANAGEMENT_KEY": "test-management-key"}
        self.now = 1_800_000_000

    def test_grace_bypasses_dynamic_daily_budget_within_twenty_four_hours(self) -> None:
        state: dict = {}
        result = reset_credit_result(self.now, 12 * 60 * 60)

        guarded = guard.apply_reset_credit_grace(self.config, self.env, result, state, self.now)
        guarded = guard.apply_dynamic_daily_budget(self.config, guarded, state, self.now)

        self.assertTrue(guarded["reset_credit_grace"]["active"])
        self.assertTrue(guarded["reset_credit_grace"]["limits_released"])
        self.assertTrue(guarded["dynamic_daily_budget"]["bypassed"])
        self.assertEqual("reset_credit_grace_active", guarded["reason"])

    def test_auto_reset_retries_with_same_redeem_request_id(self) -> None:
        state: dict = {}
        first_result = reset_credit_result(self.now, 10 * 60)
        second_result = reset_credit_result(self.now, 10 * 60)

        with mock.patch.object(
            guard,
            "consume_reset_credit",
            side_effect=[TimeoutError("lost response"), {"status": "ok"}],
        ) as consume:
            first = guard.apply_reset_credit_grace(self.config, self.env, first_result, state, self.now)
            second = guard.apply_reset_credit_grace(self.config, self.env, second_result, state, self.now + 60)

        self.assertEqual(1, first["reset_credit_grace"]["consume_error_count"])
        self.assertEqual(1, second["reset_credit_grace"]["consume_success_count"])
        self.assertEqual(2, consume.call_count)
        self.assertEqual(consume.call_args_list[0].args[3], consume.call_args_list[1].args[3])

    def test_auto_reset_triggers_early_when_quota_is_nearly_exhausted(self) -> None:
        state: dict = {}
        result = reset_credit_result(self.now, 6 * 60 * 60, remaining_percent=0.5)

        with mock.patch.object(guard, "consume_reset_credit", return_value={"status": "ok"}) as consume:
            guarded = guard.apply_reset_credit_grace(self.config, self.env, result, state, self.now)

        consume.assert_called_once()
        self.assertEqual("quota_near_exhaustion", guarded["reset_credit_grace"]["accounts"][0]["auto_reset_reason"])

    def test_manual_reset_restores_budget_and_rebuilds_baseline(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(80.0),
            state,
            self.now - 60,
        )
        guard.apply_reset_credit_grace(
            self.config,
            self.env,
            reset_credit_result(self.now, 12 * 60 * 60, remaining_percent=80.0),
            state,
            self.now,
        )
        reset_result = reset_credit_result(
            self.now,
            12 * 60 * 60,
            remaining_percent=100.0,
            available_count=0,
            include_credit=False,
            reset_at=1_801_209_600,
        )

        reset_result = guard.apply_reset_credit_grace(
            self.config,
            self.env,
            reset_result,
            state,
            self.now + 60,
        )
        reset_result = guard.apply_dynamic_daily_budget(self.config, reset_result, state, self.now + 60)

        self.assertFalse(reset_result["reset_credit_grace"]["active"])
        self.assertEqual(1, reset_result["reset_credit_grace"]["manual_reset_count"])
        self.assertEqual("reset_credit_consumed", reset_result["dynamic_daily_budget"]["baseline_reset_reason"])

    def test_expired_credit_without_reset_does_not_report_confirmation(self) -> None:
        state: dict = {}
        guard.apply_reset_credit_grace(
            self.config,
            self.env,
            reset_credit_result(self.now, 60, remaining_percent=40.0),
            state,
            self.now,
            allow_consume=False,
        )
        expired_result = reset_credit_result(
            self.now,
            60,
            remaining_percent=40.0,
            available_count=0,
            include_credit=False,
        )

        expired_result = guard.apply_reset_credit_grace(
            self.config,
            self.env,
            expired_result,
            state,
            self.now + 61,
            allow_consume=False,
        )

        self.assertFalse(expired_result["reset_credit_grace"]["active"])
        self.assertEqual(0, expired_result["reset_credit_grace"]["confirmed_reset_count"])
        self.assertEqual(1, expired_result["reset_credit_grace"]["expired_without_reset_count"])

    def test_transient_reset_credit_probe_failure_keeps_grace_active(self) -> None:
        state: dict = {}
        guard.apply_reset_credit_grace(
            self.config,
            self.env,
            reset_credit_result(self.now, 12 * 60 * 60),
            state,
            self.now,
        )
        transient = reset_credit_result(
            self.now,
            12 * 60 * 60,
            available_count=0,
            include_credit=False,
        )
        account = transient["accounts"][0]
        account["reset_credits_available"] = None
        account["reset_credits_error"] = "temporary upstream failure"

        transient = guard.apply_reset_credit_grace(
            self.config,
            self.env,
            transient,
            state,
            self.now + 60,
        )

        self.assertTrue(transient["reset_credit_grace"]["active"])
        self.assertEqual(0, transient["reset_credit_grace"]["confirmed_reset_count"])


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
