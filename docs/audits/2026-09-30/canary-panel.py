"""Read-only production acceptance; authentication never leaves this process."""
import argparse
import datetime as dt
import json
import math
import re
import sqlite3
import time
from pathlib import Path
from nekomari_auth import api_key_header, panel_call

MAC = "646e7117-b7fe-48d7-8bc1-9aef55fcf90e"
p = argparse.ArgumentParser()
p.add_argument("mode", choices=["fleet", "record", "analyze", "boundary", "boundary-check", "ping-check"])
p.add_argument("--seconds", type=int, default=120)
p.add_argument("--panel-file")
p.add_argument("--kernel-file")
a = p.parse_args()
auth = api_key_header()
assert auth, "No panel API key available"


def rpc(method, params):
    code, response = panel_call("/api/rpc2", "POST", {
        "jsonrpc": "2.0", "id": 1, "method": method, "params": params}, headers=auth)
    assert code == 200 and "error" not in response, (code, response)
    return response["result"]


def stamp(value):
    # Python 3.10 requires supported fractional widths, unlike Go's RFC3339Nano.
    value = re.sub(r"\.(\d+)", lambda m: "." + (m.group(1) + "000000")[:6], value)
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def iso(value):
    return dt.datetime.fromtimestamp(value, dt.timezone.utc).isoformat()


def latest():
    return rpc("common:getNodesLatestStatus", {"uuid": MAC, "include_ping": False})


