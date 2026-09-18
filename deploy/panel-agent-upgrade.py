#!/usr/bin/env python3
"""Upgrade a node's agent through the panel's remote-exec, for hosts with no SSH.

CloudLeadInno's key is not installed here and its SSH refuses both keys we hold, but
the node runs the agent with web-ssh enabled, so the panel can execute a command on it.
That is the same channel the panel's own "execute command" button uses (admin:exec ->
agent.exec -> agent.taskResult).

It then waits for the node to report an unlock snapshot, which is the real proof the
new binary is running -- not the exit code of the command that replaced it.

Usage: panel-agent-upgrade.py <node-uuid> <version>
Run on OC424 (uses the loopback panel and the admin login).
"""
import base64
import hashlib
import hmac
import json
import os
import struct
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:25774"
UUID = sys.argv[1]
VERSION = sys.argv[2] if len(sys.argv) > 2 else "v0.1.9"


def totp(secret):
    pad = "=" * ((8 - len(secret) % 8) % 8)
    key = base64.b32decode(secret.upper() + pad)
    digest = hmac.new(key, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code = struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7FFFFFFF
    return str(code % 1000000).zfill(6)


def call(path, method="GET", body=None, cookie=None, timeout=120, twofa=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, method=method, data=data)
    req.add_header("User-Agent", "Mozilla/5.0 (nekomari-op)")
    if body is not None:
        req.add_header("Content-Type", "application/json")
    if cookie:
        req.add_header("Cookie", cookie)
    if twofa:
        # 敏感操作（admin:exec 这类）在会话之外还要一次 2FA，服务端接受 X-2FA-Code 头。
        # 登录时那次不算：它证明的是「谁登进来了」，这次证明的是「现在还是本人」。
        req.add_header("X-2FA-Code", twofa)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            raw, status = response.read(), response.status
    except urllib.error.HTTPError as exc:
        raw, status = exc.read(), exc.code
    try:
        return status, json.loads(raw)
    except Exception:
        return status, raw


def rpc(cookie, method, params, twofa=None):
    status, body = call("/api/rpc2", "POST",
                        {"jsonrpc": "2.0", "id": 1, "method": method, "params": params},
                        cookie, twofa=twofa)
    if body.get("error"):
        raise SystemExit(f"{method} failed: {body['error']}")
    return body.get("result")


# 单行命令：下载、校验、备份、替换、重启。用 && 串起来，任何一步失败都不会去重启。
# 二进制路径从正在跑的进程取，不猜目录。
COMMAND = (
    "set -e; "
    "A=amd64; cd /tmp; "
    f"curl -fsSL -o agent-new https://github.com/Aone2233/Nekomari/releases/download/{VERSION}/komari-agent-linux-$A; "
    f"curl -fsSL -o sums https://github.com/Aone2233/Nekomari/releases/download/{VERSION}/SHA256SUMS.txt; "
    "grep \" komari-agent-linux-$A$\" sums | sed \"s#komari-agent-linux-$A#agent-new#\" | sha256sum -c -; "
    "chmod +x agent-new; "
    "BIN=$(ps -eo args | grep '[k]omari-agent' | head -1 | awk '{print $1}'); "
    f"cp -p \"$BIN\" \"$BIN.bak-pre-{VERSION}\"; "
    "install -m 0755 agent-new \"$BIN\"; "
    "systemctl restart nekomari-agent 2>/dev/null || systemctl restart komari-agent; "
    "sleep 4; systemctl is-active nekomari-agent 2>/dev/null || systemctl is-active komari-agent"
)

status, response = call("/api/login", "POST", {
    "username": os.environ["NEKOMARI_USER"],
    "password": os.environ["NEKOMARI_PASSWORD"],
    "2fa_code": totp(os.environ["NEKOMARI_2FA_SECRET"]),
})
if status != 200:
    raise SystemExit(f"login failed ({status}): {response}")
cookie = "session_token=" + response["data"]["set-cookie"]["session_token"]

print(f"dispatching upgrade to {UUID} ...")
result = rpc(cookie, "admin:exec", {"command": COMMAND, "clients": [UUID]},
             twofa=totp(os.environ["NEKOMARI_2FA_SECRET"]))
task_id = result.get("task_id")
print(f"  task_id       : {task_id}")
print(f"  online        : {result.get('online_clients')}")
print(f"  queued        : {result.get('queued_clients')}")
print(f"  offline       : {result.get('offline_clients')}")

# 等节点回报命令结果。轮询 tasks 接口。
deadline = time.time() + 240
seen = None
while time.time() < deadline:
    time.sleep(8)
    try:
        results = rpc(cookie, "admin:getTaskResults", {"task_id": task_id})
    except SystemExit:
        results = None
    if not results:
        continue
    entries = results if isinstance(results, list) else results.get("results") or []
    if entries:
        seen = entries
        break

if seen:
    for entry in seen:
        output = str(entry.get("result", "")).strip()
        print(f"  exit={entry.get('exit_code')} output={output[:200]!r}")
else:
    print("  (没有拿到命令回执，继续按解锁上报判断)")

# 真正的证据：节点开始上报解锁快照，说明新二进制在跑。
print("\nwaiting for the node to report an unlock snapshot ...")
for _ in range(30):
    time.sleep(10)
    status, body = call(f"/api/public/ip-info/v1/lookup?uuid={UUID}&ip=192.220.32.17")
    unlock = (body.get("data") or {}).get("unlock") if isinstance(body, dict) else None
    if unlock:
        print(f"  egress={unlock.get('egress_ip')}/{unlock.get('egress_region')} "
              f"probed_at={unlock.get('probed_at')}")
        for item in unlock.get("results", []):
            print(f"    {item['name']:<18} {item['status']}")
        raise SystemExit(0)
print("  ✗ 仍未上报解锁快照")
raise SystemExit(1)
