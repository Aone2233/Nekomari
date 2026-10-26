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

**Current checkpoint (2026-10-26):** OC424 serves v1.6.27/12ad73a at
`nekomari.orderly2233.org`; the five tools now open inside the admin shell.
The owner confirms the current result is fine and requests next-stage development.
The first v1.7 candidate is isolated from the exact live source and contains only
history/log query hardening. It does not deploy the separate accumulated v1.7+
workspace. Production remains v1.6.27 until a version passes its release gates.

**Adjusted active target (owner authorization, 2026-10-26):** complete bounded
v1.7 security batches with explicit verification and release gates, rather than
trying to finish every long-term version in one continuous run. Query hardening
PR78 is merged as 502a269 with merge CI40275930537 successful (7/7).
Scoped-key PR79/5b005d5 is merged as 3dd20fb with an identical tree and merge
CI40280921167 successful (7/7). Both remain unreleased. The scoped-key contract
is in [`API-KEYS.md`](./API-KEYS.md); its local/UI/migration evidence is retained,
not rerun in every backend-only batch.

The next isolated batch, `codex/v1.7-report-queue-invariants`, starts from
3dd20fb. It clamps only non-negative, in-range RAM/Swap/disk `used > total`
snapshots after all other validation succeeds. Accepted uploads record a
resource-set warning once per anomaly episode; healthy samples reset it.
Invalid uploads and failed persistence do not update runtime/anomaly state.
Queue entries are retired after successful node removal, or by the existing
minute scheduler once empty and idle for five minutes. Deleted-queue polls wake
without recreating state or reading a replacement generation. Acknowledgement,
expiry, coalescing, overflow and removal clear discarded payload references;
the newest-128 limit, command TTLs and ordinary reconnect behavior stay intact.
This is not a transport credential-revocation fence: authenticated in-flight
requests/connections are not redesigned here. Client-token rotation is next.

The two former data-safety findings are implemented in this bounded source
candidate and remain unreleased. Targeted ordinary/race checks cover the
affected packages, real HTTP/WebSocket ingest and REST removal, queue lifecycle
and scheduler wiring. Final affected checks pass 211 top-level tests (524 with
subtests) across six packages ordinarily/race, using retained final runs per
package rather than claiming one run. The Linux CI race gate now includes the
four report/queue/runtime packages. OVH's exact prebuilt race executables pass
nine top-level groups (13 with visible subtests) in private fixtures, with
Agent495903/restart0 unchanged. This is not a remote-native build or production
acceptance. No Agent/protocol, dependency,
frontend, later metrics/history or backup-policy change is part of this batch.

The application-level all-roadmap goal was still observed paused, and this
session exposes no objective/resume control. The repository target is updated
and development continues under owner authorization; this is not a claim that
the application's stored goal has been edited, resumed or completed.

The repository's key-consuming scripts are inventoried; no helper reads a key
from SQLite. Out-of-repository live consumers and secure replacement provisioning
remain a **release/cutover gate**, not proof supplied by source tests. Legacy keys
become30-day `nodes:read` credentials; this intentional security compatibility
change needs an explicit migration arrangement under the v1.x policy. No tag or
OC424 credential/schema update occurs in this preparation batch. A downgrade needs
the old binary **and pre-upgrade DB**, not the existing image-only rollback.

**Current priorities:** 200-node work remains deferred, with its original limits
and all26 failed reports preserved. Use OC424 for bounded real-environment
acceptance and OVH for suitable isolated tests/off-host backups. Existing general
push/update authorization remains valid; prefer branch + PR and candidate-relevant
checks, not redundant broad test cycles. Scoped API-key consumer compatibility,
backup key custody and other account/privilege/SLO policies are separate gates.

