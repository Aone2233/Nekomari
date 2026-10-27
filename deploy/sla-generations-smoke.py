#!/usr/bin/env python3
"""Check overlapping SLA task generations in an isolated installed Panel."""

import argparse
import contextlib
import datetime as dt
import hashlib
from http.cookiejar import CookieJar
import json
import math
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


def check_task(task, maintenance=False):
    loss, latency = task["loss"], task["latency"]
    expected = (4, 2, 0.5, 3, 30, 39, 39.8) if maintenance else (5, 3, 0.4, 4, 27.5, 38.5, 39.7)
    buckets, lost, fraction, latency_buckets, p50, p95, p99 = expected
    assert loss["has_data"] and (loss["buckets"], loss["lost"]) == (buckets, lost)
    assert math.isclose(loss["fraction"], fraction)
    assert latency["has_data"] and latency["buckets"] == latency_buckets
    assert all(math.isclose(latency[name], value) for name, value in
               (("p50_ms", p50), ("p95_ms", p95), ("p99_ms", p99)))
    assert len(task["outages"]) == 2
    assert [x["buckets"] for x in task["outages"]] == ([1, 1] if maintenance else [1, 2])
    assert [x["duration"] for x in task["outages"]] == ([0, 0] if maintenance else [0, 60_000_000_000])
    assert loss["presence"]["observed_buckets"] == (4 if maintenance else 5)
    assert loss["presence"]["expected_buckets"] == (5 if maintenance else 6)


