#!/usr/bin/env python3
"""Hermetic ciphertext/heartbeat/forced-command freshness contracts."""

import configparser
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import backup_health as health


class BackupHealthTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.root.chmod(0o700)
        self.backups = self.root / "backups"
        self.backups.mkdir(mode=0o700)
        (self.backups / ".backup.lock").touch(mode=0o600)
        self.output = self.root / "ovh.json"
        self.now = dt.datetime(2026, 10, 27, 8, tzinfo=dt.timezone.utc)

    def archive(self, age=dt.timedelta(hours=1), stamp=None):
        stamp = stamp or (self.now - age).strftime("%Y%m%dT%H%M%S%fZ")
        path = self.backups / ("oc424-" + stamp + "-abcdef012345.tar.gz.age")
        raw = b"age-encryption.org/v1\n" + b"test-only-ciphertext-not-decryptable" * 8
        path.write_bytes(raw)
        Path(str(path) + ".sha256").write_text(hashlib.sha256(raw).hexdigest() + "  " + path.name + "\n")
        return path

    def report(self, status="ok", age=dt.timedelta(hours=1)):
        return {"version": 1, "checked_at": self.now.isoformat(), "status": status,
                "backup_at": (self.now - age).isoformat() if status == "ok" else None}

    def test_ciphertext_missing_valid_stale(self):
        self.assertEqual(health.inspect(self.backups, "oc424", self.now)["status"], "missing")
        self.archive()
        report = health.inspect(self.backups, "oc424", self.now)
        self.assertEqual(report, self.report())
        self.assertEqual(health.validate_report(json.dumps(report), self.now), report)
        self.assertNotIn("archive", report)
        self.assertNotIn("sha256", report)

    def test_invalid_latest_does_not_hide_behind_older_valid_backup(self):
        self.archive(age=dt.timedelta(hours=3))
        latest = self.archive()
        latest.write_bytes(b"damaged")
        self.assertEqual(health.inspect(self.backups, "oc424", self.now)["status"], "invalid")
        Path(str(latest) + ".sha256").unlink()
        self.assertEqual(health.inspect(self.backups, "oc424", self.now)["status"], "invalid")

    def test_fake_future_and_wrong_header_refused(self):
        self.archive(age=-dt.timedelta(minutes=6))
        self.assertEqual(health.inspect(self.backups, "oc424", self.now)["status"], "invalid")
        for path in self.backups.glob("*.age*"):
            path.unlink()
        path = self.archive()
        raw = b"not-an-age-header" * 20
        path.write_bytes(raw)
        Path(str(path) + ".sha256").write_text(hashlib.sha256(raw).hexdigest() + "  " + path.name + "\n")
        self.assertEqual(health.inspect(self.backups, "oc424", self.now)["status"], "invalid")

    def test_symlink_hardlink_and_receive_lock(self):
        path = self.archive()
        os.link(path, self.root / "hardlink")
        self.assertEqual(health.inspect(self.backups, "oc424", self.now)["status"], "invalid")
        (self.root / "hardlink").unlink()
        with (self.backups / ".backup.lock").open("rb") as file:
            fcntl.flock(file, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.assertIsNone(health.inspect(self.backups, "oc424", self.now))

    def test_atomic_ingest_server_time_and_monotonic_reports(self):
        raw = json.dumps(self.report()).encode()
        self.assertEqual(health.ingest(raw, self.output, self.now), "stored")
        stored = json.loads(self.output.read_text())
        self.assertEqual(stored.pop("received_at"), self.now.isoformat())
        self.assertEqual(stored, self.report())
        before = self.output.read_bytes()
        self.assertEqual(health.ingest(raw, self.output, self.now), "ignored-out-of-order")
        self.assertEqual(self.output.read_bytes(), before)
        self.assertEqual(self.output.stat().st_mode & 0o077, 0)
        self.assertEqual(list(self.root.glob(".health-*")), [])

    def test_invalid_expired_oversize_and_extra_fields_do_not_overwrite(self):
        health.ingest(json.dumps(self.report()).encode(), self.output, self.now)
        before = self.output.read_bytes()
        cases = [b"{" + b"x" * 4096, b"null", b'{"version":1,"version":1}',
                 json.dumps(dict(self.report(), credential="test-only-secret")).encode(),
                 json.dumps(dict(self.report(), version=True)).encode(),
                 json.dumps(dict(self.report(), checked_at=(self.now-dt.timedelta(minutes=6)).isoformat())).encode(),
                 json.dumps(self.report(age=-dt.timedelta(minutes=6))).encode()]
        for raw in cases:
            with self.assertRaises((health.HealthError, ValueError, TypeError)):
                health.ingest(raw, self.output, self.now)
            self.assertEqual(self.output.read_bytes(), before)

    def test_unsafe_destination_and_atomic_failure(self):
        self.output.symlink_to(self.root / "absent")
        with self.assertRaises((health.HealthError, OSError)):
            health.ingest(json.dumps(self.report()).encode(), self.output, self.now)
        self.output.unlink()
        with patch.object(health.os, "replace", side_effect=OSError("simulated")):
            with self.assertRaises(OSError):
                health.ingest(json.dumps(self.report()).encode(), self.output, self.now)
        self.assertEqual(list(self.root.glob(".health-*")), [])
        self.assertFalse(self.output.exists())

    def test_bounded_cli_suppresses_transport_output(self):
        self.archive()
        result = subprocess.run([sys.executable, str(Path(health.__file__)), "inspect", "--output-dir", str(self.backups),
                                 "--", sys.executable, "-c", "import sys;print('private-test-token',file=sys.stderr);sys.exit(1)"],
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertNotIn(b"private-test-token", result.stdout + result.stderr)

    def test_systemd_and_forced_command_boundaries(self):
        hosts = Path(__file__).with_name("hosts")
        service = configparser.ConfigParser(interpolation=None)
        service.read(hosts / "nekomari-oc424-backup-health.service")
        self.assertEqual(service["Service"]["ProtectSystem"], "strict")
        self.assertEqual(service["Service"]["TimeoutStartSec"], "120")
        self.assertNotIn("ReadWritePaths", service["Service"])
        timer = configparser.ConfigParser(interpolation=None)
        timer.read(hosts / "nekomari-oc424-backup-health.timer")
        self.assertEqual(timer["Timer"]["OnUnitActiveSec"], "15min")
        receiver = (hosts / "oc424-receive-backup-health.sh").read_text()
        self.assertIn("timeout --signal=KILL 10s", receiver)
        self.assertIn("--output /opt/nekomari/data/backup-health/ovh.json", receiver)
        sender = (hosts / "ovh-check-oc424-backup-health.sh").read_text()
        self.assertIn("StrictHostKeyChecking=yes", sender)
        self.assertNotIn("--identity-file", receiver + sender)
        self.assertNotIn("api/key", receiver + sender)


if __name__ == "__main__":
    unittest.main()
