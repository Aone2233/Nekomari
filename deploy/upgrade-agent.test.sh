#!/usr/bin/env bash
# Offline tests for deploy/upgrade-agent.sh.
#
# Why these exist: this script is what turns a published release into the binary a
# node actually runs, and every failure mode it has is quiet. A replaced binary that
# never restarts is an "active" service running the old build; a lost file capability
# is ICMP loss on a node the panel shows as healthy; an OpenRC service left in the
# "crashed" state never comes back; a directory full of old binaries fills a small
# node's disk. Each case below pins one of them, and the healthy case has to come out
# with zero failures.
#
# The real script runs as a subprocess against fake tools: curl serves a release,
# getcap/setcap and rc-service record what they were asked to do, ps reports no
# process (so the inode evidence step is deterministic), and BIN= names the file to
# upgrade. Nothing here touches a real agent, a real service manager or the network.
set -uo pipefail

HERE=$(cd -- "$(dirname -- "$0")" && pwd)
UPGRADER="$HERE/upgrade-agent.sh"
if [ ! -f "$UPGRADER" ]; then
  printf 'FAIL cannot find %s\n' "$UPGRADER"
  exit 2
fi
BASH_BIN="${BASH:-bash}"
command -v "$BASH_BIN" >/dev/null 2>&1 || { printf 'SKIP bash not available\n'; exit 0; }

TMP=$(mktemp -d) || exit 2
trap 'rm -rf "$TMP"' EXIT
STUB="$TMP/bin"
mkdir -p "$STUB"

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf 'FAIL %s: %s\n' "$1" "$2"; }
check() { # name, haystack, needle
  case "$2" in
    *"$3"*) ok "$1" ;;
    *) bad "$1" "expected to find [$3] in: $2" ;;
  esac
}
check_absent() {
  case "$2" in
    *"$3"*) bad "$1" "did not expect [$3] in: $2" ;;
    *) ok "$1" ;;
  esac
}
expect() { # name, actual, wanted
  [ "$2" = "$3" ] && ok "$1" || bad "$1" "got [$2], want [$3]"
}

# --- fake tools ---------------------------------------------------------------------
#
# uname and ps are shimmed because the script branches on them: on Git Bash uname
# says MINGW64 (the script would refuse to run) and a real ps would make the
# "which binary is running" evidence depend on whatever else is on the host.
cat > "$STUB/uname" <<'SH'
#!/usr/bin/env bash
case "$1" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *)  printf 'unknown\n' ;;
esac
SH

cat > "$STUB/ps" <<'SH'
#!/usr/bin/env bash
# No agent process: the script then reports that it could not verify the running
# binary, which is a warning rather than a failure, deterministically.
exit 0
SH

cat > "$STUB/curl" <<'SH'
#!/usr/bin/env bash
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -fsSL|-fsS|-f|-s|-L) shift ;;
    http*) url="$1"; shift ;;
    *) shift ;;
  esac
done
printf '%s\n' "$url" >> "$CURL_LOG"
case "$url" in
  *SHA256SUMS.txt) printf '%s  komari-agent-linux-amd64\n' "$NEW_SHA" > "$out" ;;
  *komari-agent-linux-amd64) cp "$NEW_BINARY" "$out" ;;
  *) printf 'not found\n' >&2; exit 22 ;;
esac
SH

cat > "$STUB/getcap" <<'SH'
#!/usr/bin/env bash
printf '%s cap_net_raw=ep\n' "$1"
SH

cat > "$STUB/setcap" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SETCAP_LOG"
exit 0
SH

cat > "$STUB/rc-service" <<'SH'
#!/usr/bin/env bash
printf 'rc-service %s\n' "$*" >> "$RC_LOG"
action=""
for a in "$@"; do action="$a"; done
case "$action" in
  status)  if [ -f "$RC_STATE" ]; then printf ' * status: started\n'; exit 0; fi
           printf ' * status: crashed\n'; exit 3 ;;
  start|restart) : > "$RC_STATE"; exit 0 ;;
  stop|zap)      rm -f "$RC_STATE"; exit 0 ;;
esac
exit 0
SH

cat > "$STUB/rc-update" <<'SH'
#!/usr/bin/env bash
printf 'rc-update %s\n' "$*" >> "$RC_LOG"
exit 0
SH

