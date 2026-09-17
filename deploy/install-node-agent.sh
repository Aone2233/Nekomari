#!/usr/bin/env bash
# Install the Nekomari agent on a node so it reports to the restored panel.
#
# Design notes:
#   * Reuses the node's OWN token from the recovered database. --auto-discovery
#     would register a new node and orphan the restored one's group, tags,
#     pricing and historical series.
#   * Refuses to run if an agent already reports for this host, so the panel
#     never ends up with two nodes for one machine.
#   * Downloads the agent from the release rather than copying a binary around,
#     and verifies it against SHA256SUMS.txt.
#
# Usage: install-node-agent.sh <panel-url> <token> <node-name> [--force]
set -euo pipefail

PANEL_URL="${1:?panel url required}"
TOKEN="${2:?token required}"
NODE_NAME="${3:?node name required}"
FORCE="${4:-}"

VERSION="v0.1.2"
REPO="Aone2233/Nekomari"
WORKDIR="/opt/nekomari-agent"

log() { printf '  %s\n' "$*"; }

# Some nodes hand out a normal user with passwordless sudo rather than root, so
# every privileged step goes through $SUDO (empty when already root).
if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  if ! sudo -n true 2>/dev/null; then
    echo "  !! not root and passwordless sudo is unavailable"
    exit 1
  fi
  SUDO="sudo -n"
fi

echo "== ${NODE_NAME} =="

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "  !! unsupported arch $(uname -m)"; exit 1 ;;
esac
log "arch=${ARCH}"

# --- pre-flight: is something already reporting for this host? -------------
EXISTING=""
for unit in $($SUDO systemctl list-unit-files --type=service --no-pager --plain 2>/dev/null \
              | awk '{print $1}' | grep -i 'komari-agent' || true); do
  state=$($SUDO systemctl is-active "$unit" 2>/dev/null || true)
  log "existing unit: ${unit} (${state})"
  [ "$state" = "active" ] && EXISTING="$unit"
done
if [ -n "$EXISTING" ] && [ "$FORCE" != "--force" ]; then
  echo "  !! ${EXISTING} is already active; re-run with --force to replace it"
  exit 2
fi

# --- fetch + verify the agent ---------------------------------------------
$SUDO mkdir -p "$WORKDIR"
$SUDO chown "$(id -u):$(id -g)" "$WORKDIR" 2>/dev/null || true
cd "$WORKDIR"
if [ ! -x "komari-agent-linux-${ARCH}" ]; then
  log "downloading komari-agent-linux-${ARCH} ${VERSION}"
  curl -fsSL -o "komari-agent-linux-${ARCH}" \
    "https://github.com/${REPO}/releases/download/${VERSION}/komari-agent-linux-${ARCH}"
  chmod +x "komari-agent-linux-${ARCH}"
fi
# Verify against the published checksums when reachable; a mismatch aborts.
if curl -fsSL -o SHA256SUMS.txt \
     "https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS.txt" 2>/dev/null; then
  want=$(awk -v f="komari-agent-linux-${ARCH}" '$2==f {print $1}' SHA256SUMS.txt)
  got=$(sha256sum "komari-agent-linux-${ARCH}" | awk '{print $1}')
  if [ -n "$want" ] && [ "$want" != "$got" ]; then
    echo "  !! checksum mismatch: want ${want} got ${got}"
    exit 3
  fi
  log "checksum ok"
else
  log "checksum file unavailable; skipping verification"
fi

# --- install the unit -----------------------------------------------------
UNIT=/etc/systemd/system/nekomari-agent.service
UNIT_TMP=$(mktemp)
cat > "$UNIT_TMP" <<EOF
[Unit]
Description=Nekomari monitoring agent (reports to ${PANEL_URL})
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${WORKDIR}
ExecStart=${WORKDIR}/komari-agent-linux-${ARCH} \\
  -e ${PANEL_URL} \\
  -t ${TOKEN} \\
  -i 5 \\
  --info-report-interval 10 \\
  --exclude-nics lo,docker0 \\
  --disable-auto-update \\
  --disable-web-ssh
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
EOF
$SUDO install -m 0644 "$UNIT_TMP" "$UNIT"
rm -f "$UNIT_TMP"

# Retire any older agent unit so only one process reports for this host.
for unit in $(systemctl list-unit-files --type=service --no-pager --plain 2>/dev/null \
              | awk '{print $1}' | grep -i 'komari-agent' || true); do
  [ "$unit" = "nekomari-agent.service" ] && continue
  log "disabling old unit ${unit}"
  $SUDO systemctl disable --now "$unit" >/dev/null 2>&1 || true
done

$SUDO systemctl daemon-reload
$SUDO systemctl enable --now nekomari-agent.service >/dev/null 2>&1
sleep 12

state=$($SUDO systemctl is-active nekomari-agent.service 2>/dev/null || true)
log "nekomari-agent.service: ${state}"
$SUDO journalctl -u nekomari-agent.service --no-pager -n 6 2>/dev/null \
  | sed 's/^/    /' | tail -5

[ "$state" = "active" ] || exit 4
echo "  OK"
