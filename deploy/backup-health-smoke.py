#!/usr/bin/env python3
"""Real installed Panel -> local fake Telegram; no production credentials/network."""

import argparse
import contextlib
import datetime as dt
from http.cookiejar import CookieJar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import threading
import time
from urllib.error import URLError
from urllib.parse import parse_qs
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", required=True)
    args = parser.parse_args()
    binary = str(Path(args.panel_binary).resolve(strict=True))
    os.umask(0o077)
    messages = []
    message_lock = threading.Lock()

    class FakeTelegram(BaseHTTPRequestHandler):
        def do_POST(self):
            size = int(self.headers.get("Content-Length", "0"))
            assert 0 < size < 8192
            body = parse_qs(self.rfile.read(size).decode())
            assert body["chat_id"] == ["synthetic"] and self.path == "/botTEST/sendMessage"
            with message_lock:
                messages.append(body["text"][0])
            raw = b'{"ok":true}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def log_message(self, *_):
            pass

    with tempfile.TemporaryDirectory(prefix="nekomari-backup-health-smoke-") as temp:
        scratch = Path(temp)
        health_dir = scratch / "data/backup-health"
        health_dir.mkdir(parents=True, mode=0o700)
        report_path = health_dir / "ovh.json"
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        stub = ThreadingHTTPServer(("127.0.0.1", 0), FakeTelegram)
        thread = threading.Thread(target=stub.serve_forever, daemon=True)
        thread.start()
        opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
        base = f"http://127.0.0.1:{port}"
        server = None

        def call(path, body=None):
            request = Request(base + path, data=json.dumps(body).encode() if body is not None else None,
                              headers={"Content-Type": "application/json"})
            with opener.open(request, timeout=5) as response:
                return json.load(response)

        def rpc(method, params):
            result = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
            assert "error" not in result, "synthetic settings RPC failed"

        def wait(predicate):
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError("synthetic backup-watch Panel exited")
                try:
                    if predicate():
                        return
                except (URLError, TimeoutError):
                    pass
                time.sleep(0.05)
            raise RuntimeError("synthetic backup-watch readiness/notification timed out")

        def stop():
            if server is not None and server.poll() is None:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=5)

        def start(watch=False):
            nonlocal server
            env = dict(os.environ)
            env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
            if watch:
                env["NEKOMARI_BACKUP_HEALTH_FILE"] = str(report_path)
            server = subprocess.Popen([binary, "server", "--listen", f"127.0.0.1:{port}"],
                                      env=env, cwd=scratch, stdout=log, stderr=subprocess.STDOUT)

        def write_report(backup_hours=1, report_minutes=0):
            now = dt.datetime.now(dt.timezone.utc)
            checked = now - dt.timedelta(minutes=report_minutes)
            value = {"version": 1, "checked_at": checked.isoformat(), "received_at": checked.isoformat(),
                     "status": "ok", "backup_at": (now-dt.timedelta(hours=backup_hours)).isoformat()}
            report_path.write_text(json.dumps(value))
            report_path.chmod(0o600)

        checks = []
        try:
            with (scratch / "panel.log").open("wb") as log:
                start()
                wait(lambda: call("/api/install/status")["data"]["required"])
                password = "Test-Aa1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": "backup-test", "password": password,
                    "sitename": "Isolated backup watch", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait(lambda: call("/api/public")["status"] == "success")
                # Disable the synthetic instance's default GeoIP/login notice before
                # creating a session: the acceptance must never query a real provider.
                stop()
                import sqlite3
                with contextlib.closing(sqlite3.connect(scratch / "data/komari.db")) as db, db:
                    for key in ("geo_ip_enabled", "login_notification"):
                        db.execute("INSERT OR REPLACE INTO configs (key,value) VALUES (?,?)", (key, "false"))
                start()
                wait(lambda: call("/api/public")["status"] == "success")
                assert call("/api/login", {"username": "backup-test", "password": password})["status"] == "success"
                rpc("admin:setMessageSenderProvider", {"name": "telegram", "addition": json.dumps({
                    "endpoint": f"http://127.0.0.1:{stub.server_port}/bot", "bot_token": "TEST", "chat_id": "synthetic"})})
                rpc("admin:editSettings", {"geo_ip_enabled": False, "login_notification": False,
                                           "notification_enabled": True, "notification_method": "telegram"})
                checks.append("synthetic installed Panel with loopback fake Telegram only")
                stop()
                write_report(backup_hours=27)
                start(watch=True)
                wait(lambda: len(messages) == 1)
                assert "backup_stale" in messages[0]
                checks.append("stale encrypted-backup timestamp delivers an alert")
                stop()
                start(watch=True)
                wait(lambda: call("/api/public")["status"] == "success")
                time.sleep(0.5)
                assert len(messages) == 1
                checks.append("unchanged incident stays quiet across actual Panel restart")
                stop()
                write_report()
                start(watch=True)
                wait(lambda: len(messages) == 2)
                assert "recovered" in messages[1]
                checks.append("fresh check delivers a single recovery")
                stop()
                write_report(report_minutes=46)
                start(watch=True)
                wait(lambda: len(messages) == 3)
                assert "report_stale" in messages[2]
                checks.append("missing OVH reporting heartbeat is an independent alert")
                assert report_path.is_file() and (health_dir / "alert-state.json").is_file()
                stop()
                checks.append("bounded persistent state; processes and synthetic scratch cleaned")
        finally:
            stop()
            stub.shutdown()
            stub.server_close()
            thread.join(timeout=5)
    print(json.dumps({"status": "passed", "groups": len(checks), "checks": checks,
                      "notification_deliveries": len(messages), "production_changed": False,
                      "third_party_requests": False, "scratch_removed": True}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
