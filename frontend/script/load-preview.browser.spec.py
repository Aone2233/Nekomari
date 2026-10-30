"""Mounted saved-rule preview: python script/load-preview.browser.spec.py."""
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


class LoadPreviewBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/load-preview.browser.html"
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
            window.requests = [];
            window.replies = [];
            window.fetch = async (input, init) => {
              const url = String(input);
              const json = (body, status=200) => new Response(JSON.stringify(body), {status, headers: {'Content-Type':'application/json'}});
              if (url === '/api/admin/client/list') return json([
                {uuid:'a',name:'Alpha',weight:0,billing_cycle:0},
                {uuid:'b',name:'Beta',weight:1,billing_cycle:0},
              ]);
              if (url === '/api/admin/ping') return json({status:'success',data:[]});
              if (url === '/api/admin/notification/load') return json({status:'success',data:[
                {id:7,name:'CPU test',clients:['a','b'],metric:'cpu',threshold:80,ratio:0.8,interval:15},
              ]});
              if (url === '/api/admin/notification/load/preview') {
                window.requests.push({method:init.method,body:JSON.parse(init.body)});
                const reply=window.replies.shift();
                if (!reply) throw Error('No preview response queued');
                if (reply.delay) await new Promise(resolve => setTimeout(resolve,reply.delay));
                return json(reply.body, reply.status || 200);
              }
              throw Error('Unexpected request: '+url);
            };
        """)
        self.page.goto(self.url)
        self.page.get_by_role("button", name="Preview rule").wait_for()

    def tearDown(self):
        self.assertEqual(self.errors, [])
        self.page.close()

    def test_preview_is_explicit_read_only_and_reports_unknown(self):
        self.page.evaluate("""() => window.replies.push({body:{status:'success',data:{
          evaluated_at:'2026-10-30T10:00:00Z',window_start:'2026-10-30T09:45:00Z',stored_cooldown_active:true,
          targets:[{client:'a',status:'matched',samples:3,threshold:80,exceeding:2,required:2,maintenance:true},
                   {client:'b',status:'no_data',maintenance:false}]}}})""")
        self.page.get_by_role("button", name="Preview rule").click()
        expect(self.page.get_by_text("No notification is sent", exact=False)).to_be_visible()
        self.assertEqual(self.page.evaluate("window.requests.length"), 0)
        self.page.get_by_role("button", name="Run preview").click()
        expect(self.page.get_by_text("Condition matched")).to_be_visible()
        expect(self.page.get_by_text("Maintenance window active at preview time")).to_be_visible()
        expect(self.page.get_by_text("No usable current data")).to_be_visible()
        expect(self.page.get_by_text("Above threshold: 2; required: 2")).to_be_visible()
        expect(self.page.get_by_text("The stored last-notified time is within", exact=False)).to_be_visible()
        self.assertEqual(self.page.evaluate("window.requests"), [{"method":"POST", "body":{"id":7}}])
        self.page.get_by_role("button", name="Close", exact=True).click()
        self.page.get_by_role("button", name="Preview rule").click()
        expect(self.page.get_by_text("Condition matched")).to_have_count(0)

    def test_error_and_late_response_do_not_display_a_stale_result(self):
        self.page.evaluate("""() => window.replies.push({delay:300,body:{status:'success',data:{
          evaluated_at:'2026-10-30T10:00:00Z',window_start:'2026-10-30T09:45:00Z',stored_cooldown_active:false,
          targets:[{client:'a',status:'matched',maintenance:false}]}}},
          {status:503,body:{status:'error',message:'Preview busy'}})""")
        self.page.get_by_role("button", name="Preview rule").click()
        self.page.get_by_role("button", name="Run preview").click()
        self.page.get_by_role("button", name="Close", exact=True).click()
        self.page.get_by_role("button", name="Preview rule").click()
        self.page.get_by_role("button", name="Run preview").click()
        expect(self.page.get_by_role("alert")).to_have_text("Preview busy")
        self.page.wait_for_timeout(400)
        expect(self.page.get_by_text("Condition matched")).to_have_count(0)


if __name__ == "__main__":
    unittest.main()
