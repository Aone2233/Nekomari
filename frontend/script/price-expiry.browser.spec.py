"""Check nullable price expiry in a real binary's embedded admin UI, on loopback only."""

import argparse
from datetime import datetime, timedelta, timezone
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


ROOT = Path(__file__).resolve().parents[1]


def stop(process):
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def run(binary, output):
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
    report = {"status": "running", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "source": "real embedded assets and API; synthetic loopback account/nodes",
              "scenarios": [], "production_changed": False}
    process = None

    def call(path, body=None):
        request = Request(origin + path, headers={"Content-Type": "application/json"},
                          data=None if body is None else json.dumps(body).encode())
        with opener.open(request, timeout=15) as response:
            return json.load(response)

    def ready(path):
        for _ in range(200):
            assert process.poll() is None, "Synthetic Panel exited"
            try:
                if call(path)["status"] == "success":
                    return
            except (URLError, TimeoutError):
                time.sleep(0.1)
        raise TimeoutError("Synthetic Panel readiness timed out")

    with tempfile.TemporaryDirectory(prefix="nekomari-price-expiry-") as temp:
        scratch = Path(temp)
        try:
            with (scratch / "panel.log").open("wb") as log:
                env = {**os.environ, "HOME": str(scratch), "TZ": "UTC"}
                env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
                process = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                           cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)
                ready("/api/install/status")
                username, password = "price-expiry", "Price-Expiry1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": username, "password": password,
                    "sitename": "Isolated price expiry", "metric_dsn": str(scratch / "metrics.db")})["status"] == "success"
                ready("/api/version")
                assert call("/api/login", {"username": username, "password": password})["status"] == "success"
                assert call("/api/admin/settings/", {"eula_accepted": True, "private_site": True,
                    "geo_ip_enabled": False, "login_notification": False, "notification_enabled": False})["status"] == "success"
                node = call("/api/admin/client/add", {"name": "Unset expiry"})["uuid"]
                assert call(f"/api/admin/client/{node}/edit", {"price": 12, "billing_cycle": 30,
                    "currency": "USD", "expired_at": None, "tags": "Keep me"})["status"] == "success"
                rows = call("/api/admin/client/list")
                assert len(rows) == 1 and rows[0]["expired_at"] is None
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for locale, code in [("en-US", "en"), ("zh-CN", "zh_CN"), ("zh-TW", "zh_TW"),
                                             ("ja-JP", "ja_JP"), ("id-ID", "id_ID")]:
                            strings = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())["common"]
                            for width, appearance in [(1280, "light"), (390, "dark")]:
                                context = browser.new_context(locale=locale, color_scheme=appearance,
                                    viewport={"width": width, "height": 950}, service_workers="block")
                                try:
                                    response = context.request.post(origin + "/api/login", data={"username": username, "password": password})
                                    assert response.ok and response.json()["status"] == "success"
                                    context.add_init_script("""(() => {
                                        localStorage.setItem('appearance', APPEARANCE);
                                        localStorage.setItem('language', LANGUAGE);
                                        localStorage.setItem('nekomari.github.releases.v3', JSON.stringify({
                                            fetchedAt: Date.now(), attemptedAt: Date.now(), releases: []}));
                                    })()""".replace("APPEARANCE", json.dumps(appearance)).replace("LANGUAGE", json.dumps(locale)))
                                    page = context.new_page()
                                    errors = []
                                    page.on("pageerror", lambda error: errors.append(str(error)))
                                    page.route("**/*", lambda route: route.continue_()
                                               if route.request.url.startswith(origin + "/") else route.abort())
                                    page.goto(origin + "/admin/servers")
                                    row = page.get_by_test_id("node-row")
                                    expect(row).to_have_count(1)
                                    badge = row.locator(".km-price-tags .rt-Badge").nth(1)
                                    expect(badge).to_have_text(strings["expiry_not_set"])
                                    expect(badge).to_have_attribute("data-accent-color", "gray")
                                    expect(row).to_contain_text("Keep me")
                                    assert "NaN" not in row.inner_text()
                                    expiry = (datetime.now(timezone.utc) + timedelta(days=7.5)).isoformat()
                                    assert call(f"/api/admin/client/{node}/edit", {"expired_at": expiry})["status"] == "success"
                                    page.reload()
                                    expect(badge).to_have_text(strings["expired_in"].replace("{{days}}", "8"))
                                    expect(badge).to_have_attribute("data-accent-color", "orange")
                                    assert call(f"/api/admin/client/{node}/edit", {"expired_at": None})["status"] == "success"
                                    page.reload()
                                    expect(badge).to_have_text(strings["expiry_not_set"])
                                    expect(badge).to_have_attribute("data-accent-color", "gray")
                                    assert errors == [], errors
                                    if output is not None and code == "zh_CN":
                                        page.screenshot(path=str(output.with_name(f"{output.stem}-{width}-{appearance}.png")))
                                    report["scenarios"].append({"locale": code, "width": width, "appearance": appearance,
                                        "null_expiry_renew_clear": "passed", "page_errors": 0})
                                finally:
                                    context.close()
                    finally:
                        browser.close()
                report["version"] = call("/api/version")["data"]
                assert call("/api/admin/client/list")[0]["expired_at"] is None
                report.update(status="passed", synthetic_rows=1, nullable_api_contract_preserved=True)
        finally:
            stop(process)
    report["scratch_process_removed"] = True
    if output is not None:
        output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    run(Path(args.panel_binary).resolve(strict=True), args.output)
