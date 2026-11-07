"""Mounted regression for the configuration export/import page (roadmap H4).

Run with `python script/config-export.browser.spec.py`.

The assertion this feature lives on is not about rendering: **Import is locked until a check has
been shown for the document currently in the box.** An import rewrites nodes, tasks, maintenance
windows and settings at once, and a page that offered only "Import" would be asking the operator to
trust a file they cannot read. The rest follows from it:

  * editing the document **invalidates the check**, because a plan for a different document is worse
    than no plan — it looks like one
  * **removals are shown as "not in the document"**, not as something the import will do, because the
    server never deletes on import
  * **"include credentials" says what it does**, since the file that comes out carries agent tokens
  * a refusal reaches the page rather than failing silently

English resources only: what is asserted is which controls are available and what the plan says.
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


class ConfigExportBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}/script/config-export.browser.html"
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
                raise RuntimeError("Config fixture did not become ready")
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
        expect(page.locator('[data-testid="config-page"]')).to_be_visible()
        return page

    def teardown_page(self, page):
        try:
            self.assertEqual(self.errors, [])
        finally:
            page.close()

    def text(self, page, value: str):
        page.locator('[data-testid="config-document-text"]').fill(value)

    def test_export_fills_the_document(self):
        page = self.open("ready")
        try:
            page.locator('[data-testid="config-export-button"]').click()
            expect(page.locator('[data-testid="config-document-text"]')).not_to_have_value("")
            expect(page.locator('[data-testid="config-export-scope"]')).to_contain_text("database connection details")
            value = page.locator('[data-testid="config-document-text"]').input_value()
            self.assertIn("schema_version", value)
            # The default export carries no credential: the file is a configuration, not a keyring.
            self.assertNotIn("agent-token-a", value)
        finally:
            self.teardown_page(page)

    def test_disabled_import_still_allows_export(self):
        page = self.open("unavailable")
        try:
            page.locator('[data-testid="config-export-button"]').click()
            expect(page.locator('[data-testid="config-import-unavailable"]')).to_contain_text("encrypted database backup")
            expect(page.locator('[data-testid="config-plan-button"]')).to_be_disabled()
            expect(page.locator('[data-testid="config-import-button"]')).to_be_disabled()
            self.assertEqual(page.evaluate("window.__configCalls.plan || 0"), 0)
            self.assertEqual(page.evaluate("window.__configCalls.import || 0"), 0)
        finally:
            self.teardown_page(page)

    def test_include_credentials_says_what_it_does(self):
        page = self.open("ready")
        try:
            expect(page.locator('[data-testid="config-secrets-warning"]')).to_have_count(0)
            page.locator('[data-testid="config-include-secrets"]').click()
            expect(page.locator('[data-testid="config-secrets-warning"]')).to_be_visible()

            page.locator('[data-testid="config-export-button"]').click()
            value = page.locator('[data-testid="config-document-text"]').input_value()
            # The warning is not decoration: the file really does carry the token now.
            self.assertIn("agent-token-a", value)
        finally:
            self.teardown_page(page)

    def test_pinned_export_warns_that_it_is_not_a_database_backup(self):
        page = self.open("pinned")
        try:
            page.locator('[data-testid="config-export-button"]').click()
            warning = page.locator('[data-testid="config-pinned-archive-warning"]')
            expect(warning).to_be_visible()
            expect(warning).to_contain_text("Schema 3 supports")
            expect(warning).to_contain_text("encrypted database backup")
            self.assertIn('"target_family": "ipv6"',
                          page.locator('[data-testid="config-document-text"]').input_value())
            self.text(page, '{"schema_version": 3, "ping_tasks": [{"target_family": "ipv6"}]}')
            expect(warning).to_be_visible()
            self.text(page, '{"schema_version": 3, "ping_tasks": [{"target_family": ""}]}')
            expect(warning).to_have_count(0)
            self.text(page, '{"schema_version": 1}')
            expect(warning).to_have_count(0)
        finally:
            self.teardown_page(page)

    def test_import_is_locked_until_a_check_has_been_shown(self):
        page = self.open("ready")
        try:
            self.text(page, '{"schema_version": 1, "clients": []}')

            import_button = page.locator('[data-testid="config-import-button"]')
            # No check yet: an import rewrites four entities at once and is not a button to press on
            # faith.
            expect(import_button).to_be_disabled()
            expect(page.locator('[data-testid="config-needs-plan"]')).to_be_visible()

            page.locator('[data-testid="config-plan-button"]').click()
            expect(page.locator('[data-testid="config-plan"]')).to_be_visible()
            expect(import_button).to_be_enabled()
        finally:
            self.teardown_page(page)

    def test_editing_the_document_invalidates_the_check(self):
        page = self.open("ready")
        try:
            self.text(page, '{"schema_version": 1, "clients": []}')
            page.locator('[data-testid="config-plan-button"]').click()
            expect(page.locator('[data-testid="config-plan"]')).to_be_visible()
            expect(page.locator('[data-testid="config-import-button"]')).to_be_enabled()

            # One character changed: the plan shown is now about a different document, so it is
            # withdrawn and the import locks again.
            self.text(page, '{"schema_version": 1, "clients": [{"uuid": "x"}]}')
            expect(page.locator('[data-testid="config-plan"]')).to_have_count(0)
            expect(page.locator('[data-testid="config-import-button"]')).to_be_disabled()
        finally:
            self.teardown_page(page)

    def test_original_document_text_reaches_check_and_import(self):
        page = self.open("ready")
        try:
            original = '{"schema_version":3,"clients":[{"traffic_limit":9007199254740993,"name":"a","name":"b"}]}'
            self.text(page, original)
            page.locator('[data-testid="config-plan-button"]').click()
            self.assertEqual(page.evaluate("window.__configCalls.plannedText"), original)
            page.locator('[data-testid="config-import-button"]').click()
            self.assertEqual(page.evaluate("window.__configCalls.importedText"), original)
        finally:
            self.teardown_page(page)

    def test_review_token_and_second_factor_reach_import(self):
        page = self.open("ready")
        try:
            self.text(page, '{"schema_version": 3}')
            page.locator('[data-testid="config-plan-button"]').click()
            page.locator('[data-testid="config-import-2fa"]').fill("123456")
            page.locator('[data-testid="config-import-button"]').click()
            self.assertEqual(page.evaluate("window.__configCalls.reviewToken"), "a" * 64)
            self.assertEqual(page.evaluate("window.__configCalls.importCode"), "123456")
            expect(page.locator('[data-testid="config-import-2fa"]')).to_have_count(0)
        finally:
            self.teardown_page(page)

    def test_missing_review_token_keeps_import_locked(self):
        page = self.open("missing-token")
        try:
            self.text(page, '{"schema_version": 3}')
            page.locator('[data-testid="config-plan-button"]').click()
            expect(page.locator('[data-testid="config-error"]')).to_contain_text("valid review token")
            expect(page.locator('[data-testid="config-import-button"]')).to_be_disabled()
            self.assertEqual(page.evaluate("window.__configCalls.import || 0"), 0)
        finally:
            self.teardown_page(page)

    def test_the_plan_names_what_would_change(self):
        page = self.open("ready")
        try:
            self.text(page, '{"schema_version": 1}')
            page.locator('[data-testid="config-plan-button"]').click()

            plan = page.locator('[data-testid="config-plan"]')
            expect(plan).to_have_attribute("data-creates", "2")
            expect(plan).to_have_attribute("data-updates", "1")
            expect(plan).to_have_attribute("data-unchanged", "3")

            rows = page.locator('[data-testid="config-plan-row"]')
            expect(rows).to_have_count(4)
            # The update names the field it would change, so an operator can see it without reading
            # the file.
            update = page.locator('[data-testid="config-plan-row"][data-kind="update"]')
            expect(update).to_have_count(1)
            expect(update).to_contain_text("group")
        finally:
            self.teardown_page(page)

    def test_removals_are_reported_as_not_deleted(self):
        page = self.open("removals")
        try:
            self.text(page, '{"schema_version": 1}')
            page.locator('[data-testid="config-plan-button"]').click()

            plan = page.locator('[data-testid="config-plan"]')
            expect(plan).to_have_attribute("data-removals", "2")
            # One row per removal, labelled as something the import will not do.
            removals = page.locator('[data-testid="config-plan-row"][data-kind="remove"]')
            expect(removals).to_have_count(2)
            expect(removals.first).to_contain_text("not in the document")
            # And the warning says the same thing in words.
            expect(page.locator('[data-testid="config-plan-warning"]').first).to_contain_text(
                "an import does not delete them")

            page.locator('[data-testid="config-import-button"]').click()
            expect(page.locator('[data-testid="config-result"]')).to_be_visible()
            expect(page.locator('[data-testid="config-result-removals"]')).to_contain_text(
                "left alone")
        finally:
            self.teardown_page(page)

    def test_implicit_default_task_assignment_is_visible(self):
        page = self.open("implicit")
        try:
            self.text(page, '{"schema_version": 3}')
            page.locator('[data-testid="config-plan-button"]').click()
            effect = page.locator('[data-testid="config-implicit-assignment"]')
            expect(effect).to_contain_text("New nodes will also be assigned")
            expect(effect.locator("xpath=ancestor::tr")).to_contain_text("Existing task")
        finally:
            self.teardown_page(page)

    def test_a_refusal_reaches_the_page(self):
        page = self.open("refused")
        try:
            self.text(page, '{"schema_version": 2}')
            page.locator('[data-testid="config-plan-button"]').click()
            page.locator('[data-testid="config-import-button"]').click()

            error = page.locator('[data-testid="config-error"]')
            expect(error).to_be_visible()
            # The server's own reason, not a generic message.
            expect(error).to_contain_text("schema_version 2")
        finally:
            self.teardown_page(page)

    def test_import_failure_requires_a_new_review_before_retry(self):
        for state, message in (("committed", "Configuration saved; do not retry"), ("ambiguous", "connection closed after send")):
            with self.subTest(state=state):
                page = self.open(state)
                try:
                    self.text(page, '{"schema_version": 1}')
                    page.locator('[data-testid="config-plan-button"]').click()
                    button = page.locator('[data-testid="config-import-button"]')
                    expect(button).to_be_enabled()
                    button.click()
                    expect(page.locator('[data-testid="config-error"]')).to_contain_text(message)
                    expect(button).to_be_disabled()
                    expect(page.locator('[data-testid="config-plan"]')).to_have_count(0)
                    self.assertEqual(page.evaluate("window.__configCalls.import"), 1)
                finally:
                    self.teardown_page(page)

    def test_invalid_json_is_explained_and_blocks_both_buttons(self):
        page = self.open("ready")
        try:
            self.text(page, "{not json")
            expect(page.locator('[data-testid="config-parse-error"]')).to_be_visible()
            expect(page.locator('[data-testid="config-plan-button"]')).to_be_disabled()
            expect(page.locator('[data-testid="config-import-button"]')).to_be_disabled()
        finally:
            self.teardown_page(page)


if __name__ == "__main__":
    unittest.main(verbosity=2)
