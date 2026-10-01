#!/usr/bin/env bash
set -Eeuo pipefail
[[ $EUID == 0 ]]
stage=/tmp/nekomari-v1.6.4-20260930
target=/home/macos/nekomari-agent/komari-agent-linux-amd64
expected="$1"
[[ "$(readlink -f "$target")" == "$target" ]]
[[ "$(sha256sum "$stage/komari-agent-linux-amd64" | cut -d' ' -f1)" == "$expected" ]]
backup="/home/macos/nekomari-agent/backups/agent-before-v1.6.4-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -m 700 -p "$backup"
cp -a "$target" "$backup/komari-agent-linux-amd64"
cp -a /home/macos/.config/systemd/user/nekomari-agent.service "$backup/nekomari-agent.service"
cp -a /home/macos/nekomari-agent/.agent-credentials "$backup/.agent-credentials"
chmod 600 "$backup/.agent-credentials"
[[ ! -f /home/macos/nekomari-agent/net_static.json ]] || cp -a /home/macos/nekomari-agent/net_static.json "$backup/"
getcap "$target" > "$backup/capabilities.txt"
sha256sum "$target" /home/macos/.config/systemd/user/nekomari-agent.service
restore_binary() {
  local rc=$?
  trap - ERR
  cp -a "$backup/komari-agent-linux-amd64" "$target"
  setcap cap_net_raw+ep "$target"
  printf 'FAILED; previous binary restored; backup=%s; exit=%s\n' "$backup" "$rc" >&2
  exit "$rc"
}
trap restore_binary ERR
install -o root -g root -m 755 "$stage/komari-agent-linux-amd64" "$target.new"
setcap cap_net_raw+ep "$target.new"
[[ "$(getcap "$target.new")" == *cap_net_raw=ep ]]
mv -f "$target.new" "$target"
[[ "$(sha256sum "$target" | cut -d' ' -f1)" == "$expected" ]]
getcap "$target"
printf 'backup=%s\n' "$backup"
trap - ERR
