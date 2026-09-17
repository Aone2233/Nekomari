#!/usr/bin/env python3
"""Check ping-record history depth and per-client sampling interval.

Two questions the API view could not answer:
  * The 24h query returned only ~5.7h of records, starting immediately after the
    container was replaced. Either the window is not honoured, or the redeploy
    discarded history. Those have very different implications.
  * Whether the failing client is failing for a configuration reason.

Run on OC424.
"""
import datetime
import sqlite3

db = sqlite3.connect("file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
q = db.cursor()

print("=== tables that look like ping storage ===")
for (name,) in q.execute(
        "select name from sqlite_master where type='table' order by name"):
    if "ping" in name.lower() or "record" in name.lower() or "task" in name.lower():
        n = q.execute(f"select count(*) from {name}").fetchone()[0]
        print(f"  {name:<28} rows={n}")

print()
print("=== ping_records: overall time range (task 3) ===")
try:
    lo, hi, n = q.execute(
        "select min(time), max(time), count(*) from ping_records where task_id=3"
    ).fetchone()
    print(f"  count={n}")
    print(f"  earliest={lo}")
    print(f"  latest  ={hi}")
except Exception as e:
    print(f"  query failed: {e}")
    print("  columns:")
    for row in q.execute("pragma table_info(ping_records)"):
        print(f"    {row[1]} {row[2]}")

print()
print("=== per-client sample counts and gaps (task 3) ===")
try:
    rows = q.execute(
        "select client, count(*) as n, min(time), max(time) "
        "from ping_records where task_id=3 group by client order by n"
    ).fetchall()
    for client, n, lo, hi in rows:
        print(f"  {client[:20]:<22} n={n:<5} {lo} .. {hi}")
except Exception as e:
    print(f"  {e}")

print()
print("=== clients: address families (why one client may never reach an IPv4 target) ===")
for row in q.execute("pragma table_info(clients)"):
    pass
cols = [r[1] for r in q.execute("pragma table_info(clients)")]
keep = [c for c in cols if c.lower() in
        ("uuid", "name", "ipv4", "ipv6", "token")]
if keep:
    sel = ", ".join(keep)
    for r in q.execute(f"select {sel} from clients"):
        d = dict(zip(keep, r))
        print(f"  {str(d.get('name'))[:32]:<34} ipv4={d.get('ipv4') or '(none)':<20} "
              f"ipv6={d.get('ipv6') or '(none)'}")
else:
    print("  (no address columns on clients)")
db.close()
