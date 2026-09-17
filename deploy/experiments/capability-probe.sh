#!/usr/bin/env bash
# One-shot probe: does a systemd *user* unit with a file-capability binary still
# get CAP_NET_RAW at the point of opening a raw ICMP socket?
#
# The agent failed with EPERM under systemd while the identical binary worked when
# run from a login shell, even though /proc/<pid>/status reported CapEff with
# CAP_NET_RAW set in both cases. This runs the capability-bearing probe from both
# contexts so the difference is visible instead of inferred.
set -uo pipefail

PROBE=~/probe-cap
[ -x "$PROBE" ] || { echo "missing $PROBE"; exit 1; }

echo "=== file capability ==="
getcap "$PROBE"

echo
echo "=== run from this shell ==="
"$PROBE"

echo
echo "=== run as a systemd user service ==="
systemctl --user stop probe-cap.service 2>/dev/null
systemd-run --user --unit=probe-cap --collect --wait --pipe "$PROBE" 2>&1 | tail -5
echo "(exit above is systemd-run's)"

echo
echo "=== same, but with NoNewPrivileges=yes (what a hardened unit would do) ==="
systemd-run --user --unit=probe-cap2 --collect --wait --pipe \
  --property=NoNewPrivileges=yes "$PROBE" 2>&1 | tail -5
systemctl --user stop probe-cap2.service 2>/dev/null
