"""Mounted RemoteFileTree regressions: python script/remote-file-tree.browser.spec.py.

The file-manager fixture mounts `FileManagerPanel` and `FileEditorDialog` but
never opens the tree, so before this spec `RemoteFileTree` had no browser
coverage at all. This fixture mounts the tree directly against a stubbed RPC
client and asserts its behaviour: the root lists on mount, a directory lists
exactly once no matter how often it is expanded, a refresh re-lists, a reveal
expands its ancestor chain, and selection and the context menu act on the row
that was actually clicked.
"""

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


class RemoteFileTreeBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/remote-file-tree.browser.html"
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
                    raise RuntimeError("Vite exited before remote file tree fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Remote file tree fixture did not become ready")
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
        page.locator('[data-tree-path="/root"]').wait_for()
        page.locator('[data-tree-path="/root/readme.txt"]').wait_for()
        return page

    def list_paths(self, page):
        return [entry["params"]["path"] for entry in page.evaluate("treeFixture.lists()")]

    def test_root_lists_on_mount_exactly_once(self):
        page = self.open_page()
        try:
            self.assertEqual(
                page.locator('[data-tree-path="/root/docs"]').count(), 1,
            )
            self.assertEqual(
                page.locator('[data-tree-path="/root/src"]').count(), 1,
            )
            # Nothing is expanded below the root yet.
            self.assertEqual(page.locator('[data-tree-path="/root/docs/notes.md"]').count(), 0)
            self.assertEqual(self.list_paths(page), ["/root"])
        finally:
            page.close()

    def test_expanding_a_directory_lists_it_once_and_re_expanding_does_not(self):
        page = self.open_page()
        try:
            docs = page.locator('[data-tree-path="/root/docs"]')
            docs.click()
            page.locator('[data-tree-path="/root/docs/notes.md"]').wait_for()
            self.assertEqual(self.list_paths(page), ["/root", "/root/docs"])

            # Collapse and expand again. The cache is the only thing that can
            # stop the second expansion from re-listing the directory.
            docs.click()
            page.locator('[data-tree-path="/root/docs/notes.md"]').wait_for(state="detached")
            docs.click()
            page.locator('[data-tree-path="/root/docs/notes.md"]').wait_for()
            self.assertEqual(
                self.list_paths(page).count("/root/docs"), 1,
                "re-expanding a listed directory must not re-list it",
            )
            self.assertEqual(self.list_paths(page), ["/root", "/root/docs"])
        finally:
            page.close()

    def test_refresh_forces_a_re_list_of_the_root(self):
        page = self.open_page()
        try:
            self.assertEqual(self.list_paths(page).count("/root"), 1)
            page.get_by_test_id("refresh").click()
            page.wait_for_function(
                "treeFixture.lists('/root').length === 2",
            )
            self.assertEqual(self.list_paths(page).count("/root"), 2)
            self.assertEqual(page.locator('[data-tree-path="/root/readme.txt"]').count(), 1)
        finally:
            page.close()

    def test_reveal_expands_the_ancestor_chain(self):
        page = self.open_page()
        try:
            self.assertEqual(page.locator('[data-tree-path="/root/docs/notes.md"]').count(), 0)
            page.get_by_test_id("reveal").click()
            page.locator('[data-tree-path="/root/docs/deep/leaf.txt"]').wait_for()
            self.assertEqual(page.locator('[data-tree-path="/root/docs/deep"]').count(), 1)
            # The reveal effect lists `remoteAncestors(revealPath)` minus the
            # root and the leaf itself. That list starts at the filesystem root,
            # so `/` is listed too — pre-existing behaviour, recorded here rather
            # than asserted away. `/root/docs` is deliberately *not* re-listed:
            # the expansion above came from the render-time adjustment, and the
            # memo is what stops the effect's own walk from duplicating it.
            self.assertEqual(self.list_paths(page), ["/root", "/", "/root/docs", "/root/docs/deep"])
        finally:
            page.close()

    def test_changing_the_root_path_re_lists_the_new_root_and_does_not_reuse_the_cache(self):
        page = self.open_page()
        try:
            docs = page.locator('[data-tree-path="/root/docs"]')
            docs.click()
            page.locator('[data-tree-path="/root/docs/notes.md"]').wait_for()
            self.assertEqual(self.list_paths(page).count("/root"), 1)

            page.get_by_test_id("root-other").click()
            page.locator('[data-tree-path="/other/elsewhere.txt"]').wait_for()
            self.assertEqual(page.locator('[data-tree-path="/root/docs"]').count(), 0)
            self.assertEqual(self.list_paths(page).count("/other"), 1)
            # The memo was dropped with the old tree, so the old root is not
            # re-listed and the new root is listed exactly once.
            self.assertEqual(self.list_paths(page), ["/root", "/root/docs", "/other"])
        finally:
            page.close()

    def test_selection_and_the_context_menu_target_the_clicked_row(self):
        page = self.open_page()
        try:
            readme = page.locator('[data-tree-path="/root/readme.txt"]')
            src = page.locator('[data-tree-path="/root/src"]')

            # A right-click on an unselected row selects it and targets it. The
            # row is deliberately not left-clicked first: a plain click on a file
            # row opens it in the editor, which would add a second open below.
            readme.click(button="right")
            self.assertIn("bg-[#264f78]", readme.get_attribute("class"))
            self.assertNotIn("bg-[#264f78]", src.get_attribute("class"))

            menu = page.get_by_role("menu")
            menu.wait_for()
            self.assertEqual(menu.get_by_role("menuitem", name="Open in editor", exact=True).count(), 1)
            menu.get_by_role("menuitem", name="Open in editor", exact=True).click()
            self.assertEqual(page.evaluate("treeFixture.opened()"), ["/root/readme.txt"])
            self.assertEqual(self.list_paths(page), ["/root"])

            src.click(button="right")
            menu = page.get_by_role("menu")
            menu.wait_for()
            self.assertEqual(menu.get_by_role("menuitem", name="Open", exact=True).count(), 1)
            self.assertEqual(menu.get_by_role("menuitem", name="New Folder", exact=True).count(), 1)
            # A directory menu is not the file menu.
            self.assertEqual(menu.get_by_role("menuitem", name="Open in editor", exact=True).count(), 0)
        finally:
            page.close()

    def test_context_menu_on_a_multi_selection_acts_on_every_selected_path(self):
        page = self.open_page()
        try:
            docs = page.locator('[data-tree-path="/root/docs"]')
            docs.click()
            page.locator('[data-tree-path="/root/docs/notes.md"]').wait_for()

            readme = page.locator('[data-tree-path="/root/readme.txt"]')
            notes = page.locator('[data-tree-path="/root/docs/notes.md"]')
            readme.click()
            notes.click(modifiers=["Control"])
            readme.click(button="right")
            menu = page.get_by_role("menu")
            menu.wait_for()
            self.assertEqual(
                menu.get_by_role("menuitem", name="Delete (2)", exact=True).count(), 1,
            )
        finally:
            page.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
