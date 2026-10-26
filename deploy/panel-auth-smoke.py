#!/usr/bin/env python3
"""Authenticated smoke test for a *deployed* Nekomari panel.

deploy-verify.sh and deploy/panel-probe.sh answer "is the panel up, and does it serve its own
documents". Neither can answer the two questions below, because one needs a credential and the
other needs the assets a browser would fetch:

  1. does the admin API answer someone holding the panel's **API key**?
     (POST /api/rpc2, method admin:getDatabaseSize)
  2. is the panel actually **serving a theme** -- does its document reference build assets that
     really load?

Usage:

    export KOMARI_BASE_URL='https://panel.example.com'
    export KOMARI_API_KEY='<one-time nodes:read token from Settings -> Sign-on -> API>'
    python3 deploy/panel-auth-smoke.py

Why the API key and not a password: the key needs no login, no session cookie and no 2FA code,
and it does not send a login notification on every run (deploy/nekomari_auth.py documents that
in detail). Use nodes:read only; no write/exec/token-read grant is needed. Provision
the token explicitly through a secret manager, never by recovering it from SQLite.

Missing either variable prints SKIP and exits 0, so this can be called unconditionally from
deploy-verify.sh or a CI step: a deployment check that was never asked to authenticate must not
fail for the absence of a credential.

Exit codes: 0 passed or skipped, 1 at least one check failed.

What a FAIL means, by check name:

  POST /api/rpc2 admin:getDatabaseSize
      The API key was rejected, the method is not reachable, or the body was not a success.
      /api/rpc2 answers a rejected request with HTTP 200 and an error member (a JSON-RPC 2.0
      transport does not use HTTP status for application errors), so the *body* is judged --
      a status-code-only check would report a healthy panel while every admin call failed.

  GET / serves an HTML document that references the theme's build
      The panel did not return an HTML document, or the document references no same-origin
      JavaScript or stylesheet. A shell that renders but names nothing is either not the
      panel's document at all or a theme that failed to be served. (The asset may live under
      /assets/, /themes/<short>/, or the site root: installed themes differ.)

  the theme's build asset loads (path)
      The document named an asset that does not load, or that came back as a document. In a
      browser that is a blank page and a MIME error in the console, with nothing in the
      server log: the response was 200, just not the file.
"""
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from html.parser import HTMLParser

USER_AGENT = "nekomari-deploy-verify"
TIMEOUT_SECONDS = 20
RPC_PATH = "/api/rpc2"
RPC_METHOD = "admin:getDatabaseSize"
HTML_TYPE = "text/html"

# What counts as a theme build asset: a same-origin JavaScript or stylesheet the document names.
#
# This used to require an /assets/ or /themes/ prefix, which is true for the embedded default
# panel but false for installed themes that ship their own layout at the site root -- the CRT
# theme referenced /app.js and /style.css, so a production run of this check failed while the
# theme was loading perfectly. The extension is the property that matters (a browser fetches it
# as code or a stylesheet); the directory is a convention one theme may not follow.
THEME_ASSET_SUFFIXES = (".js", ".css")


def is_theme_build_asset(url):
    """Whether a document reference is a same-origin script or stylesheet."""
    cleaned = (url or "").strip()
    if not cleaned or cleaned.startswith(("http://", "https://", "//", "data:")):
        return False
    path = cleaned.split("?", 1)[0].split("#", 1)[0]
    return path.lower().endswith(THEME_ASSET_SUFFIXES)


class CheckFailure(Exception):
    """A request could not be made at all (DNS, refused connection, timeout)."""


def json_body(raw):
    """Decode a response body as JSON. Returns (value, None) or (None, message)."""
    try:
        return json.loads(raw.decode("utf-8", "replace")), None
    except Exception as exc:
        snippet = " ".join(raw[:160].decode("utf-8", "replace").split())
        return None, "the body is not JSON (%s): %s" % (exc, snippet)


def human_bytes(count):
    """Render a byte count as B/KiB/MiB/GiB, one decimal above bytes."""
    try:
        value = float(count)
    except (TypeError, ValueError):
        return str(count)
    for unit in ("B", "KiB", "MiB", "GiB"):
        if value < 1024:
            if unit == "B":
                return "%d B" % int(value)
            return "%.1f %s" % (value, unit)
        value = value / 1024
    return "%.1f TiB" % value


