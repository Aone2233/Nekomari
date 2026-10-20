#!/usr/bin/env bash
# Deterministic, offline tests for panel-upgrade-check.sh.
#
# Why these exist: the check's whole value is that it FAILS on the shapes that get past a
# hand-run rollout -- a container that is "Up" while crash-looping, /api/version reporting the
# previous tag, a database whose quick_check is no longer "ok", a missing rollback archive, and
# ERROR lines nobody counted. Each case below pins one of those shapes, and pins that the
# healthy case really produces zero failures.
#
# The real script is run as a subprocess. The panel is a local Python listener (so the HTTP
# paths go through real curl, including real status codes), while docker and sqlite3 are PATH
# stubs that the cases configure through the same environment variables the script documents.
# Nothing here talks to a real panel, a real container or a real database.
set -uo pipefail

HERE=$(cd -- "$(dirname -- "$0")" && pwd)
CHECK="$HERE/panel-upgrade-check.sh"
if [ ! -f "$CHECK" ]; then
  printf 'FAIL cannot find %s\n' "$CHECK"
  exit 2
fi

PYTHON_BIN=""
for candidate in python3 python; do
  if command -v "$candidate" >/dev/null 2>&1; then PYTHON_BIN="$candidate"; break; fi
done
if [ -z "$PYTHON_BIN" ]; then
  printf 'FAIL no python3 on PATH (the fake panel needs it)\n'
  exit 2
fi

TMP=$(mktemp -d) || exit 2
STUB="$TMP/bin"
SRV_PID=""
cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT
mkdir -p "$STUB"

# ---- the fake panel ----------------------------------------------------------------
#
# One listener per shape, so a case picks its panel by BASE_URL. The bodies are the real
# envelopes: /api/version is {"status":"success","message":"","data":{"version":..,"hash":..}}
# rendered by web/rpc/jsonrpc/bridge.go (renderStandard) for public:getVersion.
cat > "$TMP/panel.py" <<'PY'
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def envelope(data):
    return json.dumps({"status": "success", "message": "", "data": data})


OK_VERSION = envelope({"version": "v1.6.19", "hash": "5f4d7fa"})
OLD_VERSION = envelope({"version": "v1.6.18", "hash": "ded9498"})
WRONG_HASH = envelope({"version": "v1.6.19", "hash": "ded9498"})
UNKNOWN_HASH = envelope({"version": "v1.6.19", "hash": "unknown"})
PUBLIC_OK = envelope({"sitename": "upgrade-check-test"})
PUBLIC_ERROR = json.dumps({"status": "error", "message": "boom"})
INDEX_HTML = "<!doctype html><html><body>the panel</body></html>"


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

    def do_GET(self):
        mode = self.server.mode
        path = self.path.split("?")[0]
        if path.endswith("/api/version"):
            if mode == "version-500":
                self._send(500, PUBLIC_ERROR)
            elif mode == "version-html":
                self._send(200, "<html>not the panel</html>", "text/html")
            elif mode == "old-version":
                self._send(200, OLD_VERSION)
            elif mode == "wrong-hash":
                self._send(200, WRONG_HASH)
            elif mode == "unknown-hash":
                self._send(200, UNKNOWN_HASH)
            else:
                self._send(200, OK_VERSION)
            return
        if path.endswith("/api/public"):
            if mode == "public-500":
                self._send(500, PUBLIC_ERROR)
            elif mode == "public-error":
                self._send(200, PUBLIC_ERROR)
            else:
                self._send(200, PUBLIC_OK)
            return
        if mode == "home-500":
            self._send(500, INDEX_HTML, "text/html")
        elif mode == "home-json":
            self._send(200, PUBLIC_OK)
        else:
            self._send(200, INDEX_HTML, "text/html; charset=utf-8")


def main():
    modes = ("ok", "old-version", "wrong-hash", "unknown-hash", "version-500", "version-html",
             "public-500", "public-error", "home-500", "home-json")
    for mode in modes:
        httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        httpd.mode = mode
        print("%s %d" % (mode, httpd.server_address[1]), flush=True)
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
    threading.Event().wait()


