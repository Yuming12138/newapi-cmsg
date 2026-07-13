#!/usr/bin/env python3

import importlib.util
import unittest
from pathlib import Path


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
        self.assertEqual(58.0, pro["usable_balance_units"])


if __name__ == "__main__":
    unittest.main()
