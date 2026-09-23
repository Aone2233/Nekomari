"""Real-environment panel smoke test.

Boots nothing itself: point it at an already-running server, and it logs in,
harvests the navigation routes the panel exposes, visits every one of them, and
fails if any route produces a same-origin console error, a same-origin failed
request, or renders nothing.

This is deliberately different from the mounted fixtures in this directory.
Those mount one component against stubs; this drives the real SPA against the
real API, which is the only check that catches a page that loops, crashes on
mount, or is wired to a route that does not exist. `/admin/settings/sign-on`
looped on every visit for exactly that reason while every fixture stayed green.

Usage:
    python script/panel-smoke.spec.py [base_url]

Environment:
    PANEL_USER, PANEL_PASSWORD   credentials for the instance (default admin/E2e-verify1)
    PANEL_EXTRA_ROUTES           comma-separated extra routes to visit

External origins (the panel's update check calls api.github.com) are reported as
warnings and never fail the run: a blocked or rate-limited third party is not a
regression in this code.
"""
import os
import sys
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else os.environ.get("PANEL_BASE_URL", "http://127.0.0.1:25774")).rstrip("/")
USER = os.environ.get("PANEL_USER", "admin")
PASSWORD = os.environ.get("PANEL_PASSWORD", "E2e-verify1")
EXTRA = [r for r in os.environ.get("PANEL_EXTRA_ROUTES", "").split(",") if r]

HOST = urlparse(BASE).netloc
MIN_BODY = 50


def same_origin(url: str) -> bool:
    return urlparse(url).netloc == HOST


def main() -> int:
    problems = []
    warnings = []

    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()

        console: list[tuple[str, str]] = []
        failed: list[str] = []
        bad_responses: list[str] = []
        page.on("console", lambda m: console.append((m.type, m.text)))
        page.on("pageerror", lambda e: console.append(("pageerror", str(e))))
        page.on(
            "requestfailed",
            lambda r: failed.append(r.url) if same_origin(r.url) else warnings.append(f"external request failed: {r.url}"),
        )

        def on_response(r):
            if r.status < 400:
                return
            if same_origin(r.url):
                bad_responses.append(f"{r.status} {r.url}")
            else:
                warnings.append(f"external {r.status}: {r.url}")

        page.on("response", on_response)

        # --- log in through the real dialog ---
        page.goto(BASE, wait_until="networkidle", timeout=30000)
        page.wait_for_timeout(1200)
        trigger = page.get_by_role("button", name="登录")
        if trigger.count() == 0:
            trigger = page.get_by_role("button", name="Login")
        if trigger.count() == 0:
            print("FAIL: no login button found on the landing page")
            browser.close()
            return 1
        trigger.first.click()
        page.wait_for_selector('[role="dialog"]', timeout=15000)
        dialog = page.locator('[role="dialog"]')
        dialog.locator("input").nth(0).fill(USER)
        dialog.locator('input[type="password"]').fill(PASSWORD)
        dialog.get_by_role("button", name="登录").or_(dialog.get_by_role("button", name="Login")).first.click()
        page.wait_for_timeout(3000)
        if "/admin" not in page.url:
            print(f"FAIL: login did not reach an admin route (landed on {page.url})")
            browser.close()
            return 1
        print(f"logged in -> {page.url}")

        # --- harvest the routes the panel actually exposes ---
        hrefs = set()
        for anchor in page.locator("a[href]").all():
            href = anchor.get_attribute("href") or ""
            if href.startswith("/") and not href.startswith("//") and "." not in href.rsplit("/", 1)[-1]:
                hrefs.add(href)
        routes = sorted(hrefs | {"", "/admin"} | set(EXTRA))
        print(f"discovered {len(hrefs)} navigation routes; visiting {len(routes)}\n")

        print(f"{'route':<40}{'body':>7}{'errs':>6}{'reqfail':>8}  first problem")
        for route in routes:
            console.clear()
            failed.clear()
            bad_responses.clear()
            nav_error = ""
            try:
                page.goto(BASE + route, wait_until="networkidle", timeout=25000)
                page.wait_for_timeout(700)
            except Exception as exc:  # noqa: BLE001
                nav_error = str(exc)[:90]
            try:
                body = page.inner_text("body")
            except Exception:  # noqa: BLE001
                body = ""
            # The browser emits a URL-less "Failed to load resource" console error
            # for every 4xx/5xx, including third-party ones we must not fail on.
            # Same-origin HTTP failures are collected precisely from the response
            # event instead, so that generic message is dropped here and only real
            # JS exceptions (pageerror, React errors) remain as console problems.
            errors = [
                c for c in console
                if c[0] in ("error", "pageerror") and "Failed to load resource" not in c[1]
            ]
            sample = (errors[0][1] if errors else (bad_responses[0] if bad_responses else (failed[0] if failed else nav_error)))[:90]
            print(f"{route or '/':<40}{len(body):>7}{len(errors):>6}{len(bad_responses) + len(failed):>8}  {sample}")
            if errors or bad_responses or failed or nav_error or len(body) < MIN_BODY:
                problems.append((route or "/", len(body), len(errors), len(bad_responses) + len(failed), nav_error, sample))

        browser.close()

    print()
    if warnings:
        uniq = sorted(set(warnings))
        print(f"{len(uniq)} external warning(s) (not failures):")
        for w in uniq[:5]:
            print(f"    {w[:120]}")

    if problems:
        print(f"\nFAIL: {len(problems)} route(s) with problems")
        for route, body, errs, reqfail, nav, sample in problems:
            print(f"    {route}  body={body} console_errors={errs} failed_requests={reqfail} {nav} {sample}")
        return 1

    print(f"\nOK: all routes rendered with no same-origin console errors or failed requests")
    return 0


if __name__ == "__main__":
    sys.exit(main())