main()
PY

"$PYTHON_BIN" "$TMP/panel.py" > "$TMP/panel.out" 2> "$TMP/panel.err" &
SRV_PID=$!
modes=(ok old-version wrong-hash unknown-hash version-500 version-html public-500 public-error home-500 home-json)
for _ in $(seq 1 100); do
  [ "$(wc -l < "$TMP/panel.out" | tr -d ' ')" -ge 10 ] && break
  sleep 0.05
done
if [ "$(wc -l < "$TMP/panel.out" | tr -d ' ')" -lt 10 ]; then
  printf 'FAIL the fake panel did not start\n'
  sed 's/^/  | /' "$TMP/panel.err"
  exit 2
fi
port_of() { awk -v m="$1" '$1 == m { print $2 }' "$TMP/panel.out"; }
url_of()  { printf 'http://127.0.0.1:%s' "$(port_of "$1")"; }
for mode in "${modes[@]}"; do
  if [ -z "$(port_of "$mode")" ]; then
    printf 'FAIL the fake panel did not report the %s port\n' "$mode"
    cat "$TMP/panel.out"
    exit 2
  fi
done

# ---- docker and sqlite3 stubs -------------------------------------------------------
#
# The docker stub records its subcommand so a case can assert the check only ever asks for
# read-only information (inspect, logs) -- a check that stopped a container to inspect it
# would be a bug this suite must catch.
cat > "$STUB/docker" <<'SH'
#!/usr/bin/env bash
here=$(cd -- "$(dirname -- "$0")" && pwd)
printf '%s\n' "${1:-}" >> "$here/calls.log"
cmd="${1:-}"; shift || true
case "$cmd" in
  inspect)
    case "${STUB_CONTAINER:-healthy}" in
      missing) echo "Error response from daemon: No such object: nekomari" >&2; exit 1 ;;
      *)       printf '%s|%s\n' "${STUB_CONTAINER:-healthy}" "${STUB_RESTARTS:-0}" ;;
    esac
    exit 0
    ;;
  logs)
    i=0
    while [ "$i" -lt "${STUB_LOG_ERRORS:-0}" ]; do
      printf '2026/10/20 09:0%s:00 [ERROR/SERVER] synthetic failure %s\n' "$i" "$i"
      i=$((i + 1))
    done
    printf '2026/10/20 09:00:00 [INFO/SERVER] panel ready\n'
    exit 0
    ;;
  *)
    echo "unexpected docker command: $cmd" >&2
    exit 9
    ;;
esac
SH

cat > "$STUB/sqlite3" <<'SH'
#!/usr/bin/env bash
# PRAGMA quick_check prints "ok" when the database is fine and a findings list when it is not;
# both arrive with exit 0, which is why the check has to read the output.
case "${STUB_DB:-ok}" in
  corrupt) printf '*** in database main ***\nMain freelist: size is 20 but should be 21\n' ;;
  error)   echo "Error: unable to open database file" >&2; exit 1 ;;
  *)       echo ok ;;
esac
exit 0
SH
chmod +x "$STUB/docker" "$STUB/sqlite3"

# ---- the data directory the cases inspect -------------------------------------------
#
# make_data <dir> <yes|none|nodir>
#   yes    two pre-upgrade archives (the healthy shape)
#   none   data/backup exists but is empty -- an upgrade that wrote no rollback point
#   nodir  no data/backup at all -- an older layout, or a failed boot
make_data() {
  local dir="$1" mode="$2"
  rm -rf "$dir"
  mkdir -p "$dir"
  : > "$dir/komari.db"
  case "$mode" in
    nodir) ;;
    none)  mkdir -p "$dir/backup" ;;
    *)
      mkdir -p "$dir/backup"
      head -c 2048 /dev/zero > "$dir/backup/upgrade-20261020T022902Z.zip"
      head -c 1024 /dev/zero > "$dir/backup/upgrade-20261019T111111Z.zip"
      ;;
  esac
}

