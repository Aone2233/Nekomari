"""Mounted regression for the bulk edit page (roadmap H2).

Run with `python script/bulk-edit.browser.spec.py`.

The assertions follow H2's acceptance criteria, and the one that matters most is not about
failure handling at all:

  * **a field whose switch is off is not in the request** - sending every field would set a
    whole fleet's group to the empty string the first time anyone pressed apply, so the spec
    reads the payload the page actually produced
  * **a partial failure is reported per node, with the applier's own reason** - "3 of 10
    failed" without saying which three is a report the operator has to re-derive by hand
  * **the selection survives the list being replaced**, because the list polls and a selection
    keyed on row order would silently move onto different nodes
  * a node that has left the list is dropped from the selection rather than applied to

English resources only: what is asserted is which fields are sent and which nodes are named.
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


class BulkEditBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}/script/bulk-edit.browser.html"
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
                raise RuntimeError("Bulk fixture did not become ready")
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
        expect(page.locator('[data-testid="bulk-page"]')).to_be_visible()
        return page

    def teardown_page(self, page):
        try:
            self.assertEqual(self.errors, [])
        finally:
            page.close()

    # Two selector traps cost time here and are worth naming: Radix renders a Checkbox as a
    # button rather than an <input>, and TextField.Root puts the test id on the wrapper rather
    # than on the field - so `input[type=checkbox]` and `"..." input` both match nothing.

    def toggle(self, page, field: str):
        page.locator(f'[data-testid="bulk-field-{field}-toggle"]').click()

    def input(self, page, field: str):
        return page.locator(f'[data-testid="bulk-input-{field}"]')

    def calls(self, page):
        return page.evaluate("window.__bulkCalls ?? []")

    def test_only_switched_on_fields_are_sent(self):
        page = self.open("ready")
        try:
            # Two fields on, seven off.
            self.toggle(page, "group")
            self.input(page, "group").fill("apac")
            self.toggle(page, "weight")
            self.input(page, "weight").fill("9")

            expect(page.locator('[data-testid="bulk-field-count"]')).to_have_attribute("data-count", "2")
            expect(page.locator('[data-testid="bulk-field-group"]')).to_have_attribute("data-enabled", "true")
            expect(page.locator('[data-testid="bulk-field-tags"]')).to_have_attribute("data-enabled", "false")

            page.locator('[data-testid="bulk-select-all"]').click()
            page.locator('[data-testid="bulk-apply"]').click()
            expect(page.locator('[data-testid="bulk-report"]')).to_be_visible()

            calls = self.calls(page)
            self.assertEqual(len(calls), 1, calls)
            update = calls[0]["update"]
            # The assertion the page exists for: exactly the enabled fields, nothing else.
            self.assertEqual(sorted(update.keys()), ["group", "weight"], update)
            self.assertEqual(update["group"], "apac")
            self.assertEqual(update["weight"], 9)
            # And never the selector.
            self.assertNotIn("uuid", update)
        finally:
            self.teardown_page(page)

    def test_apply_is_disabled_until_something_would_change(self):
        page = self.open("ready")
        try:
            apply_button = page.locator('[data-testid="bulk-apply"]')
            # Nothing selected, no fields on.
            expect(apply_button).to_be_disabled()

            page.locator('[data-testid="bulk-select-all"]').click()
            expect(apply_button).to_be_disabled()  # still no fields

            self.toggle(page, "group")
            expect(apply_button).to_be_enabled()

            page.locator('[data-testid="bulk-select-none"]').click()
            expect(apply_button).to_be_disabled()  # selection gone again
        finally:
            self.teardown_page(page)

    def test_partial_failure_is_reported_per_node(self):
        page = self.open("partial")
        try:
            self.toggle(page, "trafficLimit")
            self.input(page, "trafficLimit").fill("-1")
            page.locator('[data-testid="bulk-select-all"]').click()
            page.locator('[data-testid="bulk-apply"]').click()

            report = page.locator('[data-testid="bulk-report"]')
            expect(report).to_be_visible()
            expect(report).to_have_attribute("data-applied", "2")
            expect(report).to_have_attribute("data-failed", "1")

            failures = page.locator('[data-testid="bulk-failure"]')
            expect(failures).to_have_count(1)
            # Named, and carrying the applier's own reason rather than a generic one.
            expect(failures.first).to_contain_text("Frankfurt Box")
            expect(failures.first).to_contain_text(
                "traffic_limit must be a valid non-negative int64 value")
            # The successes are still reported as successes: no rollback.
            expect(page.locator('[data-testid="bulk-report-summary"]')).to_contain_text("2 applied")
        finally:
            self.teardown_page(page)

    def test_selection_survives_the_list_being_replaced(self):
        page = self.open("refresh")
        try:
            # Select the first and third rows, then replace the list as a poll would: same
            # uuids, new order and new field values.
            rows = page.locator('[data-testid="bulk-node-row"]')
            rows.nth(0).locator('[data-testid="bulk-node-checkbox"]').click()
            rows.nth(2).locator('[data-testid="bulk-node-checkbox"]').click()
            expect(page.locator('[data-testid="bulk-selection-count"]')).to_have_attribute("data-count", "2")

            page.evaluate(
                """window.__setNodes([
                     { uuid: "uuid-c", name: "Silent Node", group: "asia", weight: 3, hidden: true },
                     { uuid: "uuid-b", name: "Frankfurt Box", group: "eu", weight: 2, hidden: false },
                     { uuid: "uuid-a", name: "Tokyo Relay", group: "apac", weight: 9, hidden: false },
                   ])"""
            )

            # Still two, and still the same *nodes* rather than the same row positions.
            expect(page.locator('[data-testid="bulk-selection-count"]')).to_have_attribute("data-count", "2")
            expect(page.locator('[data-uuid="uuid-a"]')).to_have_attribute("data-selected", "true")
            expect(page.locator('[data-uuid="uuid-c"]')).to_have_attribute("data-selected", "true")
            expect(page.locator('[data-uuid="uuid-b"]')).to_have_attribute("data-selected", "false")

            self.toggle(page, "group")
            page.locator('[data-testid="bulk-apply"]').click()
            expect(page.locator('[data-testid="bulk-report"]')).to_be_visible()
            calls = self.calls(page)
            self.assertEqual(sorted(calls[0]["uuids"]), ["uuid-a", "uuid-c"], calls)
        finally:
            self.teardown_page(page)

    def test_a_node_that_left_the_list_drops_out_of_the_selection(self):
        page = self.open("refresh")
        try:
            page.locator('[data-testid="bulk-select-all"]').click()
            expect(page.locator('[data-testid="bulk-selection-count"]')).to_have_attribute("data-count", "3")

            # uuid-b disappears, as a deleted node would.
            page.evaluate(
                """window.__setNodes([
                     { uuid: "uuid-a", name: "Tokyo Relay", group: "asia", weight: 1, hidden: false },
                     { uuid: "uuid-c", name: "Silent Node", group: "asia", weight: 3, hidden: true },
                   ])"""
            )
            expect(page.locator('[data-testid="bulk-selection-count"]')).to_have_attribute("data-count", "2")

            self.toggle(page, "group")
            page.locator('[data-testid="bulk-apply"]').click()
            expect(page.locator('[data-testid="bulk-report"]')).to_be_visible()
            calls = self.calls(page)
            self.assertNotIn("uuid-b", calls[0]["uuids"], calls)
        finally:
            self.teardown_page(page)

    def test_loading_state_says_so(self):
        page = self.open("loading")
        try:
            expect(page.locator('[data-testid="bulk-loading"]')).to_be_visible()
            expect(page.locator('[data-testid="bulk-node-row"]')).to_have_count(0)
        finally:
            self.teardown_page(page)

    def test_a_successful_apply_shows_no_error(self):
        page = self.open("ready")
        try:
            self.toggle(page, "group")
            self.input(page, "group").fill("apac")
            page.locator('[data-testid="bulk-select-all"]').click()
            page.locator('[data-testid="bulk-apply"]').click()
            expect(page.locator('[data-testid="bulk-report"]')).to_be_visible()
            # The throwing path is the component's own try/catch; what this pins is that a
            # success does not render an error beside it.
            expect(page.locator('[data-testid="bulk-error"]')).to_have_count(0)
        finally:
            self.teardown_page(page)


if __name__ == "__main__":
    unittest.main(verbosity=2)
