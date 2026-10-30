#!/usr/bin/env python3
"""Exercise offline grace and maintenance on an isolated Panel and fake Telegram."""

import argparse
import base64
import contextlib
import datetime as dt
from http.cookiejar import CookieJar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import struct
import subprocess
import tempfile
import threading
import time
from urllib.error import URLError
from urllib.parse import parse_qs
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener


class AgentSocket:
    """Minimal loopback WebSocket client; a periodic agent.pull keeps it alive."""

    def __init__(self, port, token):
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=5)
        try:
            self.sock.settimeout(5)
            key = base64.b64encode(os.urandom(16)).decode()
            self.sock.sendall((
                "GET /api/clients/v2/rpc HTTP/1.1\r\n"
                f"Host: 127.0.0.1:{port}\r\nOrigin: http://127.0.0.1:{port}\r\n"
                f"Authorization: Bearer {token}\r\nUpgrade: websocket\r\n"
                f"Connection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: {key}\r\n\r\n"
            ).encode())
            response = b""
            while b"\r\n\r\n" not in response and len(response) < 8192:
                data = self.sock.recv(4096)
                if not data:
                    raise RuntimeError("synthetic websocket closed during handshake")
                response += data
            if not response.startswith(b"HTTP/1.1 101 "):
                raise RuntimeError("synthetic websocket handshake rejected")
        except Exception:
            self.sock.close()
            raise
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.keep_alive, daemon=True)
        self.thread.start()

    def keep_alive(self):
        payload = b'{"jsonrpc":"2.0","id":1,"method":"agent.pull","params":{}}'
        while not self.stop.is_set():
            mask = os.urandom(4)
            frame = bytes([0x81, 0x80 | len(payload)]) + mask
            frame += bytes(byte ^ mask[index % 4] for index, byte in enumerate(payload))
            try:
                self.sock.sendall(frame)
            except OSError:
                return
            self.stop.wait(3)

    def close(self):
        if self.stop.is_set():
            return
        self.stop.set()
        self.thread.join(timeout=5)
        # An RST makes the synthetic disconnect observable even if replies are unread.
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
        self.sock.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", type=Path, required=True)
    binary = parser.parse_args().panel_binary.resolve(strict=True)
    os.umask(0o077)
    messages = []
    lock = threading.Lock()

    class FakeTelegram(BaseHTTPRequestHandler):
        def do_POST(self):
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 < size < 8192:
                self.send_error(400)
                return
            form = parse_qs(self.rfile.read(size).decode())
            if self.path != "/botTEST/sendMessage" or form.get("chat_id") != ["synthetic"]:
                self.send_error(400)
                return
            with lock:
                messages.append(form["text"][0])
            raw = b'{"ok":true}'
            self.send_response(200)
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def log_message(self, *_):
            pass

    with tempfile.TemporaryDirectory(prefix="nekomari-offline-grace-") as temp:
        scratch = Path(temp)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        stub = ThreadingHTTPServer(("127.0.0.1", 0), FakeTelegram)
        thread = threading.Thread(target=stub.serve_forever, daemon=True)
        thread.start()
        opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
        base = f"http://127.0.0.1:{port}"
        server = None
        agents = []

        def call(path, body=None):
            request = Request(base + path, data=None if body is None else json.dumps(body).encode(),
                              headers={"Content-Type": "application/json"})
            with opener.open(request, timeout=5) as response:
                return json.load(response)

        def rpc(method, params):
            reply = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
            if "error" in reply:
                raise RuntimeError(f"synthetic RPC failed: {method}: {reply['error']}")
            return reply.get("result")

        def wait(predicate, seconds=30):
            end = time.monotonic() + seconds
            while time.monotonic() < end:
                if server.poll() is not None:
                    raise RuntimeError("synthetic Panel exited")
                try:
                    if predicate():
                        return
                except (URLError, TimeoutError):
                    pass
                time.sleep(0.1)
            raise RuntimeError("synthetic Panel readiness or notification timed out")

        def stop():
            if server is not None and server.poll() is None:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=5)

        def open_agent(token):
            agent = AgentSocket(port, token)
            agents.append(agent)
            return agent

        def received(kind, node):
            with lock:
                return sum(kind in message and node in message for message in messages)

        try:
            with (scratch / "panel.log").open("wb") as log:
                def start():
                    nonlocal server
                    env = {**os.environ, "HOME": str(scratch), "TZ": "UTC",
                           "HTTP_PROXY": "http://127.0.0.1:9", "HTTPS_PROXY": "http://127.0.0.1:9",
                           "ALL_PROXY": "http://127.0.0.1:9", "NO_PROXY": "127.0.0.1,localhost"}
                    env.pop("NEKOMARI_BACKUP_HEALTH_FILE", None)
                    server = subprocess.Popen([str(binary), "server", "--listen", f"127.0.0.1:{port}"],
                                              cwd=scratch, env=env, stdout=log, stderr=subprocess.STDOUT)

                start()
                wait(lambda: call("/api/install/status")["data"]["required"])
                password = "Test-Aa1-" + secrets.token_urlsafe(24)
                assert call("/api/install/complete", {"username": "offline-test", "password": password,
                    "sitename": "Isolated offline test", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                wait(lambda: call("/api/public")["status"] == "success")
                stop()
                tokens = [secrets.token_hex(32) for _ in range(2)]
                with contextlib.closing(sqlite3.connect(scratch / "data/komari.db")) as db, db:
                    for key in ("geo_ip_enabled", "login_notification"):
                        db.execute("INSERT OR REPLACE INTO configs (key,value) VALUES (?,?)", (key, "false"))
                    for node, token, grace in zip(("window-first", "window-later"), tokens, (90, 10)):
                        db.execute("INSERT INTO clients (uuid,token,name) VALUES (?,?,?)", (node, token, node))
                        db.execute("INSERT INTO offline_notifications (client,enable,grace_period) VALUES (?,1,?)",
                                   (node, grace))
                start()
                wait(lambda: call("/api/public")["status"] == "success")
                assert call("/api/login", {"username": "offline-test", "password": password})["status"] == "success"
                rpc("admin:setMessageSenderProvider", {"name": "telegram", "addition": json.dumps({
                    "endpoint": f"http://127.0.0.1:{stub.server_port}/bot", "bot_token": "TEST", "chat_id": "synthetic"})})
                rpc("admin:editSettings", {"geo_ip_enabled": False, "login_notification": False,
                                           "notification_enabled": True, "notification_method": "telegram"})
                first, later = open_agent(tokens[0]), open_agent(tokens[1])
                time.sleep(1)
                now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
                for name, node, start_offset, end_offset in (
                    ("active at disconnect", "window-first", -10, 8),
                    ("starts during grace", "window-later", 4, 25),
                ):
                    rpc("admin:saveMaintenanceWindow", {"name": name, "clients": [node],
                        "start": (now + dt.timedelta(seconds=start_offset)).isoformat(),
                        "end": (now + dt.timedelta(seconds=end_offset)).isoformat()})
                first.close()
                later.close()
                time.sleep(13)
                assert not received("Offline", "window-later"), "alert sent while the later window was active"
                wait(lambda: received("Offline", "window-later") == 1, seconds=65)
                time.sleep(2)
                assert not received("Offline", "window-first"), "alert sent before the 90-second grace period"
                open_agent(tokens[0])
                open_agent(tokens[1])
                wait(lambda: received("Online", "window-later") == 1, seconds=5)
                time.sleep(1)
                assert not received("Online", "window-first"), "unreported outage produced a recovery"
                assert received("Offline", "window-later") == 1 and received("Online", "window-later") == 1
                version = call("/api/version")["data"]
                stop()
        finally:
            for agent in agents:
                agent.close()
            stop()
            stub.shutdown()
            stub.server_close()
            thread.join(timeout=5)
    print(json.dumps({"status": "passed", "version": version, "synthetic_only": True,
                      "checks": ["window-before-grace", "window-during-grace", "reconnect-and-recovery"],
                      "fake_telegram_deliveries": len(messages), "scratch_removed": True}))


if __name__ == "__main__":
    main()
