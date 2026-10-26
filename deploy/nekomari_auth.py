#!/usr/bin/env python3
"""Shared panel authentication for the operational scripts, including 2FA.

Enabling TOTP 2FA on the account breaks every script that logs in with just a
username and password -- they start getting 401 "2FA code is required". This module
exists so that fix lives in one place instead of being re-derived in each script.

The secret is never stored here. Pass it in the environment:

    export NEKOMARI_2FA_SECRET='<base32 secret from enrolment>'

If the account has 2FA disabled, the variable can be omitted and the login proceeds
without a code. If 2FA is enabled and the variable is missing, login fails with a
message that says so, rather than a bare 401.

## Prefer the API key: password login sends the operator a notification

`login()` posts to `/api/login`, and with `login_notification` enabled the panel sends a
Telegram message for **every** one of those. A script that polls or iterates therefore
spams the operator, who has no way to tell those apart from a real sign-in.

Scoped API keys do not go through that endpoint, so they produce no login
notification. Grant only the scopes a script needs (nodes:read for monitoring):

    export NEKOMARI_API_KEY='<one-time token from Settings -> Sign-on -> API>'
    auth = api_key_header()          # {'Authorization': 'Bearer <key>'}
    status, data = panel_call('/api/rpc2', 'POST', {...}, headers=auth)

The database stores hashes, not recoverable keys. There is deliberately no database
fallback: provision NEKOMARI_API_KEY from your secret manager. Old global keys migrate
to nodes:read for 30 days and cannot perform mutations. SQL, settings, backups and
account/key management require a session, never an all-powerful automation key.

Usage from another script in this directory:

    from nekomari_auth import login, panel_call, api_key_header
    headers = api_key_header()                       # no notification
    data = panel_call("/api/rpc2", "POST", {...}, headers=headers)
    cookie = login()                                 # only when a *session* is needed
"""
import base64
import hashlib
import hmac
import json
import os
import struct
import time
import urllib.error
import urllib.request

BASE = os.environ.get("NEKOMARI_PANEL", "http://127.0.0.1:25774")
USER = os.environ.get("NEKOMARI_USER", "AONE2233")
PASSWORD = os.environ.get("NEKOMARI_PASSWORD", "")
UA = "Mozilla/5.0 (nekomari-op)"

def totp(secret, at=None, digits=6, period=30):
    """RFC 6238 TOTP. The server uses pquerna/otp defaults: SHA-1, 6 digits, 30s."""
    pad = "=" * ((8 - len(secret) % 8) % 8)
    key = base64.b32decode(secret.upper().replace(" ", "") + pad)
    counter = int((at if at is not None else time.time()) // period)
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code = struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7FFFFFFF
    return str(code % (10 ** digits)).zfill(digits)


def panel_call(path, method="GET", body=None, cookie=None, base=None, timeout=60, headers=None):
    """One request. Returns (status, parsed-or-raw-body)."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request((base or BASE) + path, method=method, data=data)
    req.add_header("User-Agent", UA)
    if body is not None:
        req.add_header("Content-Type", "application/json")
    if cookie:
        req.add_header("Cookie", cookie)
    for name, value in (headers or {}).items():
        req.add_header(name, value)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw = r.read()
            status = r.status
    except urllib.error.HTTPError as e:
        raw, status = e.read(), e.code
    try:
        return status, json.loads(raw)
    except Exception:
        return status, raw


def api_key_header(key=None, data_dir=None):
    """Headers that authenticate without producing a login notification.

    Returns an empty dict without an explicit key/environment value. data_dir is
    accepted only for source compatibility; credentials cannot be read from SQLite.
    """
    key = key or os.environ.get("NEKOMARI_API_KEY", "").strip()
    if not key:
        return {}
    return {"Authorization": "Bearer " + key}


def login(password=None, base=None, user=None):
    """Log in and return the Cookie header value. Raises with a clear message.

    **This produces a login notification** when the panel has `login_notification`
    enabled -- one message per call. Prefer `api_key_header()`; a session is only needed
    for something that genuinely requires a browser session, such as driving the admin UI.
    """
    pw = password or PASSWORD
    if not pw:
        raise SystemExit(
            "no password: set NEKOMARI_PASSWORD (and NEKOMARI_2FA_SECRET if 2FA is on), "
            "or use api_key_header() to avoid a login notification entirely")

    body = {"username": user or USER, "password": pw}
    secret = os.environ.get("NEKOMARI_2FA_SECRET", "").strip()
    if secret:
        body["2fa_code"] = totp(secret)

    status, resp = panel_call("/api/login", "POST", body, base=base)
    if status != 200:
        msg = resp.get("message") if isinstance(resp, dict) else str(resp)[:120]
        hint = ""
        if isinstance(msg, str) and "2FA" in msg:
            hint = ("  <- the account has 2FA enabled; export NEKOMARI_2FA_SECRET "
                    "with the base32 secret from enrolment")
        raise SystemExit(f"login failed ({status}): {msg}{hint}")

    token = (((resp or {}).get("data") or {}).get("set-cookie") or {}).get("session_token")
    if not token:
        raise SystemExit("login succeeded but no session_token was returned")
    return "session_token=" + token
