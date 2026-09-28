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

The panel's API key does not go through that endpoint at all, so it produces no
notification. Use it for anything programmatic:

    export NEKOMARI_API_KEY='<from the panel: Settings -> API key>'
    auth = api_key_header()          # {'Authorization': 'Bearer <key>'}
    status, data = panel_call('/api/rpc2', 'POST', {...}, headers=auth)

If the variable is unset, `api_key_header()` reads the key from the panel's own
database, which is what the operational scripts on this host can do because they already
run there.

## Why the panel's own config needs unquoting

The `configs` table stores values **JSON-encoded**, so `api_key` is `"abc…"` with the
quotes included -- 42 bytes for a 40-byte key. Passing the stored value straight into the
header produces a 401 that looks like a wrong key rather than a quoting mistake, which is
exactly how it was found. Everything read from `configs` goes through `json.loads` here.

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
import shutil
import struct
import subprocess
import time
import urllib.error
import urllib.request

BASE = os.environ.get("NEKOMARI_PANEL", "http://127.0.0.1:25774")
USER = os.environ.get("NEKOMARI_USER", "AONE2233")
PASSWORD = os.environ.get("NEKOMARI_PASSWORD", "")
UA = "Mozilla/5.0 (nekomari-op)"

# Where the panel keeps its database on the host these scripts run on.
DATA_DIR = os.environ.get("NEKOMARI_DATA_DIR", "/opt/nekomari/data")


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


def read_api_key_from_database(data_dir=None):
    """Read the panel's API key from its own config table.

    `configs.value` is JSON-encoded, so the stored text includes the surrounding quotes.
    Returning it verbatim would send `Bearer "abc…"`, which the panel rejects as an
    invalid key -- a 401 that reads like a wrong key rather than a quoting mistake.
    """
    database = os.path.join(data_dir or DATA_DIR, "komari.db")
    if not os.path.exists(database):
        return None
    # `sqlite3` on the host may not be able to open the file without elevation, and the
    # scripts this serves run as the user that owns it; fall back to the CLI when the
    # module is unavailable rather than failing the whole call.
    try:
        import sqlite3

        connection = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
        row = connection.execute("select value from configs where key='api_key'").fetchone()
        connection.close()
        raw = row[0] if row else None
    except Exception:
        if not shutil.which("sqlite3"):
            return None
        try:
            raw = subprocess.run(
                ["sqlite3", f"file:{database}?mode=ro", "select value from configs where key='api_key'"],
                capture_output=True, text=True, timeout=20, check=True,
            ).stdout.strip() or None
        except Exception:
            return None
    if not raw:
        return None
    try:
        value = json.loads(raw)
    except Exception:
        value = raw
    if isinstance(value, str):
        value = value.strip()
    return value or None


def api_key_header(key=None, data_dir=None):
    """Headers that authenticate without producing a login notification.

    Returns an empty dict when no key can be found, so callers can fall back to
    `login()` explicitly instead of sending a request that 401s.
    """
    key = key or os.environ.get("NEKOMARI_API_KEY", "").strip() or read_api_key_from_database(data_dir)
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
