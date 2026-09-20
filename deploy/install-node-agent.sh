#!/usr/bin/env bash
# Install the Nekomari agent on a node, in one line.
#
#   curl -fsSL https://raw.githubusercontent.com/Aone2233/Nekomari/main/deploy/install-node-agent.sh \
#     | sudo bash -s -- -e https://your-panel -t <token>
#
# It accepts the same agent flags the admin panel generates, because that is what
# the panel hands the user: -e, -t, and optionally --disable-web-ssh,
# --disable-auto-update, --ignore-unsafe-cert. Anything it does not recognise as an
# installer option is passed through to the agent and written into the service, so
# the command the panel shows works verbatim.
#
# Identity is -t <token> for a node that already exists, or --auto-discovery <key>
# for the panel's "add node" dialog, which has no token yet because the node does not
# exist until the agent registers. Both are accepted; requiring -t unconditionally
# made the add-node command fail with "no token".
#
# Installer-only options (consumed here, never passed to the agent):
#   --install-dir DIR            where to put the binary   (default /opt/nekomari-agent)
#   --install-service-name NAME  systemd/launchd unit name (default nekomari-agent)
#   --install-ghproxy URL        prefix for the download, for hosts that cannot
#                                reach GitHub directly
#   --version vX.Y.Z             pin a release instead of using the latest
#   --force                      reinstall even if an agent is already running
#
# Why this exists at all: the admin panel used to hand out upstream's installer
# URL, which installs the *upstream* agent from an archived project — without the
# flags this fork added. See docs/RELEASING.md.
set -euo pipefail

REPO="Aone2233/Nekomari"
INSTALL_DIR="/opt/nekomari-agent"
SERVICE_NAME="nekomari-agent"
GH_PROXY=""
VERSION=""
FORCE=0
AGENT_ARGS=()

log()  { printf '  %s\n' "$*"; }
warn() { printf '  !! %s\n' "$*" >&2; }
die()  { printf '  !! %s\n' "$*" >&2; exit 1; }

# --- parse ------------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --install-dir)          INSTALL_DIR="${2:?--install-dir needs a value}"; shift 2 ;;
    --install-dir=*)        INSTALL_DIR="${1#*=}"; shift ;;
    --install-service-name) SERVICE_NAME="${2:?--install-service-name needs a value}"; shift 2 ;;
    --install-service-name=*) SERVICE_NAME="${1#*=}"; shift ;;
    --install-ghproxy)      GH_PROXY="${2:-}"; shift 2 ;;
    --install-ghproxy=*)    GH_PROXY="${1#*=}"; shift ;;
    --version)              VERSION="${2:?--version needs a value}"; shift 2 ;;
    --version=*)            VERSION="${1#*=}"; shift ;;
    --force)                FORCE=1; shift ;;
    -h|--help)
      sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      # Everything else belongs to the agent: -e, -t, --disable-web-ssh, ...
      AGENT_ARGS+=("$1"); shift ;;
  esac
done

# The endpoint and token are what make the node useful; check them here so a typo
# fails now rather than as a silently offline node.
#
# Identity comes one of two ways, and the installer must accept both because the
# panel emits both:
#   -t <token>            the per-node command, for a node that already exists;
#   --auto-discovery <key>  the "add node" dialog, which has no token yet -- the node
#                           does not exist until the agent registers.
# Requiring -t unconditionally made the add-node command die with "no token: pass -t".
ENDPOINT=""
TOKEN=""
AUTO_DISCOVERY=""
for i in "${!AGENT_ARGS[@]}"; do
  case "${AGENT_ARGS[$i]}" in
    -e) ENDPOINT="${AGENT_ARGS[$((i+1))]:-}" ;;
    -e=*) ENDPOINT="${AGENT_ARGS[$i]#*=}" ;;
    -t) TOKEN="${AGENT_ARGS[$((i+1))]:-}" ;;
    -t=*) TOKEN="${AGENT_ARGS[$i]#*=}" ;;
    --auto-discovery) AUTO_DISCOVERY="${AGENT_ARGS[$((i+1))]:-}" ;;
    --auto-discovery=*) AUTO_DISCOVERY="${AGENT_ARGS[$i]#*=}" ;;
  esac
done
[ -n "$ENDPOINT" ] || die "no endpoint: pass -e https://your-panel"
if [ -n "$AUTO_DISCOVERY" ]; then
  log "identity: --auto-discovery (the agent registers and saves the token in"
  log "          ${INSTALL_DIR}/auto-discovery.json; that file must survive, or the"
  log "          next start registers a second node)"
else
  [ -n "$TOKEN" ] || die "no token: pass -t <token from the panel> (or --auto-discovery <key> to enrol a new node)"
fi

# --- platform ---------------------------------------------------------------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)  ;;
  darwin) ;;
  *) die "unsupported OS '$OS'; this installer handles linux and macOS. On Windows use install-node-agent.ps1" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)   ARCH=amd64 ;;
  aarch64|arm64)  ARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
