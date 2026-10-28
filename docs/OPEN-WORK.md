# Open work

**This is the live list.** It is the only document in `docs/` that claims to say what is
still open; every other one is a dated record of a moment. If you are looking for "what
is left", read this file and nothing else. The long-term version plan (features, SLOs and exit criteria) is [`LONG-TERM-PLAN.md`](./LONG-TERM-PLAN.md).

Rewritten 2026-10-13, after [`IMPROVEMENT-AUDIT-2026-10-13.md`](./IMPROVEMENT-AUDIT-2026-10-13.md).
That audit found the reason this file had stopped being usable: the complete open list
had been delegated to `docs/audits/2026-10-01/FINDINGS-STATUS-2026-10-01.md` — a frozen
snapshot that was already wrong on four findings within days — while `ROADMAP.md`,
`DEPLOY-OC424.md` and this file each still presented their own superseded state as
current. Three tables, none of them live.

What changed on 2026-10-13 is in `CHANGELOG.md` under `[Unreleased]`; the audit document
holds the evidence and the acceptance criteria for each item. This file is the list.

**Current checkpoint (2026-10-28, Asia/Shanghai):** OC424 serves
**v1.7.7/8e1dafa** at `nekomari.orderly2233.org`, healthy/restart0. Exact
CI40642445800 8/8, release40643116847 10/10, Docker40643699302 2/2,
Linux checksums/provenance, dual-architecture image identity, ten published-binary
embedded-admin scenarios on OVH, OC424upgrade9/9,public23/23 and scoped
forecast14rows pass. Prior v1.7.6 actual SQL recovery is dated evidence; no
unrelated broad local SQL/capacity rerun.
Earlier v1.7.4 SLA14nodes/58tasks remains dated evidence, not a newly rerun
SLA live comparison. The five tools remain
in the admin shell. After the earlier removal, Pi adds a node with the owner's
explicit authorization: the current approved baseline is 14 registered clients
and 14 currently fresh CPU-reporting nodes. Preserve that addition; the earlier
13-client release records are historical. Existing UUIDs/tokens and Agent services are
unchanged. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md) for immutable artifacts,
actual evidence and patch rollback to v1.7.6. The earlier v1.7.0->v1.6
auth-migration rollback still requires the paired old binary and DB.

**Adjusted active objective:** close the bounded v1.7 delivery/handoff, then
continue small v1.8 observability/correctness batches. Do not mark the entire
roadmap or every v1.7 exit criterion complete. All 200-node experiments/benchmarks
and their 26 failed reports remain deferred. Reuse exact-source and unchanged
source-batch checks; no redundant broad local frontend/Go/capacity rerun.

**Current candidate, not deployed:** v1.7.8 adds node uptime history and an
in-panel admin view, one bounded part of v1.8 observability. Historical buckets
retain the last positive valid reading, not reboot events; zero/absent/invalid
readings remain unknown and old history is not backfilled. The new metric has
its own default one-day retention. Publication, OVH published-artifact checks,
encrypted backup/old-new recovery and OC424 acceptance remain required. Old
versions can delete the new metric on manual storage reclaim after rollback;
preserve a post-upgrade encrypted backup before any downgrade or reclaim. See
[`UPTIME-HISTORY.md`](./UPTIME-HISTORY.md).

**Latest bounded delivery:** v1.7.7 fixes missing/invalid paid-node expiry:
neutral localized unset/unknown labels replace fabricated countdowns/NaN,
without claiming lifetime service or changing valid dates. Focused static/mounted
checks and ten real published-binary admin scenarios pass across five locales
and desktop-light/mobile-dark. The test-only AbortController arrival race is
closed without changing fetch runtime. Pre encrypted recovery boots pinned
v1.7.6/v1.7.7 in8.25/9.42s, both DBs/14clients/current auth, no `ANALYZE`.
Post ciphertext boots exact-v1.7.7 in7.81s with the same checks and cleanup.
No schema/API/auth/Agent/dependency/credential change. See the deployment runbook.

