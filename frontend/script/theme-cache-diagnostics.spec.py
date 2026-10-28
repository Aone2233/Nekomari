"""Verify the diagnostic UI and authorization using an isolated embedded Panel."""

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


def run(binary, output):
    os.umask(0o077)
    scenarios = []
    versions = []
    for disabled in (False, True):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        origin = f"http://127.0.0.1:{port}"
        admin = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
        guest = build_opener(ProxyHandler({}))
        with tempfile.TemporaryDirectory(prefix="nekomari-theme-diagnostics-") as temp:
            scratch = Path(temp)
            env = {**os.environ, "HOME": str(scratch)}
            env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
            if disabled:
                env["KOMARI_THEME_CACHE"] = "0"
            else:
                env.pop("KOMARI_THEME_CACHE", None)
            with (scratch / "panel.log").open("wb") as log:
                process = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                           cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)

                def call(path, body=None, client=admin, token=None):
                    headers = {"Content-Type": "application/json"}
                    if token:
                        headers["Authorization"] = "Bearer " + token
                    req = Request(origin + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
                    with client.open(req, timeout=15) as response:
                        return response.status, response.headers, json.load(response)

                def denied(path, client, token=None, expected=401):
                    try:
                        call(path, client=client, token=token)
                    except HTTPError as exc:
                        assert exc.code == expected, (path, exc.code, expected)
                    else:
                        raise AssertionError("unprivileged caller reached diagnostic endpoint")

                def ready(path):
                    deadline = time.monotonic() + 40
                    while time.monotonic() < deadline:
                        assert process.poll() is None, "synthetic Panel exited"
                        try:
                            if call(path)[2]["status"] == "success":
                                return
                        except (URLError, TimeoutError):
                            time.sleep(0.1)
                    raise TimeoutError("synthetic Panel readiness timed out")

                try:
                    ready("/api/install/status")
                    user, password = "cache-test", "Cache-Fixture1-" + secrets.token_urlsafe(24)
                    assert call("/api/install/complete", {"username": user, "password": password,
                        "sitename": "Cache diagnostics fixture", "metric_dsn": "./data/metrics.db"})[2]["status"] == "success"
                    ready("/api/version")
                    versions.append(call("/api/version")[2]["data"])
                    assert call("/api/login", {"username": user, "password": password})[2]["status"] == "success"
                    assert call("/api/admin/settings/", {"eula_accepted": True, "private_site": True,
                        "geo_ip_enabled": False, "login_notification": False, "notification_enabled": False})[2]["status"] == "success"
                    key = call("/api/admin/api-keys", {"name": "diagnostics-test", "scopes": ["diagnostics:read"],
                        "expires_days": 1})[2]["data"]["token"]
                    path = "/api/admin/pprof/summary"
                    denied(path, guest)
                    denied(path, guest, key, 403)
                    denied(path, admin, key, 403)  # An explicit key must not inherit an admin cookie.
                    status, headers, payload = call(path)
                    assert status == 200 and "no-store" in headers.get("Cache-Control", "")
                    state = payload["data"]["runtime"]["theme_cache"]
                    assert set(state) == {"disk", "memory_loaded"}
                    with sync_playwright() as pw:
                        browser = pw.chromium.launch(headless=True)
                        try:
                            for locale, code, width, appearance in [("zh-CN", "zh_CN", 1280, "light"),
                                                                    ("en-US", "en", 390, "dark")]:
                                strings = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())["pprof"]
                                context = browser.new_context(locale=locale, color_scheme=appearance,
                                    viewport={"width": width, "height": 900}, service_workers="block")
                                try:
                                    assert context.request.post(origin + "/api/login", data={"username": user, "password": password}).json()["status"] == "success"
                                    context.add_init_script("localStorage.setItem('language', %s); localStorage.setItem('appearance', %s);" %
                                                            (json.dumps(locale), json.dumps(appearance)))
                                    page = context.new_page()
                                    errors = []
                                    page.on("pageerror", lambda error: errors.append(str(error)))
                                    page.route("**/*", lambda route: route.continue_() if route.request.url.startswith(origin + "/") else route.abort())
                                    page.goto(origin + "/admin/pprof")
                                    expected_disk = "disabled" if disabled else "ready"
                                    expected_memory = "loaded" if disabled else "not_loaded"
                                    disk = page.get_by_test_id("pprof-theme-cache-disk")
                                    memory = page.get_by_test_id("pprof-theme-cache-memory")
                                    expect(disk).to_contain_text(strings["theme_cache_disk_" + expected_disk])
                                    expect(memory).to_contain_text(strings["theme_cache_memory_" + expected_memory])
                                    expect(page.get_by_text(strings["theme_cache_note"])).to_be_visible()
                                    assert page.url.endswith("/admin/pprof")
                                    assert not errors, errors
                                    scenarios.append({"disabled": disabled, "locale": code, "width": width,
                                                      "appearance": appearance, "disk": expected_disk, "memory": expected_memory})
                                finally:
                                    context.close()
                        finally:
                            browser.close()
                    state = call(path)[2]["data"]["runtime"]["theme_cache"]
                    assert state == {"disk": "disabled" if disabled else "ready", "memory_loaded": disabled}
                finally:
                    if process.poll() is None:
                        process.terminate()
                        try:
                            process.wait(timeout=20)
                        except subprocess.TimeoutExpired:
                            process.kill()
                            process.wait(timeout=5)
    assert versions[0] == versions[1]
    report = {"status": "passed", "version": versions[0], "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "scenarios": scenarios, "guest_and_api_keys_denied": True,
              "synthetic_only": True, "scratch_removed": True}
    if output:
        output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    run(args.panel_binary.resolve(strict=True), args.output)
