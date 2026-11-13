# Probe data contract (v1.8 probe truth)

Status: draft for v1.8.0. Baseline v1.7.30. This document defines the meaning
of every value the Agent reports and the Panel stores. It is a contract, not a
description of current production behavior for optional fields.

## Non-negotiable rules

1. **Unknown is never zero.** A value that was not measured is omitted/null and
   carries a quality reason. It is never encoded as 0, 200, 0 ms or 0 bytes.
2. **A value carries its identity.** Every ping value has task, protocol,
   family, role, attempts/success, `sampled_at`, `received_at` and `quality`.
3. **Only additive changes in v1.x.** Existing fields keep their names, units
   and meanings. New fields are optional; old readers ignore them.
4. **No fabricated success.** Slow is not loss; HTTP error is not network loss;
   DNS/TLS failure is not a measured latency.
5. **Aggregation preserves meaning.** Counters are deltas; percentiles come
   from a merged distribution, not averaged bucket percentiles; empty buckets
   are unknown.

## Quality vocabulary

`ok`, `unknown`, `legacy`, `clamped`, `reset`, `skipped`,
`permission_denied`, `dns_error`, `tls_error`, `timeout`, `http_error`,
`partial`, `stale`, `clock_skew`.

## Data dictionary

| Logical field | Unit | Definition | Aggregation | Unknown rule |
|---|---|---|---|---|
| `cpu.usage` | % | host CPU over an explicit interval | avg/last | no interval/`/proc/stat` → unknown |
| `cpu.cgroup.usage` | % | cgroup v1/v2 CPU usage | avg/last | no cgroup → unknown |
| `cpu.cgroup.quota_cores` | cores | cgroup CPU quota | last | no quota → unknown |
| `cpu.cgroup.throttled_seconds_total` | s | cgroup throttling total | last | no cgroup → unknown |
| `ram.used/total` | bytes | existing RAM definition | last | collection failure → unknown |
| `ram.available/cached/buffers/zswap` | bytes | `/proc/meminfo` detail | last | field absent → that field unknown |
| `swap.used/total` | bytes | existing swap definition | last | field absent → unknown |
| `load.1/5/15` | — | system load average | last | collection failure → unknown |
| `disk.used/total` | bytes | existing physical-mount sum | last | partial failure → `partial` |
| `disk.mount.used/total` | bytes | per mount, tagged `mountpoint/device/fstype` | last | one mount fails → that point unknown |
| `net.rate.up/down` | bytes/s | actual-elapsed interval rate | avg/last | first sample/reset/gap → unknown |
| `net.total.up/down` | bytes | kernel lifetime counters | last | collection failure → unknown |
| `traffic.interval.up/down` | bytes | validated additive interval delta | sum | gap/reset/epoch change → unknown |
| `traffic.cycle.up/down` | bytes | billing-cycle cumulative | last | no cycle/first sample → unknown |
| `tcp.outsegs_total` | count | kernel counter | last | collection failure → unknown |
| `tcp.retranssegs_total` | count | kernel counter | last | collection failure → unknown |
| `tcp.retrans_pct` | % | interval retransmits / sends | avg | first/reset/>100% → unknown |
| `uptime` | s | system uptime | last | collection failure → unknown |
| `restarts` | count | boot-id change or conservative uptime drop | last | cannot decide → unknown |
| `boot.id_hash` | hash | hashed boot id; raw value never public | last | no boot id → unknown |
| `process.count` | count | visible process count | last | collection failure → unknown |
| `connections.tcp/udp` | count | sockstat inuse+tw | last | collection failure → unknown |
| `ping.attempts` | count | attempts in this cycle | sum | old Agent → unknown |
| `ping.success` | count | successes in this cycle | sum | old Agent → unknown |
| `ping.loss_pct` | % (0–100) | `(attempts-success)/attempts` | avg | no attempt or any locally unavailable attempt → unknown |
| `ping.unknown` | bool | cycle could not be measured; bounded `reason` tag identifies DNS, permission, socket or unsupported failure | sum | old Agent → unknown |
| `ping.latency_ms` | ms | per-attempt latency; failure = -1 | existing | legacy semantics |
| `ping.success_latency_ms` | ms | successful attempts only | avg/p50/p95 | no success → unknown |
| `ping.min_ms/avg_ms/max_ms` | ms | successful-attempt statistics | last | no success → unknown |
| `ping.jitter_ms` | ms | RFC 3550 instantaneous jitter | last | fewer than 2 successes → unknown |
| `ping.loss` | 0/1 | legacy per-attempt failure marker | avg | legacy compatibility |
| `http.total_ms` | ms | whole HTTP attempt | avg | no response → unknown |
| `http.dns_ms` | ms | DNS phase | avg | no resolution → unknown |
| `http.tcp_ms` | ms | TCP connect phase | avg | no connection attempt → unknown |
| `http.tls_ms` | ms | TLS handshake; plain HTTP = 0 | avg | non-HTTPS → unknown/0 |
| `http.ttfb_ms` | ms | final HTTP request fully written to first response byte (excluding prior DNS/TCP/TLS and redirect hops) | avg | no response → unknown |
| `http.status_code` | code | final response status | last | no response → unknown |
| `http.redirects` | count | redirect hops | last | no response → unknown |
| `http.tls_expiry_days` | days | remaining days of final verified leaf | last | plain HTTP/no cert → unknown |
| `http.reachable` | 0/1 | TCP/TLS completed | avg | no attempt → unknown |
| `http.available` | 0/1 | status matches expectation | avg | no response → unknown |
| `backup.age_seconds` | s | time since last successful backup | last | no status file → unknown |
| `backup.ok` | 0/1 | last backup success | last | no status file → unknown |

