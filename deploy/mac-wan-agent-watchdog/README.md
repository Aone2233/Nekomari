# MAC-WAN system Agent watchdog

These units are for MAC-WAN's system-level `nekomari-agent.service`, not the
old user-level unit. Install the script as root-owned mode `0755` under
`/usr/local/sbin` and the units as root-owned mode `0644` under
`/etc/systemd/system`; reload systemd and enable the timer. Keep the original
user units and script available for rollback, but do not run both Agent
instances with the same node token.

The watchdog gets the PID from the system unit and checks for an established
outgoing TCP connection on port 443 belonging to that PID. It rechecks after
30 seconds and restarts the system unit only if both checks show no connection.
If systemd or `ss` cannot be inspected, it fails without restarting. A successful
check is only a socket-liveness heuristic: it does not authenticate the peer or
prove the panel is accepting reports, and another Agent-owned 443 connection
could give a false positive. The panel's own node freshness still needs separate
monitoring.

`nekomari-agent-watchdog.sh --check` is a read-only one-shot check (0 connected,
1 disconnected, 2 inspection error). Run it as root: Linux may hide the owning
PID from an unprivileged `ss -p`, making a healthy connection look disconnected.
The timer itself runs as root. The timer's restart behavior and root service must
be verified before retiring the old timer or process.

## 2026-11-07 handover

The owner confirmed Hermes created the root service to replace the user unit.
Both had continued running under the same node identity; the user watchdog was
restarting the old process and the sockets displaced each other. After the
root timer was installed and its read-only check passed, the old user timer was
stopped/disabled and the user Agent stopped. Their files were retained. Over
seven minutes the root Agent stayed on the same PID with no disconnect messages;
the real root timer ran successfully, and fresh existing TCP/ICMP and CPU/memory
samples were observed on the panel. The actual restart-on-disconnect action was
tested with fake commands only; it has not been induced on the live host.

Rollback requires an intentional maintenance window: stop and disable the root
timer, stop the root Agent, then start the retained user Agent and enable its
retained watchdog timer. Never run both Agents at once. The user Agent lacks raw
ICMP privilege, so the rollback can cause ICMP permission failures again. The
root service runs more code with root privilege; same-user Hermes and Docker
membership already confer root-equivalent access on this host.

On 2026-11-07 a read-only check as `macos` returned a false disconnected result
because `ss` did not expose the root Agent's PID. The script now refuses to run
without root and returns inspection-error status 2 instead. The installed script
was atomically replaced after checking its previous hash; a root one-shot check
passes and the Agent PID and restart count were unchanged. Eight stub tests pass,
including a real unprivileged subprocess. The next real timer firing at
23:44:47 Asia/Shanghai also succeeds; an actual disconnect/restart remains
unverified.
