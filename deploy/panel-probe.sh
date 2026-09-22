#!/usr/bin/env bash
# Probe the panel's latency and record what the edge is doing, so "it feels slow"
# becomes a log line and an alert instead of a feeling.
#
# Why this exists: the 2026-09-21 slowness was found by hand. The panel itself was
# answering in 1-2 ms while requests through Cloudflare took 0.24 s to 82 s depending on
# which POP answered, and a cache fill could stall for 109 s on a 0.5 MB file. None of
# that was visible anywhere -- see docs/PERFORMANCE.md for the measurements.
#
# What it records, per run:
#   origin  - TTFB straight to the panel, bypassing nginx and Cloudflare. This is the
#             only number that implicates the panel, so it gets the tight threshold.
#   public  - TTFB through the real URL, with the cf-ray POP and the cache status, so a
#             far POP (SYD/FRA/LHR) is distinguishable from a slow origin.
#
# What counts as healthy: curl must exit 0, the HTTP status must be the expected one and
# the body must be the panel's success envelope. The 2026-09-22 review found the probe
# could not fail: a fast HTTP 500 was recorded as a healthy sample, and a refused
# connection reported 0.000000 s, which also passed the latency threshold. A nonzero exit
# is the only thing `systemctl status` shows, so an unhealthy sample now always exits
# nonzero; the thresholds remain a second, independent reason to fail.
#
# Exit code 1 when a sample is unhealthy or a threshold is exceeded, 2 on a configuration
# error, so a systemd unit (or anything else) can treat it as a failure.
# Usage: panel-probe.sh [--quiet]
set -uo pipefail

URL="${PANEL_URL:-https://komari.orderly2233.org}"
ORIGIN="${PANEL_ORIGIN:-http://127.0.0.1:25774}"
SAMPLES="${SAMPLES:-5}"
ORIGIN_MAX="${ORIGIN_MAX:-0.5}"      # seconds; the panel answers in ~2 ms when healthy
PUBLIC_MAX="${PUBLIC_MAX:-3.0}"      # seconds; a healthy edge path is 0.2-1.5 s
# curl's own limits. Overridable so the test harness can exercise the timeout path in a
# second instead of waiting out the production 10/30 s.
ORIGIN_TIMEOUT="${ORIGIN_TIMEOUT:-10}"
PUBLIC_TIMEOUT="${PUBLIC_TIMEOUT:-30}"
LOG="${PROBE_LOG:-/var/log/nekomari-panel-probe.log}"
QUIET=0
[ "${1:-}" = "--quiet" ] && QUIET=1

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# Both paths expect exactly 200. The panel has exactly one healthy answer on loopback --
# no redirect, no auth, no CDN rewriting -- and through the edge every other status is a
# real fault worth waking someone for: Cloudflare's own 52x family, a WAF challenge, or an
# origin error passed through. A 3xx is not accepted either, because curl is deliberately
# not given -L: following it would time a different URL than the one being monitored.
EXPECT_STATUS=200

# A SAMPLES that is not a positive integer would run zero public samples and report
# samples=0/abc with exit 0 -- the same silent-success shape this script exists to catch.
case "$SAMPLES" in
  ''|*[!0-9]*)
    printf '%s ALERT SAMPLES is not a positive integer (%s)\n' "$now" "$SAMPLES" >&2
    exit 2
    ;;
esac
if [ "$SAMPLES" -lt 1 ]; then
  printf '%s ALERT SAMPLES must be at least 1 (%s)\n' "$now" "$SAMPLES" >&2
  exit 2
fi

# Header and body files per sample. PrivateTmp=yes in the unit keeps this off any shared
# /tmp, and the trap removes it even when a sample fails.
work=$(mktemp -d) || { printf '%s ALERT cannot create a work directory\n' "$now" >&2; exit 2; }
trap 'rm -rf "$work"' EXIT

# ---- sample classification ----------------------------------------------------------

# curl_failure maps curl's exit status to the two failure forms used below: a short token
# for the log line and a sentence for the alert. The token is what makes the log usable
# later, because "refused" and "timeout" have different fixes.
curl_failure() {
  case "$1" in
    6)  S_REASON="dns";         S_TEXT="name resolution failed (curl exit 6)" ;;
    7)  S_REASON="refused";     S_TEXT="connection refused (curl exit 7)" ;;
    28) S_REASON="timeout";     S_TEXT="no response before the ${2}s limit (curl exit 28)" ;;
    35) S_REASON="tls";         S_TEXT="TLS handshake failed (curl exit 35)" ;;
    52) S_REASON="empty-reply"; S_TEXT="server closed the connection without a reply (curl exit 52)" ;;
    56) S_REASON="recv-error";  S_TEXT="failure receiving the response (curl exit 56)" ;;
    *)  S_REASON="curl-$1";     S_TEXT="curl exited $1" ;;
  esac
}

