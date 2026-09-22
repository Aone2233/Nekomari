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
# Exit code 1 when a threshold is exceeded, so a systemd unit (or anything else) can
# treat it as a failure. Usage: panel-probe.sh [--quiet]
set -uo pipefail

URL="${PANEL_URL:-https://komari.orderly2233.org}"
ORIGIN="${PANEL_ORIGIN:-http://127.0.0.1:25774}"
SAMPLES="${SAMPLES:-5}"
ORIGIN_MAX="${ORIGIN_MAX:-0.5}"      # seconds; the panel answers in ~2 ms when healthy
PUBLIC_MAX="${PUBLIC_MAX:-3.0}"      # seconds; a healthy edge path is 0.2-1.5 s
LOG="${PROBE_LOG:-/var/log/nekomari-panel-probe.log}"
QUIET=0
[ "${1:-}" = "--quiet" ] && QUIET=1

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# ---- origin: one sample, no proxy, no CDN -------------------------------------------
origin=$(curl -s -o /dev/null -w '%{time_starttransfer}' --max-time 10 "${ORIGIN}/api/public" 2>/dev/null)
[ -n "$origin" ] || origin="999"

# ---- public: SAMPLES samples, keeping the slowest and the POP that served it ---------
worst=0
worst_pop="-"
worst_cache="-"
ok_count=0
for _ in $(seq 1 "$SAMPLES"); do
  out=$(curl -s -D - -o /dev/null -w '%{time_starttransfer}' --max-time 30 \
          "${URL}/api/public?probe=${RANDOM}${RANDOM}" 2>/dev/null)
  t=$(printf '%s\n' "$out" | tail -1)
  pop=$(printf '%s\n' "$out" | sed -n 's/^[Cc][Ff]-[Rr]ay:.*-\([A-Za-z]*\).*/\1/p' | tail -1)
  cache=$(printf '%s\n' "$out" | sed -n 's/^[Cc][Ff]-[Cc]ache-[Ss]tatus: *//p' | tr -d '\r' | tail -1)
  [ -n "$t" ] || t="999"
  [ -n "$pop" ] || pop="-"
  [ -n "$cache" ] || cache="-"
  case "$t" in 999) ;; *) ok_count=$((ok_count + 1)) ;; esac
  if awk -v a="$t" -v b="$worst" 'BEGIN{exit !(a+0 > b+0)}'; then
    worst="$t"; worst_pop="$pop"; worst_cache="$cache"
  fi
done

line=$(printf '%s origin=%.3fs public_max=%.3fs pop=%s cache=%s samples=%d/%d' \
  "$now" "$origin" "$worst" "$worst_pop" "$worst_cache" "$ok_count" "$SAMPLES")
printf '%s\n' "$line" >> "$LOG" 2>/dev/null || true
[ "$QUIET" = 1 ] || printf '%s\n' "$line"

status=0
if awk -v a="$origin" -v b="$ORIGIN_MAX" 'BEGIN{exit !(a+0 > b+0)}'; then
  printf '%s ALERT the panel itself answered in %.3fs (limit %ss)\n' "$now" "$origin" "$ORIGIN_MAX" >&2
  status=1
fi
if awk -v a="$worst" -v b="$PUBLIC_MAX" 'BEGIN{exit !(a+0 > b+0)}'; then
  printf '%s ALERT the public URL took %.3fs via %s (limit %ss); check the POP first\n' \
    "$now" "$worst" "$worst_pop" "$PUBLIC_MAX" >&2
  status=1
fi
exit "$status"
