"""Mounted admin metrics-settings regressions: python script/metrics-settings.browser.spec.py.

`pages/admin/settings/metrics.tsx` loads the retention table from
`admin:listMetricDefinitions`. These cases pin the two ways an older response
could still be on the wire when a newer request starts: a language switch while
the first request is held (the loader is keyed on `t`, so it refetches), and
leaving the settings page while the first request is held and coming back.
"""

import json
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

ALPHA_EN = "Alpha metric"
ALPHA_ZH = "阿尔法指标"
BETA_EN = "Beta metric"
BETA_ZH = "贝塔指标"


def metric(name, en, zh, retention_days):
    return {
        "name": name,
        "type": "gauge",
        "unit": "%",
        "retention_days": retention_days,
        "metadata": {"display_name": {"en": en, "zh": zh}},
    }


def migration(metrics_done, total_metrics):
    return {
        "status": "running",
        "is_running": True,
        "source_driver": "sqlite",
        "source_dsn": "",
        "target_driver": "sqlite",
        "target_dsn": "",
        "total_metrics": total_metrics,
        "metrics_done": metrics_done,
        "current_metric": "",
        "migrated_points": metrics_done * 100,
    }


class MetricsSettingsBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/metrics-settings.browser.html"
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
                    raise RuntimeError("Vite exited before metrics fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Metrics fixture did not become ready")
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
            "window.__metricsSeed = "
            "[{ method: 'admin:listMetricDefinitions', hold: 'mount' }];"
        )
        self.page.goto(self.url)

    def _on_console(self, message):
        if message.type != "error":
            return
        # The document has no favicon, so the browser's own probe 404s. Nothing
        # the fixture under test does.
        if "Failed to load resource" in message.text:
            return
        # Pre-existing defect in the page under test, logged on every mount:
        # SettingCardShortTextInput (src/components/admin/SettingCard.tsx:419-420)
        # passes `value` and `defaultValue` to the same TextField.Root, and
        # metrics.tsx uses it four times with `defaultValue`. Dev-only (React
        # strips it from production builds), so the built-panel smoke job never
        # sees it. Reported, deliberately not fixed here; every other console
        # error still fails the test.
        if "both value and defaultValue props" in message.text:
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

    def respond_list(self, metrics):
        self.page.evaluate(
            "value => metricsFixture.respond('admin:listMetricDefinitions', value)",
            metrics,
        )

    def test_older_list_response_cannot_replace_the_newer_one(self):
        self.page.wait_for_function("metricsFixture.pending('mount')")
        expect(self.page.get_by_text("Loading metrics")).to_be_visible()

        # The loader is keyed on `t`, so switching the panel language refetches
        # the same list. Queue the newer answer first, then switch.
        self.respond_list([metric("beta.metric", BETA_EN, BETA_ZH, 5)])
        self.page.evaluate("() => metricsFixture.setLanguage('zh')")
        self.settle()
        self.assertEqual(
            self.page.evaluate("metricsFixture.requests('admin:listMetricDefinitions')"),
            2,
        )
        expect(self.page.get_by_text(BETA_ZH)).to_be_visible()
        self.assertEqual(self.page.get_by_text("beta.metric").count(), 1)

        # The first (English era) response lands after the newer one.
        self.page.evaluate(
            "value => metricsFixture.resolve('mount', value)",
            [metric("alpha.metric", ALPHA_EN, ALPHA_ZH, 9)],
        )
        self.settle()
        expect(self.page.get_by_text(BETA_ZH)).to_be_visible()
        self.assertEqual(self.page.get_by_text(ALPHA_ZH).count(), 0)
        self.assertEqual(self.page.get_by_text("alpha.metric").count(), 0)

        # Sensitivity check: the same list does render when a *current* request
        # carries it, so the absence above is the stale-response guard, not a
        # locator that can never match.
        self.respond_list([metric("alpha.metric", ALPHA_EN, ALPHA_ZH, 9)])
        self.page.evaluate("() => metricsFixture.setLanguage('en')")
        self.settle()
        expect(self.page.get_by_text(ALPHA_EN)).to_be_visible()
        self.assertEqual(self.page.get_by_text("alpha.metric").count(), 1)

    def test_leaving_and_returning_refetches_and_ignores_the_orphaned_response(self):
        self.page.wait_for_function("metricsFixture.pending('mount')")
        self.page.get_by_role("button", name="Leave settings").click()
        expect(self.page.get_by_test_id("away")).to_be_visible()

        # The page is gone; its response must not reach the new mount.
        self.page.evaluate(
            "value => metricsFixture.resolve('mount', value)",
            [metric("alpha.metric", ALPHA_EN, ALPHA_ZH, 9)],
        )
        self.settle()
        expect(self.page.get_by_test_id("away")).to_be_visible()

        self.respond_list([metric("beta.metric", BETA_EN, BETA_ZH, 5)])
        self.page.get_by_role("button", name="Back to settings").click()
        expect(self.page.get_by_text(BETA_EN)).to_be_visible()
        self.assertEqual(
            self.page.evaluate("metricsFixture.requests('admin:listMetricDefinitions')"),
            2,
        )
        self.assertEqual(self.page.get_by_text(ALPHA_EN).count(), 0)

    def test_older_migration_poll_cannot_rewind_progress(self):
        """A status poll that lands after a newer one must not move progress back.

        While a migration is running the card polls every 2 seconds and used to
        apply whatever came back, with no sequence guard, so an older poll landing
        late replaced the newer state: measured on this fixture, 10/10 became
        2/10 and the points counter went backwards with it. The same window is
        open to the manual Refresh button, which only `loadingStatus` disables and
        silent polls never set.
        """
        # Re-mount with a seed that makes the migration look like it is running
        # (so the poll starts) and holds the first poll.
        self.page.add_init_script(
            "window.__metricsSeed = ["
            "  { method: 'admin:listMetricDefinitions', value: [] },"
            "  { method: 'admin:getMetricMigrationStatus', value: "
            + json.dumps(migration(1, 10))
            + " },"
            "  { method: 'admin:getMetricMigrationStatus', hold: 'stale-poll' },"
            "  { method: 'admin:getMetricMigrationStatus', value: "
            + json.dumps(migration(10, 10))
            + " }"
            "];"
        )
        self.page.goto(self.url)

        # The newer poll (10/10) lands. The mount fetch plus two polls is 3 calls.
        expect(self.page.get_by_text("10 / 10").first).to_be_visible(timeout=20000)
        self.assertEqual(
            self.page.evaluate(
                "metricsFixture.requests('admin:getMetricMigrationStatus')"
            ),
            3,
        )

        # The first poll now lands late, carrying the older 2/10.
        self.page.evaluate(
            "value => metricsFixture.resolve('stale-poll', value)",
            migration(2, 10),
        )
        self.settle()
        expect(self.page.get_by_text("10 / 10").first).to_be_visible()
        self.assertEqual(self.page.get_by_text("2 / 10").count(), 0)
        self.assertEqual(self.page.get_by_text("200").count(), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
