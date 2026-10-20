#!/usr/bin/env bash
# Deterministic, offline tests for panel-auth-smoke.py.
#
# Why these exist: the script's only value is that it FAILS when the authenticated admin API or
# the served theme is broken, and both failures are quiet. /api/rpc2 answers a rejected API key
# with HTTP 200 and an error member, so a status-code-only check reports a healthy panel while
# every admin call is refused; a document that names a bundle which 404s renders a blank page
# with nothing in the server log. Each case below pins one of those shapes, plus the SKIP path
# that makes the script safe to call unconditionally from deploy-verify.sh.
#
# The real script is run as a subprocess against stub listeners started here; nothing in this
# file re-implements it. Nothing leaves 127.0.0.1 and no production host is contacted.
set -uo pipefail

HERE=$(cd -- "$(dirname -- "$0")" && pwd)
SMOKE="$HERE/panel-auth-smoke.py"
if [ ! -f "$SMOKE" ]; then
  printf 'FAIL cannot find %s\n' "$SMOKE"
  exit 2
fi

PYTHON_BIN=""
for candidate in python3 python; do
  if command -v "$candidate" >/dev/null 2>&1; then PYTHON_BIN="$candidate"; break; fi
done
if [ -z "$PYTHON_BIN" ]; then
  printf 'FAIL no python3 on PATH\n'
  exit 2
fi

TMP=$(mktemp -d) || exit 2
SRV_PID=""
cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

# ---- the stub panel ----------------------------------------------------------------

cat > "$TMP/server.py" <<'PY'
#!/usr/bin/env python3
"""HTTP shapes for the panel-auth-smoke tests, one listener per shape.

Every listener answers POST /api/rpc2 and GET / (plus the /assets/ the document references),
so a case picks its shape by pointing KOMARI_BASE_URL at a different port. The bodies are the
real contracts: /api/rpc2 is JSON-RPC 2.0, so a rejection is HTTP 200 with an error member.
"""
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

RPC_OK = json.dumps({
    "jsonrpc": "2.0",
    "id": 1,
    "result": {
        "type": "sqlite",
        "size": 6144,
        "main": {"driver": "sqlite", "location": "local", "size": 4096, "action": ""},
        "monitoring": {"driver": "sqlite", "location": "local", "size": 2048, "action": ""},
        "local_total": 6144,
    },
})
RPC_ERROR = json.dumps({"jsonrpc": "2.0", "id": 1, "error": {"code": -32001, "message": "Permission denied"}})
RPC_UNAUTHORIZED = json.dumps({"status": "error", "message": "Unauthorized."})
RPC_NOT_RPC = json.dumps({"status": "error", "message": "boom"})
RPC_EMPTY_RESULT = json.dumps({"jsonrpc": "2.0", "id": 1, "result": {}})

THEME_HTML = (
    '<!doctype html><html><head><title>Nekomari Monitor</title>'
    '<script type="module" crossorigin src="/assets/entry-index-0badc0de.js"></script>'
    '<link rel="stylesheet" href="/assets/entry-index-0badc0de.css">'
    '<link rel="icon" href="/favicon.ico">'
    '</head><body><div id="root"></div></body></html>'
)
BARE_HTML = '<!doctype html><html><body>nothing is referenced here</body></html>'
ASSET_BODY = "console.log('theme');"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def _send(self, code, body, ctype="application/json"):
        raw = body.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        try:
            self.wfile.write(raw)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        self.rfile.read(length)
        mode = self.server.mode
        if mode == "rpc-error":
            self._send(200, RPC_ERROR)
        elif mode == "rpc-unauthorized":
            self._send(401, RPC_UNAUTHORIZED)
        elif mode == "rpc-not-rpc":
            self._send(200, RPC_NOT_RPC)
        elif mode == "no-database-size":
            self._send(200, RPC_EMPTY_RESULT)
        else:
            self._send(200, RPC_OK)

    def do_GET(self):
        mode = self.server.mode
        path = self.path.split("?")[0]
        if path == "/":
            if mode == "no-assets":
                self._send(200, BARE_HTML, "text/html; charset=utf-8")
            elif mode == "not-html":
                self._send(200, RPC_OK)
            else:
                self._send(200, THEME_HTML, "text/html; charset=utf-8")
            return
        if path.startswith("/assets/"):
            if mode == "asset-missing":
                self._send(404, BARE_HTML, "text/html; charset=utf-8")
            elif mode == "asset-html":
                self._send(200, BARE_HTML, "text/html; charset=utf-8")
            else:
                self._send(200, ASSET_BODY, "text/javascript; charset=utf-8")
            return
        self._send(404, BARE_HTML, "text/html; charset=utf-8")


