#!/usr/bin/env python3
"""Rotate the agent token for every node, then write a push map.

Writes label|ssh-target|unit-path|needs-sudo|token to /tmp/rotate-all.txt
(mode 600) for the push step. Nothing sensitive is printed: only a verified flag
per node, so a token never reaches a terminal transcript, a commit, or a document.

Run from OC424. The panel login comes from the environment, never from this file:

    sudo -n env NEKOMARI_PASSWORD='<panel password>' \
                NEKOMARI_2FA_SECRET='<base32 secret, if 2FA is on>' \
                python3 deploy/rotate-all-tokens.py

This script used to carry the admin username and password literally, in a public
repository. That is `docs/SECRETS.md`'s rule broken in the tool that exists to
rotate credentials, and it is incident 4 in that file: the password has to be
changed, not merely deleted here, because deleting it does not un-publish it.
"""
import json
import os
import random
import sys
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
    "646e7117-b7fe-48d7-8bc1-9aef55fcf90e": ("MACWAN", "MAC-WAN-USER",
                                             "~/.config/systemd/user/nekomari-agent.service", False),
    "72a64984-8d1f-4b01-a2b9-9890b9c07005": ("NOSLA", "NOSLA",
                                             "/etc/systemd/system/nekomari-agent.service", False),
    "92d91929-772c-4a9f-b9bd-8a6f8c5a69f9": ("PZYC", "PZYC-SUDO",
                                             "/etc/systemd/system/nekomari-agent.service", True),
    "addcf4a6-24cc-475e-865e-3088777eaa85": ("JPKD2", "JPKD2-OPENRC",
                                             "/etc/conf.d/nekomari-agent", True),
}
# UUIDs that no longer exist in the panel, checked against `clients` on
# 2026-09-26. A rotation must not recreate one of these: the token would be
# written for a node that cannot use it, and the UUID would look live again.
RETIRED = {
    "4cea3016-9f17-4f46-a6db-504cd76b832c": "NOMAO (checked 2026-09-26: no such client)",
}


def panel_user() -> str:
    user = os.environ.get("NEKOMARI_USER", "").strip()
    if not user:
        sys.exit("set NEKOMARI_USER (the panel admin username); "
                 "it is deliberately not stored in this file")
    return user


def panel_password() -> str:
    password = os.environ.get("NEKOMARI_PASSWORD", "")
    if not password:
        sys.exit("set NEKOMARI_PASSWORD to the panel admin password; "
                 "it is deliberately not stored in this file")
    return password


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


# 2FA, if the account has it on, is the difference between a 401 and a rotation.
# deploy/nekomari_auth.py already implements the TOTP half and takes the same
# environment variables, so reuse it rather than duplicating RFC 6238 here.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import nekomari_auth  # noqa: E402

ck = nekomari_auth.login(password=panel_password(), user=panel_user(), base=PANEL)

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
