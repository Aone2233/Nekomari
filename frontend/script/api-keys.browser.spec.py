"""Mounted API key UI: explicit scopes, one-time secret, rotation/revocation, 2FA."""

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


class APIKeysBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/api-keys.browser.html"
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
                    raise RuntimeError("Vite exited before API key fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("API key fixture did not become ready")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception:
            cls._stop()
            cls.output.seek(0)
            print(cls.output.read()[-4000:])
            cls.output.close()
            raise

    @classmethod
    def _stop(cls):
        if cls.browser is not None:
            cls.browser.close()
        if cls.playwright is not None:
            cls.playwright.stop()
        if cls.server is not None:
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
        self.page = self.browser.new_page(viewport={"width": 1100, "height": 950})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))

    def tearDown(self):
        self.page.close()
        self.assertEqual(self.errors, [])

    def open(self, query=""):
        self.page.goto(self.url + query)
        expect(self.page.get_by_role("region", name="Existing monitor")).to_be_visible()

    def posts(self):
        return self.page.evaluate("window.__apiKeyCalls.filter(call => call.method === 'POST')")

    def test_create_is_scoped_and_secret_disappears_after_dismissal(self):
        self.open()
        page = self.page
        expect(page.get_by_role("button", name="Create key", exact=True)).to_be_disabled()
        page.get_by_label("Key name", exact=True).fill("Deploy checks")
        page.get_by_role("checkbox", name="logs:read", exact=True).check()
        page.get_by_role("button", name="Create key", exact=True).click()
        secret = page.get_by_label("API token", exact=True)
        expect(secret).to_have_value("nk_fixture-created-1")
        request = self.posts()[0]["body"]
        self.assertEqual(request["scopes"], ["nodes:read", "logs:read"])
        self.assertEqual(request["expires_days"], 90)
        self.assertNotIn("2fa_code", request)
        page.get_by_role("button", name="I have stored the token", exact=True).click()
        expect(secret).to_have_count(0)
        expect(page.get_by_role("region", name="Deploy checks")).to_be_visible()
        self.assertFalse(page.evaluate("Object.values(localStorage).some(value => value.includes('nk_fixture'))"))
        self.assertFalse(page.evaluate("document.body.textContent.includes('nk_fixture-created-1')"))
        page.reload()
        expect(page.get_by_label("API token", exact=True)).to_have_count(0)

    def test_rotation_and_revocation_require_explicit_confirmation(self):
        self.open()
        page = self.page
        section = page.get_by_role("region", name="Existing monitor")
        section.get_by_role("button", name="Rotate token", exact=True).click()
        expect(page.get_by_role("dialog")).to_contain_text("old token stops working immediately")
        self.assertEqual(self.posts(), [])
        page.get_by_role("button", name="Cancel", exact=True).click()
        self.assertEqual(self.posts(), [])
        section.get_by_role("button", name="Rotate token", exact=True).click()
        page.get_by_role("button", name="Confirm", exact=True).click()
        expect(page.get_by_label("API token", exact=True)).to_have_value("nk_fixture-rotated-1")
        page.get_by_role("button", name="I have stored the token", exact=True).click()
        section.get_by_role("button", name="Revoke key", exact=True).click()
        page.get_by_role("button", name="Confirm", exact=True).click()
        expect(section).to_contain_text("Revoked")
        expect(section.get_by_role("button", name="Rotate token", exact=True)).to_have_count(0)
        self.assertEqual(len(self.posts()), 2)
        self.assertTrue(self.posts()[1]["path"].endswith("/revoke"))

    def test_second_factor_is_per_mutation_and_not_retained(self):
        self.open("?tfa=1")
        page = self.page
        page.get_by_label("Key name", exact=True).fill("2FA key")
        create = page.get_by_role("button", name="Create key", exact=True)
        expect(create).to_be_disabled()
        page.locator("#api-key-otp").fill("123456")
        create.click()
        expect(page.get_by_label("API token", exact=True)).to_be_visible()
        self.assertEqual(self.posts()[0]["body"]["2fa_code"], "123456")
        page.get_by_role("button", name="I have stored the token", exact=True).click()
        expect(page.locator("#api-key-otp")).to_have_value("")
        page.get_by_role("region", name="Existing monitor").get_by_role("button", name="Rotate token", exact=True).click()
        expect(page.get_by_role("button", name="Confirm", exact=True)).to_be_disabled()
        page.locator("#api-key-confirm-otp").fill("234567")
        page.get_by_role("button", name="Confirm", exact=True).click()
        expect(page.get_by_label("API token", exact=True)).to_be_visible()
        self.assertEqual(self.posts()[1]["body"]["2fa_code"], "234567")

    def test_http_200_error_is_not_reported_as_creation(self):
        self.open("?refuse=1")
        page = self.page
        page.get_by_label("Key name", exact=True).fill("Refused key")
        page.get_by_role("button", name="Create key", exact=True).click()
        expect(page.get_by_role("alert")).to_contain_text("Scope approval refused")
        expect(page.get_by_label("API token", exact=True)).to_have_count(0)
        expect(page.get_by_label("Key name", exact=True)).to_have_value("Refused key")

    def resource(self, language):
        return json.loads((ROOT / f"src/i18n/locales/{language}.json").read_text())

    def respond(self, key, status=400):
        self.page.evaluate("answer => window.apiKeyFixture.respond(answer)", {
            "status": status, "body": {"status": "error", "message": "Legacy diagnostic", "error_key": key},
        })

    def test_five_locale_creation_errors_preserve_drafts_scopes_and_clear_otp_without_replay(self):
        page = self.page
        for language in ("en", "zh_CN", "zh_TW", "ja_JP", "id_ID"):
            with self.subTest(language=language):
                resource = self.resource(language)
                text = resource["api_keys"]
                self.open(f"?language={language}&tfa=1")
                page.get_by_label(text["name"], exact=True).fill("Preserved key draft")
                page.get_by_label(text["expires_days"], exact=True).fill("42")
                page.get_by_role("checkbox", name="logs:read", exact=True).check()
                create = page.get_by_role("button", name=text["create"], exact=True)
                for index, key in enumerate(("api_key_name_invalid", "api_key_scopes_invalid", "api_key_expiry_invalid",
                                             "api_key_limit", "api_key_create_failed")):
                    self.respond(key, 200 if index == 0 else 400)
                    page.locator("#api-key-otp").fill("123456")
                    before = page.evaluate("window.__apiKeyCalls.length")
                    create.click()
                    expect(page.get_by_role("alert")).to_have_text(resource["server_errors"][key])
                    expect(page.locator("#api-key-otp")).to_have_value("")
                    expect(create).to_be_disabled()
                    expect(page.get_by_label(text["name"], exact=True)).to_have_value("Preserved key draft")
                    expect(page.get_by_label(text["expires_days"], exact=True)).to_have_value("42")
                    expect(page.get_by_role("checkbox", name="logs:read", exact=True)).to_be_checked()
                    expect(page.get_by_label(text["token"], exact=True)).to_have_count(0)
                    page.wait_for_timeout(100)
                    self.assertEqual(page.evaluate("window.__apiKeyCalls.length"), before + 1)
                self.respond("future_key")
                page.locator("#api-key-otp").fill("123456")
                create.click()
                expect(page.get_by_role("alert")).to_have_text("Legacy diagnostic")
                page.locator("#api-key-otp").fill("234567")
                create.click()
                expect(page.get_by_label(text["token"], exact=True)).to_have_value("nk_fixture-created-1")
                page.get_by_role("button", name=text["saved"], exact=True).click()
                self.assertEqual(len(self.posts()), 7)
                self.assertEqual(self.posts()[-1]["body"]["scopes"], ["nodes:read", "logs:read"])

    def test_five_locale_confirmation_failures_keep_target_and_require_explicit_retry(self):
        page = self.page
        for language in ("en", "zh_CN", "zh_TW", "ja_JP", "id_ID"):
            resource = self.resource(language)
            text = resource["api_keys"]
            for kind in ("rotate", "revoke"):
                with self.subTest(language=language, kind=kind):
                    self.open(f"?language={language}&tfa=1")
                    page.get_by_role("region", name="Existing monitor").get_by_role("button", name=text[kind], exact=True).click()
                    dialog = page.get_by_role("dialog")
                    confirm = dialog.get_by_role("button", name=resource["common"]["confirm"], exact=True)
                    keys = ["api_key_id_invalid", "api_key_rotation_failed" if kind == "rotate" else "api_key_revocation_failed"]
                    for key in keys:
                        self.respond(key)
                        page.locator("#api-key-confirm-otp").fill("123456")
                        before = page.evaluate("window.__apiKeyCalls.length")
                        confirm.click()
                        expect(dialog.get_by_role("alert")).to_have_text(resource["server_errors"][key])
                        expect(dialog).to_be_visible()
                        expect(page.locator("#api-key-confirm-otp")).to_have_value("")
                        expect(confirm).to_be_disabled()
                        expect(page.get_by_label(text["token"], exact=True)).to_have_count(0)
                        page.wait_for_timeout(100)
                        self.assertEqual(page.evaluate("window.__apiKeyCalls.length"), before + 1)
                    page.locator("#api-key-confirm-otp").fill("234567")
                    confirm.click()
                    if kind == "rotate":
                        expect(page.get_by_label(text["token"], exact=True)).to_have_value("nk_fixture-rotated-1")
                        page.get_by_role("button", name=text["saved"], exact=True).click()
                    else:
                        expect(dialog).to_be_hidden()
                        expect(page.get_by_role("region", name="Existing monitor")).to_contain_text(text["revoked"])
                    self.assertEqual(len(self.posts()), 3)
                    self.assertTrue(all(call["path"].endswith("/" + kind) for call in self.posts()))

    def test_five_locale_list_errors_wait_for_explicit_refresh(self):
        page = self.page
        for language in ("en", "zh_CN", "zh_TW", "ja_JP", "id_ID"):
            with self.subTest(language=language):
                resource = self.resource(language)
                page.goto(f"{self.url}?language={language}&listError=1")
                expect(page.get_by_role("alert")).to_have_text(resource["server_errors"]["api_keys_unavailable"])
                page.wait_for_timeout(100)
                self.assertEqual(page.evaluate("window.__apiKeyCalls.length"), 1)
                page.get_by_role("button", name=resource["common"]["refresh"], exact=True).click()
                expect(page.get_by_role("region", name="Existing monitor")).to_be_visible()
                expect(page.get_by_role("alert")).to_have_count(0)
                self.assertEqual(page.evaluate("window.__apiKeyCalls.length"), 2)
                self.assertEqual(self.posts(), [])

    def test_language_change_does_not_replay_mutation_or_rewrite_existing_error_snapshot(self):
        page = self.page
        self.open()
        page.get_by_label("Key name", exact=True).fill("Preserved key draft")
        self.respond("api_key_create_failed")
        page.get_by_role("button", name="Create key", exact=True).click()
        expect(page.get_by_role("alert")).to_have_text(self.resource("en")["server_errors"]["api_key_create_failed"])
        before = page.evaluate("window.__apiKeyCalls.length")
        page.evaluate("() => window.apiKeyFixture.changeLanguage('zh_CN')")
        expect(page.get_by_label(self.resource("zh_CN")["api_keys"]["name"], exact=True)).to_have_value("Preserved key draft")
        expect(page.get_by_role("alert")).to_have_text(self.resource("en")["server_errors"]["api_key_create_failed"])
        self.assertEqual(page.evaluate("window.__apiKeyCalls.length"), before)
        self.assertEqual(len(self.posts()), 1)

    def test_mobile_form_has_no_horizontal_overflow(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.open()
        self.assertLessEqual(self.page.evaluate("document.documentElement.scrollWidth"), 390)
        self.page.get_by_label("Key name", exact=True).fill("Mobile key")
        self.page.get_by_role("button", name="Create key", exact=True).click()
        expect(self.page.get_by_label("API token", exact=True)).to_be_visible()
        self.assertLessEqual(self.page.evaluate("document.documentElement.scrollWidth"), 390)

    def test_long_list_in_actual_admin_layout_keeps_pointer_target_after_refused_create(self):
        page = self.page
        for language, width, height in (("ja_JP", 390, 1000), ("en", 390, 844), ("zh_CN", 1280, 1000),
                                         ("zh_TW", 390, 844), ("id_ID", 390, 844)):
            with self.subTest(language=language, width=width):
                page.set_viewport_size({"width": width, "height": height})
                page.goto(f"{self.url}?layout=1&language={language}")
                text = self.resource(language)["api_keys"]
                name = page.locator("#api-key-name")
                expect(name).to_be_visible()
                expect(page.get_by_role("region", name="Existing seed 11", exact=True)).to_have_count(1)
                anchoring = name.evaluate("""element => {
                  const card = element.closest('.km-setting-card');
                  return {card: getComputedStyle(card).overflowAnchor,
                    parent: getComputedStyle(card.parentElement).overflowAnchor,
                    other: getComputedStyle(document.querySelector('.km-setting-card')).overflowAnchor};
                }""")
                self.assertEqual(anchoring, {"card": "none", "parent": "auto", "other": "auto"})
                page.evaluate("""() => {
                  window.__keyPointerEvents = [];
                  for (const type of ['pointermove', 'pointerdown', 'pointerup', 'click']) {
                    document.addEventListener(type, event => {
                      const button = document.querySelector('form button[type=submit]');
                      if (button && (event.target === button || button.contains(event.target))) {
                        window.__keyPointerEvents.push({type, trusted: event.isTrusted});
                      }
                    }, true);
                  }
                }""")
                name.fill("Preserved long-list draft")
                create = page.get_by_role("button", name=text["create"], exact=True)
                self.respond("api_key_name_invalid")
                create.click(timeout=5000)
                expect(page.get_by_role("alert")).to_have_text(self.resource(language)["server_errors"]["api_key_name_invalid"])
                name.fill("Recovered long-list draft")
                self.respond("api_key_create_failed")
                create.click(timeout=5000)
                expect(page.get_by_role("alert")).to_have_text(self.resource(language)["server_errors"]["api_key_create_failed"])
                expect(name).to_have_value("Recovered long-list draft")
                page.wait_for_timeout(100)
                self.assertEqual(len(self.posts()), 2)
                self.assertEqual(page.evaluate("window.__apiKeyCalls.length"), 3)
                self.assertEqual(page.get_by_role("region").count(), 13)
                create.click(timeout=5000)
                expect(page.get_by_label(text["token"], exact=True)).to_have_value("nk_fixture-created-1")
                page.get_by_role("button", name=text["saved"], exact=True).click()
                expect(page.get_by_role("region", name="Recovered long-list draft", exact=True)).to_be_visible()
                self.assertEqual(len(self.posts()), 3)
                events = page.evaluate("window.__keyPointerEvents")
                self.assertEqual(sum(event["type"] == "click" for event in events), 3)
                self.assertTrue(all(event["trusted"] for event in events))
                self.assertLessEqual(page.evaluate("document.documentElement.scrollWidth"), width)

    def test_long_list_keeps_keyboard_and_touch_submission_and_dialog_dismissal(self):
        for mode in ("keyboard", "touch"):
            with self.subTest(mode=mode):
                context = self.browser.new_context(viewport={"width": 390, "height": 844},
                                                   has_touch=mode == "touch", is_mobile=mode == "touch")
                page = context.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                try:
                    page.goto(self.url + "?layout=1&language=en&tfa=1")
                    name = page.locator("#api-key-name")
                    expect(name).to_be_visible()
                    name.fill("Long-list " + mode)
                    page.locator("#api-key-otp").fill("123456")
                    page.evaluate("""() => {
                      window.__keyInputEvents = [];
                      for (const type of ['keydown', 'pointerdown']) document.addEventListener(type, event => {
                        window.__keyInputEvents.push({type, trusted: event.isTrusted, key: event.key ?? null,
                          pointer: event.pointerType ?? null});
                      }, true);
                      window.apiKeyFixture.respond({status:400, body:{status:'error',
                        error_key:'api_key_create_failed', message:'Synthetic refusal'}});
                    }""")
                    create = page.get_by_role("button", name="Create key", exact=True)
                    if mode == "keyboard":
                        page.locator("#api-key-otp").press("Enter")
                    else:
                        create.tap(timeout=5000)
                    expect(page.get_by_role("alert")).to_have_text(self.resource("en")["server_errors"]["api_key_create_failed"])
                    expect(name).to_have_value("Long-list " + mode)
                    expect(page.locator("#api-key-otp")).to_have_value("")
                    expect(create).to_be_disabled()
                    self.assertEqual(page.evaluate("window.__apiKeyCalls.filter(c => c.method === 'POST').length"), 1)
                    page.locator("#api-key-otp").fill("234567")
                    if mode == "keyboard":
                        page.locator("#api-key-otp").press("Enter")
                    else:
                        create.tap(timeout=5000)
                    secret = page.get_by_label("API token", exact=True)
                    expect(secret).to_have_value("nk_fixture-created-1")
                    if mode == "keyboard":
                        page.keyboard.press("Escape")
                    else:
                        page.get_by_role("button", name="I have stored the token", exact=True).tap(timeout=5000)
                    expect(secret).to_have_count(0)
                    expect(page.get_by_role("region", name="Long-list " + mode, exact=True)).to_be_visible()
                    expect(page.locator("#api-key-otp")).to_have_value("")
                    self.assertEqual(page.evaluate("window.__apiKeyCalls.filter(c => c.method === 'POST').length"), 2)
                    events = page.evaluate("window.__keyInputEvents")
                    self.assertTrue(all(event["trusted"] for event in events))
                    if mode == "keyboard":
                        self.assertEqual(sum(event["key"] == "Enter" for event in events), 2)
                    else:
                        self.assertGreaterEqual(sum(event["pointer"] == "touch" for event in events), 3)
                    self.assertEqual(errors, [])
                finally:
                    context.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
