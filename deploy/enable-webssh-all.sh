#!/usr/bin/env bash
# Re-apply the agent unit on every node, dropping --disable-web-ssh.
#
# The first rollout passed --disable-web-ssh to every agent (the installer's
# default at the time), which disabled the panel's terminal — the agent flag
# covers "remote control (web ssh and rce)" as one switch. These nodes had it
# enabled before this fork's panel existed, so this restores that behaviour.
#
# Only the unit file changes; the agent binary is already installed on each node,
# so this is a rewrite + restart and takes seconds per host.
#
# Run from a machine that can reach all of them (OC424 works: it reaches the
# IPv6-only node and the hosts whose keys are not on the workstation).
set -uo pipefail

PANEL_URL="https://komari.orderly2233.org"

# host spec | token | label
NODES=(
  "root@177.2.185.85|4592o2ZyYKAq9L5sHo0e26|华纳云HN-JP1"
  "root@82.152.161.202|4xG13yTznvhPMyL5UnIoti|HK04"
  "root@185.218.4.64|nSbH5n5vC8b4MSJxCQ4g4d|AkkoCloud"
  "root@192.220.32.17|9SBzvhcdAUXBLvXBZjQ8yT|CloudLeadInno"
  "root@[2604:abc0:50::11:601e]|7kGbyB23YBvPwE4MrrEVlB|Nomao-v6"
)

SSH_OPTS="-o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20"

for spec in "${NODES[@]}"; do
  IFS='|' read -r host token label <<< "$spec"
  echo "== ${label} (${host}) =="

  read -r -d '' remote <<REMOTE
set -e
UNIT=/etc/systemd/system/nekomari-agent.service
grep -q -- '--disable-web-ssh' "\$UNIT" && {
  sed -i 's/ *--disable-web-ssh//' "\$UNIT"
  systemctl daemon-reload
  systemctl restart nekomari-agent.service
  echo "  unit updated, agent restarted"
} || echo "  flag already absent"
sleep 8
systemctl is-active nekomari-agent.service | sed 's/^/  state: /'
grep -c -- '--disable-web-ssh' "\$UNIT" | sed 's/^/  remaining flag count: /'
REMOTE

  ssh $SSH_OPTS "$host" "$remote" 2>&1 | sed 's/^/  /'
done

echo
echo "MAC-WAN and PZYC need their own paths (no passwordless sudo / user unit),"
echo "and OC424's unit is updated from deploy/hosts/oc424.service."