if a.mode == "fleet":
    status = rpc("common:getNodesLatestStatus", {"include_ping": False})
    db = sqlite3.connect("file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
    rows = db.execute("SELECT uuid,name,version,icmp_capability FROM clients").fetchall()
    now = time.time()
    fleet = [{"uuid": uuid, "name": name, "version": version, "icmp": icmp,
              "online": status.get(uuid, {}).get("online", False),
              "age_seconds": now - stamp(status[uuid]["time"]) if uuid in status else None,
              "quality": status.get(uuid, {}).get("quality")}
             for uuid, name, version, icmp in rows]
    print(json.dumps({"registered": len(rows), "fleet": fleet}))
    assert len(rows) == 10 and all(r["online"] and r["age_seconds"] < 30 for r in fleet)
elif a.mode in ("record", "boundary"):
    deadline = time.monotonic() + a.seconds
    reports = {}
    while time.monotonic() < deadline:
        row = latest()
        reports[row["time"]] = row
        time.sleep(0.5 if a.mode == "boundary" else 1)
    rows = sorted(reports.values(), key=lambda r: stamp(r["time"]))
    print(json.dumps({"reports": rows}))
elif a.mode == "boundary-check":
    rows = json.loads(Path(a.panel_file).read_text())["reports"]
    segments = []
    for row in rows:
        if not segments or segments[-1][0]["counter_epoch"] != row["counter_epoch"]:
            segments.append([])
        segments[-1].append(row)
    assert len(segments) == 2 and all(len(s) >= 2 for s in segments), "Restart boundary not captured"
    first = segments[1][0]
    assert first["quality"]["net_rate"] != "ok" and first["net_in"] is None and first["net_out"] is None
    assert segments[1][1]["quality"]["net_rate"] == "ok"
    start, end = (math.floor(stamp(rows[0]["time"]) * 1000) + 1) / 1000, stamp(rows[-1]["time"]) + 0.001
    reply = rpc("public:queryMetrics", {"entity_id": MAC,
                "metric_keys": ["traffic.up", "traffic.down", "traffic.interval.valid"],
                "start": iso(start), "end": iso(end), "max_points": 500,
                "aggregation": "sum", "fill_empty": False})
    assert {s["metric_key"] for s in reply["series"]} == {
        "traffic.up", "traffic.down", "traffic.interval.valid"}
    evidence = []
    for series in reply["series"]:
        points = series["points"] or []
        if series["metric_key"] == "traffic.interval.valid":
            boundary = [p for p in points if abs(stamp(p["time"]) - stamp(first["time"])) < 0.002]
            assert len(boundary) == 1 and boundary[0]["value"] == 0, boundary
        else:
            field = "net_total_up" if series["metric_key"] == "traffic.up" else "net_total_down"
            expected = sum(s[-1][field] - s[0][field] for s in segments)
            actual = sum(p["value"] for p in points if p["value"] is not None)
            assert actual == expected, (series["metric_key"], actual, expected)
            evidence.append({"metric": series["metric_key"], "bytes": actual,
                             "expected_bytes_without_cross_epoch": expected})
    print(json.dumps({"epochs": [s[0]["counter_epoch"] for s in segments],
                      "reports_per_epoch": [len(s) for s in segments], "first_new_quality": first["quality"],
                      "first_new_rate_null": True, "boundary_interval_valid": 0, "traffic": evidence}))
elif a.mode == "ping-check":
    # Closed minute buckets avoid comparing a live accumulator with a stale SQL snapshot.
    end = math.floor(time.time() / 60) * 60 - 60
    start = end - 120
    query_end = end - 0.001  # API uses closed endpoints; exclude the next minute bucket.
    response = rpc("public:getPingMetricStats", {"uuid": MAC, "start": iso(start),
                   "end": iso(query_end), "max_points": 1})
    wide = rpc("public:getPingMetricStats", {"uuid": MAC, "start": iso(start),
               "end": iso(query_end), "max_points": 500})
    assert response["stats"] and response["stats"] == wide["stats"]
    db = sqlite3.connect("file:/opt/nekomari/data/metrics.db?mode=ro", uri=True)
    stored = db.execute("""SELECT s.metric_name,s.tags,SUM(r.count),SUM(r.sum),SUM(r.sum_sq),
        MIN(r.min_val),MAX(r.max_val) FROM metric_rollups r
        JOIN metric_series s ON s.id=r.series_id
        JOIN metric_resolutions d ON d.id=r.resolution_id
        WHERE s.entity_id=? AND d.resolution_milli=60000
        AND r.bucket_milli>=? AND r.bucket_milli<?
        AND s.metric_name IN ('ping.latency_ms','ping.success_latency_ms','ping.loss')
        GROUP BY s.metric_name,s.tags""", (MAC, int(start * 1000), int(end * 1000))).fetchall()
    groups = {}
    for metric, tags, count, total, square, low, high in stored:
        groups[(metric, json.dumps(json.loads(tags), sort_keys=True))] = (count, total, square, low, high)
    checks = []
    for stat in response["stats"]:
        assert stamp(stat["coverage_start"]) == start and stamp(stat["coverage_end_exclusive"]) == end
        key = json.dumps(stat["tags"], sort_keys=True)
        attempts = groups[("ping.loss", key)]
        assert stat["total"] == attempts[0] and stat["valid"] == attempts[0] - attempts[1], {"api": stat, "sql_attempts": attempts, "sql_success": groups.get(("ping.success_latency_ms", key)), "start": iso(start), "end": iso(end)}
        assert math.isclose(stat["loss"], attempts[1] / attempts[0] * 100, abs_tol=1e-8)
        success = groups.get(("ping.success_latency_ms", key))
        if stat["valid"]:
            assert success and success[0] == stat["valid"] and stat["quality"] == "success_only_v2"
            mean = success[1] / success[0]
            deviation = math.sqrt(max(0, success[2] / success[0] - mean * mean))
            assert math.isclose(stat["avg"], mean, abs_tol=1e-7)
            assert math.isclose(stat["stddev"], deviation, abs_tol=1e-6)
            assert stat["min"] == success[3] and stat["max"] == success[4] and stat["min"] >= 0
            assert stat["min"] <= stat["p50"] <= stat["p95"] <= stat["p99"] <= stat["max"]
        else:
            assert all(stat.get(k) is None for k in ["avg", "stddev", "min", "max", "p50", "p95", "p99"])
        checks.append({"tags": stat["tags"], "total": stat["total"], "valid": stat["valid"],
                       "loss_percent": stat["loss"], "quality": stat["quality"],
                       "sql_moments_match": True})
    print(json.dumps({"start": iso(start), "end": iso(end), "point_budget_invariant": True,
                      "persisted_bucket_checks": checks, "stats": response["stats"]}))
else:
    rows = json.loads(Path(a.panel_file).read_text())["reports"]
    kernel = json.loads(Path(a.kernel_file).read_text())
    samples = kernel["snapshots"]
    rows = [r for r in rows if samples[0]["after"] + 0.3 <= stamp(r["sampled_at"])
            <= samples[-1]["before"] - 0.3]
    assert len(rows) >= 15, "Insufficient distinct aligned reports"
    assert len({r["counter_epoch"] for r in rows}) == 1
    assert all(r["counter_epoch"] and r["quality"]["network"] == "ok" for r in rows)
    failures = []
    rate_errors = []
    sample_intervals = []
    ram_errors = []
    disk_errors = []
    connection_checks = []
    for row in rows:
        t = stamp(row["sampled_at"])
        lo = max((s for s in samples if s["after"] <= t - 0.02), key=lambda s: s["after"])
        hi = min((s for s in samples if s["before"] >= t + 0.02), key=lambda s: s["before"])
        for field, direction in [("net_total_up", "up"), ("net_total_down", "down")]:
            if not lo[direction] <= row[field] <= hi[direction]:
                failures.append({"time": row["time"], "field": field,
                                 "actual": row[field], "lo": lo[direction], "hi": hi[direction]})
        assert row["ram_total"] == lo["ram_total"] == hi["ram_total"]
        assert row["swap_total"] == lo["swap_total"] == hi["swap_total"]
        assert row["quality"]["ram_mode"] == "htoplike"
        ram_errors.append(min(abs(row["ram"] - lo["ram_htoplike_used"]),
                              abs(row["ram"] - hi["ram_htoplike_used"])) / row["ram_total"])
        assert lo["swap_used"] <= row["swap"] <= hi["swap_used"] or hi["swap_used"] <= row["swap"] <= lo["swap_used"]
        assert row["disk_total"] == lo["disk_total"] == hi["disk_total"]
        nearby = [s for s in samples if abs((s["before"] + s["after"]) / 2 - t) < 0.5]
        disk_errors.append(min(abs(row["disk"] - s["disk_used"]) for s in nearby))
        for field in ["connections_tcp", "connections_udp"]:
            low, high = min(s[field] for s in nearby), max(s[field] for s in nearby)
            assert row[field] is not None and low <= row[field] <= high, (row["time"], field, row[field], low, high)
        assert row["connections"] == row["connections_tcp"] + row["connections_udp"]
        connection_checks.append(row["time"])
    for previous, row in zip(rows, rows[1:]):
        seconds = stamp(row["sampled_at"]) - stamp(previous["sampled_at"])
        # Scheduling may add a one-second collection delay. Verify actual elapsed
        # time and the declared continuity contract, not an exact five-second tick.
        assert 0 < seconds <= 3 * row["sample_interval_seconds"] and row["quality"]["net_rate"] == "ok"
        sample_intervals.append(seconds)
        for rate, total in [("net_out", "net_total_up"), ("net_in", "net_total_down")]:
            expected = (row[total] - previous[total]) / seconds
            error = abs(row[rate] - expected) / max(1, expected)
            rate_errors.append(error)
    results = []
    minute_row = min(rows[1:], key=lambda r: abs(stamp(r["time"]) - stamp(rows[0]["time"]) - 60))
    assert abs(stamp(minute_row["time"]) - stamp(rows[0]["time"]) - 60) <= 3
    for label, last in [("single_sample", rows[1]), ("sixty_seconds", minute_row), ("full_capture", rows[-1])]:
        start, end = (math.floor(stamp(rows[0]["time"]) * 1000) + 1) / 1000, stamp(last["time"]) + 0.001
        expected = {"traffic.up": last["net_total_up"] - rows[0]["net_total_up"],
                    "traffic.down": last["net_total_down"] - rows[0]["net_total_down"]}
        for points in [1, 2, 500]:
            reply = rpc("public:queryMetrics", {"entity_id": MAC, "metric_keys": list(expected),
                        "start": iso(start), "end": iso(end), "max_points": points,
                        "aggregation": "sum", "fill_empty": False})
            assert {s["metric_key"] for s in reply["series"]} == set(expected)
            for series in reply["series"]:
                total = sum(point["value"] for point in series["points"] or [] if point["value"] is not None)
                results.append({"window_label": label, "seconds": stamp(last["time"]) - stamp(rows[0]["time"]),
                                "metric": series["metric_key"], "max_points": points, "bytes": total,
                                "expected_bytes": expected[series["metric_key"]],
                                "semantics": series.get("semantics"),
                                "window": series.get("window_semantics"), "point_count": series["count"],
                                "coverage_start": series.get("coverage_start"),
                                "coverage_end_exclusive": series.get("coverage_end_exclusive")})
                assert total == expected[series["metric_key"]], results[-1]
    ping = rpc("public:getPingMetricStats", {"uuid": MAC, "start": iso(start),
               "end": iso(end), "max_points": 1})
    ping_wide = rpc("public:getPingMetricStats", {"uuid": MAC, "start": iso(start),
                    "end": iso(end), "max_points": 500})
    assert ping["stats"], "No Ping samples in the acceptance window"
    assert ping["stats"] == ping_wide["stats"], "Ping depends on chart point budget"
    print(json.dumps({"interfaces": kernel["interfaces"], "reports": len(rows),
                      "start": iso(start), "end": iso(end), "kernel_bracket_failures": failures,
                      "max_rate_relative_error": max(rate_errors),
                      "actual_sample_interval_seconds": [min(sample_intervals), max(sample_intervals)],
                      "max_ram_fraction_error": max(ram_errors),
                      "mounts": kernel["mounts"], "disk_total": rows[0]["disk_total"],
                      "max_disk_used_error_bytes": max(disk_errors),
                      "connection_brackets_matched": len(connection_checks),
                      "traffic": results, "ping": ping["stats"]}))
    assert not failures, "Kernel cumulative counter does not match report"
    assert max(rate_errors) < 0.02 and max(ram_errors) < 0.01
    assert max(disk_errors) <= 1024 * 1024, "Disk usage differs by more than 1 MiB"
