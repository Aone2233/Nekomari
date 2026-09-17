#!/usr/bin/env bash
# Inventory every monitoring agent on a host.
#
# Two separate systems can be present, and they are easy to confuse:
#   * cf-probe   -> reports to monitor.orderly2233.org (CF-Server-Monitor), the
#                   platform that replaced the old Komari panel. This is the
#                   live production monitoring.
#   * nekomari-agent -> reports to komari.orderly2233.org (the rebuilt panel).
#
# Reported separately so removing one never silently removes the other.
echo "--- cf-probe ---"
if systemctl list-unit-files 2>/dev/null | grep -q '^cf-probe'; then
  printf '  unit: %s / %s\n' "$(systemctl is-enabled cf-probe 2>/dev/null || echo n/a)" \
                            "$(systemctl is-active cf-probe 2>/dev/null || echo n/a)"
  for f in /etc/config/cf-probe/config.conf; do
    if [ -r "$f" ]; then
      grep -E '^(WORKER_URL|SERVER_ID|REPORT_INTERVAL|PING_MODE|RESET_DAY)=' "$f" | sed 's/^/  /'
    else
      echo "  config: $f (unreadable without root)"
    fi
  done
else
  echo "  not installed"
fi

echo "--- nekomari agent ---"
found=0
for u in nekomari-agent.service; do
  for scope in "" "--user"; do
    if systemctl $scope list-unit-files 2>/dev/null | grep -q "^$u"; then
      printf '  %s%s: %s / %s\n' "${scope:+user }" "$u" \
        "$(systemctl $scope is-enabled $u 2>/dev/null || echo n/a)" \
        "$(systemctl $scope is-active $u 2>/dev/null || echo n/a)"
      found=1
    fi
  done
done
# Legacy units this fork's rollout replaced.
for u in komari-agent.service komari-agent-oc424-original-node.service; do
  if systemctl list-unit-files 2>/dev/null | grep -q "^$u"; then
    printf '  legacy %s: %s / %s\n' "$u" \
      "$(systemctl is-enabled $u 2>/dev/null || echo n/a)" \
      "$(systemctl is-active $u 2>/dev/null || echo n/a)"
    found=1
  fi
done
[ "$found" = 0 ] && echo "  not installed"

echo "--- other known reporters ---"
for pat in cfsm cf-probe komari nekomari; do
  n=$(pgrep -fc "$pat" 2>/dev/null || echo 0)
  [ "$n" != "0" ] && echo "  processes matching '$pat': $n"
done
exit 0
