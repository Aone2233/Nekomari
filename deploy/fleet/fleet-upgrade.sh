#!/bin/sh
# Upgrade every node's real agent, one at a time, reporting each outcome.
#
# Run **from the panel host**, whose `~/.ssh/config` holds the node aliases. Roadmap H6.
#
#     ./deploy/fleet/fleet-upgrade.sh [version]
#
# ## Why one node at a time, with a pause
#
# Each node's agent is restarted, so each node stops reporting for a few seconds. Moving them together
# would blank the panel's view of the whole fleet at once, and if the new build had a problem there would
# be nothing healthy left to notice it from. Sequential with a pause also means a failure names the node it
# happened on while the ones already done stay up.
#
# ## What it does not touch
#
# Nodes recorded as `legacy` or `neither` in `inventory.txt` are skipped and listed at the end. They need a
# migration or an investigation rather than an upgrade, and the batch script doing either silently is how
# the earlier incident happened.
#
# ## Verification
#
# The per-node script proves the new binary *executes* before replacing anything, keeps the old one, and
# reports the service state and recent authentication errors afterwards. This adds a fleet-level check: the
# nodes it changed are listed so the panel's own report can be compared against them. The panel is the
# authority on which version a node reports, and `admin:agentVersions` answers that — see the note at the
# end of this file.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INVENTORY="$HERE/inventory.txt"
TARGET="${1:-}"
PAUSE="${FLEET_PAUSE:-20}"

[ -f "$INVENTORY" ] || { echo "no inventory at $INVENTORY" >&2; exit 1; }

UPGRADED=""
SKIPPED=""
FAILED=""
UNREACHABLE=""

while read -r host shape _rest <&3; do
  case "$host" in ''|\#*) continue ;; esac
  case "$shape" in
    legacy|neither|local|elsewhere)
      SKIPPED="$SKIPPED $host($shape)"
      continue
      ;;
  esac

  echo "=== $host ($shape) ==="

  # Reached over ssh, and a node that cannot be reached is a failure rather than a success.
  #
  # The first version ran the per-node script *locally*, which on the panel host meant it ran on the panel
  # host for every row: nodes it could not reach "upgraded" the panel's own agent again, and because the
  # output was piped through `sed` the pipeline's exit status was sed's, so every row counted as upgraded.
  # The summary then claimed nine nodes had been brought to the target while two of them had not been
  # touched at all — the failure mode this whole script exists to avoid, produced by the script itself.
  #
  # `set -o pipefail` is not available in POSIX sh, so the status is captured from the ssh alone and the
  # output is filtered afterwards.
  # Run this script as the user whose ssh keys reach the nodes — **not** as root.
  #
  # The per-node script needs root on the far side (see its header), and that is arranged by the `sudo -n`
  # in the command below. Running the *batch* as root does not work: the node aliases live in the invoking
  # user's ssh configuration, and its `Host *` block uses `~/.ssh/id_*`, which root expands to
  # `/root/.ssh/id_*` — files that do not exist. The first attempt produced "Permission denied (publickey)"
  # for every node; the second, with `-F` pointing at the right config, still failed for the same reason,
  # because `-F` changes which config is read and not what `~` means.
  #
  # Refusing to run as root rather than trying to work around it: silently falling back would leave the
  # operator unsure which keys were used, and an unexplained "unreachable" on a node they can ssh into is
  # worse than a refusal that says why.
  if [ "$(id -u)" = "0" ]; then
    echo "run this as the user whose ssh keys reach the nodes, not as root:" >&2
    echo "    ./fleet-upgrade.sh [version]        # it uses sudo on each node itself" >&2
    exit 1
  fi

  SSH="ssh -o BatchMode=yes -o ConnectTimeout=45 -o StrictHostKeyChecking=accept-new"
  export SSH

  if ! $SSH "$host" true </dev/null 2>/dev/null; then
    echo "  unreachable from this host; not attempted"
    UNREACHABLE="$UNREACHABLE $host"
    echo
    continue
  fi

  # `</dev/null` on the ssh call for the same reason the loop reads from fd 3: an ssh client consumes the
  # stdin it inherits, and without this it swallowed the rest of the inventory list after the first host. The
  # symptom was a batch run that reported "upgraded: AKKO06" and stopped — a one-node fleet, silently.
  OUT=$($SSH "$host" 'sudo -n sh -s' < "$HERE/upgrade-agent.sh" 2>&1)
  STATUS=$?
  echo "$OUT" | sed 's/^/  /'

  if [ "$STATUS" -eq 0 ]; then
    UPGRADED="$UPGRADED $host"
  else
    # Not fatal, and deliberately so: the fleet is ten nodes and one bad build should not stop the others
    # being brought to the same version. The failure is reported in the summary and the node keeps running
    # its previous binary, because the per-node script only replaces after the new one is proven to run.
    FAILED="$FAILED $host"
  fi
  echo
  sleep "$PAUSE"
done 3< "$INVENTORY"

echo "-----------------------------------------------"
[ -n "$UPGRADED" ] && echo "upgraded:$UPGRADED"
[ -n "$SKIPPED" ]  && echo "skipped (needs a migration or investigation, not an upgrade):$SKIPPED"
[ -n "$UNREACHABLE" ] && echo "unreachable from this host (deploy these separately):$UNREACHABLE"
[ -n "$FAILED" ]   && echo "FAILED:$FAILED"

if [ -n "$FAILED" ]; then
  exit 1
fi

cat <<'NOTE'

Next: confirm from the panel, not from this script. The panel knows which version each node reports, which
is the only answer that reflects a node actually running and authenticating:

    admin:agentVersions        # every node's reported version and whether it is connected

A node this script upgraded but that the panel still reports at the old version is a node whose agent
restarted but could not authenticate — which is exactly the state the legacy `komari-agent` services were in
on this fleet for a week before anyone looked.
NOTE
