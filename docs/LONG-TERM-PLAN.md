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

## Current Delivery Checkpoint (2026-10-27)

**Adjusted active objective (owner-authorized):** incrementally finish v1.7
security hardening in independently verified/releasable batches. Do not treat all
v1.8-v2.1 plans as the current implementation target. All200-node work remains
deferred with its original criteria and26 failed reports preserved. This narrows
the work target; it does not declare the long-term roadmap or v1.7 exit criteria
complete, and does not decide key custody or account/privilege/SLO policies.

**Live maintenance checkpoint (2026-10-27, Asia/Shanghai):** v1.6.29/510f7d3 is
deployed on OC424 after exact-source CI7/7, release10/10, Docker2/2 and upgrade
check9/9. PR86 uses `codex/v1.6-maintenance`, not current main: only report/queue
fixes and release-image source labels are backported onto v1.6.28. Both published
images/architectures identify the correct source. A fresh OVH paired backup
passes isolated old-image recovery; both DB schemas/integrity and all13 fresh
CPU-reporting nodes among14registered clients are preserved. No Agent restart,
credential/schema migration, frontend/protocol/dependency or backup-policy change
occurs. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md) for digests and rollback.

Independent OC424/OVH SSH/sudo is verified. The owner now identifies Hermes on
another machine as the main node-management caller, accepts token edit/import
restrictions with no rotation now, approves the recommended backup architecture
and chooses self-run authenticated acceptance after deployment. No temporary
test credential is required for that path. Prepare Hermes's exact operations,
dedicated scoped key and secure handoff/write pause; establish the actual
owner-controlled key device/public recipients/offline copy/restore runner and
real encrypted recovery. Design approval does not supply these missing facts.
Next implement the arrangements in
[`V1.7-CUTOVER-READINESS.md`](./V1.7-CUTOVER-READINESS.md),
then release the exact v1.7 candidate with fresh pre-migration recovery. Until
then, only independent engineering/compatible patches may proceed;200-node work
and the later roadmap remain deferred, not completed.

**Post-v1.6.28 continuation (owner authorization, 2026-10-26):** replace the
open-ended "finish every roadmap version" work target with this ordered batch:

1. Source-identity follow-up merged as e234385 (PR83, 2026-10-27) after
   exact-source CI40324022778 passes 7/7: verify the resolved release tag against
   checkout HEAD and label both images with that commit. Actual publication
   acceptance now passes on v1.6.29; immutable v1.6.28 is not republished.
2. Token-rotation PR81 is merged as 31a5b43 (2026-10-27), keeping the current
   production checkpoint and original runtime/frontend bytes. Integrated
   6a3b7eb passes new exact-source CI40425302716 (7/7), separate from original
   13cdcc5 CI40313616989. No production token is rotated.
