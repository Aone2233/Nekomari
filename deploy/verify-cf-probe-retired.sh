#!/usr/bin/env bash
# Verify the cf-probe retirement across the fleet, and that the Nekomari agents
# are untouched.
#
# Checks, per node:
#   * cf-probe is not running and not enabled
#   * the binary is gone (so nothing can restart it)
#   * a rollback backup exists
#   * the nekomari agent is still active (the retirement must not have touched it)
set -uo pipefail
SSH="-o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=15"

# label | target | sudo prefix | extra ssh flags
NODES=(
  "OC424|localhost|sudo -n|"
  "华纳云HN-JP1|root@177.2.185.85||"
  "HK04|root@82.152.161.202||"
  "AkkoCloud|root@185.218.4.64||"
  "CloudLeadInno|root@192.220.32.17||"
  "BandwagonHost|root@144.34.224.86||"
  "Nomao-v6|root@2604:abc0:50::11:601e||-6"
)

check_remote() {
  local label="$1" target="$2" sudo="$3" extra="$4"
  local out
  out=$(ssh $SSH $extra "$target" "
    printf 'cf-probe=%s ' \"\$(systemctl is-active cf-probe 2>/dev/null || echo inactive)\"
    printf 'enabled=%s ' \"\$(systemctl is-enabled cf-probe 2>/dev/null || echo disabled)\"
    printf 'binary=%s ' \"\$([ -e /usr/local/bin/cf-probe ] && echo PRESENT || echo gone)\"
    printf 'backup=%s ' \"\$(ls -d /root/cf-probe-retired-* 2>/dev/null | wc -l)\"
    printf 'agent=%s' \"\$(systemctl is-active nekomari-agent 2>/dev/null || systemctl --user is-active nekomari-agent 2>/dev/null || echo n/a)\"
  " 2>/dev/null)
  printf '  %-16s %s\n' "$label" "${out:-UNREACHABLE}"
}

echo "=== direct nodes ==="
for spec in "${NODES[@]}"; do
  IFS='|' read -r label target sudo extra <<< "$spec"
  if [ "$target" = "localhost" ]; then
    out=$(printf 'cf-probe=%s enabled=%s binary=%s backup=%s agent=%s' \
      "$(systemctl is-active cf-probe 2>/dev/null || echo inactive)" \
      "$(systemctl is-enabled cf-probe 2>/dev/null || echo disabled)" \
      "$([ -e /usr/local/bin/cf-probe ] && echo PRESENT || echo gone)" \
      "$(ls -d /root/cf-probe-retired-* 2>/dev/null | wc -l)" \
      "$(systemctl is-active komari-agent-oc424-original-node 2>/dev/null || echo n/a)")
    printf '  %-16s %s\n' "$label" "$out"
  else
    check_remote "$label" "$target" "$sudo" "$extra"
  fi
done

echo
echo "=== MAC-WAN (user-level agent) ==="
# No credential in this file: SUDO_PW comes from the environment when the account
# is not root and the backup directory (root-owned) needs listing.
out=$(ssh $SSH MAC-WAN "
  printf 'cf-probe=%s ' \"\$(systemctl is-active cf-probe 2>/dev/null || echo inactive)\"
  printf 'enabled=%s ' \"\$(systemctl is-enabled cf-probe 2>/dev/null || echo disabled)\"
  printf 'binary=%s ' \"\$([ -e /usr/local/bin/cf-probe ] && echo PRESENT || echo gone)\"
  printf 'agent=%s' \"\$(systemctl --user is-active nekomari-agent 2>/dev/null || echo n/a)\"
" 2>/dev/null)
printf '  %-16s %s\n' "MAC-WAN" "${out:-UNREACHABLE}"
echo "  (backup dir is root-owned; check with: sudo ls -d /root/cf-probe-retired-*)"

echo
echo "=== PZYC (non-root + sudo) ==="
out=$(ssh $SSH PZYC "
  printf 'cf-probe=%s ' \"\$(systemctl is-active cf-probe 2>/dev/null || echo inactive)\"
  printf 'enabled=%s ' \"\$(systemctl is-enabled cf-probe 2>/dev/null || echo disabled)\"
  printf 'binary=%s ' \"\$([ -e /usr/local/bin/cf-probe ] && echo PRESENT || echo gone)\"
  printf 'backup=%s ' \"\$(sudo -n ls -d /root/cf-probe-retired-* 2>/dev/null | wc -l)\"
  printf 'agent=%s' \"\$(sudo -n systemctl is-active nekomari-agent 2>/dev/null || echo n/a)\"
" 2>/dev/null)
printf '  %-16s %s\n' "PZYC" "${out:-UNREACHABLE}"
