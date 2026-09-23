"""Mounted clock regression: python script/compiler-static.browser.spec.py.

Requires Playwright Python and its Chromium browser.
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


class PriceTagsBrowserTest(unittest.TestCase):
    @classmethod
    def _stop_server(cls):
        if cls.server.poll() is None:
            cls.server.terminate()
        try:
            cls.server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cls.server.kill()
            cls.server.wait(timeout=5)

    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/compiler-static.browser.html"
        cls.server = None
        cls.playwright = None
        cls.server_log = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        try:
            cls.server = subprocess.Popen(
                ["node", str(ROOT / "node_modules/vite/bin/vite.js"),
                 "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
                cwd=ROOT,
                stdout=cls.server_log,
                stderr=subprocess.STDOUT,
            )
            for _ in range(100):
                if cls.server.poll() is not None:
                    raise RuntimeError("Vite exited before fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Price tags fixture did not become ready")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception as error:
            try:
                if cls.playwright is not None:
                    cls.playwright.stop()
            finally:
                try:
                    if cls.server is not None:
                        cls._stop_server()
                finally:
                    cls.server_log.seek(0)
                    vite_log = cls.server_log.read()[-4000:]
                    cls.server_log.close()
            if vite_log:
                raise RuntimeError(f"{error}\nVite log:\n{vite_log}") from error
            raise

    @classmethod
    def tearDownClass(cls):
        try:
            cls.browser.close()
        finally:
            try:
                cls.playwright.stop()
            finally:
                try:
                    cls._stop_server()
                finally:
                    cls.server_log.close()

    def test_free_to_paid_reads_clock_on_paid_mount(self):
        page = self.browser.new_page()
        try:
            page.add_init_script("""
                window.__fixtureNow = Date.UTC(2026, 8, 23, 12);
                Date.now = () => window.__fixtureNow;
            """)
            page.goto(self.url)
            page.get_by_role("button", name="Set paid").wait_for()
            self.assertEqual(page.locator("#price-tags .rt-Badge").count(), 0)
            page.evaluate("window.__fixtureNow += 45 * 24 * 60 * 60 * 1000")
            page.get_by_role("button", name="Set paid").click()
            badge = page.locator("#price-tags .rt-Badge").last
            self.assertEqual(badge.inner_text(), "Expires in 8 days")
            self.assertEqual(badge.get_attribute("data-accent-color"), "orange")
        finally:
            page.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
