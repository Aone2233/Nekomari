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

Two kinds of route are walked:

* the navigation routes the *served document* links to, visited as found; and
* the routes named below, which a theme never links to and the href harvest therefore cannot
  discover. `/install` is a route of the panel's **front-end app** (`frontend/src/routes.ts`)
  that the installed theme used to answer with its own 404 — see `PANEL_OWNED_ROUTES` for what
  is asserted about it and why. `/database-recovery` is a *registered* route of the normal
  server and gets a different assertion again — see `REDIRECTED_ROUTES`.

Usage:
    python script/panel-smoke.spec.py [base_url]

Environment:
    PANEL_USER, PANEL_PASSWORD   credentials for the instance (default admin/E2e-verify1)
    PANEL_EXTRA_ROUTES           comma-separated extra routes to visit (visited only;
                                 the routes below carry the ownership/redirect assertions)

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

# Routes the panel's own front end serves and that no theme links to, so the href harvest above
# can never discover them. `/install` is a route of the **front-end app**
# (`frontend/src/routes.ts`), not of the admin interface: the server answers it from the
# **built-in default front end** (`panelOwnedPrefixes` -> `serveIndex` pins a panel-owned path to
# `currentTheme = DefaultTheme`), whose document is the front-end build -- `<title>Nekomari
# Monitor</title>`, bundle under `/assets/`. The admin package is a different build
# (`/admin/assets/`, `<title>Nekomari</title>`) and serves `/admin` and `/terminal` only.
# Asserting the admin markers here was wrong (and is what this list must not regress to).
#
# Both spellings: the prefix rule covers the trailing slash as well.
PANEL_OWNED_ROUTES = ("/install", "/install/")

# What a panel-owned route is checked against -- an exclusion, not a build stamp. The document
# for such a path can only come from the panel's own build or from the installed theme, and a
# pinned marker would have to guess which build this instance runs (the built-in front end is a
# build artifact; CI repacks it). So:
#
#   * it must not be the admin package (`/admin/assets/`) -- the wrong target an earlier revision
#     of this check asserted, and a real failure mode of its own; and
#   * when the panel itself says a theme is installed, it must not be that theme's document.
#
# Which theme is installed comes from the panel's own public settings (`GET /api/public` ->
# `data.theme`, the same field the front end reads). When that answers `default`, nothing is
# installed that could answer for `/install`, so the second check has nothing to judge and says
# so rather than guessing -- guessing is how a check starts failing on a correct instance.
PUBLIC_SETTINGS_ENDPOINT = "/api/public"
DEFAULT_THEME = "default"
ADMIN_BUNDLE_MARKER = "/admin/assets/"

# `/database-recovery` is a *registered* route of the normal application: the recovery UI belongs
# to the temporary restricted listener, so the normal server answers it with a 307 to the landing
# page (`internal/server/runtime.go`). It is visited, and what is asserted is that redirect --
# never that it serves a panel document, which cannot be true here. (The recovery page itself, on
# the restricted listener, is a different deployment and not what this smoke points at.)
REDIRECTED_ROUTES = {"/database-recovery": "/"}

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


def admin_bundle_problem(document: str) -> str:
    """Return "" unless the document is (wrongly) the panel's admin package.

    `/install` belongs to the front-end app, so a document loading `/admin/assets/` is the wrong
    build even though it is the panel's own. Read from the server's response, not the hydrated
    DOM: which document was served is a property of the response.
    """
    if ADMIN_BUNDLE_MARKER in document:
        return (f"served the admin package (loads {ADMIN_BUNDLE_MARKER}); /install is a front-end "
                f"app route (frontend/src/routes.ts), not the /admin interface")
    return ""


