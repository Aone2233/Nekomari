#!/usr/bin/env python3
"""Reassign nodes to ping tasks that ended up with an empty client list.

A task whose clients were all deleted keeps existing and keeps its interval, but
schedules nothing -- it looks configured in the UI and produces no data. The four
affected here are all mainland-China ISP latency targets, which is why the
assignment below favours the nodes that actually sit in or next to that region
rather than every node.

Dual-stack targets are written as hostnames, so the address-family filter routes
each node to the family it has; the same list works for v4-only and dual-stack
nodes without special-casing.

Usage: reassign-idle-tasks.py [--apply]
Run on OC424.
"""
import json
import os
import sqlite3
import sys
import urllib.request

APPLY = "--apply" in sys.argv
B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (reassign)"

# task id -> node names to assign, in order.
# China-facing targets: the two mainland nodes are the meaningful vantage points,
# Hong Kong is close enough to be useful, and the dual-stack tasks additionally
# suit a node that has IPv6.
PLAN = {
    6:  ["MAC Server | 微服务器", "并行智算云服务器"],                       # 河南移动 ipv4
    7:  ["MAC Server | 微服务器", "并行智算云服务器"],                       # 河南电信 ipv4
    11: ["MAC Server | 微服务器", "并行智算云服务器", "HK04 | 海创 Hytron Cloud"],  # 天津移动 v4+v6
    12: ["MAC Server | 微服务器", "并行智算云服务器", "HK04 | 海创 Hytron Cloud"],  # 天津电信双栈
}


sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from nekomari_auth import login, panel_call  # noqa: E402


def call(path, method="GET", body=None, cookie=None):
    status, resp = panel_call(path, method, body, cookie, base=B)
    if status != 200:
        raise SystemExit(f"{path} -> {status}: {resp}")
    return resp


k = sqlite3.connect("file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
by_name = {r[1]: r[0] for r in k.execute("select uuid, name from clients")}
addrs = {r[0]: ((r[1] or ""), (r[2] or ""))
         for r in k.execute("select uuid, ipv4, ipv6 from clients")}
k.close()

ck = login(base=B)

tasks = call("/api/rpc2", "POST",
             {"jsonrpc": "2.0", "method": "admin:getAllPingTasks",
              "params": {}, "id": 1}, cookie=ck)["result"]

edits = []
for t in tasks:
    if t["id"] not in PLAN:
        continue
    names = PLAN[t["id"]]
    missing = [n for n in names if n not in by_name]
    if missing:
        print(f"  task {t['id']}: unknown node name(s): {missing}")
        continue
    chosen = [by_name[n] for n in names]
    target = t.get("target") or ""
    host = target.rsplit(":", 1)[0]
    needs_v4 = ":" not in host
    print(f"task {t['id']}  {t.get('name')}  target={target}")
    print(f"  current clients: {len(t.get('clients') or [])}")
    for n, u in zip(names, chosen):
        v4, v6 = addrs.get(u, ("", ""))
        fam = "+".join(f for f, v in (("v4", v4), ("v6", v6)) if v) or "none"
        note = ""
        if needs_v4 and not v4:
            note = "  <-- no IPv4; the scheduler will skip it"
        print(f"    + {n[:34]:<36} {fam}{note}")
    t2 = dict(t)
    t2["clients"] = chosen
    edits.append(t2)

if not edits:
    print("nothing to reassign")
    raise SystemExit(0)
if not APPLY:
    print()
    print("dry run -- pass --apply to write")
    raise SystemExit(0)

r = call("/api/rpc2", "POST",
         {"jsonrpc": "2.0", "method": "admin:editPingTask",
          "params": {"tasks": edits}, "id": 2}, cookie=ck)
print()
print("edit result:", r.get("error") or "ok")
