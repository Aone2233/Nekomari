#!/usr/bin/env bash
# Install a staged agent binary over the running one on a node.
#
# Why this exists: the v0.1.14 fleet upgrade was done by hand on nine hosts with
# slightly different layouts, and two of the steps are easy to get wrong in a way that
# only shows up later:
#
#   * replacing the binary drops its file capability, and a non-root agent then cannot
#     send ICMP at all. Measured on MAC Server: three ICMP tasks reported
#     "socket: operation not permitted", which the panel renders as 15.2% loss. The
#     capability is recorded before the swap and restored after it.
#   * discovering "the agent" by process name is wrong twice over: `pgrep -f komari-agent`
#     also matches the shell running this script, and a leftover `docker run` of the
#     agent image looks like a host process (its parent is containerd). The running
#     systemd unit is the real answer, so the unit is found first and its MainPID is
#     resolved through /proc.
#
# The binary is copied to the node separately (PZYC cannot reach
# objects.githubusercontent.com, and one download per node is one failure mode per
# node); this script only installs what it is given, and verifies the hash first.
#
# Usage (on the target node):
#   VERSION=v0.1.16 ./install-staged-agent.sh /tmp/komari-agent-linux-amd64 [unit]
#
# Idempotent: if the running binary already has the staged hash it does nothing.
set -euo pipefail

SRC="${1:?usage: install-staged-agent.sh <staged-binary> [unit]}"
UNIT_ARG="${2:-}"
VERSION="${VERSION:-unknown}"

[ -f "$SRC" ] || { echo "no such staged binary: $SRC" >&2; exit 1; }

if [ -n "$UNIT_ARG" ]; then
  UNIT="$UNIT_ARG"
else
  UNIT="$(systemctl list-units --type=service --state=running --no-legend 2>/dev/null \
          | awk '{print $1}' | grep -iE 'komari.*agent|agent.*komari' | head -1)"
fi
[ -n "$UNIT" ] || { echo "no running komari agent unit found (pass the unit explicitly)" >&2; exit 1; }

PID="$(systemctl show -p MainPID --value "$UNIT" 2>/dev/null || true)"
[ -n "$PID" ] && [ "$PID" != "0" ] || { echo "$UNIT has no main PID" >&2; exit 1; }
BIN="$(readlink -f "/proc/$PID/exe" 2>/dev/null || true)"
[ -n "$BIN" ] && [ -x "$BIN" ] || { echo "could not resolve the binary of $UNIT" >&2; exit 1; }

OWNER="$(stat -c %U "$BIN")"; GROUP="$(stat -c %G "$BIN")"; MODE="$(stat -c %a "$BIN")"
CAPS="$(getcap "$BIN" 2>/dev/null || true)"
WANT="$(sha256sum "$SRC" | awk '{print $1}')"
GOT="$(sha256sum "$BIN" | awk '{print $1}')"

echo "  binary : $BIN ($OWNER:$GROUP mode $MODE${CAPS:+, $CAPS})"
echo "  unit   : $UNIT"
if [ "$WANT" = "$GOT" ]; then
  echo "  already at $VERSION ($WANT) -- nothing to do"
  exit 0
fi
echo "  current: $GOT"
echo "  target : $WANT"

cp -p "$BIN" "$BIN.bak-pre-$VERSION"
install -o "$OWNER" -g "$GROUP" -m "$MODE" "$SRC" "$BIN"
if [ -n "$CAPS" ]; then
  setcap cap_net_raw+ep "$BIN"
  echo "  capability restored: $(getcap "$BIN")"
fi
echo "  installed: $(sha256sum "$BIN" | awk '{print $1}')"

systemctl restart "$UNIT"
sleep 5
STATE="$(systemctl is-active "$UNIT" || true)"
echo "  $UNIT is $STATE"
[ "$STATE" = "active" ] || { echo "unit did not come back up" >&2; exit 1; }