**Prior bounded delivery:** v1.7.6 closes the dsh review's MySQL/PostgreSQL
recovery argument mismatch and MySQL parent-division defect, preserving the
v1.7.5 series-driven SQLite optimization. Real SQL old-source failures, fixed
races, dedicated CI and actual released-binary install/recovery pass. Fresh
pre ciphertext boots pinned-v1.7.5/v1.7.6 in8.41/9.74s; post boots exact-v1.7.6
in8.71s, without `ANALYZE`. All14UUIDs/tokens/reporters, schemas, grants/expiry,
GeoIP and quiet backup health remain; no Agent/Hermes restart. See
[`DSH-REVIEW-2026-10-27.md`](./DSH-REVIEW-2026-10-27.md).

**Prior bounded delivery:** v1.7.4 SLA task-generation normalization. Unique
chronological measured buckets feed loss/latency/incidents/presence/maintenance;
empty placeholders cannot override actual values. Focused regressions/races,
exact CI/publication, six installed synthetic-Panel groups on OVH, 14-client
old/new-image encrypted recoveries and OC424 continuity pass. Live scoped SLA
reads 14 nodes/58 tasks and compares 11 tasks with preserve-series bucket means
across two reporting nodes; none of those 11 tasks has multiple generations in
that window. The overlap-specific case is verified in synthetic fixtures, not
claimed from this live comparison or the 44 multi-generation source group count.
No history rewrite, coverage-definition, Agent/token/scope/backup-health change.
See [`SLA-GENERATIONS.md`](./SLA-GENERATIONS.md).

Existing v1.7.3 health service/timer and quiet persisted state remain unchanged;
recurring 15-minute checks are observed successful. The first scheduled nightly
backup is verified on 2026-10-28: timer11:30:23 -> service11:31:04
Asia/Shanghai, success/exit0, matching ciphertext/checksum pair. This closes the
first-trigger gate, not an independent-media restore or a measured RPO guarantee.
Live incident delivery and independent media remain open. dsh records owner
history/audit/API-key UI acceptance; no new owner session is exercised here.
v1.7.5 provides the SQLite source startup fix; v1.7.6 cross-backend corrections
are released/recovered/live-accepted. No stored history rewrite or production ANALYZE.
Continue one bounded correctness/observability item, with 200-node work deferred.

**Next bounded work:** verify and deliver the v1.7.8 candidate above before
choosing another batch; keep 200-node work deferred. Independent-media recovery, real incident delivery,
complete API-caller inventory and the span/window-coverage decision remain open.
No whole-roadmap app goal change or new owner-session UI acceptance is claimed.

### v1.7 release readiness (2026-10-28)

| Gate | Current state | Required next evidence/action |
|---|---|---|
| Source/publication/deployment | Through nullable expiry v1.7.7/`8e1dafa`; exact CI40642445800 8/8, release40643116847 10/10, Docker40643699302 2/2,OVH10scenarios,OC4249/9,public23/23,scoped forecast14rows and14-client/auth continuity pass | Retain immutable tags/assets and truthful release evidence; do not rebuild them for documentation |
| Legacy API consumers | Owner-configured `hermes-node-management` matches exactly `nodes:read` + `nodes:write`, active365days until2027-10-27 13:38:02 Asia/Shanghai; actual MAC-WAN disposable create/edit/read/remove passes and original13 metadata is unchanged | Reviewed CLI writes may resume; no token/exec/update grant or key rotation. Other callers/runtime rewiring are not inferred from three reference edits; old30-day read-only key remains |
| Token recovery/edit/import | Owner accepts restrictions and requests no rotation; 14 current UUIDs/tokens and all Agent services unchanged | No rotation canary now; future rotation requires independent SSH/console and per-node consent |
| D12 custody/recovery | OC424 public recipient only, OVH ciphertext, MAC-WAN online identity/recovery runner; fresh14-node v1.7.7 backups and pinned v1.7.6/v1.7.7 recovery, no ANALYZE; exact pre/post evidence in runbook | Owner reports offline copy downloaded; independent-media restore is still unverified. Same-UID Hermes/root/Docker exposure remains explicit |
| Nightly schedule/retention | Enabled daily03:30UTC OVH timer, keep14 ciphertext pairs; first actual scheduled trigger2026-10-28 11:30:23Asia/Shanghai and service11:31:04success/exit0 verified, with matching archive/checksum; release copies remain outside pruning | Initial/later recurring health checks pass; actual incident delivery remains unverified. No independent-media or measured RPO claim. No old plaintext/manual/local rollback copy is deleted; deletion needs an explicit policy |
| OC424 authenticated acceptance | Owner elects self-run history/audit/API-key UI checks after deployment | **Closed in dsh's 2026-10-27 record**: the owner reported the post-deployment history/audit/API-key pages check out. This review does not repeat an owner session. Public shells, 401 responses and synthetic checks cannot substitute for it; acceptance does not extend to other surfaces or future releases |
| Rollback | Fresh per-DB WAL-safe snapshots/compose + v1.7.6 image in `rollbacks/v1.7.7-20261028T063016Z-64133ab0`; no schema/auth change, keep current data | v1.7.7->v1.7.6 uses prior compose/image, keeping14nodes and health env/state. Across v1.7->v1.6 freeze writers and restore paired old DB + binary, losing newer data/permissions |

