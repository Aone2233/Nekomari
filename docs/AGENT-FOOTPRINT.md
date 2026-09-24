# Agent footprint, and the smallest server it will run on

Measured 2026-09-24 on the live nine-node fleet with `deploy/agent-footprint.sh`
(a 180-second window per node). This is what the agent costs the machine it runs
on, and what a host has to provide for it — including behind NAT, which is the
case the two residential nodes already prove.

Reproduce with:

```bash
bash deploy/agent-footprint.sh '' system 180     # system unit, agent may run as another user
bash deploy/agent-footprint.sh '' user 180       # a user unit (needs XDG_RUNTIME_DIR=/run/user/<uid>)
```

The script never prints the agent token: the unit's `ExecStart` is redacted before
it is echoed.

## Measured

| Node | OS / arch | host vCPU | host RAM | agent RSS | CPU (% of one core) | agent tx | agent rx | threads |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 甲骨文 OC424 | Ubuntu 22.04 arm64 | 4 | 24.5 GB | 16.9 MiB | 0.050 | 10.4 KB | 1.2 KB | 11 |
| 华纳云 HN-JP1 | Debian 12 amd64 | 1 | 964 MB | 20.3 MiB | 0.106 | 11.0 KB | 1.3 KB | 7 |
| HK04 | Debian 13 amd64 | 1 | 476 MB | 15.8 MiB | 0.083 | 11.4 KB | 1.4 KB | 7 |
| AkkoCloud SJ | Debian 12 amd64 | 1 | 997 MB | 21.0 MiB | 0.050 | 12.2 KB | 1.7 KB | 7 |
| 并行智算云 PZYC | Ubuntu 22.04 amd64 | 4 | 7.5 GB | 16.0 MiB | 0.050 | 15.4 KB | 2.6 KB | 11 |
| NOSLA 东京 | Debian 13 amd64 | 2 | 4.2 GB | 23.5 MiB | 0.050 | 12.3 KB | 2.1 KB | 9 |
| MegaBox | Debian 13 amd64 | 2 | 2.1 GB | 24.3 MiB | 0.089 | 12.9 KB | 2.1 KB | 8 |
| MAC Server | Ubuntu 24.04 amd64 | 4 | 8.0 GB | 17.8 MiB | 0.178 | n/a¹ | n/a¹ | 11 |
| CloudLeadInno | Debian 12 amd64 | 2 | 2.0 GB | 22.0 MiB | 0.050 | 8.6 KB | n/a¹ | n/a¹ |

Network columns are **bytes per 180 s**. ¹ MAC's agent runs as `macos` under a user
unit with no passwordless sudo, so its `/proc/<pid>/exe` and its socket counters
are unreadable; RSS and CPU still are. CloudLeadInno was reached through the
MAC-WAN hop, which truncated the `rx` figure.

The spread is small and it tracks the work each node was given, not the host:

- **Resident memory 15.8–24.3 MiB**, mean ≈ 20 MiB. The 4-vCPU hosts are not
  higher than the 1-vCPU ones; the two largest are MegaBox (2 vCPU) and NOSLA
  (2 vCPU), and both run five ping tasks.
- **CPU 0.05–0.18 % of a single core.** Even the worst case is under two
  thousandths of one core. Nothing here needs a fast CPU.
- **Outbound 8.6–15.4 KB per 180 s** = 48–86 B/s = **4.1–7.4 MB/day**, so roughly
  **125–220 MB/month** per node. Inbound is 1.2–2.6 KB per 180 s, about a tenth of
  that.
- **Disk writes 0–213 KB per 180 s**, and 0 on three nodes: this is the traffic
  ledger being rewritten, not continuous logging.
- **7–11 threads, 6 file descriptors, exactly 1 TCP socket** — the panel
  WebSocket. The two extra sockets that appear transiently are unlock probes.

### What drives it

Read off the units and the agent's own flags:

| Source | Setting in the field | Effect |
|---|---|---|
| Metrics report | `-i 5` (5 s) | 17 280 reports/day — this is the traffic figure above |
| Basic info | `--info-report-interval 10` (10 min) | 144/day, and it is what refreshes `clients.updated_at` |
| Ping tasks | 3–7 per node, `interval=60` | 3–7 probes/minute; the CPU and the transient sockets |
| Traffic ledger | `--month-rotate <day>` | the occasional disk write |
| Unlock probes | periodic | the transient sockets to streaming endpoints |

`-i 5` is the single biggest lever on traffic. The default is 3 s; every node in
this fleet was set to 5 s. Raising it multiplies the traffic figure down almost
linearly, and costs chart resolution.

## The smallest server it will run on

