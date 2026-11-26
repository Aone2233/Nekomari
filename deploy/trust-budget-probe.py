#!/usr/bin/env python3
"""Scratch measurement for docs/TRUST-BUDGET-DESIGN.md.

Answers one question with numbers instead of belief: at production-ish size,
which of the five trust factors can be computed from the metric store as it is
today, and what does each one cost?

It builds a throwaway SQLite database with the store's own schema statements
(prefix `metric_`), fills it with synthetic rollups, and times candidate
queries. It reads nothing from the panel, writes nothing outside its `--path`,
and produces different numbers on different disks — the point is the order of
magnitude and the relative cost of the query shapes, not a benchmark.

    python3 deploy/trust-budget-probe.py            # writes /tmp/trust_exp.db
    python3 deploy/trust-budget-probe.py --keep     # keep the database
"""
import argparse
import os
import sqlite3
import time

NODES = 50
METRICS = ["cpu.usage", "ram.used", "disk.used", "network.up", "network.down", "tcp.retrans"]
FINE = 60_000      # one-minute rollup
COARSE = 300_000   # five-minute rollup
DAY = 86_400_000

parser = argparse.ArgumentParser()
parser.add_argument("--path", default="/tmp/trust_exp.db")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()

if os.path.exists(args.path):
    os.remove(args.path)

db = sqlite3.connect(args.path)
db.executescript("""
PRAGMA journal_mode=WAL;
CREATE TABLE metric_definitions (name VARCHAR(191) PRIMARY KEY, type VARCHAR(32) NOT NULL,
  unit VARCHAR(64) NOT NULL DEFAULT '', description TEXT NOT NULL,
  retention_days INTEGER NOT NULL DEFAULT 0, metadata TEXT NOT NULL,
  created_at_milli BIGINT NOT NULL, updated_at_milli BIGINT NOT NULL);
CREATE TABLE metric_labels (id INTEGER PRIMARY KEY AUTOINCREMENT, labels_hash VARCHAR(64) NOT NULL, labels TEXT NOT NULL, UNIQUE(labels_hash));
CREATE TABLE metric_series (id INTEGER PRIMARY KEY AUTOINCREMENT, metric_name VARCHAR(191) NOT NULL,
  entity_id VARCHAR(191) NOT NULL, tags_hash VARCHAR(64) NOT NULL, tags TEXT NOT NULL, UNIQUE(metric_name, entity_id, tags_hash));
CREATE TABLE metric_resolutions (id INTEGER PRIMARY KEY AUTOINCREMENT, resolution_milli BIGINT NOT NULL, UNIQUE(resolution_milli));
CREATE TABLE metric_rollups (series_id BIGINT NOT NULL, resolution_id BIGINT NOT NULL, label_id BIGINT NOT NULL,
  bucket_milli BIGINT NOT NULL, count BIGINT NOT NULL, sum DOUBLE PRECISION NOT NULL, sum_sq DOUBLE PRECISION NOT NULL,
  min_val DOUBLE PRECISION NOT NULL, max_val DOUBLE PRECISION NOT NULL,
  first_val DOUBLE PRECISION NOT NULL, first_ts_milli BIGINT NOT NULL,
  last_val DOUBLE PRECISION NOT NULL, last_ts_milli BIGINT NOT NULL,
  digest BLOB, created_at_milli BIGINT NOT NULL, UNIQUE(series_id, resolution_id, label_id, bucket_milli));
CREATE INDEX metric_series_metric_entity_idx ON metric_series (metric_name, entity_id);
CREATE INDEX metric_rollups_resolution_bucket_idx ON metric_rollups (resolution_id, bucket_milli);
""")

