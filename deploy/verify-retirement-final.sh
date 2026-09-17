#!/usr/bin/env bash
# Final fleet verification for the CF-Server-Monitor retirement.
#
# Run from a workstation that holds the SSH keys. (Running it from OC424 produced
# false UNREACHABLE rows: nested SSH to the tunnel/port-forwarded hosts fails
# there, which is a property of that path, not of the nodes.)
#
# Two details this script has to get right, both learned the hard way:
#   * `systemctl is-active <missing-unit>` prints to stdout AND stderr, so an
#     unguarded $(...) concatenates both ("inactiveinactive"). stderr is dropped.
#   * The Nekomari agent unit is not always called nekomari-agent.service --
#     OC424 reports through komari-agent-oc424-original-node.service (the restored
#     node identity) and MAC-WAN runs a *user* unit. The agent check therefore
#     looks for any komari/nekomari agent unit in either scope.
#
# A fully retired node reads:
#   probe=inactive enabled=disabled binary=gone agent=active
set -uo pipefail
SSH="-o ConnectTimeout=20 -o BatchMode=yes -o StrictHostKeyChecking=no"

NODES=(
  "OC424|OC424"
  "华纳云HN-JP1|root@177.2.185.85"
  "HK04|root@82.152.161.202"
  "AkkoCloud|root@185.218.4.64"
  "CloudLeadInno|root@192.220.32.17"
  "BandwagonHost|megabox"
  "MAC-WAN|MAC-WAN"
  "PZYC|PZYC"
)

remote='
probe=$(systemctl is-active cf-probe 2>/dev/null | head -1)
[ -n "$probe" ] || probe=inactive
enabled=$(systemctl is-enabled cf-probe 2>/dev/null | head -1)
[ -n "$enabled" ] || enabled=disabled
if [ -e /usr/local/bin/cf-probe ]; then binary=PRESENT; else binary=gone; fi

# Any agent unit, system scope then user scope.
agent=none
for u in $(systemctl list-unit-files --type=service --no-pager --plain 2>/dev/null \
           | awk "{print \$1}" | grep -Ei "^(komari|nekomari)-agent" || true); do
  s=$(systemctl is-active "$u" 2>/dev/null | head -1)
  [ "$s" = "active" ] && agent=active
  [ "$agent" = "none" ] && agent="$s"
done
for u in $(systemctl --user list-unit-files --type=service --no-pager --plain 2>/dev/null \
           | awk "{print \$1}" | grep -Ei "^(komari|nekomari)-agent" || true); do
  s=$(systemctl --user is-active "$u" 2>/dev/null | head -1)
  [ "$s" = "active" ] && agent=active
done

printf "probe=%s enabled=%s binary=%s agent=%s" "$probe" "$enabled" "$binary" "$agent"
'

fails=0
echo "=== CF-Server-Monitor retirement — fleet state ==="
for spec in "${NODES[@]}"; do
  IFS='|' read -r label target <<< "$spec"
  out=$(ssh $SSH "$target" "$remote" 2>/dev/null | tr -d '\r\n')
  if [ -z "$out" ]; then
    printf '  %-16s UNREACHABLE\n' "$label"
    fails=$((fails+1)); continue
  fi
  printf '  %-16s %s\n' "$label" "$out"
  case "$out" in
    probe=inactive*enabled=disabled*binary=gone*agent=active*) ;;
    *) fails=$((fails+1)); printf '  %-16s ^^ incomplete retirement or agent down\n' "" ;;
  esac
done

echo
echo "=== Nekomari panel ==="
ssh $SSH OC424 'curl -s -o /dev/null -w "  panel /api/public: HTTP %{http_code}\n" http://127.0.0.1:25774/api/public' 2>/dev/null

echo
echo "=== MAC-WAN watchdog (silence = healthy) ==="
w=$(ssh $SSH MAC-WAN 'python3 ~/.hermes/scripts/macwan_health_check.py 2>&1' 2>/dev/null)
if [ -z "$w" ]; then echo "  silent (healthy)"; else echo "$w" | sed 's/^/  /'; fails=$((fails+1)); fi

echo
echo "=== backups (root-owned; verified separately) ==="
echo "  see docs/RETIRE-CF-PROBE.md — each node has /root/cf-probe-retired-*/"

echo
if [ "$fails" = 0 ]; then
  echo "RESULT: all nodes retired, agents up, watchdog quiet"
else
  echo "RESULT: $fails check(s) need attention"
fi