**Hermes handoff:** the gateway is active again (PID922780/restart0); AgentPID976/
restart0 is unchanged. The metadata-only CLI stays checksum-matched on MAC-WAN;
three references explicitly forbid old API/token/install examples for this Panel.
Old-key writes are server-denied. Dedicated-key metadata/private storage and
actual disposable CLI writes pass; three reference overrides/marker now permit
that reviewed workflow without a service restart. This is not a complete task
inventory or owner UI acceptance; no key/session is recovered from SQLite,
backups, process args or Hermes history. Installation is owner-UI only; no token-read/exec/update grant is
silently added. See [`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md).

**Backup handoff:** fresh v1.7.7 pre/post archives from the installed OVH service
pass actual isolated MAC-WAN recovery with pinned v1.7.6/v1.7.7 images,both
DBs,14 clients/current auth metadata and cleanup. Independent regular release
copies stay outside14-pair pruning; all older copies remain. Earlier13-client
release backups are historical; never use them to overwrite Pi's added node.
Owner-reported offline download is not independently exercised media recovery.
The daily timer is unchanged; health reporting/alerts and initial/later recurring
checks pass. First scheduled nightly backup is verified2026-10-28; measured
SLO/RPO/RTO and independent-media restore remain separate. See
[`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md).

**Verification scope:** focused Go1.27.3 AbortController races/vet, expiry static,
type/lint/mounted and real embedded-browser checks; required exact-source
CI/publication; attested ten-scenario installed-Panel OVH acceptance; encrypted
recovery, scoped forecast and live continuity. Prior SQL/live SLA evidence is reused.
Unchanged backup-health/query/scoped-key/report/queue/rotation/Hermes source
checks are reused, not broadly rerun locally. Only owned disposables are cleaned;
all backups,rollback images and shared caches remain. No fleet upgrade,owner
login,capacity/SLO or unattended decrypted recovery is claimed.

The application-level all-roadmap goal was previously observed paused. The owner
revokes the pause and narrows repository work, but the available goal interface
cannot edit the objective/resume app state; this is not a claim it was changed.
Source-batch details remain in the dedicated guides/changelog and retained CI,
not an open task to rerun unchanged checks.

