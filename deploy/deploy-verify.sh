#!/usr/bin/env bash
# Verify that a fresh Nekomari deployment actually works, from the published
# artifacts only — no access to an existing installation, no source tree.
#
# This is the check that answers "can someone else deploy this?": download the
# release binaries, verify them against SHA256SUMS.txt, start the server, complete
# the first-run install through its API, confirm the panel's own routes serve the
# panel's own documents rather than a theme's, connect an agent, and confirm the
# node reports. Automatic runs use a throwaway directory; explicit workdirs retain
# each run's artifacts for inspection.
#
# It deliberately avoids any running instance: its own port, its own data
# directory. That is what makes it safe to run on a host that is already serving,
# and safe to run in CI right after a release is published.
#
# Usage: deploy-verify.sh [workdir] [port] [version]
#   workdir  optional parent for a fresh, retained run directory
#            default: a fresh mktemp -d that is removed on exit
#   port     default: 25799
#   version  default: the tag in the repo's latest release
set -uo pipefail

PORT="${2:-25799}"
REPO="Aone2233/Nekomari"

SRV=""; AG=""; WORK=""; OWN_WORK=0
cleanup() {
  [ -n "$AG" ] && kill "$AG" 2>/dev/null
  [ -n "$SRV" ] && kill "$SRV" 2>/dev/null
  sleep 1
  cd /
  if [ "$OWN_WORK" = 1 ]; then
    [ -n "$WORK" ] && rm -rf -- "$WORK"
  elif [ -n "$WORK" ]; then
    printf '  workdir retained: %s\n' "$WORK"
  fi
}
trap cleanup EXIT

if [ -n "${1:-}" ]; then
  mkdir -p -- "$1" || exit 1
  WORK_PARENT="$(cd -- "$1" && pwd -P)" || exit 1
  WORK="$(mktemp -d "${WORK_PARENT}/nekomari-deploy-verify.XXXXXX")" || exit 1
else
  WORK_PARENT="$(cd -- "${TMPDIR:-/tmp}" && pwd -P)" || exit 1
  OWN_WORK=1
  WORK="$(mktemp -d "${WORK_PARENT}/nekomari-deploy-verify.XXXXXX")" || exit 1
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

mkdir -- "$WORK/data" || exit 1
cd -- "$WORK" || exit 1

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

step "3. the published binaries report their version"
v=$("./nekomari-linux-amd64" --help 2>&1 | head -1)
case "$v" in *"$VERSION"*) ok "server banner shows $VERSION";; *) bad "banner: $v";; esac
# The agent has no --version (it answers "unknown flag"), so look at the injected
# string instead. This is the assertion that catches a version stamped as "main"
# (a manual release used to do exactly that -- see the resolve job in release.yml):
# that string is what the agent's self-updater compares against.
if grep -aq -- "$VERSION" komari-agent-linux-amd64; then
  ok "agent binary carries $VERSION"
else
  bad "agent binary does not carry $VERSION"
fi

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

step "7. the panel serves its own documents, not a theme's"
# The defect this step exists for: `/install` is a route of the panel's *front end*
# (`frontend/src/routes.ts`), not of a theme, and a theme's document has no such route. It used to be
# answered with whichever document the current theme provided, so a themed panel showed the theme's
# unknown-route page instead of the install screen — while `/admin` correctly returned the panel.
#
# The assertion is deliberately **positive**: only the panel's own front-end build carries this
# title, and the built-in default theme is now that same build (`script/embed-theme.mjs`). An
# exclusion ("not the installed theme") cannot work here — this instance installs no theme at all,
# so on it a theme document and the panel document are indistinguishable by exclusion. That is
# exactly how the original defect shipped: the built-in theme *was* the document being served, so
# an exclusion was satisfied while `/install` showed a theme's unknown-route page.
#
# The title stays literal because `serveIndex` pins a panel-owned path to the built-in front end and
# skips the sitename substitution on that branch; on any other path this response would say
# "Deploy Verify", the sitename installed below.
inst_code=$(curl -sS -o install.html -w '%{http_code}' "http://127.0.0.1:${PORT}/install" 2>/dev/null)
case "$inst_code" in
  200) ok "/install answers 200";;
  *)   bad "/install status: ${inst_code:-no response}";;
esac
inst_title=$(sed -n 's:.*<title>\([^<]*\)</title>.*:\1:p' install.html 2>/dev/null | head -1)
case "$inst_title" in
  "Nekomari Monitor") ok "/install serves the panel's own front end (title: ${inst_title})";;
  *) bad "/install did not serve the panel's own front end (title: ${inst_title:-none})";;