def check_rpc_result(payload):
    """Judge a /api/rpc2 response body as success or failure.

    /api/rpc2 is a JSON-RPC 2.0 transport: success is {"jsonrpc","result","id"} and failure is
    {"jsonrpc","error":{"code","message"},"id"}, and **both arrive with HTTP 200**. That is why
    this judges the body instead of the status code -- a rejected API key looks exactly like a
    successful call from the status line alone.

    A status member, when one is present at either level, must read "success": some methods
    answer {"status": "..."} themselves, and a deployment behind a wrapper may add the REST
    envelope's field. Its absence is not an error -- JSON-RPC does not have one.

    Returns (ok, detail), where detail is the failure reason.
    """
    if not isinstance(payload, dict):
        return False, "the response is not a JSON object"
    error = payload.get("error")
    if isinstance(error, dict):
        return False, "JSON-RPC error %s: %s" % (error.get("code", "?"), error.get("message") or "no message")
    status = payload.get("status")
    if "result" not in payload:
        # A REST-style wrapper ({"status":"success","message":...,"data":...}) is not what
        # /api/rpc2 itself answers, but a deployment behind one is still a passing panel --
        # the same envelope deploy-verify.sh and panel-probe.sh judge elsewhere.
        if status == "success" and "data" in payload:
            return True, ""
        if status is not None:
            message = payload.get("message") or ""
            return False, "status is %r and there is no result%s" % (status, ": " + message if message else "")
        return False, "the response carries neither result nor error"
    if status is not None and status != "success":
        return False, "status is %r, not 'success'" % (status,)
    result = payload.get("result")
    if isinstance(result, dict):
        nested = result.get("status")
        if nested is not None and nested != "success":
            return False, "result.status is %r, not 'success'" % (nested,)
    return True, ""


def rpc_result_value(payload):
    """The payload of a successful body, from either envelope shape (JSON-RPC or REST)."""
    if not isinstance(payload, dict):
        return None
    if "result" in payload:
        return payload["result"]
    return payload.get("data")


def database_size_fields(result):
    """The storage sections a getDatabaseSize result carries, as [(name, bytes)]."""
    if not isinstance(result, dict):
        return []
    found = []
    for name in ("main", "monitoring"):
        section = result.get(name)
        if isinstance(section, dict) and isinstance(section.get("size"), (int, float)):
            found.append((name, section["size"]))
    if not found and isinstance(result.get("size"), (int, float)):
        # The original contract was {type, size} before the monitoring database existed.
        found.append(("size", result["size"]))
    return found


def check_database_size(result):
    """Require the admin method to answer with usable data, not merely with success.

    A success envelope carrying an empty result would satisfy a status-only check while saying
    nothing about whether the database is reachable. The method always reports sizes, so their
    absence is a real failure.
    """
    fields = database_size_fields(result)
    if not fields:
        return False, "the result carries no database size (expected size, main or monitoring)"
    return True, ", ".join("%s %s" % (name, human_bytes(value)) for name, value in fields)


class _ThemeAssetParser(HTMLParser):
    """Collect the build references a theme document names.

    html.parser rather than a regular expression: attribute order, quoting and the tag mix
    (module scripts, modulepreload links, stylesheets) differ per build, and a pattern that
    drifts from them would silently find nothing -- which is the very failure this check exists
    to report, so it must not be able to report it for the wrong reason.
    """

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.assets = []

    def handle_starttag(self, tag, attrs):
        if tag not in ("script", "link"):
            return
        attributes = dict(attrs)
        url = attributes.get("src") if tag == "script" else attributes.get("href")
        if not url:
            return
        cleaned = url.strip()
        if cleaned.startswith("./"):
            cleaned = cleaned[2:]
        if is_theme_build_asset(cleaned):
            self.assets.append(cleaned)


def extract_theme_assets(document):
    """The theme build references a document names, in document order."""
    parser = _ThemeAssetParser()
    try:
        parser.feed(document or "")
        parser.close()
    except Exception:
        return parser.assets
    return parser.assets


def theme_asset_path(document):
    """The first theme build reference in document, or an empty string when there is none."""
    assets = extract_theme_assets(document)
    return assets[0] if assets else ""


def check_theme_document(status, content_type, document):
    """Judge GET /: an HTML document that references the theme's own build.

    Returns (ok, detail); on success detail is the first asset reference, on failure the reason.
    """
    if status != 200:
        return False, "GET / answered %s, not 200" % (status,)
    kind = (content_type or "").split(";")[0].strip().lower()
    if kind != HTML_TYPE:
        return False, "GET / served Content-Type %r, not text/html" % (content_type or "",)
    asset = theme_asset_path(document)
    if not asset:
        return False, "the document references no same-origin JavaScript or stylesheet"
    return True, asset


