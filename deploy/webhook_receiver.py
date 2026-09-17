#!/usr/bin/env python3
"""Deploy-verification webhook receiver.

Records every request it receives to verify, with an artifact, that Nekomari's
notifier actually dispatched an alert. Binds a plain HTTP server on the LAN so
the Nekomari container (host network) can reach it.

Usage: webhook_receiver.py <port> <logfile>
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def _record(self, method):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        entry = {
            "received_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
            "remote": self.client_address[0],
            "method": method,
            "path": self.path,
            "headers": {k: v for k, v in self.headers.items()},
            "raw_body": raw.decode("utf-8", "replace"),
        }
        with open(self.server.logfile, "a", encoding="utf-8") as fh:
            fh.write(json.dumps(entry, ensure_ascii=False) + "\n")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def do_POST(self):
        self._record("POST")

    def do_GET(self):
        self._record("GET")

    def log_message(self, fmt, *args):
        # Keep stdout clean; the JSONL file is the artifact.
        pass


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 25999
    logfile = sys.argv[2] if len(sys.argv) > 2 else "/tmp/webhook_receiver.jsonl"
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    server.logfile = logfile
    print(f"webhook receiver listening on 0.0.0.0:{port} -> {logfile}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
