#!/usr/bin/env python3
"""Exercise built-in execution history against a disposable real panel."""
import argparse
import base64
from contextlib import closing
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import time
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright


def check_paging(page, history, locale, review_dir):
    def button(label, translated):
        return history.get_by_role("button", name=label if locale == "en-US" else translated, exact=True)

    def retry_page(method, advance):
        cursors = []

        def interrupt(route):
            request = route.request.post_data_json
            if isinstance(request, dict) and request.get("method") == method:
                cursors.append(request["params"]["cursor"])
                if len(cursors) == 1 and cursors[0]:
                    route.fulfill(status=503, body="unavailable")
                    return
            route.fallback()

        page.route("**/api/rpc2", interrupt)
        try:
            advance.click()
            expect(history.get_by_role("alert")).to_be_visible()
            with page.expect_response(lambda response: response.url.endswith("/api/rpc2") and
                                      isinstance(response.request.post_data_json, dict) and
                                      response.request.post_data_json.get("method") == method) as retried:
                button("Retry", "重试").click()
            assert retried.value.ok
            expect(history.get_by_role("alert")).to_have_count(0)
        finally:
            page.unroute("**/api/rpc2", interrupt)
        assert len(cursors) == 2 and cursors[0] and cursors[0] == cursors[1], f"{method}: retry did not repeat the failed cursor"

    retry_page("admin:listExecHistory", button("Older tasks", "较早的任务"))
    oldest = history.locator(".rt-Card").filter(has_text="synthetic-page-01").last
    expect(oldest).to_be_visible()
    expect(history.get_by_text("synthetic-history", exact=True)).to_have_count(0)
    oldest.get_by_role("button", name="View details" if locale == "en-US" else "查看详情", exact=True).click()
    first = history.get_by_text("review-node-01", exact=True)
    last = history.get_by_text("review-node-21", exact=True)
    expect(first).to_be_visible()
    retry_page("admin:getExecHistoryDetail", button("Next page", "下一页"))
    expect(last).to_be_visible()
    expect(first).to_have_count(0)
    if review_dir:
        last.scroll_into_view_if_needed()
        page.screenshot(path=review_dir / f"{locale}-result-page-2.png")
    button("Previous page", "上一页").click()
    expect(first).to_be_visible()
    button("Newer tasks", "较新的任务").click()
    expect(history.get_by_text("synthetic-history", exact=True)).to_be_visible()
    expect(last).to_have_count(0)


