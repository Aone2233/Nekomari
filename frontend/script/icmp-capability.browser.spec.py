"""Mounted regression for the ICMP capability badge.

Run with `python script/icmp-capability.browser.spec.py`.

The badge is the whole of roadmap E2's first phase: the node says what it can do
about ICMP, and the panel shows it. The assertion that matters most is the one
about **unknown** — every node running a pre-`icmp_capability` agent reports
nothing, and drawing that as "unavailable" would mark working nodes broken. The
two working states are asserted too, so a node with the fallback socket is not
mistaken for a node that cannot probe.
"""

import socket
import subprocess
import tempfile
import time
import unittest
from pathlib import Path
from urllib.error import URLError
from urllib.request import urlopen

from playwright.sync_api import expect, sync_playwright


ROOT = Path(__file__).resolve().parents[1]


class IcmpCapabilityBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/icmp-capability.browser.html"
        cls.output = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        cls.server = None
        cls.playwright = None
        cls.browser = None
        try:
            cls.server = subprocess.Popen(
                ["node", str(ROOT / "node_modules/vite/bin/vite.js"),
                 "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
                cwd=ROOT, stdout=cls.output, stderr=subprocess.STDOUT,
            )
            for _ in range(100):
                if cls.server.poll() is not None:
                    raise RuntimeError("Vite exited before the fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("ICMP capability fixture did not become ready")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception as error:
            cls._stop()
            cls.output.seek(0)
            log = cls.output.read()[-4000:]
            cls.output.close()
            raise RuntimeError(f"{error}\nVite log:\n{log}") from error

    @classmethod
    def _stop(cls):
        try:
            if cls.browser is not None:
                cls.browser.close()
        finally:
            try:
                if cls.playwright is not None:
                    cls.playwright.stop()
            finally:
                if cls.server is not None:
                    if cls.server.poll() is None:
                        cls.server.terminate()
                    try:
                        cls.server.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        cls.server.kill()
                        cls.server.wait(timeout=5)

    @classmethod
    def tearDownClass(cls):
        cls._stop()
        cls.output.close()

    def setUp(self):
        self.page = self.browser.new_page()
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.goto(self.url)
        expect(self.page.locator("#cases")).to_be_visible()

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    def badge(self, case):
        return self.page.locator(f"[data-case='{case}'] [data-testid='icmp-capability']")

    def test_each_state_carries_its_own_marker(self):
        # The attribute is the machine-readable fact, so a future restyle cannot
        # silently change which state is being drawn.
        for case, expected in [
            ("raw", "raw"),
            ("ping", "ping"),
            ("none", "none"),
            ("empty", "unknown"),
            ("absent", "unknown"),
        ]:
            self.assertEqual(
                self.badge(case).get_attribute("data-icmp-capability"), expected,
                f"case {case} should report {expected}")

    def test_unknown_is_never_drawn_as_unavailable(self):
        # The E2 mistake this exists to prevent: a node whose agent has not
        # reported must not look like a node that cannot probe.
        for case in ("empty", "absent"):
            expect(self.badge(case)).to_contain_text("not reported")
            expect(self.badge(case)).not_to_contain_text("cannot send ICMP probes")
            self.assertNotEqual(
                self.badge(case).get_attribute("data-icmp-capability"), "none")

    def test_unavailable_names_the_cause_and_the_fix(self):
        badge = self.badge("none")
        expect(badge).to_contain_text("ICMP unavailable")
        title = badge.get_attribute("title")
        # The tooltip is where the operator gets the cause and the remedy; a bare
        # "unavailable" would leave them reading the journal.
        self.assertIn("cap_net_raw", title)
        self.assertIn("ping_group_range", title)

    def test_both_working_sockets_read_as_available(self):
        # The fallback socket measures echo latency just as well, so it must not
        # be reported as a degraded state the user has to worry about.
        expect(self.badge("raw")).to_contain_text("raw socket")
        expect(self.badge("ping")).to_contain_text("ping socket")
        for case in ("raw", "ping"):
            self.assertNotIn(
                "cannot send ICMP probes", self.badge(case).get_attribute("title"))

    def test_compact_form_keeps_the_marker_and_drops_the_visible_label(self):
        # The node table uses the compact form: the icon and the tooltip are enough
        # in a dense row, but the state still has to be readable — including by a
        # screen reader, which gets the same sentence the tooltip shows.
        compact = self.page.locator("[data-case='compact-none'] [data-testid='icmp-capability']")
        self.assertEqual(compact.get_attribute("data-icmp-capability"), "none")
        self.assertIn("cap_net_raw", compact.get_attribute("title"))
        # The visible label is gone; the screen-reader text is not.
        self.assertNotIn("ICMP unavailable", compact.inner_text())
        self.assertIn("cannot send ICMP probes", compact.inner_text())


if __name__ == "__main__":
    unittest.main(verbosity=2)