3. Prepare the next v1.7 cutover only after live API consumers/replacements,
   token-recovery compatibility and D12 backup key custody/restore authorization
   are established. Until then, continue independent engineering, not live
   credential/schema changes or a declaration that v1.7 is delivered.
   Encrypted-backup PR84 is merged asf567128 after exact5899614
   CI40426994745 passes7/7, including candidate-built synthetic recovery.
   This source tooling does not decide D12 production custody. The live gate
   checklist is in [`OPEN-WORK.md`](./OPEN-WORK.md#v17-release-readiness-2026-10-27).

Each source batch closes with reviewed changes, exact-candidate CI and retained
evidence; publication/deployment additionally need backups/rollback and bounded
acceptance. OC424 remains the live acceptance host, OVH handles appropriate
isolated checks/backups, and only owned disposable caches/fixtures are cleaned.
No 200-node work or redundant broad local suite is part of this target. The
owner's continue request revokes the earlier pause as a work instruction; the
application's stored goal still reports paused because the available goal tool
cannot change its objective or resume it. This is a repository target update,
not a claim that the app state was changed or the original roadmap completed.

Query-hardening PR78/502a269 was released early as the compatible **v1.6.28**
patch; its retained rollout evidence follows, not the current version.
Exact-source CI40275930537 passes7/7; release
40318142810 passes10/10, including native amd64/arm64 deployment verification;
Docker40319017219 passes both jobs. The arm64 image's extracted executable
matches the verified release checksum/provenance; that rollout reported v1.6.28/502a269,
healthy/restart0, upgrade check9/9. Public route/assets and private-site denial
checks pass, both DB schemas/integrity remain unchanged, and 14 registered/12
fresh CPU-reporting nodes match the pre-upgrade baseline. OVH retains the fresh
paired backup after a successful isolated old-image restore drill. Authenticated
live history/audit owner acceptance is still pending, not replaced by a 401 or
synthetic release test. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

Scoped-key PR79/5b005d5 is merged as 3dd20fb with an identical tree and its merge
CI40280921167 passes 7/7; it is **not released/deployed**. Its isolated scope is
documented in [`API-KEYS.md`](./API-KEYS.md). Repository script inventory
does not establish all live callers: arrange explicit minimal-scope replacements
and accept the intentional legacy-key compatibility change before tag/cutover.
Retain paired pre-upgrade DB snapshots and old binary/image; image-only rollback
is insufficient after authentication migration. D12 encrypted-backup custody and
the rest of the v1.7 deliverables remain separate open gates.

The third bounded source batch starts from exact 3dd20fb and is merged as
fc84915 (PR80, exact merge CI40298579581 passes7/7), not the accumulated
development tree. Its report/queue fixes now ship in compatible v1.6.29, not
v1.6.28 or the still-gated v1.7 authentication migration.
It validates all report fields before clamping RAM/Swap/disk usage to total,
retains every other invalid-input guard, and warns once per accepted anomaly
episode. It removes queues after successful node deletion and sweeps empty
five-minute-idle queues on the existing scheduler. Blocked old-generation polls
wake without resurrecting state or reading a new generation's commands;
discarded payload references are cleared. Limits, TTLs, acknowledgement,
coalescing and reconnect delivery remain compatible. This does not add an
in-flight credential-admission/revocation fence. Targeted ordinary/race and real
HTTP/WebSocket/REST regressions verify this closure; no capacity, frontend or
Agent/protocol/dependency change is included. Client-token rotation is the
fourth independent source-merged, unreleased batch; encrypted-backup custody remains D12.
Final affected ordinary/race evidence covers 211 top-level tests (524 including
subtests), six packages in retained per-package runs. Linux CI adds race checks
for the four report/queue/runtime packages. OVH's exact prebuilt race checks pass
nine top-level groups (13 with visible subtests), Agent495903/restart0 unchanged;
that is not a native-platform or production acceptance claim. Go1.27.3 build,
affected vet, module verification, documentation check and the reachable binary
scan pass; one required-module advisory is non-reachable. No frontend rebuild
or capacity run occurs locally for that source batch.

The fourth candidate, `codex/v1.7-client-token-rotation`, originally starts from
fc84915 and is integrated with main e234385 on 2026-10-27. Only workflow/checker
and documentation differ from the original 13cdcc5; its runtime, frontend and
embedded theme remain unchanged. The following checks are retained original
evidence; integrated6a3b7eb passes exact-source CI40425302716 (7/7) and merges
as31a5b43. Source delivery is not production acceptance.
It adds session-only Agent-token rotation with existing TOTP policy and an
in-panel recovery-consent dialog. Short Agent mutations share per-node
admission with commit/retirement; delayed requests/handshakes and old runtime
generations cannot revive state. Connections, presence, queues, terminals and
file operations are retired while measurements remain. Already-admitted work
and remote jobs are not undone; legacy plaintext Agent-token storage/read
behavior remains. Generic edits and existing-node imports can no longer replace
credentials outside this boundary. No automatic fleet rotation, Agent/protocol,
schema/dependency change, release tag or production app update is included.
See [`CLIENT-TOKEN-ROTATION.md`](./CLIENT-TOKEN-ROTATION.md).

Final per-package retained evidence covers twelve affected packages, 247
top-level/580 including subtests, ordinarily/race; public/embed regressions add
71/111 ordinarily. Frontend 194/194, lint/compiler audit/build/repack, four
rotation and eight node-table mounted groups pass. A compiled embedded Panel
passes real synthetic REST rotations from desktop-dark/mobile-light UI with no
new tabs/errors/document overflow. OVH's checksum-matched prebuilt race binaries
pass six groups/13 including visible subtests, Agent495903/restart0 unchanged.
Affected vet, module verification, Go1.27.3 build and reachable/imported-package
binary scan pass (one required-module-only advisory remains). These are bounded
source/synthetic checks, not native-platform CI, production acceptance or
capacity/SLO evidence. Accept edit/import restrictions and independent recovery
before deployment; API-consumer migration and D12 are still separate gates.

The fifth candidate, `codex/v1.7-encrypted-backup-tools`, starts from exact31a5b43
and extracts only opt-in backup tooling from the separate development workspace.
It adds recipient-only age encryption, WAL-safe snapshots bound to captured
metrics configuration, selected assets, exclusive ciphertext/checksum publication,
fail-safe exact-prefix retention and bounded authenticated verify/extract. The
automated drill requires local Unix-socket Docker before decryption, digest-pinned
preinstalled images, no network/host ports and only scratch data. Local and OVH
real-age/WAL tests each pass22/22 groups; both perform real synthetic offline
restores and wrong-version cleanup with preexisting images. OVH's temporary
tools/source match checksums, Agent495903/restart0 stays unchanged, and owned
workspaces/containers are removed. CI reuses its existing Panel build for the
new drill and adds the offline harness. Exact5899614 CI40426994745 passes7/7,
including22/22 real-age groups and the actual candidate-built restore/wrong-version
drill; PR84 is merged asf567128. No broad local Go/frontend rebuild, production key/schedule/retention
change or plaintext-backup deletion occurs. See
[`ENCRYPTED-BACKUPS.md`](./ENCRYPTED-BACKUPS.md): these checks are not actual
escrow-path recovery, production custody acceptance or current-release images.
D12 canary/restore/legacy-retention and credential-consumer gates remain open.

The older application-level goal remains observed paused because no available
tool can edit its objective or resume it. This document updates repository work
scope under owner authorization, not the application's stored goal state, and
does not claim v1.7 or the long-term roadmap complete.

The production panel on OC424 is v1.6.29/510f7d3, retaining the compiler-only
v1.6.26 patch and v1.6.27's five admin tools' in-panel navigation. The owner's
earlier acceptance of v1.6.27 is not authenticated acceptance of the new patch.
The public hostname is `nekomari.orderly2233.org`. No Agent, credential or
backup-encryption cutover occurs during this panel-only update.

Continue with bounded v1.7 security candidates on clean main rather than the
accumulated unreleased development workspace. The first two batches implement
query hardening and scoped API keys; the third implements report/queue invariants,
and the fourth candidate implements client-token rotation.
Source verification/merge, release artifacts and OC424 delivery are distinct
steps. Token rotation and encrypted-backup cutover are not bundled here.

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
build or production acceptance; later release-platform and OC424 evidence is
recorded separately above. Original baseline refusals and
two fixture defects (independent comparison clocks and a missing same-origin
handshake header) remain recorded; neither production policy nor assertions
are weakened. No redundant local full-suite, frontend rebuild or capacity run
is performed for the query-only deployment.

Before the scoped-key cutover, inventory legacy API consumers and arrange scoped
replacements for intentional compatibility changes. Production backup encryption
still needs approved age key custody/escrow/unlock, an actual restore drill and
legacy-copy retention. General development/deployment permission does not decide
these policies or complete the v1.7 exit criteria. Unreleased-candidate platform,
SLO and secrets-at-rest gates likewise remain open.

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
