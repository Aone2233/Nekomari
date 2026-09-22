#!/usr/bin/env bash
# Deterministic, offline tests for panel-probe.sh.
#
# Why these exist: the probe's exit code is the only thing `systemctl status` reports, and
# the 2026-09-22 review found the probe could not fail -- a fast HTTP 500 was recorded as a
# healthy sample and a refused connection produced 0.000000 s, which also passed the
# latency threshold. Every case below pins one of those shapes.
#
# The real deploy/panel-probe.sh is run as a subprocess with PANEL_ORIGIN/PANEL_URL pointed
# at listeners started here; nothing in this file re-implements the probe. No production
# host, no internet, and PROBE_LOG always points into the temp directory so /var/log is
# never touched. Thresholds are set to 99 s in every case so that a failure can only come
# from the health check, never from the latency thresholds.
set -uo pipefail

HERE=$(cd -- "$(dirname -- "$0")" && pwd)
PROBE="$HERE/panel-probe.sh"
if [ ! -f "$PROBE" ]; then
  printf 'FAIL cannot find %s\n' "$PROBE"
  exit 2
fi

TMP=$(mktemp -d) || exit 2
SRV_PID=""
cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

# ---- the test server -----------------------------------------------------------------

cat > "$TMP/server.py" <<'PY'
#!/usr/bin/env python3
"""HTTP shapes for the panel-probe tests, one listener per shape.

Every listener serves /api/public -- the path the probe appends -- and ignores the query
string. The probe takes PANEL_ORIGIN and PANEL_URL as separate base URLs, so pointing them
at different listeners is how a case picks the shape it wants for each path.
"""
import json
import socket
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ENVELOPE = json.dumps({"data": {"sitename": "probe-test"}, "message": "", "status": "success"})
ERROR_ENVELOPE = json.dumps({"message": "boom", "status": "error"})
NO_DATA = json.dumps({"message": "", "status": "success"})
NESTED = json.dumps({"data": {"status": "error"}, "message": "", "status": "success"})
HTML = "<html><body>this is not the panel</body></html>"
SLOW_SECONDS = 30


def make_handler(mode):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *args):
            pass

        def _send(self, code, body, ctype="application/json"):
            raw = body.encode()
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(raw)))
            self.send_header("CF-Ray", "0000000000000000-TST")
            self.send_header("CF-Cache-Status", "MISS")
            self.end_headers()
            try:
                self.wfile.write(raw)
            except (BrokenPipeError, ConnectionResetError):
                pass

        def do_GET(self):
            if mode == "ok":
                self._send(200, ENVELOPE)
            elif mode == "nested-status":
                # The envelope is fine; only a nested "status" inside data says error.
                self._send(200, NESTED)
            elif mode == "500":
                self._send(500, HTML, "text/html")
            elif mode == "bad-envelope":
                self._send(200, HTML, "text/html")
            elif mode == "error-envelope":
                self._send(200, ERROR_ENVELOPE)
            elif mode == "no-data":
                self._send(200, NO_DATA)
            elif mode == "empty-body":
                # A 200 with nothing in it: awk sees no input at all, so this is the shape
                # that a check written inside an awk rule (rather than END) accepts.
                self._send(200, "")
            elif mode == "slow":
                # Longer than any timeout used below, so curl's --max-time is what ends
                # the request and the timeout path is the one under test.
                time.sleep(SLOW_SECONDS)
                self._send(200, ENVELOPE)
            else:
                self._send(404, HTML, "text/html")

    return Handler


def main():
    for mode in ("ok", "nested-status", "500", "bad-envelope", "error-envelope", "no-data", "empty-body", "slow"):
        httpd = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(mode))
        print(f"{mode} {httpd.server_address[1]}", flush=True)
        threading.Thread(target=httpd.serve_forever, daemon=True).start()

    # A port nothing listens on. On Linux this is the ECONNREFUSED case. On Windows a
    # closed loopback port is silently dropped instead (measured: a raw connect() times
    # out rather than being refused), which is why the refusal branch is also pinned with
    # a stub curl below.
    probe = socket.socket()
    probe.bind(("127.0.0.1", 0))
    closed = probe.getsockname()[1]
    probe.close()
    print(f"closed {closed}", flush=True)
    threading.Event().wait()


main()
PY

python3 "$TMP/server.py" > "$TMP/server.out" 2> "$TMP/server.err" &
SRV_PID=$!
for _ in $(seq 1 100); do
  [ "$(wc -l < "$TMP/server.out" | tr -d ' ')" -ge 9 ] && break
  sleep 0.05
done
if [ "$(wc -l < "$TMP/server.out" | tr -d ' ')" -lt 9 ]; then
  printf 'FAIL the test server did not start\n'
  sed 's/^/  | /' "$TMP/server.err"
  exit 2
fi

