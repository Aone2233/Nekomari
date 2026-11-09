# v1.8.0 "Probe Truth" checklist

Status: implementation checklist. Baseline `origin/main` at v1.7.30 (`e17bb57`).
Branch: `codex/v1.8-probe-truth`. This file is the contract for the release;
it is not a claim that any item below is implemented yet.

## Implementation status (living)

- [x] Additive protocol fields for multi-probe, HTTP phases and address identity.
- [x] Agent multi-probe (1–10, default 3), RFC3550 jitter, legacy single-probe path.
- [x] HTTP phase capture and `legacy` / `status_aware` semantics.
- [x] Panel ingestion, metric definitions and ping/HTTP metric points.
- [x] Host detail metrics: CPU interval/cgroup, RAM detail, per-mount disk, boot-id hash, TCP counters.
- [x] Retention preset backend (node/ping windows, disabled definitions preserved).
- [x] Ping stats API/UI, quality panel; task form controls; retention preset UI.
- [x] New-install defaults 30-day node / 7-day ping; existing definitions keep their stored values.
- [x] Four-way payload compatibility tests; compatibility policy document.
- [x] Host calibration harness; TCP loopback ±1 ms test; ICMP loopback vs system ping ±50 µs test; HTTP TTFB injected-delay test; boot-id restart test.
- [x] Clock-boundary and retention-expiry tests (expired data is absent, never stale or zero).
- [ ] Calibration LAN ICMP and `tc netem` loss remain NOT RUN.
- [x] 26/26 plugin archive install; 25/26 load/unload (komari-gfw skipped as a known synthetic-domain fixture). `registerNotificationChannel`, jsruntime TLS and admin channel selection UI added; six notification plugins register/remove channels on load/unload; `CallNotificationChannel` dispatch/unregister is tested against a fake channel. Real external provider delivery pending.
- [x] 64/64 theme manifests, preflight, install and reinstall pass.
- [ ] Theme browser/hydration/mobile visual gate.
- [x] OC424 scratch acceptance: `panel-upgrade-check` 9/9, migration, authenticated `admin:getProbeQuality`, `public:getPingMetricStats` and retention preset against copied production data; see `V1.8-OC424-STAGING.md`.
- [x] OC424 production upgrade to v1.8.0/a92a242, pre/post encrypted backups, `panel-upgrade-check` 9/9 and isolated amd64 restore drill. See `DEPLOY-OC424.md` and `MAC-WAN-BACKUP.md`.

## Confirmed defaults (2026-11-08)

| Decision | Value |
|---|---|
| CPU tolerance | ±2 percentage points absolute |
| RAM/Swap tolerance | `max(1% of total, 4 KiB)` |
| HTTP phase tolerance | `max(5 ms, 2%)` per phase |
| Loss field | new `loss_pct` is 0–100; legacy `ping.loss` stays 0/1 per attempt |
| Multi-probe default | `attempts=3`, spacing `200 ms`, per-attempt timeout = task timeout |
| Jitter | RFC 3550 instantaneous jitter; optional `stddev_ms` supplement |
| HTTP semantics | `reachable` = TCP/TLS completed; `available` = status matches expected; legacy tasks keep 4xx/5xx as failure; new tasks opt in to `status_aware` |
| TLS expiry | final verified leaf certificate; plain HTTP/no certificate = unknown |
| Retention defaults | new installs: node metrics 30 days, ping 7 days; existing installs keep current values with a recommendation |
| Disk mounts | top 20 by used; omitted mounts keep the sum metric and set `partial` quality |
| Out of scope | RBAC, HA/read replica, multi-region backup, marketplace signing, 200-node benchmark, cost forecast |

## 0. Baseline, branch and compatibility boundary

- [ ] Branch from `origin/main`; do not import the accumulated workspace.
- [ ] **Additive API only**: no removal, rename, or meaning change for existing
      v1.7.30 fields. New fields are optional and old readers ignore them.
- [ ] **Do not lower the version.** v1.8.x is greater than v1.7.30 and still
      satisfies marketplace constraints.
- [ ] **Do not bypass `CheckKomariVersion`.** `internal/plugin/version.go` is
      unchanged; compatibility tests cover both accept and refuse directions.
- [ ] **Upstream compatibility gate.** Pin one upstream commit and both market
      catalog hashes per month; run the 26-plugin / 64-theme fixtures on every
      release. A failure blocks the release; do not lower a requirement.
- [ ] **Four-way compatibility matrix**: old Agent + old Panel, old Agent + new
      Panel, new Agent + old Panel, new Agent + new Panel.
- [ ] Non-goals listed above do not enter this release.

## 1. `PROBE-DATA-CONTRACT.md`

- [ ] Create `docs/PROBE-DATA-CONTRACT.md` as the single source of truth for
      every reported value.
