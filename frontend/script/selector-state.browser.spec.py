"""Mounted selector-dialog draft regressions: python script/selector-state.browser.spec.py."""

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


class SelectorStateBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/selector-state.browser.html"
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
                    raise RuntimeError("Vite exited before selector fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Selector fixture did not become ready")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception as error:
            cls._stop()
            cls.output.seek(0)
            vite_log = cls.output.read()[-4000:]
            cls.output.close()
            raise RuntimeError(f"{error}\nVite log:\n{vite_log}") from error

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
        expect(self.page.get_by_role("button", name="Choose generic")).to_be_visible()

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    def test_generic_cancel_confirm_and_external_value_update(self):
        self.page.get_by_role("button", name="Choose generic").click()
        dialog = self.page.get_by_role("dialog", name="Generic nodes")
        expect(dialog.get_by_role("checkbox", name="Select alpha")).to_be_checked()
        dialog.get_by_role("checkbox", name="Select beta").click()
        expect(dialog.get_by_role("checkbox", name="Select beta")).to_be_checked()
        self.page.evaluate("window.selectorFixture.rerender()")
        expect(dialog.get_by_role("checkbox", name="Select beta")).to_be_checked()
        dialog.get_by_role("button", name="Cancel").click()
        expect(dialog).to_be_hidden()
        expect(self.page.locator("#generic-value")).to_have_text("alpha")
        expect(self.page.locator("#generic-commits")).to_have_text("0")

        self.page.get_by_role("button", name="Choose generic").click()
        expect(dialog.get_by_role("checkbox", name="Select beta")).not_to_be_checked()
        dialog.get_by_role("checkbox", name="Select beta").click()
        self.page.evaluate("window.selectorFixture.setGenericValue(['gamma'])")
        expect(dialog.get_by_role("checkbox", name="Select gamma")).to_be_checked()
        expect(dialog.get_by_role("checkbox", name="Select alpha")).not_to_be_checked()
        expect(dialog.get_by_role("checkbox", name="Select beta")).not_to_be_checked()
        dialog.get_by_role("checkbox", name="Select alpha").click()
        dialog.get_by_role("button", name="Done").click()
        expect(dialog).to_be_hidden()
        expect(self.page.locator("#generic-value")).to_have_text("gamma,alpha")
        expect(self.page.locator("#generic-commits")).to_have_text("1")

    def test_node_uncontrolled_open_cancel_confirm(self):
        self.page.get_by_role("button", name="Choose nodes").click()
        dialog = self.page.get_by_role("dialog", name="Uncontrolled nodes")
        dialog.get_by_role("checkbox", name="Select beta").click()
        self.page.evaluate("window.selectorFixture.rerender()")
        expect(dialog.get_by_role("checkbox", name="Select beta")).to_be_checked()
        dialog.get_by_role("button", name="Cancel").click()
        expect(dialog).to_be_hidden()
        expect(self.page.locator("#node-value")).to_have_text("alpha")
        expect(self.page.locator("#node-commits")).to_have_text("0")

        self.page.get_by_role("button", name="Choose nodes").click()
        expect(dialog.get_by_role("checkbox", name="Select beta")).not_to_be_checked()
        self.page.evaluate("window.selectorFixture.setNodeValue(['gamma'])")
        expect(dialog.get_by_role("checkbox", name="Select gamma")).to_be_checked()
        expect(dialog.get_by_role("checkbox", name="Select alpha")).not_to_be_checked()
        dialog.get_by_role("checkbox", name="Select beta").click()
        dialog.get_by_role("button", name="Done").click()
        expect(dialog).to_be_hidden()
        expect(self.page.locator("#node-value")).to_have_text("gamma,beta")
        expect(self.page.locator("#node-commits")).to_have_text("1")

    def test_node_parent_controls_open_without_trigger(self):
        dialog = self.page.get_by_role("dialog", name="Controlled nodes")
        self.page.evaluate("window.selectorFixture.setControlledOpen(true)")
        expect(dialog).to_be_visible()
        expect(dialog.get_by_role("checkbox", name="Select alpha")).to_be_checked()
        dialog.get_by_role("checkbox", name="Select beta").click()
        self.page.evaluate("window.selectorFixture.rerender()")
        expect(dialog.get_by_role("checkbox", name="Select beta")).to_be_checked()
        self.page.evaluate("window.selectorFixture.setControlledOpen(false)")
        expect(dialog).to_be_hidden()
        self.page.evaluate("window.selectorFixture.setControlledOpen(true)")
        expect(dialog).to_be_visible()
        expect(dialog.get_by_role("checkbox", name="Select beta")).not_to_be_checked()
        self.page.evaluate("window.selectorFixture.setControlledValue(['gamma'])")
        expect(dialog.get_by_role("checkbox", name="Select gamma")).to_be_checked()
        expect(dialog.get_by_role("checkbox", name="Select alpha")).not_to_be_checked()
        dialog.get_by_role("button", name="Cancel").click()
        expect(self.page.locator("#controlled-open")).to_have_text("false")
        expect(self.page.locator("#controlled-commits")).to_have_text("0")
        self.page.evaluate("window.selectorFixture.setControlledOpen(true)")
        dialog.get_by_role("checkbox", name="Select beta").click()
        dialog.get_by_role("button", name="Done").click()
        expect(self.page.locator("#controlled-open")).to_have_text("false")
        expect(self.page.locator("#controlled-value")).to_have_text("gamma,beta")
        expect(self.page.locator("#controlled-commits")).to_have_text("1")


if __name__ == "__main__":
    unittest.main(verbosity=2)