def theme_takeover_problem(document: str, installed_theme, landing_document) -> str:
    """Return "" unless a panel-owned route is being answered by the installed theme.

    The installed theme's document is the one `/` serves (a theme replaces the public front end),
    so an exact match means the theme answered for this path too -- the defect `/install` is here
    to catch. Byte equality, not a marker: a theme can be anything, and both documents go through
    the same substitution when the theme answers them.

    `installed_theme` is the panel's own answer (`/api/public` -> `data.theme`). With no theme
    installed there is nothing that could take the path over, and the check deliberately says
    nothing instead of comparing `/install` with a `/` that is also the built-in front end.
    """
    if not installed_theme or installed_theme == DEFAULT_THEME:
        return ""
    if landing_document is None:
        return ""
    if document == landing_document:
        return f"served the installed theme's document ({installed_theme}): the same bytes as /"
    return ""


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

        # `page.request` is the browser context's request client: the same authenticated fetch a
        # person's reload would make, minus the JavaScript. Which document the *server* sent is
        # what the panel-owned checks are about, so they read it here rather than from the DOM.
        def read_document(path: str):
            """Return (html, "") for a document, or (None, why not)."""
            try:
                response = page.request.get(BASE + path, timeout=25000)
                if not response.ok:
                    return None, f"{path} answered {response.status}"
                return response.text(), ""
            except Exception as exc:  # noqa: BLE001
                return None, f"{path} could not be read: {exc}"[:90]

        # Which theme the panel says is installed -- the panel's own answer, not a guess from the
        # markup. `data.theme` is the field the front end itself reads (PublicInfoProvider).
        installed_theme = None
        try:
            settings = page.request.get(BASE + PUBLIC_SETTINGS_ENDPOINT, timeout=25000)
            if settings.ok:
                installed_theme = ((settings.json() or {}).get("data") or {}).get("theme")
        except Exception:  # noqa: BLE001
            installed_theme = None
        if installed_theme is None:
            print(f"note: {PUBLIC_SETTINGS_ENDPOINT} did not report a theme; "
                  f"{', '.join(PANEL_OWNED_ROUTES)} are still checked against the admin package, "
                  f"but a theme takeover there cannot be judged")
        else:
            print(f"installed theme: {installed_theme}")

        # The theme's document is whatever `/` serves, so it is read once here for the comparison.
        landing_document, landing_problem = read_document("/")
        if landing_problem:
            landing_document = None
            print(f"note: could not read the landing document ({landing_problem}); "
                  f"a theme takeover of {', '.join(PANEL_OWNED_ROUTES)} cannot be judged")

        # --- harvest the routes the panel actually exposes ---
        hrefs = set()
        for anchor in page.locator("a[href]").all():
            href = anchor.get_attribute("href") or ""
            if href.startswith("/") and not href.startswith("//") and "." not in href.rsplit("/", 1)[-1]:
                hrefs.add(href)
        routes = sorted(hrefs | {"", "/admin"} | set(PANEL_OWNED_ROUTES) | set(REDIRECTED_ROUTES) | set(EXTRA))
        print(f"discovered {len(hrefs)} navigation routes; visiting {len(routes)}"
              f" (including the panel's own {', '.join(PANEL_OWNED_ROUTES)}"
              f" and the redirecting {', '.join(REDIRECTED_ROUTES)})\n")

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

            # Two routes carry a contract the harvested ones cannot have, and it is a *different*
            # contract for each -- the server treats them differently (see the constants above).
            # Read after the navigation so the browser's console/request recording above still
            # describes the same visit.
            contract_problem = ""
            if route in PANEL_OWNED_ROUTES:
                document, problem = read_document(route)
                if problem:
                    contract_problem = problem
                else:
                    contract_problem = admin_bundle_problem(document) or theme_takeover_problem(
                        document, installed_theme, landing_document
                    )
            elif route in REDIRECTED_ROUTES:
                expected = REDIRECTED_ROUTES[route]
                try:
                    # Not following it here (unlike the visit above, which follows it to the
                    # landing page): the status and the Location are the contract, and a plain 200
                    # would mean the route is no longer registered.
                    hop = page.request.get(BASE + route, max_redirects=0, timeout=25000)
                    if hop.status != 307:
                        contract_problem = f"expected a 307 to {expected}, got {hop.status}"
                    else:
                        location = urlparse(hop.headers.get("location", "")).path
                        if location != expected:
                            contract_problem = f"307 to {location or '(no Location)'}, expected {expected}"
                except Exception as exc:  # noqa: BLE001
                    contract_problem = f"could not read the redirect: {exc}"[:90]

            sample = (contract_problem or (errors[0][1] if errors else (bad_responses[0] if bad_responses else (failed[0] if failed else nav_error))))[:90]
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
            #
            # Never excused on a panel-owned route: there the theme's screen *is* the defect
            # (F5/P2-1), and `/install` rendering a theme's short 404 is exactly what this test is
            # for. `/database-recovery` is not in that list: it redirects to `/`, so what is
            # excused here is the landing page the theme put there, not this route's own document.
            theme_owned_minimal = (
                route not in PANEL_OWNED_ROUTES
                and len(body) < MIN_BODY
                and any(marker in body for marker in ("正在恢复后台入口", "restoring the admin"))
            )
            if theme_owned_minimal:
                print(f"{'':<40}{'':>7}{'':>6}{'':>8}  theme's own minimal route, excused")
                continue

            if errors or bad_responses or failed or nav_error or contract_problem or len(body.strip()) < MIN_BODY:
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
