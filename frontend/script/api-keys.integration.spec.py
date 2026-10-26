"""Bounded upgrade + compiled key UI check using synthetic loopback state only.

Usage: python script/api-keys.integration.spec.py --panel NEW --baseline OLD --output DIR
Both executables must be local, verified builds. Never point this at a live DB.
"""

import argparse
from contextlib import closing
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import sqlite3
import subprocess
import time
from urllib.error import HTTPError, URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel", required=True, type=Path)
    parser.add_argument("--baseline", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    panel, baseline, output = args.panel.resolve(), args.baseline.resolve(), args.output.resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    password, legacy = "Test-Aa1-" + secrets.token_urlsafe(24), secrets.token_urlsafe(30)
    credentials = [password, legacy]
    admin = build_opener(ProxyHandler({}), HTTPCookieProcessor(http.cookiejar.CookieJar()))
    anonymous = build_opener(ProxyHandler({}))
    server = None
    log = (output / "server.log").open("w+")
    data = output / "synthetic-state"
    data.mkdir()
    checks = []

    def start(binary, directory=data):
        return subprocess.Popen([str(binary), "-d", str(directory / "panel.db"), "server", "--listen", f"127.0.0.1:{port}"],
                                cwd=directory, stdout=log, stderr=subprocess.STDOUT)

    def snapshot(source, destination):
        with closing(sqlite3.connect(source)) as original, closing(sqlite3.connect(destination)) as copied:
            original.backup(copied)
            assert copied.execute("PRAGMA quick_check").fetchone()[0] == "ok"

    def sha(path):
        with path.open("rb") as stream:
            return hashlib.file_digest(stream, "sha256").hexdigest()

    def stop():
        if server is not None and server.poll() is None:
            server.terminate()
            try:
                server.wait(timeout=15)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=5)

    def call(path, payload=None, token=None, session=False):
        request = Request(base + path, data=None if payload is None else json.dumps(payload).encode(),
                          headers={"Content-Type": "application/json"})
        if token:
            request.add_header("Authorization", "Bearer " + token)
        try:
            response = (admin if session else anonymous).open(request, timeout=15)
        except HTTPError as error:
            response = error
        with response:
            return response.status, json.load(response), response.headers

    def wait(path):
        for _ in range(250):
            if server.poll() is not None:
                raise RuntimeError("isolated panel exited")
            try:
                status, body, _ = call(path)
                if status == 200:
                    return body
            except (URLError, TimeoutError):
                pass
            time.sleep(0.1)
        raise RuntimeError("isolated panel not ready")

    def rpc(method, token):
        return call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method, "params": {}}, token)[1]

    try:
        server = start(baseline)
        assert wait("/api/install/status")["data"]["required"]
        assert call("/api/install/complete", {"username": "scope-smoke", "password": password,
                    "sitename": "Isolated scoped keys", "metric_dsn": str(data / "metrics.db")})[0] == 200
        wait("/api/version")
        assert call("/api/login", {"username": "scope-smoke", "password": password}, session=True)[0] == 200
        assert call("/api/admin/settings/", {"api_key": legacy}, session=True)[0] == 200
        assert "error" not in rpc("admin:getSettings", legacy)
        stop()
        snapshot(data / "panel.db", output / "synthetic-pre-upgrade.db")
        snapshot(data / "metrics.db", output / "synthetic-pre-upgrade-metrics.db")
        server = start(panel)
        wait("/api/version")
        assert "error" not in rpc("admin:getDatabaseSize", legacy)
        for method in ["admin:exec", "admin:dbQuery", "admin:getSettings", "admin:getClientToken"]:
            assert rpc(method, legacy).get("error", {}).get("code") == -32041
        with closing(sqlite3.connect(data / "panel.db")) as db:
            assert db.execute("SELECT COUNT(*) FROM configs WHERE key='api_key'").fetchone()[0] == 0
            digest, scopes = db.execute("SELECT token_digest,scopes FROM api_keys").fetchone()
            assert digest == hashlib.sha256(legacy.encode()).hexdigest() and json.loads(scopes) == ["nodes:read"]
        checks.append("old installation migrates atomically to digest-only nodes:read")
        status, body, headers = call("/api/admin/api-keys", {"name": "HTTP monitor", "scopes": ["nodes:read"]}, session=True)
        assert status == 200 and headers.get("Cache-Control") == "no-store"
        token, key_id = body["data"]["token"], body["data"]["key"]["id"]
        credentials.append(token)
        for path, payload in [("/api/admin/settings/", None), ("/api/admin/upload/init", {}), ("/api/admin/api-keys", {})]:
            assert call(path, payload, token)[0] == 403
        listed = call("/api/admin/api-keys", session=True)[1]
        assert token not in json.dumps(listed) and "token_digest" not in json.dumps(listed)
        status, body, headers = call(f"/api/admin/api-keys/{key_id}/rotate", {}, session=True)
        replacement = body["data"]["token"]
        credentials.append(replacement)
        assert status == 200 and headers.get("Cache-Control") == "no-store"
        assert rpc("admin:getDatabaseSize", token).get("error", {}).get("code") == -32041
        assert "error" not in rpc("admin:getDatabaseSize", replacement)
        assert call(f"/api/admin/api-keys/{key_id}/revoke", {}, session=True)[0] == 200
        assert rpc("admin:getDatabaseSize", replacement).get("error", {}).get("code") == -32041
        checks.append("real HTTP scope, no-store, one-time display, rotation and revocation")
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            try:
                page = browser.new_page(viewport={"width": 1100, "height": 1000}, locale="en-US")
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.goto(base + "/")
                page.evaluate("localStorage.setItem('language', 'en')")
                assert page.request.post(base + "/api/login", data={"username": "scope-smoke", "password": password}).status == 200
                page.goto(base + "/admin/settings/sign-on")
                page.get_by_label("Key name", exact=True).fill("Compiled browser monitor")
                page.get_by_role("button", name="Create key", exact=True).click()
                field = page.get_by_label("API token", exact=True)
                expect(field).to_be_visible()
                browser_token = field.input_value()
                credentials.append(browser_token)
                assert browser_token.startswith("nk_") and "error" not in rpc("admin:getDatabaseSize", browser_token)
                page.get_by_role("button", name="I have stored the token", exact=True).click()
                expect(field).to_have_count(0)
                region = page.get_by_role("region", name="Compiled browser monitor")
                region.get_by_role("button", name="Rotate token", exact=True).click()
                page.get_by_role("button", name="Confirm", exact=True).click()
                expect(field).to_be_visible()
                browser_replacement = field.input_value()
                credentials.append(browser_replacement)
                assert browser_replacement != browser_token
                assert rpc("admin:getDatabaseSize", browser_token).get("error", {}).get("code") == -32041
                assert "error" not in rpc("admin:getDatabaseSize", browser_replacement)
                page.get_by_role("button", name="I have stored the token", exact=True).click()
                region.get_by_role("button", name="Revoke key", exact=True).click()
                page.get_by_role("button", name="Confirm", exact=True).click()
                expect(region).to_contain_text("Revoked")
                assert rpc("admin:getDatabaseSize", browser_replacement).get("error", {}).get("code") == -32041
                assert not errors, "compiled page emitted a JavaScript error"
            finally:
                browser.close()
        checks.append("compiled embedded sign-on UI creates, rotates and revokes without API mocks")
        stop()
        rollback = output / "synthetic-rollback-state"
        rollback.mkdir()
        shutil.copy2(output / "synthetic-pre-upgrade.db", rollback / "panel.db")
        shutil.copy2(output / "synthetic-pre-upgrade-metrics.db", rollback / "metrics.db")
        with closing(sqlite3.connect(rollback / "panel.db")) as db:
            with db:
                db.execute("UPDATE configs SET value=? WHERE key='metric_dsn'", (json.dumps(str(rollback / "metrics.db")),))
        server = start(baseline, rollback)
        wait("/api/version")
        assert "error" not in rpc("admin:getSettings", legacy)
        checks.append("old binary plus paired pre-upgrade synthetic DB restores old authorization")
        stop()
        log.flush()
        log.seek(0)
        text = log.read()
        assert all(secret not in text for secret in credentials), "secret present in synthetic server log"
        checks.append("generated credentials absent from server logs")
        (output / "report.json").write_text(json.dumps({"status": "pass", "checks": checks,
            "panel_sha256": sha(panel), "baseline_sha256": sha(baseline),
            "production_data_used": False, "browser_api_mocks": False}, indent=2) + "\n")
        print(f"PASS: {len(checks)} bounded compiled-panel integration groups")
    finally:
        stop()
        log.close()


if __name__ == "__main__":
    main()