**Updated 2026-10-19, after the `2026-10-18` review** (`docs/audits/2026-10-18/SUMMARY.md`). Three
batches shipped: the low-risk one (sensitive-operation gate on credential export and fleet rollout,
menu entries for the five standalone pages, maintenance suppression for load/ping alerts, gzip for
static assets, admin-copy deduplication, idle timeout, lazy chart, list polling); the next one
(on-demand language bundles, definition/fingerprint caches on the ingest path, RPC2 exponential
backoff); and the third one (default-theme disk cache, plugin-hook cap and timer, cgroup-aware soft
memory limit, byte-budgeted reads, the coarse-rollup measurement, and a prompt-based service-worker
update). v1.6.16 then closed the operational follow-up (upgrade-archive exclusions and retention,
startup integrity/index check, theme-cache warm-up, H6 target persistence, and maintenance in node
status); v1.6.17 relaxed the startup integrity gate after it proved too strict on this database;
v1.6.18 excluded maintenance windows from SLA, added the daily integrity check, and added an
authenticated deploy verification script. v1.6.19 completed the H6 clear-delivery edge, added
release SBOM/provenance, pprof process/pool data, and made Linux frontend builds reproducible.
v1.6.20 hardened the upgrade path (daily archive pruning and a read-only post-upgrade check),
added the metric store's pool counters, and wired the admin table's selection to the bulk editor.
v1.6.21 added audit-log filters/CSV export; v1.6.22 fixed the three netstatic counter bugs;
v1.6.23 added `--version` and clarified panel-directed updates; v1.6.24 added sub-millisecond
ping precision; v1.6.25 made the ping summaries fractional, gated token reads behind the
sensitive step-up, added OpenRC installer support and a unified upgrade script, and added the
protocol-copy contract test. The memory/performance table is empty as a result; the review's
remaining items are the feature gaps below, and the dated report holds their evidence.

## Decisions waiting on the owner

Things that are not engineering problems but need a person to choose. Each one says what
it blocks, so it can be answered out of order. **Added 2026-10-13**, restoring a practice
that `ROADMAP.md` §F had and lost when it was frozen.

| # | Decision | Options, and what I recommend | What it blocks |
|---|---|---|---|
| D2 | **Turn `ssrf_protection_enabled` on in the panel** | On, **if** every market source you use is a public host. A changed default does not rewrite a stored value, so this is a switch, not a redeploy. | Market downloads stay unfiltered, and the target is chosen by remote catalog content |
| D4 | **Push straight to `main`, or branch + PR?** | General suitable push/OC424 update permission is granted; use branch + PR as the conservative default. | Exact-candidate CI, compatibility, backup/rollback and release acceptance remain required |
| D7 | **`topology/` — commit or delete?** and **`docs/OC424-MTPROTO.md` — commit with the next batch?** | The MTProto doc is finished and its secret scan passes: commit. `topology/` is a static page whose own README says it is not wired into anything and was withdrawn the day it went live: decide before it rots. | Two untracked trees sit outside both README indexes |
| D8 | **Two operational facts I cannot check from here** | (a) the admin password from `SECRETS.md` incident 4 — has it been changed? (b) the `AAAA 100::` record and Worker for `monitor.orderly2233.org` — removed? | Both are recorded as open in `RETIRE-CF-PROBE.md` / `SECRETS.md` and neither can be verified from a checkout |
| D9 | **Authentication policy, as a group** | (a) `sso_id` after a password change; (b) v1.7.0 exact grants and the owner-configured Hermes handoff are deployed, but other callers are not fully inventoried; (c) OIDC + 2FA challenge flow; (d) globally invalidated sessions | The completed scoped-key cutover does not silently decide other account policies, grant broader Hermes permissions or retire remaining callers |
| D10 | **How much of the remaining frontend polish to do at once** | Three known batches: ~33 unconverted `toLocaleString*` sites (the helper now exists), ~24 re-implementations of the mutation-envelope check, and 8 `dangerouslySetInnerHTML` sites fed by locale strings | Each is mechanical but wide; they are the kind of change that is safe alone and risky mixed with a release |
| D11 | **Agent privilege and platform footprint** | Non-root agents are still the exception (MAC-WAN); ICMP tasks force root on hosts without `setcap`. Windows/macOS agent footprint and peak memory are unmeasured. Ping precision shipped in v1.6.24, so it is no longer part of this decision | Moving agents between users/init systems is a fleet-wide change; the footprint gaps are measurements, not code |
| D12 | **Independent-media recovery and legacy retention** | MAC-WAN custody, actual pre/post encrypted recovery, encrypted nightly schedule and v1.7.3 health/heartbeat alerts are delivered. Owner reports offline copy downloaded; actual independent-media restore remains unverified. | No identity on OC424/OVH; same-UID Hermes/root/Docker remain trusted. All old plaintext/manual rollback copies remain; deletion and periodic restore-drill policy still need explicit treatment |

