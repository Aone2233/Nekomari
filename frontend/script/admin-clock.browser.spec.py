"""Mounted admin-clock regressions: python script/admin-clock.browser.spec.py."""

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


class AdminClockBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.url = f"http://127.0.0.1:{port}/script/admin-clock.browser.html"
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
                    raise RuntimeError("Vite exited before admin clock fixture was ready")
                try:
                    with urlopen(cls.url, timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            else:
                raise RuntimeError("Admin clock fixture did not become ready")
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

    def test_sessions_list_and_detail_share_live_clock_without_refetch(self):
        page = self.browser.new_page()
        try:
            page.add_init_script("""
                window.__fixtureNow = Date.UTC(2026, 8, 23, 12);
                Date.now = () => window.__fixtureNow;
                window.__requests = 0;
                const timers = new Map();
                const setIntervalNative = window.setInterval.bind(window);
                const clearIntervalNative = window.clearInterval.bind(window);
                let nextTimer = 1000000;
                window.setInterval = (callback, delay, ...args) => {
                  if (delay !== 1000) return setIntervalNative(callback, delay, ...args);
                  const id = nextTimer++;
                  timers.set(id, () => callback(...args));
                  return id;
                };
                window.clearInterval = id => {
                  if (timers.delete(id)) return;
                  clearIntervalNative(id);
                };
                window.__advanceClock = amount => {
                  window.__fixtureNow += amount;
                  for (const tick of timers.values()) tick();
                };
                window.__activeClockTimers = () => timers.size;
                window.fetch = async input => {
                  if (String(input) !== '/api/admin/session/get') {
                    throw new Error('Unexpected request: ' + input);
                  }
                  window.__requests++;
                  return new Response(JSON.stringify({
                    status: 'success', current: 'none', data: [{
                      uuid: 'row1', session: 'session1234567890',
                      user_agent: 'Fixture browser', ip: '127.0.0.1',
                      latest_ip: '127.0.0.1', latest_user_agent: 'Fixture browser',
                      latest_online: new Date(window.__fixtureNow - 65000).toISOString(),
                      expires: new Date(window.__fixtureNow + 3600000).toISOString(),
                      created_at: new Date(window.__fixtureNow - 3600000).toISOString(),
                      login_method: 'password',
                    }],
                  }), { status: 200, headers: { 'Content-Type': 'application/json' } });
                };
            """)
            page.goto(self.url)
            age_cell = page.locator(".km-session-item td").nth(5)
            age_cell.get_by_text("1m5sago").wait_for()
            self.assertEqual(page.evaluate("window.__requests"), 1)
            self.assertEqual(page.evaluate("window.__activeClockTimers()"), 1)

            page.evaluate("window.__advanceClock(5000)")
            age_cell.get_by_text("1m10sago").wait_for()
            page.locator(".km-session-item td").first.click()
            dialog = page.get_by_role("dialog")
            dialog.get_by_text("1m10sago", exact=False).wait_for()

            page.evaluate("window.__advanceClock(1000)")
            age_cell.get_by_text("1m11sago").wait_for()
            dialog.get_by_text("1m11sago", exact=False).wait_for()
            self.assertEqual(page.evaluate("window.__requests"), 1)
            page.evaluate("window.unmountAdminClockFixture()")
            self.assertEqual(page.evaluate("window.__activeClockTimers()"), 0)
        finally:
            page.close()

    def test_dashboard_expiry_boundaries_and_renewed_exclusion(self):
        page = self.browser.new_page()
        try:
            page.goto(self.url)
            result = page.evaluate("""async () => {
              const { DAY_MS, daysUntilExpiry, getExpiringNodes } =
                await import('/src/pages/admin/expiry.ts');
              const now = Date.UTC(2026, 8, 23, 12);
              const node = (uuid, expiry) => ({
                uuid, expired_at: new Date(expiry).toISOString(),
              });
              const nodes = [
                node('expired', now - 1),
                node('renewed', now + 2 * DAY_MS),
                node('three', now + 3 * DAY_MS),
                node('seven', now + 7 * DAY_MS),
                node('outside', now + 7 * DAY_MS + 1),
              ];
              const renewed = new Set(['renewed']);
              return {
                initial: getExpiringNodes(nodes, renewed, now).map(n => n.uuid),
                later: getExpiringNodes(nodes, renewed, now + 60_000)
                  .map(n => n.uuid),
                redDays: daysUntilExpiry(nodes[2].expired_at, now),
                amberDays: daysUntilExpiry(
                  new Date(now + 3 * DAY_MS + 1).toISOString(), now),
              };
            }""")
            self.assertEqual(result["initial"], ["three", "seven"])
            self.assertEqual(result["later"], ["three", "seven", "outside"])
            self.assertEqual(result["redDays"], 3)
            self.assertEqual(result["amberDays"], 4)
        finally:
            page.close()

    def test_mounted_dashboard_moves_expiry_window_and_urgency(self):
        page = self.browser.new_page()
        try:
            page.add_init_script("""
                window.__fixtureNow = Date.UTC(2026, 8, 23, 12);
                Date.now = () => window.__fixtureNow;
                const timers = new Map();
                const setIntervalNative = window.setInterval.bind(window);
                const clearIntervalNative = window.clearInterval.bind(window);
                let nextTimer = 1000000;
                window.setInterval = (callback, delay, ...args) => {
                  if (delay !== 60000) return setIntervalNative(callback, delay, ...args);
                  const id = nextTimer++;
                  timers.set(id, () => callback(...args));
                  return id;
                };
                window.clearInterval = id => {
                  if (timers.delete(id)) return;
                  clearIntervalNative(id);
                };
                window.__advanceClock = amount => {
                  window.__fixtureNow += amount;
                  for (const tick of timers.values()) tick();
                };
                window.__activeClockTimers = () => timers.size;
                window.fetch = async input => {
                  if (String(input) !== '/api/admin/database/size') {
                    throw new Error('Unexpected request: ' + input);
                  }
                  return new Response(JSON.stringify({
                    data: { main: { size: 0 }, monitoring: { size: 0 } },
                  }), { status: 200, headers: { 'Content-Type': 'application/json' } });
                };
            """)
            dashboard_url = self.url.replace(
                "admin-clock.browser.html", "admin-dashboard-clock.browser.html"
            )
            page.goto(dashboard_url)
            card = page.locator(".km-dashboard-card").filter(has_text="Expiring soon")
            card.get_by_text("three").wait_for()
            self.assertEqual(card.get_by_text("outside").count(), 0)
            self.assertEqual(
                card.locator(".rt-Badge").filter(has_text="4 days left")
                .get_attribute("data-accent-color"), "amber"
            )
            self.assertEqual(page.evaluate("window.__activeClockTimers()"), 1)
            requests_before = page.evaluate("window.dashboardRequestCounts()")

            page.evaluate("window.__advanceClock(60000)")
            card.get_by_text("outside").wait_for()
            self.assertEqual(card.get_by_text("expiring", exact=True).count(), 0)
            self.assertEqual(
                card.locator(".rt-Badge").filter(has_text="3 days left")
                .get_attribute("data-accent-color"), "red"
            )
            self.assertEqual(page.evaluate("window.dashboardRequestCounts()"), requests_before)
            page.evaluate("window.unmountDashboardClockFixture()")
            self.assertEqual(page.evaluate("window.__activeClockTimers()"), 0)
        finally:
            page.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
