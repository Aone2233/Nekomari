# Nekomari long-term plan (2026-10-20 onward)

Status: living document. Version targets are windows, not promises; a release ships when
its exit criteria are met, not when the calendar says so.

## Baseline at v1.6.25 (2026-10-20)

- Panel: `v1.6.25` (`371d688`), 12 nodes, ~103 MiB RSS, 0 errors in the last hour.
- Agents: 12/12 `v1.6.24`, all online, targets cleared; SB01 runs the new OpenRC
  `supervise-daemon` unit and survives a child `SIGKILL`.
- Metrics: 168 MB SQLite store, 433 ping series, fractional ping values live.
- Backups: OVH nightly pull, 4 copies / 446 MB, upgrade archives included, restore drill
  completed against a scratch v1.6.24 panel.
- Delivery: CI 7/7, SBOM + provenance on releases, Linux and Windows frontend builds
  reproducible, panel upgrade check 9/9, offline install/upgrade harnesses in CI.

## Principles

1. **Security and data safety before features.** A monitoring panel that leaks agent tokens
   or loses history is worse than one missing a chart.
2. **No silent failures.** Every new failure mode gets a log line, a metric or a test.
3. **Small, reversible releases.** Canary the panel, stage agent rollouts, keep one rollback
   binary and a pre-upgrade database backup.
4. **One source of truth per contract.** Protocol, version and release metadata must not be
   duplicated without a test that compares them.
5. **Measure before optimizing.** Memory, disk and latency targets below are measured, not
   estimated.

## Version roadmap

| Version | Window | Theme | Main deliverables | Exit criteria |
|---|---|---|---|---|
| `v1.6.x` | ongoing | Patch/soak | Bug fixes, dependency and CVE updates, backup/monitoring tweaks | CI green; no P0 regression; panel check 9/9 |
| `v1.7.0` | 2026-11 | Security and invariants | API-key scopes + rotation, scoped/step-up policy for `exec`/`roll`/`getClientToken`; input caps for `getRecords`/`getLogs`; `v2EventQueues` cleanup; `used > total` clamp; encrypted off-host backups (`age`/`gpg`) with key-management decision; token rotation API/UI | No unbounded admin inputs; no global all-powerful API key; every sensitive method gated or scoped; backup encrypted and restore drill automated; zero P0 |
| `v1.8.0` | 2026-12 → 2027-01 | Observability and SLOs | uptime/restart-count metric; panel self-metrics history (RSS/heap/goroutine/theme-cache/DB pool wait) and a `/metrics` or admin trend view; backup-freshness alert; alert-rule test button; SLA tag-generation dedupe and coverage semantics; forecast reset/overflow fixes; second off-host destination decision | SLO dashboard/measurement exists; stale-backup and agent-offline alerts tested; SLA numbers reproducible against raw points; 50-node capacity test |
| `v1.9.0` | 2027-02 → 2027-03 | Operator features and UX | metric/record CSV export; exec-result history; cost-forecast column; roles (viewer/operator/admin) if not in v1.7; frontend entry < 160 KB gzip, < 35 JS requests, PWA precache < 3 MB; timezone labels, raw-HTML locale hardening, TerminalDialog focus trap; screen-reader pass | Feature acceptance tests; a11y checklist signed off; budget targets met on a production build |
| `v2.0.0` | 2027-Q2 | Agent and platform v2 | non-root agent default with capability/ICMP strategy; Windows/macOS agent support and footprint measurement; single protocol source (remove root/agent duplication); agent updater/rollback hardening; secrets at rest (session/2FA) with key management; optional DB encryption; 200-node capacity | Migration guide; protocol compatibility policy (two-minor deprecation); 200-node benchmark; no plaintext session/2FA secrets on new installs |
| `v2.1.0` | 2027-Q3 | Scale and ecosystem | HA/read-replica evaluation, plugin/theme marketplace signing, multi-region backup, optional external metrics export | Scale/HA runbook; marketplace signature verification; backup across two providers |

## Current Delivery Checkpoint (2026-10-26)

The production panel on OC424 is v1.6.27/12ad73a, following the compiler-only
v1.6.26 patch. v1.6.27 changes only the five admin tools' in-panel navigation.
The owner reports no remaining issue and explicitly resumes next-stage version
development. This is owner acceptance, not a newly performed authenticated
browser/fleet check. The public hostname is `nekomari.orderly2233.org`.

