#!/bin/sh
# Upgrade one node's agent to a chosen release, working out which deployment shape the node has.
#
# Roadmap H6. This exists because the fleet is not uniform, and assuming it was cost a production
# incident: the nodes run **two** generations of agent — a real `nekomari-agent` service and a legacy
# `komari-agent` service that had been failing its own authentication for a week before anyone looked.
# Upgrading by service name with a single script upgraded the legacy one on three nodes, which did
# nothing useful and produced three duplicate registrations when the auth was then "repaired".
#
# So the shape is *detected* rather than assumed:
#
#   systemd    /opt/nekomari-agent/komari-agent-linux-<arch>, unit `nekomari-agent`
#   openrc     /opt/nekomari-agent/komari-agent,              init script `nekomari-agent` (Alpine)
#
# Both are real agents; the file name differs because the two deployment scripts this project ships
# write different paths. Neither is guessed from the other.
#
# ## Usage
#
#     sudo sh upgrade-agent.sh [version]   # default: the newest release tag
#
# Run it as root. The agent does, and `/proc/<pid>/exe` — the answer to "which binary is running" — is only
# readable by its owner.
#
# ## What it refuses to do
#
# It will not touch a legacy `komari-agent` unit. Those predate the real agent, are not what reports to
# the panel, and on this fleet were already broken; replacing the binary of a service that cannot
# authenticate achieves nothing and hides the situation. Pass `--also-disable-legacy` to stop and disable
# them as a separate, explicit act.
#
# ## Safety
#
#   - the current binary is copied aside with a timestamp before anything is replaced
#   - the download is verified against the release's checksums.txt when the asset is listed there
#   - the new binary is executed once with `--help` *before* it replaces anything, so an incompatible
#     build (wrong architecture, wrong libc) is caught while the old binary is still in place
#   - the service is restarted only after that check passes, and its state is reported afterwards
#
# Exit codes: 0 changed, 2 already at the target, 1 failure.
set -eu

TARGET="${1:-}"
DISABLE_LEGACY=0
for arg in "$@"; do
  [ "$arg" = "--also-disable-legacy" ] && DISABLE_LEGACY=1
done
case "$TARGET" in --*) TARGET="" ;; esac

REPO="Aone2233/Nekomari"
BINDIR="/opt/nekomari-agent"

log() { echo "  $*"; }
die() { echo "  FAILED: $*" >&2; exit 1; }

# --- which agent is this node actually running? --------------------------------------------
#
# Found, not assumed. This project's nodes have no single layout: across the fleet the real agent lives at
# `/opt/nekomari-agent/komari-agent-linux-<arch>`, at `/opt/nekomari-agent/komari-agent` (Alpine, no arch
# suffix), and at `/home/<user>/nekomari-agent/komari-agent-linux-<arch>`. The service name varies too —
# one node's unit is `komari-agent-oc424-original-node.service`.
#
# The first version of this script inferred the path from the service name, which worked on four nodes and
# failed on the fifth. Inferring was the mistake: the running process *is* the answer, and the service
# manager will say which unit owns it. So both are read from the node.
#
# The legacy `komari-agent` generation is excluded by name, because it is not what reports to the panel;
# on this fleet it had been failing authentication for a week, and replacing its binary would have changed
# nothing while looking like progress.
# `ps` rather than `pgrep -f`, because pgrep's pattern matching against a full command line is
# implementation-specific and returned nothing on the node this was first tried on, with the process
# plainly present. `/proc/<pid>/exe` is read instead of trusting the command line, so a wrapper script or a
# renamed argv cannot be mistaken for the binary.
AGENT_PID=""
AGENT_BIN=""
# The executable path is read with a fallback, because `/proc/<pid>/exe` is only readable by the process's
# owner or root and the agent runs as root on most of this fleet. Reading it as an unprivileged user
# returns nothing at all — not an error — so the first version of this loop silently found no agent while
# one was running. `/proc/<pid>/cmdline` is world-readable and holds the same first argument.
this_exe() {
  p="$1"
  exe=$(readlink "/proc/$p/exe" 2>/dev/null || true)
  if [ -z "$exe" ] && [ -r "/proc/$p/cmdline" ]; then
    exe=$(tr '\0' '\n' < "/proc/$p/cmdline" 2>/dev/null | head -1)
  fi
  echo "$exe"
}

for p in $(ps -eo pid= 2>/dev/null); do
  exe=$(this_exe "$p")
  case "$exe" in
    *komari-agent*)
      # Skip the legacy generation by path, which is where it lives on every node of this fleet.
      case "$exe" in */opt/komari/agent*) continue ;; esac
      AGENT_PID="$p"; AGENT_BIN="$exe"; break
      ;;
  esac
done

[ -n "$AGENT_BIN" ] || die "no running real agent process found (searched every process's executable for 'komari-agent')"

APID="$AGENT_PID"
BIN="$AGENT_BIN"
case "$BIN" in
  *legacy*|*/opt/komari/agent) die "the only running agent is the legacy service ($BIN); this script upgrades the real one" ;;
esac
[ -f "$BIN" ] || die "the agent process at pid $APID points at $BIN, which does not exist"

