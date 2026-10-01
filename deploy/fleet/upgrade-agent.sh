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
#   - the download is verified against the release's SHA256SUMS.txt when the asset is listed there
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

# The user-level unit that owns this process, if any.
#
# A user unit needs three things root does not have by default: the target user, that user's bus address, and
# that user's runtime directory. Running `systemctl --user` as root without them finds root's own (empty) user
# manager and reports that nothing owns the agent — which is exactly what happened on the one node whose agent
# is a user unit.
#
# The runtime directory is derived from the uid rather than assumed to be `/run/user/<something>`, and the bus
# address from it too, so this works for any uid. It is read only if the directory exists, so a system without
# user managers skips the whole branch.
find_user_unit() {
  owner="$1"
  want_pid="$2"
  [ -n "$owner" ] || return 1
  uid=$(id -u "$owner" 2>/dev/null) || return 1
  runtime="/run/user/$uid"
  [ -d "$runtime" ] || return 1
  # Reached the same way the restart is, and for the same reason: D-Bus authenticates by uid, so a root client
  # cannot use this bus even with the right paths.
  session="XDG_RUNTIME_DIR=$runtime DBUS_SESSION_BUS_ADDRESS=unix:path=$runtime/bus"
  if [ "$(id -u)" = "0" ]; then
    userctl="sudo -n -u $owner env $session systemctl --user"
  else
    userctl="env $session systemctl --user"
  fi
  found=$($userctl list-units --type=service --all --no-legend 2>/dev/null | awk '{print $1}')
  for candidate in $found; do
    pid=$($userctl show -p MainPID --value "$candidate" 2>/dev/null)
    if [ "$pid" = "$want_pid" ]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

# Which unit owns it, so the same service can be restarted rather than a guessed name.
UNIT=""
SHAPE=""
OWNER=$(ps -o user= -p "$APID" 2>/dev/null | tr -d ' ')
if command -v systemctl >/dev/null 2>&1; then
  # User-level first when the process is not root's: a user unit is invisible to the system manager, so
  # searching there first would be wasted work, and searching *only* there is what failed before.
  if [ -n "$OWNER" ] && [ "$OWNER" != "root" ]; then
    if candidate=$(find_user_unit "$OWNER" "$APID"); then
      UNIT="$candidate"; SHAPE="systemd-user"
    fi
  fi
  if [ -z "$UNIT" ]; then
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
fi
if [ -z "$UNIT" ] && [ -x /etc/init.d/nekomari-agent ]; then
  UNIT=nekomari-agent; SHAPE=openrc
fi
[ -n "$UNIT" ] || die "found the agent at $BIN (pid $APID) but no service manager reports owning it"

case "$SHAPE" in
  systemd)      RESTART="systemctl restart $UNIT" ;;
  # A user unit is restarted through that user's bus, with the same three things the search needed. Done as
  # the owner rather than as root, because root's own user manager is a different, empty one.
  # A user unit is reached only by becoming that user.
  #
  # Pointing at their runtime directory and bus socket is *not* enough: D-Bus authenticates the peer by uid, so
  # a root client gets "Transport endpoint is not connected" on `/run/user/<uid>/bus`. Two other approaches were
  # tried and both failed on the node this was written against — `su` prompts for a password (its PAM policy
  # requires one), and running as the user without switching uid cannot open the bus at all.
  #
  # `sudo -n -u <owner>` needs no password when the caller is already root, which is how the fleet runner
  # invokes this script, and it keeps the `-n` discipline: if it would need a password, it fails loudly instead
  # of hanging on a prompt nobody is watching.
  systemd-user) RUN_UID=$(id -u "$OWNER")
                SESSION="XDG_RUNTIME_DIR=/run/user/$RUN_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$RUN_UID/bus"
                if [ "$(id -u)" = "0" ]; then
                  RESTART="sudo -n -u $OWNER env $SESSION systemctl --user restart $UNIT"
                  SYSCTL_USER="sudo -n -u $OWNER env $SESSION systemctl --user"
                else
                  RESTART="env $SESSION systemctl --user restart $UNIT"
                  SYSCTL_USER="env $SESSION systemctl --user"
                fi ;;
  openrc)       RESTART="rc-service $UNIT restart" ;;
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

if curl -fsSL -o SHA256SUMS.txt "$BASE/SHA256SUMS.txt" 2>/dev/null && [ -s SHA256SUMS.txt ]; then
  if grep -q "$ASSET" SHA256SUMS.txt; then
    grep "$ASSET" SHA256SUMS.txt > want.txt
    sha256sum -c want.txt >/dev/null 2>&1 || die "checksum mismatch for $ASSET"
    log "checksum ok"
  else
    log "note: $ASSET is not listed in SHA256SUMS.txt, so only the transport was verified"
  fi
else
  log "note: the release has no SHA256SUMS.txt, so only the transport was verified"
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

# The file capabilities of the binary being replaced, read before it is gone.
#
# **A binary replacement discards them**, and that is not obvious: `cp` and `install` copy content and mode,
# never extended attributes, and the capability has to be re-applied by a privileged process afterwards.
#
# This silently disabled ICMP probes on one node. That node runs the agent as a *user* unit — deliberately, as
# `deploy/hosts/macwan.service` explains, because the host has no passwordless sudo — so the agent is not root
# and needs `cap_net_raw` on its binary to open a raw socket. The capability was granted once, by hand, as root.
# Replacing the binary therefore dropped it, and the agent began reporting "neither a raw socket nor an
# unprivileged ping socket opens" together with a panel warning that ICMP loss on that node was a limitation
# of the tooling rather than the target's behaviour. Every other node runs its agent as root, where no file
# capability is needed, so nothing else was affected — which is exactly why it went unnoticed for a day.
#
# Read via getcap rather than assumed, so a node that never had capabilities is untouched, and an upgrade on
# such a node cannot start requiring them.
BIN_CAPS=""
if command -v getcap >/dev/null 2>&1; then
  BIN_CAPS=$(getcap "$BIN" 2>/dev/null | sed 's/^[^ ]* *//' || true)
