#!/usr/bin/env bash
# Retire cf-probe on every node, from a host that can reach them all.
#
# Nodes are reached over SSH; two of them need a sudo password (the account is not
# root), so the script is piped in and run under sudo there. The IPv6-only node is
# reached through OC424, which has the route.
#
# Run this from OC424.
set -uo pipefail

SCRIPT=/tmp/retire-cf-probe.sh
[ -f "$SCRIPT" ] || { echo "missing $SCRIPT"; exit 1; }

# No default password: this file is committed, and a credential does not belong in
# it. Pass SUDO_PW in the environment for hosts whose account is not root.
SUDO_PW="${SUDO_PW:-}"
SSH="-o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20"

# label | ssh target | how to become root ("root" or "sudo")
NODES=(
  "华纳云HN-JP1|root@177.2.185.85|root"
  "HK04|root@82.152.161.202|root"
  "AkkoCloud|root@185.218.4.64|root"
  "CloudLeadInno|root@192.220.32.17|root"
  "BandwagonHost|root@144.34.224.86|root"
)

echo "########## direct nodes ##########"
for spec in "${NODES[@]}"; do
  IFS='|' read -r label target mode <<< "$spec"
  echo "== ${label} (${target}) =="
  if [ "$mode" = "root" ]; then
    ssh $SSH "$target" 'bash -s' < "$SCRIPT" 2>&1 | sed 's/^/  /'
  else
    ssh $SSH "$target" "sudo -S -p '' bash -s" < "$SCRIPT" 2>&1 | sed 's/^/  /'
  fi
done

echo
echo "########## Nomao (IPv6, via this host) ##########"
ssh $SSH -6 root@2604:abc0:50::11:601e 'bash -s' < "$SCRIPT" 2>&1 | sed 's/^/  /'
