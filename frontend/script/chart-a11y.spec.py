"""Browser regression fixture: python script/chart-a11y.spec.py

Requires the local Playwright Python package and its Chromium browser.
"""

import socket
import subprocess
import tempfile
import time
import unittest
from pathlib import Path
from urllib.error import URLError
from urllib.request import urlopen

from playwright.sync_api import sync_playwright


ROOT = Path(__file__).resolve().parents[1]


class ChartBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/chart-a11y.html"
        cls.server_output = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        cls.server = None
        cls.playwright = None
        cls.browser = None
        try:
            cls.server = subprocess.Popen(
                [
                    "node",
                    str(ROOT / "node_modules/vite/bin/vite.js"),
                    "--host", "127.0.0.1", "--port", str(port), "--strictPort",
                ],
                cwd=ROOT,
                stdout=cls.server_output,
                stderr=subprocess.STDOUT,
            )
            for _ in range(100):
                status = cls.server.poll()
                if status is not None:
                    raise RuntimeError(
                        f"Vite exited with status {status} before fixture was ready:\n"
                        f"{cls._server_log()}"
                    )
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError(f"Chart fixture did not become ready:\n{cls._server_log()}")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception:
            cls._stop_fixture()
            raise

    @classmethod
    def _server_log(cls):
        cls.server_output.seek(0)
        return cls.server_output.read().strip() or "(no Vite output)"

    @classmethod
    def _stop_fixture(cls):
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
                    cls.server.wait(timeout=5)
                cls.server_output.close()

    @classmethod
    def tearDownClass(cls):
        cls._stop_fixture()

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 900, "height": 600})
        self.page.goto(self.url)
        self.page.locator(".recharts-line-curve").first.wait_for()

    def tearDown(self):
        self.page.close()

    def test_keyboard_focus_has_name_outline_and_tooltip(self):
        surface = self.page.locator(".recharts-surface")
        self.assertEqual(surface.locator("title").text_content(), "Weekly visits and sales")
        self.page.keyboard.press("Tab")
        self.page.keyboard.press("Tab")
        self.page.keyboard.press("Tab")
        self.assertTrue(surface.evaluate("el => document.activeElement === el"))
        self.assertEqual(surface.evaluate("el => getComputedStyle(el).outlineStyle"), "solid")
        self.assertEqual(surface.evaluate("el => getComputedStyle(el).outlineWidth"), "2px")
        self.page.keyboard.press("ArrowRight")
        tooltip = self.page.locator(".recharts-tooltip-wrapper")
        self.assertIn("Tue", tooltip.inner_text())
        self.assertIn("Visits", tooltip.inner_text())
        self.assertIn("Sales", tooltip.inner_text())

    def test_theme_changes_resolved_series_and_legend_colors(self):
        line = self.page.locator(".recharts-line-curve").first
        color = lambda: line.evaluate("el => getComputedStyle(el).stroke")
        swatches = lambda: self.page.locator(
            ".recharts-legend-wrapper [style*='background-color']"
        ).evaluate_all("els => els.map(el => getComputedStyle(el).backgroundColor)")
        self.assertEqual(color(), "rgb(194, 65, 12)")
        self.assertIn("Visits", self.page.locator(".recharts-legend-wrapper").inner_text())
        self.assertIn("rgb(194, 65, 12)", swatches())
        self.page.get_by_role("button", name="Toggle theme").click()
        self.page.wait_for_function(
            "getComputedStyle(document.querySelector('.recharts-line-curve')).stroke === 'rgb(253, 186, 116)'"
        )
        self.assertEqual(color(), "rgb(253, 186, 116)")
        self.assertIn("rgb(253, 186, 116)", swatches())
        self.page.get_by_role("button", name="Toggle theme").click()
        self.page.wait_for_function(
            "getComputedStyle(document.querySelector('.recharts-line-curve')).stroke === 'rgb(194, 65, 12)'"
        )

    def test_series_labels_name_chart_when_no_explicit_label(self):
        self.page.get_by_role("button", name="Toggle explicit label").click()
        self.assertEqual(
            self.page.locator(".recharts-surface title").text_content(),
            "Visits, Sales",
        )

    def test_animated_series_update_reaches_new_data(self):
        paths = self.page.locator(".recharts-line-curve")
        self.assertEqual(paths.count(), 2)
        before = [paths.nth(i).get_attribute("d") for i in range(2)]
        self.page.get_by_role("button", name="Update series").click()
        self.page.wait_for_function(
            """before => {
                const paths = [...document.querySelectorAll('.recharts-line-curve')]
                    .map(path => path.getAttribute('d'));
                if (paths.length !== before.length ||
                    paths.every((path, i) => path === before[i])) return false;
                const last = window.__chartPaths;
                const now = performance.now();
                if (!last || paths.some((path, i) => path !== last.paths[i])) {
                    window.__chartPaths = { paths, since: now };
                    return false;
                }
                return now - last.since >= 200;
            }""",
            arg=before,
            polling="raf",
        )
        after = [paths.nth(i).get_attribute("d") for i in range(2)]
        self.assertNotEqual(before, after)
        self.page.locator(".recharts-surface").focus()
        self.page.keyboard.press("ArrowRight")
        self.page.wait_for_function(
            """() => document.querySelector('.recharts-tooltip-wrapper')
                ?.innerText.trim().split(/\\s+/).join(' ') === 'Tue Visits 7 Sales 4'"""
        )
        self.assertEqual(
            self.page.locator(".recharts-tooltip-wrapper").inner_text().split(),
            ["Tue", "Visits", "7", "Sales", "4"],
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