def run(binary, review_dir):
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    opener = build_opener(ProxyHandler({}))
    process = None

    def call(path, payload=None):
        request = Request(origin + path, None if payload is None else json.dumps(payload).encode(),
                          {"Content-Type": "application/json"})
        with opener.open(request, timeout=2) as response:
            return json.load(response)

    def wait(path):
        for _ in range(300):
            if process.poll() is not None:
                raise RuntimeError("synthetic panel exited during startup")
            try:
                if call(path).get("status") == "success":
                    return
            except (OSError, URLError, ValueError):
                pass
            time.sleep(.1)
        raise RuntimeError("synthetic panel startup timed out")

    with tempfile.TemporaryDirectory(prefix="nekomari-exec-history-panel-") as tmp:
        root = Path(tmp)
        env = {**os.environ, "HOME": tmp, "TZ": "UTC", "HTTP_PROXY": "http://127.0.0.1:9",
               "HTTPS_PROXY": "http://127.0.0.1:9", "ALL_PROXY": "http://127.0.0.1:9",
               "NO_PROXY": "127.0.0.1,localhost"}
        env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
        with (root / "panel.log").open("wb") as log:
            process = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                       cwd=root, env=env, stdout=log, stderr=subprocess.STDOUT)
            try:
                wait("/api/install/status")
                username, password = "history-test", "Test-fixture1-" + secrets.token_urlsafe(12)
                assert call("/api/install/complete", {"username": username, "password": password,
                    "sitename": "test", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait("/api/version")
                with closing(sqlite3.connect(root / "data/komari.db")) as db, db:
                    db.execute("INSERT INTO tasks(task_id,clients,command,history_order) VALUES(?,?,?,?)",
                               ("synthetic-history", '["node"]', "<script>synthetic-command</script>", 21))
                    db.execute("""INSERT INTO task_results(task_id,client,result,exit_code,finished_at,created_at)
                                  VALUES(?,?,?,?,?,?)""", ("synthetic-history", "node",
                        "<img src=x onerror=alert(1)>synthetic-output", 0,
                        "2026-11-05 02:00:01+00:00", "2026-11-05 02:00:00+00:00"))
                    db.execute("INSERT INTO tasks(task_id,clients,command,history_order) VALUES(?,?,CAST(? || zeroblob(?) AS TEXT),?)",
                               ("synthetic-oversized", '["node"]', "large-command-marker", (16 << 20) + 1, 22))
                    db.execute("""INSERT INTO task_results(task_id,client,result,created_at)
                                  VALUES(?,?,CAST(? || zeroblob(?) AS TEXT),?)""",
                               ("synthetic-oversized", "node", "large-output-marker", (16 << 20) + 1, "2026-11-05 02:01:00+00:00"))
                    clients = [f"review-node-{index:02d}" for index in range(1, 22)]
                    db.executemany("INSERT INTO tasks(task_id,clients,command,history_order) VALUES(?,?,?,?)",
                                   [(f"synthetic-page-{index:02d}", json.dumps(clients if index == 1 else []), "fixture", index)
                                    for index in range(1, 21)])
                    db.executemany("INSERT INTO task_results(task_id,client,result,created_at) VALUES(?,?,?,?)",
                                   [("synthetic-page-01", client, "fixture", "2026-11-05 02:00:00+00:00") for client in clients])
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for locale, width, appearance in [("en-US", 1280, "light"), ("zh-CN", 390, "dark")]:
                            context = browser.new_context(locale="en-US", timezone_id="America/New_York",
                                                          viewport={"width": width, "height": 900},
                                                          color_scheme=appearance, service_workers="block")
                            try:
                                assert context.request.post(origin + "/api/login", data={"username": username,
                                    "password": password}).json()["status"] == "success"
                                assert context.request.post(origin + "/api/admin/settings/", data={
                                    "eula_accepted": True, "geo_ip_enabled": False,
                                    "login_notification": False, "notification_enabled": False,
                                }).json()["status"] == "success"
                                context.add_init_script("localStorage.setItem('language', %s); localStorage.setItem('appearance', %s);" %
                                                        (json.dumps(locale), json.dumps(appearance)))
                                page = context.new_page()
                                errors, calls = [], []
                                page.on("pageerror", lambda error: errors.append(str(error)))

                                def record(request):
                                    if request.url.endswith("/api/rpc2") and request.method == "POST":
                                        try:
                                            calls.append(json.loads(request.post_data)["method"])
                                        except (ValueError, TypeError, KeyError):
                                            pass

                                page.on("request", record)
                                page.route("**/*", lambda route: route.continue_()
                                           if route.request.url.startswith(origin + "/") else route.abort())
                                page.goto(origin + "/admin/exec", wait_until="domcontentloaded")
                                history = page.get_by_test_id("exec-history")
                                history.get_by_role("button", name="View history" if locale == "en-US" else "查看历史",
                                                    exact=True).click()
                                expect(history).to_contain_text("synthetic-history")
                                task = history.locator(".rt-Card").filter(has_text="synthetic-history").last
                                created, finished = page.evaluate('''language => {
                                    const clock = new Intl.DateTimeFormat(language, {year: 'numeric', month: '2-digit',
                                        day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
                                        timeZoneName: 'short', timeZone: 'America/New_York'});
                                    return ['2026-11-05T02:00:00Z', '2026-11-05T02:00:01Z'].map(iso => clock.format(new Date(iso)));
                                }''', locale)
                                expect(task).to_contain_text(created)
                                if review_dir:
                                    history.get_by_role("button", name="Close history" if locale == "en-US" else "关闭历史").scroll_into_view_if_needed()
                                    page.screenshot(path=review_dir / f"{locale}-task-list.png")
                                task.get_by_role("button", name="View details" if locale == "en-US" else "查看详情",
                                                 exact=True).click()
                                expect(task).to_contain_text("node")
                                expect(task).to_contain_text(finished)
                                task.get_by_role("button", name="Show command" if locale == "en-US" else "显示命令",
                                                 exact=True).click()
                                expect(task.locator("pre").first).to_contain_text("<script>synthetic-command</script>")
                                task.get_by_role("button", name="Show output" if locale == "en-US" else "显示输出",
                                                 exact=True).click()
                                expect(task.locator("pre").nth(1)).to_contain_text("<img src=x onerror=alert(1)>synthetic-output")
                                if review_dir:
                                    task.locator("pre").first.scroll_into_view_if_needed()
                                    page.screenshot(path=review_dir / f"{locale}-detail.png")
                                assert history.locator("img").count() == 0
                                assert "admin:exec" not in calls, calls
                                assert calls.count("admin:readExecHistoryChunk") == 2, calls
                                large = history.locator(".rt-Card").filter(has_text="synthetic-oversized").last
                                large.get_by_role("button", name="View details" if locale == "en-US" else "查看详情",
                                                  exact=True).click()
                                large.get_by_role("button", name="Show command" if locale == "en-US" else "显示命令",
                                                  exact=True).click()
                                expect(large.locator("pre").first).to_contain_text("large-command-marker")
                                large.get_by_role("button", name="Show output" if locale == "en-US" else "显示输出",
                                                  exact=True).click()
                                expect(large.locator("pre").nth(1)).to_contain_text("large-output-marker")
                                assert calls.count("admin:readExecHistoryChunk") == 4, calls
                                detail = context.request.post(origin + "/api/rpc2", data={"jsonrpc": "2.0", "id": 1,
                                    "method": "admin:getExecHistoryDetail", "params": {"task_id": "synthetic-oversized"}}).json()["result"]
                                for kind, cursor in [("command", ""), ("output", detail["results"][0]["cursor"])]:
                                    tail = context.request.post(origin + "/api/rpc2", data={"jsonrpc": "2.0", "id": 1,
                                        "method": "admin:readExecHistoryChunk", "params": {"task_id": "synthetic-oversized",
                                            "kind": kind, "result_cursor": cursor, "offset": (16 << 20)}}).json()["result"]
                                    data = base64.b64decode(tail["data_base64"])
                                    assert tail["available"] and len(data) <= 16384 and not tail["has_more"]
                                    assert tail["next_offset"] == tail["total_bytes"] and data.endswith(b"\0")
                                check_paging(page, history, locale, review_dir)
                                assert calls.count("admin:readExecHistoryChunk") == 4 and "admin:exec" not in calls, calls
                                assert not errors, errors
                                if width == 390:
                                    assert page.evaluate("document.documentElement.scrollWidth") <= width
                            finally:
                                context.close()
                    finally:
                        browser.close()
                print("PASS: embedded admin UI and backend, English desktop/light and Chinese mobile/dark; no exec RPC")
            finally:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--panel-binary", type=Path, required=True)
    parser.add_argument("--review-dir", type=Path, help="write synthetic desktop/mobile screenshots for owner review")
    args = parser.parse_args()
    if args.review_dir:
        args.review_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    run(args.panel_binary.resolve(strict=True), args.review_dir)