def main():
    modes = ("ok", "rpc-error", "rpc-unauthorized", "rpc-not-rpc", "no-database-size",
             "no-assets", "not-html", "asset-missing", "asset-html")
    for mode in modes:
        httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        httpd.mode = mode
        print("%s %d" % (mode, httpd.server_address[1]), flush=True)
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
    threading.Event().wait()


main()
PY

"$PYTHON_BIN" "$TMP/server.py" > "$TMP/server.out" 2> "$TMP/server.err" &
SRV_PID=$!
for _ in $(seq 1 100); do
  [ "$(wc -l < "$TMP/server.out" | tr -d ' ')" -ge 9 ] && break
  sleep 0.05
done
if [ "$(wc -l < "$TMP/server.out" | tr -d ' ')" -lt 9 ]; then
  printf 'FAIL the stub panel did not start\n'
  sed 's/^/  | /' "$TMP/server.err"
  exit 2
fi

port_of() { awk -v m="$1" '$1 == m { print $2 }' "$TMP/server.out"; }
url_of()  { printf 'http://127.0.0.1:%s' "$(port_of "$1")"; }

for mode in ok rpc-error rpc-unauthorized rpc-not-rpc no-database-size no-assets not-html asset-missing asset-html; do
  if [ -z "$(port_of "$mode")" ]; then
    printf 'FAIL the stub panel did not report the %s port\n' "$mode"
    cat "$TMP/server.out"
    exit 2
  fi
done

# ---- harness ----------------------------------------------------------------------

PASS=0
FAIL=0