## How to read this

| Column | Meaning |
|---|---|
| Item | one sentence, enough to recognise it |
| Where | the code or the doc that owns it |
| Why it is still here | the reason it was not done — a decision, a dependency, or a size |

**A row is not a promise.** Several are deliberate decisions to do nothing (marked
*accepted*), and two of the audit's findings were withdrawn after checking (marked
*withdrawn*). Both are listed, because "we decided not to" is a different fact from
"nobody looked".

## Security

| Item | Where | Why it is still here |
|---|---|---|
| History query hardening owner acceptance | `web/rpc/jsonrpc/common.record.go`; `web/rpc/jsonrpc/budget.go` | Released/deployed as v1.6.28/502a269: 366-day ceiling, shared budgets/cancellation and guest visibility filtering. **Closed in dsh's 2026-10-27 owner-acceptance record**; no new owner session is performed here and future releases are not pre-accepted |
| Client-token rotation owner acceptance | PR81, `docs/CLIENT-TOKEN-ROTATION.md` | Published/deployed in v1.7.0. Owner accepts edit/import restrictions and requests no rotation now; no production rotation canary is required. Owner UI acceptance remains; future rotation requires independent per-node recovery, never SQLite edits |
| Scoped API-key remaining consumers | `internal/apikeys/`, `pkg/rpc/apikey_scopes.go`, `docs/API-KEYS.md`, `docs/HERMES-NODE-CLIENT.md` | v1.7.0 delivered; owner-configured Hermes key and actual disposable CLI writes pass; dsh records owner API-key UI acceptance. Reconcile other callers before retiring the migrated30-day read-only key; no complete runtime inventory or new owner session is claimed |
| Audit-log pagination hardening owner acceptance | `web/rpc/jsonrpc/admin.system.go` | Released/deployed as v1.6.28: 1-1000 rows, checked offsets, strict binding and cancellation. REST/filter/CSV compatibility verified in exact-source CI; **closed in dsh's 2026-10-27 owner-acceptance record**, without a new owner session in this review |
| `/plugin/*` is still owned by the installed theme | `web/public/public.go` | **Accepted**: the panel took back only its own pages. Tightening it would break installed themes |
| An installed theme that renders "no data" as 0 | outside this repository (SAO) | **Accepted**: the default document is the panel's own now; the remaining risk is a third-party bundle |

## Data safety

| Item | Where | Why it is still here |
|---|---|---|
| Restored metrics startup could exhaust connection deadlines with stale planner statistics | `pkg/metric/coarse_rollup.go`; `docs/METRIC-RECOVERY-STARTUP.md` | **Fixed and deployed in v1.7.5**: the recovery statement constrained the tier through a join, so stale statistics drove the scan from the resolution index and re-read every series' rows once per metric; it now binds the tier id and forces the series-leading index on SQLite. Released binaries on a production-shaped restored store: 1.7s vs 30s+ deadline-exceeded, and MAC-WAN pinned v1.7.5 recovery passes on fresh pre/post ciphertext. The pinned v1.7.4 image still cannot restore a fresh archive without the scratch `ANALYZE` workaround, which is a rollback caveat rather than an open defect. Independent-media restore remains unverified and no production database was ever analysed |
| The legacy monitoring migration has no checkpoint, so a crash restarts it from the beginning | `internal/migrations/legacy_monitoring.go` | Re-running is idempotent (argued in the audit report), so the cost is time, not correctness |
| The auto-upgrade archive excludes `metrics.db` by design | `database/dbcore/dbcore.go` | **Accepted** — but it should be stated in the upgrade log, so an operator does not read the archive as a full backup |
| ipinfo caches an empty country for 48 h; an empty provider logs nothing | `utils/geoip/ipinfo.go`, `utils/geoip/geoip.go` | Cosmetic-to-misleading; no incident has been traced to it |
| The "persistent data" warning is inverted under a custom docker data-root | `database/dbcore/dbcore.go` | The regex only matches the default volume path, so a custom root prints "persists" for something that does not |
| Session tokens and the 2FA secret are stored unencrypted at rest | `docs/AUTH-HARDENING.md` §3 | Proposal only; needs a key-management decision before code |

