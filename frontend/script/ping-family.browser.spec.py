"""Mounted ping-family creation/edit regression: python script/ping-family.browser.spec.py."""

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


class PingFamilyBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/ping-family.browser.html"
        cls.output = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        cls.server = cls.playwright = cls.browser = None
        try:
            cls.server = subprocess.Popen(
                ["node", str(ROOT / "node_modules/vite/bin/vite.js"),
                 "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
                cwd=ROOT, stdout=cls.output, stderr=subprocess.STDOUT,
            )
            for _ in range(100):
                if cls.server.poll() is not None:
                    raise RuntimeError("Vite exited before the ping fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Ping fixture did not become ready")
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

    def open_page(self, viewport):
        page = self.browser.new_page(viewport=viewport)
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(self.url)
        expect(page.locator("#draft").get_by_role("button", name="Add")).to_be_visible()
        return page, errors

    def test_gated_create_and_failure_handling(self):
        for viewport in [{"width": 1200, "height": 900}, {"width": 390, "height": 844}]:
            with self.subTest(viewport=viewport):
                page, errors = self.open_page(viewport)
                try:
                    page.locator("#legacy").get_by_role("button", name="Add").click()
                    dialog = page.get_by_role("dialog", name="Add")
                    expect(dialog.get_by_text("Target address family")).to_have_count(0)
                    dialog.get_by_role("button", name="Close").click()
                    page.locator("#draft").get_by_role("button", name="Add").click()
                    expect(dialog.get_by_text("A compatible Agent with a WebSocket connection is required", exact=False)).to_be_visible()
                    dialog.locator("#ping_name").fill("Tianjin IPv6")
                    dialog.locator("#ping_target").fill("tj-ct-v6.ip.zstaticcdn.com:80")
                    dialog.locator("#type").click()
                    page.get_by_role("option", name="Auto (probe the target and pick a protocol it answers)").click()
                    dialog.locator("#target_family").click()
                    page.get_by_role("option", name="IPv6", exact=True).click()
                    dialog.get_by_role("button", name="Select", exact=True).click()
                    selector = page.get_by_role("dialog", name="Select")
                    selector.get_by_role("button", name="Select all").click()
                    selector.get_by_role("button", name="Done").click()
                    expect(dialog.get_by_text("3 selected")).to_be_visible()
                    self.assertEqual(page.evaluate("window.pingFamilyFixture.calls()"), [])

                    page.evaluate("window.pingFamilyFixture.respond(200, {status:'error', message:'draft rejected'})")
                    dialog.get_by_role("button", name="Add", exact=True).click()
                    expect(page.get_by_text("draft rejected")).to_be_visible()
                    expect(dialog).to_be_visible()
                    self.assertEqual(page.evaluate("window.pingFamilyFixture.refreshes()"), 0)
                    page.evaluate("window.pingFamilyFixture.respond(200, {status:'success', data:{task_id:8}})")
                    dialog.get_by_role("button", name="Add", exact=True).click()
                    expect(dialog).to_be_hidden()
                    calls = page.evaluate("window.pingFamilyFixture.calls()")
                    self.assertEqual(len(calls), 2)
                    self.assertEqual(calls[0], calls[1])
                    self.assertEqual(calls[0], {
                        "url": "/api/admin/ping/add",
                        "body": {"name": "Tianjin IPv6", "type": "auto",
                                 "target": "tj-ct-v6.ip.zstaticcdn.com:80", "reference": "",
                                 "default_on": False, "clients": ["alpha", "beta", "gamma"],
                                 "interval": 60, "target_family": "ipv6",
                                 "probe_attempts": 3, "probe_spacing_ms": 200,
                                 "http_mode": "legacy", "http_expected_status": "200-399",
                                 "ip_version_preference": "auto"},
                    })
                    self.assertEqual(page.evaluate("window.pingFamilyFixture.refreshes()"), 1)
                    self.assertEqual(errors, [])
                finally:
                    page.close()

    def test_pinned_edits_keep_identity_and_membership_payload(self):
        page, errors = self.open_page({"width": 1200, "height": 900})
        try:
            expect(page.get_by_text("example.test:80 (IPV6)")).to_be_visible()
            page.get_by_role("button", name="Edit", exact=True).first.click()
            dialog = page.get_by_role("dialog", name="Edit")
            expect(dialog.get_by_role("combobox")).to_be_disabled()
            expect(dialog.locator('input[value="example.test:80"]')).to_be_disabled()
            expect(dialog.locator('input[value="gateway"]')).to_be_disabled()
            expect(dialog.get_by_text("The target, protocol, reference and IPV6 family", exact=False)).to_be_visible()
            dialog.locator('input[value="IPv6 fixture"]').fill("Renamed")
            page.evaluate("window.pingFamilyFixture.respond(200, {status:'error', message:'edit rejected'})")
            dialog.get_by_role("button", name="Save").click()
            expect(page.get_by_text("edit rejected")).to_be_visible()
            expect(dialog).to_be_visible()
            page.evaluate("window.pingFamilyFixture.respond(200, {status:'success'})")
            dialog.get_by_role("button", name="Save").click()
            expect(dialog).to_be_hidden()
            calls = page.evaluate("window.pingFamilyFixture.calls()")
            edit = calls[-1]["body"]["tasks"][0]
            self.assertEqual(edit, {"id": 7, "name": "Renamed", "type": "auto", "target": "example.test:80",
                                    "reference": "gateway", "default_on": False,
                                    "clients": ["alpha", "beta"], "interval": 60,
                                    "probe_attempts": 0, "probe_spacing_ms": 200, "probe_timeout_ms": 0,
                                    "http_mode": "legacy", "http_expected_status": "200-399",
                                    "ip_version_preference": "auto"})

            row = page.locator(".km-pingtask-server-list tr").filter(has_text="gamma")
            row.get_by_role("button", name="Select tasks").click()
            server_dialog = page.get_by_role("dialog", name="Server - gamma")
            server_dialog.get_by_role("checkbox", name="Select 7").click()
            server_dialog.get_by_role("button", name="Save").click()
            expect(server_dialog).to_be_hidden()
            membership = page.evaluate("window.pingFamilyFixture.calls()")[-1]["body"]["tasks"][0]
            self.assertEqual(membership, {"id": 7, "name": "IPv6 fixture", "type": "auto",
                                          "target": "example.test:80", "default_on": False,
                                          "clients": ["alpha", "beta", "gamma"], "interval": 60})
            self.assertEqual(page.evaluate("window.pingFamilyFixture.refreshes()"), 2)
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_legacy_edit_explains_new_task_boundary(self):
        page, errors = self.open_page({"width": 1200, "height": 900})
        try:
            page.locator("#legacy-task").get_by_role("button", name="Edit", exact=True).click()
            dialog = page.get_by_role("dialog", name="Edit")
            expect(dialog.get_by_text("An existing legacy task cannot be switched", exact=False)).to_be_visible()
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_committed_reload_failure_closes_and_refreshes_without_retry(self):
        page, errors = self.open_page({"width": 1200, "height": 900})
        try:
            page.locator("#draft").get_by_role("button", name="Add").click()
            dialog = page.get_by_role("dialog", name="Add")
            dialog.locator("#ping_name").fill("Saved IPv6")
            dialog.locator("#ping_target").fill("example.test:80")
            dialog.get_by_role("button", name="Select", exact=True).click()
            selector = page.get_by_role("dialog", name="Select")
            selector.get_by_role("button", name="Select all").click()
            selector.get_by_role("button", name="Done").click()
            page.evaluate("window.pingFamilyFixture.respond(503, {status:'error', message:'saved, do not retry', committed:true, task_id:27})")
            dialog.get_by_role("button", name="Add", exact=True).click()
            expect(dialog).to_be_hidden()
            expect(page.get_by_text("saved, do not retry (ID: 27)")).to_be_visible()
            self.assertEqual(len(page.evaluate("window.pingFamilyFixture.calls()")), 1)
            self.assertEqual(page.evaluate("window.pingFamilyFixture.refreshes()"), 1)

            page.get_by_role("button", name="Edit", exact=True).first.click()
            edit = page.get_by_role("dialog", name="Edit")
            page.evaluate("window.pingFamilyFixture.respond(503, {status:'error', message:'edit saved', committed:true})")
            edit.get_by_role("button", name="Save").click()
            expect(edit).to_be_hidden()
            expect(page.get_by_text("edit saved")).to_be_visible()
            self.assertEqual(len(page.evaluate("window.pingFamilyFixture.calls()")), 2)
            self.assertEqual(page.evaluate("window.pingFamilyFixture.refreshes()"), 2)

            row = page.locator(".km-pingtask-server-list tr").filter(has_text="gamma")
            row.get_by_role("button", name="Select tasks").click()
            server = page.get_by_role("dialog", name="Server - gamma")
            server.get_by_role("checkbox", name="Select 7").click()
            page.evaluate("window.pingFamilyFixture.respond(503, {status:'error', message:'membership saved', committed:true})")
            server.get_by_role("button", name="Save").click()
            expect(server).to_be_hidden()
            expect(page.get_by_text("membership saved")).to_be_visible()
            self.assertEqual(len(page.evaluate("window.pingFamilyFixture.calls()")), 3)
            self.assertEqual(page.evaluate("window.pingFamilyFixture.refreshes()"), 3)

            page.get_by_role("button", name="Delete", exact=True).first.click()
            deletion = page.get_by_role("dialog", name="Delete")
            page.evaluate("window.pingFamilyFixture.respond(503, {status:'error', message:'deleted, cleanup pending', committed:true})")
            deletion.get_by_role("button", name="Delete", exact=True).click()
            expect(deletion).to_be_hidden()
            expect(page.get_by_text("deleted, cleanup pending")).to_be_visible()
            self.assertEqual(len(page.evaluate("window.pingFamilyFixture.calls()")), 4)
            self.assertEqual(page.evaluate("window.pingFamilyFixture.refreshes()"), 4)
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_schedule_refresh_ignores_older_success(self):
        page, errors = self.open_page({"width": 1200, "height": 900})
        try:
            page.wait_for_function("window.pingFamilyFixture.scheduleRequests() === 1")
            page.get_by_role("button", name="Refresh schedule").click()
            page.wait_for_function("window.pingFamilyFixture.scheduleRequests() === 2")
            page.evaluate("window.pingFamilyFixture.replySchedule(1, true)")
            expect(page.locator("#schedule-probe output")).to_have_text("true")
            page.evaluate("window.pingFamilyFixture.replySchedule(0, false)")
            expect(page.locator("#schedule-probe output")).to_have_text("true")
            page.get_by_role("button", name="Refresh schedule").click()
            page.wait_for_function("window.pingFamilyFixture.scheduleRequests() === 3")
            expect(page.locator("#schedule-probe output")).to_have_text("unknown")
            page.evaluate("window.pingFamilyFixture.replySchedule(2, false)")
            expect(page.locator("#schedule-probe output")).to_have_text("false")
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_diagnostic_requires_explicit_refresh_and_expires(self):
        for viewport in [{"width": 1200, "height": 900}, {"width": 390, "height": 844}]:
            with self.subTest(viewport=viewport):
                page, errors = self.open_page(viewport)
                try:
                    page.get_by_role("button", name="Inspect probe status").click()
                    dialog = page.get_by_role("dialog", name="Probe status")
                    self.assertEqual(page.evaluate("window.pingFamilyFixture.calls()"), [])
                    expect(dialog.get_by_text("Unknown or skipped is not packet loss", exact=False)).to_be_visible()
                    page.evaluate("""() => window.pingFamilyFixture.respond(200, {status:'success', data:{
                        state:'session_active', observed_at:'2026-11-05T18:00:00Z', valid_until:'2026-11-05T18:00:30Z',
                        dropped_updates:2, legs:[{role:'primary', leg:'auto', status:'no_family_address'},
                                                 {role:'reference', leg:'reference', status:'recorded'}]}})""")
                    dialog.get_by_role("button", name="Check status").click()
                    expect(dialog.get_by_text("Handshake active; probe execution not yet proven")).to_be_visible()
                    expect(dialog.get_by_text("No address in selected family; not probed", exact=False)).to_be_visible()
                    expect(dialog.get_by_text("A probe result was stored (not necessarily successful)", exact=False)).to_be_visible()
                    expect(dialog.get_by_text("2 diagnostic updates dropped", exact=False)).to_be_visible()
                    self.assertEqual(page.evaluate("window.pingFamilyFixture.calls()")[-1],
                                     {"url": "/api/admin/ping/diagnostic", "body": {"task_id": 7, "client": "alpha"}})
                    dialog.get_by_role("combobox", name="Server").click()
                    page.get_by_role("option", name="beta", exact=True).click()
                    expect(dialog.get_by_text("No address in selected family; not probed", exact=False)).to_have_count(0)
                    page.evaluate("""() => window.pingFamilyFixture.respond(200, {status:'success', data:{
                        state:'session_active', observed_at:'2026-11-05T18:00:00Z', valid_until:'2026-11-05T18:00:30Z',
                        dropped_updates:0, legs:[{role:'primary', leg:'auto', status:'permit_unavailable'}]}})""")
                    dialog.get_by_role("button", name="Check status").click()
                    expect(dialog.get_by_text("Permit not obtained; reason unknown, no probe sent")).to_be_visible()
                    page.evaluate("""() => window.pingFamilyFixture.respond(200, {status:'success', data:{
                        state:'offline', observed_at:'2026-11-05T18:00:00Z', valid_until:'2026-11-05T18:00:00.3Z',
                        dropped_updates:0, legs:[{role:'primary', leg:'auto', status:'unknown'}]}})""")
                    dialog.get_by_role("button", name="Check status").click()
                    expect(dialog.get_by_text("The snapshot has expired. Check again.")).to_be_visible(timeout=5000)
                    self.assertEqual(page.evaluate("window.pingFamilyFixture.calls()")[-1]["body"]["client"], "beta")
                    page.evaluate("window.pingFamilyFixture.respond(200, {status:'success', data:{state:'offline'}})")
                    dialog.get_by_role("button", name="Check status").click()
                    expect(dialog.get_by_role("alert")).to_have_text("The snapshot could not be loaded.")
                    self.assertEqual(errors, [])
                finally:
                    page.close()

    def test_diagnostic_close_ignores_late_response(self):
        page, errors = self.open_page({"width": 1200, "height": 900})
        try:
            page.get_by_role("button", name="Inspect probe status").click()
            dialog = page.get_by_role("dialog", name="Probe status")
            page.evaluate("""() => { window.pingFamilyFixture.respond(200, {status:'success', data:{
                state:'session_active', observed_at:'2026-11-05T18:00:00Z', valid_until:'2026-11-05T18:01:00Z',
                dropped_updates:0, legs:[{role:'primary', leg:'auto', status:'recorded'}]}});
                window.pingFamilyFixture.holdNext(); }""")
            dialog.get_by_role("button", name="Check status").click()
            expect(dialog.get_by_role("button", name="Checking...")).to_be_disabled()
            dialog.get_by_role("button", name="Close").click()
            page.evaluate("window.pingFamilyFixture.release()")
            page.get_by_role("button", name="Inspect probe status").click()
            expect(dialog.get_by_text("A probe result was stored", exact=False)).to_have_count(0)
            expect(dialog.get_by_role("button", name="Check status")).to_be_enabled()
            self.assertEqual(errors, [])
        finally:
            page.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
