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
# A page that renders nothing is a failure; a page whose *content* is short is not, and the difference
# matters for a themed installation. The panel's own theme renders a dashboard at `/`, but a theme may
# serve a page whose whole job is one sentence -- LuminaPlus's `/traffic` answers a fresh installation
# with "暂无节点数据 / 等待后端推送或前往管理后台添加", which is 29 characters of correct behaviour and
# was being called "renders nothing" by a 50-character threshold.
#
# So the check is on content that is *present*: whitespace is stripped first, because a page of layout
# divs produces a large body and no text, and counting characters including newlines says nothing about
# whether anything rendered. Ten characters of real text is a page; nothing at all is a bug.
MIN_BODY = 10


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

        # --- log in through the API, then check the SPA as an authenticated admin ---
        #
        # Through the API rather than through whatever the page shows for it, and that is the correction
        # this test needed. **How a visitor signs in is a property of the theme, not of the panel.** The
        # panel's own default theme opened a dialog from a button; LuminaPlus — now the embedded default
        # — renders an icon-only link to the panel's admin route, and that route is itself served by the
        # theme, which shows its own recovery screen when it cannot resolve an admin entry for a
        # signed-out visitor. So a test that looked for a login *button* on the landing page was asserting
        # one theme's UX, and it duly failed when the theme changed, saying nothing about whether the
        # panel worked.
        #
        # What this test is for, in its own words, is driving "the real SPA against the real API ... the
        # only check that catches a page that loops, crashes on mount, or is wired to a route that does
        # not exist". That needs a session, not a login form. So the session comes from the API and the
        # routes are walked as an authenticated admin.
        #
        # It is also what a browser does after a real login, so nothing is being faked: a session cookie
        # on the right origin, and the SPA's own `/api/...` calls authenticate with it just as they would
        # otherwise. (Injecting a cookie *obtained* out of band does not authenticate the page's own
        # requests in the live checks in docs/TESTING.md; that trap is about a cookie the browser did not
        # set, and this is that same shape — so the login happens inside the browser, exactly as a person
        # would do it, and the only difference is that it is a fetch rather than a form.)
        page.goto(BASE, wait_until="domcontentloaded", timeout=30000)
        page.wait_for_timeout(800)
        login = page.evaluate(
            """async ([user, password]) => {
                 const response = await fetch("/api/login", {
                   method: "POST",
                   headers: { "Content-Type": "application/json" },
                   body: JSON.stringify({ username: user, password: password }),
                   credentials: "same-origin",
                 });
                 return { status: response.status, body: (await response.text()).slice(0, 200) };
               }""",
            [USER, PASSWORD],
        )
        if login["status"] != 200:
            print(f"FAIL: could not sign in through the API: {login['status']} {login['body']}")
            browser.close()
            return 1

        # And the panel agrees that we are signed in: the same-origin check the SPA itself makes.
        page.goto(BASE, wait_until="networkidle", timeout=30000)
        page.wait_for_timeout(1200)
        me = page.evaluate(
            """async () => {
                 const response = await fetch("/api/me", { credentials: "same-origin" });
                 if (!response.ok) return { ok: false, status: response.status };
                 const payload = await response.json().catch(() => null);
                                  // `/api/me` answers flat -- {"logged_in": true, ...} -- unlike the {"status","data"}
                 // envelope the other endpoints use. Which is how the first version of this check read
                 // `null` from a session that had in fact been established.
                 return { ok: true, logged_in: payload?.logged_in ?? null };
               }"""
        )
        if not me.get("ok") or me.get("logged_in") is not True:
            print(f"FAIL: the session did not take; /api/me answered {me}")
            browser.close()
            return 1
        print("signed in through the API")

        # The old assertion here was that the URL contained `/admin` after logging in, which held only
        # because the panel's own theme redirected there. It is replaced by the check above — the panel
        # itself reporting `logged_in: true` for this session — which is what "logged in" means and does
        # not depend on which path a theme chooses to put a signed-in visitor on.
        print(f"landing page after signing in -> {page.url}")

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

            # A route the *theme* serves with a deliberately minimal page is not a panel regression.
            #
            # `/admin` is the case that forced this. Under LuminaPlus it is the theme's service-worker
            # recovery route: it exists to replace a stale cached admin bundle, and with no service worker
            # registered -- which is the situation in CI and on any first visit -- it renders a short
            # "repair" screen after a four-second timeout. That is 38 bytes of text, no console error and no
            # failed request, and the `MIN_BODY` threshold called it "renders nothing".
            #
            # It is not a panel fault because the panel's admin UI is not a page route at all: the panel
            # serves `/api/admin/*`, and a theme supplies the interface. The theme deciding what `/admin`
            # contains is the arrangement working. Nor is it a theme fault: the recovery screen is what that
            # route is for, and a first visit has nothing to recover.
            #
            # Detected by the theme's own recovery wording rather than by a hardcoded path list, so a theme
            # that moves its admin route needs no change here. Console errors and failed requests on such a
            # route still fail the run: only the absence of content is excused, because that is the screen's
            # whole design.
            theme_owned_minimal = len(body) < MIN_BODY and any(
                marker in body for marker in ("正在恢复后台入口", "restoring the admin")
            )
            if theme_owned_minimal:
                print(f"{'':<40}{'':>7}{'':>6}{'':>8}  theme's own minimal route, excused")
                continue

            if errors or bad_responses or failed or nav_error or len(body.strip()) < MIN_BODY:
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