- [ ] State at the top: **unknown is never zero; no measurement means no value.**
- [ ] Every value has: logical name, storage name, unit, definition,
      aggregation, valid range, unknown rule, quality, introduced version.
- [ ] Every ping result carries: `task_id`, `protocol`, `family`, `role`,
      `attempts`, `success`, `sampled_at`, `received_at`, `quality`.
- [ ] Quality enum: `ok`, `unknown`, `legacy`, `clamped`, `reset`, `skipped`,
      `permission_denied`, `dns_error`, `tls_error`, `timeout`, `http_error`,
      `partial`, `stale`, `clock_skew`.
- [ ] `sample_interval_seconds`, `counter_epoch`, `resolved_ip`,
      `resolved_family` are part of the contract.
- [ ] Legacy semantics are documented separately: old `ping.loss` (0/1 per
      probe), old HTTP 4xx/5xx as failure, mixed legacy traffic, legacy
      timestamps using server receipt time.
- [ ] Contract test: emitted field set equals documented field set; missing or
      extra fields fail.
- [ ] Frozen JSON fixture prevents silent field changes.

### Data dictionary (must cover)

| Logical field | Unit | Definition | Aggregation | Unknown rule |
|---|---|---|---|---|
| `cpu.usage` | % | host CPU over an explicit interval | avg/last | no interval or `/proc/stat` → unknown |
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
| `boot.id_hash` | hash | hashed boot id; never expose the raw value | last | no boot id → unknown |
| `process.count` | count | visible process count | last | collection failure → unknown |
| `connections.tcp/udp` | count | sockstat inuse+tw | last | collection failure → unknown |
| `ping.attempts` | count | attempts in this cycle | sum | old Agent → unknown |
| `ping.success` | count | successes in this cycle | sum | old Agent → unknown |
| `ping.loss_pct` | % (0–100) | `(attempts-success)/attempts` | avg | attempts=0 → unknown |
| `ping.latency_ms` | ms | per-attempt latency; failure = -1 | existing | legacy semantics |
| `ping.success_latency_ms` | ms | successful attempts only | avg/p50/p95 | no success → unknown |
| `ping.min_ms/avg_ms/max_ms` | ms | successful-attempt statistics | last | no success → unknown |
| `ping.jitter_ms` | ms | RFC 3550 jitter | last | fewer than 2 successes → unknown |
| `ping.loss` | 0/1 | legacy per-attempt failure marker | avg | legacy compatibility |
| `http.total_ms` | ms | whole HTTP attempt | avg | no response → unknown |
| `http.dns_ms` | ms | DNS phase | avg | no resolution → unknown |
| `http.tcp_ms` | ms | TCP connect phase | avg | no connection → unknown |
| `http.tls_ms` | ms | TLS handshake; plain HTTP = 0 | avg | non-HTTPS → unknown/0 |
| `http.ttfb_ms` | ms | request sent to first byte | avg | no response → unknown |
| `http.status_code` | code | final response status | last | no response → unknown |
| `http.redirects` | count | redirect hops | last | no response → unknown |
| `http.tls_expiry_days` | days | remaining days of final verified leaf | last | plain HTTP/no cert → unknown |
| `http.reachable` | 0/1 | TCP/TLS completed | avg | no attempt → unknown |
| `http.available` | 0/1 | status matches expectation | avg | no response → unknown |
| `backup.age_seconds` | s | time since last successful backup | last | no status file → unknown |
| `backup.ok` | 0/1 | last backup success | last | no status file → unknown |

### Unknown-never-zero rules

- [ ] Collection failure, timeout, permission denial, DNS/TLS failure, and no
      sample are all null/omitted, never 0.
- [ ] Old Agent missing fields → new Panel marks `legacy_unknown`; never guesses
      zero.
- [ ] Mixed legacy data → `legacy`; quantiles/minima may be unknown.
- [ ] UI shows `-` or "unknown"; no ranking and no alert denominator use.
- [ ] Unit test for every unknown scenario asserts no zero point is emitted.
- [ ] Runtime invariant: validate before write; reject with quality, never
      silently degrade.

## 2. Calibration harness

- [ ] Add `deploy/calibrate/` (or `tools/probecal/`) with one command producing
      JSON and Markdown.
- [ ] Report under `.runtest/probe-calibration-<UTC>/` with environment,
      versions, raw samples, expected/actual, tolerance, pass/fail.
- [ ] Keep failures; never delete, widen tolerance, or retry until green.
- [ ] Environment: Linux, `/proc`, root for `tc netem`, loopback, disposable
      Panel; never production.

