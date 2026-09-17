#!/usr/bin/env python3
"""Fleet-wide check: which ping tasks pair an IPv6-only node with an IPv4 target,
and which carry references to nodes that no longer exist.

Either condition makes a healthy target render as a total failure, so it is worth
knowing whether this is one task or a pattern.

Run on OC424.
"""
import json
import sqlite3

k = sqlite3.connect("file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
kc = k.cursor()

live = {r[0]: (r[1], r[2] or "", r[3] or "")
        for r in kc.execute("select uuid, name, ipv4, ipv6 from clients")}

print("=== nodes ===")
for uuid, (nm, v4, v6) in live.items():
    fam = []
    if v4:
        fam.append("v4")
    if v6:
        fam.append("v6")
    print(f"  {nm[:32]:<34} {'+'.join(fam) or 'no address recorded'}")

print()
print("=== every ping task ===")
hdr = f"  {'id':>3} {'name':<24} {'type':<5} {'target':<26} {'fam':<5} {'listed':>6} {'stale':>6}  issues"
print(hdr)
print("  " + "-" * 104)

issues_total = 0
for tid, name, typ, target, interval, clients_json, all_clients in kc.execute(
        "select id, name, type, target, interval, clients, all_clients "
        "from ping_tasks order by id"):
    try:
        listed = json.loads(clients_json or "[]")
    except Exception:
        listed = []
    stale = [c for c in listed if c not in live]

    host = (target or "").rsplit(":", 1)[0]
    tgt_is_v4 = ":" not in host
    fam = "IPv4" if tgt_is_v4 else "IPv6"

    # Nodes that would always fail: wrong family, and they are on the task.
    bad = []
    for c in listed:
        if c not in live:
            continue
        nm, v4, v6 = live[c]
        if tgt_is_v4 and not v4:
            bad.append(nm)
        if not tgt_is_v4 and not v6:
            bad.append(nm)

    issues = []
    if bad:
        issues.append(f"family mismatch: {', '.join(n[:14] for n in bad)}")
    if stale:
        issues.append(f"{len(stale)} stale ref(s)")
    if issues:
        issues_total += 1
    print(f"  {tid:>3} {(name or '')[:24]:<24} {typ:<5} {(target or '')[:26]:<26} "
          f"{fam:<5} {len(listed):>6} {len(stale):>6}  {'; '.join(issues)}")

print()
print(f"  tasks with at least one issue: {issues_total}")

# Do the stale uuids still have stored history?
m = sqlite3.connect("file:/opt/nekomari/data/metrics.db?mode=ro", uri=True)
mc = m.cursor()
print()
print("=== do removed nodes still hold history? ===")
removed = mc.execute("""
    select distinct entity_id from metric_series
    where metric_name like 'ping.%'
""").fetchall()
removed = [e for (e,) in removed if e not in live]
print(f"  entities with ping series but no live node: {len(removed)}")
for e in removed:
    n = mc.execute("select count(*) from metric_series where entity_id=?", (e,)).fetchone()[0]
    print(f"    {e}  series={n}")

m.close()
k.close()