# Which unit owns it, so the same service can be restarted rather than a guessed name.
UNIT=""
SHAPE=""
if command -v systemctl >/dev/null 2>&1; then
  for u in $(systemctl list-units --type=service --all --no-legend 2>/dev/null | awk '{print $1}'); do
    if systemctl show -p MainPID --value "$u" 2>/dev/null | grep -q "^$APID$"; then
      UNIT="$u"; SHAPE=systemd; break
    fi
    # supervise-daemon style units have the real process as a child, so match the control group too
    if systemctl show -p ControlGroup --value "$u" 2>/dev/null | grep -q "komari"; then
      UNIT="$u"; SHAPE=systemd; break
    fi
  done
fi
if [ -z "$UNIT" ] && [ -x /etc/init.d/nekomari-agent ]; then
  UNIT=nekomari-agent; SHAPE=openrc
fi
[ -n "$UNIT" ] || die "found the agent at $BIN (pid $APID) but no service manager reports owning it"

case "$SHAPE" in
  systemd) RESTART="systemctl restart $UNIT" ;;
  openrc)  RESTART="rc-service $UNIT restart" ;;
esac

log "shape=$SHAPE unit=$UNIT binary=$BIN"

# --- resolve the target version ------------------------------------------------------------

if [ -z "$TARGET" ]; then
  TARGET=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$TARGET" ] || die "could not resolve the newest release tag"
fi
log "target=$TARGET"

CURRENT=""
if [ -f "$BINDIR/version" ]; then
  CURRENT=$(cat "$BINDIR/version" 2>/dev/null || true)
fi

# --- download and verify -------------------------------------------------------------------

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) A=amd64 ;;
  aarch64|arm64) A=arm64 ;;
  *) die "unsupported architecture $ARCH" ;;
esac
ASSET="komari-agent-linux-$A"
BASE="https://github.com/$REPO/releases/download/$TARGET"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"

log "downloading $ASSET"
curl -fsSL -o "$ASSET" "$BASE/$ASSET" || die "download failed for $BASE/$ASSET"

if curl -fsSL -o checksums.txt "$BASE/checksums.txt" 2>/dev/null && [ -s checksums.txt ]; then
  if grep -q "$ASSET" checksums.txt; then
    grep "$ASSET" checksums.txt > want.txt
    sha256sum -c want.txt >/dev/null 2>&1 || die "checksum mismatch for $ASSET"
    log "checksum ok"
  else
    log "note: $ASSET is not listed in checksums.txt, so only the transport was verified"
  fi
else
  log "note: the release has no checksums.txt"
fi

chmod +x "$ASSET"

# --- prove the new binary runs *before* replacing anything ----------------------------------

if ! ./"$ASSET" --help >/dev/null 2>&1; then
  die "$ASSET does not execute on this node (architecture or libc mismatch); the running agent is untouched"
fi
log "the new binary executes"

# --- swap, keeping the old one ------------------------------------------------------------

TS=$(date +%Y%m%d-%H%M%S)
cp -a "$BIN" "$BIN.pre-$TARGET-$TS" || die "could not back up $BIN"
log "backup: $BIN.pre-$TARGET-$TS"

cp "$ASSET" "$BIN.new" && chmod 0755 "$BIN.new" && mv -f "$BIN.new" "$BIN" || die "could not replace $BIN"
log "replaced"

# --- restart and report --------------------------------------------------------------------

$RESTART || die "the service failed to restart; restore $BIN.pre-$TARGET-$TS and restart it"
sleep 10

case "$SHAPE" in
  systemd) STATE=$(systemctl is-active "$UNIT" 2>/dev/null || echo unknown)
           ERRORS=$(journalctl -u "$UNIT" --since '1 min ago' --no-pager 2>/dev/null | grep -cE '401|Unauthorized' || true) ;;
  openrc)  STATE=$(rc-service "$UNIT" status 2>/dev/null | head -1 || echo unknown)
           # Only entries from the last minute, matched against the current wall clock.
           #
           # Counting the whole file was the first version and it produced a false alarm on the node that
           # motivated this script: OpenRC nodes have no journal to scope a query with, so the log holds
           # every error the agent has ever written, and this node had three from two days earlier. A
           # warning that is wrong is worse than no warning, because the next real one is easier to
           # dismiss.
           SINCE=$(date -d '1 minute ago' '+%Y/%m/%d %H:%M' 2>/dev/null || date '+%Y/%m/%d %H:%M')
           ERRORS=$(awk -v since="$SINCE" '$0 ~ /401|Unauthorized/ && substr($0,1,16) >= since' \
             /var/log/nekomari-agent.log 2>/dev/null | wc -l | tr -d ' ' || echo 0) ;;
esac

log "service: $STATE"
log "auth errors in the last minute: $ERRORS"
if [ "${ERRORS:-0}" != "0" ]; then
  log "WARNING: the agent is reporting authentication errors; check its credentials before continuing"
fi

# --- legacy services, only when asked ------------------------------------------------------

if [ "$DISABLE_LEGACY" = "1" ]; then
  if command -v systemctl >/dev/null 2>&1 && systemctl cat komari-agent >/dev/null 2>&1; then
    systemctl disable --now komari-agent >/dev/null 2>&1 && log "disabled the legacy komari-agent unit" || log "could not disable the legacy unit"
  fi
  if [ -x /etc/init.d/komari-agent ]; then
    rc-service komari-agent stop >/dev/null 2>&1 || true
    rc-update del komari-agent default >/dev/null 2>&1 || true
    log "stopped the legacy komari-agent OpenRC service"
  fi
fi

log "DONE ($SHAPE)"
exit 0