port_of() { awk -v m="$1" '$1 == m { print $2 }' "$TMP/server.out"; }
url_of()  { printf 'http://127.0.0.1:%s' "$(port_of "$1")"; }

PORT_OK=$(port_of ok)
PORT_NESTED=$(port_of nested-status)
PORT_500=$(port_of 500)
PORT_BADENV=$(port_of bad-envelope)
PORT_ERRENV=$(port_of error-envelope)
PORT_NODATA=$(port_of no-data)
PORT_EMPTY=$(port_of empty-body)
PORT_SLOW=$(port_of slow)
PORT_CLOSED=$(port_of closed)
for v in "$PORT_OK" "$PORT_NESTED" "$PORT_500" "$PORT_BADENV" "$PORT_ERRENV" "$PORT_NODATA" "$PORT_EMPTY" "$PORT_SLOW" "$PORT_CLOSED"; do
  if [ -z "$v" ]; then
    printf 'FAIL the test server did not report every port\n'
    cat "$TMP/server.out"
    exit 2
  fi
done

# ---- stub curls ----------------------------------------------------------------------
#
# `curl` exit 7 with "000 0.000000" is exactly what a refused connection looks like, and
# the zero passing the latency threshold was half of the reported bug. The stub is needed
# because that branch is unreachable through a real socket on this host (see server.py),
# and it keeps the assertion deterministic on Ubuntu too, where the real closed port is
# used as well.

mkdir -p "$TMP/stub-refused" "$TMP/stub-zero-timing"
cat > "$TMP/stub-refused/curl" <<'SH'
#!/usr/bin/env bash
printf '000 0.000000'
exit 7
SH
cat > "$TMP/stub-zero-timing/curl" <<'SH'
#!/usr/bin/env bash
# A 200 whose write-out carries no timing: the shape that must never read as "fast".
printf '200 0.000000'
exit 0
SH
chmod +x "$TMP/stub-refused/curl" "$TMP/stub-zero-timing/curl"

# ---- harness -------------------------------------------------------------------------

PASS=0
FAIL=0
LAST_MS=0

ok()   { PASS=$((PASS + 1)); printf '  ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf '  FAIL %s\n' "$1"; }

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

expect_fast() { # expect_fast <label> <ms> <limit-ms>
  if [ "$2" -lt "$3" ]; then
    ok "$1 (${2} ms)"
  else
    bad "$1 (${2} ms, wanted < ${3} ms -- the failure was only caught by waiting)"
  fi
}

# run_case <name> <origin-url> <public-url> <expected-exit> <alert-ERE|none|""> [log-ERE]
run_case() {
  local name="$1" origin_url="$2" public_url="$3" want_rc="$4" want_alert="$5" want_log="${6:-}"
  local log="$TMP/$name.log" err="$TMP/$name.err" out rc start end

  : > "$log"
  start=$(date +%s%N)
  out=$(PATH="$CASE_PATH" PANEL_ORIGIN="$origin_url" PANEL_URL="$public_url" \
        SAMPLES="${CASE_SAMPLES:-1}" ORIGIN_MAX=99 PUBLIC_MAX=99 \
        ORIGIN_TIMEOUT="$CASE_TIMEOUT" PUBLIC_TIMEOUT="$CASE_TIMEOUT" \
        PROBE_LOG="$log" \
        bash "$PROBE" --quiet 2> "$err")
  rc=$?
  end=$(date +%s%N)
  LAST_MS=$(( (end - start) / 1000000 ))

  printf -- '--- %s (exit %s, %s ms)\n' "$name" "$rc" "$LAST_MS"
  expect "$name: exit code" "$rc" "$want_rc"
  expect "$name: --quiet keeps stdout empty" "$out" ""
  expect "$name: one line appended to the log" "$(wc -l < "$log" | tr -d ' ')" "1"
  expect_match "$name: log keeps the documented fields" "$log" \
    ' origin=[0-9.]+s public_max=[0-9.]+s pop=[^ ]+ cache=[^ ]+ samples=[0-9]+/[0-9]+( origin_fail=[^ ]+)?( public_fail=[^ ]+)?$'
  case "$want_alert" in
    "") ;;
    none) expect "$name: no alert" "$(cat "$err")" "" ;;
    *)    expect_match "$name: alert names the failure" "$err" "$want_alert" ;;
  esac
  [ -n "$want_log" ] && expect_match "$name: log names the failure" "$log" "$want_log"
  return 0
}

CASE_PATH="$PATH"
CASE_TIMEOUT=5
CASE_SAMPLES=1

printf '\n=== healthy: 200 + success envelope ===\n'
run_case healthy "$(url_of ok)" "$(url_of ok)" 0 none ' samples=1/1$'

printf '\n=== a nested "status" inside data is not the envelope ===\n'
run_case nested-status "$(url_of nested-status)" "$(url_of ok)" 0 none ' samples=1/1$'

