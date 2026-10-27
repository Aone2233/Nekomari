#!/usr/bin/env python3
"""Offline contract for the installed OVH encrypted-backup scheduler."""

import configparser
from pathlib import Path
import shlex
import unittest


HOSTS = Path(__file__).resolve().parent / "hosts"


def unit(name):
    config = configparser.ConfigParser(interpolation=None)
    config.read(HOSTS / ("nekomari-oc424-encrypted-backup." + name))
    return config


class ScheduleContract(unittest.TestCase):
    def test_fixed_locked_receiver(self):
        service = unit("service")["Service"]
        self.assertEqual(service["Type"], "oneshot")
        self.assertEqual(service["User"], "root")
        self.assertEqual(service["UMask"], "0077")
        self.assertEqual(
            shlex.split(service["ExecStart"]),
            ["/usr/bin/flock", "--nonblock",
             "/run/lock/nekomari-oc424-encrypted.lock",
             "/usr/local/sbin/receive-oc424-encrypted-backup"],
        )
        self.assertEqual(service["TimeoutStartSec"], "360")

    def test_sandbox_and_failure_visibility(self):
        service = unit("service")["Service"]
        for key in ("NoNewPrivileges", "PrivateTmp", "ProtectHome"):
            self.assertEqual(service[key], "true")
        self.assertEqual(service["ProtectSystem"], "strict")
        self.assertEqual(
            shlex.split(service["ReadWritePaths"]),
            ["/srv/backups/oc424-encrypted", "/run/lock"],
        )
        self.assertEqual(service["StandardOutput"], "journal")
        self.assertEqual(service["StandardError"], "journal")
        self.assertNotIn("SuccessExitStatus", service)
        self.assertNotIn("Restart", service)

    def test_utc_persistent_schedule(self):
        config = unit("timer")
        timer = config["Timer"]
        self.assertEqual(timer["OnCalendar"], "*-*-* 03:30:00 UTC")
        self.assertEqual(timer["Persistent"], "true")
        self.assertEqual(timer["AccuracySec"], "1min")
        self.assertEqual(timer["Unit"], "nekomari-oc424-encrypted-backup.service")
        self.assertEqual(config["Install"]["WantedBy"], "timers.target")

    def test_existing_receiver_contract(self):
        receiver = (HOSTS / "ovh-receive-oc424-encrypted-backup.sh").read_text()
        self.assertIn('[ "$#" -eq 0 ] || exit 64', receiver)
        self.assertIn("--output-dir /srv/backups/oc424-encrypted --prefix oc424 --keep 14", receiver)
        self.assertIn("--timeout 300", receiver)
        self.assertIn("StrictHostKeyChecking=yes", receiver)
        self.assertNotIn("--identity-file", receiver)


if __name__ == "__main__":
    unittest.main()