Continue with bounded v1.7 security candidates, starting from the exact clean
v1.6.27 tree rather than the accumulated unreleased development workspace.
The first candidate covers only `getRecords` window/read budgets, retained-history
visibility and `getLogs` pagination/cancellation. Query-limit code is implemented;
release CI, publication and OC424 delivery remain separate steps. API-key scopes,
token rotation, report/queue invariants and encrypted-backup tooling in the
separate development workspace are not shipped by this candidate.

200-node optimization, experiments, benchmarks and soak runs remain explicitly
deferred by the owner; preserve the original thresholds and all26 failed reports.
OC424 real-environment acceptance and suitable isolated OVH tests/backups are
authorized. Use branch + PR and reuse unchanged evidence; do not repeat unrelated
frontend, full-suite or capacity runs. Exact-candidate CI and safe rollout,
backup/rollback and post-upgrade checks still apply when publishing a version.

Both affected Go packages pass151 top-level tests (379 including subtests)
ordinarily and with the race detector; vet, module verification, the complete
Panel build with Go1.27.3 and reachable binary vulnerability scanning pass.
The new real HTTP/WebSocket matrix covers guests/sessions, load/ping bounds,
visible/hidden/deleted/unknown UUIDs and audit-log permission/limit controls;
the real REST bridge still rejects unsafe pagination. OVH runs the exact
prebuilt race executable against private fixtures (12 focused top-level groups)
with its existing Agent PID/restart count unchanged. This is not a native OVH
build, arm64/Windows CI or production acceptance. Original baseline refusals and
two fixture defects (independent comparison clocks and a missing same-origin
handshake header) remain recorded; neither production policy nor assertions
are weakened. No full-suite, frontend rebuild or capacity run is performed.

Before the scoped-key batch, inventory legacy API consumers and arrange scoped
replacements for intentional compatibility changes. Production backup encryption
still needs approved age key custody/escrow/unlock, an actual restore drill and
legacy-copy retention. General development/deployment permission does not decide
these policies or complete the v1.7 exit criteria. Native-platform, SLO and
secrets-at-rest gates likewise remain open.

## Stability and quality targets

| Area | Metric | Current baseline | Target | When / how measured |
|---|---|---|---|---|
| Panel availability | monthly uptime (external probe) | not formally tracked | **99.9%** (≤43 min/month), planned maintenance excluded | `deploy/panel-probe.sh` every minute + incident log |
| Panel errors | 5xx / ERROR rate | 0 in recent checks | **< 0.1%** of requests, no repeating ERROR class | reverse proxy/panel logs, daily review |
| API latency | p95 / p99 for common RPCs | not formalized | **p95 < 300 ms, p99 < 1 s** | panel metrics or access-log histogram |
| Dashboard UX | LCP on a cold visit | not formalized | **< 2.5 s** on a 10 Mbps link | Lighthouse/CI browser run |
| Panel memory | RSS p95, post-warmup growth | ~103 MiB | **< 256 MiB**; < 1 MiB/day drift after warmup | cgroup/RSS samples, 7-day window |
| Agent memory | RSS p95 per node | 16–25 MiB | **< 40 MiB**; no monotonic growth over 30 days | `deploy/agent-footprint.sh` weekly |
| Agent disk | directory size per node | 9–39 MB after pruning | **≤ 50 MB**, one rollback binary, ledger ≤ 1 MB/interface | backup/footprint script |
| Fleet freshness | nodes reporting within 2 min | 12/12 | **≥ 99.5%** of node-minutes | panel last-report timestamps |
| Upgrade success | panel + agent rollouts reaching target | 12/12 last rollout | **≥ 99%** first attempt; **100%** eventually | rollout outcomes + version counts |
| Rollback | time to restore previous known-good | not exercised this quarter | **< 5 min** panel, **< 15 min** agent | quarterly drill |
| Backup | RPO / RTO | nightly, restore drill done once | **RPO ≤ 24 h, RTO ≤ 30 min**; restore check monthly | OVH backup log + restore drill |
| Data integrity | `quick_check` result | ok | **100% pass** at startup/daily; monthly full `integrity_check` | startup/daily logs |
| Security | sensitive operations gated | API keys exempt, token read now gated | **100%** of sensitive methods require step-up or a scoped key | `sensitiveMethodsAllowlist` + tests |
| Dependencies | known critical/high CVEs | scan green | **0 critical**, high fixed within 7 days | CI vulnerability scan + Dependabot |
| Delivery | CI first-pass success / flaky tests | not tracked | **≥ 95%** first-pass; **< 2%** flaky | GitHub Actions history |
| Capacity | supported nodes | 12 | **50** by v1.8, **200** by v2.0 | load test with synthetic agents |