ASSET="komari-agent-${OS}-${ARCH}"
log "platform ${OS}/${ARCH}"

# --- privileges -------------------------------------------------------------
# Nodes are not always root; several hand out a normal user with passwordless
# sudo. Every privileged step goes through $SUDO, which is empty when already root.
if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  sudo -n true 2>/dev/null || die "not root and passwordless sudo is unavailable; re-run with sudo"
  SUDO="sudo -n"
fi

# --- already installed? -----------------------------------------------------
# Two agents on one host means two nodes in the panel for one machine.
if [ "$FORCE" -ne 1 ]; then
  for unit in "$SERVICE_NAME" komari-agent nekomari-agent; do
    if $SUDO systemctl is-active "$unit" >/dev/null 2>&1; then
      die "$unit is already running; pass --force to replace it"
    fi
  done
fi

# --- version ----------------------------------------------------------------
if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
             | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$VERSION" ] || die "could not determine the latest release; pass --version vX.Y.Z"
fi
log "version ${VERSION}"

BASE="https://github.com/${REPO}/releases/download/${VERSION}"
if [ -n "$GH_PROXY" ]; then
  BASE="${GH_PROXY%/}/${BASE#https://}"
  log "via proxy ${GH_PROXY}"
fi

# --- download + verify ------------------------------------------------------
TMP="$(mktemp -d)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

log "downloading ${ASSET}"
curl -fsSL -o "$TMP/$ASSET" "$BASE/$ASSET" || die "download failed from $BASE"
curl -fsSL -o "$TMP/SHA256SUMS.txt" "$BASE/SHA256SUMS.txt" || die "could not fetch SHA256SUMS.txt"

want="$(awk -v n="$ASSET" '$2==n{print $1}' "$TMP/SHA256SUMS.txt")"
[ -n "$want" ] || die "$ASSET is not listed in SHA256SUMS.txt"
got="$( (sha256sum "$TMP/$ASSET" 2>/dev/null || shasum -a 256 "$TMP/$ASSET") | awk '{print $1}')"
[ "$want" = "$got" ] || die "checksum mismatch for $ASSET (want $want, got $got)"
log "checksum verified"

# --- install ----------------------------------------------------------------
$SUDO mkdir -p "$INSTALL_DIR"
$SUDO install -m 0755 "$TMP/$ASSET" "$INSTALL_DIR/$ASSET"
BIN="$INSTALL_DIR/$ASSET"
log "installed to ${BIN}"

# The agent resolves this relative to the binary; keep it beside it so state does
# not land in whatever directory the service happens to start in.
ARGS_STR=""
for a in "${AGENT_ARGS[@]}"; do
  # Quote values that contain spaces or shell metacharacters.
  case "$a" in
    *[!A-Za-z0-9_@%+=:,./-]*) ARGS_STR="$ARGS_STR \"$(printf '%s' "$a" | sed 's/"/\\"/g')\"" ;;
    *) ARGS_STR="$ARGS_STR $a" ;;
  esac
done

if [ "$OS" = "linux" ]; then
  UNIT="/etc/systemd/system/${SERVICE_NAME}.service"
  $SUDO tee "$UNIT" >/dev/null <<UNIT_EOF
[Unit]
Description=Nekomari monitoring agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BIN}${ARGS_STR}
Restart=always
RestartSec=10
# Note for anyone adding hardening here: NoNewPrivileges=yes and PrivateTmp=yes
# each silently defeat a file capability (setcap cap_net_raw+ep) on the binary, so
# ICMP tasks fail with EPERM even though CapEff still reports CAP_NET_RAW. Leave
# both unset if this node runs ICMP monitoring as a non-root user.
# (No backticks in this block: the heredoc is unquoted so that the paths above
# interpolate, which means any backtick here would be executed as a subshell.)
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
UNIT_EOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable --now "$SERVICE_NAME"
  sleep 3
  state="$($SUDO systemctl is-active "$SERVICE_NAME" || true)"
  log "service ${SERVICE_NAME}: ${state}"
  [ "$state" = "active" ] || { $SUDO journalctl -u "$SERVICE_NAME" --no-pager -n 15 || true; die "service failed to start"; }
else
  PLIST="$HOME/Library/LaunchAgents/com.nekomari.${SERVICE_NAME}.plist"
  mkdir -p "$(dirname "$PLIST")"
  cat > "$PLIST" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.nekomari.${SERVICE_NAME}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${BIN}</string>$(for a in "${AGENT_ARGS[@]}"; do printf '\n    <string>%s</string>' "$a"; done)
  </array>
  <key>WorkingDirectory</key><string>${INSTALL_DIR}</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
PLIST_EOF
  launchctl unload "$PLIST" 2>/dev/null || true
  launchctl load "$PLIST"
  log "launchd agent loaded: ${PLIST}"
fi

echo
log "done. The node should appear in the panel within a minute."
