#!/usr/bin/env python3
"""Are the latency spikes on a task simultaneous across probes, or independent?

That distinction decides where the problem is. Simultaneous spikes mean the target
(or the path to it) degraded, and every probe saw it. Independent spikes mean each
probe has its own problem -- its own uplink, its own load, its own route.

Usage: ping-spike-shape.py <task_id> [hours]
Run on OC424.
"""
import datetime
import json
import sqlite3
import statistics
import sys

TASK_ID = str(int(sys.argv[1]) if len(sys.argv) > 1 else 3)
HOURS = int(sys.argv[2]) if len(sys.argv) > 2 else 6
MDB = "/opt/nekomari/data/metrics.db"
KDB = "/opt/nekomari/data/komari.db"

k = sqlite3.connect(f"file:{KDB}?mode=ro", uri=True)
names = {r[0]: r[1] for r in k.execute("select uuid, name from clients")}
k.close()

m = sqlite3.connect(f"file:{MDB}?mode=ro", uri=True)
mc = m.cursor()

# Pick the newest tag shape for this task, as elsewhere.
#
# The candidate list is materialised first: calling execute() on the same cursor
# while iterating it resets the outer iteration, which silently yields a partial
# list and then "no samples in the window".
cands = [t for (t,) in mc.execute(
    "select distinct tags from metric_series where metric_name='ping.latency_ms'")]
best, best_ts = None, -1
for t in cands:
    try:
        if json.loads(t).get("task_id") != TASK_ID:
            continue
    except Exception:
        continue
    r = mc.execute("""select max(r.bucket_milli) from metric_series s
        join metric_rollups r on r.series_id=s.id
        where s.metric_name='ping.latency_ms' and s.tags=?""", (t,)).fetchone()
    if r and r[0] and r[0] > best_ts:
        best, best_ts = t, r[0]
if not best:
    raise SystemExit(f"no latency series for task {TASK_ID}")

cut = int((datetime.datetime.utcnow() - datetime.timedelta(hours=HOURS)).timestamp() * 1000)
rows = mc.execute("""
    select s.entity_id, r.bucket_milli, r.last_val
    from metric_series s join metric_rollups r on r.series_id = s.id
    where s.metric_name='ping.latency_ms' and s.tags=? and r.bucket_milli > ?
      and r.last_val > 0
    order by r.bucket_milli
""", (best, cut)).fetchall()

series = {}
for e, b, v in rows:
    series.setdefault(e, {})[b // 60000] = v   # bucket to the minute

if not series:
    raise SystemExit("no samples in the window")

print(f"=== task {TASK_ID}, tag {best}, last {HOURS}h ===")
print(f"  probes with data: {len(series)}")

# Baseline and spike threshold per probe, so a slow-but-stable probe is not counted
# as spiking just for being slow.
stats = {}
for e, pts in series.items():
    vals = sorted(pts.values())
    p50 = statistics.median(vals)
    p95 = vals[int(len(vals) * 0.95)] if len(vals) > 1 else vals[0]
    stats[e] = (p50, p95, max(vals))
    print(f"  {names.get(e, e[:8])[:32]:<34} p50={p50:>7.1f} p95={p95:>7.1f} max={max(vals):>8.1f}  n={len(vals)}")

# A probe is "spiking" in a minute when it is well above its own p95.
spike_minutes = {}
for e, pts in series.items():
    p50, p95, _ = stats[e]
    thresh = max(p95 * 1.5, p50 * 3)
    for minute, v in pts.items():
        if v > thresh:
            spike_minutes.setdefault(minute, set()).add(e)

if not spike_minutes:
    print("\n  no spikes above each probe's own p95x1.5 / p50x3")
    raise SystemExit(0)

total = len(series)
print(f"\n  minutes with at least one probe spiking: {len(spike_minutes)}")
print(f"  {'minute':<18} {'probes spiking':>14}  {'share':>6}  who")
shared = 0
for minute in sorted(spike_minutes):
    who = spike_minutes[minute]
    share = len(who) / total
    if share >= 0.5:
        shared += 1
    ts = datetime.datetime.utcfromtimestamp(minute * 60).strftime("%m-%d %H:%M")
    label = ", ".join(sorted(names.get(e, e[:8])[:16] for e in who))[:70]
    print(f"  {ts:<18} {len(who):>10}/{total:<3} {share*100:>5.0f}%  {label}")

print()
print(f"  minutes where >=50% of probes spiked together: {shared}/{len(spike_minutes)}")
if shared / max(1, len(spike_minutes)) > 0.5:
    print("  => spikes are mostly SHARED: the target or the common path degraded")
else:
    print("  => spikes are mostly INDEPENDENT: each probe has its own problem")

m.close()
