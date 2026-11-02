"""Exercise the embedded admin rule-preview UI using a disposable Panel."""
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
from urllib.error import URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[1]


def run(binary):
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    admin = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
    process = None
    scenarios = []

    def call(path, body=None):
        request = Request(origin + path, data=None if body is None else json.dumps(body).encode(),
                          headers={"Content-Type": "application/json"})
        with admin.open(request, timeout=15) as response:
            return json.load(response)

    def wait(path):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("synthetic Panel exited")
            try:
                if call(path)["status"] == "success":
                    return
            except (URLError, TimeoutError):
                pass
            time.sleep(.1)
        raise RuntimeError("synthetic Panel readiness timed out")

    with tempfile.TemporaryDirectory(prefix="nekomari-load-preview-ui-") as temp:
        scratch = Path(temp)
        try:
            with (scratch / "panel.log").open("wb") as log:
                env = {**os.environ, "HOME": str(scratch), "TZ": "UTC",
                       "HTTP_PROXY": "http://127.0.0.1:9", "HTTPS_PROXY": "http://127.0.0.1:9",
                       "ALL_PROXY": "http://127.0.0.1:9", "NO_PROXY": "127.0.0.1,localhost"}
                env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
                process = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                           cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)
                wait("/api/install/status")
                username, password = "preview-ui", "Preview-Ui1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username":username, "password":password,
                    "sitename":"Synthetic preview", "metric_dsn":"./data/metrics.db"})["status"] == "success"
                wait("/api/public")
                assert call("/api/login", {"username":username, "password":password})["status"] == "success"
                assert call("/api/admin/settings/", {"eula_accepted":True, "geo_ip_enabled":False,
                    "login_notification":False, "notification_enabled":False})["status"] == "success"
                node = call("/api/admin/client/add", {"name":"Preview fixture"})["uuid"]
                added = call("/api/admin/notification/load/add", {"name":"Preview CPU", "clients":[node],
                    "metric":"cpu", "mode":"baseline", "threshold":0, "ratio":.8, "interval":240,
                    "baseline_days":7, "multiplier":3})
                assert added["status"] == "success"
                rule = added["data"]["task_id"]
                def saved_rule():
                    result = call("/api/admin/notification/load/")
                    return next(item for item in result["data"] if item["id"] == rule)
                assert saved_rule()["threshold"] == 0
                version = call("/api/version")["data"]

                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for locale, code, width, appearance in [("zh-CN", "zh_CN", 1280, "light"),
                                                                ("en-US", "en", 390, "dark")]:
                            strings = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())["loadAlert"]["preview"]
                            common = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())["common"]
                            context = browser.new_context(locale=locale, viewport={"width":width, "height":900},
                                                          color_scheme=appearance, service_workers="block")
                            try:
                                assert context.request.post(origin + "/api/login", data={"username":username,
                                    "password":password}).json()["status"] == "success"
                                context.add_init_script("localStorage.setItem('language', %s); localStorage.setItem('appearance', %s);" %
                                                        (json.dumps(locale), json.dumps(appearance)))
                                page = context.new_page()
                                errors = []
                                page.on("pageerror", lambda error: errors.append(str(error)))
                                page.route("**/*", lambda route: route.continue_() if route.request.url.startswith(origin + "/") else route.abort())
                                page.goto(origin + "/admin/notification/load")
                                row = page.get_by_test_id("load-rule-row").filter(has_text="Preview CPU")
                                row.get_by_role("button", name=common["edit"]).click()
                                dialog = page.get_by_test_id("load-rule-edit")
                                expect(dialog.get_by_test_id("load-rule-threshold")).to_have_value("0")
                                dialog.get_by_role("button", name=common["save"], exact=True).click()
                                expect(dialog).not_to_be_visible()
                                assert saved_rule()["threshold"] == 0
                                page.get_by_role("button", name=strings["button"]).click()
                                expect(page.get_by_text(strings["description"], exact=True)).to_be_visible()
                                page.get_by_role("button", name=strings["run"], exact=True).click()
                                expect(page.get_by_text(strings["status"]["no_data"], exact=True)).to_be_visible()
                                assert page.url.endswith("/admin/notification/load") and not errors, errors
                                scenarios.append({"locale":code, "width":width, "appearance":appearance})
                            finally:
                                context.close()
                    finally:
                        browser.close()
        finally:
            if process is not None and process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
    print(json.dumps({"status":"passed", "version":version, "binary_sha256":hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "scenarios":scenarios, "synthetic_only":True, "scratch_removed":True}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    run(parser.parse_args().panel_binary.resolve(strict=True))