# ---- harness ------------------------------------------------------------------------

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

# run_case <name> <panel-mode> <expected-exit> <output-ERE|none> <backups> [KEY=VALUE ...]
#
# Extra KEY=VALUE pairs are handed to the script under test through env(1), which is how each
# case sets STUB_CONTAINER / STUB_DB / EXPECTED_HASH / limits without another script flag.
run_case() {
  local name="$1" mode="$2" want_rc="$3" want="$4" backups="$5"
  shift 5
  local data="$TMP/data.$name" out="$TMP/$name.out" rc url
  make_data "$data" "$backups"
  : > "$STUB/calls.log"
  url="$(url_of "$mode")"
  env PATH="$STUB:$PATH" \
      BASE_URL="$url" DATA_DIR="$data" CONTAINER=nekomari \
      DOCKER_BIN="$STUB/docker" SQLITE_BIN="$STUB/sqlite3" \
      EXPECTED_VERSION=v1.6.19 HTTP_TIMEOUT=5 \
      "$@" \
      bash "$CHECK" > "$out" 2>&1
  rc=$?
  expect "$name exit" "$rc" "$want_rc"
  if [ "$want" != "none" ]; then
    expect_match "$name reason" "$out" "$want"
  fi
}

# ---- the healthy case ---------------------------------------------------------------

run_case ok ok 0 "===== [0-9]+ passed, 0 failed" yes
OK_OUT="$TMP/ok.out"
expect_match "ok: container health"        "$OK_OUT" "PASS  container nekomari is healthy"
expect_match "ok: restart count"           "$OK_OUT" "PASS  container nekomari restarted 0 time"
expect_match "ok: version matches"         "$OK_OUT" "PASS  /api/version reports v1.6.19"
expect_match "ok: hash is skipped"         "$OK_OUT" "SKIP  /api/version hash: EXPECTED_HASH is not set"
expect_match "ok: public api"              "$OK_OUT" "PASS  /api/public: 200 with a success envelope"
expect_match "ok: front page"              "$OK_OUT" "PASS  GET /: 200"
expect_match "ok: quick_check"             "$OK_OUT" "PASS  database quick_check: ok"
expect_match "ok: upgrade archives"        "$OK_OUT" "PASS  upgrade archives: 2 file"
expect_match "ok: log window"              "$OK_OUT" "PASS  logs: 0 ERROR line"
expect_match "ok: nothing was written"     "$OK_OUT" "no failures"
expect "ok: docker was only asked to inspect and read logs" \
  "$(sort -u "$STUB/calls.log" | tr '\n' ' ')" "inspect logs "

# ---- the failing shapes -------------------------------------------------------------

run_case old-version old-version 1 "reports v1.6.18, expected v1.6.19" yes
run_case version-500 version-500 1 "/api/version: HTTP 500" yes
run_case version-html version-html 1 "not the panel's success envelope" yes
run_case public-500 public-500 1 "/api/public: HTTP 500" yes
run_case public-error public-error 1 "/api/public: 200 but not a success envelope" yes
run_case home-500 home-500 1 "GET /: HTTP 500" yes
run_case home-json home-json 1 "not an HTML document" yes

run_case db-corrupt ok 1 "database quick_check: \*\*\* in database main" yes STUB_DB=corrupt
run_case db-error ok 1 "database: sqlite3 exited 1: Error: unable to open database file" yes STUB_DB=error

run_case container-unhealthy ok 1 "container nekomari is unhealthy" yes STUB_CONTAINER=unhealthy
run_case container-starting ok 1 "health is 'starting'" yes STUB_CONTAINER=starting
run_case container-missing ok 1 "docker inspect failed" yes STUB_CONTAINER=missing
run_case restarts ok 1 "restarted 3 time" yes STUB_CONTAINER=healthy STUB_RESTARTS=3
run_case restarts-allowed ok 0 "restarted 3 time" yes STUB_CONTAINER=healthy STUB_RESTARTS=3 MAX_RESTARTS=5

