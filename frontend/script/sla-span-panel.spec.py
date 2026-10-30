"""Verify SLA span wording in both embedded pages against scratch ping data."""

import argparse
import contextlib
import datetime as dt
import hashlib
from http.cookiejar import CookieJar
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
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright


ROOT = Path(__file__).resolve().parents[1]


def seed(path, entity, now):
    # The 24h SLA uses 5-minute buckets. Data ends hours before now to make
    # a 100% observed span unambiguously different from full-window coverage.
    minute = now.replace(second=0, microsecond=0)
    start = minute - dt.timedelta(minutes=minute.minute % 5, hours=6)
    with contextlib.closing(sqlite3.connect(path)) as db, db:
        db.execute("INSERT OR IGNORE INTO metric_resolutions (resolution_milli) VALUES (300000)")
        resolution = db.execute("SELECT id FROM metric_resolutions WHERE resolution_milli=300000").fetchone()[0]
        label = hashlib.sha256(b"sla-span-fixture").hexdigest()
        db.execute("INSERT OR IGNORE INTO metric_label_sets (labels_hash,labels) VALUES (?, '{}')", (label,))
        label_id = db.execute("SELECT id FROM metric_label_sets WHERE labels_hash=?", (label,)).fetchone()[0]
        tags = json.dumps({"task_id": "7"}, sort_keys=True, separators=(",", ":"))
        series = db.execute("INSERT INTO metric_series (metric_name,entity_id,tags_hash,tags) VALUES (?,?,?,?)",
                            ("ping.loss", entity, hashlib.sha256(tags.encode()).hexdigest(), tags)).lastrowid
        for i in range(2):
            timestamp = int((start + dt.timedelta(minutes=5*i)).timestamp() * 1000)
            db.execute("""INSERT INTO metric_rollups
                (series_id,resolution_id,label_id,bucket_milli,count,sum,sum_sq,min_val,max_val,
                 first_val,first_ts_milli,last_val,last_ts_milli,digest,created_at_milli)
                VALUES (?,?,?,?,1,0,0,0,0,0,?,0,?,NULL,?)""",
                       (series, resolution, label_id, timestamp, timestamp, timestamp, timestamp))
    return start


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

    def rpc(method, params):
        answer = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
        assert "error" not in answer, f"synthetic RPC failed: {answer.get('error')}"
        return answer["result"]

    def wait(path="/api/install/status"):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("scratch Panel exited")
            try:
                if call(path)["status"] == "success":
                    return
            except (URLError, TimeoutError):
                pass
            time.sleep(.1)
        raise RuntimeError("scratch Panel readiness timed out")

    def stop():
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)

    with tempfile.TemporaryDirectory(prefix="nekomari-sla-span-ui-") as temp:
        scratch = Path(temp)
        try:
            with (scratch / "panel.log").open("wb") as log:
                def start(path):
                    nonlocal process
                    env = {**os.environ, "HOME": str(scratch), "TZ": "UTC",
                           "HTTP_PROXY": "http://127.0.0.1:9", "HTTPS_PROXY": "http://127.0.0.1:9",
                           "ALL_PROXY": "http://127.0.0.1:9", "NO_PROXY": "127.0.0.1,localhost"}
                    env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
                    process = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                               cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)
                    wait(path)

                start("/api/install/status")
                username, password = "sla-ui", "Sla-Ui1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": username, "password": password,
                    "sitename": "Synthetic SLA", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait("/api/public")
                assert call("/api/login", {"username": username, "password": password})["status"] == "success"
                assert call("/api/admin/settings/", {"eula_accepted": True, "private_site": False,
                    "geo_ip_enabled": False, "login_notification": False, "notification_enabled": False})["status"] == "success"
                observed = call("/api/admin/client/add", {"name": "SLA observed fixture"})["uuid"]
                empty = call("/api/admin/client/add", {"name": "SLA empty fixture"})["uuid"]
                stop()
                start_at = seed(scratch / "data/metrics.db", observed, dt.datetime.now(dt.timezone.utc))
                start("/api/public")
                assert call("/api/login", {"username": username, "password": password})["status"] == "success"
                result = rpc("public:getSlaReport", {"window": "24h", "entity_ids": [observed, empty]})
                nodes = {n["entity_id"]: n for n in result["nodes"]}
                assert set(nodes) == {observed, empty} and result["interval_seconds"] == 300
                presence = nodes[observed]["presence"]
                task = nodes[observed]["tasks"][0]["loss"]["presence"]
                for item in (presence, task):
                    assert item["observed_buckets"] == item["expected_buckets"] == 2 and item["coverage"] == 1
                    assert dt.datetime.fromisoformat(item["last_data"].replace("Z", "+00:00")) == start_at + dt.timedelta(minutes=5)
                assert not nodes[empty]["has_report"]
                version = call("/api/version")["data"]

                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for locale, code, width, appearance in (("zh-CN", "zh_CN", 1280, "light"),
                                                                ("en-US", "en", 390, "dark")):
                            strings = json.loads((ROOT / f"src/i18n/locales/{code}.json").read_text())["status"]
                            context = browser.new_context(locale=locale, viewport={"width": width, "height": 900},
                                color_scheme=appearance, timezone_id="UTC", service_workers="block")
                            try:
                                assert context.request.post(origin + "/api/login", data={"username": username,
                                    "password": password}).json()["status"] == "success"
                                context.add_init_script("localStorage.setItem('language', %s); localStorage.setItem('appearance', %s);" %
                                                        (json.dumps(locale), json.dumps(appearance)))
                                page = context.new_page()
                                errors = []
                                page.on("pageerror", lambda error: errors.append(str(error)))
                                page.route("**/*", lambda route: route.continue_() if route.request.url.startswith(origin + "/") else route.abort())
                                for route in ("/admin/sla", "/standalone/sla/sla.html"):
                                    page.goto(origin + route, wait_until="domcontentloaded")
                                    expect(page.get_by_test_id("sla-report")).to_be_visible()
                                    expect(page.get_by_test_id("sla-coverage-explanation")).to_have_text(strings["coverageExplanation"])
                                    node = page.locator(f'[data-testid="sla-node"][data-entity-id="{observed}"]')
                                    expect(node.get_by_test_id("sla-node-presence")).to_contain_text(strings["nodePingSpan"])
                                    expect(node.get_by_test_id("sla-node-presence")).to_contain_text("2/2")
                                    for testid in ("sla-node-observed-range", "sla-task-observed-range"):
                                        expect(node.get_by_test_id(testid)).to_contain_text(strings["firstBucket"])
                                        expect(node.get_by_test_id(testid)).to_contain_text(strings["lastBucket"])
                                        expect(node.get_by_test_id(testid)).to_contain_text("UTC")
                                    missing = page.locator(f'[data-testid="sla-node"][data-entity-id="{empty}"]')
                                    expect(missing.get_by_test_id("sla-node-nodata")).to_have_text(strings["nodeNoData"])
                                    expect(missing.get_by_test_id("sla-node-observed-range")).to_have_count(0)
                                    assert page.url.endswith(route) and not errors, errors
                                    scenarios.append({"route": route, "locale": code, "width": width, "appearance": appearance})
                            finally:
                                context.close()
                    finally:
                        browser.close()
        finally:
            stop()
    print(json.dumps({"status": "passed", "version": version, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "scenarios": scenarios, "synthetic_only": True, "scratch_removed": True}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    run(parser.parse_args().panel_binary.resolve(strict=True))
