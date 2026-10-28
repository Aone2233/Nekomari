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

## Current Delivery Checkpoint (2026-10-28)

**Latest delivery: v1.7.8 (2026-10-28 15:42:01 Asia/Shanghai).** The new
same-shell node uptime history records last positive valid readings with a
one-day default retention, no backfill and no reboot inference. Exact
CI40649551718 passes8/8, release40650315129 10/10, Docker40651006736 2/2;
OVH published-binary report/permission/browser tests pass. Fresh MAC-WAN pre
and post ciphertext restores under both pinned v1.7.7/v1.7.8, preserving the
post-upgrade uptime rollups without reclaim. OC424 upgrade9/9, public24/24,
scoped forecast14rows and all14 fresh CPU/uptime reporters pass. No Agent,
schema, credential, permission or schedule change. Independent-media restore,
production owner UI acceptance and real incident delivery remain open. See
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Prior delivery: v1.7.7 (2026-10-28 14:30:19 Asia/Shanghai).** Missing/invalid
paid-node expiry is neutral and localized, preserving actual countdowns and the
shared theme. The fetch-abort test synchronizes with handler arrival; no runtime
fetch change. Exact CI40642445800 passes8/8, release40643116847 10/10 and
Docker40643699302 2/2. Ten actual embedded-admin scenarios pass on the attested
published binary on OVH. Fresh MAC-WAN pre pinned-v1.7.6/v1.7.7 recovery takes
8.25/9.42s and post-v1.7.7 takes7.81s without `ANALYZE`. OC424 upgrade9/9,
public23/23,scoped forecast14rows and all14fresh reporters pass. No schema/API,
auth/Agent/dependency/credential change. First actual scheduled nightly backup
is independently observed today; independent-media and real incident delivery
remain open. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Prior delivery: v1.7.6 (2026-10-27 21:25:17 Asia/Shanghai).** The dsh review
finds three recovery placeholders supplied with five arguments, rejected by
actual MySQL/PostgreSQL, and a preexisting MySQL fractional parent-division
defect. Both are fixed while preserving v1.7.5's series-driven SQLite startup
optimization. Exact CI40506697515 passes 8/8, release40507767971 10/10 and
Docker40511319737 2/2. Published-binary actual SQL install/recovery, OVH 6/6,
fresh MAC-WAN pre old/new and post new encrypted recovery, OC424 upgrade9/9,
public23/23 and scoped forecast14rows pass. No schema, auth, Agent, frontend or
dependency change; no production `ANALYZE`. See
[`DSH-REVIEW-2026-10-27.md`](./DSH-REVIEW-2026-10-27.md).

**Adjusted active objective (owner-authorized):** close v1.7's actual handoff and
remaining operational gates, then continue independently verified v1.8 observability/
correctness batches. The full v1.8-v2.1 roadmap is not one continuous task.
All 200-node work remains deferred, preserving its original limits and 26 failed
reports. Publication is not proof that every roadmap exit criterion passed.

**Delivered:** OC424 serves v1.7.8/75168d5 at `nekomari.orderly2233.org`, healthy/
restart0, after exact-source CI 8/8, release 10/10, Docker 2/2 and upgrade check 9/9.
Both Linux assets pass checksums/tag/source/workflow attestations; both image
architectures identify exact source. Public checks24/24 and scoped forecast14rows
pass; the earlier v1.7.4 bounded live SLA reads are dated evidence, not rerun
for v1.7.8. The owner-approved
baseline now includes Pi's explicitly owner-authorized addition: 14 clients and
14 currently fresh reporting nodes, not the earlier 13-client release snapshot.
UUIDs/tokens/Agent services are unchanged. The existing tools and new uptime page
retain shared in-panel navigation. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

Scoped keys, history/audit bounds, report/queue invariants, session-only token
rotation and encrypted-backup tooling are published. The one old global key
migrates to 30-day `nodes:read`; no live Agent token is rotated. Generic token
edit/import restrictions are owner-accepted. During the earlier v1.7.0 cutover,
Hermes's gateway was paused and resumed with three references directed to the metadata-only CLI;
the owner-configured scoped key now matches only `nodes:read` + `nodes:write`
and passes actual disposable CLI create/edit/read/remove. Original13 metadata is
unchanged at that handoff; Pi's later authorized addition is preserved in the
current14-node baseline. The owner selects365days to2027-10-27 13:38:02
Asia/Shanghai, not the suggested90; no silent rotation. Other runtime callers
are not fully inventoried. v1.7.8 does not restart Hermes or any Agent.

**Backup delivery:** OC424 has only public recipients; OVH's encrypted daily
03:30 UTC timer replaces only the old plaintext cron row, with 14 ciphertext-pair
retention. The installed service succeeds; fresh pre/post release archives pass
isolated MAC-WAN pinned-v1.7.7/v1.7.8 recovery with both DBs,14 clients and
cleanup, without refreshing planner statistics or reclaim. Release
recovery copies are outside automatic retention, and all old plaintext/local
rollback assets are kept. The owner reports downloading the offline identity
copy and authorizes cutover; independent-media restore is not claimed. Online
same-UID Hermes/root/Docker exposure remains explicit. No unattended decrypt
scheduler or production RPO/RTO/SLO acceptance is implied.
The first actual nightly timer2026-10-28 11:30:23 -> service11:31:04
Asia/Shanghai succeeds with a verified ciphertext pair and independent regular
copy outside pruning. This closes first-trigger observation only.

