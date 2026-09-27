"""Mounted regression for the SLA report table (roadmap H1).

Run with `python script/status-sla.browser.spec.py`.

The table is the panel's half of H1: the server computes availability from measured
buckets, reports coverage beside it, and lists outages separately from reporting gaps.
Each of those is a definition that the presentation could quietly undo, so the
assertions below are about the definitions rather than about layout:

  * a node with no data must say so, and must **not** read "0.0%" — a silent node and a
    dead one would otherwise be indistinguishable, and the silent case is the one
    nobody notices;
  * coverage must be visible **beside** availability, because a node up for all of a
    window it only partly reported is not the same as one that reported throughout;
  * an outage on a task must not become an outage of the node;
  * a reporting gap must be labelled as a gap, not as downtime.

The fixture takes the report as a prop, so every state is reachable by URL without a
server. English resources only: what is asserted is which figures appear, not in which
language.
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


class SlaReportBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}/script/status-sla.browser.html"
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
                    with urlopen(cls.base + "?state=healthy", timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("SLA fixture did not become ready")
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
        expect(page.locator('[data-testid="status-page"], [data-testid="sla-report"],'
                            ' [data-testid="sla-loading"], [data-testid="sla-error"]').first).to_be_visible()
        return page

    def teardown_page(self, page):
        try:
            self.assertEqual(self.errors, [])
        finally:
            page.close()

    def test_healthy_report_renders_availability_coverage_and_latency(self):
        page = self.open("healthy")
        try:
            report = page.locator('[data-testid="sla-report"]')
            expect(report).to_be_visible()
            expect(report).to_have_attribute("data-window", "24h")

            rows = page.locator('[data-testid="sla-task-row"]')
            expect(rows).to_have_count(2)

            # Task 1 is fully up, and its availability reads as a percentage.
            first = rows.nth(0)
            expect(first.locator('[data-testid="sla-task-availability"]')).to_contain_text("100.0%")
            # Task 2 lost one bucket of sixty.
            second = rows.nth(1)
            expect(second.locator('[data-testid="sla-task-availability"]')).to_contain_text("98.8%")
            # Coverage is beside availability, not instead of it.
            expect(first.locator('[data-testid="sla-task-coverage"]')).to_contain_text("100.0%")
            expect(first.locator('[data-testid="sla-task-coverage"]')).to_contain_text("60/60")
            # Percentiles, all three.
            expect(first.locator('[data-testid="sla-task-latency"]')).to_contain_text("12.5")
            expect(first.locator('[data-testid="sla-task-latency"]')).to_contain_text("48.9")
            # Task names come from the caller, so an outage says what the task is.
            expect(page.locator('[data-testid="sla-task-table"]')).to_contain_text("Cloudflare TCP 443")
        finally:
            self.teardown_page(page)

    def test_no_data_is_not_zero_percent(self):
        page = self.open("nodata")
        try:
            expect(page.locator('[data-testid="sla-node-nodata"]')).to_be_visible()
            row = page.locator('[data-testid="sla-task-row"]')
            expect(row).to_have_attribute("data-has-data", "false")
            availability = row.locator('[data-testid="sla-task-availability"]')
            expect(availability).to_contain_text("no data")
            # The assertion that matters: a figure of zero with no evidence behind it
            # must never be rendered as a percentage.
            expect(availability).not_to_contain_text("0.0%")
            expect(row.locator('[data-testid="sla-task-coverage"]')).to_contain_text("no data")
            expect(row.locator('[data-testid="sla-task-latency"]')).to_contain_text("no data")
        finally:
            self.teardown_page(page)

    def test_a_task_outage_does_not_become_a_node_outage(self):
        page = self.open("outage")
        try:
            rows = page.locator('[data-testid="sla-task-row"]')
            expect(rows).to_have_count(1)
            outages = rows.nth(0).locator('[data-testid="sla-task-outages"]')
            expect(outages).to_contain_text("→")
            # The node itself kept reporting, so its coverage badge is untouched and no
            # reporting-gap section appears. Conflating the two is the misreading the
            # report exists to prevent.
            expect(page.locator('[data-testid="sla-node-presence"]')).to_contain_text("100.0%")
            expect(page.locator('[data-testid="sla-reporting-gaps"]')).to_have_count(0)
        finally:
            self.teardown_page(page)

    def test_reporting_gaps_are_labelled_as_gaps(self):
        page = self.open("gaps")
        try:
            gaps = page.locator('[data-testid="sla-reporting-gaps"]')
            expect(gaps).to_be_visible()
            expect(gaps).to_contain_text("Reporting gaps")
            expect(gaps).to_contain_text("not outages")
            # And the shortened window is stated rather than silently answered.
            expect(page.locator('[data-testid="sla-clamped"]')).to_be_visible()
            # Coverage reflects the missing stretch while the measured buckets are clean.
            expect(page.locator('[data-testid="sla-node-presence"]')).to_contain_text("40/60")
        finally:
            self.teardown_page(page)

    def test_a_hidden_node_is_absent_entirely(self):
        page = self.open("hidden")
        try:
            # One node in the report, and the silent one from the other fixtures is not
            # mentioned at all — no row, no zeroes, nothing to infer its existence from.
            expect(page.locator('[data-testid="sla-node"]')).to_have_count(1)
            expect(page.locator('[data-testid="sla-node"]').first).to_have_attribute(
                "data-entity-id", "node-a")
            expect(page.locator("body")).not_to_contain_text("Silent Node")
        finally:
            self.teardown_page(page)

    def test_loading_becomes_a_report_without_a_reload(self):
        page = self.open("loading")
        try:
            expect(page.locator('[data-testid="sla-loading"]')).to_be_visible()
            page.evaluate("window.__showState('outage')")
            expect(page.locator('[data-testid="sla-report"]')).to_be_visible()
            expect(page.locator('[data-testid="sla-loading"]')).to_have_count(0)
        finally:
            self.teardown_page(page)

    def test_an_error_is_shown_rather_than_looking_empty(self):
        page = self.open("error")
        try:
            expect(page.locator('[data-testid="sla-error"]')).to_be_visible()
            expect(page.locator('[data-testid="sla-error"]')).to_contain_text("metric store not initialized")
            # An empty table and a failed request look identical to a reader, and only one
            # of them means nothing is wrong.
            expect(page.locator('[data-testid="sla-empty"]')).to_have_count(0)
        finally:
            self.teardown_page(page)

    def test_the_window_control_offers_every_preset(self):
        page = self.open("healthy")
        try:
            control = page.locator('[data-testid="sla-window-control"]')
            expect(control).to_be_visible()
            for label in ("24 hours", "7 days", "30 days", "90 days"):
                expect(control).to_contain_text(label)
            expect(page.locator('[data-testid="sla-interval"]')).to_contain_text("60")
        finally:
            self.teardown_page(page)


if __name__ == "__main__":
    unittest.main(verbosity=2)
