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


class ManagementHeadersTest(unittest.TestCase):
    def test_key_only_sends_bearer_and_management_header(self) -> None:
        headers = guard.management_headers(
            {"CPA_MANAGEMENT_KEY": "test-management-key"},
            "http://172.17.0.1:8327",
        )

        self.assertEqual("Bearer test-management-key", headers["Authorization"])
        self.assertEqual("test-management-key", headers["X-Management-Key"])

    def test_basic_auth_keeps_management_key_in_separate_header(self) -> None:
        headers = guard.management_headers(
            {
                "CPA_MANAGEMENT_KEY": "test-management-key",
                "CPA_BASIC_USERNAME": "admin",
                "CPA_BASIC_PASSWORD": "test-password",
            },
            "https://cliproxy.example.com",
        )

        self.assertTrue(headers["Authorization"].startswith("Basic "))
        self.assertEqual("test-management-key", headers["X-Management-Key"])

    def test_home_auth_identity_falls_back_to_auth_index_hash(self) -> None:
        first = guard.account_identity({"auth_index": "home-runtime-auth"})
        second = guard.account_identity({"auth_index": "home-runtime-auth"})

        self.assertEqual("home-runtime-auth", first[0])
        self.assertEqual("", first[1])
        self.assertTrue(first[2])
        self.assertEqual(first[2], second[2])

    def test_wham_probe_omits_redacted_account_header(self) -> None:
        with mock.patch.object(
            guard,
            "request_json",
            return_value={"status_code": 200, "body": {"plan_type": "pro"}},
        ) as request:
            usage = guard.call_wham_usage_for_auth(
                guard.DEFAULT_CONFIG,
                "http://home.internal:8327",
                {"X-Management-Key": "test-management-key"},
                30,
                {"auth_index": "home-runtime-auth", "provider": "codex"},
            )

        payload = request.call_args.args[3]
        self.assertNotIn("Chatgpt-Account-Id", payload["header"])
        self.assertTrue(usage["_guard_auth"]["account_id_hash"])


class OptionOverrideTest(unittest.TestCase):
    class StaticDB:
        def psql(self, sql: str, capture: bool = False) -> str:
            return "\n".join([
                "cliproxy_cpa_quota_guard.daily_budget_model_reserve_percent\t5",
                'cliproxy_cpa_quota_guard.daily_budget_model_reserve_models\t["gpt-5.6-luna", ""]',
            ])

    def test_loads_model_reserve_percent_and_json_list(self) -> None:
        overrides = guard.load_option_overrides(self.StaticDB())

        self.assertEqual(5.0, overrides["daily_budget_model_reserve_percent"])
        self.assertEqual(["gpt-5.6-luna"], overrides["daily_budget_model_reserve_models"])


class ApplyResultAbilityReconciliationTest(unittest.TestCase):
    class RecordingDB:
        def __init__(self) -> None:
            self.statements: list[str] = []

        def psql(self, sql: str, capture: bool = False) -> str:
            self.statements.append(sql)
            return ""

    def test_enabled_channel_reenables_stale_abilities(self) -> None:
        db = self.RecordingDB()
        channel = {"id": 12, "name": "test-cpa", "status": guard.STATUS_ENABLED, "other_info": "{}"}
        result = {
            "ok": True,
            "quota_ok": True,
            "usable_balance_units": 5.0,
            "total_balance_units": 50.0,
            "reason": "usable_balance_available",
        }

        guard.apply_result(db, channel, result, {})

        self.assertEqual(1, len(db.statements))
        self.assertIn("update abilities set enabled = true where channel_id = 12;", db.statements[0])
        self.assertNotIn("status = 1", db.statements[0])

    def test_model_reserve_enables_only_allowlisted_ability(self) -> None:
        db = self.RecordingDB()
        channel = {"id": 12, "name": "test-cpa", "status": guard.STATUS_AUTO_DISABLED, "other_info": "{}"}
        result = {
            "ok": True,
            "quota_ok": True,
            "usable_balance_units": 4.25,
            "total_balance_units": 50.0,
            "reason": "dynamic_daily_budget_model_reserve_active",
            "quota_model_allowlist": ["gpt-5.6-luna"],
        }

        message = guard.apply_result(db, channel, result, {})

        self.assertEqual(1, len(db.statements))
        self.assertIn("status = 1", db.statements[0])
        self.assertIn(
            "update abilities set enabled = case when model in ('gpt-5.6-luna') then true else false end where channel_id = 12;",
            db.statements[0],
        )
        self.assertIn("models=gpt-5.6-luna", message)

    def test_auto_disabled_channel_keeps_abilities_disabled(self) -> None:
        db = self.RecordingDB()
        channel = {"id": 12, "name": "test-cpa", "status": guard.STATUS_AUTO_DISABLED, "other_info": "{}"}
        result = {
            "ok": True,
            "quota_ok": False,
            "usable_balance_units": 0.0,
            "reason": "quota_low_watermark_reached",
        }

        guard.apply_result(db, channel, result, {})

        self.assertEqual(1, len(db.statements))
        self.assertIn("update abilities set enabled = false where channel_id = 12;", db.statements[0])
        self.assertNotIn("status = 3", db.statements[0])

    def test_manually_disabled_channel_does_not_override_abilities(self) -> None:
        db = self.RecordingDB()
        channel = {"id": 12, "name": "test-cpa", "status": guard.STATUS_MANUALLY_DISABLED, "other_info": "{}"}
        result = {
            "ok": True,
            "quota_ok": True,
            "usable_balance_units": 5.0,
            "total_balance_units": 50.0,
            "reason": "usable_balance_available",
        }

        guard.apply_result(db, channel, result, {})

        self.assertEqual(1, len(db.statements))
        self.assertNotIn("update abilities", db.statements[0])

    def test_fail_closed_preserves_last_balance_and_marks_source_unknown(self) -> None:
        db = self.RecordingDB()
        channel = {
            "id": 12,
            "name": "test-cpa",
            "status": guard.STATUS_ENABLED,
            "balance": 42.5,
            "other_info": "{}",
        }
        result = {
            "ok": False,
            "reason": "quota_probe_failed",
            "error": "management_auth_http_403",
            "fail_closed": True,
        }

        message = guard.apply_result(db, channel, result, {"failure_count": 1})

        self.assertEqual(1, len(db.statements))
        statement = db.statements[0]
        self.assertNotIn("balance = ", statement)
        self.assertNotIn("balance_updated_time = ", statement)
        self.assertIn("status = 3", statement)
        self.assertIn("update abilities set enabled = false where channel_id = 12;", statement)
        self.assertIn('"balance": 42.5', statement)
        self.assertIn('"spendable": false', statement)
        self.assertIn('"status": "unknown"', statement)
        self.assertIn("balance_preserved", message)


