#!/usr/bin/env python3
"""Find holes in a ping task's samples: is the chart's "断断续续" real data loss?

The panel draws gaps when a minute has no sample, so a broken-looking line can mean
either (a) the probes genuinely did not report that minute, or (b) the chart is
downsampling and the holes are an artifact. Those have opposite fixes, so measure
before touching anything.

For each task it takes the newest tag shape (the pre-fork agents wrote
{"task_id":"N"}, this fork writes {"protocol":"tcp","task_id":"N"}), buckets the
samples per minute, and reports the holes per node.

Usage: ping-gap-analysis.py [hours] [task_id ...]
Run on OC424.
"""
import datetime
import json
import sqlite3
import sys
from collections import defaultdict

HOURS = float(sys.argv[1]) if len(sys.argv) > 1 else 4
ONLY = [str(int(a)) for a in sys.argv[2:]]
MDB = "/opt/nekomari/data/metrics.db"
KDB = "/opt/nekomari/data/komari.db"

m = sqlite3.connect(f"file:{MDB}?mode=ro", uri=True)
mc = m.cursor()
k = sqlite3.connect(f"file:{KDB}?mode=ro", uri=True)
kc = k.cursor()

now = datetime.datetime.now(datetime.timezone.utc).timestamp()
since_ms = int((now - HOURS * 3600) * 1000)

tasks = {str(r[0]): r for r in kc.execute(
    "select id, name, type, target, interval from ping_tasks")}
names = {r[0]: r[1] for r in kc.execute("select uuid, name from clients")}


def newest_tag(task_id):
    """The tag set carrying the freshest data for this task, not a hardcoded shape.

    The candidate list is materialised before the per-candidate query. Reusing one
    cursor while iterating it silently truncates the outer result -- which is exactly
    what happened here: the loop saw only the first distinct tag, so every task whose
    newest data lived under a different tag looked like it had no samples at all.
    """
    candidates = [t for (t,) in mc.execute(
        "select distinct tags from metric_series where metric_name='ping.loss'").fetchall()]
    best, best_ts = None, -1
    for t in candidates:
        try:
            if json.loads(t).get("task_id") != task_id:
                continue
        except Exception:
            continue
        r = mc.execute("""select max(r.bucket_milli) from metric_series s
            join metric_rollups r on r.series_id=s.id
            where s.metric_name='ping.loss' and s.tags=?""", (t,)).fetchone()
        if r and r[0] and r[0] > best_ts:
            best, best_ts = t, r[0]
    return best


def fmt(ms):
    return datetime.datetime.fromtimestamp(ms / 1000, datetime.timezone.utc).strftime("%H:%M")


print(f"=== ping sample gaps, last {HOURS:g}h (UTC window {fmt(since_ms)}..{fmt(int(now*1000))}) ===\n")

order = sorted(tasks, key=lambda t: int(t))
for tid in order:
    if ONLY and tid not in ONLY:
        continue
    _, tname, ttype, target, interval = tasks[tid]
    tag = newest_tag(tid)
    if not tag:
        print(f"task {tid:>3} {tname[:26]:<28} no series\n")
        continue

    rows = mc.execute("""
        select s.entity_id, r.bucket_milli
        from metric_series s join metric_rollups r on r.series_id = s.id
        where s.metric_name='ping.loss' and s.tags=? and r.bucket_milli >= ?
        order by s.entity_id, r.bucket_milli
    """, (tag, since_ms)).fetchall()

    per_node = defaultdict(list)
    for entity, bucket in rows:
        per_node[entity].append(bucket // 60000)  # minute index

    expected = int(HOURS * 3600 / (int(interval) if interval else 60))
    print(f"task {tid:>3} {tname[:26]:<28} {ttype:<5} {target[:30]:<32} every {interval}s, "
          f"expect ~{expected} samples/node")

    for entity, minutes in sorted(per_node.items()):
        uniq = sorted(set(minutes))
        if not uniq:
            continue
        gaps, longest, longest_at = 0, 0, None
        for a, b in zip(uniq, uniq[1:]):
            d = b - a
            if d > 1:
                gaps += 1
                if d > longest:
                    longest, longest_at = d, a
        span = uniq[-1] - uniq[0] + 1
        cover = len(uniq) / span * 100 if span else 0
        note = ""
        if longest >= 10:
            note = f"  <-- longest hole {longest} min at {fmt(longest_at*60000)}"
        print(f"      {names.get(entity, entity[:8])[:30]:<32} n={len(uniq):>4} "
              f"coverage={cover:5.1f}%  holes={gaps:>3}{note}")
    print()

m.close()
k.close()
