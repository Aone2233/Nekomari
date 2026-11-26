"""Exercise the actual quality and forecast shells with controlled RPC failures."""
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

class ProbeStateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("localhost", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://localhost:{port}/script/probe-state.browser.html"
        cls.log = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        cls.server = subprocess.Popen(["node", str(ROOT / "node_modules/vite/bin/vite.js"), "--host", "localhost", "--port", str(port), "--strictPort"], cwd=ROOT, stdout=cls.log, stderr=subprocess.STDOUT)
        try:
            for _ in range(100):
                if cls.server.poll() is not None:
                    raise RuntimeError("Vite exited")
                try:
                    with urlopen(cls.base, timeout=1):
                        break
                except (URLError, TimeoutError):
                    time.sleep(.1)
            else:
                raise RuntimeError("Vite did not start")
            cls.pw = sync_playwright().start()
            cls.browser = cls.pw.chromium.launch(headless=True)
        except Exception:
            cls.server.terminate(); cls.server.wait(timeout=5)
            cls.log.seek(0); print(cls.log.read()[-4000:]); cls.log.close()
            raise

    @classmethod
    def tearDownClass(cls):
        cls.browser.close(); cls.pw.stop(); cls.server.terminate(); cls.server.wait(timeout=5); cls.log.close()

    def test_initial_failure_and_retry_in_both_languages(self):
        for feature in ("quality", "forecast"):
            for lang, retry in (("en", "Retry"), ("zh_CN", "重试")):
                with self.subTest(feature=feature, lang=lang):
                    page = self.browser.new_page()
                    errors = []
                    page.on("pageerror", lambda e: errors.append(str(e)))
                    try:
                        page.goto(f"{self.base}?page={feature}&lang={lang}&initialFailure=1")
                        alert = page.get_by_role("alert")
                        expect(alert).to_contain_text("fixture read failed")
                        expect(page.get_by_test_id("forecast-page")).to_have_count(0)
                        expect(page.get_by_text("Fixture node")).to_have_count(0)
                        alert.get_by_role("button", name=retry).click()
                        expect(alert).to_have_count(0)
                        if feature == "quality":
                            expect(page.get_by_text("Fixture node")).to_be_visible()
                        else:
                            expect(page.get_by_test_id("forecast-page")).to_be_visible()
                        self.assertEqual(errors, [])
                    finally:
                        page.close()

    def test_quality_refresh_retains_stale_data_and_ages_then_retries(self):
        page = self.browser.new_page()
        page.clock.install()
        try:
            page.goto(self.base + "?page=quality&lang=zh_CN")
            row = page.get_by_role("row").filter(has_text="Fixture node")
            expect(row).to_contain_text("10s")
            page.evaluate("window.probeFixture.failNext = true")
            page.clock.run_for(31000)
            expect(page.get_by_role("alert")).to_contain_text("正在显示上次的数据")
            expect(row).to_contain_text("41s")
            page.get_by_role("alert").get_by_role("button", name="重试").click()
            expect(page.get_by_role("alert")).to_have_count(0)
            expect(row).to_contain_text("10s")
        finally:
            page.close()

    def test_forecast_failed_preview_keeps_old_data_and_retry_recovers(self):
        page = self.browser.new_page()
        try:
            page.goto(self.base + "?page=forecast&lang=zh_CN")
            expect(page.get_by_test_id("forecast-page")).to_be_visible()
            page.evaluate("window.probeFixture.failNext = true")
            page.get_by_test_id("forecast-cycle-day").fill("15")
            page.get_by_test_id("forecast-preview").click()
            alert = page.get_by_role("alert")
            expect(alert).to_contain_text("正在显示上次的数据")
            expect(page.get_by_test_id("forecast-page")).to_be_visible()
            alert.get_by_role("button", name="重试").click()
            expect(alert).to_have_count(0)
            expect(page.get_by_test_id("forecast-page")).to_be_visible()
        finally:
            page.close()

if __name__ == "__main__":
    unittest.main()