printf '\n=== regression: a fast 500 must exit nonzero ===\n'
run_case origin-500 "$(url_of 500)" "$(url_of ok)" 1 'unexpected HTTP status 500' 'origin_fail=status-500'
expect_fast 'origin-500: failed instantly, not by waiting out the 5s limit' "$LAST_MS" 2000
run_case public-500 "$(url_of ok)" "$(url_of 500)" 1 'unexpected HTTP status 500' 'public_fail=status-500'
expect_fast 'public-500: failed instantly, not by waiting out the 5s limit' "$LAST_MS" 2000

printf '\n=== 200 with the wrong body must exit nonzero ===\n'
run_case origin-bad-envelope "$(url_of bad-envelope)" "$(url_of ok)" 1 'envelope' 'origin_fail=envelope'
run_case public-error-envelope "$(url_of ok)" "$(url_of error-envelope)" 1 'envelope' 'public_fail=envelope'
run_case origin-no-data "$(url_of no-data)" "$(url_of ok)" 1 'envelope' 'origin_fail=envelope'
run_case origin-empty-body "$(url_of empty-body)" "$(url_of ok)" 1 'envelope' 'origin_fail=envelope'

printf '\n=== regression: a refused connection must exit nonzero even at 0.000000 s ===\n'
CASE_PATH="$TMP/stub-refused:$PATH"
run_case origin-refused "$(url_of ok)" "$(url_of ok)" 1 'connection refused' 'origin_fail=refused'
expect_fast 'origin-refused: failed instantly, not by waiting out the 5s limit' "$LAST_MS" 2000
run_case public-refused "$(url_of ok)" "$(url_of ok)" 1 'connection refused' 'public_fail=refused'
CASE_PATH="$PATH"

printf '\n=== a really closed port (refused on Ubuntu, dropped on Windows) ===\n'
CASE_TIMEOUT=1
run_case closed-port "http://127.0.0.1:$PORT_CLOSED" "$(url_of ok)" 1 \
  'connection refused|no response before the 1s limit' 'origin_fail=(refused|timeout)'
CASE_TIMEOUT=5

printf '\n=== timeout: a slow response must exit nonzero ===\n'
CASE_TIMEOUT=1
run_case origin-timeout "$(url_of slow)" "$(url_of ok)" 1 \
  'no response before the 1s limit' 'origin_fail=timeout'
CASE_TIMEOUT=5

printf '\n=== a zero time_starttransfer is not a fast success ===\n'
CASE_PATH="$TMP/stub-zero-timing:$PATH"
run_case zero-timing "$(url_of ok)" "$(url_of ok)" 1 'no usable time_starttransfer' 'origin_fail=no-timing'
expect_fast 'zero-timing: rejected instantly' "$LAST_MS" 2000
CASE_PATH="$PATH"

printf '\n=== a non-numeric SAMPLES is a configuration error, not a healthy run ===\n'
err="$TMP/samples.err"
rc=0
SAMPLES=abc PANEL_ORIGIN="$(url_of ok)" PANEL_URL="$(url_of ok)" \
  PROBE_LOG="$TMP/samples.log" bash "$PROBE" --quiet > /dev/null 2> "$err" || rc=$?
expect 'bad SAMPLES: exit code' "$rc" "2"
expect_match 'bad SAMPLES: alert' "$err" 'SAMPLES is not a positive integer'

printf '\n=== one append-only line per run; --quiet is what silences stdout ===\n'
log="$TMP/append.log"
: > "$log"
out=$(PANEL_ORIGIN="$(url_of ok)" PANEL_URL="$(url_of ok)" SAMPLES=1 \
      ORIGIN_MAX=99 PUBLIC_MAX=99 ORIGIN_TIMEOUT=5 PUBLIC_TIMEOUT=5 \
      PROBE_LOG="$log" bash "$PROBE" 2> "$TMP/append.err")
printf '%s\n' "$out" > "$TMP/append.out"
expect_match 'without --quiet the line goes to stdout' "$TMP/append.out" \
  ' origin=[0-9.]+s public_max=[0-9.]+s pop=[^ ]+ cache=[^ ]+ samples=1/1$'
expect 'without --quiet there is no alert' "$(cat "$TMP/append.err")" ""
out=$(PANEL_ORIGIN="$(url_of ok)" PANEL_URL="$(url_of ok)" SAMPLES=1 \
      ORIGIN_MAX=99 PUBLIC_MAX=99 ORIGIN_TIMEOUT=5 PUBLIC_TIMEOUT=5 \
      PROBE_LOG="$log" bash "$PROBE" --quiet 2>> "$TMP/append.err")
expect 'second run appends a second line' "$(wc -l < "$log" | tr -d ' ')" "2"
expect 'second run is still silent on stdout' "$out" ""

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
