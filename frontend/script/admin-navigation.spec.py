"""Exercise the shipped admin bundle against an installed local test server.

Unlike mounted component tests, this checks the embedded archive and real API.
It creates one disposable node per test; loopback-only to avoid production writes.
Usage: python script/admin-navigation.spec.py http://127.0.0.1:25884
"""
import json
import os
import re
import sys
import unittest
from urllib.parse import urlparse
from uuid import uuid4

from playwright.sync_api import expect, sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:25884").rstrip("/")
ORIGIN = urlparse(BASE)
if ORIGIN.hostname not in ("127.0.0.1", "localhost", "::1"):
    raise SystemExit("This mutating regression requires a disposable loopback instance.")


class AdminNavigationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.playwright = sync_playwright().start()
        cls.browser = cls.playwright.chromium.launch()

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()

    def watch_page(self, page):
        page.on("pageerror", lambda error: self.problems.append(str(error)))
        page.on("response", lambda response: self.problems.append(
            f"{response.status} {response.url}"
        ) if response.status >= 400 and urlparse(response.url).netloc == ORIGIN.netloc else None)
        page.on("console", lambda message: self.problems.append(message.text)
                if message.type == "error" and "Failed to load resource" not in message.text else None)

    def setUp(self):
        self.context = self.browser.new_context(viewport={"width": 1280, "height": 900})
        self.problems = []
        self.context.on("page", self.watch_page)
        self.page = self.context.new_page()
        self.page.goto(BASE, wait_until="networkidle")
        login_status = self.page.evaluate("""async ([username, password]) => {
            const response = await fetch('/api/login', {
                method: 'POST', headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({username, password}), credentials: 'same-origin'
            });
            return response.status;
        }""", [os.environ.get("PANEL_USER", "admin"), os.environ.get("PANEL_PASSWORD", "E2e-verify1")])
        self.assertEqual(login_status, 200)
        me = self.page.request.get(BASE + "/api/me")
        self.assertTrue(me.json().get("logged_in"))
        self.name = "nav-" + uuid4().hex[:8]
        response = self.page.request.post(BASE + "/api/admin/client/add",
                                          data=json.dumps({"name": self.name}),
                                          headers={"Content-Type": "application/json"})
        self.assertEqual(response.status, 200)
        self.node_uuid = response.json()["uuid"]
        self.page.goto(BASE + "/admin/servers", wait_until="networkidle")
        eula = self.page.locator(".km-admin-eula-dialog")
        if eula.count():
            # Radix portals the inner content outside the styled dialog wrapper.
            self.page.get_by_role("button", name=re.compile("I have read and accept|我已详细阅读并接受")).click()
            expect(eula).to_be_hidden()
        self.problems.clear()

    def tearDown(self):
        self.context.close()

    def assert_healthy(self, page):
        self.assertNotIn("/404", page.url)
        self.assertNotIn("failed to slot", page.inner_text("body"))
        self.assertEqual(self.problems, [])

    def open_details(self):
        row = self.page.locator("tbody tr", has_text=self.name)
        expect(row).to_have_count(1)
        row.get_by_text(self.name, exact=True).click()
        dialog = self.page.get_by_role("dialog")
        expect(dialog).to_be_visible()
        expect(dialog.get_by_text(self.name, exact=True)).to_be_visible()
        expect(dialog.get_by_text(re.compile("Machine details|机器详细信息"))).to_be_visible()
        self.page.keyboard.press("Escape")
        expect(dialog).to_be_hidden()
        self.assert_healthy(self.page)

    def test_shipped_server_details_desktop(self):
        self.open_details()

    def test_shipped_server_details_mobile(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.open_details()

    def test_workbench_sidebar_click_opens_terminal_not_theme_404(self):
        with self.context.expect_page() as opened:
            self.page.locator('a[href="/terminal"]').first.click()
        terminal = opened.value
        terminal.wait_for_load_state("networkidle")
        expect(terminal.locator(".km-page-terminal")).to_be_visible()
        self.assertEqual(urlparse(terminal.url).path, "/terminal")
        terminal.reload(wait_until="networkidle")
        expect(terminal.locator(".km-page-terminal")).to_be_visible()
        self.assert_healthy(terminal)

    def test_terminal_direct_and_trailing_slash(self):
        for path in ("/terminal", "/terminal/"):
            with self.subTest(path=path):
                self.page.goto(BASE + path, wait_until="networkidle")
                expect(self.page.locator(".km-page-terminal")).to_be_visible()
                self.assertEqual(urlparse(self.page.url).path, path)
                self.assert_healthy(self.page)


if __name__ == "__main__":
    unittest.main(verbosity=2, argv=[sys.argv[0]])