cat > "$STUB/systemctl" <<'SH'
#!/usr/bin/env bash
# Lists nothing at all: a node with no unit that owns the agent.
exit 0
SH
chmod +x "$STUB"/*

# --- fixtures -----------------------------------------------------------------------
BASE="$TMP/case"
mkdir -p "$BASE/initd" "$BASE/out"
INIT_SCRIPT="$BASE/initd/nekomari-agent"

# make_binary <path> <version|noflag|broken>
#   <version>  answers --version with the given tag
#   noflag     an older agent: --version is an unknown flag, --help works
#   broken     does not execute successfully at all (wrong arch / wrong libc)
make_binary() {
  case "$2" in
    broken)
      printf '#!/usr/bin/env bash\nexit 1\n' > "$1"
      ;;
    noflag)
      printf '#!/usr/bin/env bash\nif [ "$1" = --version ]; then echo "unknown flag: --version" >&2; exit 1; fi\nexit 0\n' > "$1"
      ;;
    *)
      printf '#!/usr/bin/env bash\nif [ "$1" = --version ]; then echo "%s"; fi\nexit 0\n' "$2" > "$1"
      ;;
  esac
  chmod 0755 "$1"
}

# A release: the asset the download serves, and its real hash.
prepare_release() { # <version|noflag|broken>
  make_binary "$BASE/out/asset" "$1"
  NEW_BINARY="$BASE/out/asset"
  NEW_SHA=$(sha256sum "$NEW_BINARY" | awk '{print $1}')
  CURL_LOG="$BASE/out/curl.log"; RC_LOG="$BASE/out/rc.log"
  SETCAP_LOG="$BASE/out/setcap.log"; RC_STATE="$BASE/out/rc.state"
  : > "$CURL_LOG"; : > "$RC_LOG"; : > "$SETCAP_LOG"; : > "$RC_STATE"
}

# reset_case <current-binary-version|noflag>
#   A fresh BIN= plus the OpenRC init script that points at it, so the script's own
#   "which service owns this binary" search finds it.
reset_case() {
  rm -rf "$BASE/install"; mkdir -p "$BASE/install"
  BIN="$BASE/install/komari-agent-linux-amd64"
  make_binary "$BIN" "$1"
  printf '#!/sbin/openrc-run\ncommand="%s"\n' "$BIN" > "$INIT_SCRIPT"
  chmod 0755 "$INIT_SCRIPT"
  : > "$RC_STATE"
}

run_upgrader() { # args...
  env -i \
    PATH="$STUB:$PATH" \
    HOME="$BASE/home" \
    BIN="$BIN" \
    NEKOMARI_INIT="${FORCE_INIT:-openrc}" \
    NEKOMARI_INIT_DIR="$BASE/initd" \
    NEW_BINARY="$NEW_BINARY" \
    NEW_SHA="$NEW_SHA" \
    CURL_LOG="$CURL_LOG" \
    RC_LOG="$RC_LOG" \
    RC_STATE="$RC_STATE" \
    SETCAP_LOG="$SETCAP_LOG" \
    "$BASH_BIN" "$UPGRADER" "$@" 2>&1
}

# --- 1. already at the target version: --version short-circuits, no download -------
prepare_release v1.6.19
reset_case v1.6.18
out=$(run_upgrader --version v1.6.18); rc=$?
check "skip: says it is already there"     "$out" "已经是 v1.6.18，跳过"
expect "skip: exit code"                   "$rc" "0"
expect "skip: nothing was downloaded"      "$(cat "$CURL_LOG")" ""
check "skip: current version is read"      "$out" "当前版本：v1.6.18（--version）"

# --- 2. --dry-run reports the plan and changes nothing ----------------------------
reset_case v1.6.18
before=$(sha256sum "$BIN" | awk '{print $1}')
out=$(run_upgrader --version v1.6.19 --dry-run); rc=$?
check "dry run: names the download"        "$out" "komari-agent-linux-amd64"
check "dry run: names the backup"          "$out" "bak-v1.6.19-"
check "dry run: names the restart"         "$out" "openrc nekomari-agent"
check "dry run: says nothing changed"      "$out" "没有下载、没有替换、没有重启"
expect "dry run: nothing was downloaded"   "$(cat "$CURL_LOG")" ""
expect "dry run: the binary is untouched"  "$(sha256sum "$BIN" | awk '{print $1}')" "$before"
expect "dry run: no backup was written"    "$(ls "$BASE/install" | grep -c 'bak-')" "0"

# --- 3. the upgrade itself -------------------------------------------------------
# Everything the upgrade is supposed to do, in one case: replace, restore the file
# capability, restart per init, prune the old backups to one, and report the evidence.
prepare_release v1.6.19
reset_case v1.6.18
# Older backups in all three naming shapes the fleet has used.
make_binary "$BIN.bak-pre-v1.6.10" v1.6.10
make_binary "$BIN.pre-v1.6.9-20260101T000000Z" v1.6.9
make_binary "$BIN.bak-v1.6.8-20260101T000000Z" v1.6.8
out=$(run_upgrader --version v1.6.19); rc=$?
check "upgrade: replaces the binary"       "$out" "已替换"
check "upgrade: restores the capability"   "$(cat "$SETCAP_LOG")" "cap_net_raw=ep $BIN"
check "upgrade: restarts via rc-service"   "$(cat "$RC_LOG")" "rc-service nekomari-agent restart"
check "upgrade: reports the service state" "$out" "服务状态"
check "upgrade: warns when the running process cannot be seen" "$out" "没有找到正在运行的探针进程"
expect "upgrade: the new binary is in place" "$("$BIN" --version)" "v1.6.19"
expect "upgrade: exit code"                "$rc" "0"
backups=$(ls -1 "$BASE/install" | grep -c -E '\.(bak-|pre-v)')
expect "upgrade: only the newest backup is kept" "$backups" "1"
newest=$(ls -1 "$BASE/install" | grep -E '\.bak-v1\.6\.19-' || true)
[ -n "$newest" ] && ok "upgrade: the kept backup is this run's" \
  || bad "upgrade: the kept backup is this run's" "found: $(ls -1 "$BASE/install")"

# --- 4. SHA256 fallback when --version is unavailable ----------------------------
# An agent from before --version existed: the same idempotency question, answered by
# the published hash instead.
prepare_release noflag
reset_case noflag
# The installed copy has to be *different* from the release for the upgrade to run at
# all: same bytes would be the idempotent case below.
printf '\n# an older build\n' >> "$BIN"
out=$(run_upgrader --version v1.6.19); rc=$?
check "sha fallback: says the version is unknown" "$out" "改用哈希判断"
check "sha fallback: replaces anyway"             "$out" "已替换"
expect "sha fallback: exit code"                  "$rc" "0"

# 4b. and when the hashes already match, it is an idempotent no-op.
prepare_release noflag
reset_case noflag
cp "$NEW_BINARY" "$BIN"; chmod 0755 "$BIN"
out=$(run_upgrader --version v1.6.19); rc=$?
check "sha idempotent: reports the match"  "$out" "已经是目标版本，跳过"
expect "sha idempotent: exit code"         "$rc" "0"
expect "sha idempotent: service untouched" "$(cat "$RC_LOG")" ""

# --- 5. a corrupt download is refused before anything is replaced ----------------
prepare_release broken
reset_case v1.6.18
NEW_SHA=0000000000000000000000000000000000000000000000000000000000000000
before=$(sha256sum "$BIN" | awk '{print $1}')
out=$(run_upgrader --version v1.6.19); rc=$?
check "checksum: refuses a mismatch"       "$out" "校验和不匹配"
expect "checksum: exit code"               "$rc" "1"
expect "checksum: old binary untouched"    "$(sha256sum "$BIN" | awk '{print $1}')" "$before"

# --- 6. a binary that cannot run is refused too ---------------------------------
prepare_release broken
reset_case v1.6.18
before=$(sha256sum "$BIN" | awk '{print $1}')
out=$(run_upgrader --version v1.6.19); rc=$?
check "unrunnable: refuses before replacing" "$out" "跑不起来"
expect "unrunnable: exit code"               "$rc" "1"
expect "unrunnable: old binary untouched"    "$(sha256sum "$BIN" | awk '{print $1}')" "$before"
expect "unrunnable: no backup written"       "$(ls "$BASE/install" | grep -c 'bak-')" "0"

# --- 7. no service to restart is an error, not a silent success ------------------
prepare_release v1.6.19
reset_case v1.6.18
FORCE_INIT=systemd
out=$(run_upgrader --version v1.6.19); rc=$?
FORCE_INIT=""
check "no service: says no unit owns the agent" "$out" "没有任何单元"
expect "no service: exit code"                  "$rc" "1"

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
