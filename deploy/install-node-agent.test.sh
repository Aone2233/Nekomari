#!/usr/bin/env bash
# Offline tests for deploy/install-node-agent.sh.
#
# Why these exist: the installer used to write the node's token straight into the
# unit's ExecStart, which puts it in /proc/<pid>/cmdline and `systemctl show -p
# ExecStart` for every local user to read. The fix is only worth anything if it
# holds for every spelling of the token argument, so each case below runs the real
# installer and inspects the unit it would write.
#
# Nothing here touches a real host: --install-dir, --install-token-file and a
# PATH shim keep every write inside the temp directory. The dry run writes the
# credential file and stops before the network, so no case needs a download.
set -uo pipefail

HERE=$(cd -- "$(dirname -- "$0")" && pwd)
INSTALLER="$HERE/install-node-agent.sh"
if [ ! -f "$INSTALLER" ]; then
  printf 'FAIL cannot find %s\n' "$INSTALLER"
  exit 2
fi
BASH_BIN=${BASH:-bash}
command -v "$BASH_BIN" >/dev/null 2>&1 || { printf 'SKIP bash not available\n'; exit 0; }

TMP=$(mktemp -d) || exit 2
trap 'rm -rf "$TMP"' EXIT

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

TOKEN='tok-abcdefghijklmnopqrst'
STEM=$(basename "$INSTALLER" .sh)
BASE="$TMP/$STEM"
mkdir -p "$BASE/install" "$BASE/bin" "$BASE/out"

# Git Bash and MSYS cannot enforce POSIX permission bits, so the mode assertions
# are skipped there and run on Linux CI. Everything else runs everywhere.
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) MODE_UNSUPPORTED=1 ;;
  *) MODE_UNSUPPORTED=0 ;;
esac

check_mode() { # name, path, expected
  if [ "$MODE_UNSUPPORTED" = "1" ]; then
    ok "$1 (skipped: this filesystem cannot report mode bits)"
    return
  fi
  if [ ! -e "$2" ]; then
    bad "$1" "not found at $2"
    return
  fi
  actual=$(stat -c '%a' "$2" 2>/dev/null || stat -f '%Lp' "$2")
  [ "$actual" = "$3" ] && ok "$1" || bad "$1" "got $actual, want $3"
}

# A `sudo` that simply runs the rest: the installer's writes are already confined
# to the temp directory by its own flags.
cat > "$BASE/bin/sudo" <<'SH'
#!/usr/bin/env bash
while [ $# -gt 0 ]; do
  case "$1" in
    -n) shift ;;
    --) shift; break ;;
    -*) shift ;;
    *) break ;;
  esac
done
exec "$@"
SH
cat > "$BASE/bin/systemctl" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat > "$BASE/bin/journalctl" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$BASE/bin/"*

# Case 4 needs a download. The expected hash is handed to the shim through the
# environment, so the installer's own `sha256sum` verification really runs.
cat > "$BASE/bin/curl" <<'SH'
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
case "$url" in
  *SHA256SUMS.txt) printf '%s  komari-agent-linux-amd64\n' "$FAKE_SHA" > "$out" ;;
  *) printf '#!/bin/sh\nexit 0\n' > "$out" ;;
esac
SH
chmod +x "$BASE/bin/curl"
FAKE_SHA=$(printf '#!/bin/sh\nexit 0\n' | sha256sum | awk '{print $1}')