| Item | Method | Tolerance |
|---|---|---|
| CPU | against `/proc/stat` over 60 s | ±2 percentage points |
| RAM/Swap | against `/proc/meminfo` in the same window | `max(1%, 4 KiB)` |
| Disk | against `df` for the selected mount | ±0.5% |
| Network rate | against `/proc/net/dev` over ≥60 s | ±0.5% |
| Traffic interval | gap-free window total | exactly 0 bytes; gaps/reset unknown |
| ICMP RTT | loopback + LAN, against `ping` | loopback ±50 µs; LAN ±1 ms |
| TCP RTT | loopback + LAN, against handshake capture | ±1 ms |
| HTTP phases | local server with injected DNS/TCP/TLS/TTFB delays | each phase `max(5 ms, 2%)` |
| Loss | `tc netem loss` 0/1/5/20% | ≤1 percentage point (N≥100) |
| Clock | inject +2 min / -10 min | reject or mark `clock_skew`; no silent rewrite |
| Restart | real reboot; second reboot within 5 min | exact count; unknown when undecidable |
| Retention | boundary write + reopen after expiry | expired = unknown; no stale value |

- [ ] ICMP/TCP use a real Agent binary or the same measurement package, not
      simulated values.
- [ ] HTTP uses a local HTTP/HTTPS server: 200/301/404/503, redirect chain,
      slow TTFB, slow TLS.
- [ ] DNS phase uses a local DNS server with 0/50/200 ms delays.
- [ ] `tc netem` requires root; without root the item is "not run", not "pass".
- [ ] Repeat each calibration at least 3 times; report median and p95.
- [ ] CI runs only root/network-free algorithm tests; full calibration is a
      release gate.

## 3. Multi-probe loss and jitter

- [ ] `PingTask` additive fields:
  - `probe_attempts`: 1–10, default 3; legacy tasks 1.
  - `probe_spacing_ms`: 50–1000, default 200.
  - `probe_timeout_ms`: default task timeout; each attempt has its own timeout.
- [ ] Agent performs N attempts per cycle; records success/RTT/family/resolved
      IP for each.
- [ ] Additive wire fields: `attempts`, `success`, `loss_pct` (0–100),
      `min_us`, `avg_us`, `max_us`, `jitter_us`.
- [ ] `value`/`value_us` keep legacy semantics: first successful attempt; all
      failed = -1.
- [ ] Panel metrics: `ping.attempts`, `ping.success`, `ping.loss_pct`,
      `ping.min_ms`, `ping.avg_ms`, `ping.max_ms`, `ping.jitter_ms`.
- [ ] Keep legacy metrics: `ping.latency_ms` (one point per attempt, -1 on
      failure), `ping.success_latency_ms`, `ping.loss` (0/1 per attempt).
- [ ] Jitter = RFC 3550 instantaneous jitter; optional `stddev_ms`.
- [ ] All-fail: `attempts=N`, `success=0`, `loss_pct=100`; min/avg/max/jitter
      unknown.
- [ ] Single-probe mode (attempts=1) is byte-for-byte legacy behavior; UI labels
      it "single probe".
- [ ] UI shows attempts/success/loss%/min/avg/max/jitter; single-probe text says
      "probe failure rate", multi-probe text says "packet loss".
- [ ] Alerts use `loss_pct` for multi-probe; legacy tasks keep old semantics.
- [ ] Tests: 0/1/5/20/100% loss, partial success, all fail, jitter, four-way
      compatibility.

## 4. HTTP phases

- [ ] Task additive fields:
  - `http_mode`: `legacy` (default) | `status_aware`.
  - `http_expected_status`: e.g. `200-399`, `200,204,301-308`; default `200-399`.
- [ ] Each HTTP attempt records `dns_us`, `tcp_us`, `tls_us`, `ttfb_us`,
      `total_us`, `status_code`, `redirects`, `tls_expiry_days`, `reachable`,
      `available`.
- [ ] Panel metrics: `http.dns_ms`, `http.tcp_ms`, `http.tls_ms`,
      `http.ttfb_ms`, `http.total_ms`, `http.status_code`, `http.redirects`,
      `http.tls_expiry_days`, `http.reachable`, `http.available`.
- [ ] Legacy mode: 4xx/5xx still count as failure/loss; `reachable=true`,
      `available=false` are still recorded.
- [ ] `status_aware` mode: `loss = !reachable`; `available` drives HTTP
      availability and alerting.
- [ ] `tls_expiry_days`: final verified leaf; plain HTTP/no cert/handshake
      failure = unknown, never 0.
- [ ] Redirects are followed by default; `redirects` counts hops; `total`
      includes every hop.
- [ ] DNS time is split out of total; `total` remains the legacy `value`.
- [ ] Tests: 200/301/404/503, redirect chain, slow DNS/TCP/TLS/TTFB, expiring
      certificate, four-way compatibility.