# envelope_ok <body-file>
#
# The panel answers {"data":...,"message":"","status":"success"} (web/api/Common.go,
# rendered by web/rpc/jsonrpc/bridge.go). A 200 carrying anything else -- an nginx or
# Cloudflare HTML error page, or {"status":"error"} -- is not the panel answering, so it
# must not count as a healthy sample. Only top-level keys are considered: a nested
# "status" inside data is not the envelope.
#
# awk rather than jq because the probe runs on hosts where only curl/coreutils/awk/sed are
# guaranteed.
envelope_ok() {
  tr -d '\r\n' < "$1" | awk '
    { body = $0
      seen = 1
      n = length(body)
      bad = (substr(body, 1, 1) != "{")
      # The object has to end where the body ends; trailing junk is not an envelope.
      m = n
      while (m > 0 && substr(body, m, 1) == " ") m--
      if (!bad && substr(body, m, 1) != "}") bad = 1
      if (!bad) {
        depth = 0; i = 1; status = ""; data = 0
        while (i <= n) {
          c = substr(body, i, 1)
          if (c == "\"") {
            # Read the whole string, then look at what follows it: a key is a string at
            # depth 1 whose next non-space character is ":".
            j = i + 1; s = ""
            while (j <= n) {
              d = substr(body, j, 1)
              if (d == "\\") { s = s substr(body, j + 1, 1); j += 2; continue }
              if (d == "\"") break
              s = s d; j++
            }
            k = j + 1
            while (k <= n && substr(body, k, 1) == " ") k++
            if (depth == 1 && substr(body, k, 1) == ":") {
              if (s == "data") data = 1
              if (s == "status") {
                v = k + 1
                while (v <= n && substr(body, v, 1) == " ") v++
                if (substr(body, v, 1) == "\"") {
                  w = v + 1
                  while (w <= n) {
                    e = substr(body, w, 1)
                    if (e == "\\") { status = status substr(body, w + 1, 1); w += 2; continue }
                    if (e == "\"") break
                    status = status e; w++
                  }
                }
              }
            }
            i = j + 1
            continue
          }
          if (c == "{" || c == "[") depth++
          else if (c == "}" || c == "]") depth--
          i++
        }
        if (depth != 0) bad = 1
        if (status != "success") bad = 1
        if (!data) bad = 1
      }
    }
    # The decision lives in END, not in the rule: an empty body produces no record at
    # all, so a check written inside the rule would never run and would accept it.
    END { exit (seen && !bad) ? 0 : 1 }'
}

# sample <name> <url> <max-time> <expected-status>
#
# Runs one request and classifies it. Sets S_TIME (time_starttransfer, 0 when curl
# reported none), S_CODE (HTTP status, 000 when curl reported none), and S_REASON/S_TEXT
# (empty when healthy). Returns 0 only when curl exited 0, the status is the expected one,
# the timing is a real positive number and the body is a success envelope.
#
# Order matters: curl's exit status is checked first. On a refused connection curl still
# prints "000 0.000000", and that zero has to read as "no answer", never as a fast answer.
# -f is deliberately not used: curl must report the status rather than collapse a 500 into
# exit 22, because the status is the diagnosis.
sample() {
  local name="$1" url="$2" tmax="$3" expect="$4"
  local meta rc

  # Pre-create both files: curl does not create -o/-D targets when the transfer never
  # starts, so an empty file (rather than a stale one from the previous sample) is what
  # the parser below must see.
  : > "$work/$name.hdr"
  : > "$work/$name.body"

  meta=$(curl -s -D "$work/$name.hdr" -o "$work/$name.body" \
           -w '%{http_code} %{time_starttransfer}' --max-time "$tmax" "$url" 2>/dev/null)
  rc=$?

  S_CODE=$(printf '%s\n' "$meta" | awk 'NR == 1 { print $1 }')
  S_TIME=$(printf '%s\n' "$meta" | awk 'NR == 1 { print $2 }')
  [ -n "$S_CODE" ] || S_CODE="000"
  [ -n "$S_TIME" ] || S_TIME="0"
  S_REASON=""
  S_TEXT=""

  if [ "$rc" -ne 0 ]; then
    curl_failure "$rc" "$tmax"
  elif [ "$S_CODE" != "$expect" ]; then
    S_REASON="status-$S_CODE"
    S_TEXT="unexpected HTTP status $S_CODE (expected $expect)"
  elif ! awk -v t="$S_TIME" 'BEGIN { exit !(t + 0 > 0) }'; then
    S_REASON="no-timing"
    S_TEXT="no usable time_starttransfer ('$S_TIME') with HTTP $S_CODE; a missing timing is not a fast answer"
  elif ! envelope_ok "$work/$name.body"; then
    S_REASON="envelope"
    S_TEXT="HTTP $S_CODE body is not the {\"status\":\"success\",...} envelope"
  fi

  [ -z "$S_REASON" ]
}

