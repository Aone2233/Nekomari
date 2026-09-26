"""Mounted regression for the auto-discovery section.

Run with `python script/admin-autodiscovery.browser.spec.py`.

`AutoDiscoverySection` was split out of `pages/admin/index.tsx` with the rest of
A1, and until now only its *disabled* branch was mounted, indirectly, through the
page fixture's "add node" dialog. This drives the section directly so all three
branches are covered, and so the install command it builds has an assertion.

That command is a second implementation: `nodeTable/GenerateCommandButton.tsx`
builds the per-node command and this builds the auto-discovery one. They share
flags and quoting rules, so the flags asserted here are the same set the node
fixture relies on — divergence between them is the bug this is most likely to
catch.

`loading` and the backend version are read at mount, so a case picks its URL
rather than mutating state after the fact.
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
BASE_PATH = "/script/admin-autodiscovery.browser.html"
KEY = "adkey-0123456789abcdef"


class AutoDiscoveryBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.origin = f"http://127.0.0.1:{port}"
        cls.url = cls.origin + BASE_PATH
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
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Auto-discovery fixture did not become ready")
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

    def tearDown(self):
        try:
            self.assertEqual(self.errors, [])
        finally:
            self.page.close()

    # -- helpers ------------------------------------------------------------

    def open(self, query="", settings=None):
        """Mount the section with these settings, seeded before the app runs."""
        payload = settings if settings is not None else {}
        self.page.add_init_script(
            "window.__AD_SETTINGS__ = %s;" % _json(payload))
        self.page.goto(self.url + query)
        expect(self.page.get_by_test_id("ad-section")).to_be_visible()

    def command(self):
        return self.page.evaluate("() => window.autoDiscoveryFixture.command()")

    def set_settings(self, settings):
        self.page.evaluate("(s) => window.autoDiscoveryFixture.setSettings(s)", settings)
        self.page.wait_for_timeout(50)

    def options_toggle(self):
        return self.page.get_by_role("checkbox").first

    def platform(self, name):
        """The segmented control's own item. Radix gives each one role=radio, and
        its accessible name is how a user picks it — matching on the visible text
        is ambiguous, because each item is labelled twice in the DOM."""
        return self.page.get_by_test_id("ad-platform").get_by_role(
            "radio", name=name, exact=True)

    def test_loading_shows_a_placeholder_and_nothing_else(self):
        self.open("?loading=1", {"auto_discovery_key": KEY})
        # No command, no platform control: the section is not ready to build one.
        self.assertEqual(self.page.locator("textarea").count(), 0)
        expect(self.page.get_by_test_id("ad-platform")).to_have_count(0)

    def test_disabled_state_points_at_the_settings_page(self):
        self.open("", {})
        expect(self.page.get_by_text("Try auto discovery")).to_be_visible()
        # `Link` wraps a Radix Button, so this is an <a> that contains a button:
        # the href is the contract (it has to reach the general settings page),
        # and the role is not a reliable way to find it.
        link = self.page.locator("a[href='/admin/settings/general']")
        expect(link).to_have_count(1)
        expect(link.get_by_text("Go to general settings")).to_be_visible()
        self.assertEqual(self.page.locator("textarea").count(), 0)

    def test_enabled_state_builds_a_linux_command(self):
        self.open("", {"auto_discovery_key": KEY})
        command = self.command()
        # The two flags that identify this as the auto-discovery command, and the
        # key itself: a section that rendered the per-node command would pass
        # neither.
        self.assertIn("--auto-discovery", command)
        self.assertIn(KEY, command)
        self.assertNotIn("-t ", command)
        self.assertIn("install-node-agent.sh", command)
        self.assertIn("sudo bash -s --", command)
        self.assertEqual(self.command(), command, "the command must be stable across reads")

    def test_each_platform_uses_its_own_installer_and_quoting(self):
        self.open("", {"auto_discovery_key": KEY, "script_domain": "panel.example.test"})

        self.platform("Windows").click()
        windows = self.command()
        self.assertIn("install-node-agent.ps1", windows)
        self.assertIn("powershell.exe", windows)
        self.assertIn("panel.example.test", windows)

        self.platform("macOS").click()
        macos = self.command()
        self.assertIn("install-node-agent.sh", macos)
        self.assertIn("curl -fsSL", macos)

        self.platform("Docker").click()
        docker = self.command()
        self.assertIn("docker run -d --name komari-agent", docker)
        # The installer-only flags must not reach the container's command line.
        self.assertNotIn("--install-dir", docker)
        self.assertNotIn("--install-service-name", docker)

    def test_script_domain_overrides_the_page_origin(self):
        self.open("", {"auto_discovery_key": KEY, "script_domain": "cdn.example.test"})
        self.assertIn("-e http://cdn.example.test", self.command())

        # A domain that already carries a scheme keeps it; a bare host gets http.
        self.open("", {"auto_discovery_key": KEY, "script_domain": "https://already.example/"})
        self.assertIn("-e https://already.example", self.command())

    def test_install_options_reach_the_command(self):
        self.open("", {"auto_discovery_key": KEY})
        before = self.command()

        self.options_toggle().click()
        expect(self.page.get_by_text("Install options")).to_be_visible()

        self.page.get_by_text("Disable web SSH").click()
        self.page.get_by_text("Disable auto update").click()
        after = self.command()

        self.assertIn("--disable-web-ssh", after)
        self.assertIn("--disable-auto-update", after)
        self.assertNotEqual(before, after)

    def test_a_snapshot_backend_pins_the_version_option(self):
        # `useIsSnapshotBackend` reads `common:getVersion`; a snapshot build has no
        # releases, so the section turns the pin on and fills it in itself — but
        # only once the options block is open, which is what the state sync keys on.
        #
        # Two traps this case had to get past, both of them the test's rather than
        # the component's: the version arrives from an async call, and the
        # auto-pin only happens while the options are expanded.
        self.open("?version=snapshot", {"auto_discovery_key": KEY})
        self.options_toggle().click()
        expect(self.page.get_by_text("Pin a version")).to_be_visible()
        self.page.wait_for_function(
            "() => window.autoDiscoveryFixture.command().includes('--version')")
        command = self.command()
        self.assertIn("--version", command)
        self.assertIn("snapshot", command)

        self.open("?version=v0.1.27", {"auto_discovery_key": KEY})
        self.options_toggle().click()
        expect(self.page.get_by_text("Pin a version")).to_be_visible()
        # Give the version call the same chance to land before asserting absence.
        self.page.wait_for_timeout(300)
        self.assertNotIn("--version", self.command())

    def test_the_copy_button_copies_what_is_shown(self):
        self.open("", {"auto_discovery_key": KEY})
        shown = self.command()
        self.page.get_by_role("button", name="Copy").click()
        self.page.wait_for_function("() => window.autoDiscoveryFixture.clipboard() !== null")
        self.assertEqual(
            self.page.evaluate("() => window.autoDiscoveryFixture.clipboard()"), shown)

    def test_settings_arriving_later_flip_the_section_from_disabled_to_enabled(self):
        # How the real page uses it: it mounts with the settings store still empty
        # and the key shows up after `/api/admin/settings` resolves.
        self.open("", {"script_domain": "panel.example.test"})
        expect(self.page.get_by_text("Try auto discovery")).to_be_visible()

        self.set_settings({"auto_discovery_key": KEY, "script_domain": "panel.example.test"})
        expect(self.page.get_by_text("Auto discovery", exact=True)).to_be_visible()
        self.assertIn(KEY, self.command())


def _json(value):
    """json.dumps, but kept out of the f-string above for readability."""
    import json

    return json.dumps(value)


if __name__ == "__main__":
    unittest.main(verbosity=2)
