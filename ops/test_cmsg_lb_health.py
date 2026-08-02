#!/usr/bin/env python3

from __future__ import annotations

import datetime as dt
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest import mock


MODULE_PATH = Path(__file__).with_name("cmsg_lb_health.py")
SPEC = importlib.util.spec_from_file_location("cmsg_lb_health", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
HEALTH = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HEALTH)


NOW = dt.datetime(2026, 8, 2, 10, 0, tzinfo=dt.timezone.utc)


def sample(
    minute: int,
    *,
    head_ok: bool = True,
    speed_ok: bool = True,
    retrans: int = 5,
    sent: int = 1000,
    new_api_5xx: int = 0,
    quota_429: int = 0,
    cpa_failures: int = 0,
    mihomo_failures: int = 0,
) -> dict[str, Any]:
    timestamp = NOW - dt.timedelta(minutes=minute)
    code = 200 if head_ok else 0
    return {
        "schema_version": 2,
        "site_id": "test",
        "ts_utc": timestamp.isoformat(),
        "head": [
            {
                "url": "https://mirrors.tuna.tsinghua.edu.cn/",
                "diagnostic_only": False,
                "ok": head_ok,
                "http_code": code,
                "ttfb": 0.2,
            },
            {
                "url": "https://www.cloudflare.com/",
                "diagnostic_only": False,
                "ok": head_ok,
                "http_code": code,
                "ttfb": 0.4,
            },
            {
                "url": "https://chatgpt.com/",
                "diagnostic_only": True,
                "ok": False,
                "http_code": 0,
                "ttfb": 0.0,
            },
        ],
        "speed_test": {"ok": speed_ok, "http_code": 200 if speed_ok else 0, "ttfb": 0.5},
        "logs": {
            "new_api": {"ok": True},
            "cpa": {"ok": True},
            "mihomo": {"ok": True},
        },
        "signals": {
            "new_api_network_5xx": new_api_5xx,
            "new_api_quota_429": quota_429,
            "cpa_transport_failures": cpa_failures,
            "mihomo_connect_failures": mihomo_failures,
            "network_error_observed": bool(new_api_5xx or cpa_failures or mihomo_failures),
        },
        "new_api_log_counts": {"success_consume": 100},
        "nstat_delta": {"TcpOutSegs": sent, "TcpRetransSegs": retrans},
        "mihomo": {"nodes": [{"ok": True, "delay": 300}]},
    }


