"""Mounted load-chart regressions: python script/load-chart.browser.spec.py.

`pages/instance/LoadChart.tsx` fetches `public:queryMetrics` for the selected
sampling algorithm / time range / node. These cases pin the two ways an older
response could still be on the wire when a newer request starts: switching the
sampling algorithm while the first request is held, and leaving the instance
route while the first request is held and coming back.
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

# The dashboard template the fixture renders: one chart bound to a metric that
# the real-time view also queries, so the mount request is a real one.
NEWER_VALUE = 900
STALE_VALUE = 100
NEWER_TEXT = f"Traffic Upload: {NEWER_VALUE} B"
STALE_TEXT = f"Traffic Upload: {STALE_VALUE} B"


def query_metrics(value):
    return {
        "series": [
            {
                "metric_key": "traffic.up",
                "entity_id": "node-1",
                "unit": "bytes",
                "count": 1,
                "points": [{"time": "2026-01-01T00:00:00Z", "value": value}],
            }
        ]
    }


class LoadChartBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/load-chart.browser.html"
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
                    raise RuntimeError("Vite exited before load chart fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Load chart fixture did not become ready")
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
        self.page = self.browser.new_page(viewport={"width": 1200, "height": 900})
        self.errors = []
        self.page.on("console", lambda message: self._on_console(message))
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.add_init_script(
            "window.__loadChartSeed = [{ method: 'public:queryMetrics', hold: 'mount' }];"
        )
        self.page.goto(self.url)

    def _on_console(self, message):
        if message.type != "error":
            return
        # The document has no favicon, so the browser's own probe 404s. Nothing
        # the fixture under test does.
        if "Failed to load resource" in message.text:
            return
        self.errors.append(message.text)

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    def settle(self):
        self.page.evaluate(
            "() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))"
        )

    def test_older_sampling_response_cannot_replace_the_newer_one(self):
        self.page.wait_for_function("loadChartFixture.pending('mount')")
        self.page.evaluate(
            "value => loadChartFixture.respond('public:queryMetrics', value)",
            query_metrics(NEWER_VALUE),
        )
        self.page.get_by_role("combobox", name="Sampling algorithm").click()
        self.page.get_by_role("option", name="Max", exact=True).click()
        expect(self.page.get_by_text(NEWER_TEXT)).to_be_visible()
        self.assertEqual(
            [call["params"]["aggregation"] for call in self.page.evaluate(
                "loadChartFixture.calls().filter(call => call.method === 'public:queryMetrics')")],
            ["avg", "max"],
        )
        self.assertEqual(
            [call["params"]["hours"] for call in self.page.evaluate(
                "loadChartFixture.calls().filter(call => call.method === 'public:queryMetrics')")],
            [1, 1],
        )

        # The first (average) response lands after the newer one.
        self.page.evaluate(
            "value => loadChartFixture.resolve('mount', value)",
            query_metrics(STALE_VALUE),
        )
        self.settle()
        expect(self.page.get_by_text(NEWER_TEXT)).to_be_visible()
        self.assertEqual(self.page.get_by_text(STALE_TEXT).count(), 0)

        # Sensitivity check: the very same text does appear when a *current*
        # request carries that value, so the absence above is the stale-response
        # guard, not a locator that can never match.
        self.page.evaluate(
            "value => loadChartFixture.respond('public:queryMetrics', value)",
            query_metrics(STALE_VALUE),
        )
        self.page.get_by_role("combobox", name="Sampling algorithm").click()
        self.page.get_by_role("option", name="Min", exact=True).click()
        expect(self.page.get_by_text(STALE_TEXT)).to_be_visible()

    def test_leaving_and_returning_refetches_and_ignores_the_orphaned_response(self):
        self.page.wait_for_function("loadChartFixture.pending('mount')")
        self.page.get_by_role("button", name="Leave instance").click()
        expect(self.page.get_by_test_id("away")).to_be_visible()

        # The instance is gone; its response must not reach the new mount.
        self.page.evaluate(
            "value => loadChartFixture.resolve('mount', value)",
            query_metrics(STALE_VALUE),
        )
        self.settle()
        expect(self.page.get_by_test_id("away")).to_be_visible()

        self.page.evaluate(
            "value => loadChartFixture.respond('public:queryMetrics', value)",
            query_metrics(NEWER_VALUE),
        )
        self.page.get_by_role("button", name="Back to instance").click()
        expect(self.page.get_by_text(NEWER_TEXT)).to_be_visible()
        self.assertEqual(
            self.page.evaluate("loadChartFixture.requests('public:queryMetrics')"), 2
        )
        self.assertEqual(self.page.get_by_text(STALE_TEXT).count(), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
