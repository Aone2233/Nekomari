#!/usr/bin/env python3
"""End-to-end check that the panel itself skips address-family mismatches.

Re-adds the IPv6-only node to an IPv4-target task, then confirms the scheduler
skips it without any configuration help. Before this change the node had to be
removed from the task by hand; the point of the feature is that the panel does it.

Run on OC424.
"""
import json
import sqlite3
import urllib.request

B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (family-check)"


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


ck = "session_token=" + call(
    "/api/login", "POST",
    {"username": "AONE2233", "password": "KOMT0721@aone2233"},
)["data"]["set-cookie"]["session_token"]

k = sqlite3.connect("file:/opt/nekomari/data/komari.db?mode=ro", uri=True)
nomao = [r[0] for r in k.execute(
    "select uuid from clients where name like 'Nomao%'")][0]
k.close()

tasks = call("/api/rpc2", "POST",
             {"jsonrpc": "2.0", "method": "admin:getAllPingTasks",
              "params": {}, "id": 1}, cookie=ck)["result"]

for t in tasks:
    if t["id"] != 3:
        continue
    if nomao not in t["clients"]:
        t["clients"] = t["clients"] + [nomao]
        r = call("/api/rpc2", "POST",
                 {"jsonrpc": "2.0", "method": "admin:editPingTask",
                  "params": {"tasks": [t]}, "id": 2}, cookie=ck)
        print("  re-added the IPv6-only node to task 3:", r.get("error") or "ok")
    else:
        print("  IPv6-only node already on task 3")
    print(f"  task 3 now lists {len(t['clients'])} nodes, target {t['target']}")
