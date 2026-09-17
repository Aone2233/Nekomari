#!/usr/bin/env python3
"""Which ping tasks are producing fresh data, and is the IPv6-only node in any of
them?

After the address-family filter landed, an IPv6-only node only produces samples for
targets it can actually reach: hostnames (resolved per node, may be dual-stack) or
IPv6 literals. So "does this node have data" is the question that decides whether
it is worth unhiding.

Run on OC424.
"""
import datetime
import json
import sqlite3

MDB = "/opt/nekomari/data/metrics.db"
KDB = "/opt/nekomari/data/komari.db"
FRESH_MIN = 240

m = sqlite3.connect(f"file:{MDB}?mode=ro", uri=True)
k = sqlite3.connect(f"file:{KDB}?mode=ro", uri=True)
live = {r[0]: r[1] for r in k.execute("select uuid, name from clients")}
now = datetime.datetime.utcnow()
fmt = lambda ms: datetime.datetime.utcfromtimestamp(ms / 1000)


def age_min(ms):
    return (now - fmt(ms)).total_seconds() / 60


print("=== fresh task series (newest tag shape per task) ===")
by_task = {}
for (tags,) in m.execute(
        "select distinct tags from metric_series where metric_name='ping.loss'"):
    try:
        tid = json.loads(tags).get("task_id")
    except Exception:
        continue
    r = m.execute("""select max(r.bucket_milli), count(*) from metric_series s
        join metric_rollups r on r.series_id = s.id
        where s.metric_name='ping.loss' and s.tags=?""", (tags,)).fetchone()
    if not r[0]:
        continue
    prev = by_task.get(tid)
    if prev is None or r[0] > prev[1]:
        by_task[tid] = (tags, r[0], r[1])

for tid, (tags, hi, n) in sorted(by_task.items(), key=lambda kv: int(kv[0])):
    a = age_min(hi)
    if a < FRESH_MIN:
        print(f"  task {tid:>2}  last={fmt(hi).strftime('%H:%M')} ({a:.0f} min ago)  "
              f"rows={n}  tags={tags}")

print()
print("=== per-node freshness for the IPv6-only node ===")
nomao = [u for u, n in live.items() if n.startswith("Nomao")]
if not nomao:
    print("  no Nomao node found")
else:
    u = nomao[0]
    found = False
    for (tags,) in m.execute(
            "select distinct tags from metric_series where metric_name='ping.loss' "
            "and entity_id=?", (u,)):
        r = m.execute("""select max(r.bucket_milli), count(*) from metric_series s
            join metric_rollups r on r.series_id = s.id
            where s.metric_name='ping.loss' and s.tags=? and s.entity_id=?""",
            (tags, u)).fetchone()
        if not r[0]:
            continue
        found = True
        a = age_min(r[0])
        flag = "  <-- FRESH" if a < FRESH_MIN else ""
        print(f"  {tags:<40} last={fmt(r[0]).strftime('%m-%d %H:%M')} "
              f"({a:.0f} min ago) rows={r[1]}{flag}")
    if not found:
        print("  no stored series at all")

print()
print("=== latency values, if fresh ===")
for (tags,) in m.execute(
        "select distinct tags from metric_series where metric_name='ping.latency_ms'"):
    try:
        d = json.loads(tags)
    except Exception:
        continue
    if not nomao:
        break
    u = nomao[0]
    rows = m.execute("""select r.last_val from metric_series s
        join metric_rollups r on r.series_id = s.id
        where s.metric_name='ping.latency_ms' and s.tags=? and s.entity_id=?
          and r.last_val > 0 order by r.bucket_milli desc limit 40""",
        (tags, u)).fetchall()
    if rows:
        vals = sorted(v for (v,) in rows)
        med = vals[len(vals) // 2]
        print(f"  task {d.get('task_id')} proto={d.get('protocol')}: "
              f"n={len(vals)} p50={med:.1f} min={vals[0]:.1f} max={vals[-1]:.1f} ms")

m.close()
k.close()
