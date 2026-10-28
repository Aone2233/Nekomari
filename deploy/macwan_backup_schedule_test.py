#!/usr/bin/env python3
"""Offline contracts for the MAC-WAN user backup and health services."""

import configparser
from pathlib import Path
import subprocess
import unittest


HOSTS = Path(__file__).resolve().parent / "hosts"
BASE = "/home/macos/.local/share/nekomari-recovery"


def unit(name):
    parser = configparser.ConfigParser(interpolation=None)
    parser.optionxform = str
    with (HOSTS / name).open() as file:
        parser.read_file(file)
    return parser


class MacWanBackupContracts(unittest.TestCase):
    def test_receivers_use_separate_pinned_restricted_keys(self):
        for filename, key, mode in (
            ("mac-wan-receive-oc424-encrypted-backup.sh", "oc424-backup-pull", "receive"),
            ("mac-wan-check-oc424-backup-health.sh", "oc424-health-push", "inspect"),
        ):
            path = HOSTS / filename
            subprocess.run(["sh", "-n", str(path)], check=True)
            text = path.read_text()
            for term in ('[ "$#" -eq 0 ]', "umask 077", 'base=' + BASE,
                         "python3 -I", mode, '--output-dir "$base/scheduled"',
                         '"$base/credentials/' + key + '"', "-F /dev/null -T",
                         "IdentitiesOnly=yes", "BatchMode=yes", "StrictHostKeyChecking=yes",
                         'UserKnownHostsFile="$base/credentials/oc424-known-hosts"',
                         "GlobalKnownHostsFile=/dev/null"):
                self.assertIn(term, text, (filename, term))
            self.assertNotIn("--identity-file", text)
        backup = (HOSTS / "mac-wan-receive-oc424-encrypted-backup.sh").read_text()
        self.assertIn("--keep 14", backup)
        self.assertNotIn('"$base/incoming"', backup)

    def test_user_timers_and_services_are_bounded(self):
        backup = unit("nekomari-macwan-oc424-backup.timer")["Timer"]
        self.assertEqual(backup["OnCalendar"], "*-*-* 03:30:00 UTC")
        self.assertEqual(backup["Persistent"], "true")
        self.assertEqual(backup["Unit"], "nekomari-macwan-oc424-backup.service")
        health = unit("nekomari-macwan-oc424-health.timer")["Timer"]
        self.assertEqual(health["OnUnitActiveSec"], "15min")
        self.assertEqual(health["Unit"], "nekomari-macwan-oc424-health.service")
        for name, timeout in (("backup", "360"), ("health", "120")):
            service = unit(f"nekomari-macwan-oc424-{name}.service")["Service"]
            self.assertEqual(service["Type"], "oneshot")
            self.assertEqual(service["UMask"], "0077")
            self.assertEqual(service["TimeoutStartSec"], timeout)
            self.assertEqual(service["NoNewPrivileges"], "true")
            self.assertNotIn("User", service)
            self.assertIn("/usr/bin/timeout --signal=KILL", service["ExecStart"])


if __name__ == "__main__":
    unittest.main()
