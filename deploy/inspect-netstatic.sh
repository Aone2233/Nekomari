#!/usr/bin/env bash
# Report each node's netstatic coverage and current raw counters.
#
# The agent reports cycle traffic by summing per-interval deltas recorded in
# netstatic. A window that starts before the agent's first sample therefore
# undercounts, while an unset --month-rotate reports the raw kernel counter since
# boot (overcounting a plan). This shows which situation each node is in.
echo "=== netstatic coverage ==="
for f in /opt/nekomari-agent/net_static.json /opt/komari/net_static.json; do
  [ -f "$f" ] || continue
  echo "  file: $f ($(stat -c %s "$f") bytes)"
  python3 - "$f" <<'PY'
import json, sys, datetime
try:
    d = json.load(open(sys.argv[1]))
except Exception as e:
    print("    unreadable:", e); raise SystemExit
ifaces = d.get("interfaces") or d.get("Interfaces") or {}
for name, arr in list(ifaces.items())[:2]:
    if not arr:
        print(f"    {name}: no samples"); continue
    ts = [s.get("timestamp") or s.get("Timestamp") or 0 for s in arr]
    lo, hi = min(ts), max(ts)
    f = lambda t: datetime.datetime.utcfromtimestamp(t).strftime("%Y-%m-%d %H:%M")
    print(f"    {name}: {len(arr)} samples  {f(lo)} .. {f(hi)}")
PY
done
echo
echo "=== raw kernel counters (non-lo) ==="
awk 'NR>2 { gsub(/^[ \t]+/,""); split($0,a,":"); if (a[1]=="lo") next;
  split(a[2],f," "); printf "  %-12s rx=%s tx=%s\n", a[1], f[1], f[9] }' /proc/net/dev
echo
echo "=== uptime / agent flags ==="
uptime | sed 's/^/  /'
grep -oE '\-\-month-rotate [0-9]+' /etc/systemd/system/nekomari-agent.service 2>/dev/null \
  | sed 's/^/  /' || echo "  --month-rotate: not set"