class LBHealthTest(unittest.TestCase):
    def config(self, root: Path) -> dict[str, Any]:
        return HEALTH.deep_merge(
            HEALTH.DEFAULT_CONFIG,
            {
                "site_id": "test",
                "probe_path": str(root / "probe.jsonl"),
                "state_path": str(root / "state.json"),
                "freshness_sec": 720,
                "lookback_sec": 1800,
                "hysteresis": {"fail_after": 2, "recover_after": 3, "recovery_cooldown_sec": 0},
            },
        )

    def write_records(self, root: Path, records: list[dict[str, Any]]) -> None:
        with (root / "probe.jsonl").open("w", encoding="utf-8") as handle:
            for record in records:
                handle.write(json.dumps(record) + "\n")

    def test_healthy_signal_ignores_diagnostic_chatgpt_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.write_records(root, [sample(20), sample(15), sample(10), sample(5)])
            status = HEALTH.evaluate(self.config(root), NOW)
        self.assertTrue(status["lb_healthy"])
        self.assertEqual([], status["reasons"])
        self.assertEqual(4000, status["metrics"]["tcp"]["out_segments"])
        self.assertEqual(0.5, status["metrics"]["tcp"]["retrans_percent"])

    def test_quota_429_is_reported_but_not_a_network_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.write_records(root, [sample(20, quota_429=20), sample(15), sample(10), sample(5)])
            status = HEALTH.evaluate(self.config(root), NOW)
        self.assertTrue(status["lb_healthy"])
        self.assertEqual(20, status["metrics"]["errors"]["new_api_quota_429"])
        self.assertNotIn("new_api_network_5xx_high", status["reasons"])

    def test_transport_failures_make_raw_signal_unhealthy(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            records = [sample(20, cpa_failures=3), sample(15), sample(10), sample(5)]
            self.write_records(root, records)
            status = HEALTH.evaluate(self.config(root), NOW)
        self.assertFalse(status["raw_healthy"])
        self.assertIn("cpa_transport_failures_high", status["reasons"])

    def test_tcp_rate_uses_total_segments_not_window_average(self) -> None:
        records = [sample(20, sent=100, retrans=10), sample(15, sent=9900, retrans=0)]
        metrics = HEALTH.collect_metrics(records, HEALTH.DEFAULT_CONFIG["required_head_hosts"])
        self.assertEqual(10000, metrics["tcp"]["out_segments"])
        self.assertEqual(10, metrics["tcp"]["retrans_segments"])
        self.assertEqual(0.1, metrics["tcp"]["retrans_percent"])

    def test_stale_probe_is_hard_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.write_records(root, [sample(60), sample(55), sample(50), sample(45)])
            status = HEALTH.evaluate(self.config(root), NOW)
        self.assertFalse(status["lb_healthy"])
        self.assertIn("probe_stale", status["reasons"])

    def test_eligibility_gate_blocks_healthy_network(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.write_records(root, [sample(20), sample(15), sample(10), sample(5)])
            eligibility = root / "eligibility.json"
            eligibility.write_text(
                json.dumps({"ready": False, "reason": "postgres_standby_not_promoted"}),
                encoding="utf-8",
            )
            config = self.config(root)
            config["eligibility"] = {"required": True, "path": str(eligibility), "max_age_sec": 0}
            status = HEALTH.evaluate(config, NOW)
        self.assertTrue(status["effective_network_healthy"])
        self.assertFalse(status["lb_healthy"])
        self.assertIn("data_plane_postgres_standby_not_promoted", status["reasons"])

    def test_hysteresis_advances_once_per_probe(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            state = root / "state.json"
            config = self.config(root)
            first = HEALTH.apply_hysteresis(True, "p1", state, config, NOW)
            bad_once = HEALTH.apply_hysteresis(False, "p2", state, config, NOW + dt.timedelta(minutes=5))
            repeated = HEALTH.apply_hysteresis(False, "p2", state, config, NOW + dt.timedelta(minutes=6))
            bad_twice = HEALTH.apply_hysteresis(False, "p3", state, config, NOW + dt.timedelta(minutes=10))
        self.assertTrue(first["effective_healthy"])
        self.assertTrue(bad_once["effective_healthy"])
        self.assertEqual(1, bad_once["consecutive_bad"])
        self.assertEqual(1, repeated["consecutive_bad"])
        self.assertFalse(bad_twice["effective_healthy"])

    def test_recovery_requires_three_new_good_probes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            state = root / "state.json"
            config = self.config(root)
            HEALTH.apply_hysteresis(False, "p1", state, config, NOW)
            one = HEALTH.apply_hysteresis(True, "p2", state, config, NOW + dt.timedelta(minutes=5))
            two = HEALTH.apply_hysteresis(True, "p3", state, config, NOW + dt.timedelta(minutes=10))
            three = HEALTH.apply_hysteresis(True, "p4", state, config, NOW + dt.timedelta(minutes=15))
        self.assertFalse(one["effective_healthy"])
        self.assertFalse(two["effective_healthy"])
        self.assertTrue(three["effective_healthy"])

    def test_public_payload_does_not_include_paths_or_raw_metrics(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.write_records(root, [sample(20), sample(15), sample(10), sample(5)])
            payload = HEALTH.public_payload(HEALTH.evaluate(self.config(root), NOW))
        encoded = json.dumps(payload)
        self.assertNotIn("probe_path", encoded)
        self.assertNotIn("metrics", payload)
        self.assertNotIn("raw", encoded.lower())

    @mock.patch.object(HEALTH.subprocess, "run")
    def test_runtime_container_health_requires_running_and_healthy(self, run: mock.Mock) -> None:
        run.side_effect = [
            mock.Mock(returncode=0, stdout=json.dumps({"Running": True, "Health": {"Status": "healthy"}})),
            mock.Mock(returncode=0, stdout=json.dumps({"Running": False})),
        ]
        config = HEALTH.deep_merge(
            HEALTH.DEFAULT_CONFIG,
            {"runtime": {"required_containers": ["new-api", "postgres"]}},
        )
        runtime = HEALTH.collect_runtime_health(config)
        self.assertFalse(runtime["ready"])
        self.assertTrue(runtime["containers"]["new-api"]["ready"])
        self.assertFalse(runtime["containers"]["postgres"]["ready"])

    def test_probe_reader_keeps_only_requested_tail(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "probe.jsonl"
            with path.open("w", encoding="utf-8") as handle:
                for index in range(12):
                    handle.write(json.dumps({"id": index, "ts_utc": (NOW + dt.timedelta(seconds=index)).isoformat()}) + "\n")
            records, invalid = HEALTH.read_probe_records(path, 5)
        self.assertEqual(0, invalid)
        self.assertEqual([7, 8, 9, 10, 11], [record["id"] for record in records])


if __name__ == "__main__":
    unittest.main()