## Wire identity

Every `agent.pingResult` carries `task_id`, `ping_type`, `finished_at`,
`value`, `value_us`, and optionally `role`, `family`, `resolved_ip`,
`resolved_family`, `attempts`, `success`, `loss_pct`, `min_us`, `avg_us`,
`max_us`, `jitter_us`, `probe_mode`, and nested `http`.

The legacy `value`/`value_us` pair is always the first successful attempt of
the cycle, or -1 when every attempt failed. Old Panels therefore keep their
existing loss/latency behavior.

## Legacy behavior

- A task with `probe_attempts == 0` is a legacy single-probe task. It emits only
  the pre-v1.8 latency/loss fields and keeps the SYN-retransmit retry path.
- A task with `http_mode` empty or `legacy` keeps 4xx/5xx as a failed probe,
  even though `http.status_code`, `reachable` and `available` are recorded.
- `http_mode == status_aware` accepts an HTTP response regardless of status for
  the legacy loss marker; 4xx/5xx is reachable but may be unavailable. A
  response timeout remains a failed attempt even when the connection succeeded;
  consult `http.reachable` to distinguish it from connection failure.
- Old data with mixed latency/loss meanings keeps the `legacy` quality and may
  report unknown quantiles/minima.

## Compatibility and tests

- Contract tests compare the Agent's ping wire keys with the Panel decoder and
  compare the shared structs in both protocol copies.
- Unknown-never-zero tests cover each collection/phase failure.
- Four-way tests cover old/new Agent and old/new Panel.
- No v1.x field is removed or renamed; no `CheckKomariVersion` change.

A cycle with any local inability to start (DNS failure, missing ICMP permission,
unavailable ICMP socket, or unsupported probe) reports `probe_status=unknown`
and a bounded `probe_reason`. The Panel records `ping.unknown` and omits both
legacy `ping.loss` and cycle loss/latency, so SLA sees a gap, not packet loss.
Other errors such as connection refusal or timeout remain failed attempts.
Old Agents retain legacy behavior. Old Panels ignore the new status and may
still treat the legacy negative sentinel as loss; deploy the Panel before an
Agent that emits this status. HTTP status-aware mode retains its existing
response/error loss behavior; `http.reachable` separately records a completed
connection even when no response arrives.
