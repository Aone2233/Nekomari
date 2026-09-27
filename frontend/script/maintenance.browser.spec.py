"""Mounted regression for the maintenance window page (roadmap H3).

Run with `python script/maintenance.browser.spec.py`.

The assertions follow H3's acceptance criteria. The one that matters most is not about
suppression at all — it is that **an open window is unambiguous on the page**, because an operator
cannot check the notifier from here. A list that showed a closed window as open, or a fleet-wide
window as scoped, would leave someone believing alerts are suppressed when they are not.

The other two:

  * **a refusal keeps what the operator described.** Clearing the form on a rejected save makes them
    type the times again, and the refusal is the moment they most need to see what they wrote.
  * **the scope is visible per row**, because "every node" and "these three" are different claims
    and the fleet-wide one is easy to create by accident.

English resources only: what is asserted is which state a row reports.
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


class MaintenanceBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}/script/maintenance.browser.html"
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
                    with urlopen(cls.base + "?state=ready", timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Maintenance fixture did not become ready")
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

    def open(self, state: str):
        page = self.browser.new_page()
        self.errors = []
        page.on("pageerror", lambda error: self.errors.append(str(error)))
        page.goto(f"{self.base}?state={state}")
        expect(page.locator('[data-testid="maintenance-page"]')).to_be_visible()
        return page

    def teardown_page(self, page):
        try:
            self.assertEqual(self.errors, [])
        finally:
            page.close()

    def test_open_and_closed_windows_are_distinguishable(self):
        page = self.open("ready")
        try:
            rows = page.locator('[data-testid="maintenance-row"]')
            expect(rows).to_have_count(3)

            # Two open, one closed — and the closed one says so rather than showing an empty
            # "remaining" that reads as open.
            expect(page.locator('[data-testid="maintenance-open-count"]')).to_have_attribute("data-count", "2")
            expect(page.locator('[data-id="1"]')).to_have_attribute("data-open", "true")
            expect(page.locator('[data-id="2"]')).to_have_attribute("data-open", "true")
            expect(page.locator('[data-id="3"]')).to_have_attribute("data-open", "false")
            expect(page.locator('[data-id="3"] [data-testid="maintenance-row-state"]')).to_contain_text("closed")
            expect(page.locator('[data-id="1"] [data-testid="maintenance-row-state"]')).to_contain_text("open")
        finally:
            self.teardown_page(page)

    def test_scope_is_visible_per_row(self):
        page = self.open("ready")
        try:
            # The fleet-wide window and the scoped one are different claims and must not look the
            # same: creating a fleet-wide window by accident is easy.
            expect(page.locator('[data-id="1"]')).to_have_attribute("data-scope", "all")
            expect(page.locator('[data-id="2"]')).to_have_attribute("data-scope", "scoped")
            expect(page.locator('[data-id="2"] [data-testid="maintenance-row-scope"]')).to_contain_text("uuid-a")
        finally:
            self.teardown_page(page)

    def test_a_refused_save_keeps_what_was_typed(self):
        page = self.open("refused")
        try:
            page.locator('[data-testid="maintenance-input-name"]').fill("bad window")
            page.locator('[data-testid="maintenance-input-start"]').fill("2026-09-27T20:00")
            page.locator('[data-testid="maintenance-input-end"]').fill("2026-09-27T19:00")
            page.locator('[data-testid="maintenance-save"]').click()

            error = page.locator('[data-testid="maintenance-error"]')
            expect(error).to_be_visible()
            # The server's own reason, not a generic one.
            expect(error).to_contain_text("end must be after start")

            # The form still holds the window, so the operator can fix the time rather than
            # describe the whole thing again.
            expect(page.locator('[data-testid="maintenance-input-name"]')).to_have_value("bad window")
            expect(page.locator('[data-testid="maintenance-input-start"]')).to_have_value("2026-09-27T20:00")

            # And it was sent, with the timezone attached rather than a naive timestamp: the server
            # refuses those, and a window that starts at the wrong hour suppresses the wrong hour.
            saves = page.evaluate("window.__maintenanceSaves ?? []")
            self.assertEqual(len(saves), 1, saves)
            self.assertTrue(saves[0]["start"].endswith("Z"), saves[0]["start"])
            self.assertTrue(saves[0]["end"].endswith("Z"), saves[0]["end"])
        finally:
            self.teardown_page(page)

    def test_a_successful_save_clears_the_form(self):
        page = self.open("ready")
        try:
            page.locator('[data-testid="maintenance-input-name"]').fill("new window")
            page.locator('[data-testid="maintenance-input-start"]').fill("2026-09-28T02:00")
            page.locator('[data-testid="maintenance-input-end"]').fill("2026-09-28T03:00")
            page.locator('[data-testid="maintenance-save"]').click()

            expect(page.locator('[data-testid="maintenance-error"]')).to_have_count(0)
            # Cleared only on success: a second window is a new description, not an edit.
            expect(page.locator('[data-testid="maintenance-input-name"]')).to_have_value("")
            expect(page.locator('[data-testid="maintenance-draft-id"]')).to_have_attribute("data-id", "0")
        finally:
            self.teardown_page(page)

    def test_editing_loads_the_window_into_the_form(self):
        page = self.open("ready")
        try:
            page.locator('[data-id="2"] [data-testid="maintenance-edit"]').click()
            expect(page.locator('[data-testid="maintenance-draft-id"]')).to_have_attribute("data-id", "2")
            expect(page.locator('[data-testid="maintenance-input-name"]')).to_have_value("switch swap")
            # And the scope came with it: an edit that silently widened a scoped window to the whole
            # fleet would suppress alerts on nodes the operator never chose.
            expect(page.locator('[data-testid="maintenance-draft-scope"]')).to_have_attribute("data-all", "false")
        finally:
            self.teardown_page(page)

    def test_deleting_sends_the_id(self):
        page = self.open("ready")
        try:
            page.locator('[data-id="3"] [data-testid="maintenance-delete"]').click()
            deletes = page.evaluate("window.__maintenanceDeletes ?? []")
            self.assertEqual(deletes, [3], deletes)
        finally:
            self.teardown_page(page)

    def test_empty_and_loading_states_say_so(self):
        for state, testid in (("empty", "maintenance-empty"), ("loading", "maintenance-loading")):
            page = self.open(state)
            try:
                expect(page.locator(f'[data-testid="{testid}"]')).to_be_visible()
                expect(page.locator('[data-testid="maintenance-row"]')).to_have_count(0)
            finally:
                self.teardown_page(page)

    def test_save_is_disabled_until_the_window_is_described(self):
        page = self.open("ready")
        try:
            button = page.locator('[data-testid="maintenance-save"]')
            expect(button).to_be_disabled()  # no name, no times

            page.locator('[data-testid="maintenance-input-name"]').fill("partial")
            expect(button).to_be_disabled()  # still no times

            page.locator('[data-testid="maintenance-input-start"]').fill("2026-09-28T02:00")
            page.locator('[data-testid="maintenance-input-end"]').fill("2026-09-28T03:00")
            expect(button).to_be_enabled()
        finally:
            self.teardown_page(page)


if __name__ == "__main__":
    unittest.main(verbosity=2)