## Numbers and correctness

| Item | Where | Why it is still here |
|---|---|---|
| `Presence.Coverage` is a span, not a window coverage | `internal/sla/availability.go`; `docs/SLA-COVERAGE.md` | Clarified 2026-10-27 without changing behaviour: coverage is observed/expected over the observed span, capped by the requested window, so a series that reports only the last ten minutes of an hour reads 1 (window fraction 1/6). The field comment, worked examples, correct reading for freshness and the compatibility rules are now stated; `batch_test.go` still pins the value. Still open: the owner decision on adding a separate window-coverage field (which must also report the inferred cadence) — an in-place redefinition would move every stored figure and is not done |
| `PriceTags` missing/invalid expiry | `frontend/src/components/PriceTags.tsx` | **Fixed/deployed v1.7.7**: neutral localized unset/unknown labels preserve real dates/tags; focused, real embedded-browser, publication and OC424 continuity pass |
| Dragging under a filter reorders only the visible subset | `frontend/src/pages/admin/index.tsx` | Needs a decision about what reordering a filtered list should mean |
| Uptime history / restart count | `docs/UPTIME-HISTORY.md` | Uptime history is a v1.7.8 candidate, not yet deployed. It records bucketed positive readings, not reboot events; trustworthy restart counting remains open |
| One bad metric parameter fails a whole batch query | `web/rpc/jsonrpc/public.metric.go` | Should report per-key rather than all-or-nothing |
| Non-root ICMP and Windows/macOS agent footprint | `agent/` | netstatic, ping precision and the Linux footprint measurement shipped (v1.6.22–v1.6.25). Still open: non-root agents without `CAP_NET_RAW` cannot probe, and Windows/macOS peak memory/disk growth are unmeasured |
| Legacy ping quantiles are unknown across the migration window | `web/rpc/jsonrpc/public.metric.go` | **Accepted**: the report says so itself |
| Timestamps render in the viewer's own timezone with no zone label | ~33 remaining `toLocaleString*` sites | The shared helper now exists (`components/status/slaFormat.ts`'s `formatTime`, which takes the language and appends the zone) and the SLA incident list — the surface where a shareable report makes the ambiguity matter — uses it. The rest are the same one-line change, site by site |

## Feature gaps (from the 2026-10-18 review)

| Item | Where | Why it is still here |
|---|---|---|
| Feedback from the `2026-10-18` feature audit not yet scheduled | `docs/audits/2026-10-18/feature-gaps.md` §FG-10…FG-20 | Audit-log filtering and CSV export shipped in v1.6.21. Still open: metric/record CSV export, exec result history, forecast cost column, CPU temperature and disk I/O (agent release), HTTP status/TLS expiry (agent release), single-account/roles decision, English server errors in the UI |

## Release, deployment and operations