run_case logs-errors ok 1 "3 ERROR line" yes STUB_LOG_ERRORS=3
run_case logs-allowed ok 0 "3 ERROR line" yes STUB_LOG_ERRORS=3 MAX_ERRORS=5

run_case no-archives ok 1 "no upgrade-\*.zip" none
run_case no-backup-dir ok 1 "does not exist, so no pre-upgrade backup" nodir

# ---- the missing tools --------------------------------------------------------------
#
# SKIP, not FAIL: a host without docker or sqlite3 still gets the checks it can do, and the
# skip names what was not checked. The healthy panel and the archives are still checked, so
# the run still exits 0 -- a missing optional tool must not read as "the upgrade failed".

run_case docker-missing ok 0 "SKIP  container nekomari: /nonexistent/docker is not installed" yes \
  DOCKER_BIN=/nonexistent/docker
expect_match "docker-missing: logs skipped too" "$TMP/docker-missing.out" "SKIP  logs: /nonexistent/docker is not installed"
expect_match "docker-missing: the rest still passes" "$TMP/docker-missing.out" "PASS  /api/version reports v1.6.19"

run_case sqlite3-missing ok 0 "SKIP  database: /nonexistent/sqlite3 is not installed" yes \
  SQLITE_BIN=/nonexistent/sqlite3
expect_match "sqlite3-missing: the rest still passes" "$TMP/sqlite3-missing.out" "PASS  upgrade archives: 2 file"

# ---- the expected hash --------------------------------------------------------------

run_case hash-match ok 0 "hash 5f4d7fa matches 5f4d7fa" yes EXPECTED_HASH=5f4d7fa
run_case hash-mismatch wrong-hash 1 "hash is ded9498, expected 5f4d7fa" yes EXPECTED_HASH=5f4d7fa
run_case hash-unknown unknown-hash 1 "was not built by the release workflow" yes EXPECTED_HASH=5f4d7fa

# ---- configuration errors -----------------------------------------------------------
#
# Exit 2, not 1: "I was not told what to expect" is a different failure from "the panel is
# wrong", and a caller that only reads the exit code must be able to tell them apart.

out="$TMP/no-expected.out"
env -u EXPECTED_VERSION DATA_DIR="$TMP/data.ok" DOCKER_BIN=/nonexistent/docker \
  bash "$CHECK" > "$out" 2>&1
expect "missing EXPECTED_VERSION exits 2" "$?" "2"
expect_match "missing EXPECTED_VERSION says why" "$out" "EXPECTED_VERSION is not set"

out="$TMP/bad-limit.out"
EXPECTED_VERSION=v1.6.19 MAX_ERRORS=abc bash "$CHECK" > "$out" 2>&1
expect "a non-numeric limit exits 2" "$?" "2"
expect_match "a non-numeric limit names the variable" "$out" "MAX_ERRORS must be a non-negative integer"

out="$TMP/unknown-arg.out"
bash "$CHECK" --frobnicate > "$out" 2>&1
expect "an unknown argument exits 2" "$?" "2"
expect_match "an unknown argument points at --help" "$out" "unknown argument: --frobnicate"

# ---- it does not touch the data -----------------------------------------------------
#
# "Read-only" is a promise in the script's header. Pin it: the files, their sizes and their
# mtimes under DATA_DIR must be identical after a run.

data="$TMP/data.readonly"
make_data "$data" yes
before=$(find "$data" -printf '%p %s %T@\n' | sort)
env PATH="$STUB:$PATH" BASE_URL="$(url_of ok)" DATA_DIR="$data" CONTAINER=nekomari \
    DOCKER_BIN="$STUB/docker" SQLITE_BIN="$STUB/sqlite3" EXPECTED_VERSION=v1.6.19 \
    bash "$CHECK" >/dev/null 2>&1
after=$(find "$data" -printf '%p %s %T@\n' | sort)
expect "a run leaves DATA_DIR byte-for-byte unchanged" "$after" "$before"

printf '\n===== %d passed, %d failed =====\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
