#!/usr/bin/env python3
"""Stability sweep: is anything actually flapping right now?

Looks at the things that would show instability rather than at uptime alone:
  * nodes that stopped reporting inside the window (not just "online now")
  * ping loss per task over the recent window, so a single bad target stands out
  * restart/error lines in the container log
  * whether the panel itself restarted recently

Run on OC424.
"""
import datetime
import json
import re
import sqlite3
import subprocess
import urllib.request

B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (stability)"
MDB = "/opt/nekomari/data/metrics.db"
WINDOW_MIN = 60
now = datetime.datetime.utcnow()


def sh(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True).stdout.strip()


def call(path, method="GET", body=None, cookie=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(B + path, method=method, data=data)
    req.add_header("User-Agent", UA)
    if body is not None:
        req.add_header("Content-Type", "application/json")
    if cookie:
        req.add_header("Cookie", cookie)
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


print("=== 1. container ===")
print("  " + sh("docker ps --filter name=nekomari --format '{{.Status}}'"))
print("  restarts: " + sh("docker inspect nekomari --format '{{.RestartCount}}'"))
print("  oom: " + sh("docker inspect nekomari --format '{{.State.OOMKilled}}'"))

print()
print("=== 2. errors / warnings in the container log ===")
log = sh("docker logs nekomari --since 2h 2>&1")
for pat in ("ERROR", "WARN", "panic", "fatal"):
    hits = [l for l in log.splitlines() if pat in l]
    print(f"  {pat:<6} {len(hits)}")
    for h in hits[:3]:
        print("    " + h[:150])

print()
def parse_ts(s):
    """Parse a Go/RFC3339 timestamp.

    The database stores nanosecond precision (2026-09-17T11:44:45.796839057Z) and
    Python 3.10's fromisoformat rejects more than 6 fractional digits, so the
    fraction is trimmed to microseconds first.
    """
    s = str(s).strip().replace("Z", "+00:00")
    m = re.match(r"^(.*\.\d{6})\d*(\+\d{2}:\d{2})?$", s)
    if m:
        s = m.group(1) + (m.group(2) or "")
    t = datetime.datetime.fromisoformat(s)
    if t.tzinfo is not None:
        t = t.astimezone(datetime.timezone.utc).replace(tzinfo=None)
    return t


print("=== 3. nodes: freshness of reports in the last %d min ===" % WINDOW_MIN)
k = sqlite3.connect(f"file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
nodes = list(k.execute("select uuid, name, updated_at from clients order by name"))
k.close()
for uuid, name, updated in nodes:
    if not updated:
        print(f"  {name[:32]:<34} never reported")
        continue
    try:
        t = parse_ts(updated)
    except Exception as e:
        print(f"  {name[:32]:<34} unparsable ({type(e).__name__}: {updated})")
        continue
    age = (now - t).total_seconds() / 60
    flag = "" if age < 5 else ("  <-- STALE" if age > 15 else "  <-- slow")
    print(f"  {name[:32]:<34} last report {age:>6.1f} min ago{flag}")

print()
print("=== 4. ping loss per task, last %d min ===" % WINDOW_MIN)
m = sqlite3.connect(f"file:{MDB}?mode=ro", uri=True)
cut = int((now - datetime.timedelta(minutes=WINDOW_MIN)).timestamp() * 1000)
rows = m.execute("""
    select s.tags, count(*) as n,
           sum(case when r.last_val > 0 then 1 else 0 end) as lossy
    from metric_series s join metric_rollups r on r.series_id = s.id
    where s.metric_name = 'ping.loss' and r.bucket_milli > ?
    group by 1 order by 1
""", (cut,)).fetchall()

tasks = {}
for tags, n, lossy in rows:
    try:
        d = json.loads(tags)
    except Exception:
        continue
    tid = d.get("task_id")
    if not tid:
        continue
    cur = tasks.setdefault(tid, [0, 0])
    cur[0] += n
    cur[1] += lossy

k = sqlite3.connect(f"file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
names = {str(r[0]): r[1] for r in k.execute("select id, name from ping_tasks")}
k.close()

for tid in sorted(tasks, key=lambda x: int(x)):
    n, lossy = tasks[tid]
    pct = lossy / n * 100 if n else 0
    flag = "  <-- loss" if pct > 5 else ""
    print(f"  task {tid:>2} {(names.get(tid) or '')[:26]:<28} samples={n:<6} "
          f"loss={pct:>5.1f}%{flag}")
m.close()
