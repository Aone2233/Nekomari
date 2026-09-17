#!/usr/bin/env bash
# Confirm the cf-probe rollback material exists on every node.
#
# The backups live in /root/cf-probe-retired-<timestamp>/, so on hosts where the
# login user is not root this needs the sudo password. It is taken from
# SUDO_PW in the environment -- deliberately not stored in this file.
#
# A complete backup contains four files; anything less cannot restore the probe.
set -uo pipefail
SSH="-o ConnectTimeout=20 -o BatchMode=yes -o StrictHostKeyChecking=no"
PW="${SUDO_PW:-}"

# label | target | needs sudo
NODES=(
  "OC424|OC424|no"
  "华纳云HN-JP1|root@177.2.185.85|no"
  "HK04|root@82.152.161.202|no"
  "AkkoCloud|root@185.218.4.64|no"
  "CloudLeadInno|root@192.220.32.17|no"
  "BandwagonHost|megabox|no"
  "MAC-WAN|MAC-WAN|yes"
  "PZYC|PZYC|yes"
)

echo "=== rollback material (/root/cf-probe-retired-*) ==="
for spec in "${NODES[@]}"; do
  IFS='|' read -r label target needs_sudo <<< "$spec"
  if [ "$needs_sudo" = "yes" ]; then
    if [ -z "$PW" ]; then
      printf '  %-16s skipped (set SUDO_PW to check)\n' "$label"
      continue
    fi
    out=$(ssh $SSH "$target" "echo '$PW' | sudo -S -p '' bash -c '
      d=\$(ls -d /root/cf-probe-retired-* 2>/dev/null | tail -1)
      [ -n \"\$d\" ] || { echo NO-BACKUP; exit 0; }
      n=\$(ls -1 \"\$d\" 2>/dev/null | wc -l)
      echo \"\$(basename \$d) files=\$n\"'" 2>/dev/null | tr -d '\r')
  else
    out=$(ssh $SSH "$target" 'd=$(ls -d /root/cf-probe-retired-* 2>/dev/null | tail -1); [ -n "$d" ] || { echo NO-BACKUP; exit 0; }; echo "$(basename $d) files=$(ls -1 "$d" | wc -l)"' 2>/dev/null | tr -d '\r')
  fi
  printf '  %-16s %s\n' "$label" "${out:-UNREACHABLE}"
done

echo
echo "expected: 4 files (config.conf, traffic.dat, cf-probe.service, cf-probe binary)"
