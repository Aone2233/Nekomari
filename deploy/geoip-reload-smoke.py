#!/usr/bin/env python3
"""Exercise real GeoIP settings events in an isolated, synthetic installed Panel."""

import argparse
from http.cookiejar import CookieJar
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
from urllib.error import URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", required=True)
    args = parser.parse_args()
    binary = str(Path(args.panel_binary).resolve(strict=True))
    os.umask(0o077)
    checks = []
    with tempfile.TemporaryDirectory(prefix="nekomari-geoip-smoke-") as temp:
        scratch = Path(temp)
        (scratch / "data").mkdir(mode=0o700)
        # Constructor failure must be offline, not a real MMDB download.
        (scratch / "data/GeoLite2-Country.mmdb").write_bytes(b"invalid-test-database")
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
        base = f"http://127.0.0.1:{port}"
        log_path = scratch / "panel.log"

        def call(path, body=None):
            request = Request(base + path, data=json.dumps(body).encode() if body is not None else None,
                              headers={"Content-Type": "application/json"})
            with opener.open(request, timeout=5) as response:
                return json.load(response)

        def wait(predicate):
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError("synthetic Panel exited")
                try:
                    if predicate():
                        return
                except (URLError, TimeoutError):
                    pass
                time.sleep(0.05)
            raise RuntimeError("synthetic Panel readiness/reload timed out")

        def rpc(method, params=None):
            result = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method,
                                        "params": {} if params is None else params})
            assert "error" not in result, "synthetic settings RPC failed"
            return result.get("result")

        def edit(settings, diagnostic):
            offset = log_path.stat().st_size
            rpc("admin:editSettings", settings)
            wait(lambda: diagnostic in log_path.read_text(errors="replace")[offset:])
            current = rpc("admin:getSettings")
            assert all(current[key] == value for key, value in settings.items())
            assert call("/api/public")["status"] == "success"

        with log_path.open("wb") as log:
            server = subprocess.Popen([binary, "server", "--listen", f"127.0.0.1:{port}"],
                                      cwd=scratch, stdout=log, stderr=subprocess.STDOUT)
            pid = server.pid
            try:
                wait(lambda: call("/api/install/status")["data"]["required"])
                password = "Test-Aa1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": "geoip-test", "password": password,
                    "sitename": "Isolated GeoIP test", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait(lambda: call("/api/public")["status"] == "success")
                assert call("/api/login", {"username": "geoip-test", "password": password})["status"] == "success"
                wait(lambda: "using GeoIP provider" in log_path.read_text(errors="replace"))
                checks.append("synthetic install/login; no production credentials or mounts")
                edit({"geo_ip_enabled": False}, "GeoIP disabled")
                checks.append("disable event reloads without a restart")
                edit({"geo_ip_enabled": True}, "using GeoIP provider")
                checks.append("enable event reloads without changing provider key")
                edit({"geo_ip_provider": "geojs"}, "using GeoIP provider")
                checks.append("provider replacement reloads")
                edit({"geo_ip_enabled": "wrong-type"}, "invalid GeoIP enabled setting")
                checks.append("malformed boolean falls back without process exit")
                edit({"geo_ip_enabled": True, "geo_ip_provider": 12}, "invalid GeoIP provider setting")
                checks.append("malformed provider falls back without process exit")
                edit({"geo_ip_provider": "mmdb"}, "provider initialization failed")
                result = call("/api/rpc2", {"jsonrpc": "2.0", "id": 2, "method": "admin:testGeoip",
                                             "params": {"ip": "192.0.2.1"}})
                assert "empty GeoIP provider" in result["error"]["message"]
                checks.append("invalid local MMDB falls back; no network lookup/download")
                edit({"geo_ip_provider": "ipinfo"}, "using GeoIP provider")
                assert server.pid == pid and server.poll() is None
                checks.append("valid settings recover; original Panel PID remains alive")
            finally:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=5)
    print(json.dumps({"status": "passed", "checks": checks, "groups": len(checks),
                      "scratch_removed": True, "production_changed": False}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
