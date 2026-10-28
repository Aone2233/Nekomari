"""Exercise uptime ingestion, visibility, restart and embedded admin in a disposable Panel."""

import argparse
import datetime as dt
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
from urllib.error import URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright


METRIC = "system.uptime_seconds"
ROOT = Path(__file__).resolve().parents[1]


def run(binary, output, previous_binary=None):
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    admin = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
    guest = build_opener(ProxyHandler({}))
    process = None
    checks = []
    current_binary = binary
    previous_version = None

    def call(path, body=None, client=admin, token=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        request = Request(origin + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
        with client.open(request, timeout=15) as response:
            return json.load(response)

    def rpc(method, params, client=admin, token=None):
        answer = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, client, token)
        assert "error" not in answer, f"synthetic RPC {method} failed: {answer.get('error')}"
        return answer["result"]

    def wait(path):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            assert process.poll() is None, "synthetic Panel exited"
            try:
                if call(path)["status"] == "success":
                    return
            except (URLError, TimeoutError):
                pass
            time.sleep(0.1)
        raise RuntimeError("synthetic Panel readiness timed out")

    def stop():
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)

    with tempfile.TemporaryDirectory(prefix="nekomari-uptime-history-") as temp:
        scratch = Path(temp)
        try:
            with (scratch / "panel.log").open("wb") as log:
                def start():
                    nonlocal process
                    env = {**os.environ, "HOME": str(scratch), "TZ": "UTC"}
                    env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
                    process = subprocess.Popen([str(current_binary), "server", "--listen", f"127.0.0.1:{port}"],
                                               cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)

                start()
                wait("/api/install/status")
                username, password = "uptime-fixture", "Uptime-Fixture1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": username, "password": password,
                    "sitename": "Synthetic uptime history", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait("/api/version")
                assert call("/api/login", {"username": username, "password": password})["status"] == "success"
                assert call("/api/admin/settings/", {"eula_accepted": True, "private_site": False,
                    "geo_ip_enabled": False, "login_notification": False, "notification_enabled": False})["status"] == "success"
                visible = call("/api/admin/client/add", {"name": "Uptime visible fixture"})
                hidden = call("/api/admin/client/add", {"name": "Uptime hidden fixture"})
                empty = call("/api/admin/client/add", {"name": "Uptime empty fixture"})
                assert call(f"/api/admin/client/{hidden['uuid']}/edit", {"hidden": True})["status"] == "success"
                keys = {}
                for scope in ("nodes:read", "tasks:exec"):
                    keys[scope] = call("/api/admin/api-keys", {"name": "uptime " + scope,
                        "scopes": [scope], "expires_days": 1})["data"]["token"]

                base = dt.datetime.now(dt.timezone.utc).replace(second=0, microsecond=0) - dt.timedelta(minutes=2)
                def report(node, offset, uptime=None, quality=None):
                    payload = {"sampled_at": (base + dt.timedelta(seconds=offset)).isoformat(),
                               "cpu": {}, "ram": {}, "swap": {}, "load": {}, "disk": {},
                               "network": {}, "connections": {}}
                    if uptime is not None:
                        payload["uptime"] = uptime
                    if quality is not None:
                        payload["quality"] = {"uptime": quality}
                    answer = call("/api/clients/v2/rpc", {"jsonrpc": "2.0", "id": 1, "method": "agent.report",
                        "params": {"report": payload}}, guest, node["token"])
                    assert "error" not in answer, "synthetic agent report failed"

                report(visible, 0, 100, "ok")
                report(visible, 3, 103, "ok")
                report(visible, 6, 999, "unknown")
                report(visible, 9)  # Old agent omitted the field: unknown, not zero.
                report(visible, 10, quality="ok")  # A claimed quality does not prove the field exists.
                report(visible, 11, 0, "ok")  # Wire zero is indistinguishable from an omission.
                report(visible, 12, 2, "ok")
                report(visible, 15, 5)  # Positive legacy reading remains usable.
                report(hidden, 0, 700, "ok")
                report(hidden, 3, 703, "ok")
                report(empty, 0, 0, "ok")
                report(empty, 3, quality="ok")
                checks.append("real HTTP agent reports: ok, unknown, absent and legacy inputs")
                stop()  # Flush partial buckets; no writes to a real node.
                start()
                wait("/api/version")
                assert call("/api/login", {"username": username, "password": password})["status"] == "success"

                params = {"metric_key": METRIC, "entity_ids": [visible["uuid"], hidden["uuid"]],
                          # Start outside raw retention to exercise the durable rollup, not exact raw samples.
                          "start": (base - dt.timedelta(minutes=11)).isoformat(),
                          "end": (base + dt.timedelta(minutes=1)).isoformat(), "max_points": 50}
                def values(client, token=None):
                    result = rpc("public:queryMetrics", params, client, token)
                    return {item["entity_id"]: item for item in result["series"]}

                for client, token, ids in [(guest, None, {visible["uuid"]}), (guest, keys["tasks:exec"], {visible["uuid"]}),
                                           (guest, keys["nodes:read"], {visible["uuid"], hidden["uuid"]}),
                                           (admin, None, {visible["uuid"], hidden["uuid"]})]:
                    series = values(client, token)
                    assert set(series) == ids
                    item = series[visible["uuid"]]
                    assert item["semantics"] == "node_uptime_seconds;not_reboot_count"
                    assert item["quality"] == "positive_ok_or_legacy;zero_missing_unknown"
                    assert item["downsample_algorithm"] == "last"
                    assert [p["value"] for p in item["points"] if p["value"] is not None] == [5]
                empty_result = rpc("public:queryMetrics", {**params, "entity_ids": [empty["uuid"]]})["series"]
                assert len(empty_result) == 1 and not any(p["value"] is not None for p in (empty_result[0]["points"] or []))
                checks.append("durable last bucket after restart; guest/scoped/hidden-node visibility")

                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for locale, code, width, appearance in [("zh-CN", "zh_CN", 1280, "light"), ("en-US", "en", 390, "dark")]:
                            strings = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())
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
                                page.goto(origin + "/admin/uptime-history")
                                node_select = page.get_by_role("combobox", name=strings["uptimeHistory"]["node"])
                                expect(node_select).to_be_visible()
                                node_select.click()
                                page.get_by_role("option", name="Uptime visible fixture").click()
                                expect(page.get_by_test_id("uptime-latest")).to_have_text("5 " + strings["nodeCard"]["time_second"])
                                expect(page.get_by_test_id("uptime-history-note")).to_have_text(strings["chart"]["uptimeHistoryNote"])
                                node_select.click()
                                page.get_by_role("option", name="Uptime hidden fixture").click()
                                expect(page.get_by_test_id("uptime-latest")).to_have_text("11 " + strings["nodeCard"]["time_minute"] + " 43 " + strings["nodeCard"]["time_second"])
                                node_select.click()
                                page.get_by_role("option", name="Uptime empty fixture").click()
                                expect(page.get_by_test_id("uptime-latest")).to_have_text("-")
                                expect(page.get_by_text(strings["uptimeHistory"]["noData"]).first).to_be_visible()
                                node_select.click()
                                page.get_by_role("option", name="Uptime visible fixture").click()
                                expect(page.get_by_test_id("uptime-latest")).to_have_text("5 " + strings["nodeCard"]["time_second"])
                                assert not errors, errors
                            finally:
                                context.close()
                    finally:
                        browser.close()
                checks.append("embedded admin history, hidden/empty node switching in desktop-light and mobile-dark with real authenticated APIs")
                version = call("/api/version")["data"]
                if previous_binary:
                    stop()
                    current_binary = previous_binary
                    start()
                    wait("/api/version")
                    previous_version = call("/api/version")["data"]
                    assert call("/api/login", {"username": username, "password": password})["status"] == "success"
                    old_series = rpc("public:queryMetrics", {**params, "aggregation": "last"})["series"]
                    old_visible = next(item for item in old_series if item["entity_id"] == visible["uuid"])
                    assert [p["value"] for p in old_visible["points"] if p["value"] is not None] == [5]
                    stop()
                    current_binary = binary
                    start()
                    wait("/api/version")
                    assert call("/api/version")["data"] == version
                    assert call("/api/login", {"username": username, "password": password})["status"] == "success"
                    assert [p["value"] for p in values(admin)[visible["uuid"]]["points"] if p["value"] is not None] == [5]
                    checks.append("synthetic current-to-previous-to-current restart preserves uptime without reclaim")
        finally:
            stop()
    result = {"status": "passed", "version": version, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "checks": checks, "production_changed": False, "scratch_removed": True}
    if previous_binary:
        result["previous_version"] = previous_version
        result["previous_binary_sha256"] = hashlib.sha256(previous_binary.read_bytes()).hexdigest()
    if output:
        output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    parser.add_argument("--previous-panel-binary", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    run(args.panel_binary.resolve(strict=True), args.output,
        args.previous_panel_binary.resolve(strict=True) if args.previous_panel_binary else None)
