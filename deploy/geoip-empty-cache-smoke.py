#!/usr/bin/env python3
"""Test GeoIP caching through an isolated Panel and a local fake HTTPS proxy."""

import argparse
from collections import Counter
from http.cookiejar import CookieJar
import json
import os
from pathlib import Path
import secrets
import socket
import socketserver
import ssl
import subprocess
import tempfile
import threading
import time
from urllib.error import URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener


class FakeIPInfo(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self, context):
        self.context = context
        self.counts = Counter()
        self.lock = threading.Lock()
        super().__init__(("127.0.0.1", 0), ProxyHandlerForIPInfo)


class ProxyHandlerForIPInfo(socketserver.StreamRequestHandler):
    def handle(self):
        self.connection.settimeout(5)
        if self.rfile.readline(4096) != b"CONNECT ipinfo.io:443 HTTP/1.1\r\n":
            return
        while self.rfile.readline(4096) not in (b"\r\n", b""):
            pass
        self.wfile.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        self.wfile.flush()
        with self.server.context.wrap_socket(self.connection, server_side=True) as connection:
            with connection.makefile("rb") as stream:
                request = stream.readline(4096).decode("ascii").split()
                while stream.readline(4096) not in (b"\r\n", b""):
                    pass
            if len(request) != 3 or request[0] != "GET" or request[1] not in (
                "/192.0.2.1/json", "/192.0.2.2/json"
            ):
                return
            path = request[1]
            with self.server.lock:
                self.server.counts[path] += 1
                count = self.server.counts[path]
            body = b"{}" if path == "/192.0.2.1/json" and count == 1 else (
                b'{"country":"SG"}' if path == "/192.0.2.1/json" else b'{"country":"US"}'
            )
            connection.sendall(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n"
                               + f"Content-Length: {len(body)}\r\n".encode()
                               + b"Connection: close\r\n\r\n" + body)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", required=True)
    args = parser.parse_args()
    binary = str(Path(args.panel_binary).resolve(strict=True))
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix="nekomari-geoip-empty-cache-") as temp:
        scratch = Path(temp)
        key, cert = scratch / "key.pem", scratch / "cert.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                        "-keyout", str(key), "-out", str(cert), "-days", "1",
                        "-subj", "/CN=ipinfo.io", "-addext", "subjectAltName=DNS:ipinfo.io",
                        "-addext", "basicConstraints=critical,CA:TRUE"],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        proxy = FakeIPInfo(context)
        worker = threading.Thread(target=proxy.serve_forever, daemon=True)
        worker.start()
        try:
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", 0))
                port = sock.getsockname()[1]
            opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
            base = f"http://127.0.0.1:{port}"
            log_path = scratch / "panel.log"
            env = dict(os.environ, HTTPS_PROXY=f"http://127.0.0.1:{proxy.server_address[1]}",
                       HTTP_PROXY=f"http://127.0.0.1:{proxy.server_address[1]}",
                       NO_PROXY="127.0.0.1,localhost", SSL_CERT_FILE=str(cert))

            def call(path, body=None):
                request = Request(base + path, data=json.dumps(body).encode() if body is not None else None,
                                  headers={"Content-Type": "application/json"})
                with opener.open(request, timeout=5) as response:
                    return json.load(response)

            def wait(predicate):
                deadline = time.monotonic() + 30
                while time.monotonic() < deadline:
                    if server.poll() is not None:
                        raise RuntimeError("isolated Panel exited")
                    try:
                        if predicate():
                            return
                    except (URLError, TimeoutError):
                        pass
                    time.sleep(0.05)
                raise RuntimeError("isolated Panel readiness/reload timed out")

            def rpc(method, params):
                result = call("/api/rpc2", {"jsonrpc": "2.0", "id": 1,
                                            "method": method, "params": params})
                if "error" in result:
                    raise RuntimeError(f"synthetic RPC failed: {method}: {result['error']}")
                return result.get("result")

            def counts():
                with proxy.lock:
                    return dict(proxy.counts)

            with log_path.open("wb") as log:
                server = subprocess.Popen([binary, "server", "--listen", f"127.0.0.1:{port}"],
                                          cwd=scratch, stdout=log, stderr=subprocess.STDOUT, env=env)
                try:
                    wait(lambda: call("/api/install/status")["data"]["required"])
                    password = "Test-Aa1-" + secrets.token_urlsafe(24)
                    assert call("/api/install/complete", {"username": "geoip-test", "password": password,
                        "sitename": "Isolated GeoIP test", "metric_dsn": "./data/metrics.db"})["status"] == "success"
                    wait(lambda: call("/api/public")["status"] == "success")
                    assert call("/api/login", {"username": "geoip-test", "password": password})["status"] == "success"
                    wait(lambda: "using GeoIP provider" in log_path.read_text(errors="replace"))
                    result = rpc("admin:testGeoip", {"ip": "192.0.2.1"})
                    assert result == {"ISOCode": "", "Name": ""} and counts() == {"/192.0.2.1/json": 1}
                    assert rpc("admin:testGeoip", {"ip": "192.0.2.1"}) == result
                    assert counts() == {"/192.0.2.1/json": 1}
                    positive = rpc("admin:testGeoip", {"ip": "192.0.2.2"})
                    assert positive == {"ISOCode": "US", "Name": "US"}
                    assert rpc("admin:testGeoip", {"ip": "192.0.2.2"}) == positive
                    assert counts() == {"/192.0.2.1/json": 1, "/192.0.2.2/json": 1}
                    offset = log_path.stat().st_size
                    rpc("admin:editSettings", {"geo_ip_provider": "empty"})
                    wait(lambda: "using GeoIP provider" in log_path.read_text(errors="replace")[offset:])
                    offset = log_path.stat().st_size
                    rpc("admin:editSettings", {"geo_ip_provider": "ipinfo"})
                    wait(lambda: "using GeoIP provider" in log_path.read_text(errors="replace")[offset:])
                    assert rpc("admin:testGeoip", {"ip": "192.0.2.1"}) == {"ISOCode": "SG", "Name": "SG"}
                    assert counts() == {"/192.0.2.1/json": 2, "/192.0.2.2/json": 1}
                finally:
                    server.terminate()
                    try:
                        server.wait(timeout=15)
                    except subprocess.TimeoutExpired:
                        server.kill()
                        server.wait(timeout=5)
        finally:
            proxy.shutdown()
            proxy.server_close()
            worker.join(timeout=5)
    print(json.dumps({"status": "passed", "empty_and_positive_cached": True,
                      "reload_allows_recovery": True, "ttl_seconds_tested_in_unit_suite": True,
                      "upstream": "local fake HTTPS proxy", "production_changed": False,
                      "scratch_removed": True}))


if __name__ == "__main__":
    main()
