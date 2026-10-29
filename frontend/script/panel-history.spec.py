"""Check private panel history and the embedded UI in a disposable instance."""

import argparse
import hashlib
from http.cookiejar import CookieJar
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[1]
METRICS = ["panel.self.rss_bytes", "panel.self.heap_alloc_bytes", "panel.self.goroutines"]
POOL_METRICS = ["panel.pool.main.wait_count", "panel.pool.main.wait_ms",
                "panel.pool.metrics_write.wait_count", "panel.pool.metrics_write.wait_ms",
                "panel.pool.metrics_read.wait_count", "panel.pool.metrics_read.wait_ms"]
POOL_FIELDS = ["main_wait_count", "main_wait_ms", "write_wait_count", "write_wait_ms",
               "read_wait_count", "read_wait_ms"]
THEME_METRICS = ["panel.theme_cache.disk_state", "panel.theme_cache.memory_loaded"]
THEME_FIELDS = ["theme_cache_disk", "theme_cache_memory_loaded"]


def run(binary, output):
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    admin = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
    guest = build_opener(ProxyHandler({}))
    process = None
    scenarios = []

    def call(path, body=None, client=admin, token=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        req = Request(origin + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
        with client.open(req, timeout=15) as response:
            return response.status, response.headers, json.load(response)

    def rpc(method, params, client=admin, token=None):
        return call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, client, token)[2]

    def denied(path, client, token=None, expected=401):
        try:
            call(path, client=client, token=token)
        except HTTPError as exc:
            assert exc.code == expected, (path, exc.code, expected)
        else:
            raise AssertionError("unprivileged history request succeeded")

    def wait(path):
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            assert process.poll() is None, "synthetic Panel exited"
            try:
                if call(path)[2]["status"] == "success":
                    return
            except (URLError, TimeoutError):
                time.sleep(0.1)
        raise TimeoutError("synthetic Panel did not start")

    def stop():
        if process and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)

    with tempfile.TemporaryDirectory(prefix="nekomari-panel-history-") as temp:
        scratch = Path(temp)
        try:
            with (scratch / "panel.log").open("wb") as log:
                def start():
                    nonlocal process
                    env = {**os.environ, "HOME": str(scratch), "TZ": "UTC"}
                    env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
                    process = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                               cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)

                start()
                wait("/api/install/status")
                username, password = "history-fixture", "History-Fixture1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": username, "password": password,
                    "sitename": "Panel history fixture", "metric_dsn": "./data/metrics.db"})[2]["status"] == "success"
                wait("/api/version")
                version = call("/api/version")[2]["data"]
                assert call("/api/login", {"username": username, "password": password})[2]["status"] == "success"
                assert call("/api/admin/settings/", {"eula_accepted": True, "private_site": False,
                    "geo_ip_enabled": False, "login_notification": False, "notification_enabled": False})[2]["status"] == "success"
                keys = [call("/api/admin/api-keys", {"name": "history " + scope, "scopes": [scope],
                    "expires_days": 1})[2]["data"]["token"] for scope in ("nodes:read", "diagnostics:read")]
                path = "/api/admin/pprof/history?hours=24"
                denied(path, guest)
                for key in keys:
                    denied(path, guest, key, 403)
                    denied(path, admin, key, 403)
                for client, key in [(guest, None), (guest, keys[0]), (guest, keys[1])]:
                    definitions = rpc("public:listMetricDefinitions", {}, client, key)
                    assert "result" in definitions and all(m not in json.dumps(definitions) for m in METRICS + POOL_METRICS + THEME_METRICS)
                    for name in METRICS + POOL_METRICS + THEME_METRICS:
                        assert "error" in rpc("public:queryMetrics", {"metric_key": name, "hours": 1}, client, key)
                status, headers, data = call(path)
                assert status == 200 and "no-store" in headers.get("Cache-Control", "")
                history = data["data"]
                assert all(history["retention_days"][name] == 1 for name in METRICS)
                assert all(history["retention_days"][name] == 1 for name in POOL_METRICS)
                assert all(history["retention_days"][name] == 1 for name in THEME_METRICS)
                assert all(field in point and (point[field] is None or point[field] >= 0)
                           for point in history["points"] for field in POOL_FIELDS)
                assert all(point["theme_cache_disk"] in (None, "disabled", "uninitialized", "initializing", "ready", "failed", "degraded")
                           and (point["theme_cache_memory_loaded"] is None or isinstance(point["theme_cache_memory_loaded"], bool))
                           for point in history["points"])
                assert any(point["theme_cache_disk"] is not None and point["theme_cache_memory_loaded"] is not None
                           for point in history["points"])
                before = {point["time"]: point["heap_bytes"] for point in history["points"] if point["heap_bytes"] is not None}
                assert before
                stop()
                start()
                wait("/api/version")
                assert call("/api/login", {"username": username, "password": password})[2]["status"] == "success"
                # If startup resamples inside the same bucket, that bucket's
                # last value can legitimately change. Only verify durability
                # by comparing a closed prior bucket after the clock advances.
                deadline = time.monotonic() + 75
                while True:
                    restored = call("/api/admin/pprof/history?hours=1")[2]["data"]
                    closed = [point for point in restored["points"][:-1] if point["heap_bytes"] is not None]
                    if closed:
                        break
                    assert time.monotonic() < deadline, "sample did not survive a minute boundary"
                    time.sleep(2)
                stop()
                start()
                wait("/api/version")
                assert call("/api/login", {"username": username, "password": password})[2]["status"] == "success"
                after = call("/api/admin/pprof/history?hours=1")[2]["data"]["points"]
                by_time = {point["time"]: point["heap_bytes"] for point in after}
                assert all(by_time.get(point["time"]) == point["heap_bytes"] for point in closed)

                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for locale, code, width, appearance in [("zh-CN", "zh_CN", 1280, "light"), ("en-US", "en", 390, "dark")]:
                            strings = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())["pprof"]
                            context = browser.new_context(locale=locale, viewport={"width": width, "height": 900},
                                color_scheme=appearance, service_workers="block")
                            try:
                                assert context.request.post(origin + "/api/login", data={"username": username, "password": password}).json()["status"] == "success"
                                context.add_init_script("localStorage.setItem('language', %s); localStorage.setItem('appearance', %s);" %
                                                        (json.dumps(locale), json.dumps(appearance)))
                                page = context.new_page()
                                errors = []
                                page.on("pageerror", lambda error: errors.append(str(error)))
                                page.route("**/*", lambda route: route.continue_() if route.request.url.startswith(origin + "/") else route.abort())
                                page.goto(origin + "/admin/pprof")
                                expect(page.get_by_text(strings["history_title"], exact=True)).to_be_visible()
                                expect(page.get_by_test_id("panel-history")).to_contain_text(strings["history_note"])
                                expect(page.get_by_test_id("panel-history-latest")).not_to_have_text("-")
                                # Intercept only a private test response so zero, missing and old-server
                                # compatibility are deterministic without generating DB contention.
                                for scenario in ("values", "older", "disabled"):
                                    fixture = json.loads(json.dumps(data))
                                    for point in fixture["data"]["points"]:
                                        for field in POOL_FIELDS:
                                            if scenario == "older":
                                                point.pop(field, None)
                                            else:
                                                point[field] = None
                                    if scenario == "values":
                                        fixture["data"]["points"][-1]["write_wait_count"] = 0
                                    if scenario == "disabled":
                                        fixture["data"]["retention_days"][POOL_METRICS[2]] = 0
                                    def respond_fixture(route):
                                        route.fulfill(status=200, json=fixture)
                                    page.route("**/api/admin/pprof/history?*", respond_fixture)
                                    page.get_by_test_id("panel-history").get_by_role("button", name="Refresh" if code == "en" else "刷新").click()
                                    page.get_by_label(strings["history_metric"]).click()
                                    page.get_by_role("option", name=strings["pool_write_wait_count"]).click()
                                    expect(page.get_by_test_id("panel-pool-note")).to_contain_text(strings["pool_history_note"])
                                    if scenario == "values":
                                        expect(page.get_by_test_id("panel-history-latest")).to_have_text("0")
                                    else:
                                        expect(page.get_by_test_id("panel-history-latest")).to_have_text("-")
                                        expect(page.get_by_test_id("panel-history-empty")).to_contain_text(
                                            strings["history_disabled"] if scenario == "disabled" else strings["history_empty"])
                                    page.unroute("**/api/admin/pprof/history?*")
                                for scenario in ("states", "gaps", "older", "future", "disabled"):
                                    fixture = json.loads(json.dumps(data))
                                    for point in fixture["data"]["points"]:
                                        for field in THEME_FIELDS:
                                            if scenario == "older":
                                                point.pop(field, None)
                                            else:
                                                point[field] = None
                                    if scenario == "states":
                                        fixture["data"]["points"][-1]["theme_cache_disk"] = "degraded"
                                        fixture["data"]["points"][-1]["theme_cache_memory_loaded"] = False
                                    if scenario == "gaps":
                                        for point in fixture["data"]["points"][-69:-5:2]:
                                            point["theme_cache_disk"] = "ready"
                                    if scenario == "future":
                                        fixture["data"]["points"][-1]["theme_cache_disk"] = "future-state"
                                    if scenario == "disabled":
                                        fixture["data"]["retention_days"][THEME_METRICS[0]] = 0
                                    page.route("**/api/admin/pprof/history?*", lambda route: route.fulfill(status=200, json=fixture))
                                    page.get_by_test_id("panel-history").get_by_role("button", name="Refresh" if code == "en" else "刷新").click()
                                    page.get_by_label(strings["history_metric"]).click()
                                    page.get_by_role("option", name=strings["theme_cache_disk"], exact=True).click()
                                    expect(page.get_by_test_id("panel-theme-note")).to_contain_text(strings["theme_history_note"])
                                    if scenario == "states":
                                        expect(page.get_by_test_id("panel-history-latest")).to_have_text(strings["theme_cache_disk_degraded"])
                                        page.get_by_label(strings["history_metric"]).click()
                                        page.get_by_role("option", name=strings["theme_cache_memory"], exact=True).click()
                                        expect(page.get_by_test_id("panel-history-latest")).to_have_text(strings["theme_cache_memory_not_loaded"])
                                    elif scenario == "gaps":
                                        chart = page.get_by_test_id("panel-history-chart")
                                        expect(chart).to_be_visible()
                                        assert chart.locator(".recharts-dot").count() == 32
                                        assert chart.locator(".recharts-line-curve").get_attribute("stroke-opacity") == "0"
                                        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
                                    else:
                                        expect(page.get_by_test_id("panel-history-latest")).to_have_text("-")
                                        expect(page.get_by_test_id("panel-history-empty")).to_contain_text(
                                            strings["history_disabled"] if scenario == "disabled" else strings["history_empty"])
                                    page.unroute("**/api/admin/pprof/history?*")
                                assert page.url.endswith("/admin/pprof") and not errors, errors
                                scenarios.append({"locale": code, "width": width, "appearance": appearance})
                            finally:
                                context.close()
                    finally:
                        browser.close()

                answer = rpc("admin:updateMetricDefinition", {"name": METRICS[1], "retention_days": 0})
                assert "result" in answer, answer
                time.sleep(0.2)
                disabled = call(path)[2]["data"]
                assert disabled["retention_days"][METRICS[1]] == 0
                assert all(point["heap_bytes"] is None for point in disabled["points"])
        finally:
            stop()
    result = {"status": "passed", "version": version, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "scenarios": scenarios, "guest_and_keys_denied": True, "restart_and_disabled_retention": True,
              "synthetic_only": True, "scratch_removed": True}
    if output:
        output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    run(args.panel_binary.resolve(strict=True), args.output)
