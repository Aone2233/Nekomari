"""Mounted execution-history regressions against synthetic, in-memory replies."""
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


class ExecHistoryBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/exec-history.browser.html"
        cls.output = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        cls.server = subprocess.Popen(
            ["node", str(ROOT / "node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
            cwd=ROOT, stdout=cls.output, stderr=subprocess.STDOUT,
        )
        cls.playwright = None
        cls.browser = None
        try:
            for _ in range(100):
                if cls.server.poll() is not None:
                    raise RuntimeError("Vite exited before fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(.1)
            else:
                raise RuntimeError("Fixture did not become ready")
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
        if cls.browser:
            cls.browser.close()
        if cls.playwright:
            cls.playwright.stop()
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
          window.requests = []; window.failList = false; window.holdDetail = false;
          window.fetch = async (input, init = {}) => {
            const json = (result, status=200) => new Response(JSON.stringify(result), {status, headers:{'Content-Type':'application/json'}});
            if (String(input) === '/api/admin/client/list') return json([{uuid:'node-a',name:'Alpha',weight:0,billing_cycle:0}]);
            if (String(input) !== '/api/rpc2') throw Error('Unexpected endpoint');
            const call = JSON.parse(init.body); window.requests.push(call);
            if (call.method === 'admin:listExecHistory') {
              if (window.failList) return json({error:'unavailable'}, 503);
              const task = call.params.cursor ? 'task-older' : 'task-current';
              return json({jsonrpc:'2.0',id:1,result:{tasks:[{task_id:task,target_count:2,result_count:1,pending_count:1,success_count:0,failed_count:0,unknown_count:0,counts_truncated:false,earliest_result_time:'2026-11-05T02:00:00Z',time_incomplete:true,status:'incomplete'}],...(call.params.cursor ? {} : {next_cursor:'task-current'})}});
            }
            if (call.method === 'admin:getExecHistoryDetail') {
              if (window.holdDetail) await new Promise(resolve => { window.releaseDetail = resolve; });
              return json({jsonrpc:'2.0',id:1,result:{task_id:call.params.task_id,results:[{cursor:'7',client:'deleted-node',client_truncated:false,created_at:'2026-11-05T02:00:00Z',finished_at:null,exit_code:null,output_bytes:41}]}});
            }
            if (call.method === 'admin:readExecHistoryChunk') {
              const text = call.params.kind === 'command' ? '<img src=x onerror="window.pwned=1">' : '<script>window.pwned=1</script>secret';
              const all = new TextEncoder().encode(text);
              const start = call.params.offset || 0;
              const end = Math.min(start + 16, all.length);
              const data = all.slice(start,end);
              return json({jsonrpc:'2.0',id:1,result:{data_base64:btoa(String.fromCharCode(...data)),offset:start,next_offset:end,total_bytes:all.length,has_more:end<all.length}});
            }
            throw Error('Unexpected method: ' + call.method);
          };
        """)
        self.page.goto(self.url)
        self.page.get_by_role("button", name="View history").wait_for()

    def tearDown(self):
        self.assertEqual(self.errors, [])
        self.page.close()

    def test_explicit_read_paging_and_plain_text(self):
        history = self.page.get_by_test_id("exec-history")
        self.assertEqual(self.page.evaluate("window.requests.length"), 0)
        history.get_by_role("button", name="View history").click()
        expect(history).to_contain_text("task-current")
        expect(history).to_contain_text("may contain secrets")
        self.assertEqual(self.page.evaluate("window.requests.map(r => r.method)"), ["admin:listExecHistory"])
        history.get_by_role("button", name="Older tasks").click()
        expect(history).to_contain_text("task-older")
        history.get_by_role("button", name="Newer tasks").click()
        expect(history).to_contain_text("task-current")
        history.get_by_role("button", name="Older tasks").click()
        expect(history).to_contain_text("task-older")
        history.get_by_role("button", name="Newer tasks").click()
        expect(history).to_contain_text("task-current")
        history.get_by_role("button", name="View details").click()
        expect(history).to_contain_text("deleted-node")
        expect(history).to_contain_text("Pending / unknown")
        expect(history).to_contain_text("Output can change between chunks")
        self.assertNotIn("admin:readExecHistoryChunk", self.page.evaluate("window.requests.map(r => r.method)"))
        history.get_by_role("button", name="Show command").click()
        expect(history.locator("pre")).to_contain_text("<img")
        history.get_by_role("button", name="Show output").click()
        expect(history.locator("pre")).to_have_count(2)
        expect(history.locator("pre").nth(1)).to_contain_text("<script>")
        self.assertIsNone(self.page.evaluate("window.pwned"))
        history.get_by_role("button", name="Next chunk").first.click()
        expect(history.locator("pre").first).to_contain_text('or="window.pwned')
        self.assertEqual(history.locator("img").count(), 0)
        self.assertTrue(all(item["method"] != "admin:exec" for item in self.page.evaluate("window.requests")))
        self.page.reload()
        expect(self.page.get_by_role("button", name="View history")).to_be_visible()
        self.assertEqual(self.page.locator("pre").count(), 0)

    def test_error_and_late_reply_fail_closed(self):
        history = self.page.get_by_test_id("exec-history")
        self.page.evaluate("window.failList = true")
        history.get_by_role("button", name="View history").click()
        expect(history.get_by_role("alert")).to_contain_text("could not be read")
        self.page.evaluate("window.failList = false")
        history.get_by_role("button", name="Retry").click()
        expect(history).to_contain_text("task-current")
        self.page.evaluate("window.holdDetail = true")
        history.get_by_role("button", name="View details").click()
        self.page.wait_for_function("typeof window.releaseDetail === 'function'")
        history.get_by_role("button", name="Close history").click()
        self.page.evaluate("window.releaseDetail()")
        self.page.wait_for_timeout(100)
        expect(history).not_to_contain_text("deleted-node")
        history.get_by_role("button", name="View history").click()
        expect(history).to_contain_text("task-current")
        expect(history).not_to_contain_text("deleted-node")

    def test_mobile_dark_and_keyboard(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.page.goto(self.url + "?theme=dark")
        history = self.page.get_by_test_id("exec-history")
        button = history.get_by_role("button", name="View history")
        button.focus()
        self.page.keyboard.press("Enter")
        expect(history).to_contain_text("task-current")
        detail = history.get_by_role("button", name="View details")
        detail.focus()
        self.page.keyboard.press("Enter")
        expect(history).to_contain_text("deleted-node")
        self.assertLessEqual(self.page.evaluate("document.documentElement.scrollWidth"), 390)


if __name__ == "__main__":
    unittest.main(verbosity=2)
