#!/usr/bin/env python3
"""Rewrite ping targets from IPv4 literals to their hostnames, so nodes of both
address families can be scheduled on them.

An IPv4 literal can only be reached over IPv4 -- there is no IPv6 route to
"1.1.1.1", because the families are separate address spaces. So an IPv6-only node
is skipped, correctly. Writing the target as the hostname instead lets each agent
resolve it for its own family: IPv4 nodes get the A record, IPv6 nodes the AAAA.

What changes semantically, and should be stated rather than glossed over: the two
families may resolve to different machines. For 1.1.1.1 -> one.one.one.one both are
Cloudflare anycast DNS. For 1.51.3.134 -> mirrors.cernet.edu.cn the A record IS
1.51.3.134, so IPv4 nodes keep measuring exactly the same host; only IPv6 nodes
land elsewhere. That is the price of including them.

Usage: literal-targets-to-hostnames.py [--apply]
Run on OC424.
"""
import json
import sys
import urllib.request

APPLY = "--apply" in sys.argv
B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (target-rewrite)"

# literal host -> (hostname, why it is the same service)
REWRITES = {
    "1.1.1.1": ("one.one.one.one", "Cloudflare DNS; AAAA 2606:4700:4700::1001"),
    "1.51.3.134": ("mirrors.cernet.edu.cn",
                   "CERNET mirror; A record is 1.51.3.134 itself, AAAA 2001:250:4:100::2"),
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


ck = "session_token=" + call(
    "/api/login", "POST",
    {"username": "AONE2233", "password": "KOMT0721@aone2233"},
)["data"]["set-cookie"]["session_token"]

tasks = call("/api/rpc2", "POST",
             {"jsonrpc": "2.0", "method": "admin:getAllPingTasks",
              "params": {}, "id": 1}, cookie=ck)["result"]

edits = []
for t in tasks:
    target = t.get("target") or ""
    host, _, port = target.rpartition(":")
    if not host:
        host, port = target, ""
    if host not in REWRITES:
        continue
    new_host, why = REWRITES[host]
    new_target = f"{new_host}:{port}" if port else new_host
    print(f"task {t['id']}: {t.get('name')}")
    print(f"  {target}  ->  {new_target}")
    print(f"  {why}")
    t2 = dict(t)
    t2["target"] = new_target
    edits.append(t2)

if not edits:
    print("no IPv4-literal targets to rewrite")
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
