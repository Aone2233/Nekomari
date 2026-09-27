"""Mounted regression for the traffic forecast page (roadmap H5).

Run with `python script/forecast.browser.spec.py`.

The assertion this page exists for is not about numbers: **a refusal must not look like a good result.**
The server declines to project from too little data and says why, and both outcomes — "we cannot tell" and
"nothing to worry about" — produce no projected figure. If they render alike, the page is worse than
useless, because an operator scanning it would read silence as safety. So the spec pins that a refused row
carries its reason where the number would have been, and that the counts separate the two.

The rest follows:

  * a projection is shown **with its basis**, because a figure that cannot be argued with is not one to act
    on
  * the cycle day is editable here and previewable before it is stored, since it is the one input that
    changes every number on the page
  * a node with no allowance says so rather than showing a fraction of nothing

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


class ForecastBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}/script/forecast.browser.html"
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
                raise RuntimeError("Forecast fixture did not become ready")
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
        expect(page.locator('[data-testid="forecast-page"]')).to_be_visible()
        return page

    def teardown_page(self, page):
        try:
            self.assertEqual(self.errors, [])
        finally:
            page.close()

    def row(self, page, uuid: str):
        return page.locator(f'[data-testid="forecast-row"][data-uuid="{uuid}"]')

    def test_a_refusal_does_not_look_like_a_good_result(self):
        page = self.open("ready")
        try:
            # Four rows: one over the limit, one fine, one refused, one with no allowance.
            expect(page.locator('[data-testid="forecast-row"]')).to_have_count(4)

            refused = self.row(page, "uuid-young")
            expect(refused).to_have_attribute("data-method", "insufficient")
            # The reason sits where the projected number would have been, rather than a blank cell that
            # would read as "nothing to report".
            expect(refused.locator('[data-testid="forecast-row-reason"]')).to_be_visible()
            expect(refused.locator('[data-testid="forecast-row-reason"]')).to_contain_text(
                "a projection needs at least 5")
            expect(refused.locator('[data-testid="forecast-row-projected"]')).not_to_contain_text("GiB")

            # And the counts separate the two outcomes, so the summary cannot be read as "all fine".
            expect(page.locator('[data-testid="forecast-refused"]')).to_have_attribute("data-count", "1")
            expect(page.locator('[data-testid="forecast-projected"]')).to_have_attribute("data-count", "3")
        finally:
            self.teardown_page(page)

    def test_a_projection_is_shown_with_its_basis(self):
        page = self.open("ready")
        try:
            basis = self.row(page, "uuid-over").locator('[data-testid="forecast-row-basis"]')
            expect(basis).to_contain_text("41 samples")
            expect(basis).to_contain_text("33% of cycle")
            expect(basis).to_contain_text("±7%")
        finally:
            self.teardown_page(page)

    def test_a_node_projected_over_its_limit_is_marked(self):
        page = self.open("ready")
        try:
            over = self.row(page, "uuid-over")
            expect(over).to_have_attribute("data-exceeds", "true")
            expect(over.locator('[data-testid="forecast-row-percent"]')).to_contain_text("120%")
            # The crossing is reported because it is within the cycle, which is what makes it urgent now
            # rather than eventually.
            expect(over.locator('[data-testid="forecast-row-crosses"]')).to_contain_text("5.0 days")

            fine = self.row(page, "uuid-fine")
            expect(fine).to_have_attribute("data-exceeds", "false")
            expect(fine.locator('[data-testid="forecast-row-crosses"]')).to_contain_text("not this cycle")

            expect(page.locator('[data-testid="forecast-exceeding"]')).to_have_attribute("data-count", "1")
        finally:
            self.teardown_page(page)

    def test_a_node_with_no_allowance_says_so(self):
        page = self.open("ready")
        try:
            # Not forecast against infinity: the limit cell says there is none, and no percentage is shown.
            no_limit = self.row(page, "uuid-nolimit")
            expect(no_limit).to_contain_text("no limit")
            expect(no_limit.locator('[data-testid="forecast-row-percent"]')).to_have_count(0)
            # It still gets a projected total, which is useful on its own.
            expect(no_limit.locator('[data-testid="forecast-row-projected"]')).to_contain_text("GiB")
        finally:
            self.teardown_page(page)

    def test_the_cycle_day_can_be_previewed_before_it_is_stored(self):
        page = self.open("ready")
        try:
            expect(page.locator('[data-testid="forecast-cycle-day"]')).to_have_value("1")
            page.locator('[data-testid="forecast-cycle-day"]').fill("15")
            page.locator('[data-testid="forecast-preview"]').click()
            page.wait_for_timeout(200)

            calls = page.evaluate("window.__forecastCalls")
            self.assertEqual(calls["preview"], [15], calls)
            # A preview must not store anything: the day changes every number, and an operator checking it
            # against their agent has not decided yet.
            self.assertEqual(calls["save"], [], calls)
        finally:
            self.teardown_page(page)

    def test_the_stored_cycle_day_is_saved(self):
        page = self.open("ready")
        try:
            page.locator('[data-testid="forecast-cycle-day"]').fill("31")
            page.locator('[data-testid="forecast-save-day"]').click()
            page.wait_for_timeout(200)
            calls = page.evaluate("window.__forecastCalls")
            self.assertEqual(calls["save"], [31], calls)
        finally:
            self.teardown_page(page)

    def test_an_impossible_cycle_day_is_refused_before_it_is_sent(self):
        page = self.open("ready")
        try:
            page.locator('[data-testid="forecast-cycle-day"]').fill("45")
            page.locator('[data-testid="forecast-save-day"]').click()
            page.wait_for_timeout(200)

            expect(page.locator('[data-testid="forecast-error"]')).to_be_visible()
            # Nothing was sent: the page knows the range, so it does not need the server to say so.
            calls = page.evaluate("window.__forecastCalls")
            self.assertEqual(calls["save"], [], calls)
        finally:
            self.teardown_page(page)

    def test_a_refused_write_reaches_the_page(self):
        page = self.open("refused")
        try:
            page.locator('[data-testid="forecast-cycle-day"]').fill("5")
            page.locator('[data-testid="forecast-save-day"]').click()
            page.wait_for_timeout(300)

            error = page.locator('[data-testid="forecast-error"]')
            expect(error).to_be_visible()
            # The server's own reason, not a generic message.
            expect(error).to_contain_text("reset_day must be between 1 and 31")
        finally:
            self.teardown_page(page)

    def test_the_timezone_and_threshold_are_stated(self):
        page = self.open("ready")
        try:
            # The panel computes the cycle in its own timezone because the agent does not report one, so
            # which timezone was used has to be visible: a mismatch shifts every projection on the page.
            expect(page.locator('[data-testid="forecast-location"]')).to_contain_text("UTC")
            expect(page.locator('[data-testid="forecast-window"]')).to_contain_text("2026-09-01")
            expect(page.locator('[data-testid="forecast-threshold"]')).to_have_attribute("data-threshold", "0.9")
        finally:
            self.teardown_page(page)

    def test_loading_state_says_so(self):
        page = self.open("loading")
        try:
            expect(page.locator('[data-testid="forecast-loading"]')).to_be_visible()
            expect(page.locator('[data-testid="forecast-row"]')).to_have_count(0)
        finally:
            self.teardown_page(page)


if __name__ == "__main__":
    unittest.main(verbosity=2)
