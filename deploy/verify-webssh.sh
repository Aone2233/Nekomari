#!/usr/bin/env bash
# Verify every node's agent unit has web-ssh enabled, and that the agent is up.
set -uo pipefail
SSH_OPTS="-o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20"

check() {
  local label="$1"; shift
  local out
  out=$("$@" "grep -c -- '--disable-web-ssh' /etc/systemd/system/nekomari-agent.service 2>/dev/null || echo MISSING; systemctl is-active nekomari-agent.service 2>/dev/null || echo inactive" 2>&1)
  local flag state
  flag=$(echo "$out" | head -1)
  state=$(echo "$out" | tail -1)
  printf '  %-18s web-ssh-flag=%s  state=%s\n' "$label" "$flag" "$state"
}

echo "=== remote nodes ==="
check "华纳云HN-JP1" ssh $SSH_OPTS root@177.2.185.85
check "HK04"         ssh $SSH_OPTS root@82.152.161.202
check "AkkoCloud"    ssh $SSH_OPTS root@185.218.4.64
check "CloudLeadInno" ssh $SSH_OPTS root@192.220.32.17
echo
echo "=== IPv6-only node (from OC424) ==="
ssh $SSH_OPTS localhost "ssh $SSH_OPTS -6 root@2604:abc0:50::11:601e \"grep -c -- '--disable-web-ssh' /etc/systemd/system/nekomari-agent.service || true; systemctl is-active nekomari-agent.service\"" 2>&1 | sed 's/^/  Nomao-v6           /'
echo
echo "=== OC424 (host unit) ==="
grep -c -- '--disable-web-ssh' /etc/systemd/system/komari-agent-oc424-original-node.service 2>/dev/null | sed 's/^/  flag count: /'
systemctl is-active komari-agent-oc424-original-node.service | sed 's/^/  state: /'
