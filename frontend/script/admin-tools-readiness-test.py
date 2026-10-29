"""Pin the bounded readiness behavior used by the synthetic navigation check."""

import importlib.util
from http.client import RemoteDisconnected
from pathlib import Path
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("admin_tools_navigation", Path(__file__).with_name("admin-tools-navigation.browser.spec.py"))
navigation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(navigation)


class Process:
    def poll(self):
        return None


class Response:
    status = 200

    def __enter__(self):
        return self

    def __exit__(self, *_):
        pass


class ReadinessTest(unittest.TestCase):
    def test_disconnect_during_listener_handoff_is_retried(self):
        class Opener:
            calls = 0

            def open(self, *_args, **_kwargs):
                self.calls += 1
                if self.calls == 1:
                    raise RemoteDisconnected("listener closed during handoff")
                return Response()

        opener = Opener()
        with patch.object(navigation.time, "sleep") as sleep:
            navigation.ready("http://127.0.0.1/version", Process(), lambda: opener, attempts=2)
        self.assertEqual(opener.calls, 2)
        sleep.assert_called_once_with(0.1)

    def test_persistent_disconnect_still_fails(self):
        class Opener:
            def open(self, *_args, **_kwargs):
                raise RemoteDisconnected("listener never responds")

        with patch.object(navigation.time, "sleep"), self.assertRaises(TimeoutError):
            navigation.ready("http://127.0.0.1/version", Process(), Opener, attempts=2)

    def test_exited_process_still_fails(self):
        class ExitedProcess:
            def poll(self):
                return 1

        with self.assertRaisesRegex(AssertionError, "exited before readiness"):
            navigation.ready("http://127.0.0.1/version", ExitedProcess(), attempts=2)


if __name__ == "__main__":
    unittest.main()