| Resource | Measured need | Recommendation |
|---|---|---|
| CPU | < 0.2 % of one core | any 1 vCPU, any architecture (amd64/arm64) |
| RAM | 16–25 MiB resident | **64 MiB floor**, 128 MiB comfortable — the OS needs the rest |
| Disk | 9.6 MB binary (8.9 MB arm64) + 53 KB ledger | **~30 MB**, so 100 MB free is plenty² |
| Network | 4–7 MB/day up, ~0.5 MB/day down | any link; no burst behaviour observed |
| Privileges | none for TCP/HTTP tasks | see below for ICMP |
| Inbound | **nothing** | no public IP, no port forwarding |

² Each upgrade keeps the previous binary as `<bin>.bak-pre-<version>`, ~8–9 MB
each. On HK04 those rollback copies were 43 MB of the 52 MB the agent directory
occupied — larger than everything else the agent uses put together. Prune them if
space is tight.

A 1 vCPU / 64 MiB / 1 GB-disk VPS is comfortably above what the agent needs. The
smallest host in this fleet that is actually running it is HK04 at 1 vCPU and
476 MB, and its agent uses 15.8 MiB and 0.083 % of that core.

## Behind NAT

The agent is NAT-compatible **by construction**: it never listens.

- `agent/` contains no `net.Listen`, no `ListenAndServe` and no `http.Server{` —
  it only dials out. `ss -tlnp` for the agent's PID returns 0 listening sockets on
  every node.
- It holds one outbound WebSocket to the panel over 443 (through Cloudflare), plus
  DNS and the probe targets. Commands from the panel — web-ssh, remote exec —
  travel back down that same connection, which is why nothing needs to reach the
  host.

Two nodes in this fleet are live proof, not a laboratory case:

- **MAC Server** has only private addresses (`192.168.100.168/169`) behind a home
  router and reaches the panel through it. Its public path exists solely as a
  router DNAT for SSH; the agent does not use it.
- **CloudLeadInno** is a US residential ISP node, reached from this workstation
  only by hopping through MAC-WAN.

So a NAT'd host needs: outbound TCP 443 to the panel, and working DNS. That is
all. No public IP, no port forwarding, no UPnP, and CGNAT is fine — the agent
behind a carrier-grade NAT behaves identically because it is the one opening the
connection. The connection is long-lived and the agent reconnects on its own, so a
NAT table that drops idle mappings costs a reconnect, not data.

## Privileges: only ICMP needs any

TCP and HTTP ping tasks need nothing. ICMP tasks need `CAP_NET_RAW`, because the
agent asks pro-bing for a **raw** socket (`pinger.SetPrivileged(true)` in
`agent/server/task.go:190`). Measured on NOSLA as its agent user, without the
capability:

```
$ sudo -u komari python3 -c "import socket; socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP)"
PermissionError: [Errno 1] Operation not permitted
```

Three ways to grant it, best first:

1. **systemd ambient capability** — what NOSLA does, and the pattern worth copying
   for a small or shared host. The agent runs as an unprivileged dedicated user
   and still gets raw ICMP:
   ```ini
   User=komari
   AmbientCapabilities=CAP_NET_RAW
   CapabilityBoundingSet=CAP_NET_RAW
   NoNewPrivileges=yes
   ```
   Verified on the running process: `CapEff: 0000000000002000` (`cap_net_raw`).
2. **File capability** — `setcap cap_net_raw+ep <bin>`. Note that *replacing the
   binary drops it*, which is why the fleet's upgrade script records and restores
   it; MAC Server has no passwordless sudo and restores it through a privileged
   container.
3. **Run as root** — what the other eight nodes do. It works, and it is the
   least privilege-conscious of the three.

`net.ipv4.ping_group_range` does **not** help here: it permits unprivileged
`SOCK_DGRAM` ping sockets, and the agent uses `SOCK_RAW`. A host that allows only
the former will report ICMP tasks as loss unless the capability is granted.

What happens without it is not silent-failure-proof, but it is bounded: the
`auto` protocol decision checks `isPermissionErr` and falls back to TCP
(`agent/server/task.go:369`), so an `auto` task keeps working. A task typed
explicitly as `icmp` has no fallback and reports the failure as loss.

## What this did not measure

- **Peak, not average.** The window covers three minutes of steady state. A burst
  of unlock probes, or a reconnect storm, would show higher instantaneous CPU and
  sockets than the table above.
- **The panel side.** This is the agent on a monitored host, not the panel's own
  cost.
- **Windows and macOS agents**, and the arm64 agent beyond OC424's numbers.
- **Long-run disk growth.** The ledger was 53 KB after ~2 days with `--month-rotate`
  set. It is bounded by the number of buckets, not by uptime, but that is read from
  the code and one sample rather than from a months-long measurement.
- **Reconnect churn.** Two nodes stood out over 24 hours — 华纳云 HN-JP1 at 166
  WebSocket errors and NOSLA at 181, against 10–18 everywhere else. The nodes kept
  reporting throughout, so this is a connectivity observation about those two
  paths (both go through Cloudflare), not a footprint figure. It is recorded here
  because a host that drops idle mappings will look like this.