def check_asset_response(status, content_type, size):
    """Judge the referenced asset: it must exist, be non-empty, and not be a document."""
    if status != 200:
        return False, "answered %s, not 200" % (status,)
    if size <= 0:
        return False, "the body is empty"
    kind = (content_type or "").split(";")[0].strip().lower()
    if "html" in kind:
        return False, "served %r: the asset route fell through to the document" % (content_type or "",)
    return True, "%s, %s" % (kind or "no Content-Type", human_bytes(size))


def header_value(headers, name):
    """Case-insensitive header lookup.

    A response's headers are an email.message.Message, whose own lookup folds case; the dict
    copy some call sites keep loses that, and a proxy that lowercases Content-Type would then
    read as "no Content-Type at all".
    """
    wanted = (name or "").lower()
    for key, value in (headers or {}).items():
        if key.lower() == wanted:
            return value
    return ""


def request(url, method="GET", payload=None, api_key=None, timeout=TIMEOUT_SECONDS):
    """One HTTP request. Returns (status, headers, body); raises CheckFailure when unreachable."""
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("User-Agent", USER_AGENT)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if api_key:
        req.add_header("Authorization", "Bearer " + api_key)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            return response.status, dict(response.headers), response.read()
    except urllib.error.HTTPError as exc:
        # 401/403 still carry a body worth reporting, so they are responses rather than failures
        # to connect.
        return exc.code, dict(exc.headers or {}), exc.read()
    except Exception as exc:
        raise CheckFailure("could not reach %s: %s" % (url, exc))


def masked_api_key(key):
    """Show enough of a key to tell two of them apart, never enough to use one."""
    if len(key) <= 8:
        return "*" * len(key)
    return key[:4] + "..." + key[-2:]


def main():
    """Run the checks. Returns the process exit code."""
    base = (os.environ.get("KOMARI_BASE_URL") or "").strip().rstrip("/")
    key = (os.environ.get("KOMARI_API_KEY") or "").strip()
    missing = []
    if not base:
        missing.append("KOMARI_BASE_URL")
    if not key:
        missing.append("KOMARI_API_KEY")
    if missing:
        print("SKIP  panel-auth-smoke: set %s to check a deployed panel with its API key"
              % " and ".join(missing))
        print("      (nothing was checked; exit 0 so an unconfigured run cannot fail a deployment)")
        return 0

    print("panel-auth-smoke: %s (API key %s)" % (base, masked_api_key(key)))
    results = []

    def record(ok, name, detail=""):
        results.append((ok, name, detail))
        print("  %s  %s%s" % ("PASS" if ok else "FAIL", name, "  -- " + detail if detail else ""))

    # 1. the authenticated admin API
    rpc_name = "POST %s %s" % (RPC_PATH, RPC_METHOD)
    payload = {"jsonrpc": "2.0", "id": 1, "method": RPC_METHOD, "params": {}}
    try:
        status, _headers, body = request(base + RPC_PATH, "POST", payload, key)
        parsed, parse_error = json_body(body)
        if status != 200:
            detail = "HTTP %s" % (status,)
            if status in (401, 403):
                detail += ": the API key was rejected (panel: Settings -> API key)"
            elif parse_error is None and isinstance(parsed, dict) and isinstance(parsed.get("error"), dict):
                detail += ": %s" % (parsed["error"].get("message") or "no message",)
            record(False, rpc_name, detail)
        elif parse_error is not None:
            record(False, rpc_name, parse_error)
        else:
            ok, detail = check_rpc_result(parsed)
            if ok:
                ok, detail = check_database_size(rpc_result_value(parsed))
            record(ok, rpc_name, detail)
    except CheckFailure as exc:
        record(False, rpc_name, str(exc))

    # 2. the theme the panel is actually serving
    theme_name = "GET / serves an HTML document that references the theme's build"
    try:
        status, headers, body = request(base + "/")
        ok, detail = check_theme_document(status, header_value(headers, "Content-Type"),
                                          body.decode("utf-8", "replace"))
        record(ok, theme_name, detail)
        if ok:
            asset_url = urllib.parse.urljoin(base + "/", detail)
            asset_status, asset_headers, asset_body = request(asset_url)
            asset_ok, asset_detail = check_asset_response(
                asset_status, header_value(asset_headers, "Content-Type"), len(asset_body))
            record(asset_ok, "the theme's build asset loads (%s)" % (detail,), asset_detail)
    except CheckFailure as exc:
        record(False, theme_name, str(exc))

    failed = [item for item in results if not item[0]]
    print("")
    for _ok, name, detail in failed:
        print("  FAILED: %s -- %s" % (name, detail))
    print("===== %d/%d checks passed =====" % (len(results) - len(failed), len(results)))
    return 1 if failed else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
