"""Mounted node-token rotation: explicit recovery consent, TOTP and no replay."""

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


class ClientTokenBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/client-token.browser.html"
        cls.output = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        cls.server = subprocess.Popen(
            ["node", str(ROOT / "node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
            cwd=ROOT, stdout=cls.output, stderr=subprocess.STDOUT,
        )
        try:
            for _ in range(100):
                if cls.server.poll() is not None:
                    raise RuntimeError("Vite exited before fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1):
                        break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Client token fixture did not become ready")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception:
            cls.server.terminate()
            cls.server.wait(timeout=5)
            cls.output.seek(0)
            print(cls.output.read()[-4000:])
            cls.output.close()
            raise

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()
        cls.server.terminate()
        cls.server.wait(timeout=5)
        cls.output.close()

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 1100, "height": 850})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))

    def tearDown(self):
        self.page.close()
        self.assertEqual(self.errors, [])

    def open(self, query=""):
        self.page.goto(self.url + query)
        self.page.get_by_role("button", name="Rotate agent token", exact=True).click()

    def calls(self):
        return self.page.evaluate("clientTokenFixture.calls")

    def test_recovery_consent_and_secret_disposal(self):
        self.open()
        page = self.page
        confirm = page.get_by_role("button", name="Invalidate old token and rotate", exact=True)
        expect(confirm).to_be_disabled()
        expect(page.get_by_role("dialog")).to_contain_text("independent recovery channel")
        self.assertEqual(self.calls(), [])
        page.get_by_role("checkbox").check()
        confirm.click()
        expect(page.get_by_label("New agent token", exact=True)).to_have_value("na_fixture-only-token")
        self.assertEqual(len(self.calls()), 1)
        self.assertEqual(self.calls()[0]["body"], {})
        page.get_by_role("button", name="I have stored the new token", exact=True).click()
        expect(page.get_by_label("New agent token", exact=True)).to_have_count(0)
        page.get_by_role("button", name="Rotate agent token", exact=True).click()
        expect(page.get_by_role("checkbox")).not_to_be_checked()
        expect(confirm).to_be_disabled()
        self.assertFalse(page.evaluate("document.body.textContent.includes('na_fixture-only-token')"))
        self.assertFalse(page.evaluate("Object.values(localStorage).some(v => v.includes('na_fixture'))"))
        self.assertFalse(page.evaluate("Object.values(sessionStorage).some(v => v.includes('na_fixture'))"))

    def test_totp_and_busy_prevent_double_submission(self):
        self.open("?tfa=1&hold=1")
        page = self.page
        page.get_by_role("checkbox").check()
        confirm = page.get_by_role("button", name="Invalidate old token and rotate", exact=True)
        expect(confirm).to_be_disabled()
        page.get_by_label("Please enter the one-time password from your authenticator app").fill("123456")
        confirm.dblclick()
        expect(confirm).to_be_disabled()
        page.keyboard.press("Escape")
        expect(page.get_by_role("dialog")).to_be_visible()
        self.assertEqual(len(self.calls()), 1)
        self.assertEqual(self.calls()[0]["body"], {"2fa_code": "123456"})
        page.evaluate("clientTokenFixture.release()")
        expect(page.get_by_label("New agent token", exact=True)).to_be_visible()

    def test_uncertain_or_business_refusal_never_replays(self):
        for query in ["?fail=1&tfa=1", "?refuse=1&tfa=1"]:
            with self.subTest(query=query):
                self.open(query)
                page = self.page
                page.get_by_role("checkbox").check()
                otp = page.get_by_label("Please enter the one-time password from your authenticator app")
                otp.fill("123456")
                page.get_by_role("button", name="Invalidate old token and rotate", exact=True).click()
                expect(page.get_by_role("alert")).to_contain_text("old token may already be invalid")
                expect(otp).to_have_value("")
                expect(page.get_by_role("checkbox")).not_to_be_checked()
                expect(page.get_by_label("New agent token", exact=True)).to_have_count(0)
                self.assertEqual(len(self.calls()), 1)
                self.assertEqual(page.evaluate("clientTokenFixture.state.refreshes"), 1)

    def test_mobile_dark_dialog_stays_in_page(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.open("?dark=1")
        expect(self.page.locator(".radix-themes.dark [role=dialog]")).to_be_visible()
        self.assertEqual(len(self.page.context.pages), 1)
        self.assertTrue(self.page.evaluate("document.documentElement.scrollWidth <= innerWidth"))


if __name__ == "__main__":
    unittest.main()
