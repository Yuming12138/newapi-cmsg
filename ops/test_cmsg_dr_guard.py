#!/usr/bin/env python3

from __future__ import annotations

import contextlib
import datetime as dt
import importlib.util
import io
import json
import os
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("cmsg_dr_guard.py")
SPEC = importlib.util.spec_from_file_location("cmsg_dr_guard", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
GUARD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GUARD)

NOW = dt.datetime(2026, 8, 2, 12, 0, tzinfo=dt.timezone.utc)
FAKE_SQL_SECRET = "fake-sql-secret-never-print"
FAKE_REDIS_SECRET = "fake-redis-secret-never-print"


def env_text(sql_host: str = "10.203.66.1", redis_host: str = "10.203.66.1") -> str:
    return (
        f"SQL_DSN=postgresql://newapi:{FAKE_SQL_SECRET}@{sql_host}:5432/newapi?sslmode=disable\n"
        f"REDIS_CONN_STRING=redis://:{FAKE_REDIS_SECRET}@{redis_host}:6379/0\n"
        "NODE_TYPE=slave\n"
    )


def valid_evidence(checked_at: dt.datetime = NOW) -> dict[str, object]:
    return {
        "schema_version": 1,
        "site_id": "campus",
        "checked_at": checked_at.isoformat(),
        "postgres": {"in_recovery": False},
        "redis": {"role": "master"},
        "new_api": {
            "healthy": True,
            "sql_host": "postgres-standby",
            "redis_host": "redis-standby",
        },
        "write_probe": {"ok": True, "request_id": "req-test", "db_log_delta": 1},
    }


class DrGuardTest(unittest.TestCase):
    def write_env(self, root: Path, text: str | None = None) -> Path:
        path = root / "new-api-failover.env"
        path.write_text(text or env_text(), encoding="utf-8")
        os.chmod(path, 0o600)
        return path

    def test_inspect_env_never_prints_credentials(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = self.write_env(Path(temporary))
            result = GUARD.inspect_env(path)
        encoded = json.dumps(result)
        self.assertEqual("10.203.66.1", result["endpoints"][GUARD.SQL_KEY]["host"])
        self.assertNotIn(FAKE_SQL_SECRET, encoded)
        self.assertNotIn(FAKE_REDIS_SECRET, encoded)

    def test_rewrite_changes_only_hosts_and_preserves_secrets(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            path = self.write_env(root)
            result = GUARD.rewrite_env(
                path,
                expected_sql_host="10.203.66.1",
                expected_redis_host="10.203.66.1",
                sql_host="postgres-standby",
                sql_port=5432,
                redis_host="redis-standby",
                redis_port=6379,
                backup_dir=root / "backups",
                dry_run=False,
            )
            rewritten = path.read_text(encoding="utf-8")
            backup = Path(result["backup"])
            backup_text = backup.read_text(encoding="utf-8")
            backup_mode = backup.stat().st_mode & 0o777
            output = json.dumps(result)
        self.assertIn(f":{FAKE_SQL_SECRET}@postgres-standby:5432", rewritten)
        self.assertIn(f":{FAKE_REDIS_SECRET}@redis-standby:6379", rewritten)
        self.assertIn("10.203.66.1", backup_text)
        self.assertEqual(0o600, backup_mode)
        self.assertNotIn(FAKE_SQL_SECRET, output)
        self.assertNotIn(FAKE_REDIS_SECRET, output)

    def test_expected_host_fence_prevents_mutation(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            path = self.write_env(root, env_text(sql_host="unexpected.example"))
            before = path.read_bytes()
            with self.assertRaises(GUARD.GuardError):
                GUARD.rewrite_env(
                    path,
                    expected_sql_host="10.203.66.1",
                    expected_redis_host="10.203.66.1",
                    sql_host="postgres-standby",
                    sql_port=5432,
                    redis_host="redis-standby",
                    redis_port=6379,
                    backup_dir=root / "backups",
                    dry_run=False,
                )
            self.assertEqual(before, path.read_bytes())
            self.assertFalse((root / "backups").exists())

    def test_ready_gate_requires_fresh_complete_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            evidence_path = root / "evidence.json"
            evidence_path.write_text(json.dumps(valid_evidence()), encoding="utf-8")
            gate = root / "eligibility.json"
            result = GUARD.set_eligibility_ready(
                gate,
                "campus_data_plane_promoted_and_write_verified",
                evidence_path,
                max_age_sec=900,
                now=NOW + dt.timedelta(minutes=5),
                dry_run=False,
            )
            value = json.loads(gate.read_text(encoding="utf-8"))
            gate_mode = gate.stat().st_mode & 0o777
        self.assertTrue(value["ready"])
        self.assertEqual("campus_local_promoted", value["mode"])
        self.assertEqual(1, result["evidence_summary"]["db_log_delta"])
        self.assertEqual(0o600, gate_mode)

    def test_stale_or_non_writing_evidence_cannot_open_gate(self) -> None:
        stale = valid_evidence(NOW - dt.timedelta(hours=1))
        with self.assertRaises(GUARD.GuardError):
            GUARD.validate_ready_evidence(stale, max_age_sec=900, now=NOW)
        no_write = valid_evidence()
        no_write["write_probe"] = {"ok": True, "request_id": "req-test", "db_log_delta": 0}
        with self.assertRaises(GUARD.GuardError):
            GUARD.validate_ready_evidence(no_write, max_age_sec=900, now=NOW)

    def test_block_gate_is_atomic_and_safe(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "eligibility.json"
            value = GUARD.set_eligibility_blocked(path, "standby_remote_data_plane", now=NOW)
            stored = json.loads(path.read_text(encoding="utf-8"))
        self.assertFalse(value["ready"])
        self.assertEqual(value, stored)
        self.assertEqual("standby_or_transition", stored["mode"])

    def test_cli_error_output_does_not_contain_credentials(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = self.write_env(Path(temporary))
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                rc = GUARD.main(
                    [
                        "rewrite-env",
                        str(path),
                        "--expected-sql-host",
                        "wrong.example",
                        "--expected-redis-host",
                        "10.203.66.1",
                        "--sql-host",
                        "postgres-standby",
                        "--redis-host",
                        "redis-standby",
                        "--backup-dir",
                        str(Path(temporary) / "backups"),
                    ]
                )
        self.assertEqual(2, rc)
        self.assertNotIn(FAKE_SQL_SECRET, output.getvalue())
        self.assertNotIn(FAKE_REDIS_SECRET, output.getvalue())


if __name__ == "__main__":
    unittest.main()
