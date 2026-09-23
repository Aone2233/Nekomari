"""Mounted node-list recovery regression: python script/node-details.browser.spec.py."""

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


def node(uuid):
    return {"uuid": uuid, "name": uuid, "weight": 0, "billing_cycle": 0}


class NodeDetailsBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/node-details.browser.html"
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
                    raise RuntimeError("Vite exited before node details fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Node details fixture did not become ready")
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
        self.page.add_init_script("""
            window.nodeResponses = [{ body: [{ uuid: 'alpha', name: 'alpha', weight: 0, billing_cycle: 0 }] }];
            window.pendingNodeResponses = new Map();
            window.queueNodeResponse = next => window.nodeResponses.push(next);
            window.completeNodeResponse = async (key, response) => {
              const complete = window.pendingNodeResponses.get(key);
              if (!complete) throw new Error('No pending response: ' + key);
              window.pendingNodeResponses.delete(key);
              complete(response);
              await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
            };
            window.fetch = async input => {
              if (String(input) !== '/api/admin/client/list') {
                throw new Error('Unexpected request: ' + input);
              }
              let next = window.nodeResponses.shift();
              if (!next) throw new Error('No queued response');
              if (next.hold) {
                const key = next.hold;
                next = await new Promise(resolve => window.pendingNodeResponses.set(key, resolve));
              }
              if (next.reject) throw new Error(next.reject);
              return new Response(JSON.stringify(next.body), {
                status: next.status ?? 200,
                headers: { 'Content-Type': 'application/json' },
              });
            };
        """)
        self.page.goto(self.url)

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    def wait_state(self, uuids, error):
        expected = json.dumps({
            "nodeDetail": [node(uuid) for uuid in uuids],
            "isLoading": False,
            "error": error,
        }, separators=(",", ":"))
        expect(self.page.get_by_test_id("node-state")).to_have_text(expected)
        state = json.loads(self.page.get_by_test_id("node-state").inner_text())
        self.assertEqual([node["uuid"] for node in state["nodeDetail"]], uuids)
        self.assertEqual(state["error"], error)

    def refresh_with(self, response, uuids, error):
        self.page.evaluate("next => window.queueNodeResponse(next)", response)
        self.page.get_by_role("button", name="Refresh nodes").click()
        self.wait_state(uuids, error)

    def test_failure_recovery_and_invalid_responses(self):
        self.wait_state(["alpha"], None)
        self.refresh_with({"reject": "offline"}, ["alpha"], "offline")
        self.refresh_with({"body": [node("beta")]}, ["beta"], None)
        self.refresh_with({"status": 503, "body": {"status": "error"}},
                          ["beta"], "Failed to load nodes (503)")
        self.refresh_with({"body": {"status": "error"}},
                          ["beta"], "Invalid node list response")
        self.refresh_with({"body": [None]},
                          ["beta"], "Invalid node list response")
        self.refresh_with({"body": [{"uuid": "broken", "weight": 0, "billing_cycle": 0}]},
                          ["beta"], "Invalid node list response")
        self.refresh_with({"body": [node("gamma")]}, ["gamma"], None)

    def test_older_failure_cannot_override_newer_success(self):
        self.wait_state(["alpha"], None)
        self.page.evaluate("() => window.queueNodeResponse({hold: 'old'})")
        self.page.get_by_role("button", name="Refresh nodes").click()
        self.assertTrue(self.page.evaluate("() => window.pendingNodeResponses.has('old')"))
        self.refresh_with({"body": [node("latest")]}, ["latest"], None)
        self.page.evaluate("""async () => window.completeNodeResponse(
            'old', {status: 503, body: {status: 'error'}})""")
        self.wait_state(["latest"], None)

    def test_older_success_cannot_replace_newer_success(self):
        self.wait_state(["alpha"], None)
        self.page.evaluate("() => window.queueNodeResponse({hold: 'old'})")
        self.page.get_by_role("button", name="Refresh nodes").click()
        self.assertTrue(self.page.evaluate("() => window.pendingNodeResponses.has('old')"))
        self.refresh_with({"body": [node("latest")]}, ["latest"], None)
        self.page.evaluate("""async () => window.completeNodeResponse(
            'old', {body: [{uuid: 'stale', name: 'stale', weight: 0, billing_cycle: 0}]})""")
        self.wait_state(["latest"], None)


if __name__ == "__main__":
    unittest.main(verbosity=2)
