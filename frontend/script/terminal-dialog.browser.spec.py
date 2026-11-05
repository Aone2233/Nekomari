"""Keyboard and focus regression tests against the real mounted TerminalDialog."""

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


class TerminalDialogBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/terminal-dialog.browser.html"
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
                    raise RuntimeError("Vite exited before the dialog fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Dialog fixture did not become ready")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception as error:
            cls.stop()
            cls.output.seek(0)
            log = cls.output.read()[-4000:]
            cls.output.close()
            raise RuntimeError(f"{error}\nVite log:\n{log}") from error

    @classmethod
    def stop(cls):
        if cls.browser is not None:
            cls.browser.close()
        if cls.playwright is not None:
            cls.playwright.stop()
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
        cls.stop()
        cls.output.close()

    def open_page(self, mobile=False):
        page = self.browser.new_page(viewport={"width": 390, "height": 844} if mobile else {"width": 1200, "height": 800})
        page.goto(self.url)
        page.get_by_test_id("outside").wait_for()
        self.addCleanup(page.close)
        return page

    def test_highlighted_suggestions_do_not_hijack_cancel_enter(self):
        page = self.open_page()
        page.get_by_test_id("outside").click()
        dialog = page.get_by_role("dialog", name="Quick open")
        expect(dialog.get_by_placeholder("File path")).to_be_focused()
        dialog.get_by_role("button", name="Cancel").focus()
        page.keyboard.press("Enter")
        expect(dialog).to_have_count(0)
        expect(page.get_by_test_id("results")).to_have_attribute("data-cancel", "1")
        expect(page.get_by_test_id("results")).to_have_attribute("data-submit", "0")
        expect(page.get_by_test_id("results")).to_have_attribute("data-selected", "")
        expect(page.get_by_test_id("outside")).to_be_focused()

    def test_suggestion_button_enter_activates_that_button(self):
        page = self.open_page()
        page.get_by_test_id("outside").click()
        dialog = page.get_by_role("dialog", name="Quick open")
        dialog.get_by_role("button", name="beta.txt").focus()
        page.keyboard.press("Enter")
        expect(page.get_by_test_id("results")).to_have_attribute("data-selected", "beta.txt")
        expect(page.get_by_test_id("results")).to_have_attribute("data-submit", "1")

    def test_suggestion_input_arrow_navigation_and_enter(self):
        page = self.open_page()
        page.get_by_test_id("outside").click()
        dialog = page.get_by_role("dialog", name="Quick open")
        field = dialog.get_by_placeholder("File path")
        expect(field).to_be_focused()
        page.keyboard.press("ArrowDown")
        page.keyboard.press("Enter")
        expect(page.get_by_test_id("results")).to_have_attribute("data-selected", "beta.txt")
        expect(page.get_by_test_id("results")).to_have_attribute("data-submit", "1")
        expect(field).to_have_value("beta.txt")

    def test_destructive_default_focus_traps_and_restores(self):
        page = self.open_page()
        page.get_by_test_id("delete").click()
        dialog = page.get_by_role("alertdialog", name="Delete item")
        cancel = dialog.get_by_role("button", name="Cancel")
        delete = dialog.get_by_role("button", name="Delete")
        expect(cancel).to_be_focused()
        self.assertEqual(dialog.get_attribute("aria-modal"), "true")
        self.assertEqual(dialog.get_attribute("aria-describedby"), dialog.get_by_text("This cannot be undone.").get_attribute("id"))
        page.keyboard.press("Shift+Tab")
        expect(delete).to_be_focused()
        page.keyboard.press("Tab")
        expect(cancel).to_be_focused()
        page.keyboard.press("Enter")
        expect(dialog).to_have_count(0)
        expect(page.get_by_test_id("results")).to_have_attribute("data-submit", "0")
        expect(page.get_by_test_id("delete")).to_be_focused()

    def test_escape_outside_click_and_mobile_focus(self):
        page = self.open_page(mobile=True)
        page.get_by_test_id("outside").click()
        dialog = page.get_by_role("dialog", name="Quick open")
        field = dialog.get_by_placeholder("File path")
        expect(field).to_be_focused()
        page.mouse.click(5, 5)
        expect(dialog).to_be_visible()
        field.focus()
        page.keyboard.press("Shift+Tab")
        expect(dialog.get_by_role("button", name="OK")).to_be_focused()
        page.keyboard.press("Tab")
        expect(field).to_be_focused()
        page.keyboard.press("Escape")
        expect(dialog).to_have_count(0)
        expect(page.get_by_test_id("outside")).to_be_focused()
        expect(page.get_by_test_id("results")).to_have_attribute("data-outer-escape", "0")

    def test_submitting_disables_confirmation_but_cancel_works(self):
        page = self.open_page()
        page.get_by_test_id("busy").click()
        page.get_by_test_id("outside").click()
        dialog = page.get_by_role("dialog", name="Quick open")
        expect(dialog.get_by_role("button", name="OK")).to_be_disabled()
        expect(dialog.get_by_role("button", name="alpha.txt")).to_be_disabled()
        page.keyboard.press("Enter")
        expect(page.get_by_test_id("results")).to_have_attribute("data-submit", "0")
        expect(page.get_by_test_id("results")).to_have_attribute("data-selected", "")
        expect(dialog.get_by_placeholder("File path")).to_have_value("")
        dialog.get_by_role("button", name="Cancel").focus()
        page.keyboard.press("Enter")
        expect(dialog).to_have_count(0)
        expect(page.get_by_test_id("results")).to_have_attribute("data-cancel", "1")

    def test_nested_dialog_escape_only_closes_top_and_restores_parent(self):
        page = self.open_page()
        page.get_by_test_id("nested").click()
        parent = page.get_by_role("alertdialog", name="Parent dialog")
        child_trigger = parent.get_by_role("button", name="Open child")
        child_trigger.click()
        child = page.get_by_role("alertdialog", name="Child dialog")
        expect(child.get_by_role("button", name="Cancel")).to_be_focused()
        page.keyboard.press("Escape")
        expect(child).to_have_count(0)
        expect(page.get_by_test_id("results")).to_have_attribute("data-submit", "0")
        expect(parent).to_be_visible()
        expect(child_trigger).to_be_focused()
        page.wait_for_timeout(100)
        expect(parent).to_be_visible()
        expect(page.get_by_test_id("results")).to_have_attribute("data-submit", "0")
        page.keyboard.press("Escape")
        expect(parent).to_have_count(0)
        expect(page.get_by_test_id("nested")).to_be_focused()

    def test_removed_trigger_does_not_leave_modal_or_block_focus(self):
        page = self.open_page()
        page.get_by_test_id("removed").click()
        dialog = page.get_by_role("alertdialog", name="Delete item")
        expect(dialog.get_by_role("button", name="Cancel")).to_be_focused()
        page.keyboard.press("Escape")
        expect(dialog).to_have_count(0)
        page.get_by_test_id("outside").focus()
        expect(page.get_by_test_id("outside")).to_be_focused()

    def test_unmount_open_dialog_restores_focus(self):
        page = self.open_page()
        trigger = page.get_by_test_id("unmount")
        trigger.click()
        dialog = page.get_by_role("alertdialog", name="Unmount on confirm")
        expect(dialog.get_by_role("button", name="OK")).to_be_focused()
        page.keyboard.press("Enter")
        expect(dialog).to_have_count(0)
        expect(trigger).to_be_focused()
        page.get_by_test_id("outside").focus()
        expect(page.get_by_test_id("outside")).to_be_focused()


if __name__ == "__main__":
    unittest.main(verbosity=2)
