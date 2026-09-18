#!/usr/bin/env python3
"""Enable TOTP 2FA on the admin account, and print what the user needs to enrol.

The flow is a two-step handshake: /api/admin/2fa/generate returns a QR image and
puts the secret in a short-lived cookie, then /api/admin/2fa/enable?code=...
validates a code against *that cookie* before persisting it. So the code has to be
computed from the cookie, not from anything in a response body.

The secret is printed so the account owner can add it to their authenticator. It is
also the only way back in if the enrolment is lost, short of the disable-2fa CLI.

Run on OC424.
"""
import base64
import hashlib
import hmac
import json
import struct
import sys
import time
import urllib.request

B = "http://127.0.0.1:25774"
UA = "Mozilla/5.0 (enable-2fa)"
USER = "AONE2233"
PASS = "KOMT0721@aone2233"


def totp(secret, at=None, digits=6, period=30):
    """RFC 6238 TOTP. pquerna/otp defaults to SHA-1, 6 digits, 30s."""
    pad = "=" * ((8 - len(secret) % 8) % 8)
    key = base64.b32decode(secret.upper() + pad)
    counter = int((at if at is not None else time.time()) // period)
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code = struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7FFFFFFF
    return str(code % (10 ** digits)).zfill(digits)


class Session:
    def __init__(self):
        self.cookies = {}

    def header(self):
        return "; ".join(f"{k}={v}" for k, v in self.cookies.items())

    def absorb(self, response):
        for raw in response.headers.get_all("Set-Cookie") or []:
            pair = raw.split(";", 1)[0]
            if "=" in pair:
                k, v = pair.split("=", 1)
                if v == "" or "Max-Age=0" in raw:
                    self.cookies.pop(k.strip(), None)
                else:
                    self.cookies[k.strip()] = v

    def call(self, path, method="GET", body=None, ctype=None):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(B + path, method=method, data=data)
        req.add_header("User-Agent", UA)
        if self.header():
            req.add_header("Cookie", self.header())
        if ctype:
            req.add_header("Content-Type", ctype)
        elif body is not None:
            req.add_header("Content-Type", "application/json")
        with urllib.request.urlopen(req, timeout=30) as r:
            self.absorb(r)
            return r.status, r.read()


s = Session()
st, _ = s.call("/api/login", "POST", {"username": USER, "password": PASS})
print(f"  login: {st}, cookies={list(s.cookies)}")
if "session_token" not in s.cookies:
    sys.exit("  login failed")

# Step 1: generate. The response is a PNG; the secret arrives in a cookie.
st, png = s.call("/api/admin/2fa/generate")
secret = s.cookies.get("2fa_secret", "")
print(f"  generate: {st}, {len(png)} bytes of PNG, secret in cookie: {bool(secret)}")
if not secret:
    sys.exit("  no 2fa_secret cookie; cannot continue")

# Step 2: prove possession of the secret, then persist it.
code = totp(secret)
st, body = s.call(f"/api/admin/2fa/enable?code={code}", "POST", {})
print(f"  enable: {st} {body[:120].decode(errors='replace')}")

label = urllib.parse.quote(f"Nekomari:{USER}") if False else "Nekomari:" + USER
uri = (f"otpauth://totp/{label}?secret={secret}"
       f"&issuer=Nekomari&algorithm=SHA1&digits=6&period=30")

print()
print("  ================ 请立刻保存 ================")
print(f"  账户      : {USER}")
print(f"  密钥      : {secret}")
print(f"  otpauth   : {uri}")
print(f"  当前验证码 : {totp(secret)}   （30 秒后变为 {totp(secret, time.time() + 30)}）")
print("  ==========================================")
print()
print("  在认证器 App 里选「手动输入密钥」，填上面的密钥，类型选基于时间(TOTP)。")
print("  若需撤销：docker compose run --rm --entrypoint /app/nekomari nekomari disable-2fa")
