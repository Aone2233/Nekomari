#!/usr/bin/env python3
"""Remove address-family mismatches from ping tasks, and drop stale client refs.

A node with no IPv4 address cannot reach an IPv4 target, so listing it produces a
permanent 100% loss bar. That is not a network fault, and it makes a healthy target
look broken -- the same class of problem as a protocol mismatch, which is what the
fork's `netcheck` exists to catch.

Also drops references to nodes that no longer exist: inert, but they inflate the
server count the UI shows.

Address families are read from the database, NOT from /api/nodes. The nodes API
does not expose ipv4/ipv6 at all, and an earlier version of this script assumed it
did -- which made every node look IPv4-less and would have emptied every task.
Hence the dry run.

Usage: fix-ping-family.py [--apply]
Run on OC424 (needs to read the database).
"""
import json
import sqlite3
import sys
import urllib.request

APPLY = "--apply" in sys.argv
B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (fix-ping-family)"
KDB = "/opt/nekomari/data/komari.db"


def call(path, method="GET", body=None, cookie=None):
    req = urllib.request.Request(
        B + path, method=method,
        data=json.dumps(body).encode() if body is not None else None)
    req.add_header("User-Agent", UA)
    if body is not None:
        req.add_header("Content-Type", "application/json")
    if cookie:
        req.add_header("Cookie", cookie)
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


k = sqlite3.connect(f"file:{KDB}?mode=ro", uri=True)
live = {r[0]: {"name": r[1], "ipv4": r[2] or "", "ipv6": r[3] or ""}
        for r in k.execute("select uuid, name, ipv4, ipv6 from clients")}
k.close()

ck = "session_token=" + call(
    "/api/login", "POST",
    {"username": "AONE2233", "password": "KOMT0721@aone2233"},
)["data"]["set-cookie"]["session_token"]

resp = call("/api/rpc2", "POST",
            {"jsonrpc": "2.0", "method": "admin:getAllPingTasks",
             "params": {}, "id": 1}, cookie=ck)
tasks = resp.get("result") or []

edits, skipped = [], []
for t in tasks:
    target = t.get("target") or ""
    host = target.rsplit(":", 1)[0]
    tgt_is_v4 = ":" not in host
    clients = list(t.get("clients") or [])

    keep, drop_family, drop_stale = [], [], []
    for c in clients:
        n = live.get(c)
        if n is None:
            drop_stale.append(c)
            continue
        if (tgt_is_v4 and not n["ipv4"]) or (not tgt_is_v4 and not n["ipv6"]):
            drop_family.append(n["name"])
            continue
        keep.append(c)

    if not drop_family and not drop_stale:
        skipped.append(t)
        continue

    print(f"task {t['id']}: {t.get('name')}  target={target}  "
          f"[{'IPv4' if tgt_is_v4 else 'IPv6'}]")
    if drop_family:
        print(f"  drop, cannot reach this family: {', '.join(drop_family)}")
    if drop_stale:
        print(f"  drop {len(drop_stale)} stale reference(s)")
    print(f"  clients: {len(clients)} -> {len(keep)}")
    edits.append((t, keep))

print()
print(f"  tasks needing a change: {len(edits)}   already consistent: {len(skipped)}")

if not edits:
    raise SystemExit(0)
if not APPLY:
    print("  dry run -- pass --apply to write")
    raise SystemExit(0)

payload = []
for t, keep in edits:
    new = dict(t)
    new["clients"] = keep
    payload.append(new)

r = call("/api/rpc2", "POST",
         {"jsonrpc": "2.0", "method": "admin:editPingTask",
          "params": {"tasks": payload}, "id": 2}, cookie=ck)
print("  edit result:", json.dumps(r.get("error") or "ok", ensure_ascii=False))
