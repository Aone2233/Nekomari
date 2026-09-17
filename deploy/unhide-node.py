#!/usr/bin/env python3
"""Unhide a node once it is actually contributing data.

A hidden node is excluded from the public view. That is the right default while a
node is being set up, but once it reports on at least one ping task it is worth
showing -- and the address-family filter means an IPv6-only node legitimately
contributes to hostname and IPv6 targets while being skipped on IPv4 literals, so
"has it produced anything recently" is the question that decides it.

Usage: unhide-node.py <name-substring> [--apply]
Run on OC424.
"""
import datetime
import json
import sqlite3
import sys
import urllib.request

NEEDLE = sys.argv[1] if len(sys.argv) > 1 else "Nomao"
APPLY = "--apply" in sys.argv
FRESH_MIN = 240
B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (unhide-node)"


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
rows = list(k.execute(
    "select uuid, name, hidden, ipv4, ipv6 from clients where name like ?",
    (f"%{NEEDLE}%",)))
k.close()
if not rows:
    raise SystemExit(f"no node matching {NEEDLE!r}")

m = sqlite3.connect("file:/opt/nekomari/data/metrics.db?mode=ro", uri=True)
now = datetime.datetime.utcnow()

for uuid, name, hidden, v4, v6 in rows:
    fams = "+".join(f for f, v in (("v4", v4), ("v6", v6)) if v)
    print(f"  {name}  uuid={uuid[:8]}  addresses={fams or 'none'}  hidden={bool(hidden)}")

    fresh = []
    for (tags,) in m.execute(
            "select distinct tags from metric_series where metric_name='ping.loss' "
            "and entity_id=?", (uuid,)):
        r = m.execute("""select max(r.bucket_milli) from metric_series s
            join metric_rollups r on r.series_id = s.id
            where s.metric_name='ping.loss' and s.tags=? and s.entity_id=?""",
            (tags, uuid)).fetchone()
        if not r[0]:
            continue
        age = (now - datetime.datetime.utcfromtimestamp(r[0] / 1000)).total_seconds() / 60
        if age < FRESH_MIN:
            fresh.append((tags, age))

    if fresh:
        print("    fresh ping data:")
        for tags, age in sorted(fresh, key=lambda x: x[1]):
            print(f"      {tags:<38} {age:.0f} min ago")
    else:
        print("    no fresh ping data")

    if not hidden:
        print("    already visible")
        continue
    if not fresh:
        print("    leaving hidden: nothing fresh to show yet")
        continue
    if not APPLY:
        print("    would unhide (dry run)")
        continue

    ck = "session_token=" + call(
        "/api/login", "POST",
        {"username": "AONE2233", "password": "KOMT0721@aone2233"},
    )["data"]["set-cookie"]["session_token"]
    r = call("/api/rpc2", "POST",
             {"jsonrpc": "2.0", "method": "admin:editClient",
              "params": {"uuid": uuid, "hidden": False}, "id": 1}, cookie=ck)
    print("    unhide:", r.get("error") or "ok")

m.close()