cat > "$BASE/bin/tee" <<'SH'
#!/usr/bin/env bash
case "$1" in
  /etc/systemd/system/*) exec "$(dirname "$0")/write_unit" ;;
esac
exit 0
SH
cat > "$BASE/bin/write_unit" <<'SH'
#!/usr/bin/env bash
cat > "$UNIT_OUT"
SH
# The installer refuses to run on a non-Linux host, and Git Bash reports
# `mingw64_nt-*`. Only `uname -s` and `uname -m` are read, so the shim answers
# those and forwards everything else to the real binary. Without this, the case
# that inspects the written unit would silently not run on Windows.
cat > "$BASE/bin/uname" <<'SH'
#!/usr/bin/env bash
case "$1" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exec /usr/bin/uname "$@" ;;
esac
SH
chmod +x "$BASE/bin/tee" "$BASE/bin/write_unit" "$BASE/bin/uname"

run_installer() { # args...
  env -i \
    PATH="$BASE/bin:$PATH" \
    HOME="$BASE/home" \
    UNIT_OUT="$BASE/out/last-unit" \
    FAKE_SHA="$FAKE_SHA" \
    NEKOMARI_INSTALLER_DRY_RUN="${DRY:-0}" \
    "$BASH_BIN" "$INSTALLER" "$@" 2>&1
}

# ---------------------------------------------------------------------------
# 1. `-t` is moved into the credential file and replaced by --token-file.
DRY=1 out=$(run_installer --dry-run -e http://panel.test -t "$TOKEN" \
  --install-dir "$BASE/install" --version v9.9.9 --force)
check     "dry run reports the move"        "$out" "moved to $BASE/install/.agent-credentials"
check     "dry run passes --token-file"     "$out" "--token-file $BASE/install/.agent-credentials"
check_absent "dry run keeps the token out of the command line" "$out" "$TOKEN"
check_mode "credential file mode is 600" "$BASE/install/.agent-credentials" 600
if [ -f "$BASE/install/.agent-credentials" ]; then
  content=$(cat "$BASE/install/.agent-credentials")
  [ "$content" = "AGENT_TOKEN=$TOKEN" ] && ok "credential file holds the token" \
    || bad "credential file content" "got [$content]"
else
  bad "credential file written" "not found at $BASE/install/.agent-credentials"
fi

# 2. --token= is the same thing spelled differently.
rm -f "$BASE/install/.agent-credentials"
DRY=1 out=$(run_installer --dry-run -e http://panel.test --token="$TOKEN" \
  --install-dir "$BASE/install" --version v9.9.9 --force)
check     "--token= is handled"             "$out" "--token-file $BASE/install/.agent-credentials"
check_absent "--token= keeps the token out" "$out" "$TOKEN"

# 3. --install-token-file places the credential wherever the operator says.
CUSTOM="$TMP/custom/agent-credentials"
DRY=1 out=$(run_installer --dry-run -e http://panel.test -t "$TOKEN" \
  --install-dir "$BASE/install" --install-token-file "$CUSTOM" --version v9.9.9 --force)
check "custom credential path is used" "$out" "--token-file $CUSTOM"
[ -f "$CUSTOM" ] && ok "custom credential file exists" || bad "custom credential file" "not found"
check_mode "custom credential mode is 600" "$CUSTOM" 600

# 4. A re-run with no token reuses the credential an earlier install left.
DRY=1 out=$(run_installer --dry-run -e http://panel.test \
  --install-dir "$BASE/install" --version v9.9.9 --force)
check "re-run reuses the credential file" "$out" "--token-file $BASE/install/.agent-credentials"
check "re-run says so"                    "$out" "reusing $BASE/install/.agent-credentials"
check_absent "re-run has no token in the command" "$out" "$TOKEN"

# 5. A token that is neither given nor stored is still an error, not a node that
#    starts with no identity.
DRY=1 out=$(run_installer --dry-run -e http://panel.test \
  --install-dir "$TMP/empty-install" --version v9.9.9 --force)
check "missing identity fails" "$out" "no token: pass -t"

# 6. The unit actually written (not the dry run) carries --token-file and no token.
#    This is the case the report cares about: what `systemctl show` would print.
rm -f "$BASE/out/last-unit" "$BASE/install/.agent-credentials"
DRY=0 out=$(run_installer -e http://panel.test -t "$TOKEN" \
  --install-dir "$BASE/install" --version v9.9.9 --force)
if [ -f "$BASE/out/last-unit" ]; then
  unit=$(cat "$BASE/out/last-unit")
  check        "unit ExecStart uses --token-file" "$unit" "--token-file $BASE/install/.agent-credentials"
  check_absent "unit ExecStart has no token"      "$unit" "$TOKEN"
  check_absent "unit ExecStart has no bare -t"    "$unit" " -t "
  check        "unit keeps the other arguments"   "$unit" "-e http://panel.test"
else
  bad "unit written" "installer did not write a unit: $out"
fi
check_mode "installed credential mode is 600" "$BASE/install/.agent-credentials" 600

# ---------------------------------------------------------------------------
printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
