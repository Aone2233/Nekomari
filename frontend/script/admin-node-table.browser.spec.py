"""Mounted state regression for the admin node table.

Run with `python script/admin-node-table.browser.spec.py`.

Covers the wiring `frontend/src/pages/admin/index.tsx` owns and the server-backed
write-path specs do not: the 5s poll, the search filter, the weight sort, and the
selection shared between the Header and the table. It mounts the real page body,
not a re-implementation of it, so it is the test that has to pass unchanged when
the file is split.

Time is driven by Playwright's clock rather than waited on, so the poll cases are
deterministic and fast.

The table renders every node once per row (in the name cell, the drawer title and
the region flag's title), so "which nodes are on screen" is read from the
`data-testid="node-row"` elements rather than from the page's text.
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
POLL_MS = 5000
NODE_LIST = "/api/admin/client/list"


def node(uuid, name=None, weight=0, **extra):
    # `region` is required in practice: the row renders <Flag flag={node.region}>,
    # and Flag calls Array.from on it, so an absent region throws during render.
    return {
        "uuid": uuid,
        "name": uuid if name is None else name,
        "weight": weight,
        "billing_cycle": 30,
        "region": "SG",
        **extra,
    }


def rendered_order(body):
    """The order the table renders a response in: sorted by weight."""
    return [item["uuid"] for item in sorted(body, key=lambda item: item["weight"])]


class AdminNodeTableBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/admin-node-table.browser.html"
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
                    raise RuntimeError("Vite exited before the node table fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Node table fixture did not become ready")
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
        self.page = self.browser.new_page()
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.body = self.page.locator("body")

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    # -- helpers ------------------------------------------------------------

    def mount(self, *responses):
        """Open the page with these node-list responses already queued.

        The queue has to exist before the app script runs: the provider fetches
        the list on mount, so a queue seeded after `goto` arrives one response too
        late and the first poll consumes the mount's answer.
        """
        self.page.add_init_script(
            "window.nodeListQueue = %s; window.nodeListHolds = new Map();"
            % json.dumps(list(responses))
        )
        self.page.clock.install()
        self.page.goto(self.url)
        self.wait_for_table()
        self.wait_rows(rendered_order(responses[0]["body"]))

    def queue(self, response):
        self.page.evaluate("(r) => window.adminNodeTableFixture.queue(r)", response)

    def list_requests(self):
        calls = self.page.evaluate("() => window.adminNodeTableFixture.urlCalls()")
        return [url for url in calls if url == NODE_LIST]

    def rendered(self):
        """The node names on screen, in the order the table renders them."""
        return self.page.get_by_test_id("node-row").evaluate_all(
            "rows => rows.map(row => row.getAttribute('data-node-uuid'))")

    def advance(self, ms):
        self.page.clock.run_for(ms)

    def wait_requests(self, count, timeout_ms=5000):
        """Wait until `count` node-list requests have been issued."""
        deadline = time.monotonic() + timeout_ms / 1000
        while time.monotonic() < deadline:
            if len(self.list_requests()) >= count:
                return
            self.page.wait_for_timeout(50)
        self.fail(
            f"expected {count} node-list requests, saw {len(self.list_requests())}")

    def wait_for_table(self):
        expect(self.page.get_by_test_id("node-table")).to_be_visible()

    def wait_rows(self, uuids):
        self.wait_for_table()
        # Report what was actually rendered when the wait fails, rather than only
        # the condition that did not hold.
        try:
            self.page.wait_for_function(
                "expected => window.adminNodeTableFixture.renderedRows().join(',') === expected",
                arg=",".join(uuids),
                timeout=5000,
            )
        except Exception as error:
            self.fail(
                f"expected rows {uuids}, saw {self.rendered()}: {error}")
        self.assertEqual(self.rendered(), list(uuids))

    # -- cases --------------------------------------------------------------

    def test_polls_on_its_own_interval_and_not_before_it(self):
        self.mount(
            {"body": [node("alpha")]},
            {"body": [node("alpha"), node("beta")]},
            {"body": [node("alpha"), node("beta"), node("gamma")]},
        )
        self.assertEqual(len(self.list_requests()), 1, "the mount fetch")

        # Well short of the interval: still one request. This is the assertion
        # that fails if the interval is rebuilt on every poll response, which is
        # the bug the dependency comment in the file warns about.
        self.advance(POLL_MS // 2)
        self.assertEqual(len(self.list_requests()), 1, "no poll at half an interval")

        # Across one interval, exactly one more request — not one per render.
        self.advance(POLL_MS)
        self.wait_rows(["alpha", "beta"])
        self.assertEqual(len(self.list_requests()), 2, "one poll per interval")

        self.advance(POLL_MS)
        self.wait_rows(["alpha", "beta", "gamma"])
        self.assertEqual(
            len(self.list_requests()), 3,
            "one request per interval, not one per render")

    def test_a_failed_poll_shows_the_error_and_the_next_one_recovers(self):
        self.mount(
            {"body": [node("alpha")]},
            {"status": 503, "body": {"status": "error"}},
            {"body": [node("alpha"), node("recovered")]},
        )
        self.advance(POLL_MS)
        expect(self.body).to_contain_text("Failed to load nodes (503)")

        self.advance(POLL_MS)
        self.wait_rows(["alpha", "recovered"])
        expect(self.body).not_to_contain_text("Failed to load nodes (503)")

    def test_an_older_response_cannot_replace_a_newer_one(self):
        self.mount(
            {"body": [node("alpha")]},
            {"hold": "slow"},
            {"body": [node("alpha"), node("latest")]},
        )
        # First poll: held open. Second poll: answers with the newer list.
        self.advance(POLL_MS)
        self.wait_requests(2)
        self.page.wait_for_function("() => window.adminNodeTableFixture.isHeld('slow')")
        self.advance(POLL_MS)
        self.wait_requests(3)
        self.wait_rows(["alpha", "latest"])

        # The held response lands afterwards and must be discarded, not painted.
        self.page.evaluate(
            """async () => window.adminNodeTableFixture.release(
                'slow', {body: [{uuid: 'stale', name: 'stale', weight: 0, billing_cycle: 30,
                                 region: 'SG'}]})"""
        )
        self.assertEqual(
            self.rendered(), ["alpha", "latest"],
            "a stale response must not replace a newer one")

    def test_filter_and_sort_are_applied_to_the_polled_list(self):
        # weight decides the order and the search box filters by name; the
        # response arrives with a name the filter will remove.
        self.mount(
            {"body": [
                node("charlie", "charlie", weight=30),
                node("alpha", "alpha", weight=10),
                node("bravo", "bravo", weight=20),
            ]},
        )
        self.wait_rows(["alpha", "bravo", "charlie"])

        self.page.get_by_placeholder("Search by name").fill("bra")
        self.wait_rows(["bravo"])

        self.page.get_by_placeholder("Search by name").fill("")
        self.wait_rows(["alpha", "bravo", "charlie"])

    def test_selection_is_shared_between_the_header_and_the_table(self):
        self.mount({"body": [node("alpha"), node("bravo")]})
        checkboxes = self.page.get_by_test_id("node-row").locator("button[role=checkbox]")
        self.assertEqual(checkboxes.count(), 2, "one checkbox per row")
        select_all = self.page.get_by_test_id("select-all")

        checkboxes.nth(0).click()
        expect(self.page.get_by_test_id("selected-count")).to_have_text("(1 selected)")

        checkboxes.nth(1).click()
        expect(self.page.get_by_test_id("selected-count")).to_have_text("(2 selected)")

        select_all.click()
        expect(self.page.get_by_test_id("selected-count")).to_have_count(0)

        select_all.click()
        expect(self.page.get_by_test_id("selected-count")).to_have_text("(2 selected)")

    def test_a_poll_does_not_disturb_the_selection(self):
        self.mount(
            {"body": [node("alpha"), node("bravo")]},
            {"body": [node("alpha"), node("bravo")]},
        )
        self.page.get_by_test_id("node-row").locator("button[role=checkbox]").nth(0).click()
        expect(self.page.get_by_test_id("selected-count")).to_have_text("(1 selected)")

        self.advance(POLL_MS)
        expect(self.page.get_by_test_id("node-table")).to_be_visible()
        expect(self.page.get_by_test_id("selected-count")).to_have_text("(1 selected)")


if __name__ == "__main__":
    unittest.main(verbosity=2)