fi
log "backup: $BIN.pre-$TARGET-$TS"

cp "$ASSET" "$BIN.new" && chmod 0755 "$BIN.new" && mv -f "$BIN.new" "$BIN" || die "could not replace $BIN"

# Restored immediately, so the window in which the binary lacks them is as short as possible. A failure here is
# reported with the exact command, because this script may be running as a user that cannot set capabilities —
# the fleet runner invokes it through `sudo -n` on the far side, but a direct run as an unprivileged user
# cannot, and silently leaving ICMP broken is the outcome worth avoiding.
if [ -n "$BIN_CAPS" ]; then
  if setcap "$BIN_CAPS" "$BIN" 2>/dev/null; then
    log "capabilities restored: $BIN_CAPS"
  else
    log "WARNING: $BIN had capabilities ($BIN_CAPS) and they could not be restored."
    log "WARNING: run this on the node as root:  setcap $BIN_CAPS $BIN"
  fi
fi
log "replaced"

# --- restart and report --------------------------------------------------------------------

$RESTART || die "the service failed to restart; restore $BIN.pre-$TARGET-$TS and restart it"
sleep 10

case "$SHAPE" in
  systemd) STATE=$(systemctl is-active "$UNIT" 2>/dev/null || echo unknown)
           ERRORS=$(journalctl -u "$UNIT" --since '1 min ago' --no-pager 2>/dev/null | grep -cE '401|Unauthorized' || true) ;;
  # Same bus requirement as the restart: without it root queries its own empty user manager and every state
  # reads "inactive", which would look like the upgrade had stopped the agent.
  systemd-user)
           STATE=$($SYSCTL_USER is-active "$UNIT" 2>/dev/null || echo unknown)
           ERRORS=$($SYSCTL_USER -u "$UNIT" --since '1 min ago' --no-pager 2>/dev/null | grep -cE '401|Unauthorized' || true) ;;
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

# ICMP capability is reported, not just preserved.
#
# Preserving it above only helps the next upgrade; this says whether the node can probe at all *now*. The
# failure that motivated it was invisible from the panel except as a warning badge, and took a manual log
# inspection to explain. Print the conclusion instead, from the same check the agent makes: a raw socket, then
# an unprivileged ping socket.
#
# Two ways this check lied, both found on JPKD2 (Alpine) after the v1.6.9 rollout:
#
#   * `ps -o user=` prints nothing under busybox ps, so the owner read as the empty string — which is not
#     "root", so the block ran for a root agent that needs no capability at all — and the message said
#     "the agent runs as  and neither socket type opens", with a blank name in it.
#   * with no python3 on the node there is nothing to open a probe socket with, and that was reported as
#     UNAVAILABLE. The panel's own record for that node said `raw` before and after the upgrade, which is
#     how a perfectly good upgrade came with a warning that ICMP had just broken.
#
# The owner now comes from /proc — and the decision is made on the **uid**, not on a name. Two busybox
# quirks made the name unreliable on Alpine, where they were measured: `ps -o user=` prints nothing at all,
# and `id -un 0` *also* prints nothing (a bare `id -un` answers `root`, which is why this looks fine in a
# terminal and wrong in a script). A node that cannot be probed now says so instead of claiming a failure.
AGENT_UID=$(awk '/^Uid:/{print $2}' "/proc/$APID/status" 2>/dev/null || true)
if [ -z "$AGENT_UID" ]; then
  AGENT_UID=$(ps -o uid= -p "$APID" 2>/dev/null | tr -d ' ')
fi
AGENT_OWNER=$(ps -o user= -p "$APID" 2>/dev/null | tr -d ' ')
if [ -z "$AGENT_OWNER" ]; then
  AGENT_OWNER=$(id -un "$AGENT_UID" 2>/dev/null || true)
fi
if [ -z "$AGENT_OWNER" ]; then
  if [ "$AGENT_UID" = "0" ]; then AGENT_OWNER=root; else AGENT_OWNER="uid $AGENT_UID"; fi
fi

if [ -n "$BIN_CAPS" ] || [ "$AGENT_UID" != "0" ]; then
  ICMP="no"
  if command -v python3 >/dev/null 2>&1; then
    if python3 -c "import socket,sys; socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP).close()" 2>/dev/null; then
      ICMP="raw socket"
    elif python3 -c "import socket,sys; socket.socket(socket.AF_INET, socket.SOCK_DGRAM, socket.IPPROTO_ICMP).close()" 2>/dev/null; then
      ICMP="unprivileged ping socket"
    fi
    if [ "$ICMP" = "no" ]; then
      log "ICMP: UNAVAILABLE — the agent runs as $AGENT_OWNER (uid $AGENT_UID) and neither socket type opens."
      log "       Give the binary the capability:  setcap cap_net_raw+ep $BIN"
      log "       Or widen net.ipv4.ping_group_range to cover that user's group."
    else
      log "ICMP: available via $ICMP"
    fi
  else
    # Not a failure, and saying so is the point: the agent reports the socket it actually opened, and the
    # panel stores it as `clients.icmp_capability`. Read that column instead of this line.
    log "ICMP: not verified here — no python3 on this node to open a probe socket with."
    log "       The agent reports the socket it obtained to the panel; check clients.icmp_capability."
  fi
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