class ManagementAuthBackoffTest(unittest.TestCase):
    def test_transient_probe_failure_uses_recent_success_grace(self) -> None:
        now = 1_800_000_000
        state = {"last_success_at": now - 60}

        result = guard.record_probe_failure(
            {**guard.DEFAULT_CONFIG, "probe_failure_grace_sec": 600},
            state,
            RuntimeError("timed out while reading authorization=do-not-store"),
            now,
        )

        self.assertFalse(result["fail_closed"])
        self.assertTrue(result["stale_grace"])
        self.assertEqual("timeout", result["probe_error_category"])
        self.assertIn("authorization=[REDACTED]", state["last_probe_failure"]["error"])
        self.assertNotIn("do-not-store", state["last_probe_failure"]["error"])
        self.assertEqual("quota_probe_failed_stale_grace", result["reason"])

    def test_probe_failure_closes_after_success_grace_expires(self) -> None:
        now = 1_800_000_000
        state = {"last_success_at": now - 601}
        config = {**guard.DEFAULT_CONFIG, "probe_failure_grace_sec": 600}

        guard.record_probe_failure(config, state, RuntimeError("temporary network failure"), now)
        guard.record_probe_failure(config, state, RuntimeError("temporary network failure"), now + 60)
        result = guard.record_probe_failure(config, state, RuntimeError("temporary network failure"), now + 120)

        self.assertTrue(result["fail_closed"])
        self.assertFalse(result["stale_grace"])
        self.assertEqual(3, state["failure_count"])

    def test_request_json_classifies_management_403_without_response_body(self) -> None:
        error = guard.urllib.error.HTTPError(
            "http://home.internal/v0/management/quota-health",
            403,
            "Forbidden",
            {},
            None,
        )
        with mock.patch.object(guard.urllib.request, "urlopen", side_effect=error):
            with self.assertRaisesRegex(guard.ManagementAuthError, "management_auth_http_403"):
                guard.request_json("http://home.internal/v0/management/quota-health", {}, 30)

    def test_auth_failure_fails_closed_immediately_and_starts_backoff(self) -> None:
        state: dict = {}
        now = 1_800_000_000
        config = {**guard.DEFAULT_CONFIG, "management_auth_failure_backoff_sec": 1_800}

        result = guard.record_probe_failure(
            config,
            state,
            guard.ManagementAuthError("management_auth_http_403"),
            now,
        )

        self.assertTrue(result["fail_closed"])
        self.assertTrue(result["management_auth_failure"])
        self.assertEqual(now + 1_800, state["management_auth_backoff_until"])
        self.assertEqual(1, state["failure_count"])

        backoff = guard.management_auth_backoff_result(state, now + 60)
        self.assertIsNotNone(backoff)
        assert backoff is not None
        self.assertTrue(backoff["fail_closed"])
        self.assertEqual("management_auth_backoff", backoff["error"])
        self.assertEqual(1_740, backoff["retry_after_seconds"])

    def test_auth_failure_does_not_probe_fallback_management_endpoints(self) -> None:
        with (
            mock.patch.object(
                guard,
                "call_cpa_quota_health",
                side_effect=guard.ManagementAuthError("management_auth_http_403"),
            ),
            mock.patch.object(guard, "call_home_quota_health") as home,
            mock.patch.object(guard, "call_wham_usages") as fallback,
        ):
            with self.assertRaisesRegex(guard.ManagementAuthError, "management_auth_http_403"):
                guard.call_quota_health(guard.DEFAULT_CONFIG, {})

        home.assert_not_called()
        fallback.assert_not_called()


def combined_dynamic_budget_result(*results: dict) -> dict:
    accounts = [account for result in results for account in result["accounts"]]
    remaining_total = sum(
        float(account["windows"]["7d"]["remaining_percent"])
        for account in accounts
    )
    return {
        "ok": True,
        "quota_ok": True,
        "within_share": True,
        "usable_balance_units": remaining_total,
        "remaining_share_percent": remaining_total,
        "reason": "usable_balance_available",
        "accounts": accounts,
    }


class ResetCreditConsumeRoutingTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = {
            **guard.DEFAULT_CONFIG,
            "cpa_base_url": "http://management.internal:8327",
        }
        self.env = {"CPA_MANAGEMENT_KEY": "test-management-key"}

    def test_home_consume_uses_credential_path_and_expected_expiry(self) -> None:
        with mock.patch.object(guard, "request_json", return_value={"status": "ok"}) as request:
            guard.consume_reset_credit(
                self.config,
                self.env,
                "",
                "6885d7c5-c5d8-46d6-a37d-f0c52e49232e",
                credential_id="credential/with slash",
                expected_expires_at="2026-08-01T03:03:09Z",
            )

        url, _headers, _timeout, body = request.call_args.args
        self.assertTrue(url.endswith("/quota/credentials/credential%2Fwith%20slash/reset-credits/consume"))
        self.assertEqual("2026-08-01T03:03:09Z", body["expected_expires_at"])
        self.assertNotIn("auth_index", body)

    def test_legacy_cpa_consume_path_remains_compatible(self) -> None:
        with mock.patch.object(guard, "request_json", return_value={"status": "ok"}) as request:
            guard.consume_reset_credit(
                self.config,
                self.env,
                "legacy-auth-index",
                "6885d7c5-c5d8-46d6-a37d-f0c52e49232e",
            )

        url, _headers, _timeout, body = request.call_args.args
        self.assertTrue(url.endswith("/v0/management/consume-codex-reset-credit"))
        self.assertEqual("legacy-auth-index", body["auth_index"])
        self.assertNotIn("expected_expires_at", body)


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

    def test_personal_accounts_and_shared_pro_are_grouped_separately(self) -> None:
        config = dict(guard.DEFAULT_CONFIG)
        accounts = [
            guard.evaluate_account_quota(config, {}, weekly_only_usage(22.0, plan_type))
            for plan_type in ("plus", "free", "free", "free", "pro")
        ]

        result = guard.evaluate_quota(config, accounts)

        self.assertEqual(4, result["buckets"]["personal"]["account_count"])
        self.assertEqual(1, result["buckets"]["protected"]["account_count"])
        self.assertTrue(all(account["can_exhaust"] for account in accounts[:4]))
        self.assertFalse(accounts[4]["can_exhaust"])

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

    def test_unavailable_account_probe_error_is_not_treated_as_zero_quota(self) -> None:
        entries = [{
            "provider": "codex",
            "auth_index": "pro-auth",
            "account_id": "pro-account",
            "plan_type": "pro",
            "unavailable": True,
        }]
        with (
            mock.patch.object(guard, "request_json", return_value={"files": entries}),
            mock.patch.object(
                guard,
                "call_wham_usage_for_auth",
                side_effect=RuntimeError("wham_usage_http_503"),
            ),
        ):
            with self.assertRaisesRegex(RuntimeError, "wham_usage_all_accounts_failed"):
                guard.call_wham_usages(
                    {**self.config, "cpa_base_url": "http://127.0.0.1:8317"},
                    {"CPA_MANAGEMENT_KEY": "test-management-key"},
                )

    def test_intentionally_disabled_accounts_still_report_zero_quota(self) -> None:
        entries = [{
            "provider": "codex",
            "auth_index": "pro-auth",
            "account_id": "pro-account",
            "plan_type": "pro",
            "disabled": True,
        }]
        with mock.patch.object(guard, "request_json", return_value={"files": entries}):
            accounts = guard.call_wham_usages(
                {**self.config, "cpa_base_url": "http://127.0.0.1:8317"},
                {"CPA_MANAGEMENT_KEY": "test-management-key"},
            )

        self.assertEqual(1, len(accounts))
        self.assertTrue(accounts[0]["skipped"])
        self.assertEqual("auth_disabled", accounts[0]["reason"])

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

    def test_quota_source_includes_structured_protection_block(self) -> None:
        result = {
            "ok": True,
            "reason": "dynamic_daily_budget_exhausted",
            "quota_block": {
                "kind": "daily_protected_budget",
                "code": "channel_daily_protected_budget_exhausted",
                "http_status": 429,
                "retry_at": 1_800_028_800,
                "retry_after_seconds": 28_800,
                "timezone": "UTC",
            },
        }

        source = guard.build_quota_source(result, 0.0, False, 1_800_000_000)

        self.assertEqual(429, source["block"]["http_status"])
        self.assertEqual("channel_daily_protected_budget_exhausted", source["block"]["code"])


