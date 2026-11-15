#!/usr/bin/env python3
"""Exercise the watchdog with fake systemd/socket commands; never touch services."""

import os
import pwd
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("nekomari-agent-watchdog.sh")


class WatchdogTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.stub("systemctl", r'''#!/bin/bash
case "$1" in
    show)
        [[ ${INSPECTION_ERROR:-0} == 0 ]] || exit 1
        case "$2" in
            --property=ActiveState) echo "${AGENT_STATE:-active}" ;;
            --property=MainPID) echo "${AGENT_PID:-123}" ;;
            *) exit 1 ;;
        esac ;;
    restart) echo restart >> "$EVENTS"; exit "${RESTART_EXIT:-0}" ;;
    *) exit 1 ;;
esac
''')
        self.stub("ss", r'''#!/bin/bash
count=0
[[ ! -f "$COUNTER" ]] || read -r count < "$COUNTER"
count=$((count + 1)); echo "$count" > "$COUNTER"
IFS=, read -ra values <<< "$SOCKETS"
i=$((count - 1))
(( i < ${#values[@]} )) || i=$((${#values[@]} - 1))
case "${values[$i]}" in
    connected) echo '0 0 192.0.2.1:60000 192.0.2.2:443 users:(("agent",pid=123,fd=4))' ;;
    other) echo '0 0 192.0.2.1:60000 192.0.2.2:443 users:(("other",pid=1234,fd=4))' ;;
    missing) : ;;
    error) exit 2 ;;
    *) exit 3 ;;
esac
''')
        self.stub("sleep", "#!/bin/bash\nexit 0\n")
        self.stub("logger", "#!/bin/bash\necho log >> \"$EVENTS\"\n")
        self.env = os.environ | {
            "PATH": f"{self.bin}:/usr/bin:/bin",
            "EVENTS": str(self.root / "events"),
            "COUNTER": str(self.root / "counter"),
            "SOCKETS": "connected",
            "NEKOMARI_AGENT_WATCHDOG_LOCK_FILE": str(self.root / "lock"),
        }

    def stub(self, name, body):
        path = self.bin / name
        path.write_text(body)
        path.chmod(0o755)

    def run_watchdog(self, sockets, *args, **env):
        result = subprocess.run(
            ["bash", str(SCRIPT), *args],
            env=self.env | {"SOCKETS": sockets} | env,
            capture_output=True,
            text=True,
            timeout=5,
        )
        events = (self.root / "events").read_text() if (self.root / "events").exists() else ""
        return result.returncode, events

    def test_read_only_check_requires_the_exact_agent_pid(self):
        self.assertEqual(self.run_watchdog("connected", "--check"), (0, ""))
        (self.root / "counter").unlink()
        self.assertEqual(self.run_watchdog("other", "--check"), (1, ""))

    def test_unprivileged_check_is_an_inspection_error(self):
        if os.geteuid() != 0:
            self.skipTest("requires root to drop privileges in the subprocess")
        nobody = pwd.getpwnam("nobody")
        self.root.chmod(0o755)
        self.bin.chmod(0o755)
        script = self.root / "watchdog.sh"
        script.write_bytes(SCRIPT.read_bytes())
        script.chmod(0o755)
        result = subprocess.run(
            ["bash", str(script), "--check"],
            env=self.env,
            user=nobody.pw_uid,
            group=nobody.pw_gid,
            extra_groups=[],
            capture_output=True,
            text=True,
            timeout=5,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("Root privileges are required", result.stderr)

    def test_healthy_or_transient_connection_does_not_restart(self):
        self.assertEqual(self.run_watchdog("missing,connected"), (0, ""))

    def test_dead_connection_is_rechecked_then_restarted(self):
        code, events = self.run_watchdog("missing,missing,connected")
        self.assertEqual(code, 0)
        self.assertEqual(events.count("restart\n"), 1)

    def test_inspection_error_never_restarts(self):
        for sockets, environment in [("error", {}), ("connected", {"INSPECTION_ERROR": "1"})]:
            with self.subTest(sockets=sockets, environment=environment):
                (self.root / "events").unlink(missing_ok=True)
                (self.root / "counter").unlink(missing_ok=True)
                code, events = self.run_watchdog(sockets, **environment)
                self.assertEqual(code, 1)
                self.assertNotIn("restart", events)

    def test_second_inspection_error_never_restarts(self):
        code, events = self.run_watchdog("missing,error")
        self.assertEqual(code, 1)
        self.assertNotIn("restart", events)

    def test_failed_restart_and_failed_recovery_are_reported(self):
        code, events = self.run_watchdog("missing", RESTART_EXIT="1")
        self.assertEqual(code, 1)
        self.assertEqual(events.count("restart\n"), 1)
        (self.root / "events").unlink()
        (self.root / "counter").unlink()
        code, events = self.run_watchdog("missing")
        self.assertEqual(code, 1)
        self.assertEqual(events.count("restart\n"), 1)

    def test_invalid_pid_is_an_inspection_error(self):
        code, events = self.run_watchdog("connected", AGENT_PID="unknown")
        self.assertEqual(code, 1)
        self.assertNotIn("restart", events)


if __name__ == "__main__":
    unittest.main()
