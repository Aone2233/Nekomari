#!/usr/bin/env python3
"""Upload a theme/plugin archive to Nekomari through its chunked upload API.

The theme install path is not a single POST: web/router/router.go mounts
/api/admin/upload/{init,chunk,merge}, and each of those is driven by a session
id. This script does the three steps in order so the server's own
extract-and-validate path runs, rather than writing into the data volume by
hand (which would skip validation and any bookkeeping).

Usage: upload_theme.py <base-url> <cookie> <zip-path> [purpose]
"""
import json
import sys
import urllib.request
import uuid

BASE = sys.argv[1].rstrip("/")
COOKIE = sys.argv[2]
ZIP = sys.argv[3]
PURPOSE = sys.argv[4] if len(sys.argv) > 4 else "theme"


def call(path, payload=None):
    url = BASE + path
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Cookie", COOKIE)
    if data:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.load(resp)


def post_multipart(path, fields, files):
    boundary = "----nk" + uuid.uuid4().hex
    body = b""
    for name, value in fields.items():
        body += (
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"\r\n\r\n{value}\r\n"
        ).encode()
    for name, (filename, content) in files.items():
        body += (
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"; "
            f"filename=\"{filename}\"\r\nContent-Type: application/octet-stream\r\n\r\n"
        ).encode()
        body += content + b"\r\n"
    body += f"--{boundary}--\r\n".encode()

    req = urllib.request.Request(BASE + path, data=body, method="POST")
    req.add_header("Cookie", COOKIE)
    req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    with urllib.request.urlopen(req, timeout=300) as resp:
        return json.load(resp)


blob = open(ZIP, "rb").read()
print(f"archive: {ZIP} ({len(blob)} bytes), purpose={PURPOSE}")

init = call("/api/admin/upload/init", {
    "purpose": PURPOSE,
    "size": len(blob),
    "filename": ZIP.rsplit("/", 1)[-1],
})
print("init  ->", init.get("data"))
upload_id = init["data"]["upload_id"]
chunk_size = init["data"].get("chunk_size") or 4 * 1024 * 1024

total = (len(blob) + chunk_size - 1) // chunk_size
for i in range(total):
    part = blob[i * chunk_size:(i + 1) * chunk_size]
    res = post_multipart("/api/admin/upload/chunk",
                         {"upload_id": upload_id, "chunk_index": str(i)},
                         {"chunk_data": ("chunk", part)})
    print(f"chunk {i + 1}/{total} -> {res.get('status')}")

merged = call("/api/admin/upload/merge", {"upload_id": upload_id})
print("merge ->", json.dumps(merged, ensure_ascii=False)[:400])
