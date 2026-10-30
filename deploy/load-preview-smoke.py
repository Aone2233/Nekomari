#!/usr/bin/env python3
"""Exercise the published saved-rule preview on a disposable loopback Panel."""
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
from urllib.error import HTTPError, URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    binary = parser.parse_args().panel_binary.resolve(strict=True)
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    admin = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
    guest = build_opener(ProxyHandler({}))
    process = None

    def call(path, body=None, opener=admin, headers=None):
        request = Request(origin + path, data=None if body is None else json.dumps(body).encode(),
                          headers={"Content-Type": "application/json", **(headers or {})})
        try:
            with opener.open(request, timeout=10) as response:
                return response.status, json.load(response)
        except HTTPError as error:
            return error.code, json.load(error)

    def wait(path):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("synthetic preview Panel exited")
            try:
                status, body = call(path)
                if status == 200 and body["status"] == "success":
                    return
            except (URLError, TimeoutError):
                pass
            time.sleep(.1)
        raise RuntimeError("synthetic preview Panel readiness timed out")

    def stop():
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)

    with tempfile.TemporaryDirectory(prefix="nekomari-load-preview-") as temp:
        scratch = Path(temp)
        try:
            with (scratch / "panel.log").open("wb") as log:
                def start():
                    nonlocal process
                    env = {**os.environ, "HOME": str(scratch), "TZ": "UTC",
                           "HTTP_PROXY": "http://127.0.0.1:9", "HTTPS_PROXY": "http://127.0.0.1:9",
                           "ALL_PROXY": "http://127.0.0.1:9", "NO_PROXY": "127.0.0.1,localhost"}
                    env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
                    process = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                               cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)

                start()
                wait("/api/install/status")
                password = "Test-Aa1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username":"preview-test", "password":password,
                    "sitename":"Synthetic preview", "metric_dsn":"./data/metrics.db"})[1]["status"] == "success"
                wait("/api/public")
                assert call("/api/login", {"username":"preview-test", "password":password})[1]["status"] == "success"
                node = call("/api/admin/client/add", {"name":"Synthetic preview node"})[1]["uuid"]
                added = call("/api/admin/notification/load/add", {"name":"CPU rule", "clients":[node],
                    "metric":"cpu", "threshold":80, "ratio":.8, "interval":240})[1]
                assert added["status"] == "success", added
                rule = added["data"]["task_id"]
                api_key = call("/api/admin/api-keys", {"name":"preview read-only", "scopes":["nodes:read"],
                                                       "expires_days":1})[1]["data"]["token"]
                stop()
                with contextlib.closing(sqlite3.connect(scratch / "data/komari.db")) as db, db:
                    for setting in ("geo_ip_enabled", "login_notification", "notification_enabled"):
                        db.execute("INSERT OR REPLACE INTO configs (key,value) VALUES (?,?)", (setting, "false"))
                now = dt.datetime.now(dt.timezone.utc)
                with contextlib.closing(sqlite3.connect(scratch / "data/metrics.db")) as db, db:
                    db.execute("INSERT OR IGNORE INTO metric_resolutions (resolution_milli) VALUES (60000)")
                    resolution = db.execute("SELECT id FROM metric_resolutions WHERE resolution_milli=60000").fetchone()[0]
                    label = hashlib.sha256(b"preview-fixture").hexdigest()
                    db.execute("INSERT OR IGNORE INTO metric_label_sets (labels_hash,labels) VALUES (?, '{}')", (label,))
                    label_id = db.execute("SELECT id FROM metric_label_sets WHERE labels_hash=?", (label,)).fetchone()[0]
                    series = db.execute("INSERT INTO metric_series (metric_name,entity_id,tags_hash,tags) VALUES (?,?,?,?)",
                                        ("cpu.usage", node, hashlib.sha256(b"{}").hexdigest(), "{}")).lastrowid
                    timestamp = int((now - dt.timedelta(minutes=2)).timestamp() // 60 * 60000)
                    db.execute("""INSERT INTO metric_rollups
                        (series_id,resolution_id,label_id,bucket_milli,count,sum,sum_sq,min_val,max_val,
                         first_val,first_ts_milli,last_val,last_ts_milli,digest,created_at_milli)
                        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?)""",
                        (series, resolution, label_id, timestamp, 1, 90, 8100, 90, 90, 90, timestamp, 90, timestamp, timestamp))
                start()
                wait("/api/public")
                assert call("/api/login", {"username":"preview-test", "password":password})[1]["status"] == "success"
                key_headers = {"Authorization":"Bearer " + api_key}
                code, response = call("/api/admin/database/size", opener=guest, headers=key_headers)
                assert code == 200 and response["status"] == "success", response
                assert call("/api/admin/notification/load/preview", {"id":rule}, opener=guest)[0] in (401, 403)
                # A real, scoped key is denied even though the owner session is allowed.
                assert call("/api/admin/notification/load/preview", {"id":rule}, opener=guest,
                            headers=key_headers)[0] == 403
                path = "/api/admin/notification/load/preview"
                statuses = []
                for _ in range(2):
                    code, response = call(path, {"id":rule})
                    assert code == 200 and response["status"] == "success", response
                    targets = response["data"]["targets"]
                    assert len(targets) == 1 and targets[0]["client"] == node, targets
                    assert targets[0]["status"] == "matched" and targets[0]["samples"] >= 1, targets
                    assert targets[0]["threshold"] == 80 and targets[0]["exceeding"] >= targets[0]["required"], targets
                    statuses.append(targets[0]["status"])
                with contextlib.closing(sqlite3.connect(scratch / "data/komari.db")) as db:
                    assert db.execute("SELECT last_notified FROM load_notifications WHERE id=?", (rule,)).fetchone()[0] is None
                version = call("/api/version")[1]["data"]
        finally:
            stop()
    print(json.dumps({"status":"passed", "version":version, "binary_sha256":hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "checks":["scoped-key-control", "guest-denial", "scoped-key-denial", "saved-rule-matched-twice", "last-notified-unchanged"],
                      "statuses":statuses, "synthetic_only":True, "scratch_removed":True}))


if __name__ == "__main__":
    main()
