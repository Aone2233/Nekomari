#!/usr/bin/env python3
"""Per-node stability for one ping task, using the series the current agents write.

Getting this wrong is easy and was: the pre-fork agents tagged their series
{"task_id":"3"}, while this fork's agents tag them
{"protocol":"tcp","task_id":"3"}. Both exist in the restored database, so a query
matching only the old shape returns history that stopped when the old instance was
retired -- and then every node looks dead or flaky for reasons that have nothing to
do with the network.

So this picks the newest tag shape per task and says which one it used.

Usage: ping-task-stats.py <task_id>
Run on OC424.
"""
import datetime
import json
import sqlite3
import statistics
import sys

TASK_ID = str(int(sys.argv[1]) if len(sys.argv) > 1 else 3)
KDB = "/opt/nekomari/data/komari.db"
MDB = "/opt/nekomari/data/metrics.db"

k = sqlite3.connect(f"file:{KDB}?mode=ro", uri=True)
kc = k.cursor()
m = sqlite3.connect(f"file:{MDB}?mode=ro", uri=True)
mc = m.cursor()

row = kc.execute("select id, name, type, target, interval, clients, all_clients "
                 "from ping_tasks where id=?", (int(TASK_ID),)).fetchone()
if not row:
    raise SystemExit(f"task {TASK_ID} not found")
tid, name, typ, target, interval, clients_json, all_clients = row
live = {r[0]: (r[1], r[2] or "", r[3] or "")
        for r in kc.execute("select uuid, name, ipv4, ipv6 from clients")}

# Pick the tag set with the newest data, not a hardcoded shape.
cands = [t for (t,) in mc.execute(
    "select distinct tags from metric_series where metric_name='ping.loss'")]
best, best_ts = None, -1
for t in cands:
    try:
        if json.loads(t).get("task_id") != TASK_ID:
            continue
    except Exception:
        continue
    r = mc.execute("""select max(r.bucket_milli) from metric_series s
        join metric_rollups r on r.series_id=s.id
        where s.metric_name='ping.loss' and s.tags=?""", (t,)).fetchone()
    if r and r[0] and r[0] > best_ts:
        best, best_ts = t, r[0]

if not best:
    raise SystemExit(f"no stored series for task {TASK_ID}")

fmt = lambda ms: datetime.datetime.utcfromtimestamp(ms / 1000).strftime("%m-%d %H:%M")
print(f"=== task {tid}: {name} ===")
print(f"  type={typ}  target={target}  interval={interval}s  all_clients={all_clients}")
print(f"  series tag in use: {best}")
print(f"  newest sample:     {fmt(best_ts)}")
try:
    listed = json.loads(clients_json or "[]")
except Exception:
    listed = []
print(f"  clients on task: {len(listed)}   existing: {len(live)}   "
      f"stale: {len([c for c in listed if c not in live])}")
tgt_host = (target or "").rsplit(":", 1)[0]
tgt_is_v4 = ":" not in tgt_host

loss_rows = mc.execute("""
    select s.entity_id, count(*),
           sum(case when r.last_val > 0 then 1 else 0 end)
    from metric_series s join metric_rollups r on r.series_id = s.id
    where s.metric_name='ping.loss' and s.tags=? group by 1
""", (best,)).fetchall()
lat_rows = mc.execute("""
    select s.entity_id, r.last_val
    from metric_series s join metric_rollups r on r.series_id = s.id
    where s.metric_name='ping.latency_ms' and s.tags=? and r.last_val > 0
""", (best,)).fetchall()
loss_by = {e: (n, l) for e, n, l in loss_rows}
lat_by = {}
for e, v in lat_rows:
    lat_by.setdefault(e, []).append(v)

print()
print(f"  {'node':<34} {'n':>5} {'loss%':>7} {'p50':>7} {'p95':>7} {'max':>8}  note")
print("  " + "-" * 88)
out = []
for e in set(loss_by) | set(lat_by):
    nm, v4, v6 = live.get(e, (f"(removed {e[:8]})", "", ""))
    n, lossy = loss_by.get(e, (0, 0))
    vals = sorted(lat_by.get(e, []))
    lp = (lossy / n * 100) if n else 0
    p50 = statistics.median(vals) if vals else None
    p95 = vals[int(len(vals) * 0.95)] if len(vals) > 1 else (vals[0] if vals else None)
    mx = vals[-1] if vals else None
    note = ""
    if e not in live:
        note = "node removed"
    elif tgt_is_v4 and not v4:
        note = "IPv6-only vs IPv4 target -> always 100%"
    out.append((nm, n, lp, p50, p95, mx, note))

for nm, n, lp, p50, p95, mx, note in sorted(out, key=lambda r: r[2]):
    f = lambda v: f"{v:>7.1f}" if v is not None else f"{'-':>7}"
    mx_s = f"{mx:>8.1f}" if mx is not None else f"{'-':>8}"
    print(f"  {nm[:32]:<34} {n:>5} {lp:>6.1f}% {f(p50)} {f(p95)} {mx_s}  {note}")

m.close()
k.close()
