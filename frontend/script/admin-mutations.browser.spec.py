"""Mounted admin deletion regression: python script/admin-mutations.browser.spec.py."""

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


class AdminMutationsBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/admin-mutations.browser.html"
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
                    raise RuntimeError("Vite exited before mutation fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Mutation fixture did not become ready")
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

    def test_http_and_rpc_failures_keep_confirmation_open_until_success(self):
        page = self.browser.new_page(viewport={"width": 1000, "height": 700})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        try:
            page.goto(self.url)
            page.get_by_role("button", name="Delete", exact=True).click()
            dialog = page.get_by_role("dialog", name="Delete")
            confirm = dialog.get_by_role("button", name="Confirm delete")
            for status, body, message in [
                (500, {"status": "error", "message": "storage unavailable"}, "storage unavailable"),
                (401, {"status": "error", "message": "Please sign in"}, "Please sign in"),
                (200, {"status": "error", "message": "RPC rejected"}, "RPC rejected"),
            ]:
                page.evaluate("([status, body]) => window.mutationFixture.respond(status, body)", [status, body])
                confirm.click()
                expect(page.get_by_text(f"Error: {message}")).to_be_visible()
                expect(dialog).to_be_visible()
                self.assertEqual(page.evaluate("window.mutationFixture.refreshes()"), 0)
            page.evaluate("() => window.mutationFixture.respond(200, {status: 'success'})")
            confirm.click()
            expect(dialog).to_be_hidden()
            expect(page.get_by_text("Delete alpha")).to_be_visible()
            self.assertEqual(page.evaluate("window.mutationFixture.refreshes()"), 1)
            self.assertEqual(page.evaluate("window.mutationFixture.calls()"),
                             ["/api/admin/client/node-1/remove"] * 4)
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_edit_and_billing_preserve_forms_after_failed_save(self):
        page = self.browser.new_page(viewport={"width": 1000, "height": 800})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        try:
            page.goto(self.url)
            page.get_by_role("button", name="Edit info").click()
            edit = page.get_by_role("dialog", name="Edit info")
            name = edit.get_by_placeholder("请输入名称")
            name.fill("renamed alpha")
            page.evaluate("() => window.mutationFixture.respond(500, {status: 'error', message: 'write failed'})")
            edit.get_by_role("button", name="Save", exact=True).click()
            expect(page.get_by_text("Error: write failed")).to_be_visible()
            expect(edit).to_be_visible()
            self.assertEqual(name.input_value(), "renamed alpha")
            self.assertEqual(page.evaluate("window.mutationFixture.refreshes()"), 0)
            page.evaluate("() => window.mutationFixture.respond(200, {status: 'success'})")
            edit.get_by_role("button", name="Save", exact=True).click()
            expect(edit).to_be_hidden()
            self.assertEqual(page.evaluate("window.mutationFixture.refreshes()"), 1)

            page.get_by_role("button", name="Billing").click()
            billing = page.get_by_role("dialog", name="Billing")
            price = billing.locator('input[name="price"]')
            price.fill("17")
            page.evaluate("() => window.mutationFixture.respond(401, {status: 'error', message: 'Session expired'})")
            billing.get_by_role("button", name="Save", exact=True).click()
            expect(page.get_by_text("Failed to save billing information:Error: Session expired")).to_be_visible()
            expect(billing).to_be_visible()
            self.assertEqual(price.input_value(), "17")
            self.assertEqual(page.evaluate("window.mutationFixture.refreshes()"), 1)
            page.evaluate("() => window.mutationFixture.respond(200, {status: 'success'})")
            billing.get_by_role("button", name="Save", exact=True).click()
            expect(billing).to_be_hidden()
            self.assertEqual(page.evaluate("window.mutationFixture.refreshes()"), 2)
            self.assertEqual(page.evaluate("window.mutationFixture.calls()"),
                             ["/api/admin/client/node-1/edit"] * 4)
            self.assertEqual(errors, [])
        finally:
            page.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
