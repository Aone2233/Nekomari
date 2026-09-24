"""Authenticated, server-backed regression for two representative admin write paths.

Boots nothing itself: point it at an already-running, already-installed instance
(the same one script/panel-smoke.spec.py walks) and it logs in as the real admin
and drives the real SPA against the real API:

    python script/admin-write-paths.spec.py [base_url]

This is the complement to the mounted fixtures in this directory. Those mount one
component against stubs; script/panel-smoke.spec.py proves every installed route
renders; neither executes an authenticated admin mutation against a server. Each
test here forces its mutation to fail once -- Playwright fulfils it with a 500 --
and then asserts that the dialog and the draft survive for a retry, that the
failure reaches the user, and that the retry is what the server actually holds,
proved with a fresh read rather than with a success toast.

Two paths, per docs/FOLLOWUP-v0.1.25.md:

  * a node edit (pages/admin/index.tsx EditButton -> POST /api/admin/client/:uuid/edit)
  * an offline-notification save (pages/admin/notification/offline.tsx ->
    POST /api/admin/notification/offline/edit), including the first-configuration
    case where the node ID used to be omitted from the payload

Selectors come from the two pages and the URLs from web/router/router.go; both
are named at their use site below. The instance's UI language decides the labels,
so the few text selectors accept the Chinese and English variants the panel ships.

Environment:
    PANEL_USER, PANEL_PASSWORD   credentials for the instance (default admin/E2e-verify1)
"""
import json
import os
import sys
import unittest
from urllib.parse import urlparse
from uuid import uuid4

from playwright.sync_api import expect, sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else os.environ.get("PANEL_BASE_URL", "http://127.0.0.1:25774")).rstrip("/")
USER = os.environ.get("PANEL_USER", "admin")
PASSWORD = os.environ.get("PANEL_PASSWORD", "E2e-verify1")

HOST = urlparse(BASE).netloc

# What the test injects. Both call sites surface the message the server returned,
# so that message is what must appear in the UI.
NODE_EDIT_ERROR = "E2E forced node edit failure"
OFFLINE_SAVE_ERROR = "E2E forced offline save failure"

NODE_EDIT_ROUTE = "**/api/admin/client/*/edit"
OFFLINE_SAVE_ROUTE = "**/api/admin/notification/offline/edit"

# The node table renders an empty-state guide instead of rows until a node
# exists, and the offline page lists one row per node, so each test seeds one.
# pages/admin/index.tsx:2491   EditButton    -> IconButton title={nodeEdit.editInfo}
# pages/admin/notification/offline.tsx:404  ActionButtons -> IconButton aria-label={common.edit}
NODE_EDIT_BUTTON = 'button[title="编辑信息"], button[title="Edit information"]'
OFFLINE_EDIT_BUTTON = 'button[aria-label="编辑"], button[aria-label="Edit"]'


def same_origin(url: str) -> bool:
    return urlparse(url).netloc == HOST


def safe(text) -> str:
    """Keep a GBK console from turning page text (emoji) into a crash."""
    encoding = sys.stdout.encoding or "utf-8"
    return str(text).encode(encoding, "replace").decode(encoding)


def save_button(dialog):
    """The dialog's submit control, in either language the panel ships."""
    button = dialog.get_by_role("button", name="保存", exact=True)
    if button.count() == 0:
        button = dialog.get_by_role("button", name="Save", exact=True)
    return button.first


class AdminWritePathTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.playwright = sync_playwright().start()
        cls.browser = cls.playwright.chromium.launch()

    @classmethod
    def tearDownClass(cls):
        try:
            cls.browser.close()
        finally:
            cls.playwright.stop()

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 1280, "height": 900})
        self.console_errors: list[str] = []
        self.failed_requests: list[str] = []
        self.bad_responses: list[str] = []
        # URLs this test answered with a 5xx on purpose, so the same-origin
        # failure discipline below does not count the injected failure itself.
        self.intentional: list[str] = []
        # Some call sites log the caught error with console.error (offline.tsx:432).
        # A console message carrying the injected message can only come from the
        # injected failure, so it is tolerated; anything else still fails.
        self.tolerated_console: list[str] = []
        self.page.on("console", self._on_console)
        self.page.on("pageerror", lambda error: self.console_errors.append(f"pageerror: {error}"))
        self.page.on("requestfailed", self._on_requestfailed)
        self.page.on("response", self._on_response)
        self.login()

    def tearDown(self):
        self.page.close()

    # --- the console-error / failed-request discipline panel-smoke uses ---

    def _on_console(self, message):
        # The browser emits a URL-less "Failed to load resource" console error for
        # every 4xx/5xx, including the 500 this test forces. Same-origin HTTP
        # failures are collected precisely from the response event instead, so
        # that generic message is dropped and only real JS exceptions remain.
        if message.type not in ("error", "pageerror") or "Failed to load resource" in message.text:
            return
        if any(marker in message.text for marker in self.tolerated_console):
            return
        self.console_errors.append(f"{message.type}: {message.text}")

    def _on_requestfailed(self, request):
        if same_origin(request.url):
            self.failed_requests.append(f"{request.url} :: {request.failure}")

    def _on_response(self, response):
        if response.status >= 400 and same_origin(response.url) and response.url not in self.intentional:
            self.bad_responses.append(f"{response.status} {response.url}")

    def assert_no_page_problems(self):
        self.assertEqual(self.console_errors, [], "same-origin console errors")
        self.assertEqual(self.bad_responses, [], "same-origin 4xx/5xx responses")
        self.assertEqual(self.failed_requests, [], "same-origin failed requests")

    # --- reaching the real admin UI ---

    def login(self):
        """The login walk script/panel-smoke.spec.py uses, against the real dialog."""
        page = self.page
        page.goto(BASE, wait_until="networkidle", timeout=30000)
        page.wait_for_timeout(1200)
        trigger = page.get_by_role("button", name="登录")
        if trigger.count() == 0:
            trigger = page.get_by_role("button", name="Login")
        self.assertGreater(trigger.count(), 0, "no login button found on the landing page")
        trigger.first.click()
        page.wait_for_selector('[role="dialog"]', timeout=15000)
        dialog = page.locator('[role="dialog"]')
        dialog.locator("input").nth(0).fill(USER)
        dialog.locator('input[type="password"]').fill(PASSWORD)
        dialog.get_by_role("button", name="登录").or_(dialog.get_by_role("button", name="Login")).first.click()
        page.wait_for_timeout(2500)
        self.assertIn("/admin", page.url, f"login did not reach an admin route (landed on {page.url})")
        # The landing page's own in-flight requests are cancelled by the SPA's
        # post-login navigation, the same blind spot panel-smoke has by clearing
        # per route. The discipline below starts at the admin UI.
        self.console_errors.clear()
        self.failed_requests.clear()
        self.bad_responses.clear()
        self.dismiss_eula()

    def dismiss_eula(self):
        """The admin layout blocks on a legal-notice modal for Chinese-language
        instances (pages/admin/_layout.tsx). Accepting it is a real settings write
        that persists, so on an instance that already accepted it this is a no-op."""
        eula = self.page.locator(".km-admin-eula-dialog")
        if eula.count() == 0:
            return
        accept = self.page.get_by_role("button", name="我已详细阅读并接受", exact=True)
        if accept.count() == 0:
            accept = self.page.get_by_role("button", name="I have read and accept", exact=True)
        self.assertGreater(accept.count(), 0, "EULA modal offered no accept button")
        accept.first.click()
        expect(eula).to_be_hidden(timeout=15000)

    def create_node(self, name):
        """Seed one node through the real admin API (the panel's own add path).

        The session cookie is shared with page.request, so this is the same
        authenticated write the UI makes."""
        response = self.page.request.post(
            BASE + "/api/admin/client/add",
            data=json.dumps({"name": name}),
            headers={"Content-Type": "application/json"},
        )
        self.assertEqual(response.status, 200, response.text()[:300])
        body = response.json()
        self.assertEqual(body.get("status"), "success", body)
        return body["uuid"]

    def fail_first_mutation(self, route, message, status=500):
        """Answer the first matching mutation with a 5xx, then pass retries through.

        Returns the list of decoded request payloads, one per attempt."""
        payloads: list[object] = []
        self.tolerated_console.append(message)

        def handler(intercepted, request):
            payloads.append(json.loads(request.post_data or "null"))
            if len(payloads) == 1:
                self.intentional.append(request.url)
                intercepted.fulfill(
                    status=status,
                    content_type="application/json",
                    body=json.dumps({"status": "error", "message": message}),
                )
            else:
                intercepted.continue_()

        self.page.route(route, handler)
        return payloads

    # --- the two write paths ---

    def test_node_edit_rejects_keeps_draft_retries_and_persists(self):
        """pages/admin/index.tsx:2457-2487 EditButton.save -> POST /api/admin/client/:uuid/edit."""
        # Both names stay under 25 characters: the name cell truncates longer ones
        # (pages/admin/index.tsx:2649), so a longer name is not findable as text.
        suffix = uuid4().hex
        node_name = f"e2e-edit-{suffix[:8]}"
        edited_name = f"e2e-edited-{suffix[8:16]}"
        node_uuid = self.create_node(node_name)

        self.page.goto(BASE + "/admin/servers", wait_until="networkidle", timeout=30000)
        row = self.page.locator("tbody tr", has_text=node_name)
        expect(row).to_have_count(1, timeout=15000)
        row.locator(NODE_EDIT_BUTTON).first.click()

        dialog = self.page.locator('[role="dialog"]')
        expect(dialog).to_be_visible(timeout=15000)
        # The dialog's first field is 名称/Name, prefilled with the node it was
        # opened for: asserting the pre-state proves we opened the right dialog.
        name_input = dialog.locator("input").first
        expect(name_input).to_have_value(node_name, timeout=15000)
        name_input.fill(edited_name)

        attempts = self.fail_first_mutation(NODE_EDIT_ROUTE, NODE_EDIT_ERROR)
        save_button(dialog).click()

        # The failure is reported, and neither the dialog nor the draft is lost.
        expect(self.page.locator("[data-sonner-toast]", has_text=NODE_EDIT_ERROR).first).to_be_visible(timeout=15000)
        expect(dialog).to_be_visible()
        expect(name_input).to_have_value(edited_name)
        self.assertEqual(len(attempts), 1)
        self.assertEqual(attempts[0]["name"], edited_name)

        save_button(dialog).click()
        expect(dialog).to_be_hidden(timeout=15000)
        self.assertEqual(len(attempts), 2, "the retry did not reach the server")
        self.assertEqual(attempts[1]["name"], edited_name, "the retry did not re-send the draft")

        # Persistence, from a fresh read of the server rather than from the toast.
        readback = self.page.request.get(f"{BASE}/api/admin/client/{node_uuid}")
        self.assertEqual(readback.status, 200, readback.text()[:300])
        self.assertEqual(readback.json().get("name"), edited_name, readback.text()[:300])

        # And the panel renders the persisted value after a full reload, with the
        # pre-edit name gone.
        self.page.goto(BASE + "/admin/servers", wait_until="networkidle", timeout=30000)
        expect(self.page.locator("tbody tr", has_text=edited_name)).to_have_count(1, timeout=15000)
        expect(self.page.locator("tbody tr", has_text=node_name)).to_have_count(0)

        self.page.unroute(NODE_EDIT_ROUTE)
        print(safe(f"node edit: forced {NODE_EDIT_ERROR!r} on attempt 1, persisted name={edited_name!r}"))
        self.assert_no_page_problems()

    def test_offline_notification_save_rejects_keeps_draft_retries_and_persists(self):
        """pages/admin/notification/offline.tsx:421-437 ActionButtons -> POST
        /api/admin/notification/offline/edit, for a node with no configuration yet."""
        node_name = f"E2E-offline-{uuid4().hex[:8]}"
        node_uuid = self.create_node(node_name)
        grace_period = 777

        self.page.goto(BASE + "/admin/notification/offline", wait_until="networkidle", timeout=30000)
        row = self.page.locator("tbody tr", has_text=node_name)
        expect(row).to_have_count(1, timeout=15000)
        row.locator(OFFLINE_EDIT_BUTTON).first.click()

        dialog = self.page.locator('[role="dialog"]')
        expect(dialog).to_be_visible(timeout=15000)
        toggle = dialog.locator('[role="switch"]')
        grace_input = dialog.locator("input#grace_period")
        # No configuration row exists yet for this node, so the form starts from
        # its defaults -- this is the "first offline-notification configuration".
        expect(toggle).to_have_attribute("aria-checked", "false", timeout=15000)
        expect(grace_input).to_have_value("300", timeout=15000)
        toggle.click()
        expect(toggle).to_have_attribute("aria-checked", "true")
        grace_input.fill(str(grace_period))

        attempts = self.fail_first_mutation(OFFLINE_SAVE_ROUTE, OFFLINE_SAVE_ERROR)
        save_button(dialog).click()

        expect(self.page.locator("[data-sonner-toast]", has_text=OFFLINE_SAVE_ERROR).first).to_be_visible(timeout=15000)
        expect(dialog).to_be_visible()
        expect(grace_input).to_have_value(str(grace_period))
        expect(toggle).to_have_attribute("aria-checked", "true")
        self.assertEqual(len(attempts), 1)
        # The regression docs/FOLLOWUP-v0.1.25.md calls out: a node getting its
        # first configuration must name itself in the payload.
        self.assertEqual([item["client"] for item in attempts[0]], [node_uuid])

        save_button(dialog).click()
        expect(dialog).to_be_hidden(timeout=15000)
        self.assertEqual(len(attempts), 2, "the retry did not reach the server")
        self.assertEqual([item["client"] for item in attempts[1]], [node_uuid])
        self.assertEqual(attempts[1][0]["grace_period"], grace_period)

        # Persistence, from a fresh read of the server rather than from the toast.
        readback = self.page.request.get(BASE + "/api/admin/notification/offline")
        self.assertEqual(readback.status, 200, readback.text()[:300])
        stored = [n for n in readback.json()["data"] if n["client"] == node_uuid]
        self.assertEqual(len(stored), 1, readback.text()[:300])
        self.assertTrue(stored[0]["enable"], stored[0])
        self.assertEqual(stored[0]["grace_period"], grace_period, stored[0])

        # And the panel renders the persisted configuration after a full reload.
        self.page.goto(BASE + "/admin/notification/offline", wait_until="networkidle", timeout=30000)
        row = self.page.locator("tbody tr", has_text=node_name)
        expect(row).to_have_count(1, timeout=15000)
        expect(row).to_contain_text(str(grace_period))

        self.page.unroute(OFFLINE_SAVE_ROUTE)
        print(safe(f"offline save: forced {OFFLINE_SAVE_ERROR!r} on attempt 1, persisted client={node_uuid} grace_period={grace_period}"))
        self.assert_no_page_problems()


if __name__ == "__main__":
    unittest.main(verbosity=2, argv=[sys.argv[0]])
