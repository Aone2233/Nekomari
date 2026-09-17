#!/usr/bin/env python3
"""Explain a node's displayed traffic versus the provider's own counter.

The panel renders `getTrafficPercentage(agent.totalUp, agent.totalDown, limit,
type)`, i.e. it divides the agent's *cumulative* interface counter by the plan
limit. That counter starts at boot (or at the agent's rotation boundary when
`--month-rotate` is set), while a provider meters its own billing cycle. This
prints both sides so the difference is visible instead of inferred.

Usage: traffic_explain.py <base-url> <username> <password> [node-name-substring]
"""
import json
import sys
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:25774").rstrip("/")
USER = sys.argv[2] if len(sys.argv) > 2 else "AONE2233"
PASS = sys.argv[3] if len(sys.argv) > 3 else ""
WANT = (sys.argv[4] if len(sys.argv) > 4 else "").lower()

UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/140.0 Safari/537.36")
GiB = 1024 ** 3


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


token = "session_token=" + call(
    "/api/login", "POST", {"username": USER, "password": PASS}
)["data"]["set-cookie"]["session_token"]

nodes = call("/api/nodes", cookie=token)["data"]
print(f"{'NODE':<30} {'UP':>10} {'DOWN':>10} {'USED':>10} {'LIMIT':>10} {'TYPE':>5} {'SHOWN':>7}")
print("-" * 92)
for n in nodes:
    if WANT and WANT not in (n.get("name") or "").lower():
        continue
    try:
        rec = call(f"/api/recent/{n['uuid']}", cookie=token)["data"]
        net = (rec[0].get("network") or {}) if rec else {}
    except Exception:
        net = {}
    up = net.get("totalUp", 0) or 0
    dn = net.get("totalDown", 0) or 0
    limit = n.get("traffic_limit") or 0
    kind = (n.get("traffic_limit_type") or "sum")
    used = {"sum": up + dn, "max": max(up, dn), "min": min(up, dn),
            "up": up, "down": dn}.get(kind, up + dn)
    shown = f"{used / limit * 100:.1f}%" if limit else "no limit"
    print(f"{(n.get('name') or '')[:28]:<30} {up/GiB:>9.1f}G {dn/GiB:>9.1f}G "
          f"{used/GiB:>9.1f}G {limit/GiB if limit else 0:>9.1f}G {kind:>5} {shown:>7}")

print()
print("USED is what the panel divides by the limit. It is the agent's cumulative")
print("counter since boot (or since --month-rotate), not the provider's cycle usage.")
