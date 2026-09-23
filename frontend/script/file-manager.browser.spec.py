"""Mounted file-manager regressions: python script/file-manager.browser.spec.py."""

import socket
import subprocess
import tempfile
import time
import unittest
from pathlib import Path
from urllib.error import URLError
from urllib.request import urlopen

from playwright.sync_api import sync_playwright


ROOT = Path(__file__).resolve().parents[1]


class FileManagerBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/file-manager.browser.html"
        cls.output = tempfile.TemporaryFile(mode="w+t", encoding="utf-8")
        cls.server = None
        cls.playwright = None
        cls.browser = None
        try:
            cls.server = subprocess.Popen(
                ["node", str(ROOT / "node_modules/vite/bin/vite.js"),
                 "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
                cwd=ROOT,
                stdout=cls.output,
                stderr=subprocess.STDOUT,
            )
            for _ in range(100):
                if cls.server.poll() is not None:
                    raise RuntimeError("Vite exited before file manager fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("File manager fixture did not become ready")
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

    def open_page(self):
        page = self.browser.new_page(viewport={"width": 1200, "height": 800})
        page.goto(self.url)
        page.locator('[data-file-path="/alpha.txt"]').wait_for()
        return page

    def test_rename_and_delete_refresh_the_actual_directory(self):
        page = self.open_page()
        try:
            page.locator('[data-file-path="/alpha.txt"]').click()
            page.get_by_title("Rename", exact=True).first.click()
            rename = page.locator('[data-file-path="/alpha.txt"] input')
            rename.fill("renamed.txt")
            rename.press("Enter")
            page.locator('[data-file-path="/renamed.txt"]').wait_for()
            self.assertEqual(
                [entry["params"] for entry in page.evaluate("fileFixture.calls()")
                 if entry["method"] == "admin:fileMove"],
                [{"uuid": "A", "source": "/alpha.txt", "destination": "/renamed.txt"}],
            )

            page.locator('[data-file-path="/renamed.txt"]').click()
            page.get_by_title("Delete", exact=True).first.click()
            dialog = page.get_by_role("alertdialog", name="Delete")
            dialog.get_by_text("Delete renamed.txt?").wait_for()
            dialog.get_by_role("button", name="Delete").click()
            page.locator('[data-file-path="/renamed.txt"]').wait_for(state="detached")
            self.assertEqual(
                [entry["params"] for entry in page.evaluate("fileFixture.calls()")
                 if entry["method"] == "admin:fileDelete"],
                [{"uuid": "A", "path": "/renamed.txt"}],
            )
            self.assertGreaterEqual(
                len([entry for entry in page.evaluate("fileFixture.calls()")
                     if entry["method"] == "admin:fileList" and entry["params"] == {"uuid": "A", "path": "/"}]),
                3,
            )
        finally:
            page.close()

    def test_old_directory_response_cannot_replace_new_node(self):
        page = self.open_page()
        try:
            page.evaluate("fileFixture.holdList('A', '/slow')")
            path = page.get_by_role("textbox", name="Path")
            path.fill("/slow")
            path.press("Enter")
            page.wait_for_function("fileFixture.pendingList('A', '/slow')")
            page.evaluate("fileFixture.setNode('B')")
            page.locator('[data-file-path="/beta.txt"]').wait_for()
            page.evaluate("""async () => {
                fileFixture.resolveList('A', '/slow', ['stale.txt']);
                await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
            }""")
            self.assertEqual(path.input_value(), "/")
            self.assertEqual(page.locator('[data-file-path="/beta.txt"]').count(), 1)
            self.assertEqual(page.locator('[data-file-path="/slow/stale.txt"]').count(), 0)
        finally:
            page.close()

    def test_delayed_read_updates_its_tab_without_reactivating_it(self):
        page = self.open_page()
        try:
            page.evaluate("fileFixture.holdRead('/one.md')")
            page.evaluate("fileFixture.setView('editor')")
            page.wait_for_function("fileFixture.pendingRead('/one.md')")
            page.locator('.km-file-editor [title="/one.md"] .animate-spin').wait_for()
            page.evaluate("fileFixture.setEditorFile('/two.md')")
            dialog = page.get_by_role("dialog", name="/two.md")
            dialog.wait_for()
            page.evaluate("fileFixture.resolveRead('/one.md', 'late first document')")
            page.locator('.km-file-editor [title="/one.md"] .animate-spin').wait_for(state="detached")
            self.assertEqual(dialog.get_attribute("aria-label"), "/two.md")
            self.assertEqual(page.locator('.km-file-editor [title="/one.md"]').count(), 1)
            self.assertEqual(page.locator('.km-file-editor [title="/two.md"]').count(), 1)
        finally:
            page.close()

    def test_refresh_after_reconnect_ignores_a_prior_connection_response(self):
        page = self.open_page()
        try:
            page.evaluate("fileFixture.holdList('A', '/')")
            page.get_by_title("Refresh", exact=True).first.click()
            page.wait_for_function("fileFixture.pendingList('A', '/')")
            page.evaluate("fileFixture.setConnected(false)")
            page.wait_for_function("document.querySelector('[data-testid=rpc-connection]').dataset.state === 'disconnected'")
            self.assertEqual(page.get_by_test_id("rpc-connection").get_attribute("data-state"), "disconnected")
            page.evaluate("""() => {
                fileFixture.setDirectory('A', '/', ['reconnected.txt']);
                fileFixture.setConnected(true);
            }""")
            page.wait_for_function("document.querySelector('[data-testid=rpc-connection]').dataset.state === 'connected'")
            page.get_by_title("Refresh", exact=True).first.click()
            page.locator('[data-file-path="/reconnected.txt"]').wait_for()
            page.evaluate("""async () => {
                fileFixture.resolveList('A', '/', ['stale.txt']);
                await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
            }""")
            self.assertEqual(page.locator('[data-file-path="/reconnected.txt"]').count(), 1)
            self.assertEqual(page.locator('[data-file-path="/stale.txt"]').count(), 0)
        finally:
            page.close()


if __name__ == "__main__":
    unittest.main()