def seed_metrics(path, nodes, start):
    with contextlib.closing(sqlite3.connect(path)) as db, db:
        db.execute("INSERT OR IGNORE INTO metric_resolutions (resolution_milli) VALUES (60000)")
        resolution = db.execute("SELECT id FROM metric_resolutions WHERE resolution_milli=60000").fetchone()[0]
        label = hashlib.sha256(b"sla-fixture").hexdigest()
        db.execute("INSERT OR IGNORE INTO metric_label_sets (labels_hash,labels) VALUES (?, '{}')", (label,))
        label_id = db.execute("SELECT id FROM metric_label_sets WHERE labels_hash=?", (label,)).fetchone()[0]
        for entity, task_id in ((nodes[0], "7"), (nodes[0], "8"), (nodes[1], "7"), (nodes[3], "7")):
            generations = ({"task_id": task_id}, {"task_id": task_id, "protocol": "icmp", "family": "ipv4"})
            for metric in ("ping.loss", "ping.latency_ms"):
                assert db.execute("SELECT 1 FROM metric_definitions WHERE name=?", (metric,)).fetchone()
                for generation, tags in enumerate(generations):
                    raw = json.dumps(tags, sort_keys=True, separators=(",", ":"))
                    cursor = db.execute("INSERT INTO metric_series (metric_name,entity_id,tags_hash,tags) VALUES (?,?,?,?)",
                                        (metric, entity, hashlib.sha256(raw.encode()).hexdigest(), raw))
                    series = cursor.lastrowid
                    for minute in range(6):
                        timestamp = int((start + dt.timedelta(minutes=minute)).timestamp() * 1000)
                        value, count = (0 if metric == "ping.loss" else 10), 1
                        if entity == nodes[0] and task_id == "7":
                            count = 0 if minute == 2 else 1
                            if metric == "ping.loss":
                                value = int(minute in (1, 3) or (minute == 4 and generation == 1))
                            else:
                                value = ([10, -1, 0, 20, 30, 40] if generation == 0 else [15, 0, 0, 25, -1, 0])[minute]
                                if generation == 1 and minute in (1, 5):
                                    count = 0
                        db.execute("""INSERT INTO metric_rollups
                            (series_id,resolution_id,label_id,bucket_milli,count,sum,sum_sq,min_val,max_val,
                             first_val,first_ts_milli,last_val,last_ts_milli,digest,created_at_milli)
                            VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?)""",
                                   (series, resolution, label_id, timestamp, count, value * count,
                                    value * value * count, value, value, value, timestamp, value, timestamp, timestamp))
        return db.execute("SELECT count(*) FROM metric_rollups").fetchone()[0]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", required=True)
    args = parser.parse_args()
    binary = str(Path(args.panel_binary).resolve(strict=True))
    os.umask(0o077)
    checks = []
    with tempfile.TemporaryDirectory(prefix="nekomari-sla-generations-") as temp:
        scratch = Path(temp)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
        guest = build_opener(ProxyHandler({}))
        server = None

        def call(path, body=None, client=opener):
            request = Request(base + path, data=json.dumps(body).encode() if body is not None else None,
                              headers={"Content-Type": "application/json"})
            with client.open(request, timeout=10) as response:
                return json.load(response)

        def rpc(method, params, client=opener):
            result = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, client)
            assert "error" not in result, "synthetic SLA RPC failed"
            return result.get("result")

        def wait(predicate):
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError("synthetic SLA Panel exited")
                try:
                    if predicate():
                        return
                except (URLError, TimeoutError):
                    pass
                time.sleep(0.05)
            raise RuntimeError("synthetic SLA Panel readiness timed out")

        def stop():
            if server is not None and server.poll() is None:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=5)

        def start():
            nonlocal server
            env = dict(os.environ)
            env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
            server = subprocess.Popen([binary, "server", "--listen", f"127.0.0.1:{port}"],
                                      cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)

        try:
            with (scratch / "panel.log").open("wb") as log:
                start()
                wait(lambda: call("/api/install/status")["data"]["required"])
                password = "Test-Aa1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": "sla-test", "password": password,
                    "sitename": "Isolated SLA generations", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait(lambda: call("/api/public")["status"] == "success")
                stop()
                with contextlib.closing(sqlite3.connect(scratch / "data/komari.db")) as db, db:
                    for key in ("geo_ip_enabled", "login_notification", "notification_enabled", "private_site"):
                        db.execute("INSERT OR REPLACE INTO configs (key,value) VALUES (?,?)", (key, "false"))
                start()
                wait(lambda: call("/api/public")["status"] == "success")
                assert call("/api/login", {"username": "sla-test", "password": password})["status"] == "success"
                nodes = [call("/api/admin/client/add", {"name": name})["uuid"]
                         for name in ("overlap", "control", "empty", "hidden")]
                assert call("/api/admin/client/" + nodes[3] + "/edit", {"hidden": True})["status"] == "success"
                stop()
                start_at = dt.datetime.now(dt.timezone.utc).replace(second=0, microsecond=0) - dt.timedelta(minutes=10)
                rows = seed_metrics(scratch / "data/metrics.db", nodes, start_at)
                start()
                wait(lambda: call("/api/public")["status"] == "success")
                checks.append("installed synthetic Panel and scratch-only overlapping SQLite series; no real GeoIP/notifications")
                params = {"entity_ids": nodes[:3], "start": start_at.isoformat(),
                          "end": (start_at + dt.timedelta(minutes=6)).isoformat()}
                plain = rpc("public:getSlaReport", params)
                reports = {node["entity_id"]: node for node in plain["nodes"]}
                assert plain["interval_seconds"] == 60 and plain["count"] == 3
                task = next(t for t in reports[nodes[0]]["tasks"] if t["task_id"] == "7")
                check_task(task)
                checks.append("overlapping task generations counted once in loss,latency percentiles and chronological outages")
                for node_id, task_id in ((nodes[0], "8"), (nodes[1], "7")):
                    clean = next(t for t in reports[node_id]["tasks"] if t["task_id"] == task_id)
                    assert clean["loss"]["buckets"] == clean["latency"]["buckets"] == 6
                    assert clean["loss"]["fraction"] == 1 and not clean["outages"]
                assert not reports[nodes[2]]["has_report"] and not reports[nodes[0]]["reporting_gaps"]
                assert reports[nodes[0]]["presence"]["observed_buckets"] == 6
                checks.append("node/task isolation,empty-node reporting,task gaps and node presence union remain intact")
                window = rpc("admin:saveMaintenanceWindow", {"name": "synthetic", "clients": [nodes[0]],
                    "start": (start_at + dt.timedelta(minutes=3)).isoformat(),
                    "end": (start_at + dt.timedelta(minutes=4)).isoformat()})
                maintained = rpc("public:getSlaReport", params)
                node = next(n for n in maintained["nodes"] if n["entity_id"] == nodes[0])
                check_task(next(t for t in node["tasks"] if t["task_id"] == "7"), maintenance=True)
                assert node["maintenance_excluded_seconds"] == 60
                rpc("admin:deleteMaintenanceWindow", {"id": window["id"]})
                checks.append("maintenance removes the same unique buckets from loss,latency,coverage and incidents")
                public = rpc("public:getSlaReport", {**params, "entity_ids": nodes}, guest)
                assert {n["entity_id"] for n in public["nodes"]} == set(nodes[:3])
                assert rpc("public:getSlaReport", {**params, "entity_ids": [nodes[3]]}, guest)["count"] == 0
                checks.append("guest visibility filtering still excludes a hidden node with actual data")
                stop()
                start()
                wait(lambda: call("/api/public")["status"] == "success")
                assert rpc("public:getSlaReport", params) == plain
                stop()
                with contextlib.closing(sqlite3.connect(scratch / "data/metrics.db")) as db:
                    assert db.execute("SELECT count(*) FROM metric_rollups").fetchone()[0] == rows
                checks.append("restart produces the identical report without altering stored rollup count; scratch/process cleanup")
        finally:
            stop()
    print(json.dumps({"status": "passed", "groups": len(checks), "checks": checks,
                      "production_changed": False, "third_party_requests": False, "scratch_removed": True}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
