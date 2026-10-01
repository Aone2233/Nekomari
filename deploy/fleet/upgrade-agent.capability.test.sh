#!/bin/sh
# The upgrade script must preserve a binary's file capabilities across the replacement.
#
# ## The defect this guards against
#
# `cp` and `install` copy a file's content and mode and nothing else — never extended attributes. So replacing
# an agent binary silently drops any file capability on it, and the capability has to be re-applied by a
# privileged process afterwards.
#
# That is not hypothetical. One node runs its agent as a *user* unit (deliberately: the host has no
# passwordless sudo) so the agent is not root, and it needs `cap_net_raw` on its binary to open a raw socket
# for ICMP probes. A binary replacement dropped the capability, and the node began reporting
# `ICMP: neither a raw socket nor an unprivileged ping socket opens` — which the panel surfaces as a warning
# badge saying that ICMP loss there is a limitation of the tooling rather than the target's behaviour. Every
# other node runs its agent as root, where no file capability is needed, so nothing else broke; that is why it
# went unnoticed.
#
# `deploy/upgrade-agent.sh` had handled this for a long time ("恢复文件能力。这一步不能省"). The fleet script
# written later for roadmap H6 did not, and the node in question was upgraded through the new one. A fix that
# exists in one of two deployment paths is not a fix, so this test asserts it in the path that lacked it.
#
# ## What is asserted
#
# Read as text, because both scripts are shell whose behaviour on a real node cannot be exercised here. The
# assertions are deliberately about the *sequence* — capture, replace, restore — since a version that captured
# the capability and never restored it, or restored it before replacing, would pass a mere "mentions setcap"
# check while still losing ICMP.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
FLEET="$HERE/upgrade-agent.sh"
WORKSTATION="$HERE/../../deploy/upgrade-agent.sh"

fail() { printf '  FAIL: %s\n' "$*" >&2; exit 1; }
pass() { printf '  ok: %s\n' "$*"; }

for script in "$FLEET" "$WORKSTATION"; do
  [ -f "$script" ] || fail "$script is missing"

  grep -q 'getcap' "$script" \
    || fail "$script never reads the binary's capabilities, so a replacement drops them"

  grep -q 'setcap' "$script" \
    || fail "$script never restores capabilities, so a replacement drops them"

  # Capture must come before the replacement, and restore after it. Line numbers are enough for a sequence
  # assertion and avoid depending on the exact shell used to write the replacement.
  getcap_line=$(grep -n 'getcap' "$script" | head -1 | cut -d: -f1)
  setcap_line=$(grep -n 'setcap' "$script" | tail -1 | cut -d: -f1)

  # The replacement is the line that *writes* $BIN, which is not the same as the first line mentioning it: the
  # backup reads it (`cp -a "$BIN" "$BIN.pre-…"`) and comes first. Matching on the first mention picked the
  # backup and reported the capture as happening after the replacement — the assertion was right to fail, and
  # that is what it was failing on.
  #
  # Done with awk rather than a pipeline of greps: the exclusion needs alternation, and `grep -v 'a\|b'` is a
  # GNU basic-regex extension that dash's grep does not honour, so the filter silently matched nothing and the
  # line it was meant to drop stayed in. One pass, no alternation, no ambiguity.
  replace_line=$(awk '
    /^[[:space:]]*#/ { next }
    /\$BIN[.]new/ && /mv/ { print NR; exit }
    /(^|[^a-z])(cp|install|mv)[[:space:]].*\$BIN/ && !/\.pre-|\.bak/ && !/\$BIN[.]new/ { print NR; exit }
  ' "$script")
  [ -n "$replace_line" ] || fail "$script: could not locate the step that replaces \$BIN"

  [ "$getcap_line" -lt "$replace_line" ] \
    || fail "$script: capabilities are read (line $getcap_line) after the binary is replaced (line $replace_line); the old binary is gone by then"

  [ "$setcap_line" -gt "$replace_line" ] \
    || fail "$script: capabilities are restored (line $setcap_line) before the replacement (line $replace_line), so the replacement discards them again"

  pass "$(basename "$(dirname "$script")")/$(basename "$script"): capture $getcap_line < replace $replace_line < restore $setcap_line"
done

# The fleet script must also *report* ICMP availability, not merely preserve the capability.
#
# Preservation only helps the next upgrade. The node that broke was detected from a panel badge and explained
# by reading its journal by hand; the conclusion belongs in the upgrade output, where whoever ran it will see
# it. This asserts the report exists, and that it names the remedy for the node it cannot fix itself.
grep -q 'ICMP' "$FLEET" || fail "$FLEET does not report ICMP availability after an upgrade"
grep -q 'ping_group_range' "$FLEET" || fail "$FLEET does not name the non-capability remedy (ping_group_range)"
pass "fleet script reports ICMP availability and both remedies"

# And the report must not invent a failure it could not measure.
#
# Measured on JPKD2 (Alpine) after the v1.6.9 rollout: busybox `ps -o user=` prints nothing, `id -un 0` prints
# nothing either, and the node has no python3 — so the owner read as the empty string, which is not "root", and
# the check therefore ran for an agent that needs no capability, found no probe it could run, and reported
# UNAVAILABLE. The panel's own column for that node said `raw` before and after. The decision has to come from
# the uid in /proc (0 is root), and a node that cannot be probed has to say so.
grep -Fq "awk '/^Uid:/{print \$2}'" "$FLEET" || fail "$FLEET does not read the agent uid from /proc/status"
grep -Fq 'AGENT_UID" != "0"' "$FLEET" || fail "$FLEET decides 'is root' from something other than the uid"
grep -Fq 'not verified here' "$FLEET" || fail "$FLEET reports a missing python3 as an ICMP failure"
pass "fleet script decides root from the uid, and does not call an unmeasured node broken"

printf '\n  all capability-preservation assertions passed\n'
