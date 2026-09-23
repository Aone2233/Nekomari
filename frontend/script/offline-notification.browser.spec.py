"""Mounted offline notification save regressions: python script/offline-notification.browser.spec.py."""

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


class OfflineNotificationBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/offline-notification.browser.html"
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
                    raise RuntimeError("Vite exited before offline notification fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Offline notification fixture did not become ready")
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

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 1200, "height": 800})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.add_init_script("""
            window.saveResponses = [];
            window.savePayloads = [];
            window.offlineFetchCount = 0;
            window.queueSave = response => window.saveResponses.push(response);
            window.fetch = async (input, init) => {
              const url = String(input);
              const json = body => new Response(JSON.stringify(body), {
                status: 200, headers: {'Content-Type': 'application/json'},
              });
              if (url === '/api/admin/client/list') {
                return json([
                  {uuid: 'alpha', name: 'Alpha', weight: 0, billing_cycle: 0},
                  {uuid: 'beta', name: 'Beta', weight: 1, billing_cycle: 0},
                  {uuid: 'gamma', name: 'Gamma', weight: 2, billing_cycle: 0},
                ]);
              }
              if (url === '/api/admin/notification/offline') {
                window.offlineFetchCount++;
                return json({status: 'success', data: [
                  {client: 'alpha', enable: false, grace_period: 300, last_notified: null},
                  {client: 'beta', enable: true, grace_period: 450, last_notified: null},
                ]});
              }
              if (url === '/api/admin/notification/offline/edit') {
                window.savePayloads.push(JSON.parse(init.body));
                const next = window.saveResponses.shift();
                if (!next) throw new Error('No queued save response');
                return new Response(JSON.stringify(next.body), {
                  status: next.httpStatus ?? 200,
                  headers: {'Content-Type': 'application/json'},
                });
              }
              throw new Error('Unexpected request: ' + url);
            };
        """)
        self.page.goto(self.url)
        self.page.get_by_role("row", name="Beta Enabled 450seconds - Edit").wait_for()

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    def save(self, dialog, response):
        self.page.evaluate("response => window.queueSave(response)", response)
        dialog.get_by_role("button", name="Save").click()

    def test_batch_failures_keep_dialog_and_draft_until_success(self):
        self.page.locator("thead [role=checkbox]").click()
        self.page.get_by_role("button", name="Batch edit").click()
        dialog = self.page.get_by_role("dialog", name="Batch edit")
        grace = dialog.locator("input#grace_period")
        grace.fill("987")

        for response, message in [
            ({"httpStatus": 401, "body": {"status": "error", "message": "Session expired"}}, "Session expired"),
            ({"httpStatus": 500, "body": {"status": "error", "message": "Database unavailable"}}, "Database unavailable"),
            ({"body": {"status": "error", "message": "RPC rejected"}}, "RPC rejected"),
        ]:
            self.save(dialog, response)
            expect(self.page.get_by_text(message, exact=True)).to_be_visible()
            expect(dialog).to_be_visible()
            expect(grace).to_have_value("987")
            self.assertEqual(self.page.evaluate("window.offlineFetchCount"), 1)

        self.save(dialog, {"body": {"status": "success"}})
        expect(dialog).not_to_be_visible()
        self.page.wait_for_function("window.offlineFetchCount === 2")
        payloads = self.page.evaluate("window.savePayloads")
        self.assertEqual(len(payloads), 4)
        for payload in payloads:
            self.assertEqual([item["client"] for item in payload], ["alpha", "beta", "gamma"])
            self.assertEqual([item["grace_period"] for item in payload], [987, 987, 987])

    def test_single_edit_failure_keeps_draft_for_retry(self):
        self.page.get_by_role("row", name="Alpha Disabled 300seconds - Edit").get_by_role("button", name="Edit").click()
        dialog = self.page.get_by_role("dialog", name="Edit")
        grace = dialog.locator("input#grace_period")
        grace.fill("123")
        self.save(dialog, {"httpStatus": 500, "body": {"status": "error", "message": "Save failed"}})
        expect(self.page.get_by_text("Save failed", exact=True)).to_be_visible()
        expect(dialog).to_be_visible()
        expect(grace).to_have_value("123")
        self.assertEqual(self.page.evaluate("window.offlineFetchCount"), 1)
        self.save(dialog, {"body": {"status": "success"}})
        expect(dialog).not_to_be_visible()
        self.page.wait_for_function("window.offlineFetchCount === 2")
        self.assertEqual(self.page.evaluate("window.savePayloads[1][0].grace_period"), 123)

    def test_single_edit_for_node_without_configuration_uses_node_id(self):
        self.page.get_by_role("row", name="Gamma Disabled 300seconds - Edit").get_by_role("button", name="Edit").click()
        dialog = self.page.get_by_role("dialog", name="Edit")
        self.save(dialog, {"body": {"status": "success"}})
        expect(dialog).not_to_be_visible()
        self.assertEqual(self.page.evaluate("window.savePayloads[0][0].client"), "gamma")


if __name__ == "__main__":
    unittest.main(verbosity=2)
