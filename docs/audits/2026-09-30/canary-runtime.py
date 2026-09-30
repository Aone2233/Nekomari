"""Read-only identity, SQLite and authentication checks on the panel host."""
import argparse
import datetime as dt
import json
import sqlite3
import subprocess
from nekomari_auth import api_key_header, panel_call

p = argparse.ArgumentParser()
p.add_argument("--version", required=True)
p.add_argument("--commit", required=True)
p.add_argument("--image", required=True)
a = p.parse_args()

container = json.loads(subprocess.check_output(["docker", "inspect", "nekomari"]))[0]
assert container["Config"]["Image"] == a.image
assert container["State"]["Health"]["Status"] == "healthy"
assert container["RestartCount"] == 0
assert container["HostConfig"]["PortBindings"] == {
    "25774/tcp": [{"HostIp": "127.0.0.1", "HostPort": "25774"}]}
assert any(m["Source"] == "/opt/nekomari/data" and m["Destination"] == "/app/data"
           and m["Type"] == "bind" and m["RW"] for m in container["Mounts"])
image = json.loads(subprocess.check_output(["docker", "image", "inspect", a.image]))[0]
assert image["Architecture"] == "arm64"
assert image["Config"]["Labels"]["org.opencontainers.image.revision"] == a.commit
code, version = panel_call("/api/version")
assert code == 200 and version["data"] == {"version": a.version, "hash": a.commit[:7]}
code, _ = panel_call("/api/admin/client/list")
assert code == 401
authenticated, clients = panel_call("/api/admin/client/list", headers=api_key_header())
assert authenticated == 200
assert isinstance(clients, list) and len(clients) == 10
databases = {}
for name in ["komari.db", "metrics.db"]:
    db = sqlite3.connect("file:/opt/nekomari/data/" + name + "?mode=ro", uri=True)
    db.execute("PRAGMA query_only=ON")
    result = db.execute("PRAGMA quick_check").fetchall()
    assert result == [("ok",)], (name, result)
    databases[name] = "ok"
    db.close()
logs = subprocess.check_output(["docker", "logs", "--since", container["State"]["StartedAt"],
                                "nekomari"], stderr=subprocess.STDOUT).decode(errors="replace")
bad = [line for line in logs.splitlines() if any(s in line.lower() for s in [
    "panic:", "fatal error", "database is locked", "database disk image is malformed"])]
assert not bad, "Container has fatal/SQLite error signatures; inspect privately"
print(json.dumps({"checked_at": dt.datetime.now(dt.timezone.utc).isoformat(),
                  "version": version["data"], "image": a.image,
                  "revision": a.commit, "architecture": image["Architecture"],
                  "health": "healthy", "restarts": 0, "loopback_bind_preserved": True,
                  "data_mount_preserved": True, "databases": databases,
                  "unauthenticated_clients": code, "authenticated_clients": authenticated,
                  "registered_clients": len(clients),
                  "fatal_or_sqlite_log_errors": len(bad)}))