# ---- origin: one sample, no proxy, no CDN -------------------------------------------
if sample origin "${ORIGIN}/api/public" "$ORIGIN_TIMEOUT" "$EXPECT_STATUS"; then
  origin_fail=""
  origin_text=""
else
  origin_fail="$S_REASON"
  origin_text="$S_TEXT"
fi
# The timing is recorded even when the sample failed -- a 500 that took 0.6 s is worth
# seeing in the log -- but only a healthy sample is thresholded below.
origin="$S_TIME"

# ---- public: SAMPLES samples, keeping the slowest and the POP that served it ---------
worst=0
worst_pop="-"
worst_cache="-"
ok_count=0
public_fail=""
public_text=""

for _ in $(seq 1 "$SAMPLES"); do
  if sample public "${URL}/api/public?probe=${RANDOM}${RANDOM}" "$PUBLIC_TIMEOUT" "$EXPECT_STATUS"; then
    ok_count=$((ok_count + 1))
  else
    # Distinct reasons in first-seen order: SAMPLES is small, but one reason repeated
    # five times should not turn the log line into a paragraph.
    case ",$public_fail," in
      *",$S_REASON,"*) ;;
      *)
        public_fail="${public_fail:+$public_fail,}$S_REASON"
        public_text="${public_text:+$public_text; }$S_TEXT"
        ;;
    esac
  fi

  pop=$(sed -n 's/^[Cc][Ff]-[Rr]ay:.*-\([A-Za-z]*\).*/\1/p' "$work/public.hdr" | tail -1)
  cache=$(sed -n 's/^[Cc][Ff]-[Cc]ache-[Ss]tatus: *//p' "$work/public.hdr" | tr -d '\r' | tail -1)
  [ -n "$pop" ] || pop="-"
  [ -n "$cache" ] || cache="-"

  # A zero timing never wins this comparison, so a failed sample cannot masquerade as the
  # fastest one; a failed sample that did take measurable time still reports its POP.
  if awk -v a="$S_TIME" -v b="$worst" 'BEGIN { exit !(a + 0 > b + 0) }'; then
    worst="$S_TIME"; worst_pop="$pop"; worst_cache="$cache"
  fi
done

# One line per run, append-only. The healthy line keeps the exact fields it always had so
# existing log readers keep working; failure detail is appended after them.
detail=""
[ -n "$origin_fail" ] && detail="$detail origin_fail=$origin_fail"
[ -n "$public_fail" ] && detail="$detail public_fail=$public_fail"

line=$(printf '%s origin=%.3fs public_max=%.3fs pop=%s cache=%s samples=%d/%d%s' \
  "$now" "$origin" "$worst" "$worst_pop" "$worst_cache" "$ok_count" "$SAMPLES" "$detail")
printf '%s\n' "$line" >> "$LOG" 2>/dev/null || true
[ "$QUIET" = 1 ] || printf '%s\n' "$line"

status=0

if [ -n "$origin_fail" ]; then
  printf '%s ALERT the panel itself did not answer healthily: %s\n' "$now" "$origin_text" >&2
  status=1
fi

if [ -n "$public_fail" ]; then
  printf '%s ALERT %d/%d public samples failed (%s): %s; check the POP/edge path before the panel\n' \
    "$now" "$((SAMPLES - ok_count))" "$SAMPLES" "$public_fail" "$public_text" >&2
  status=1
fi

# Thresholds only apply to samples that were healthy: "the panel answered in 0.6 s" is a
# misleading alert when the panel actually answered 500, and the failure alert already
# exits nonzero.
if [ -z "$origin_fail" ] && awk -v a="$origin" -v b="$ORIGIN_MAX" 'BEGIN { exit !(a + 0 > b + 0) }'; then
  printf '%s ALERT the panel itself answered in %.3fs (limit %ss)\n' "$now" "$origin" "$ORIGIN_MAX" >&2
  status=1
fi
if [ -z "$public_fail" ] && awk -v a="$worst" -v b="$PUBLIC_MAX" 'BEGIN { exit !(a + 0 > b + 0) }'; then
  printf '%s ALERT the public URL took %.3fs via %s (limit %ss); check the POP first\n' \
    "$now" "$worst" "$worst_pop" "$PUBLIC_MAX" >&2
  status=1
fi

exit "$status"
