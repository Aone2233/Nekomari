"""Read-only, fixed-window OC424 evidence. Run remotely through existing SSH."""

import json
import math
import sqlite3


def emit(**fields):
    print(json.dumps(fields, sort_keys=True))


connection = sqlite3.connect(
    "file:/opt/nekomari/data/metrics.db?mode=ro", uri=True, timeout=3
)
connection.execute("PRAGMA query_only=ON")
connection.execute("BEGIN")
try:
    # 2026-09-30 10:00-11:30 Asia/Shanghai. No migration/reset boundary.
    start, end = 1790733600000, 1790739000000
    rows_sql = """
        SELECT r.bucket_milli,r.count,r.first_val,r.first_ts_milli,
               r.last_val,r.last_ts_milli
        FROM metric_rollups r
        JOIN metric_series s ON s.id=r.series_id
        JOIN metric_resolutions d ON d.id=r.resolution_id
        WHERE s.entity_id=? AND s.metric_name=? AND d.resolution_milli=?
          AND r.bucket_milli>=? AND r.bucket_milli<?
        ORDER BY r.bucket_milli
    """
    for name in ("traffic.up", "traffic.down"):
        for resolution in (60000, 300000):
            rows = connection.execute(rows_sql, (
                "ad0c4739-9823-4f78-9017-005094b047c2", name, resolution, start, end
            )).fetchall()
            inside = sum(
                (r[4] - r[2] if r[4] >= r[2] else r[4]) if r[1] >= 2 else 0
                for r in rows
            )
            # Only normal-cadence, non-reset boundaries are included.
            boundary = sum(
                b[2] - a[4] for a, b in zip(rows, rows[1:])
                if 0 < b[3] - a[5] < 15000 and b[2] >= a[4]
            )
            growth = inside + boundary
            emit(metric=name, resolution_ms=resolution, buckets=len(rows),
                 inside=inside, boundary=boundary, observed_growth=growth,
                 missing_percent=100 * boundary / growth if growth else None)

    ping_sql = """
        SELECT s.metric_name,s.tags,r.count,r.sum,r.min_val,r.max_val
        FROM metric_rollups r
        JOIN metric_series s ON s.id=r.series_id
        JOIN metric_resolutions d ON d.id=r.resolution_id
        WHERE s.entity_id=? AND s.metric_name IN ('ping.latency_ms','ping.loss')
          AND d.resolution_milli=300000 AND r.bucket_milli=1790736000000
    """
    for row in connection.execute(ping_sql, (
        "41b1f6f8-ee8c-4dd9-8d43-2435af31b719",
    )):
        if json.loads(row[1]).get("task_id") == "17":
            emit(ping_row=row)

    variance_sql = """
        SELECT s.tags,r.count,r.sum,r.sum_sq,r.min_val,r.max_val
        FROM metric_rollups r
        JOIN metric_series s ON s.id=r.series_id
        JOIN metric_resolutions d ON d.id=r.resolution_id
        WHERE s.entity_id=? AND s.metric_name='ping.latency_ms'
          AND d.resolution_milli=60000 AND r.bucket_milli>=? AND r.bucket_milli<?
    """
    groups = {}
    for tags, n, total, squared, low, high in connection.execute(variance_sql, (
        "646e7117-b7fe-48d7-8bc1-9aef55fcf90e", start, end
    )):
        task = json.loads(tags).get("task_id")
        groups.setdefault(task, []).append((n, total, squared, low, high))
    for task, rows in sorted(groups.items()):
        if task not in ("18", "19"):
            continue
        count = sum(r[0] for r in rows)
        total = sum(r[1] for r in rows)
        squared = sum(r[2] for r in rows)
        bucket_stddev = sum(
            math.sqrt(max(0, r[2] / r[0] - (r[1] / r[0]) ** 2)) * r[0]
            for r in rows
        ) / count
        window_stddev = math.sqrt(max(0, squared / count - (total / count) ** 2))
        emit(task_id=task, buckets=len(rows), samples=count,
             minimum=min(r[3] for r in rows), maximum=max(r[4] for r in rows),
             weighted_bucket_stddev=bucket_stddev, window_stddev=window_stddev)
finally:
    connection.rollback()
    connection.close()