## 5. Address family, host semantics, retention and quality panel

### Address family

- [ ] `PingTask` additive `ip_version_preference`: `auto` (default, legacy
      order) | `4` | `6`; coexists with `target_family`.
- [ ] Resolve all A/AAAA with context/timeout; record `resolved_ip` and
      `resolved_family`.
- [ ] Every result carries `family`; different families never merge.
- [ ] Legacy tasks keep first-address behavior but record resolved IP/family and
      mark `legacy`.
- [ ] New tasks prefer an explicit family; `auto` records the choice and reason;
      multi-node hostname tasks require an explicit family to be comparable.
- [ ] `--prefer-ip-version` remains for panel connections only; probes use the
      task-level field.

### Host semantics

- [ ] CPU: explicit `sample_interval_s` + `/proc/stat` delta; add cgroup v1/v2
      fields; unavailable cgroup is unknown, never replaces host usage.
- [ ] RAM: add `available/cached/buffers/zswap`; keep `ram_mode`.
- [ ] Disk: add per-mount metrics tagged `mountpoint/device/fstype`; top 20,
      omitted mounts set `partial`; keep the sum metric.
- [ ] boot_id: read `/proc/sys/kernel/random/boot_id`, report a hash; restart
      detection uses boot-id change first, uptime drop >300 s as fallback;
      undecidable = unknown.
- [ ] TCP retrans: add `outsegs_total`, `retranssegs_total`, `retrans_pct`;
      first/reset/>100% = unknown.
- [ ] All new fields carry quality; old fields unchanged.

### Retention presets

- [ ] Install/settings offer 7 / 30 / 90 / 365 days.
- [ ] New installs default to 30 days node metrics, 7 days ping.
- [ ] Existing installs keep current values; show recommendation and migration
      warning; no silent change.
- [ ] Show an estimated disk range based on interval, series count, and bytes
      per point.
- [ ] Rollup tiers follow metric retention; raw remains 10 minutes in memory;
      expired = unknown.
- [ ] Per-metric overrides remain; existing retention API unchanged.

### Quality panel

- [ ] New admin quality view (page or card): last sample age, unknown %, coverage,
      clock skew, counter resets, probe attempts/success, family/protocol/role
      split, HTTP status/TLS.
- [ ] Admin session or `diagnostics:read` only; never public.
- [ ] UI uses `-`/unknown, never 0; five locales; desktop/mobile.

## 6. Compatibility boundary with v1.7.30

- [ ] Protocol: optional fields only; old Agent missing fields → new Panel
      unknown; old Panel ignores new fields; legacy `value/value_us` unchanged.
- [ ] RPC/HTTP: optional params/fields only; no required additions; existing
      methods and meanings unchanged.
- [ ] Database: additive columns/indexes/metric definitions; GORM AutoMigrate;
      old binary ignores new columns; new binary runs on old database; rollback
      keeps old data.
- [ ] Metrics: new names only; no rename/delete; old data keeps legacy meaning.
- [ ] Themes/plugins: no manifest, route, permission, or notification-channel
      contract changes; compatibility gate still runs.
- [ ] Version: v1.8.x; no lowering; `CheckKomariVersion` unchanged; market
      `>=1.6.0` still satisfied.
- [ ] Rollback: new Agent → old Panel; new Panel → old Agent; old image +
      pre-upgrade DB.

## 7. Exit criteria

- [ ] `PROBE-DATA-CONTRACT.md` reviewed; field-document consistency tests pass.
- [ ] Calibration harness passes every tolerance; report includes raw data,
      environment, and versions; failures retained; no tolerance widened.
- [ ] Multi-probe, HTTP, address family, host, retention, and quality panel
      implemented; four-way compatibility tests pass.
- [ ] Unknown-never-zero contract tests, runtime invariants, and UI assertions
      pass.
- [ ] Upstream compatibility gate: pinned upstream commit + catalog hashes;
      26 plugins / 64 themes fixtures; accept/refuse version tests pass.
- [ ] OC424: upgrade 9/9; both DB quick checks; client identity digest; fresh
      pre/post encrypted backups; isolated restore drill; no P0.
- [ ] Release: CI / release / Docker green; SBOM/checksum/provenance; docs
      updated; rollback instructions.
- [ ] No RBAC/HA/multi-region/200-node/cost-forecast changes.

## 8. Evidence commands (to be filled by the implementation)

```bash
# root module
go test ./... -count=1

# agent module
cd agent && go test ./... -count=1

# contract tests
go test ./protocol/v2 -count=1
cd agent && go test ./protocol/v2 -count=1

# targeted
go test ./internal/metricstore ./web/api/client ./web/rpc/jsonrpc -count=1
cd agent && go test ./server ./monitoring/... -count=1
```
