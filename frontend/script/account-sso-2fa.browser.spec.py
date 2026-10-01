"""Mounted SSO sensitive-2FA regression: python script/account-sso-2fa.browser.spec.py.

`/api/admin/oauth2/bind` and `/unbind` sit behind `api.RequireSensitive2FA()`, so the account
page has to send the same one-time code its 2FA buttons collect -- and send nothing extra when
the account has no factor, which the server lets through. Both halves are asserted here on the
real page: the URL a click produces, the prompt it does or does not show, and the toast a
refusal produces instead of silently doing nothing.
"""

import json
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
BIND_PATH = "/api/admin/oauth2/bind"
UNBIND_PATH = "/api/admin/oauth2/unbind"


class AccountSso2faBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}"
        cls.url = f"{cls.base}/script/account-sso-2fa.browser.html"
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
                    raise RuntimeError("Vite exited before the SSO fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("SSO fixture did not become ready")
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

    def _open(self, bind_answer=(302, None), **params):
        """Open the fixture and record every request the page makes to the bind endpoint.

        The bind request is answered here rather than in the fixture's fetch stub, because the
        page's failure path depends on the browser's own `redirect: "manual"` handling of the
        302 -- a synthetic Response cannot reproduce that.
        """
        page = self.browser.new_page(viewport={"width": 1000, "height": 900})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        bind_urls: list[str] = []
        status, body = bind_answer

        def answer_bind(route):
            bind_urls.append(route.request.url)
            if body is None:
                route.fulfill(status=status, headers={"Location": "/api/oauth"})
            else:
                route.fulfill(status=status, content_type="application/json", body=json.dumps(body))

        page.route(f"**{BIND_PATH}*", answer_bind)
        page.route(
            "**/api/oauth",
            lambda route: route.fulfill(
                status=200, content_type="text/html", body="<html><body>provider</body></html>"
            ),
        )
        query = "&".join(f"{key}={value}" for key, value in params.items())
        page.goto(f"{self.url}?{query}")
        expect(page.get_by_role("button", name="Bind External Account").or_(
            page.get_by_role("button", name="Unbind External Account")
        )).to_be_visible()
        return page, bind_urls, errors

    def test_bind_without_a_factor_is_the_bare_request_it_always_was(self):
        page, bind_urls, errors = self._open(bound=0, tfa=0)
        try:
            page.get_by_role("button", name="Bind External Account").click()
            page.wait_for_timeout(400)
            expect(page.get_by_role("dialog")).to_have_count(0)
            self.assertEqual(
                bind_urls,
                [f"{self.base}{BIND_PATH}"],
                "an account with no factor must not gain a query parameter or a prompt",
            )
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_bind_with_a_factor_asks_for_the_code_and_carries_it(self):
        page, bind_urls, errors = self._open(bound=0, tfa=1)
        try:
            page.get_by_role("button", name="Bind External Account").click()
            dialog = page.get_by_role("dialog", name="Bind External Account")
            expect(dialog).to_be_visible()
            self.assertEqual(bind_urls, [], "opening the prompt must not send anything")

            confirm = dialog.get_by_role("button", name="Confirm", exact=True)
            confirm.click()
            expect(page.get_by_text("Empty otp code").first).to_be_visible()
            self.assertEqual(bind_urls, [], "an empty code must be refused before the request")

            dialog.locator('input[type="number"]').fill("123456")
            confirm.click()
            page.wait_for_timeout(600)
            self.assertTrue(bind_urls, "confirming must send the bind request")
            self.assertEqual(
                set(bind_urls),
                {f"{self.base}{BIND_PATH}?2fa_code=123456"},
                f"every bind request must carry the code: {bind_urls}",
            )
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_bind_refusal_is_shown_instead_of_navigating_away(self):
        page, bind_urls, errors = self._open(
            bound=0, tfa=1,
            bind_answer=(401, {"status": "error", "message": "Invalid 2FA code"}),
        )
        try:
            page.get_by_role("button", name="Bind External Account").click()
            dialog = page.get_by_role("dialog", name="Bind External Account")
            dialog.locator('input[type="number"]').fill("000000")
            dialog.get_by_role("button", name="Confirm", exact=True).click()
            expect(page.get_by_text("Invalid 2FA code").first).to_be_visible()
            self.assertEqual(
                len(bind_urls), 1,
                f"a refusal must not be followed by the navigation: {bind_urls}",
            )
            self.assertIn("account-sso-2fa.browser.html", page.url)
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_unbind_carries_the_code_and_surfaces_the_refusal(self):
        page, _, errors = self._open(bound=1, tfa=1, unbind=401)
        try:
            page.get_by_role("button", name="Unbind External Account").click()
            dialog = page.get_by_role("dialog", name="Confirm Unbind")
            expect(dialog).to_be_visible()
            dialog.locator('input[type="number"]').fill("123456")
            dialog.get_by_role("button", name="Confirm Unbind").click()
            # The server's message has to survive into the toast; "Unbinding failed" alone would
            # leave the operator guessing whether the code was wrong or the request never left.
            expect(page.get_by_text("Unbinding failed: Invalid 2FA code").first).to_be_visible()
            self.assertIn(
                f"POST {UNBIND_PATH}?2fa_code=123456",
                page.evaluate("window.ssoFixture.calls()"),
            )
            expect(dialog).to_be_visible()
            self.assertEqual(errors, [])
        finally:
            page.close()

    def test_unbind_without_a_factor_sends_no_parameter(self):
        page, _, errors = self._open(bound=1, tfa=0)
        try:
            page.get_by_role("button", name="Unbind External Account").click()
            dialog = page.get_by_role("dialog", name="Confirm Unbind")
            expect(dialog).to_be_visible()
            self.assertEqual(
                dialog.locator('input[type="number"]').count(), 0,
                "there is no factor to verify, so there must be no code field",
            )
            dialog.get_by_role("button", name="Confirm Unbind").click()
            expect(page.get_by_text("Successfully unbound").first).to_be_visible()
            calls = page.evaluate("window.ssoFixture.calls()")
            self.assertIn(f"POST {UNBIND_PATH}", calls)
            self.assertEqual([call for call in calls if "2fa_code" in call], [])
            self.assertEqual(errors, [])
        finally:
            page.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
