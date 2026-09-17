#!/usr/bin/env bash
# Set --month-rotate on a node's agent and adopt the previous agent's netstatic.
#
# Why both steps:
#
#   * Without --month-rotate the agent reports the raw kernel interface counter,
#     which starts at boot. The panel divides that counter by the plan limit, so
#     a host up for months shows a wildly inflated "used" figure (华纳云: 599 GB
#     of an 800 GB plan, versus 26 GB on the provider's own dashboard).
#
#   * With --month-rotate the agent instead sums the per-interval deltas that
#     netstatic recorded inside the cycle window. Those deltas live in
#     net_static.json, and this agent's working directory is fresh, so without
#     carrying the old file over the window would be empty and the panel would
#     undercount instead.
#
# The rotation day is the node's billing day, so the counter resets when the
# provider's cycle does.
#
# Usage: set-month-rotate.sh <day> [old-netstatic] [new-workdir]
set -euo pipefail

DAY="${1:?rotation day required (1-31)}"
OLD_FILE="${2:-/opt/komari/net_static.json}"
NEW_DIR="${3:-/opt/nekomari-agent}"

log() { printf '  %s\n' "$*"; }

if [ "$(id -u)" -eq 0 ]; then SUDO=""; else SUDO="sudo -n"; fi

UNIT=/etc/systemd/system/nekomari-agent.service
[ -f "$UNIT" ] || { echo "  !! $UNIT not found"; exit 1; }

echo "== setting --month-rotate ${DAY} =="

# 1. Adopt the previous agent's traffic history, if it exists and we have none.
if [ -f "$OLD_FILE" ]; then
  if [ -s "$NEW_DIR/net_static.json" ]; then
    log "keeping existing $NEW_DIR/net_static.json ($(stat -c %s "$NEW_DIR/net_static.json") bytes)"
  else
    $SUDO mkdir -p "$NEW_DIR"
    $SUDO cp "$OLD_FILE" "$NEW_DIR/net_static.json"
    log "adopted $OLD_FILE -> $NEW_DIR/net_static.json ($(stat -c %s "$OLD_FILE") bytes)"
  fi
else
  log "no previous netstatic at $OLD_FILE; history starts now"
fi

# 2. Add or replace the flag in ExecStart.
$SUDO cp "$UNIT" "$UNIT.bak-month-rotate"
if grep -q -- '--month-rotate' "$UNIT"; then
  $SUDO sed -i "s/--month-rotate [0-9]*/--month-rotate ${DAY}/" "$UNIT"
  log "replaced existing --month-rotate with ${DAY}"
else
  # Append to the last continuation line of ExecStart.
  $SUDO sed -i "0,/^\s*--disable-auto-update/s//  --disable-auto-update \\\\\n  --month-rotate ${DAY}/" "$UNIT"
  log "added --month-rotate ${DAY}"
fi

$SUDO systemctl daemon-reload
$SUDO systemctl restart nekomari-agent.service
sleep 10

log "state: $($SUDO systemctl is-active nekomari-agent.service || true)"
grep -oE '\-\-month-rotate [0-9]+' "$UNIT" | sed 's/^/  effective: /'