**Next bounded actions:**

1. dsh records the owner's history/audit/API-key UI acceptance as closed; no
   new owner session is performed in this review, nor is that acceptance
   extended to future versions. Private key handoff and disposable
   create/edit/read/remove have passed; reviewed Hermes CLI
   metadata writes may resume. No admin session/token-read/exec/update privilege is given
   to Hermes. See [`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md).
2. Observe subsequent encrypted timer success, document independent-media
   recovery separately, and retain all old recovery copies until an explicit
   deletion policy. Automated restore harness/real manual restores exist, but
   scheduled decryption and measured RPO/RTO remain separate. v1.7.3 health/
   heartbeat alerts are delivered.
3. Continue one bounded correctness or observability change at a time, with
   exact candidate CI, safe backups and OC424 acceptance. The first completed
   batch is the compatible v1.7.1 forecast-boundary patch, not a full v1.8 feature release.
   The next completed batch is v1.7.2 GeoIP configuration safety/hot reload:
   focused lifecycle races,exact CI,8/8 isolated OVH real-Panel groups,
   fresh14-node old/new encrypted recovery and OC424 deployment pass. See
   [`GEOIP-RELOAD.md`](./GEOIP-RELOAD.md).
   Do not bundle fleet privilege/platform changes or 200-node work.
   v1.7.3 backup freshness/heartbeat alerts are delivered with restricted
   metadata-only SSH reporting and existing Panel notification delivery. Exact
   CI/publication, six real OVH synthetic groups, 14-node recovery/continuity,
   installed service and initial/later recurring health checks pass. First actual
   nightly backup is now verified2026-10-28; live Telegram delivery is separate. See
   [`BACKUP-HEALTH.md`](./BACKUP-HEALTH.md).
   v1.7.4 SLA task-generation normalization is delivered: unique chronological
   measured buckets before loss/latency/incidents/maintenance; focused races,
   exact CI/publication, 6/6 OVH synthetic groups, 14-client old/new-image recovery
   and OC424 acceptance pass. Bounded live SLA reads cover 14 nodes/58 tasks;
   11 independent task-level bucket comparisons pass, with no multi-generation
   task in that selected live window. Synthetic fixtures verify the overlap case.
   The separate span-based
   coverage definition remains open. Existing backup-health recurring checks now
   succeed; first nightly backup is now observed, live incident delivery is not.
   The subsequent v1.7.5 SQLite recovery-startup fix removes the need for
   scratch `ANALYZE`; v1.7.6 fixes SQL backend compatibility and adds real
   MySQL/PostgreSQL CI. Actual published binaries and fresh encrypted restores
   pass. Old v1.7.4 restores still need the documented workaround. See
   [`METRIC-RECOVERY-STARTUP.md`](./METRIC-RECOVERY-STARTUP.md) and
   [`SLA-GENERATIONS.md`](./SLA-GENERATIONS.md).
   v1.7.7 closes the small nullable-expiry UI gap and test arrival race.
   v1.7.8 adds and deploys node uptime history as a partial v1.8 observability
   milestone, not reboot counts or a completed v1.8 release. Release, encrypted
   recovery and OC424 continuity gates pass; owner UI acceptance remains
   separate. See [`UPTIME-HISTORY.md`](./UPTIME-HISTORY.md).

v1.7 is released/deployed, **not unconditionally exit-complete**: remaining
caller inventory, independent-media recovery, live incident delivery and
stronger restore-drill policy remain open in
[`OPEN-WORK.md`](./OPEN-WORK.md#v17-release-readiness-2026-10-28).
v1.7.8->v1.7.7 needs the pinned prior compose/image (keeping the health opt-in/state),
preserving current DB data and all14nodes. Fresh per-DB WAL-safe snapshots/compose
are retained in `rollbacks/v1.7.8-20261028T074158Z-6e323629`; they do not claim
cross-DB atomicity. Old-image manual metric reclaim deletes uptime history, so
retain and verify the fresh post-upgrade ciphertext before downgrade or reclaim.
Across credential migration to v1.6, rollback needs the old binary and pre-upgrade
DB, not an image-only downgrade; frozen-writer/failure-preservation instructions
are retained with the paired local snapshots.

The active app goal covers selecting bounded work and continuing delivery, not
a declaration that the long-term roadmap is done. OC424 remains the
real acceptance host; OVH is the backup/appropriate isolated-test host. Only
owned disposable downloads/fixtures are cleaned; unchanged broad local suites
are not rerun. Earlier source-batch checks remain in their dedicated guides,
changelog and retained exact-source CI.

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
- uptime/restart-count metric (uptime history ships in v1.7.8; reboot
  counting remains open); panel self-metrics history and export.
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
