"""Mounted number-picker contract and admin-log pagination regression.

Run: python script/number-picker.browser.spec.py

Requires Playwright Python and its Chromium browser.
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


class NumberPickerBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/number-picker.browser.html"
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
                    raise RuntimeError("Vite exited before number-picker fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Number picker fixture did not become ready")
            cls.playwright = sync_playwright().start()
            cls.browser = cls.playwright.chromium.launch(headless=True)
        except Exception as error:
            cls._stop()
            cls.output.seek(0)
            vite_log = cls.output.read()[-4000:]
            cls.output.close()
            raise RuntimeError(f"{error}\nVite log:\n{vite_log}") from error

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
        self.page = self.browser.new_page()
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.goto(self.url)
        expect(self.page.locator("#picker")).to_be_visible()
        # The log page starts by rendering <Loading />, so wait for its table.
        expect(self.page.locator("#log tbody tr").first).to_be_visible()

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    def picker(self, section="picker"):
        return self.page.locator(f"#{section} input[inputmode='numeric']")

    def test_valid_typing_fires_once_and_out_of_range_is_ignored_then_clamped(self):
        field = self.picker("inline-picker")
        expect(field).to_have_value("7")

        field.fill("")
        field.type("12")
        # One onChange per keystroke: "1" is in range, "12" is in range.
        expect(self.page.locator("#calls")).to_have_text("1,12")
        expect(self.page.locator("#call-count")).to_have_text("2")

        self.page.evaluate("window.numberPickerFixture.resetCount()")
        # 99 is above max=20: the displayed draft keeps the text, and no
        # onChange is emitted because the value is not in range.
        field.fill("99")
        expect(field).to_have_value("99")
        expect(self.page.locator("#call-count")).to_have_text("0")

        # Blur is what clamps the draft and reports the clamped value.
        field.blur()
        expect(field).to_have_value("20")
        expect(self.page.locator("#calls")).to_have_text("20")

    def test_out_of_range_below_min_is_clamped_on_blur(self):
        field = self.picker("inline-picker")
        field.fill("0")
        expect(self.page.locator("#call-count")).to_have_text("0")
        field.blur()
        expect(field).to_have_value("1")
        expect(self.page.locator("#calls")).to_have_text("1")

    def test_genuine_default_value_change_resyncs_without_refiring_on_rerender(self):
        inline = self.picker("inline-picker")
        stable = self.picker("stable-picker")
        expect(inline).to_have_value("7")
        expect(stable).to_have_value("7")

        # A parent render that changes nothing must not re-fire onChange.
        self.page.evaluate("window.numberPickerFixture.rerender()")
        expect(self.page.locator("#rerenders")).to_have_text("1")
        expect(self.page.locator("#call-count")).to_have_text("0")
        expect(self.page.locator("#stable-count")).to_have_text("0")

        # A genuine defaultValue change does re-sync the displayed value. It is
        # the parent telling the picker what it already knows, so it is not
        # reported back as an onChange: that echo is what reset pagination.
        self.page.evaluate("window.numberPickerFixture.setDefaultValue(15)")
        expect(inline).to_have_value("15")
        expect(stable).to_have_value("15")
        expect(self.page.locator("#call-count")).to_have_text("0")
        expect(self.page.locator("#stable-count")).to_have_text("0")

        # A defaultValue above max re-syncs to the clamp, not to the raw prop.
        self.page.evaluate("window.numberPickerFixture.setDefaultValue(99)")
        expect(inline).to_have_value("20")
        expect(stable).to_have_value("20")

        # Two more unrelated renders, one of them carrying the inline arrow the
        # log page used to pass: no further onChange.
        self.page.evaluate("window.numberPickerFixture.resetCount()")
        self.page.evaluate("window.numberPickerFixture.rerender()")
        self.page.evaluate("window.numberPickerFixture.rerender()")
        expect(self.page.locator("#rerenders")).to_have_text("3")
        expect(self.page.locator("#call-count")).to_have_text("0")
        expect(self.page.locator("#stable-count")).to_have_text("0")

    def test_user_edit_survives_an_unrelated_parent_rerender(self):
        field = self.picker("inline-picker")
        field.fill("13")
        self.page.evaluate("window.numberPickerFixture.rerender()")
        self.page.evaluate("window.numberPickerFixture.rerender()")
        expect(field).to_have_value("13")
        expect(self.page.locator("#calls")).to_have_text("13")
        expect(self.page.locator("#default-value")).to_have_text("7")

    def test_admin_log_pagination_stays_on_the_selected_page(self):
        page_two = self.page.locator("#log button", has_text="2").first
        page_two.click()
        # The regression: the picker's onChange echo used to fire setPage(1)
        # again on the next parent render, so page 2 was unreachable.
        expect(self.page.locator("#log tbody tr").first).to_have_attribute(
            "data-log-id", "11"
        )
        expect(self.page.locator("#log tbody tr").last).to_have_attribute(
            "data-log-id", "20"
        )

        # Every request the page made must agree with the page it is showing.
        requests = self.page.evaluate("window.numberPickerFixture.requests")
        self.assertEqual(
            requests,
            [{"limit": 10, "page": 1}, {"limit": 10, "page": 2}],
            f"unexpected request sequence: {requests}",
        )

    def test_admin_log_limit_edit_resets_to_page_one_once(self):
        self.page.locator("#log button", has_text="2").first.click()
        expect(self.page.locator("#log tbody tr").first).to_have_attribute(
            "data-log-id", "11"
        )

        field = self.page.locator("#log input[inputmode='numeric']")
        field.fill("")
        field.type("5")
        expect(self.page.locator("#log tbody tr").first).to_have_attribute(
            "data-log-id", "1"
        )
        requests = self.page.evaluate("window.numberPickerFixture.requests")
        self.assertEqual(
            requests,
            [
                {"limit": 10, "page": 1},
                {"limit": 10, "page": 2},
                {"limit": 5, "page": 1},
            ],
            f"unexpected request sequence: {requests}",
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
