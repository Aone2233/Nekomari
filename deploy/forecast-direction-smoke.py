#!/usr/bin/env python3
"""Check forecast direction gaps in a disposable, installed Panel."""

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


def seed_metrics(path, nodes, now, reset_day):
    with contextlib.closing(sqlite3.connect(path)) as db, db:
        db.execute("INSERT OR IGNORE INTO metric_resolutions (resolution_milli) VALUES (60000)")
        db.execute("INSERT OR IGNORE INTO metric_resolutions (resolution_milli) VALUES (300000)")
        db.execute("UPDATE metric_definitions SET retention_days=30 WHERE name IN ('traffic.up','traffic.down')")
        resolution = db.execute("SELECT id FROM metric_resolutions WHERE resolution_milli=60000").fetchone()[0]
        five_minutes = db.execute("SELECT id FROM metric_resolutions WHERE resolution_milli=300000").fetchone()[0]
        label = hashlib.sha256(b"forecast-fixture").hexdigest()
        db.execute("INSERT OR IGNORE INTO metric_label_sets (labels_hash,labels) VALUES (?, '{}')", (label,))
        label_id = db.execute("SELECT id FROM metric_label_sets WHERE labels_hash=?", (label,)).fetchone()[0]
        boundary = now.replace(day=reset_day, hour=0, minute=0, second=0, microsecond=0)
        if boundary > now:
            previous = boundary.replace(day=1) - dt.timedelta(days=1)
            boundary = previous.replace(day=reset_day)
        for node, kind in nodes:
            for direction in ("up", "down"):
                name = "traffic." + direction
                assert db.execute("SELECT 1 FROM metric_definitions WHERE name=?", (name,)).fetchone()
                series = db.execute("INSERT INTO metric_series (metric_name,entity_id,tags_hash,tags) VALUES (?,?,?,?)",
                                    (name, node, hashlib.sha256(b"{}").hexdigest(), "{}")).lastrowid
                if kind == "boundary":
                    for day in range(6):
                        timestamp = int((boundary + dt.timedelta(minutes=5, days=day)).timestamp() // 300 * 300000)
                        value = (day + 1) * (100 if direction == "up" else 50)
                        db.execute("""INSERT INTO metric_rollups
                            (series_id,resolution_id,label_id,bucket_milli,count,sum,sum_sq,min_val,max_val,
                             first_val,first_ts_milli,last_val,last_ts_milli,digest,created_at_milli)
                            VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?)""",
                                   (series, five_minutes, label_id, timestamp, 1, value, value*value, value, value,
                                    value, timestamp, value, timestamp, timestamp))
                    continue
                for day in range(6, 0, -1):
                    if kind in ("gap", "up") and direction == "down" and day == 3:
                        continue
                    value = (7-day) * (100 if direction == "up" else 50)
                    if kind == "reset" and direction == "up" and day <= 3:
                        value -= 300
                    timestamp = int((now - dt.timedelta(days=day)).timestamp() // 60 * 60000)
                    db.execute("""INSERT INTO metric_rollups
                        (series_id,resolution_id,label_id,bucket_milli,count,sum,sum_sq,min_val,max_val,
                         first_val,first_ts_milli,last_val,last_ts_milli,digest,created_at_milli)
                        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?)""",
                               (series, resolution, label_id, timestamp, 1, value, value*value, value, value,
                                value, timestamp, value, timestamp, timestamp))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    args = parser.parse_args()
    binary = args.panel_binary.resolve(strict=True)
    os.umask(0o077)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    admin = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
    process = None

    def call(path, body=None):
        request = Request(origin + path, data=None if body is None else json.dumps(body).encode(),
                          headers={"Content-Type": "application/json"})
        with admin.open(request, timeout=10) as response:
            return json.load(response)

    def wait(path):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("synthetic forecast Panel exited")
            try:
                if call(path)["status"] == "success":
                    return
            except (URLError, TimeoutError):
                pass
            time.sleep(0.1)
        raise RuntimeError("synthetic forecast Panel readiness timed out")

    def stop():
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)

    with tempfile.TemporaryDirectory(prefix="nekomari-forecast-direction-") as temp:
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
                password = "Test-Aa1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": "forecast-test", "password": password,
                    "sitename": "Synthetic forecast", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait("/api/public")
                stop()
                with contextlib.closing(sqlite3.connect(scratch / "data/komari.db")) as db, db:
                    for key in ("geo_ip_enabled", "login_notification", "notification_enabled"):
                        db.execute("INSERT OR REPLACE INTO configs (key,value) VALUES (?,?)", (key, "false"))
                start()
                wait("/api/public")
                assert call("/api/login", {"username": "forecast-test", "password": password})["status"] == "success"
                nodes = [(call("/api/admin/client/add", {"name": "Forecast " + kind})["uuid"], kind)
                         for kind in ("complete", "gap", "reset", "up", "empty", "boundary")]
                for uuid, kind in nodes:
                    assert call(f"/api/admin/client/{uuid}/edit", {"traffic_limit": 10000,
                        "traffic_limit_type": "up" if kind == "up" else "sum"})["status"] == "success"
                stop()
                now = dt.datetime.now(dt.timezone.utc)
                # Reset day 8 days ago makes this fixture independent of the calendar day.
                reset_day = (now - dt.timedelta(days=8)).day
                seed_metrics(scratch / "data/metrics.db", [(uuid, kind) for uuid, kind in nodes if kind != "empty"], now, reset_day)
                start()
                wait("/api/public")
                assert call("/api/login", {"username": "forecast-test", "password": password})["status"] == "success"
                response = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1,
                    "method": "admin:getTrafficForecast", "params": {"reset_day": reset_day}})
                assert "error" not in response, response.get("error")
                rows = {row["uuid"]: row for row in response["result"]["rows"]}
                assert len(rows) == len(nodes)
                for uuid, kind in nodes:
                    row = rows[uuid]
                    if kind in ("complete", "up", "boundary"):
                        assert row["method"] == "linear_rate" and row["used_bytes_known"] and row["used_bytes"] == (600 if kind == "up" else 900), row
                        if kind == "boundary":
                            assert row["basis"]["samples"] == 6 and row["used_bytes"] == 900, row
                    elif kind == "empty":
                        assert row["method"] == "insufficient" and not row["used_bytes_known"] and row["projected_bytes"] == 0, row
                    else:
                        assert row["method"] == "insufficient" and not row["used_bytes_known"] and row["projected_bytes"] == 0 and not row.get("warning"), row
                        assert ("missing" if kind == "gap" else "decreased") in row.get("reason", ""), row
                version = call("/api/version")["data"]
        finally:
            stop()
    print(json.dumps({"status": "passed", "version": version, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "scenarios": ["complete sum", "missing direction", "masked direction reset", "single direction", "no observations", "cycle boundary first bucket"],
                      "synthetic_only": True, "scratch_removed": True}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