esac
# A title alone is not the whole criterion: a theme document that copies the panel's title would
# pass it. The entry *naming* is the second, independent stamp — `frontend/vite.config.ts` sets
# `entryFileNames: "assets/entry-[name]-[hash].js"`, while a theme build uses Vite's default
# `assets/index-<hash>.js` (the previous embedded theme did). The Go unit test that guards the same
# contract makes the same two-part argument (web/public/embedded_theme_test.go).
inst_entry=$(sed -n 's:.*type="module"[^>]*src="\([^"]*\)".*:\1:p' install.html 2>/dev/null | head -1)
case "$inst_entry" in
  /assets/entry-*) ok "/install loads the panel front end's entry naming (${inst_entry})";;
  "") bad "/install loads no module entry at all";;
  *) bad "/install loads ${inst_entry}; the panel's build names its entries /assets/entry-*";;
esac
# A document whose assets 404 is a blank page and no log line, so the entry it names is fetched too.
# This is the prefix rule (the archive holds the contents of `dist/`, not `dist/` itself) holding on
# the request path a browser actually takes.
if [ -n "$inst_entry" ]; then
  entry_code=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}${inst_entry}" 2>/dev/null)
  case "$entry_code" in
    200) ok "the document's entry bundle loads (${inst_entry})";;
    *)   bad "entry bundle ${inst_entry} answered ${entry_code:-no response}";;
  esac
fi

# `/database-recovery` is registered by the normal server as a 307 to `/`: the recovery UI belongs to
# its temporary restricted listener, so on the normal listener there is nothing to serve. Read
# *without* following the redirect — with `-L` this looks like an ordinary 200 landing page and the
# redirect, which is the contract, goes unnoticed. The status line and `Location` are read from the
# headers; the body of a 307 is not a document anyone should be judging.
dr_headers=$(curl -sS -o /dev/null -D - "http://127.0.0.1:${PORT}/database-recovery" 2>/dev/null | tr -d '\r')
dr_code=$(printf '%s\n' "$dr_headers" | sed -n '1s:^HTTP/[^ ]* \([0-9]\{3\}\).*:\1:p')
dr_loc=$(printf '%s\n' "$dr_headers" | sed -n 's|^[Ll]ocation: *||p' | head -1)
case "$dr_code" in
  307) ok "/database-recovery answers 307 without following it";;
  *)   bad "/database-recovery status: ${dr_code:-no response} (expected 307)";;
esac
case "$dr_loc" in
  "/") ok "/database-recovery Location: ${dr_loc}";;
  *)   bad "/database-recovery Location: ${dr_loc:-none} (expected /)";;
esac

# Existing behaviour, and the check that keeps this step from being satisfied by breaking `/admin`:
# the admin interface is its own build (`frontend/admin.html`), served from the archive's `admin/`
# subtree, and it must not follow the default theme.
admin_code=$(curl -sS -o admin.html -w '%{http_code}' "http://127.0.0.1:${PORT}/admin" 2>/dev/null)
case "$admin_code" in
  200) ok "/admin answers 200";;
  *)   bad "/admin status: ${admin_code:-no response}";;
esac
admin_title=$(sed -n 's:.*<title>\([^<]*\)</title>.*:\1:p' admin.html 2>/dev/null | head -1)
case "$admin_title" in
  "Nekomari") ok "/admin still serves the panel's admin interface (title: ${admin_title})";;
  *) bad "/admin did not serve the panel's admin interface (title: ${admin_title:-none})";;
esac

step "8. log in and create an agent token"
curl -fsS -c cookie.txt -X POST "http://127.0.0.1:${PORT}/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Deploy-Verify-2026"}' >/dev/null 2>&1
tok=$(curl -fsS -b cookie.txt -X POST "http://127.0.0.1:${PORT}/api/rpc2" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"admin:addClient","params":{"name":"verify-node"},"id":1}' 2>/dev/null \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -n "$tok" ]; then ok "agent token issued"; else bad "could not create an agent token"; fi

step "9. the agent connects and the node reports"
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
  # End to end: the version the agent reports is the string its self-updater uses to
  # decide whether a newer release exists, so a wrong one is a silent update failure.
  av=$(curl -fsS -b cookie.txt -X POST "http://127.0.0.1:${PORT}/api/rpc2" \
         -H 'Content-Type: application/json' \
         -d '{"jsonrpc":"2.0","method":"admin:listClients","params":{},"id":1}' 2>/dev/null \
       | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
  case "$av" in "$VERSION") ok "agent reports $VERSION";; *) bad "agent reports version '${av:-none}', expected $VERSION";; esac
  kill $AG 2>/dev/null; AG=""
fi

step "10. shutdown and cleanup"
kill $SRV 2>/dev/null; SRV=""
sleep 1
printf '\n===== %d passed, %d failed =====\n' "$pass" "$fail"
[ "$fail" = 0 ] || exit 1
