#!/usr/bin/env bash
# Install the Nekomari agent on PZYC (并行智算云).
#
# Written separately because this host cannot reliably fetch release assets from
# GitHub: `objects.githubusercontent.com` resets the TLS connection, while
# github.com itself answers fine. The agent binary was already downloaded and
# verified against the published SHA256SUMS (c3f090b6…a67a), so this script
# skips the download and only installs and starts the unit.
set -euo pipefail

PANEL_URL="https://komari.orderly2233.org"
# Never hardcode the token: this repository is public. Export it before running.
TOKEN="${NEKOMARI_AGENT_TOKEN:?set NEKOMARI_AGENT_TOKEN to this node's token}"
NAME="并行智算云服务器"
WORKDIR=/opt/nekomari-agent
BIN="$WORKDIR/komari-agent-linux-amd64"
EXPECT=c3f090b6158a18ae2e09ce716737ba6ead2e8e562595b9ad3da36fc6c9b4a67a

SUDO="sudo -n"
log() { printf '  %s\n' "$*"; }

echo "== ${NAME} =="

got=$(sha256sum "$BIN" | awk '{print $1}')
if [ "$got" != "$EXPECT" ]; then
  echo "  !! checksum mismatch: want $EXPECT got $got"
  exit 3
fi
log "checksum ok"
chmod +x "$BIN"

UNIT=/etc/systemd/system/nekomari-agent.service
TMP=$(mktemp)
cat > "$TMP" <<EOF
[Unit]
Description=Nekomari monitoring agent (reports to ${PANEL_URL})
Documentation=https://github.com/Aone2233/Nekomari
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${WORKDIR}
ExecStart=${BIN} \\
  -e ${PANEL_URL} \\
  -t ${TOKEN} \\
  -i 5 \\
  --info-report-interval 10 \\
  --exclude-nics lo,docker0 \\
  --disable-auto-update
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
EOF
$SUDO install -m 0644 "$TMP" "$UNIT"
rm -f "$TMP"

for unit in $($SUDO systemctl list-unit-files --type=service --no-pager --plain 2>/dev/null \
              | awk '{print $1}' | grep -i 'komari-agent' || true); do
  [ "$unit" = "nekomari-agent.service" ] && continue
  log "disabling old unit ${unit}"
  $SUDO systemctl disable --now "$unit" >/dev/null 2>&1 || true
done

$SUDO systemctl daemon-reload
$SUDO systemctl enable --now nekomari-agent.service >/dev/null 2>&1
sleep 12
state=$($SUDO systemctl is-active nekomari-agent.service || true)
log "nekomari-agent.service: ${state}"
$SUDO journalctl -u nekomari-agent.service --no-pager -n 6 2>/dev/null | sed 's/^/    /' | tail -5
[ "$state" = "active" ] || exit 4
echo "  OK"
