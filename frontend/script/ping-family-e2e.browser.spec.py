"""Drive the mounted family selector against the isolated real Go backend."""

import os
import unittest
from urllib.parse import urlsplit

from playwright.sync_api import expect, sync_playwright


class PinnedBrowserAcceptance(unittest.TestCase):
    def test_multi_node_ipv6_creation(self):
        url = os.environ["NEKOMARI_PINNED_BROWSER_URL"]
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            page = browser.new_page(viewport={"width": 1200, "height": 900})
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            try:
                origin = urlsplit(url)
                page.context.add_cookies([{"name": "session_token", "value": os.environ["NEKOMARI_PINNED_BROWSER_SESSION"],
                                           "url": f"{origin.scheme}://{origin.netloc}/"}])
                page.goto(url)
                page.get_by_role("button", name="Add", exact=True).click()
                dialog = page.get_by_role("dialog", name="Add")
                dialog.locator("#ping_name").fill("Browser IPv6 acceptance")
                dialog.locator("#ping_target").fill("tj-ct-v6.ip.zstaticcdn.com:80")
                dialog.locator("#interval").fill("1")
                dialog.locator("#type").click()
                page.get_by_role("option", name="Auto (probe the target and pick a protocol it answers)").click()
                dialog.locator("#target_family").click()
                page.get_by_role("option", name="IPv6", exact=True).click()
                dialog.get_by_role("button", name="Select", exact=True).click()
                selector = page.get_by_role("dialog", name="Select")
                selector.get_by_role("button", name="Select all").click()
                selector.get_by_role("button", name="Done").click()
                expect(dialog.get_by_text("3 selected")).to_be_visible()
                with page.expect_response(lambda response: response.url.endswith("/api/admin/ping/add")) as pending:
                    dialog.get_by_role("button", name="Add", exact=True).click()
                response = pending.value
                self.assertEqual(response.status, 200, response.text())
                self.assertEqual(response.json()["status"], "success")
                request = response.request.post_data_json
                self.assertEqual(request["target"], "tj-ct-v6.ip.zstaticcdn.com:80")
                self.assertEqual(request["target_family"], "ipv6")
                self.assertEqual(request["interval"], 1)
                self.assertEqual(sorted(request["clients"]),
                                 ["browser-disabled", "browser-ipv6", "browser-nofamily"])
                expect(dialog).to_be_hidden()
                page.wait_for_function("window.isolatedPingAcceptance.refreshes === 1")
                self.assertEqual(errors, [])
            finally:
                page.close()
                browser.close()


if __name__ == "__main__":
    unittest.main()