| Item | Where | Why it is still here |
|---|---|---|
| Most agents still run as root; only MAC is non-root | `docs/DEPLOY-OC424.md` | The NOSLA pattern (unprivileged user + `AmbientCapabilities=CAP_NET_RAW`) works; Alpine has no `setcap`. OpenRC install support shipped in v1.6.25, but moving users is still a fleet-wide change |
| Windows local builds can fail on a locked esbuild temp file | Windows dev environment | The build itself is deterministic on Windows: two builds with a project-local `TEMP`/`TMP` were byte-identical (819 files, diff 0). With the system `%TEMP%`, esbuild sometimes cannot delete its temp file ("Access is denied"); CI's Windows runner is unaffected. Not a source change — a dev-environment workaround until the temp cleaner/AV exclusion is settled |
| Encrypted backup follow-up | `docs/ENCRYPTED-BACKUPS.md`; `docs/MAC-WAN-RECOVERY.md` | Encrypted nightly timer/service and v1.7.3 freshness/heartbeat alerts delivered; recurring health and first scheduled nightly2026-10-28success verified. Owner-reported offline download is not independent-media restore. Live incident delivery/stronger drill policy remain open; keep all old plaintext/manual rollback copies until explicit deletion approval |
| OIDC + 2FA makes SSO login fail-closed with no way for the browser to answer the challenge | `docs/ROADMAP.md` §F12 | Deliberately shipped as a gate, not half a state machine. Needs a one-time pending token plus a code prompt; production impact is zero while OIDC is off |
| `sso_id` survives a password change, and `DeleteAllSessions()` is global | `database/accounts/` | Policy questions now that the SSO path requires TOTP; recommendation was (b)+(c) in the audit report |
| A committed admin password from the SECRETS incident still has to be changed | `docs/SECRETS.md` incident 4 | An ops action on the live panel, not a repository change |
| A Cloudflare Worker and an `AAAA 100::` record for `monitor.orderly2233.org` remain | `docs/RETIRE-CF-PROBE.md` | Removable only in the Cloudflare dashboard |
| `topology/` is uncommitted and its README says the site was withdrawn the same day it went live | `topology/` (untracked) | Needs a decision: commit it (its secret scanner passes), or delete it. It is currently in neither README's index |
| The connection-pool counters are snapshots, not history | `web/api/admin/pprof.go` | v1.6.20 added `db_pool`/`metrics_db` snapshots with `wait_count`; there is no trend series, so a queue that clears between page loads is invisible |

## Accessibility, verification and visual checks

| Item | Where | Why it is still here |
|---|---|---|
| No screen-reader pass over the node-detail chart or the dashboard | `frontend/script/chart-a11y.spec.py` is keyboard-only | Needs an actual assistive-technology pass |
| OAuth admission has never been exercised against a real provider | `docs/OAUTH-INTEGRATION.md` | Production OAuth is off; every provider path is tested against a fake |
| A mixed-family task's chart legibility has not been reviewed on real data | `docs/ROADMAP.md` C2 | Visual; needs a human looking at the dashboard |
| `MiniMetricChart` ("最近 1 天") has never been rendered with data | `frontend/src/components/MiniMetricChart.tsx` | Shares a code path with charts that have been, which is why it keeps being deferred |
| The edge path (origin → US-East POPs) and whether `PUBLIC_MAX` is the right alert threshold | `deploy/panel-probe.sh` | POP-specific and time-window-specific; needs a live sample, not a code change |

## Repository and documentation hygiene

