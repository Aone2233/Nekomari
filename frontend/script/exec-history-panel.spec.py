#!/usr/bin/env python3
"""Exercise built-in execution history against a disposable real panel."""
import argparse
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


def run(binary):
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
                               ("synthetic-history", '["node"]', "<script>synthetic-command</script>", 1))
                    db.execute("""INSERT INTO task_results(task_id,client,result,exit_code,finished_at,created_at)
                                  VALUES(?,?,?,?,?,?)""", ("synthetic-history", "node",
                        "<img src=x onerror=alert(1)>synthetic-output", 0,
                        "2026-11-05 02:00:01+00:00", "2026-11-05 02:00:00+00:00"))
                    db.execute("INSERT INTO tasks(task_id,clients,command,history_order) VALUES(?,?,CAST(zeroblob(?) AS TEXT),?)",
                               ("synthetic-oversized", '["node"]', (8 << 20) + 1, 2))
                    db.execute("""INSERT INTO task_results(task_id,client,result,created_at)
                                  VALUES(?,?,CAST(zeroblob(?) AS TEXT),?)""",
                               ("synthetic-oversized", "node", (8 << 20) + 1, "2026-11-05 02:01:00+00:00"))
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for locale, width, appearance in [("en-US", 1280, "light"), ("zh-CN", 390, "dark")]:
                            context = browser.new_context(locale=locale, viewport={"width": width, "height": 900},
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
                                task.get_by_role("button", name="View details" if locale == "en-US" else "查看详情",
                                                 exact=True).click()
                                expect(task).to_contain_text("node")
                                task.get_by_role("button", name="Show command" if locale == "en-US" else "显示命令",
                                                 exact=True).click()
                                expect(task.locator("pre").first).to_contain_text("<script>synthetic-command</script>")
                                task.get_by_role("button", name="Show output" if locale == "en-US" else "显示输出",
                                                 exact=True).click()
                                expect(task.locator("pre").nth(1)).to_contain_text("<img src=x onerror=alert(1)>synthetic-output")
                                assert history.locator("img").count() == 0
                                assert "admin:exec" not in calls, calls
                                assert calls.count("admin:readExecHistoryChunk") == 2, calls
                                large = history.locator(".rt-Card").filter(has_text="synthetic-oversized").last
                                large.get_by_role("button", name="View details" if locale == "en-US" else "查看详情",
                                                  exact=True).click()
                                expect(large).to_contain_text("8 MiB")
                                assert large.get_by_role("button", name="Show output" if locale == "en-US" else "显示输出",
                                                         exact=True).count() == 0
                                large.get_by_role("button", name="Show command" if locale == "en-US" else "显示命令",
                                                  exact=True).click()
                                expect(large.get_by_text("8 MiB", exact=False)).to_have_count(2)
                                assert large.locator("pre").count() == 0
                                assert calls.count("admin:readExecHistoryChunk") == 3, calls
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
    run(parser.parse_args().panel_binary.resolve(strict=True))