**First-batch verification:** both affected packages pass151 top-level checks
ordinarily/race (379 including subtests); real HTTP/WebSocket and REST controls,
vet, module verification, Panel build and reachable binary scan pass. OVH's exact
prebuilt race executable passes12 focused groups in private fixtures without
Agent restart. This is not remote-native/arm64/Windows CI or a production update.
Initial baseline and fixture failures are retained; no full-suite/frontend/
capacity rerun or legacy-credential/configuration change occurs.

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
| D3 | **What the release-cadence check should compare** | Compare `agent/**/*.go` and `protocol/`, not all of `agent/ protocol/ pkg/`. The agent has its own `go.mod` and imports none of the server's packages, so `pkg/rpc` and `pkg/secretcmp` changing says nothing about the fleet — and the current rule reads the deleted `agent/.github/workflows/*` as a fleet change. | Every panel-only release is mislabelled "the fleet moves", which costs a needless rollout or trains people to ignore the check |
| D4 | **Push straight to `main`, or branch + PR?** | General suitable push/OC424 update permission is granted; use branch + PR as the conservative default. | Exact-candidate CI, compatibility, backup/rollback and release acceptance remain required |
| D7 | **`topology/` — commit or delete?** and **`docs/OC424-MTPROTO.md` — commit with the next batch?** | The MTProto doc is finished and its secret scan passes: commit. `topology/` is a static page whose own README says it is not wired into anything and was withdrawn the day it went live: decide before it rots. | Two untracked trees sit outside both README indexes |
| D8 | **Two operational facts I cannot check from here** | (a) the admin password from `SECRETS.md` incident 4 — has it been changed? (b) the `AAAA 100::` record and Worker for `monitor.orderly2233.org` — removed? | Both are recorded as open in `RETIRE-CF-PROBE.md` / `SECRETS.md` and neither can be verified from a checkout |
| D9 | **Authentication policy, as a group** | (a) `sso_id` after a password change; (b) the scoped-key candidate replaces blanket exemption with exact grants, but live consumer migration/compatibility acceptance is still required; (c) OIDC + 2FA challenge flow; (d) globally invalidated sessions | Scoped-key source work does not silently decide the other account policies or authorize the compatibility cutover |
| D10 | **How much of the remaining frontend polish to do at once** | Three known batches: ~33 unconverted `toLocaleString*` sites (the helper now exists), ~24 re-implementations of the mutation-envelope check, and 8 `dangerouslySetInnerHTML` sites fed by locale strings | Each is mechanical but wide; they are the kind of change that is safe alone and risky mixed with a release |
| D11 | **Agent privilege and platform footprint** | Non-root agents are still the exception (MAC-WAN); ICMP tasks force root on hosts without `setcap`. Windows/macOS agent footprint and peak memory are unmeasured. Ping precision shipped in v1.6.24, so it is no longer part of this decision | Moving agents between users/init systems is a fleet-wide change; the footprint gaps are measurements, not code |
| D12 | **Encrypted-backup key custody and restore authorization** | Recommend source-side age public recipients, ciphertext-only OVH storage, independent recovery escrow and a separately approved restore runner. Decide custodians/unlock and old plaintext-copy retention before cutover. | Source tooling in the separate development workspace does not establish production encryption or a custody/restore drill; this query-only candidate changes no backup policy |

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
| History query hardening release/acceptance | `web/rpc/jsonrpc/common.record.go`; `web/rpc/jsonrpc/budget.go` | Implemented in the bounded v1.7 candidate: 366-day ceiling, shared historical pool/budgets/cancellation and guest hidden/deleted-history filtering. Not released/deployed; API-key migration and unrelated roadmap work are excluded |
| No supported way to rotate a client token | `web/rpc/jsonrpc/` (only `admin:getClientToken` exists) | The only path is writing the live SQLite directly, which is what caused the 2026-08 rotation incident. Needs an API + a UI action, not a patch |
| Scoped API-key release and consumer migration | `internal/apikeys/`, `pkg/rpc/apikey_scopes.go`, `docs/API-KEYS.md` | PR79 merged as 3dd20fb; exact PR and merge CI each pass 7/7. Not released: OC424 still has the old global credential policy. Inventory/provision actual legacy consumers before the intentional read-only migration |
| Audit-log pagination hardening release/acceptance | `web/rpc/jsonrpc/admin.system.go` | Implemented in the bounded v1.7 candidate: 1-1000 rows, checked offset, strict field binding, no fetch beyond total and SQL cancellation. REST filtering/CSV compatibility retained; not released/deployed |
| `/plugin/*` is still owned by the installed theme | `web/public/public.go` | **Accepted**: the panel took back only its own pages. Tightening it would break installed themes |
| An installed theme that renders "no data" as 0 | outside this repository (SAO) | **Accepted**: the default document is the panel's own now; the remaining risk is a third-party bundle |

