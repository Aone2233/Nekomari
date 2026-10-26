"""Check compiled in-panel tools against a disposable real Panel, never production.

python script/admin-tools-navigation.browser.spec.py --panel-binary /path/to/nekomari \
    --dist /path/to/dist --output /path/to/report.json
"""

import argparse
from datetime import datetime, timezone
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
from urllib.error import URLError
from urllib.parse import urlsplit
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright


ROOT = Path(__file__).resolve().parents[1]
PAGES = [
    ("sla", "status", "status-page"),
    ("bulk", "bulk", "bulk-page"),
    ("maintenance", "maintenance", "maintenance-page"),
    ("config", "config", "config-page"),
    ("forecast", "forecast", "forecast-page"),
]
LOCALES = [("en-US", "en"), ("zh-CN", "zh_CN"), ("zh-TW", "zh_TW"),
           ("ja-JP", "ja_JP"), ("id-ID", "id_ID")]


def unused_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def stop(process):
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def run(binary, dist, output):
    os.umask(0o077)
    scratch = Path(tempfile.mkdtemp(prefix="admin-tools-owned-", dir=output.parent))
    report = dict(at_utc=datetime.now(timezone.utc).isoformat(), status="running",
                  binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                  admin_document_sha256=hashlib.sha256((dist / "standalone/admin/index.html").read_bytes()).hexdigest(),
                  source="compiled assets, real HTTP/RPC/WebSocket and isolated synthetic credentials",
                  scenarios=[], checks=[], production_changed=False)
    backend_port, ui_port = unused_port(), unused_port()
    backend, origin = f"http://127.0.0.1:{backend_port}", f"http://127.0.0.1:{ui_port}"
    opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(http.cookiejar.CookieJar()))
    server = proxy = None

    def request(path, data=None):
        payload = None if data is None else json.dumps(data).encode()
        with opener.open(Request(backend + path, data=payload, headers={"Content-Type": "application/json"}), timeout=20) as response:
            return json.load(response)

    def ready(url, process):
        for _ in range(200):
            assert process.poll() is None, "Owned process exited before readiness"
            try:
                with build_opener(ProxyHandler({})).open(url, timeout=1) as response:
                    if response.status == 200:
                        return
            except (URLError, TimeoutError):
                time.sleep(0.1)
        raise TimeoutError("Owned process never became ready")

    try:
        with (scratch / "process.log").open("x") as log:
            server = subprocess.Popen([str(binary), "-d", str(scratch / "panel.db"), "server", "--listen", f"127.0.0.1:{backend_port}"],
                                      cwd=scratch, stdout=log, stderr=subprocess.STDOUT,
                                      env={**os.environ, "HOME": str(scratch), "TZ": "UTC"})
            ready(backend + "/api/install/status", server)
            username, password = "owned-navigation", "Owned-Navigation1-" + secrets.token_urlsafe(24)
            request("/api/install/complete", dict(username=username, password=password,
                    sitename="Owned navigation fixture", metric_dsn=str(scratch / "metrics.db")))
            ready(backend + "/api/version", server)
            request("/api/login", dict(username=username, password=password))
            request("/api/admin/settings/", dict(eula_accepted=True, private_site=True))
            node = request("/api/admin/client/add", dict(name="Owned navigation node"))
            assert node["uuid"]
            proxy = subprocess.Popen(["node", str(ROOT / "script/production-assets-server.mjs"), str(dist), backend, str(ui_port)],
                                     cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
            ready(origin + "/admin/account", proxy)

            with sync_playwright() as pw:
                browser = pw.chromium.launch(headless=True)
                try:
                    for locale, code in LOCALES:
                        strings = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())
                        for width, appearance, system in [(1280, "dark", "light"), (390, "light", "dark")]:
                            context = browser.new_context(locale=locale, color_scheme=system,
                                viewport={"width": width, "height": 950}, service_workers="block")
                            try:
                                response = context.request.post(origin + "/api/login", data=dict(username=username, password=password))
                                assert response.ok and response.json().get("status") == "success"
                                context.add_init_script("""(() => {
                                    localStorage.setItem('appearance', APPEARANCE);
                                    localStorage.setItem('color', 'teal');
                                    localStorage.setItem('language', LANGUAGE);
                                    localStorage.setItem('nekomari.github.releases.v3', JSON.stringify({
                                        fetchedAt: Date.now(), attemptedAt: Date.now(), releases: []}));
                                })()""".replace("APPEARANCE", json.dumps(appearance)).replace("LANGUAGE", json.dumps(locale)))
                                page = context.new_page()
                                errors, documents = [], []
                                page.on("pageerror", lambda error: errors.append(str(error)))
                                page.on("request", lambda req: documents.append(urlsplit(req.url).path)
                                        if req.is_navigation_request() and req.frame == page.main_frame else None)
                                page.route("**/*", lambda route: route.continue_() if route.request.url.startswith(origin + "/") else route.abort())
                                page.goto(origin + "/admin/account")
                                expect(page.locator(".km-admin-panel-topbar")).to_be_visible(timeout=15000)
                                page.evaluate("window.__navigationMarker = 'same-document'; window.__shellNode = document.querySelector('.km-admin-panel-bar')")
                                root = page.locator(".theme-root")

                                def assert_theme(value):
                                    page.wait_for_function("value => document.querySelector('.theme-root').classList.contains(value)", arg=value)
                                    expect(root).to_have_attribute("data-accent-color", "teal")
                                    assert page.evaluate("document.documentElement.classList.contains('dark')") is (value == "dark")

                                def settled_content():
                                    page.wait_for_function("""() => {
                                        const main = document.querySelector('.km-admin-panel-content');
                                        return getComputedStyle(main).display !== 'none' &&
                                            getComputedStyle(main).opacity === '1' &&
                                            (!matchMedia('(max-width: 767px)').matches ||
                                             document.querySelector('.km-admin-panel-nav').getBoundingClientRect().width < 1);
                                    }""")

                                def open_tool(name):
                                    nav = page.locator(".km-admin-panel-nav")
                                    content = page.locator(".km-admin-panel-content")
                                    closed = content.evaluate("element => getComputedStyle(element).display !== 'none'") if width < 768 else nav.evaluate("element => element.getBoundingClientRect().width < 10")
                                    if closed:
                                        page.locator(".km-admin-panel-topbar").get_by_role("button", name=strings["common"]["menu_sidebar"], exact=True).click()
                                    page.wait_for_function("width => Math.abs(document.querySelector('.km-admin-panel-nav').getBoundingClientRect().width - width) < 1", arg=width if width < 768 else 240)
                                    link = nav.locator(f'a[href="/admin/{name}"]')
                                    # Collapsed motion panels keep links in the DOM; visibility
                                    # alone does not tell us whether their parent clips them.
                                    expanded = link.evaluate("element => element.closest('a').parentElement.parentElement.getBoundingClientRect().height > 10")
                                    if not expanded:
                                        nav.get_by_text(strings["operations"]["title"], exact=True).click()
                                    expect(link).to_be_visible()
                                    assert link.get_attribute("target") is None
                                    link.click()

                                assert_theme(appearance)
                                visited = []
                                for name, key, test_id in PAGES:
                                    open_tool(name)
                                    expect(page).to_have_url(origin + f"/admin/{name}")
                                    content = page.locator(".km-admin-panel-content")
                                    expect(content.get_by_test_id(test_id)).to_be_visible()
                                    expect(content.get_by_role("heading", name=strings[key]["title"], exact=True)).to_be_visible()
                                    settled_content()
                                    assert_theme(appearance)
                                    assert page.evaluate("window.__navigationMarker === 'same-document' && window.__shellNode === document.querySelector('.km-admin-panel-bar')")
                                    assert documents == ["/admin/account"] and len(context.pages) == 1
                                    expect(page.locator("iframe")).to_have_count(0)
                                    if name == "sla":
                                        expect(page.get_by_test_id("sla-report")).to_be_visible(timeout=15000)
                                    elif name == "bulk":
                                        expect(page.get_by_test_id("bulk-node-count")).to_contain_text("1 nodes")
                                    elif name == "maintenance":
                                        expect(page.get_by_test_id("maintenance-empty")).to_be_visible()
                                    elif name == "forecast":
                                        expect(page.get_by_test_id("forecast-loading")).to_have_count(0)
                                    visited.append(name)

                                if code == "zh_CN":
                                    page.screenshot(path=str(output.with_name(f"{output.stem}-{code}-{width}.png")))

                                # Browser Back/Forward returns within the same document and shell.
                                page.go_back()
                                expect(page).to_have_url(origin + "/admin/config")
                                expect(page.get_by_test_id("config-page")).to_be_visible()
                                page.go_forward()
                                expect(page).to_have_url(origin + "/admin/forecast")
                                expect(page.get_by_test_id("forecast-page")).to_be_visible()
                                assert_theme(appearance)

                                # The existing appearance control updates the embedded page immediately.
                                next_appearance = "light" if appearance == "dark" else "dark"
                                page.locator(".km-admin-panel-topbar").get_by_role("button", name=strings["common"]["appearance"], exact=True).click()
                                page.get_by_role("menuitem", name=strings["theme"][next_appearance], exact=True).click()
                                assert_theme(next_appearance)
                                open_tool("sla")
                                assert_theme(next_appearance)
                                page.get_by_role("radio", name=strings["status"]["window_7d"], exact=True).click()
                                expect(page).to_have_url(origin + "/admin/sla?window=7d")
                                expect(page.get_by_test_id("sla-report")).to_have_attribute("data-window", "7d")
                                assert page.evaluate("typeof history.state.idx === 'number'")
                                page.go_back()
                                expect(page).to_have_url(origin + "/admin/forecast")
                                page.go_forward()
                                expect(page).to_have_url(origin + "/admin/sla?window=7d")
                                expect(page.get_by_test_id("sla-report")).to_have_attribute("data-window", "7d")
                                assert page.evaluate("window.__navigationMarker === 'same-document'")
                                assert len(context.pages) == 1 and documents == ["/admin/account"]

                                # The node-list bulk action uses the same route and retains its selection.
                                page.goto(origin + "/admin/servers")
                                table_row = page.get_by_test_id("node-row")
                                expect(table_row).to_have_count(1)
                                table_row.get_by_role("checkbox").check()
                                entry = page.get_by_test_id("bulk-entry-link")
                                expect(entry).to_have_attribute("href", "/admin/bulk?uuids=" + node["uuid"])
                                assert entry.get_attribute("target") is None
                                page.evaluate("window.__bulkNavigation = true")
                                entry.click()
                                expect(page.get_by_test_id("bulk-selection-count")).to_have_attribute("data-count", "1")
                                assert page.evaluate("window.__bulkNavigation === true")
                                assert len(context.pages) == 1

                                # Deep links and reload work without having clicked a menu first.
                                for name, key, test_id in PAGES:
                                    path = f"/admin/{name}?window=30d&keep=1#report" if name == "sla" else f"/admin/{name}"
                                    page.goto(origin + path)
                                    expect(page.locator(".km-admin-panel-content").get_by_test_id(test_id)).to_be_visible()
                                    if name == "sla":
                                        expect(page.get_by_test_id("sla-report")).to_have_attribute("data-window", "30d")
                                        page.get_by_role("radio", name=strings["status"]["window_24h"], exact=True).click()
                                        expect(page).to_have_url(origin + "/admin/sla?window=24h&keep=1#report")
                                        page.reload()
                                        expect(page.get_by_test_id("sla-report")).to_have_attribute("data-window", "24h")
                                    # Init script deliberately resets the stored preference on reload.
                                    assert_theme(appearance)
                                assert not errors, errors
                                report["scenarios"].append(dict(locale=code, width=width, stored_appearance=appearance,
                                    opposing_system_appearance=system, menu_pages=visited,
                                    same_document_and_shell=True, new_tabs=0, theme_switch_live=True,
                                    browser_history=True, SLA_query_reload=True, direct_routes=5,
                                    selected_node_bulk_action_same_document=True))
                            finally:
                                context.close()

                    context = browser.new_context(service_workers="block")
                    try:
                        response = context.request.post(origin + "/api/login", data=dict(username=username, password=password))
                        assert response.ok
                        page = context.new_page()
                        errors = []
                        page.on("pageerror", lambda error: errors.append(str(error)))
                        for name, _, test_id in PAGES:
                            page.goto(origin + f"/standalone/{name}/{name}.html")
                            expect(page.get_by_test_id(test_id)).to_be_visible(timeout=15000)
                        assert not errors, errors
                        report["checks"].append("All five original standalone links still mount their real pages")
                    finally:
                        context.close()

                    context = browser.new_context(service_workers="block")
                    try:
                        page = context.new_page()
                        for name, _, _ in PAGES:
                            page.goto(origin + f"/admin/{name}")
                            expect(page.get_by_role("dialog")).to_be_visible(timeout=15000)
                        response = context.request.get(origin + "/api/me")
                        assert response.json()["logged_in"] is False
                        report["checks"].append("Anonymous direct admin links retain existing login dialog; no authentication bypass")
                    finally:
                        context.close()
                finally:
                    browser.close()
        report["status"] = "passed"
    except BaseException as error:
        report["status"] = "failed"
        report["error_class"] = type(error).__name__
        report["failed_owned_directory"] = str(scratch)
        raise
    finally:
        stop(proxy)
        stop(server)
        if report["status"] == "passed":
            shutil.rmtree(scratch)
            report["successful_owned_runtime_removed"] = True
        with output.open("x") as stream:
            json.dump(report, stream, indent=2)
            stream.write("\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", required=True, type=Path)
    parser.add_argument("--dist", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    run(args.panel_binary.resolve(), args.dist.resolve(), args.output.resolve())
