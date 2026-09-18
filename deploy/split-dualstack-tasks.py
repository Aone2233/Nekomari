#!/usr/bin/env python3
"""Split dual-stack ping tasks so each task measures one address family.

A dual-stack hostname in a mixed fleet produces incomparable numbers: the dual-stack
probes dial IPv6 and the v4-only probes dial IPv4, so one task reports two different
network paths as if they were the same measurement. HK04 read 12.6% loss on 天津移动
against 0.0% from the two v4-only probes -- the difference between the routes, not
instability in either.

The fix keeps the hostname (so DNS resilience is preserved) and makes each task
single-family by choosing probes that can only reach one family:

  * the existing task keeps the v4-only probes, so every number in it is IPv4
  * a new task per target takes the dual-stack probes, which all prefer IPv6

Both stay honest: within a task, every probe measures the same family.

Usage: split-dualstack-tasks.py [--apply]
Run on OC424.
"""
import json
import os
import sqlite3
import sys
import urllib.request

APPLY = "--apply" in sys.argv
B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (split-dualstack)"

# task id -> (new task name, hostname target)
SPLIT = {
    11: ("天津移动 IPv6", "tj-cm-dualstack.ip.zstaticcdn.com:80"),
    12: ("天津电信 IPv6", "tj-ct-dualstack.ip.zstaticcdn.com:80"),
}


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


k = sqlite3.connect("file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
nodes = {r[0]: {"name": r[1], "v4": r[2] or "", "v6": r[3] or ""}
         for r in k.execute("select uuid, name, ipv4, ipv6 from clients")}
k.close()

v4_only = [u for u, n in nodes.items() if n["v4"] and not n["v6"]]
v6_capable = [u for u, n in nodes.items() if n["v6"]]

print("  v4-only probes :", ", ".join(nodes[u]["name"] for u in v4_only))
print("  v6-capable     :", ", ".join(nodes[u]["name"] for u in v6_capable))
print()

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from nekomari_auth import login  # noqa: E402

ck = login(base=B)

tasks = call("/api/rpc2", "POST",
             {"jsonrpc": "2.0", "method": "admin:getAllPingTasks",
              "params": {}, "id": 1}, cookie=ck)["result"]
by_id = {t["id"]: t for t in tasks}
existing_names = {t.get("name") for t in tasks}

edits, adds = [], []
for tid, (newname, target) in SPLIT.items():
    t = by_id.get(tid)
    if not t:
        print(f"  task {tid}: not found, skipping")
        continue

    # Existing task: keep only probes that cannot use IPv6, so it is unambiguous.
    keep = [u for u in (t.get("clients") or []) if u in v4_only]
    print(f"task {tid} {t.get('name')}  -> IPv4-only")
    print(f"  clients {len(t.get('clients') or [])} -> {len(keep)}: "
          f"{', '.join(nodes[u]['name'] for u in keep)}")
    t2 = dict(t)
    t2["clients"] = keep
    edits.append(t2)

    if newname in existing_names:
        print(f"  {newname} already exists, not creating")
        continue
    adds.append({
        "name": newname,
        "target": target,
        "type": t.get("type") or "tcp",
        "interval": t.get("interval") or 60,
        "clients": v6_capable,
        "default_on": False,
    })
    print(f"  + new task {newname}  target={target}")
    print(f"    clients: {', '.join(nodes[u]['name'] for u in v6_capable)}")

if not edits and not adds:
    print("nothing to do")
    raise SystemExit(0)
if not APPLY:
    print()
    print("dry run -- pass --apply to write")
    raise SystemExit(0)

if edits:
    r = call("/api/rpc2", "POST",
             {"jsonrpc": "2.0", "method": "admin:editPingTask",
              "params": {"tasks": edits}, "id": 2}, cookie=ck)
    print("  edit:", r.get("error") or "ok")
for a in adds:
    r = call("/api/rpc2", "POST",
             {"jsonrpc": "2.0", "method": "admin:addPingTask",
              "params": a, "id": 3}, cookie=ck)
    print(f"  add {a['name']}:", r.get("error") or "ok")
