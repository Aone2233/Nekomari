#!/usr/bin/env python3
"""Rotate the agent tokens exposed in deploy/enable-webssh-all.sh, then push the
new values to each node's agent unit.

Writes label->token to /tmp/rotated2.txt (mode 600) for the push step; nothing is
printed except a verified/not-verified flag, so a token never reaches a terminal
transcript, a commit message, or a document.

Run from OC424.
"""
import json
import os
import random
import subprocess
import urllib.request

PANEL = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (token-rotate)"
OUT = "/tmp/rotated2.txt"

# uuid -> (label, ssh target, unit path, sudo?)
NODES = {
    "e5a37d87-18a4-4de0-a091-47a226bfb511": ("HUANAYUN", "root@177.2.185.85",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "d67e6b38-7b98-4b18-900e-85f7c820f81e": ("HK04", "root@82.152.161.202",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "600e5169-9937-476c-ad99-8bd2f17c5ce3": ("AKKO", "root@185.218.4.64",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "40c8a6e5-46b0-4821-ad4e-003215daecf0": ("CLOUDLEAD", "root@192.220.32.17",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "4cea3016-9f17-4f46-a6db-504cd76b832c": ("NOMAO", "root@[2604:abc0:50::11:601e]",
                                             "/etc/systemd/system/nekomari-agent.service", False),
}


def call(path, method="GET", body=None, cookie=None):
    req = urllib.request.Request(
        PANEL + path, method=method,
        data=json.dumps(body).encode() if body is not None else None)
    req.add_header("User-Agent", UA)
    if body is not None:
        req.add_header("Content-Type", "application/json")
    if cookie:
        req.add_header("Cookie", cookie)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


token_cookie = "session_token=" + call(
    "/api/login", "POST",
    {"username": "AONE2233", "password": "KOMT0721@aone2233"},
)["data"]["set-cookie"]["session_token"]

CHARS = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
rotated = {}

for uuid, (label, target, unit, needs_sudo) in NODES.items():
    new = "".join(random.choice(CHARS) for _ in range(22))
    # call() json-encodes the body itself, so pass dicts here -- passing
    # json.dumps(...).encode() double-encodes and raises TypeError.
    call("/api/rpc2", "POST", {
        "jsonrpc": "2.0", "method": "admin:editClient",
        "params": {"uuid": uuid, "token": new}, "id": 1},
        cookie=token_cookie)
    back = call("/api/rpc2", "POST", {
        "jsonrpc": "2.0", "method": "admin:getClientToken",
        "params": {"uuid": uuid}, "id": 2},
        cookie=token_cookie)
    ok = (back.get("result") or {}).get("token") == new
    rotated[label] = (new, target, unit, needs_sudo)
    print(f"  {label:<10} rotated verified={ok}")

with open(OUT, "w") as fh:
    os.chmod(OUT, 0o600)
    for label, (tok, target, unit, sudo) in rotated.items():
        fh.write(f"{label}|{target}|{unit}|{'1' if sudo else '0'}|{tok}\n")
print(f"\nwrote {OUT} (mode 600)")
