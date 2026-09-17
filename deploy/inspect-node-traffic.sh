#!/usr/bin/env bash
# Show per-interface counters on a node, so it is clear what the agent sums.
echo "=== interfaces ==="
ip -o link show | sed 's/: <.*//' | awk '{print $2}' | tr '\n' ' '
echo
echo "=== /proc/net/dev (excluding lo) ==="
printf '%-14s %16s %16s\n' INTERFACE RX_BYTES TX_BYTES
awk 'NR>2 {
  gsub(/^[ \t]+/, "");
  split($0, a, ":");
  name = a[1];
  if (name == "lo") next;
  split(a[2], f, " ");
  rx = f[1]; tx = f[9];
  printf "%-14s %16s %16s\n", name, rx, tx;
}' /proc/net/dev
echo
echo "=== uptime ==="
uptime
echo
echo "=== agent flags ==="
grep -A10 ExecStart /etc/systemd/system/nekomari-agent.service 2>/dev/null \
  | grep -oE '\-\-[a-z-]+( [^ \\]*)?' | tr '\n' ' '
echo