# Aligned to the coarse bucket, or the bucket-level join matches nothing.
T0 = (1_760_000_000_000 // COARSE) * COARSE

for name in METRICS:
    db.execute("INSERT INTO metric_definitions VALUES (?,?,?,?,?,?,?,?)", (name, "gauge", "", "", 30, "{}", T0, T0))
db.execute("INSERT INTO metric_labels (labels_hash, labels) VALUES ('h', '{}')")
db.execute("INSERT INTO metric_resolutions (resolution_milli) VALUES (?)", (FINE,))
db.execute("INSERT INTO metric_resolutions (resolution_milli) VALUES (?)", (COARSE,))

series = []
for node in range(NODES):
    for name in METRICS:
        series.append(db.execute(
            "INSERT INTO metric_series (metric_name, entity_id, tags_hash, tags) VALUES (?,?,?,?)",
            (name, f"node-{node:02d}", "h", "{}")).lastrowid)

started = time.time()
db.executemany("INSERT INTO metric_rollups VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", [
    (sid, 1, 1, T0 + bucket, 1, 1.0, 1.0, 1.0, 1.0, 1.0, T0 + bucket, 1.0, T0 + bucket + FINE, None, T0 + bucket)
    for sid in series for bucket in range(0, DAY, FINE)
])
db.executemany("INSERT INTO metric_rollups VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", [
    (sid, 2, 1, T0 + bucket, 5, 5.0, 5.0, 1.0, 1.0, 1.0, T0 + bucket, 1.0, T0 + bucket + COARSE, None, T0 + bucket)
    for sid in series for bucket in range(0, 7 * DAY, COARSE)
])
db.commit()
rows = db.execute("SELECT count(*) FROM metric_rollups").fetchone()[0]
print(f"{len(series)} series, {rows} rollup rows, {os.path.getsize(args.path)/1e6:.0f} MB, filled in {time.time()-started:.1f}s")

WINDOW_START = T0 + DAY - 3_600_000
WINDOW_END = T0 + DAY


def timed(label, sql, params):
    started = time.perf_counter()
    result = db.execute(sql, params).fetchall()
    print(f"{label}: {(time.perf_counter()-started)*1000:.1f} ms, {len(result)} rows")
    return result


timed("completeness, 1h", """
SELECT series_id, count(*) FROM metric_rollups
WHERE resolution_id = 1 AND bucket_milli >= ? AND bucket_milli < ?
GROUP BY series_id""", (WINDOW_START, WINDOW_END))

timed("completeness, 24h", """
SELECT series_id, count(*) FROM metric_rollups
WHERE resolution_id = 1 AND bucket_milli >= ? AND bucket_milli < ?
GROUP BY series_id""", (T0, T0 + DAY))

timed("schedule lag, 1h", """
SELECT series_id, avg(first_ts_milli - bucket_milli), max(first_ts_milli - bucket_milli)
FROM metric_rollups
WHERE resolution_id = 1 AND bucket_milli >= ? AND bucket_milli < ?
GROUP BY series_id""", (WINDOW_START, WINDOW_END))

timed("reconciliation, bucket-aligned join", """
SELECT f.series_id, sum(f.count), c.count
FROM metric_rollups f
JOIN metric_rollups c ON c.series_id = f.series_id AND c.resolution_id = 2
  AND c.bucket_milli = (f.bucket_milli / ?) * ?
WHERE f.resolution_id = 1 AND f.bucket_milli >= ? AND f.bucket_milli < ?
GROUP BY f.series_id, c.count""", (COARSE, COARSE, WINDOW_START, WINDOW_END))

timed("reconciliation, totals-only join", """
SELECT f.series_id, sum(f.count), sum(c.count)
FROM metric_rollups f
JOIN metric_rollups c ON c.series_id = f.series_id AND c.resolution_id = 2
WHERE f.resolution_id = 1 AND f.bucket_milli >= ? AND f.bucket_milli < ?
  AND c.bucket_milli >= ? AND c.bucket_milli < ?
GROUP BY f.series_id""", (WINDOW_START, WINDOW_END, WINDOW_START, WINDOW_END))

timed("reconciliation, coarse-driven", """
SELECT c.series_id, c.bucket_milli, c.count,
       (SELECT sum(f.count) FROM metric_rollups f
         WHERE f.series_id = c.series_id AND f.resolution_id = 1
           AND f.bucket_milli >= c.bucket_milli AND f.bucket_milli < c.bucket_milli + ?)
FROM metric_rollups c
WHERE c.resolution_id = 2 AND c.bucket_milli >= ? AND c.bucket_milli < ?""", (COARSE, WINDOW_START, WINDOW_END))

timed("reconciliation, coarse-driven with forced index", """
SELECT c.series_id, c.bucket_milli, c.count,
       (SELECT sum(f.count) FROM metric_rollups f INDEXED BY sqlite_autoindex_metric_rollups_1
         WHERE f.series_id = c.series_id AND f.resolution_id = 1
           AND f.bucket_milli >= c.bucket_milli AND f.bucket_milli < c.bucket_milli + ?)
FROM metric_rollups c
WHERE c.resolution_id = 2 AND c.bucket_milli >= ? AND c.bucket_milli < ?""", (COARSE, WINDOW_START, WINDOW_END))

columns = [row[1] for row in db.execute("PRAGMA table_info(metric_rollups)")]
print("clock factor needs received_at: stored:", "received_at_milli" in columns)
print("usability factor needs quality: stored:", "quality" in columns)

db.close()
if not args.keep:
    for suffix in ("", "-wal", "-shm"):
        if os.path.exists(args.path + suffix):
            os.remove(args.path + suffix)