ok()  { PASS=$((PASS + 1)); printf '  ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf '  FAIL %s\n' "$1"; }

expect() { # expect <label> <actual> <wanted>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', wanted '$3')"; fi
}

expect_match() { # expect_match <label> <file> <ERE>
  if grep -Eq -- "$3" "$2" 2>/dev/null; then
    ok "$1"
  else
    bad "$1 (no match for /$3/)"
    sed 's/^/       | /' "$2" 2>/dev/null
  fi
}

expect_no_match() { # expect_no_match <label> <file> <ERE>
  if grep -Eq -- "$3" "$2" 2>/dev/null; then
    bad "$1 (matched /$3/)"
    sed 's/^/       | /' "$2" 2>/dev/null
  else
    ok "$1"
  fi
}

# run_case <name> <stub-mode> <expected-exit> <output-ERE|none>
run_case() {
  local name="$1" mode="$2" want_rc="$3" want="$4"
  local out="$TMP/$name.out" rc
  KOMARI_BASE_URL="$(url_of "$mode")" KOMARI_API_KEY="test-only-key-0123456789" \
    "$PYTHON_BIN" "$SMOKE" > "$out" 2>&1
  rc=$?
  expect "$name exit" "$rc" "$want_rc"
  if [ "$want" != "none" ]; then
    expect_match "$name says why" "$out" "$want"
  fi
  if [ "$want_rc" != 0 ]; then
    sed 's/^/       | /' "$out"
  fi
}

# ---- the pure JSON-shape checks -----------------------------------------------------
#
# The transport contracts, without any HTTP: which bodies count as success, and which
# references count as a theme document. These are the parts that decide pass/fail, so they are
# pinned directly as well as through the stub panel.

"$PYTHON_BIN" - "$SMOKE" <<'PY'
import importlib.util
import sys

path = sys.argv[1]
spec = importlib.util.spec_from_file_location("panel_auth_smoke", path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

rpc_cases = [
    ("a JSON-RPC success", {"jsonrpc": "2.0", "id": 1, "result": {"size": 4096}}, True),
    ("a JSON-RPC error", {"jsonrpc": "2.0", "id": 1, "error": {"code": -32001, "message": "no"}}, False),
    ("the REST envelope", {"status": "success", "message": "", "data": {"size": 1}}, True),
    ("the REST error envelope", {"status": "error", "message": "boom"}, False),
    ("a nested status that is not success", {"result": {"status": "error"}}, False),
    ("a nested status that is success", {"result": {"status": "success", "size": 1}}, True),
    ("neither result nor error", {"jsonrpc": "2.0", "id": 1}, False),
    ("not an object", [], False),
]
for label, payload, want in rpc_cases:
    got, detail = module.check_rpc_result(payload)
    assert got is want, (label, payload, got, detail)

assert module.database_size_fields({"size": 4096}) == [("size", 4096)]
assert module.database_size_fields({"size": 1, "main": {"size": 2}, "monitoring": {"size": 3}}) == [
    ("main", 2), ("monitoring", 3)]
assert module.database_size_fields({}) == []
assert module.rpc_result_value({"jsonrpc": "2.0", "id": 1, "result": {"size": 1}}) == {"size": 1}
assert module.rpc_result_value({"status": "success", "data": {"size": 1}}) == {"size": 1}
assert module.rpc_result_value({"status": "error"}) is None
assert module.check_database_size({})[0] is False
assert module.check_database_size({"main": {"size": 2}})[0] is True
assert module.header_value({"Content-Type": "text/html"}, "content-type") == "text/html"
assert module.header_value({"content-type": "text/html"}, "Content-Type") == "text/html"
assert module.header_value({}, "Content-Type") == ""
assert module.check_rpc_result({"status": "success", "data": {"size": 1}})[0] is True
assert module.check_rpc_result({"status": "error", "message": "boom"})[0] is False
assert module.human_bytes(0) == "0 B"
assert module.human_bytes(1024) == "1.0 KiB"
assert module.human_bytes(4096) == "4.0 KiB"

document = (
    '<script type="module" src="/assets/entry-index-0badc0de.js"></script>'
    '<link rel="stylesheet" href="/assets/entry-index-0badc0de.css">'
    '<link rel="icon" href="/favicon.ico">'
    '<script src="/themes/lumina/dist/main.js"></script>'
)
assert module.extract_theme_assets(document) == [
    "/assets/entry-index-0badc0de.js",
    "/assets/entry-index-0badc0de.css",
    "/themes/lumina/dist/main.js",
], module.extract_theme_assets(document)
assert module.extract_theme_assets('<link rel="icon" href="/favicon.ico">') == []
assert module.theme_asset_path("<html></html>") == ""
assert module.check_theme_document(200, "text/html; charset=utf-8", document)[0] is True
assert module.check_theme_document(200, "application/json", document)[0] is False
assert module.check_theme_document(404, "text/html", document)[0] is False
assert module.check_theme_document(200, "text/html", "<html></html>")[0] is False
assert module.check_asset_response(200, "text/javascript; charset=utf-8", 18)[0] is True
assert module.check_asset_response(200, "text/html", 18)[0] is False
assert module.check_asset_response(404, "text/javascript", 0)[0] is False
assert module.check_asset_response(200, "text/javascript", 0)[0] is False
print("pure functions: ok")
PY
expect "the pure JSON-shape checks pass" "$?" "0"

# ---- the SKIP path ------------------------------------------------------------------

out="$TMP/skip.out"
env -u KOMARI_BASE_URL -u KOMARI_API_KEY "$PYTHON_BIN" "$SMOKE" > "$out" 2>&1
expect "no environment skips with exit 0" "$?" "0"
expect_match "the skip names both variables" "$out" "SKIP.*KOMARI_BASE_URL and KOMARI_API_KEY"
expect_no_match "the skip does not claim a check ran" "$out" "PASS|FAILED:"

out="$TMP/skip-key-only.out"
KOMARI_API_KEY="test-only-key-0123456789" env -u KOMARI_BASE_URL \
  "$PYTHON_BIN" "$SMOKE" > "$out" 2>&1
expect "an API key without a base URL skips with exit 0" "$?" "0"
expect_match "the skip names the missing base URL" "$out" "SKIP.*KOMARI_BASE_URL"

# ---- the cases ----------------------------------------------------------------------

run_case auth-ok ok 0 "3/3 checks passed"
out="$TMP/auth-ok.out"
expect_match "the admin call reports the database sizes" "$out" "PASS +POST /api/rpc2 admin:getDatabaseSize +-- +main 4.0 KiB, monitoring 2.0 KiB"
expect_match "the theme check names the asset it fetched" "$out" "the theme's build asset loads .*assets/entry-index-0badc0de.js"
expect_no_match "the API key is never printed in full" "$out" "test-only-key-0123456789"

run_case rpc-error rpc-error 1 "Permission denied"
run_case rpc-unauthorized rpc-unauthorized 1 "401.*rejected"
run_case rpc-not-rpc rpc-not-rpc 1 "status is 'error'.*boom"
run_case no-database-size no-database-size 1 "carries no database size"
run_case no-assets no-assets 1 "references no build under /assets/ or /themes/"
run_case not-html not-html 1 "not text/html"
run_case asset-missing asset-missing 1 "answered 404, not 200"
run_case asset-html asset-html 1 "fell through to the document"

printf '\n===== %d passed, %d failed =====\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