## Feature backlog by priority

### P0 — security and data integrity (v1.7)
- API-key scopes, rotation, audit attribution and a policy for step-up on `exec`/`roll`.
- Rotation API + UI for client tokens; no direct SQLite edits.
- Encrypted off-host backups + documented key management.
- Input ceilings (`getRecords` hours, `getLogs` limit/offset).
- `v2EventQueues` cleanup on client removal; report clamping.
- Secrets-at-rest plan (session token / 2FA secret); implement by v2.0.

### P1 — observability and correctness (v1.8)
- uptime/restart-count metric; panel self-metrics history and export.
- Backup freshness/agent-offline alerts with a test button.
- SLA tag-generation dedupe, coverage definition, forecast boundary fixes.
- Automated restore drill (monthly), second backup destination.
- 50-node capacity and memory test.

### P2 — operator features (v1.9)
- metric/record CSV export; exec-result history; cost forecast.
- roles (viewer/operator/admin) and scoped API keys UI.
- frontend budgets, timezone labels, raw-HTML locale hardening, TerminalDialog focus trap, screen-reader pass.

### P3 — agent/platform (v2.0)
- non-root default and capability strategy for ICMP; Windows/macOS support.
- single protocol module and migration policy; updater/rollback hardening.
- 200-node benchmark; optional HA/read replica; marketplace signing.

## Release and verification process

1. Branch + PR; CI must be green (ubuntu/windows, frontend, panel smoke, deploy scripts,
   workflow contracts, vulnerability scan).
2. Remote build on BERO (Go + frontend) and two-build comparison; update embedded archive.
3. Tag creates SBOM/provenance assets; Docker multi-arch image published.
4. Canary the panel on OC424: backup → digest-pinned deploy → `panel-upgrade-check.sh` 9/9
   → auth smoke.
5. Agent rollout: one node first, then 10%, then 100%; targets cleared afterwards; watch
   version counts and error logs for 24 h.
6. Record backup path, image digest, check output and rollback line in
   `docs/DEPLOY-OC424.md`.
7. Monthly SLO review: availability, errors, fleet freshness, backup age, upgrade success,
   memory trend.

**Compatibility policy:** within `v1.x`, RPC methods and protocol fields stay
backward-compatible; new fields are optional and old panels/agents keep working. Removing or
changing a field requires a two-minor deprecation window and a migration note. `v2.0` is the
first allowed breaking release.

## Risks and mitigations

| Risk | Impact | Mitigation / milestone |
|---|---|---|
| Global API key compromise | Full panel and fleet control | Scopes/rotation and step-up policy (v1.7); audit attribution already added |
| Unencrypted OVH backup | Token/notification secrets exposed | Encrypt with `age`/`gpg` + key management (v1.7) |
| Alpine/OpenRC service left down after update | Node silently offline | Installer now writes `supervise-daemon`; verify respawn in rollout; monitor fleet freshness (v1.6.25+) |
| Non-root ICMP unavailable | False packet loss | Capability strategy and pilot migration (v2.0); keep root fallback documented |
| Protocol copies drift | Silent data loss | Contract test added (v1.6.25); consolidate module (v2.0) |
| Single maintainer / bus factor | Slow fixes | Documented runbooks, CI gates, reproducible releases; keep scripts in repo |
| Backup only on one provider | Correlated loss | Second destination decision (v1.8) and multi-region copies (v2.1) |
| Feature creep before hardening | Regressions in auth/data paths | This roadmap's P0/P1 order; no feature release before v1.7 exit criteria |

## Non-goals

- Multi-tenant SaaS or per-customer isolation.
- A general-purpose Prometheus/Grafana replacement.
- Native mobile apps.
- Storing raw high-frequency metrics indefinitely; rollups and retention remain the model.
- Supporting arbitrary untrusted plugin/theme code with host privileges.
