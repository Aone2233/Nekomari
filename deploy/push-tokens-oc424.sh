#!/usr/bin/env bash
# Push rotated tokens to the hosts OC424 can reach directly, then restart each
# agent. Reads /tmp/rotate-all.txt; the token travels as a file so it never
# appears in a command line or a process table.
#
# Run from OC424. The remaining hosts (BandwagonHost on a non-standard port,
# MAC-WAN and PZYC, whose keys live elsewhere) are handled separately.
set -uo pipefail

MAP=/tmp/rotate-all.txt
[ -f "$MAP" ] || { echo "missing $MAP"; exit 1; }

push_one() {
  local label="$1" target="$2" unit="$3" extra="$4"
  printf '%s' "$(awk -F'|' -v l="$label" '$1==l{print $5}' "$MAP")" > /tmp/.tok
  chmod 600 /tmp/.tok
  if ! scp -q $extra -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 \
        /tmp/.tok "${target}:/tmp/.tok"; then
    echo "  ${label}: scp failed"; rm -f /tmp/.tok; return
  fi
  # -n / </dev/null: ssh would otherwise eat the loop's stdin.
  ssh -n $extra -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 "$target" \
    "T=\$(cat /tmp/.tok); U='${unit}'; \
     sed -i -E \"s/-t [A-Za-z0-9]{22}/-t \$T/\" \"\$U\" && \
     systemctl daemon-reload && systemctl restart nekomari-agent && sleep 8 && \
     echo \"  ${label}: \$(systemctl is-active nekomari-agent) prefix=\$(grep -oE -- '-t [A-Za-z0-9]{4}' \"\$U\" | head -1)\" && \
     rm -f /tmp/.tok" < /dev/null
  rm -f /tmp/.tok
}

echo "== HUANAYUN =="; push_one HUANAYUN root@177.2.185.85 /etc/systemd/system/nekomari-agent.service ""
echo "== HK04 ==";     push_one HK04     root@82.152.161.202 /etc/systemd/system/nekomari-agent.service ""
echo "== AKKO ==";     push_one AKKO     root@185.218.4.64 /etc/systemd/system/nekomari-agent.service ""
echo "== CLOUDLEAD =="; push_one CLOUDLEAD root@192.220.32.17 /etc/systemd/system/nekomari-agent.service ""
echo "== NOMAO (IPv6) =="; push_one NOMAO "root@[2604:abc0:50::11:601e]" /etc/systemd/system/nekomari-agent.service "-6"

echo
echo "done (OC424 itself is updated by its own unit path)"
