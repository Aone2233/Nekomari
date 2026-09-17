#!/usr/bin/env python3
"""Post-deployment health check for the Nekomari panel.

Checks the things that actually break in production: the container, the agent,
the reverse proxy, the databases, and whether the panel is serving real data
rather than merely answering 200.

Usage: healthcheck.py [base-url] [cookie]
"""
import json
import subprocess
import sys
import urllib.error
import urllib.request

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:25774"
COOKIE = sys.argv[2] if len(sys.argv) > 2 else None

results = []


def record(ok, name, detail=""):
    results.append((ok, name, detail))
    print(f"  {'PASS' if ok else 'FAIL'}  {name}{'  — ' + detail if detail else ''}")


def sh(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True).stdout.strip()


def get(path, cookie=None):
    req = urllib.request.Request(BASE + path)
    req.add_header("User-Agent", "Mozilla/5.0 (healthcheck)")
    if cookie:
        req.add_header("Cookie", cookie)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


print("=== 1. host services ===")
for unit in ("nginx", "docker"):
    state = sh(f"systemctl is-active {unit}")
    record(state == "active", f"{unit} active", state)

print("\n=== 2. container ===")
ps = sh("docker ps --filter name=nekomari --format '{{.Status}}|{{.Image}}'")
record("Up" in ps and "healthy" in ps, "container up + healthy", ps)
record("ipinfo" in ps or "v0.1.2" in ps, "image is the ip-info build", ps.split("|")[-1])

print("\n=== 3. agent ===")
agent = sh("systemctl is-active komari-agent-oc424")
record(agent == "active", "agent service active", agent)

print("\n=== 4. databases ===")
for db in ("komari.db", "metrics.db"):
    size = sh(f"stat -c %s /opt/nekomari/data/{db} 2>/dev/null")
    record(bool(size) and int(size or 0) > 0, f"{db} present", f"{size} bytes")
wal = sh("ls /opt/nekomari/data/*.db-wal 2>/dev/null | wc -l")
record(True, "WAL files (informational)", wal)

print("\n=== 5. API reachable + real data ===")
try:
    pub = get("/api/public")
    record(pub.get("status") == "success", "/api/public", pub.get("data", {}).get("sitename", ""))
except Exception as e:
    record(False, "/api/public", str(e))

try:
    nodes = get("/api/nodes", COOKIE)["data"]
    record(len(nodes) >= 9, "nodes present", f"{len(nodes)} nodes")
except Exception as e:
    record(False, "/api/nodes", str(e))

print("\n=== 6. live reporting ===")
online = 0
names = []
for n in nodes:
    try:
        rec = get(f"/api/recent/{n['uuid']}", COOKIE)["data"]
        if rec:
            online += 1
            names.append(n.get("name", ""))
    except Exception:
        pass
record(online >= 1, "at least one node reporting", f"{online}/{len(nodes)}: {', '.join(names)[:60]}")

print("\n=== 7. history retained ===")
try:
    uuid = nodes[0]["uuid"]
    recs = get(f"/api/records/load?uuid={uuid}&load_type=cpu&hours=720", COOKIE)["data"].get("records", [])
    record(len(recs) > 0, "historical metric series served", f"{len(recs)} points over 30d")
except Exception as e:
    record(False, "historical metrics", str(e))

print("\n=== 8. ip-info endpoints ===")
for path, label in (("/api/public/ip-info/v1/status", "status"),
                    ("/api/public/ip-info/v1/lookup?uuid=h&ip=8.8.8.8", "lookup"),
                    ("/api/public/ip-info/v1/latency?uuid=h&ip=8.8.8.8", "latency")):
    try:
        body = get(path, COOKIE)
        d = body.get("data", {})
        extra = ""
        if label == "latency":
            extra = f"{d.get('latency', {}).get('available_count')} probes"
        elif label == "lookup":
            extra = f"{d.get('network', {}).get('asn')} {d.get('location', {}).get('country_code')}"
        record(body.get("ok") is True, f"ip-info {label}", extra)
    except Exception as e:
        record(False, f"ip-info {label}", str(e))

print("\n=== 9. private-IP rejection ===")
try:
    get("/api/public/ip-info/v1/lookup?uuid=h&ip=192.168.1.1", COOKIE)
    record(False, "private IP rejected", "returned 200")
except urllib.error.HTTPError as e:
    record(e.code == 400, "private IP rejected", f"HTTP {e.code}")

print("\n=== 10. theme ===")
try:
    theme = get("/api/public")["data"].get("theme")
    record(theme == "LuminaPlus", "active theme", str(theme))
except Exception as e:
    record(False, "active theme", str(e))

failed = [r for r in results if not r[0]]
print(f"\n===== {len(results) - len(failed)}/{len(results)} passed =====")
for ok, name, detail in failed:
    print(f"  FAILED: {name} — {detail}")
sys.exit(1 if failed else 0)