## Data safety

| Item | Where | Why it is still here |
|---|---|---|
| The legacy monitoring migration has no checkpoint, so a crash restarts it from the beginning | `internal/migrations/legacy_monitoring.go` | Re-running is idempotent (argued in the audit report), so the cost is time, not correctness |
| The auto-upgrade archive excludes `metrics.db` by design | `database/dbcore/dbcore.go` | **Accepted** — but it should be stated in the upgrade log, so an operator does not read the archive as a full backup |
| ipinfo caches an empty country for 48 h; an empty provider logs nothing | `utils/geoip/ipinfo.go`, `utils/geoip/geoip.go` | Cosmetic-to-misleading; no incident has been traced to it |
| The "persistent data" warning is inverted under a custom docker data-root | `database/dbcore/dbcore.go` | The regex only matches the default volume path, so a custom root prints "persists" for something that does not |
| Event-queue lifecycle release/acceptance | `web/agent/v2_events.go`, `web/rpc/jsonrpc/admin.client.go`, `internal/server/runtime.go` | Implemented in the bounded report/queue candidate: successful-removal cleanup, idle sweep, generation-isolated blocked polls and payload release. Not released/deployed; ordinary disconnects preserve valid commands |
| Report resource-clamp release/acceptance | `database/clients/report.go`, `web/api/client/ingest.go` | Implemented in the bounded report/queue candidate: validate first, then clamp only RAM/Swap/disk used to total; accepted-sample warning episodes. Invalid inputs still reject; not released/deployed |
| Session tokens and the 2FA secret are stored unencrypted at rest | `docs/AUTH-HARDENING.md` §3 | Proposal only; needs a key-management decision before code |

## Numbers and correctness

| Item | Where | Why it is still here |
|---|---|---|
| A task's series from different tag generations are concatenated before the SLA/percentile maths | `internal/sla/query.go` | The metric path got `dedupe()` in v0.1.34; this path still merges by task id |
| `Presence.Coverage` is a span, not a window coverage | `internal/sla/availability.go` | The field's own documentation disagrees with it; `batch_test.go` pins the current behaviour |
| The forecast detects a counter reset only on a net decrease, and `CrossesAt` can overflow | `internal/forecast/forecast.go` | Two boundary cases with no observed production trigger |
| `PriceTags` has no "no expiry" state | `frontend/src/components/PriceTags.tsx` | Only checks `undefined`, so a `null` expiry reads as "expires in 30 days" |
| Dragging under a filter reorders only the visible subset | `frontend/src/pages/admin/index.tsx` | Needs a decision about what reordering a filtered list should mean |
| No uptime / restart-count metric exists | `internal/metricstore/definitions.go` | A capability gap: the panel cannot state "how long was this node up" |
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
| Off-host backups are not encrypted | `docs/DEPLOY-OC424.md` Off-host backup | v1.6.25's backup now includes the three upgrade archives (~160 MB) and was successfully restored into a scratch panel (v1.6.24 served `/api/public` 200). The remaining decision is encryption at rest (`age`/`gpg`); 14 copies need ~2.2 GB of the 366 GB free |
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
| `geoip.InitGeoIp` panics on a wrongly-typed settings value | `utils/geoip/geoip.go` | The assertion is unchecked and `GetMany` returns raw JSON types; a background-goroutine panic exits the process |
| Toggling "enable GeoIP" needs a restart | `internal/server/runtime.go` | The reload handler only watches the provider key |
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