class QuotaHealthEndpointTest(unittest.TestCase):
    def test_probe_failure_payload_falls_back_instead_of_reporting_zero_quota(self) -> None:
        payload = {
            "ok": False,
            "guard_mode": "bucket_low_watermark",
            "reason": "quota_probe_failed",
            "error": "wham_usage_http_503",
            "accounts": [{"skipped": True, "error": "wham_usage_http_503"}],
        }
        with mock.patch.object(guard, "request_json", return_value=payload):
            with self.assertRaisesRegex(RuntimeError, "cpa_quota_health_probe_failed"):
                guard.call_cpa_quota_health(
                    {
                        **guard.DEFAULT_CONFIG,
                        "cpa_base_url": "http://127.0.0.1:8317",
                    },
                    {"CPA_MANAGEMENT_KEY": "test-management-key"},
                )

    @staticmethod
    def home_payload(now: int, expires_after: int = 12 * 60 * 60) -> tuple[dict, dict]:
        reset_at = guard.dt.datetime.fromtimestamp(
            now + 5 * 86_400,
            guard.dt.timezone.utc,
        ).isoformat().replace("+00:00", "Z")
        expires_at = guard.dt.datetime.fromtimestamp(
            now + expires_after,
            guard.dt.timezone.utc,
        ).isoformat().replace("+00:00", "Z")
        item = {
            "credential_id": "home-pro-credential",
            "credential_status": "enabled",
            "quota_status": "healthy",
            "freshness": "fresh",
            "collection_status": "partial",
            "label": "test-pro-account",
            "plan": {"name": "Pro 20x", "premium": True},
            "primary_windows": [
                {
                    "id": "codex-bengalfox-1-week",
                    "scope": "model",
                    "scope_id": "codex_bengalfox",
                    "remaining_ratio": 1.0,
                    "used_ratio": 0.0,
                    "window_seconds": guard.WINDOW_7D_SECONDS,
                    "reset_at": reset_at,
                },
                {
                    "id": "codex-1-week",
                    "scope": "account",
                    "remaining_ratio": 0.45,
                    "used_ratio": 0.55,
                    "window_seconds": guard.WINDOW_7D_SECONDS,
                    "reset_at": reset_at,
                },
            ],
        }
        detail = {
            "credential": item,
            "windows": item["primary_windows"],
            "reset_credits": {
                "available_count": 2,
                "observed_at": guard.dt.datetime.fromtimestamp(now, guard.dt.timezone.utc).isoformat(),
                "credits": [
                    {
                        "key": "credit-home-opaque-key",
                        "status": "available",
                        "granted_at": guard.dt.datetime.fromtimestamp(now - 86_400, guard.dt.timezone.utc).isoformat(),
                        "expires_at": expires_at,
                    }
                ],
            },
        }
        return {"items": [item], "total": 1}, detail

    def test_home_snapshot_converts_account_quota_and_reset_credit(self) -> None:
        now = 1_800_000_000
        listing, detail = self.home_payload(now)

        def request(url: str, *_args, **_kwargs) -> dict:
            if "/quota/credentials?" in url:
                return listing
            if "/quota/credentials/home-pro-credential" in url:
                return detail
            raise AssertionError(url)

        config = {
            **guard.DEFAULT_CONFIG,
            "cpa_base_url": "http://home.internal:8327",
            "dynamic_daily_budget_enabled": True,
            "reset_credit_grace_enabled": True,
        }
        with (
            mock.patch.object(guard, "request_json", side_effect=request),
            mock.patch.object(guard.time, "time", return_value=now),
        ):
            result = guard.call_home_quota_health(config, {"CPA_MANAGEMENT_KEY": "test-management-key"})

        self.assertEqual("home_quota_snapshots", result["quota_health_source"])
        self.assertFalse(result["reset_credit_consume_supported"])
        self.assertEqual(45.0, result["usable_balance_units"])
        self.assertEqual("protected", result["accounts"][0]["bucket"])
        self.assertEqual(45.0, result["accounts"][0]["windows"]["7d"]["remaining_percent"])
        self.assertEqual(2, result["accounts"][0]["reset_credits_available"])
        self.assertEqual(detail["reset_credits"]["credits"][0]["expires_at"], result["accounts"][0]["reset_credits"][0]["expires_at"])
        self.assertEqual(
            detail["reset_credits"]["credits"][0]["expires_at"],
            result["buckets"]["protected"]["accounts"][0]["reset_credits_earliest_expires_at"],
        )
        self.assertEqual(2, result["buckets"]["protected"]["reset_credits_available"])
        self.assertEqual(
            detail["reset_credits"]["credits"][0]["expires_at"],
            result["buckets"]["protected"]["reset_credits_earliest_expires_at"],
        )
        self.assertEqual("home-pro-credential", result["accounts"][0]["credential_id"])
        self.assertEqual("credit-home-opaque-key", result["accounts"][0]["reset_credits"][0]["id_suffix"])

        state: dict = {}
        guarded = guard.apply_reset_credit_grace(
            config,
            {},
            result,
            state,
            now,
            allow_consume=result["reset_credit_consume_supported"],
        )
        guarded = guard.apply_dynamic_daily_budget(config, guarded, state, now)
        self.assertTrue(guarded["reset_credit_grace"]["active"])
        self.assertTrue(guarded["reset_credit_grace"]["limits_released"])
        self.assertTrue(guarded["dynamic_daily_budget"]["bypassed"])
        self.assertEqual(45.0, guarded["usable_balance_units"])

    def test_fresh_home_snapshot_survives_latest_probe_failure(self) -> None:
        now = 1_800_000_000
        listing, detail = self.home_payload(now)
        listing["items"][0]["collection_status"] = "failed"
        detail["credential"]["collection_status"] = "failed"

        def request(url: str, *_args, **_kwargs) -> dict:
            if "/quota/credentials?" in url:
                return listing
            if "/quota/credentials/home-pro-credential" in url:
                return detail
            if url.endswith("/capabilities"):
                return {"capabilities": {}}
            raise AssertionError(url)

        config = {
            **guard.DEFAULT_CONFIG,
            "cpa_base_url": "http://home.internal:8327",
        }
        with (
            mock.patch.object(guard, "request_json", side_effect=request),
            mock.patch.object(guard.time, "time", return_value=now),
        ):
            result = guard.call_home_quota_health(config, {"CPA_MANAGEMENT_KEY": "test-management-key"})

        account = result["accounts"][0]
        self.assertEqual("failed", account["home_collection_status"])
        self.assertEqual(2, account["reset_credits_available"])
        self.assertEqual(
            detail["reset_credits"]["credits"][0]["expires_at"],
            account["reset_credits_earliest_expires_at"],
        )

    def test_home_snapshot_does_not_call_unsupported_credit_consume(self) -> None:
        now = 1_800_000_000
        listing, detail = self.home_payload(now, expires_after=5 * 60)

        def request(url: str, *_args, **_kwargs) -> dict:
            return listing if "/quota/credentials?" in url else detail

        config = {
            **guard.DEFAULT_CONFIG,
            "cpa_base_url": "http://home.internal:8327",
            "reset_credit_grace_enabled": True,
        }
        with (
            mock.patch.object(guard, "request_json", side_effect=request),
            mock.patch.object(guard.time, "time", return_value=now),
        ):
            result = guard.call_home_quota_health(config, {"CPA_MANAGEMENT_KEY": "test-management-key"})
        with mock.patch.object(guard, "consume_reset_credit") as consume:
            guarded = guard.apply_reset_credit_grace(
                config,
                {},
                result,
                {},
                now,
                allow_consume=result["reset_credit_consume_supported"],
            )

        consume.assert_not_called()
        self.assertTrue(guarded["reset_credit_grace"]["active"])

    def test_home_snapshot_uses_capability_gated_consume_endpoint(self) -> None:
        now = 1_800_000_000
        listing, detail = self.home_payload(now, expires_after=5 * 60)

        def request(url: str, *_args, **_kwargs) -> dict:
            if "/quota/credentials?" in url:
                return listing
            if url.endswith("/capabilities"):
                return {"capabilities": {"quota_reset_credit_consume": True}}
            return detail

        config = {
            **guard.DEFAULT_CONFIG,
            "cpa_base_url": "http://home.internal:8327",
            "reset_credit_grace_enabled": True,
            "reset_credit_auto_consume_enabled": True,
        }
        with (
            mock.patch.object(guard, "request_json", side_effect=request),
            mock.patch.object(guard.time, "time", return_value=now),
        ):
            result = guard.call_home_quota_health(config, {"CPA_MANAGEMENT_KEY": "test-management-key"})

        self.assertTrue(result["reset_credit_consume_supported"])
        with mock.patch.object(guard, "consume_reset_credit", return_value={"status": "ok"}) as consume:
            guard.apply_reset_credit_grace(
                config,
                {"CPA_MANAGEMENT_KEY": "test-management-key"},
                result,
                {},
                now,
                allow_consume=True,
            )

        consume.assert_called_once()
        self.assertEqual("home-pro-credential", consume.call_args.kwargs["credential_id"])
        self.assertEqual(
            detail["reset_credits"]["credits"][0]["expires_at"],
            consume.call_args.kwargs["expected_expires_at"],
        )

    def test_call_quota_health_falls_back_from_cpa_route_to_home(self) -> None:
        expected = {
            "ok": True,
            "quota_health_source": "home_quota_snapshots",
            "accounts": [],
            "reset_credit_consume_supported": False,
        }
        with (
            mock.patch.object(guard, "call_cpa_quota_health", side_effect=RuntimeError("http_404")),
            mock.patch.object(guard, "call_home_quota_health", return_value=expected) as home,
            mock.patch.object(guard, "call_wham_usages") as legacy,
        ):
            result = guard.call_quota_health(guard.DEFAULT_CONFIG, {})

        home.assert_called_once()
        legacy.assert_not_called()
        self.assertEqual("home_quota_snapshots", result["quota_health_source"])
        self.assertIn("cpa=http_404", result["cpa_quota_health_endpoint_error"])


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

    def test_daily_budget_exhaustion_activates_luna_only_reserve(self) -> None:
        self.config.update({
            "daily_budget_model_reserve_percent": 5.0,
            "daily_budget_model_reserve_models": ["gpt-5.6-luna"],
        })
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)

        self.assertTrue(result["quota_ok"])
        self.assertEqual("dynamic_daily_budget_model_reserve_active", result["reason"])
        self.assertEqual(["gpt-5.6-luna"], result["quota_model_allowlist"])
        self.assertEqual(["gpt-5.6-luna"], result["quota_block"]["allowed_models"])
        self.assertAlmostEqual(4.142857, result["usable_balance_units"], places=6)
        self.assertTrue(result["dynamic_daily_budget"]["model_reserve_active"])

    def test_luna_only_reserve_exhaustion_disables_channel(self) -> None:
        self.config.update({
            "daily_budget_model_reserve_percent": 5.0,
            "daily_budget_model_reserve_models": ["gpt-5.6-luna"],
        })
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(82.0), state, self.now + 60)

        self.assertFalse(result["quota_ok"])
        self.assertEqual("dynamic_daily_budget_exhausted", result["reason"])
        self.assertNotIn("quota_model_allowlist", result)
        self.assertFalse(result["dynamic_daily_budget"]["model_reserve_active"])
        self.assertEqual(0.0, result["usable_balance_units"])

    def test_hard_reserve_overrides_luna_only_reserve(self) -> None:
        self.config.update({
            "daily_budget_model_reserve_percent": 5.0,
            "daily_budget_model_reserve_models": ["gpt-5.6-luna"],
        })
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(15.0), state, self.now + 60)

        self.assertFalse(result["quota_ok"])
        self.assertEqual("protected_reserve_reached", result["reason"])
        self.assertNotIn("quota_model_allowlist", result)

    def test_new_guard_day_restores_all_models_after_luna_only_reserve(self) -> None:
        self.config.update({
            "daily_budget_model_reserve_percent": 5.0,
            "daily_budget_model_reserve_models": ["gpt-5.6-luna"],
        })
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        exhausted = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)
        restored = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(86.0), state, self.now + 86_400)

        self.assertTrue(exhausted["dynamic_daily_budget"]["model_reserve_active"])
        self.assertTrue(restored["quota_ok"])
        self.assertFalse(restored["dynamic_daily_budget"]["model_reserve_active"])
        self.assertNotIn("quota_model_allowlist", restored)
        self.assertNotIn("quota_block", restored)

    def test_hard_reserve_stops_at_fifteen_percent(self) -> None:
        state: dict = {}
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(15.0), state, self.now)

        self.assertFalse(result["quota_ok"])
        self.assertEqual("protected_reserve_reached", result["reason"])

    def test_manual_force_unlock_restores_original_quota_until_reset(self) -> None:
        state: dict = {}
        initial = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(100.0),
            state,
            self.now,
        )
        self.config["manual_force_unlock"] = {
            "active": True,
            "until": self.now + 3_600,
            "cycle_signature": initial["dynamic_daily_budget"]["planning_signature"],
        }

        result = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(87.0),
            state,
            self.now + 60,
        )

        self.assertTrue(result["quota_ok"])
        self.assertEqual(87.0, result["usable_balance_units"])
        self.assertEqual("manual_force_unlock_active", result["reason"])
        self.assertTrue(result["manual_force_unlock"]["active"])
        self.assertNotIn("quota_block", result)
        self.assertTrue(result["dynamic_daily_budget"]["daily_exhausted"])

    def test_manual_force_unlock_expires_automatically(self) -> None:
        state: dict = {}
        initial = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(100.0),
            state,
            self.now,
        )
        self.config["manual_force_unlock"] = {
            "active": True,
            "until": self.now + 30,
            "cycle_signature": initial["dynamic_daily_budget"]["planning_signature"],
        }

        result = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(87.0),
            state,
            self.now + 60,
        )

        self.assertFalse(result["quota_ok"])
        self.assertFalse(result["manual_force_unlock"]["active"])
        self.assertEqual("dynamic_daily_budget_exhausted", result["reason"])

    def test_manual_force_unlock_stops_when_official_cycle_changes(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(100.0),
            state,
            self.now,
        )
        self.config["manual_force_unlock"] = {
            "active": True,
            "until": self.now + 3_600,
            "cycle_signature": "different-cycle",
        }

        result = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(87.0),
            state,
            self.now + 60,
        )

        self.assertFalse(result["quota_ok"])
        self.assertFalse(result["manual_force_unlock"]["active"])

    def test_manual_force_unlock_does_not_bypass_real_upstream_exhaustion(self) -> None:
        state: dict = {}
        initial = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(100.0),
            state,
            self.now,
        )
        self.config["manual_force_unlock"] = {
            "active": True,
            "until": self.now + 3_600,
            "cycle_signature": initial["dynamic_daily_budget"]["planning_signature"],
        }
        exhausted = dynamic_budget_result(0.0)
        exhausted["quota_ok"] = False
        exhausted["within_share"] = False
        exhausted["usable_balance_units"] = 0.0
        exhausted["remaining_share_percent"] = 0.0

        result = guard.apply_dynamic_daily_budget(
            self.config,
            exhausted,
            state,
            self.now + 60,
        )

        self.assertFalse(result["quota_ok"])
        self.assertEqual(0.0, result["usable_balance_units"])
        self.assertTrue(result["manual_force_unlock"]["active"])

    def test_manual_force_unlock_requires_explicit_active_flag_and_cycle_signature(self) -> None:
        state: dict = {}
        initial = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(100.0),
            state,
            self.now,
        )
        planning_signature = initial["dynamic_daily_budget"]["planning_signature"]

        for override in (
            {"until": self.now + 3_600, "cycle_signature": planning_signature},
            {"active": True, "until": self.now + 3_600},
        ):
            with self.subTest(override=override):
                self.config["manual_force_unlock"] = override
                result = guard.apply_dynamic_daily_budget(
                    self.config,
                    dynamic_budget_result(87.0),
                    state,
                    self.now + 60,
                )
                self.assertFalse(result["manual_force_unlock"]["active"])
                self.assertFalse(result["quota_ok"])

    def test_five_hour_reserve_uses_same_fifteen_percent_line(self) -> None:
        state: dict = {}
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(80.0, remaining_5h=15.0), state, self.now)

        self.assertFalse(result["quota_ok"])
        self.assertEqual("protected_reserve_reached", result["reason"])

    def test_small_quota_increase_does_not_rebuild_or_reopen_budget(self) -> None:
        self.config["quota_reset_increase_threshold_percent"] = 0.25
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        exhausted = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(88.0), state, self.now + 120)

        self.assertFalse(exhausted["quota_ok"])
        self.assertFalse(result["quota_ok"])
        self.assertEqual(100.0, result["dynamic_daily_budget"]["baseline_remaining_percent"])
        self.assertEqual(13.0, result["dynamic_daily_budget"]["consumed_today_percent"])
        self.assertEqual(5.0, result["dynamic_daily_budget"]["effective_quota_reset_increase_threshold_percent"])
        self.assertTrue(result["dynamic_daily_budget"]["daily_exhausted"])
        self.assertEqual(0.0, result["dynamic_daily_budget"]["remaining_today_percent"])

    def test_weekly_reset_schedule_change_replans_without_reopening_exhausted_budget(self) -> None:
        state: dict = {}
        initial = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(100.0, reset_at=self.now + 7 * 86_400),
            state,
            self.now,
        )
        exhausted = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(87.0, reset_at=self.now + 7 * 86_400),
            state,
            self.now + 60,
        )
        changed_reset_at = self.now + 6 * 86_400
        pending = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(87.0, reset_at=changed_reset_at),
            state,
            self.now + 120,
        )
        replanned = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(87.0, reset_at=changed_reset_at),
            state,
            self.now + 180,
        )

        self.assertEqual("baseline_missing", initial["dynamic_daily_budget"]["baseline_reset_reason"])
        self.assertFalse(exhausted["quota_ok"])
        self.assertFalse(pending["quota_ok"])
        self.assertFalse(replanned["quota_ok"])
        self.assertEqual(100.0, replanned["dynamic_daily_budget"]["baseline_remaining_percent"])
        self.assertEqual(13.0, replanned["dynamic_daily_budget"]["consumed_today_percent"])
        self.assertTrue(replanned["dynamic_daily_budget"]["daily_exhausted"])
        self.assertEqual(
            "weekly_reset_schedule_changed",
            replanned["dynamic_daily_budget"]["last_replan_reason"],
        )
        self.assertNotEqual(
            "weekly_reset_schedule_changed",
            replanned["dynamic_daily_budget"]["baseline_reset_reason"],
        )

    def test_countdown_reset_jitter_is_stabilized(self) -> None:
        state: dict = {}
        first_result = dynamic_budget_result(100.0)
        first_weekly = first_result["accounts"][0]["windows"]["7d"]
        first_weekly.pop("reset_at")
        first_weekly["reset_after_seconds"] = 7 * 86_400
        first = guard.apply_dynamic_daily_budget(self.config, first_result, state, self.now)

        jittered_result = dynamic_budget_result(99.0)
        jittered_weekly = jittered_result["accounts"][0]["windows"]["7d"]
        jittered_weekly.pop("reset_at")
        jittered_weekly["reset_after_seconds"] = 7 * 86_400 - 58
        second = guard.apply_dynamic_daily_budget(self.config, jittered_result, state, self.now + 60)

        self.assertEqual(
            first["dynamic_daily_budget"]["weekly_signature"],
            second["dynamic_daily_budget"]["observed_weekly_signature"],
        )
        self.assertNotIn("reset_candidate", second["dynamic_daily_budget"])
        self.assertEqual(
            "stabilized_within_tolerance",
            second["dynamic_daily_budget"]["account_plans"][0]["weekly_reset_observation"],
        )

    def test_exhausted_budget_exposes_next_midnight_retry(self) -> None:
        state: dict = {}
        guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(100.0), state, self.now)
        result = guard.apply_dynamic_daily_budget(self.config, dynamic_budget_result(87.0), state, self.now + 60)
        expected_retry_at = guard.next_guard_day_start(self.config, self.now + 60)

        self.assertEqual(429, result["quota_block"]["http_status"])
        self.assertEqual("channel_daily_protected_budget_exhausted", result["quota_block"]["code"])
        self.assertEqual(expected_retry_at, result["quota_block"]["retry_at"])
        self.assertGreater(result["quota_block"]["retry_after_seconds"], 0)

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

    def test_legacy_budget_state_rebuilds_planning_metadata_once(self) -> None:
        day = guard.dt.datetime.fromtimestamp(self.now, guard.dt.timezone.utc).date().isoformat()
        state = {
            "dynamic_daily_budget": {
                "day": day,
                "account_signature": "legacy-signature",
                "reset_at": self.now + 7 * 86_400,
                "baseline_remaining_percent": 94.0,
                "daily_limit_percent": 79.0 / 7.0,
                "minimum_remaining_percent_seen": 90.0,
                "last_remaining_percent": 90.0,
            }
        }

        result = guard.apply_dynamic_daily_budget(
            self.config,
            dynamic_budget_result(90.0),
            state,
            self.now,
        )

        budget = result["dynamic_daily_budget"]
        self.assertEqual("planning_metadata_missing", budget["baseline_reset_reason"])
        self.assertEqual(90.0, budget["baseline_remaining_percent"])
        self.assertAlmostEqual(75.0 / 7.0, budget["daily_limit_percent"], places=6)
        self.assertEqual(budget["planning_signature"], budget["observed_planning_signature"])

    def test_reset_credit_before_weekly_reset_shortens_effective_horizon(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        state: dict = {}

        result = guard.apply_dynamic_daily_budget(
            self.config,
            reset_credit_result(self.now, 3 * 86_400, remaining_percent=100.0),
            state,
            self.now,
        )

        budget = result["dynamic_daily_budget"]
        self.assertAlmostEqual(85.0 / 3.0, budget["daily_limit_percent"], places=6)
        self.assertEqual("reset_credit", budget["effective_reset_source"])
        self.assertEqual(3, budget["account_plans"][0]["days_remaining"])
        self.assertLess(
            budget["account_plans"][0]["effective_reset_at"],
            budget["account_plans"][0]["weekly_reset_at"],
        )

    def test_reset_credit_after_weekly_reset_keeps_weekly_horizon(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        state: dict = {}

        result = guard.apply_dynamic_daily_budget(
            self.config,
            reset_credit_result(self.now, 8 * 86_400, remaining_percent=100.0),
            state,
            self.now,
        )

        budget = result["dynamic_daily_budget"]
        self.assertAlmostEqual(85.0 / 7.0, budget["daily_limit_percent"], places=6)
        self.assertEqual("weekly", budget["effective_reset_source"])
        self.assertEqual(7, budget["account_plans"][0]["days_remaining"])

    def test_missing_reset_credit_keeps_weekly_horizon(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        state: dict = {}
        result_without_credit = reset_credit_result(
            self.now,
            3 * 86_400,
            remaining_percent=100.0,
            available_count=0,
            include_credit=False,
        )

        result = guard.apply_dynamic_daily_budget(
            self.config,
            result_without_credit,
            state,
            self.now,
        )

        budget = result["dynamic_daily_budget"]
        self.assertAlmostEqual(85.0 / 7.0, budget["daily_limit_percent"], places=6)
        self.assertEqual("weekly", budget["effective_reset_source"])

    def test_reset_credit_for_unselected_plan_is_ignored(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        state: dict = {}
        plus_result = reset_credit_result(self.now, 3 * 86_400, remaining_percent=100.0)
        plus_result["accounts"][0]["plan_type"] = "plus"

        result = guard.apply_dynamic_daily_budget(self.config, plus_result, state, self.now)

        budget = result["dynamic_daily_budget"]
        self.assertAlmostEqual(85.0 / 7.0, budget["daily_limit_percent"], places=6)
        self.assertEqual("weekly", budget["effective_reset_source"])

    def test_reset_credit_is_ignored_for_independent_quota_feature(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        self.config["quota_feature"] = "codex_bengalfox"
        state: dict = {}

        result = guard.apply_dynamic_daily_budget(
            self.config,
            reset_credit_result(self.now, 3 * 86_400, remaining_percent=100.0),
            state,
            self.now,
        )

        budget = result["dynamic_daily_budget"]
        self.assertAlmostEqual(85.0 / 7.0, budget["daily_limit_percent"], places=6)
        self.assertEqual("weekly", budget["effective_reset_source"])

    def test_multiple_accounts_use_independent_effective_horizons(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        state: dict = {}
        three_day = reset_credit_result(self.now, 3 * 86_400, remaining_percent=100.0)
        seven_day = reset_credit_result(
            self.now,
            8 * 86_400,
            remaining_percent=80.0,
        )
        seven_day_account = seven_day["accounts"][0]
        seven_day_account["auth_index"] = "pro-auth-index-b"
        seven_day_account["account_id_hash"] = "pro-account-hash-b"
        combined = combined_dynamic_budget_result(three_day, seven_day)

        result = guard.apply_dynamic_daily_budget(self.config, combined, state, self.now)

        budget = result["dynamic_daily_budget"]
        expected = (100.0 - 15.0) / 3.0 + (80.0 - 15.0) / 7.0
        self.assertAlmostEqual(expected, budget["daily_limit_percent"], places=6)
        plans = {plan["account_key"]: plan for plan in budget["account_plans"]}
        self.assertEqual(3, plans["pro-account-hash"]["days_remaining"])
        self.assertEqual(7, plans["pro-account-hash-b"]["days_remaining"])

    def test_new_reset_credit_plan_requires_confirmation_before_rebaseline(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        state: dict = {}
        no_credit = reset_credit_result(
            self.now,
            3 * 86_400,
            remaining_percent=100.0,
            available_count=0,
            include_credit=False,
        )
        with_credit = reset_credit_result(self.now, 3 * 86_400, remaining_percent=100.0)
        guard.apply_dynamic_daily_budget(self.config, no_credit, state, self.now)

        pending = guard.apply_dynamic_daily_budget(self.config, with_credit, state, self.now + 60)
        pending_daily_limit = pending["dynamic_daily_budget"]["daily_limit_percent"]
        pending_candidate_count = pending["dynamic_daily_budget"]["reset_candidate"]["count"]
        degraded = reset_credit_result(
            self.now,
            3 * 86_400,
            remaining_percent=100.0,
            available_count=0,
            include_credit=False,
        )
        degraded["accounts"][0]["reset_credits_error"] = "temporary upstream failure"
        degraded_result = guard.apply_dynamic_daily_budget(self.config, degraded, state, self.now + 120)
        degraded_candidate_count = degraded_result["dynamic_daily_budget"]["reset_candidate"]["count"]
        degraded_daily_limit = degraded_result["dynamic_daily_budget"]["daily_limit_percent"]
        confirmed = guard.apply_dynamic_daily_budget(self.config, with_credit, state, self.now + 180)

        self.assertAlmostEqual(85.0 / 7.0, pending_daily_limit, places=6)
        self.assertEqual(1, pending_candidate_count)
        self.assertEqual(1, degraded_candidate_count)
        self.assertAlmostEqual(85.0 / 7.0, degraded_daily_limit, places=6)
        self.assertAlmostEqual(85.0 / 3.0, confirmed["dynamic_daily_budget"]["daily_limit_percent"], places=6)
        self.assertEqual("baseline_missing", confirmed["dynamic_daily_budget"]["baseline_reset_reason"])
        self.assertEqual(
            "reset_credit_schedule_changed",
            confirmed["dynamic_daily_budget"]["last_replan_reason"],
        )

    def test_transient_reset_credit_probe_failure_keeps_effective_plan(self) -> None:
        self.config["reset_credit_grace_enabled"] = True
        state: dict = {}
        initial = reset_credit_result(self.now, 3 * 86_400, remaining_percent=100.0)
        first = guard.apply_dynamic_daily_budget(self.config, initial, state, self.now)
        baseline_signature = first["dynamic_daily_budget"]["planning_signature"]
        baseline_daily_limit = first["dynamic_daily_budget"]["daily_limit_percent"]
        transient = reset_credit_result(
            self.now,
            3 * 86_400,
            remaining_percent=100.0,
            available_count=0,
            include_credit=False,
        )
        transient_account = transient["accounts"][0]
        transient_account["reset_credits_available"] = None
        transient_account["reset_credits_error"] = "temporary upstream failure"

        second = guard.apply_dynamic_daily_budget(self.config, transient, state, self.now + 60)
        fallback = reset_credit_result(
            self.now,
            3 * 86_400,
            remaining_percent=100.0,
            available_count=0,
            include_credit=False,
        )
        fallback["quota_health_source"] = "python_guard_fallback"
        fallback["quota_health_endpoint_error"] = "temporary management endpoint failure"
        third = guard.apply_dynamic_daily_budget(self.config, fallback, state, self.now + 120)

        self.assertEqual(
            baseline_signature,
            second["dynamic_daily_budget"]["observed_planning_signature"],
        )
        self.assertEqual(baseline_daily_limit, second["dynamic_daily_budget"]["daily_limit_percent"])
        self.assertEqual(1, second["dynamic_daily_budget"]["credit_probe_degraded_account_count"])
        self.assertEqual("cached", second["dynamic_daily_budget"]["account_plans"][0]["reset_credit_observation"])
        self.assertEqual(baseline_signature, third["dynamic_daily_budget"]["observed_planning_signature"])
        self.assertEqual(baseline_daily_limit, third["dynamic_daily_budget"]["daily_limit_percent"])
        self.assertEqual(1, third["dynamic_daily_budget"]["credit_probe_degraded_account_count"])
        self.assertEqual("cached", third["dynamic_daily_budget"]["account_plans"][0]["reset_credit_observation"])


class ResetCreditGraceTest(unittest.TestCase):
    def setUp(self) -> None:
        self.config = dict(guard.DEFAULT_CONFIG)
        self.config.update({
            "dynamic_daily_budget_enabled": True,
            "reset_credit_grace_enabled": True,
            "reset_credit_auto_consume_enabled": True,
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
            first = guard.apply_reset_credit_grace(
                self.config, self.env, first_result, state, self.now, allow_consume=True
            )
            second = guard.apply_reset_credit_grace(
                self.config, self.env, second_result, state, self.now + 60, allow_consume=True
            )

        self.assertEqual(1, first["reset_credit_grace"]["consume_error_count"])
        self.assertEqual(1, second["reset_credit_grace"]["consume_success_count"])
        self.assertEqual(2, consume.call_count)
        self.assertEqual(consume.call_args_list[0].args[3], consume.call_args_list[1].args[3])

    def test_auto_reset_triggers_early_when_quota_is_nearly_exhausted(self) -> None:
        state: dict = {}
        result = reset_credit_result(self.now, 6 * 60 * 60, remaining_percent=0.5)

        with mock.patch.object(guard, "consume_reset_credit", return_value={"status": "ok"}) as consume:
            guarded = guard.apply_reset_credit_grace(
                self.config, self.env, result, state, self.now, allow_consume=True
            )

        consume.assert_called_once()
        self.assertEqual("quota_near_exhaustion", guarded["reset_credit_grace"]["accounts"][0]["auto_reset_reason"])

    def test_consume_capability_defaults_to_off(self) -> None:
        state: dict = {}
        result = reset_credit_result(self.now, 5 * 60)

        with mock.patch.object(guard, "consume_reset_credit") as consume:
            guarded = guard.apply_reset_credit_grace(self.config, self.env, result, state, self.now)

        consume.assert_not_called()
        self.assertEqual("unsupported", guarded["reset_credit_grace"]["auto_consume_blocked_reason"])
        self.assertTrue(guarded["reset_credit_grace"]["limits_released"])

    def test_auto_consume_switch_defaults_to_off(self) -> None:
        state: dict = {}
        config = dict(self.config)
        config["reset_credit_auto_consume_enabled"] = False
        result = reset_credit_result(self.now, 5 * 60)

        with mock.patch.object(guard, "consume_reset_credit") as consume:
            guarded = guard.apply_reset_credit_grace(config, self.env, result, state, self.now)

        consume.assert_not_called()
        self.assertEqual("disabled", guarded["reset_credit_grace"]["auto_consume_blocked_reason"])
        self.assertTrue(guarded["reset_credit_grace"]["limits_released"])

    def test_official_or_manual_refill_prevents_later_auto_consume(self) -> None:
        state: dict = {}
        guard.apply_reset_credit_grace(
            self.config,
            self.env,
            reset_credit_result(self.now, 12 * 60 * 60, remaining_percent=40.0, reset_at=1_800_604_800),
            state,
            self.now,
        )
        refilled = reset_credit_result(
            self.now,
            12 * 60 * 60,
            remaining_percent=100.0,
            reset_at=1_801_209_600,
        )

        with mock.patch.object(guard, "consume_reset_credit") as consume:
            guarded = guard.apply_reset_credit_grace(
                self.config,
                self.env,
                refilled,
                state,
                self.now + 60,
            )

        consume.assert_not_called()
        self.assertEqual(1, guarded["reset_credit_grace"]["manual_reset_count"])
        self.assertFalse(guarded["reset_credit_grace"]["active"])

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
        self.assertEqual("weekly", reset_result["dynamic_daily_budget"]["effective_reset_source"])
        self.assertEqual(
            reset_result["dynamic_daily_budget"]["observed_planning_signature"],
            reset_result["dynamic_daily_budget"]["planning_signature"],
        )

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
