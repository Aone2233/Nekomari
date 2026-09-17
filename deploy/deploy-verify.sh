#!/usr/bin/env bash
# Verify that a fresh Nekomari deployment actually works, from the published
# artifacts only — no access to an existing installation, no source tree.
#
# This is the check that answers "can someone else deploy this?": download the
# release binaries, verify them against SHA256SUMS.txt, start the server, complete
# the first-run install through its API, connect an agent, and confirm the node
# reports. Everything lives under one throwaway directory so cleanup is one rm -rf.
#
# It deliberately avoids any running instance: its own port, its own data
# directory. That is what makes it safe to run on a host that is already serving,
# and safe to run in CI right after a release is published.
#
# Usage: deploy-verify.sh [workdir] [port] [version]
#   workdir  default: a fresh mktemp -d
#   port     default: 25799
#   version  default: the tag in the repo's latest release
set -uo pipefail

PORT="${2:-25799}"
REPO="Aone2233/Nekomari"

if [ -n "${1:-}" ]; then
  WORK="$1"
  OWN_WORK=0
else
  WORK="$(mktemp -d "${TMPDIR:-/tmp}/nekomari-deploy-verify.XXXXXX")"
  OWN_WORK=1
fi

if [ -n "${3:-}" ]; then
  VERSION="$3"
else
  # Resolve the latest tag from the API rather than hardcoding one, so the script
  # keeps working across releases without edits.
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
fi
[ -n "$VERSION" ] || { echo "could not determine a release version"; exit 1; }

BASE="https://github.com/${REPO}/releases/download/${VERSION}"

pass=0; fail=0
ok()   { printf '  PASS  %s\n' "$*"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$*"; fail=$((fail+1)); }
step() { printf '\n=== %s ===\n' "$*"; }

SRV=""; AG=""
cleanup() {
  [ -n "$AG" ] && kill "$AG" 2>/dev/null
  [ -n "$SRV" ] && kill "$SRV" 2>/dev/null
  sleep 1
  cd /
  # Remove the directory only when we created it; a caller-supplied workdir is
  # theirs to manage. The condition was inverted in the first version, so the
  # throwaway directory survived every run and a caller-supplied one was deleted.
  # Always runs, including when a check above failed -- a CI job that leaves state
  # behind on failure is worse than the failure itself.
  [ "$OWN_WORK" = 1 ] && rm -rf "$WORK"
}
trap cleanup EXIT

rm -rf "$WORK"; mkdir -p "$WORK/data"; cd "$WORK" || exit 1

step "1. download the published artifacts (${VERSION})"
for f in nekomari-linux-amd64 komari-agent-linux-amd64 SHA256SUMS.txt; do
  if curl -fsSL -o "$f" "$BASE/$f"; then ok "downloaded $f"; else bad "download $f"; fi
done
chmod +x nekomari-linux-amd64 komari-agent-linux-amd64 2>/dev/null

step "2. verify checksums against SHA256SUMS.txt"
if [ -f SHA256SUMS.txt ]; then
  for f in nekomari-linux-amd64 komari-agent-linux-amd64; do
    want=$(awk -v n="$f" '$2==n{print $1}' SHA256SUMS.txt)
    got=$(sha256sum "$f" | awk '{print $1}')
    if [ -n "$want" ] && [ "$want" = "$got" ]; then ok "$f checksum matches"; else bad "$f checksum (want ${want:-none} got $got)"; fi
  done
else
  bad "no SHA256SUMS.txt to verify against"
fi

step "3. the server reports its version"
v=$("./nekomari-linux-amd64" --help 2>&1 | head -1)
case "$v" in *"$VERSION"*) ok "banner shows $VERSION";; *) bad "banner: $v";; esac

step "4. first run serves the install guide"
./nekomari-linux-amd64 server -l "127.0.0.1:${PORT}" > server.log 2>&1 &
SRV=$!
for _ in $(seq 1 30); do
  sleep 1
  curl -fsS "http://127.0.0.1:${PORT}/api/install/status" >/dev/null 2>&1 && break
done
st=$(curl -fsS "http://127.0.0.1:${PORT}/api/install/status" 2>/dev/null)
case "$st" in *'"required":true'*) ok "install guide is active";; *) bad "install status: ${st:-no response}";; esac

step "5. complete the install through the API"
body='{"username":"admin","password":"Deploy-Verify-2026","sitename":"Deploy Verify","description":"throwaway","metric_dsn":"./data/metrics.db"}'
resp=$(curl -fsS -X POST "http://127.0.0.1:${PORT}/api/install/complete" \
        -H 'Content-Type: application/json' -d "$body" 2>&1)
case "$resp" in *success*) ok "install completed";; *) bad "install: $resp";; esac
sleep 3

step "6. the panel serves its public API"
pub=$(curl -fsS "http://127.0.0.1:${PORT}/api/public" 2>/dev/null)
case "$pub" in *'"sitename":"Deploy Verify"'*) ok "sitename applied";; *) bad "public api: ${pub:0:120}";; esac
case "$pub" in *'"theme"'*) ok "theme reported";; *) bad "no theme in public api";; esac

step "7. log in and create an agent token"
curl -fsS -c cookie.txt -X POST "http://127.0.0.1:${PORT}/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Deploy-Verify-2026"}' >/dev/null 2>&1
tok=$(curl -fsS -b cookie.txt -X POST "http://127.0.0.1:${PORT}/api/rpc2" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"admin:addClient","params":{"name":"verify-node"},"id":1}' 2>/dev/null \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -n "$tok" ]; then ok "agent token issued"; else bad "could not create an agent token"; fi

step "8. the agent connects and the node reports"
if [ -n "$tok" ]; then
  ./komari-agent-linux-amd64 -e "http://127.0.0.1:${PORT}" -t "$tok" -i 3 \
    --disable-auto-update --disable-web-ssh > agent.log 2>&1 &
  AG=$!
  sleep 20
  if grep -q "WebSocket connected" agent.log; then ok "agent connected over WebSocket"; else bad "agent did not connect"; fi
  uuid=$(curl -fsS -b cookie.txt -X POST "http://127.0.0.1:${PORT}/api/rpc2" \
           -H 'Content-Type: application/json' \
           -d '{"jsonrpc":"2.0","method":"admin:listClients","params":{},"id":1}' 2>/dev/null \
         | sed -n 's/.*"uuid":"\([^"]*\)".*/\1/p')
  rec=$(curl -fsS -b cookie.txt "http://127.0.0.1:${PORT}/api/recent/${uuid}" 2>/dev/null)
  case "$rec" in *'"cpu"'*) ok "node is reporting metrics";; *) bad "no live report (uuid=${uuid:-none})";; esac
  kill $AG 2>/dev/null; AG=""
fi

step "9. shutdown and cleanup"
kill $SRV 2>/dev/null; SRV=""
sleep 1
printf '\n===== %d passed, %d failed =====\n' "$pass" "$fail"
[ "$fail" = 0 ] || exit 1
