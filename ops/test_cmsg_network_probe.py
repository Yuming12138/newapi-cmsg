#!/usr/bin/env python3

from __future__ import annotations

import datetime as dt
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("cmsg_network_probe.py")
SPEC = importlib.util.spec_from_file_location("cmsg_network_probe", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
PROBE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROBE)


class NetworkProbeTest(unittest.TestCase):
    def test_normalize_config_maps_legacy_docker_logs(self) -> None:
        config = PROBE.normalize_config({"docker_logs": {"enabled": False, "tail": 123}})
        self.assertFalse(config["new_api_logs"]["enabled"])
        self.assertEqual(123, config["new_api_logs"]["tail"])
        self.assertIn("network_5xx", config["new_api_logs"]["patterns"])

    def test_delta_counters_skips_reset_and_builds_rates(self) -> None:
        current = {"TcpOutSegs": 1200, "TcpRetransSegs": 15, "TcpInSegs": 10}
        previous = {"nstat": {"TcpOutSegs": 1000, "TcpRetransSegs": 10, "TcpInSegs": 20}}
        delta, resets = PROBE.delta_counters(current, previous)
        self.assertEqual(200, delta["TcpOutSegs"])
        self.assertEqual(5, delta["TcpRetransSegs"])
        self.assertEqual(["TcpInSegs"], resets)
        rates = PROBE.nstat_rates(delta)
        self.assertEqual(0.025, rates["tcp_retrans_rate"])
        self.assertEqual(2.5, rates["tcp_retrans_percent"])

    def test_resolve_log_window_uses_state_without_overlap(self) -> None:
        now = dt.datetime(2026, 7, 30, 10, 0, tzinfo=dt.timezone.utc)
        since, until, meta = PROBE.resolve_log_window(
            now,
            {"ts": "2026-07-30T09:55:00+00:00"},
            {"log_window": {"fallback_sec": 360, "max_lookback_sec": 900}},
        )
        self.assertEqual(dt.datetime(2026, 7, 30, 9, 55, tzinfo=dt.timezone.utc), since)
        self.assertEqual(now, until)
        self.assertEqual(300.0, meta["seconds"])
        self.assertFalse(meta["overlap_expected"])
        self.assertFalse(meta["truncated"])

    def test_resolve_log_window_caps_stale_state(self) -> None:
        now = dt.datetime(2026, 7, 30, 10, 0, tzinfo=dt.timezone.utc)
        since, _, meta = PROBE.resolve_log_window(
            now,
            {"ts": "2026-07-30T08:00:00+00:00"},
            {"log_window": {"fallback_sec": 360, "max_lookback_sec": 900}},
        )
        self.assertEqual(dt.datetime(2026, 7, 30, 9, 45, tzinfo=dt.timezone.utc), since)
        self.assertTrue(meta["truncated"])

    def test_pattern_counts_are_line_based(self) -> None:
        lines = [
            "channel error (status code: 429): usage limit has been reached",
            "channel error (status code: 500): upstream error: do request failed",
        ]
        counts = PROBE.count_pattern_lines(lines, PROBE.DEFAULT_CONFIG["new_api_logs"]["patterns"])
        self.assertEqual(1, counts["quota_429"])
        self.assertEqual(1, counts["network_5xx"])
        self.assertEqual(2, counts["channel_error_total"])
        self.assertEqual(2, counts["channel_error"])
        self.assertEqual(1, counts["usage_limit"])

    def test_cpa_log_snapshot_filters_utc_window_and_extracts_nodes(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "main.log"
            path.write_text(
                "\n".join(
                    [
                        "[2026-07-30 09:54:59] [old] [warn ] failure observed failure_class=h2_protocol_error selected_node=old selected_node_source=snapshot",
                        "[2026-07-30 09:56:00] [new] [warn ] failure observed failure_class=h2_protocol_error selected_node=🇸🇬 新加坡1 selected_node_source=snapshot",
                        "[2026-07-30 09:56:01] [new] [warn ] stream execution failed unexpected EOF",
                    ]
                )
                + "\n",
                encoding="utf-8",
            )
            cfg = dict(PROBE.DEFAULT_CONFIG["cpa_logs"])
            cfg["path"] = str(path)
            snapshot = PROBE.collect_cpa_log_snapshot(
                cfg,
                dt.datetime(2026, 7, 30, 9, 55, tzinfo=dt.timezone.utc),
                dt.datetime(2026, 7, 30, 10, 0, tzinfo=dt.timezone.utc),
            )
        self.assertTrue(snapshot["ok"])
        self.assertEqual(1, snapshot["counts"]["h2_protocol_error"])
        self.assertEqual(1, snapshot["counts"]["unexpected_eof"])
        self.assertEqual({"🇸🇬 新加坡1": 1}, snapshot["failure_nodes"])

    def test_cpa_log_snapshot_honors_configured_local_timezone(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "main.log"
            path.write_text(
                "[2026-07-30 17:56:00] [local] [warn ] "
                "failure observed failure_class=h2_protocol_error "
                "selected_node=campus selected_node_source=snapshot\n",
                encoding="utf-8",
            )
            cfg = dict(PROBE.DEFAULT_CONFIG["cpa_logs"])
            cfg["path"] = str(path)
            cfg["timezone"] = "Asia/Shanghai"
            snapshot = PROBE.collect_cpa_log_snapshot(
                cfg,
                dt.datetime(2026, 7, 30, 9, 55, tzinfo=dt.timezone.utc),
                dt.datetime(2026, 7, 30, 10, 0, tzinfo=dt.timezone.utc),
            )
        self.assertEqual(1, snapshot["counts"]["h2_protocol_error"])

    @mock.patch.object(PROBE, "run_cmd")
    def test_docker_log_snapshot_returns_counts_without_raw_logs(self, run_cmd: mock.Mock) -> None:
        run_cmd.return_value = (
            0,
            "channel error (status code: 429): usage limit has been reached\n",
            "",
            0.1,
        )
        cfg = dict(PROBE.DEFAULT_CONFIG)
        log_cfg = PROBE.DEFAULT_CONFIG["new_api_logs"]
        snapshot, lines = PROBE.collect_docker_log_snapshot(
            cfg,
            log_cfg,
            dt.datetime(2026, 7, 30, 9, 55, tzinfo=dt.timezone.utc),
            dt.datetime(2026, 7, 30, 10, 0, tzinfo=dt.timezone.utc),
        )
        self.assertTrue(snapshot["ok"])
        self.assertEqual(1, snapshot["counts"]["quota_429"])
        self.assertEqual(1, len(lines))
        self.assertFalse(snapshot["tail_limit_reached"])
        self.assertNotIn("raw", snapshot)
        command = run_cmd.call_args.args[0]
        self.assertIn("--since", command)
        self.assertIn("--until", command)

    def test_signal_summary_keeps_quota_separate(self) -> None:
        logs = {
            "new_api": {"counts": {"network_5xx": 1, "quota_429": 4}},
            "cpa": {"counts": {"h2_protocol_error": 2, "terminal_protocol_error": 2}},
            "mihomo": {"counts": {"connect_io_timeout": 3}},
        }
        signals = PROBE.build_signal_summary(logs)
        self.assertEqual(1, signals["new_api_network_5xx"])
        self.assertEqual(4, signals["new_api_quota_429"])
        self.assertEqual(0, signals["new_api_stream_failures"])
        self.assertEqual(2, signals["cpa_transport_failures"])
        self.assertEqual(2, signals["upstream_transport_failures"])
        self.assertEqual(3, signals["mihomo_connect_failures"])
        self.assertTrue(signals["network_error_observed"])

    def test_signal_summary_counts_terminal_protocol_error_without_shadow_event(self) -> None:
        logs = {
            "new_api": {"counts": {}},
            "cpa": {"counts": {"h2_protocol_error": 0, "terminal_protocol_error": 2}},
            "mihomo": {"counts": {}},
        }
        signals = PROBE.build_signal_summary(logs)
        self.assertEqual(2, signals["cpa_transport_failures"])
        self.assertEqual(2, signals["upstream_transport_failures"])
        self.assertTrue(signals["network_error_observed"])

    def test_signal_summary_uses_new_api_stream_errors_as_transport_fallback(self) -> None:
        logs = {
            "new_api": {"counts": {"protocol_error": 2, "unexpected_eof": 1}},
            "cpa": {"counts": {}},
            "mihomo": {"counts": {}},
        }
        signals = PROBE.build_signal_summary(logs)
        self.assertEqual(3, signals["new_api_stream_failures"])
        self.assertEqual(0, signals["cpa_transport_failures"])
        self.assertEqual(3, signals["upstream_transport_failures"])
        self.assertTrue(signals["network_error_observed"])


if __name__ == "__main__":
    unittest.main()
