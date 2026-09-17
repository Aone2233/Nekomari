#!/usr/bin/env python3
"""Rotate the agent token for every node, then write a push map.

Writes label|ssh-target|unit-path|needs-sudo|token to /tmp/rotate-all.txt
(mode 600) for the push step. Nothing sensitive is printed: only a verified flag
per node, so a token never reaches a terminal transcript, a commit, or a document.

Run from OC424.
"""
import json
import os
import random
import urllib.request

PANEL = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (rotate-all)"
OUT = "/tmp/rotate-all.txt"

# uuid -> (label, ssh target, unit path, needs sudo)
NODES = {
    "ad0c4739-9823-4f78-9017-005094b047c2": ("OC424", "LOCAL",
                                             "/etc/systemd/system/komari-agent-oc424-original-node.service", True),
    "e5a37d87-18a4-4de0-a091-47a226bfb511": ("HUANAYUN", "root@177.2.185.85",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "d67e6b38-7b98-4b18-900e-85f7c820f81e": ("HK04", "root@82.152.161.202",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "600e5169-9937-476c-ad99-8bd2f17c5ce3": ("AKKO", "root@185.218.4.64",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "40c8a6e5-46b0-4821-ad4e-003215daecf0": ("CLOUDLEAD", "root@192.220.32.17",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "41b1f6f8-ee8c-4dd9-8d43-2435af31b719": ("BWG", "root@144.34.224.86",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "4cea3016-9f17-4f46-a6db-504cd76b832c": ("NOMAO", "root@[2604:abc0:50::11:601e]",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "646e7117-b7fe-48d7-8bc1-9aef55fcf90e": ("MACWAN", "MAC-WAN-USER",
                                             "~/.config/systemd/user/nekomari-agent.service", False),
    "92d91929-772c-4a9f-b9bd-8a6f8c5a69f9": ("PZYC", "PZYC-SUDO",
                                             "/etc/systemd/system/nekomari-agent.service", True),
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


ck = "session_token=" + call("/api/login", "POST",
                             {"username": "AONE2233", "password": "KOMT0721@aone2233"},
                             )["data"]["set-cookie"]["session_token"]

CHARS = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
rows = []
for uuid, (label, target, unit, sudo) in NODES.items():
    tok = "".join(random.choice(CHARS) for _ in range(22))
    call("/api/rpc2", "POST", {"jsonrpc": "2.0", "method": "admin:editClient",
                              "params": {"uuid": uuid, "token": tok}, "id": 1}, cookie=ck)
    back = call("/api/rpc2", "POST", {"jsonrpc": "2.0", "method": "admin:getClientToken",
                                      "params": {"uuid": uuid}, "id": 2}, cookie=ck)
    ok = (back.get("result") or {}).get("token") == tok
    rows.append(f"{label}|{target}|{unit}|{'1' if sudo else '0'}|{tok}")
    print(f"  {label:<10} rotated verified={ok}")

with open(OUT, "w") as fh:
    os.chmod(OUT, 0o600)
    fh.write("\n".join(rows) + "\n")
print(f"\nwrote {OUT} (mode 600), {len(rows)} nodes")
