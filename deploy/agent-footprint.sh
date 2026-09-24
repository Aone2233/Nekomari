#!/usr/bin/env bash
# Measure the Nekomari agent's real footprint over a window, for capacity planning.
# The measurements and what they mean are written up in docs/AGENT-FOOTPRINT.md.
#
# Two independent network figures are taken because neither alone is honest:
#   agent_net_*  sums the byte counters of the sockets the agent itself owns
#                (ss -tinp), which is the agent's own traffic but misses raw
#                ICMP, since ping tasks use raw sockets that ss does not list.
#   host_net_*   is the whole interface, which on a host running other services
#                includes their traffic too.
#
# Usage: agent-footprint.sh [unit] [system|user] [window-seconds]
#   on the node:      bash agent-footprint.sh '' system 180
#   as a non-root user whose unit is a user unit:
#                     bash agent-footprint.sh '' user 180
set -uo pipefail

UNIT_ARG="${1:-}"
MODE="${2:-system}"
WINDOW="${3:-180}"

if [ "$MODE" = "user" ]; then
  export XDG_RUNTIME_DIR=/run/user/1000
  SC="systemctl --user"
else
  SC="systemctl"
fi
# The agent may run as another user (root, or a dedicated account), so reading
# its /proc entry and its sockets needs privileges even for a user unit.
if [ -n "${SUDO:-}" ]; then
  :
elif sudo -n true 2>/dev/null; then
  SUDO="sudo -n"
else
  SUDO=""
fi

UNIT="$UNIT_ARG"
if [ -z "$UNIT" ]; then
  UNIT="$($SC list-units --type=service --state=running --no-legend 2>/dev/null \
          | awk '{print $1}' | grep -iE 'komari.*agent|agent.*komari' | head -1)"
fi
[ -n "$UNIT" ] || { echo "ERROR=no-agent-unit"; exit 1; }

PID="$($SC show -p MainPID --value "$UNIT" 2>/dev/null)"
[ -n "$PID" ] && [ "$PID" != "0" ] || { echo "ERROR=no-main-pid"; exit 1; }

HZ="$(getconf CLK_TCK)"
cpu()  { $SUDO awk '{print $14+$15}' "/proc/$PID/stat" 2>/dev/null; }
rss()  { $SUDO awk '/^VmRSS/{print $2}' "/proc/$PID/status" 2>/dev/null; }
thr()  { $SUDO awk '/^Threads/{print $2}' "/proc/$PID/status" 2>/dev/null; }
iof()  { $SUDO awk '/^read_bytes/{r=$2} /^write_bytes/{w=$2} END{print r+0, w+0}' "/proc/$PID/io" 2>/dev/null; }
net()  { awk '/:/{rx+=$2; tx+=$10} END{print rx+0, tx+0}' /proc/net/dev; }
# ss -t prints the socket state in column 1 (ESTAB, LISTEN, ...) and the byte
# counters on the indented detail line that follows, so a new socket entry
# starts at any line that is not indented.
sock() {
  $SUDO ss -tinp 2>/dev/null | awk -v pid="$PID" '
    /^[^ \t]/ { keep = (index($0, "pid=" pid) > 0); next }
    keep { for (i = 1; i <= NF; i++) {
             if ($i ~ /^bytes_sent:/)     { v = $i; sub(/^bytes_sent:/, "", v);     sent += v }
             if ($i ~ /^bytes_received:/) { v = $i; sub(/^bytes_received:/, "", v); recv += v }
           } }
    END { printf "%d %d\n", recv + 0, sent + 0 }'
}
socks() { $SUDO ss -tinp 2>/dev/null | grep -c "pid=$PID," || true; }
# A socket can close inside the window, which makes a cumulative total go
# backwards. The arithmetic is done in awk because these counters are large
# enough that bash can turn them into scientific notation and fail to parse it.
delta() { awk -v a="${1:-0}" -v b="${2:-0}" 'BEGIN{ d = b - a; if (d < 0) d = 0; printf "%.0f", d }'; }

BIN="$($SUDO readlink -f "/proc/$PID/exe" 2>/dev/null)"
C0="$(cpu)"; I0="$(iof)"; N0="$(net)"; S0="$(sock)"; NS0="$(socks)"
sleep "$WINDOW"
C1="$(cpu)"; I1="$(iof)"; N1="$(net)"; S1="$(sock)"; NS1="$(socks)"

read -r I0R I0W <<<"$I0"; read -r I1R I1W <<<"$I1"
read -r N0R N0T <<<"$N0"; read -r N1R N1T <<<"$N1"
read -r S0R S0T <<<"$S0"; read -r S1R S1T <<<"$S1"

echo "host=$(hostname)"
echo "unit=$UNIT"
echo "bin=${BIN:-unknown}"
echo "bin_bytes=$($SUDO stat -c %s "$BIN" 2>/dev/null)"
echo "caps=$($SUDO getcap "$BIN" 2>/dev/null || echo none)"
echo "run_user=$($SUDO ps -o user= -p "$PID" 2>/dev/null | tr -d ' ')"
echo "ambient_caps=$($SUDO grep '^CapEff' "/proc/$PID/status" 2>/dev/null | awk '{print $2}')"
echo "threads=$(thr)"
echo "fds=$($SUDO ls "/proc/$PID/fd" 2>/dev/null | wc -l)"
echo "sockets_start=$NS0"
echo "sockets_end=$NS1"
echo "rss_kb=$(rss)"
echo "window_s=$WINDOW"
echo "cpu_ticks_delta=$(( ${C1:-0} - ${C0:-0} ))"
echo "hz=$HZ"
echo "cpu_percent=$(awk -v d="$(( ${C1:-0} - ${C0:-0} ))" -v hz="$HZ" -v w="$WINDOW" 'BEGIN{printf "%.3f", (d/hz)/w*100}')"
echo "agent_net_rx_bytes=$(delta "$S0R" "$S1R")"
echo "agent_net_tx_bytes=$(delta "$S0T" "$S1T")"
echo "agent_net_rx_total=$S1R"
echo "agent_net_tx_total=$S1T"
echo "disk_read_bytes=$(delta "$I0R" "$I1R")"
echo "disk_write_bytes=$(delta "$I0W" "$I1W")"
echo "host_net_rx_bytes=$(delta "$N0R" "$N1R")"
echo "host_net_tx_bytes=$(delta "$N0T" "$N1T")"
echo "host_cpus=$(nproc)"
echo "host_mem_kb=$(awk '/MemTotal/{print $2}' /proc/meminfo)"
echo "os=$(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME")"
echo "kernel=$(uname -r)"
# The unit carries the agent token in ExecStart; never print it.
echo "flags=$($SC show -p ExecStart --value "$UNIT" 2>/dev/null \
        | sed -E 's/-t +[A-Za-z0-9]+/-t <redacted>/g' \
        | sed -E 's/\\x20/ /g' | tr -s ' ' | cut -c1-500)"
