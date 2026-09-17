#!/usr/bin/env python3
"""Report which restored nodes are actually reporting to the panel.

The panel is a private site, so this logs in first rather than assuming a
session. "Online" means the panel has a live report for the node, which is what
tells a restored-from-backup node apart from a reconnected one.

Usage: node_status.py <base-url> <username> <password>
"""
import json
import sys
import urllib.error
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:25774").rstrip("/")
USER = sys.argv[2] if len(sys.argv) > 2 else "AONE2233"
PASS = sys.argv[3] if len(sys.argv) > 3 else ""

UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/140.0 Safari/537.36")

token = None


def call(path, method="GET", body=None, cookie=None):
    req = urllib.request.Request(
        BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None)
    req.add_header("User-Agent", UA)
    if body is not None:
        req.add_header("Content-Type", "application/json")
    if cookie:
        req.add_header("Cookie", cookie)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


if not token:
    res = call("/api/login", "POST", {"username": USER, "password": PASS})
    token = "session_token=" + res["data"]["set-cookie"]["session_token"]
    print(f"logged in as {USER}\n")

nodes = call("/api/nodes", cookie=token)["data"]
online, offline = [], []
for n in nodes:
    uuid = n["uuid"]
    try:
        rec = call(f"/api/recent/{uuid}", cookie=token)["data"]
    except urllib.error.HTTPError:
        rec = []
    if rec:
        r = rec[0]
        online.append((
            n.get("name", ""),
            (r.get("cpu") or {}).get("usage"),
            (r.get("ram") or {}).get("used"),
            (r.get("ram") or {}).get("total"),
            r.get("updated_at", "")[:19],
        ))
    else:
        offline.append(n.get("name", ""))

print(f"{'NODE':<38} {'CPU':>7} {'RAM':>16}  LAST REPORT")
print("-" * 88)
for name, cpu, used, total, last in sorted(online):
    ram = f"{used/2**30:.1f}/{total/2**30:.1f}G" if used and total else "-"
    print(f"{name[:36]:<38} {cpu:>6.1f}% {ram:>16}  {last}")

if offline:
    print()
    for name in sorted(offline):
        print(f"{name[:36]:<38} {'—':>7} {'offline':>16}")

print()
print(f"online: {len(online)} / {len(nodes)}")
if offline:
    print("offline: " + ", ".join(sorted(offline)))