| Item | Where | Why it is still here |
|---|---|---|
| `README.md`'s doc index lists 14 of ~40 documents | `README.md` | It omits ROADMAP, PERFORMANCE, AUTH-HARDENING, AGENT-FOOTPRINT and both `docs/audits/` trees, so it is not a map |
| Two broken relative links | `docs/audits/2026-09-30/notes/README.md` | One needs `../../`, the other `../../../` |
| Three documents nothing references | `HEALTH-REVIEW-2026-09-19.md`, `MONITORING-ACCURACY-REVIEW-2026-09-30.md`, `OPTIMIZATION-RECOMMENDATIONS-2026-09-26.md` | Orphans; either link them or fold them into the record they belong to |
| `docs/audits/` has no retention policy or owner, and one tree ships a production-mutating script | `docs/audits/2026-09-30/` | Keep audits as immutable dated evidence, but say so somewhere and decide what stays |
| The React Compiler rules are advisory until the remaining findings are triaged | `docs/ROADMAP.md` A4, `frontend/script/audit-compiler.mjs` | Now a CI step and currently at 0; adoption needs the triage, not the gate |
| `TerminalDialog` claims modality it does not enforce | `frontend/src/pages/terminal/TerminalDialog.tsx` | `aria-modal="true"` with no Tab trap, and focus is not returned to the trigger on close. The destructive half — Enter on the focused *Cancel* button running the confirm action — is fixed (the decision moved into `dialogKeys.ts`, which is unit-tested); a Tab trap needs a decision about focus order across the suggestion list |
| The mutation-envelope check is re-implemented at ~24 call sites | `frontend/src/lib/`, `frontend/src/pages/admin/` | Only the node table uses the shared helper; the loosest call site does not check `response.ok` at all |
| Locale strings are injected as raw HTML at 8 sites with `escapeValue: false` | `frontend/src/i18n/config.ts` + 8 components | Today's values are benign markup, but `frontend/crowdin.yml` accepts outside contributions and `dompurify` is already a dependency |
| Absolute asset URLs ignore the configurable `base` | `frontend/src/components/Flag.tsx` + 2 more | A sub-path deployment gets 404 flags and logos |
| `utils.DataMasking` and its Levenshtein chain are dead code | `utils/utils.go` | ~150 lines with no caller, and it suggests log redaction happens there (the real redaction is regex-based in `internal/metricstore/redact.go`) |
| The theme extractor honours the archive's own file modes while the plugin extractor does not | `web/api/admin/theme.go` vs `internal/plugin/install.go` | Two hardening standards for the same untrusted input |
| `/api/clients` re-identifies the principal on every WebSocket message | `web/api/ws.go` | A session lookup per frame, behind the same lock the session cache used to hold |

## Withdrawn — checked, and not defects

| Item | Why it is not a finding |
|---|---|
| "The rollup cleanup deletes a whole tier unconditionally" | Both branches are deliberate. A metric with `RetentionDays: 0` is a **disabled metric**, and tiers that `withMetricRetention` omits are derivable from the retained finer tier. `TestCleanupExpiredRemovesExpiredAndInvalidTiers` and `TestUpdateMetricRetentionDefersDisabledMetricCleanup` pin both |
| "The restore path overwrites `MigrationTargetKey`, so a migration sees source == target" | The write is correct; the *documentation* was narrower than the behaviour. The key records the current target, and the refusal is explicit and now says what to pass instead |

## Historical

The 2026-09-18 batch this file used to track is fully released (v0.1.6 through v0.1.8),
and its decisions are recorded where they happened:

| Record | What it holds |
|---|---|
| `docs/ROADMAP.md` | the audit of what was wrong (sections A–G), the six-feature plan (H), and the decisions waiting on the owner (F). **Frozen 2026-09-27**; read it as history |
| `docs/DEPLOY-OC424.md` | the deployment runbook and one section per rollout, each dated |
| `docs/audits/2026-09-30/`, `docs/audits/2026-10-01/` | dated audit evidence. The 2026-10-01 `FINDINGS-STATUS` table is a **snapshot**, superseded by this file |
| `docs/FOLLOWUP-v0.1.*.md` | per-release follow-ups |
| `CHANGELOG.md` | what shipped, per release |

**Things that will bite again** (kept from the previous revision, all still true):

- **2FA breaks password-only automation.** Every script that logs in must go through
  `deploy/nekomari_auth.py` with `NEKOMARI_2FA_SECRET` set. The failure looks like a
  broken script rather than a rejected login.
- **`/api/me` returns 200 for guests.** Check `logged_in`, not the status code.
- **Test theme changes against the origin on the host.** Through Cloudflare a cache hit
  and dead code are indistinguishable.
- **Do not reason about minified bundles.** Run `deploy/theme-contract-check.mjs`: it
  pulls the schema out of the installed theme and reports the exact rejected field path.
- **A client's strict schema is part of the server's contract even though the server
  cannot see it.** Four rounds were spent reasoning about minified code and three of them
  produced confident wrong answers.
- **Deleting a node does not tell the host to stop reporting.** Pair it with stopping the
  agent, or the panel logs a permanent 401 stream that looks like an auth problem.
