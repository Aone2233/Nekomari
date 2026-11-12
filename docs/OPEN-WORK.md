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

**Current v1.8.1 candidate:** See [the short implementation and acceptance
status](./V1.8.1-CURRENT-STATUS.md). v1.8.1 may fix existing probe correctness
and visibility defects; the three field verification gates below remain open.
The prepared release policy keeps Agent assets manual, GitHub Latest unchanged,
and both Docker moving tags unchanged. This is not evidence of publication,
deployment or Agent rollout.

**Current v1.8.0 Probe Truth checkpoint (2026-11-09, Asia/Shanghai):** The formal
[v1.8.0 release](https://github.com/Aone2233/Nekomari/releases/tag/v1.8.0)
and its pinned panel image are live on OC424; the release record, image digest,
backup hashes, rollout evidence and caveats are in
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md). All 14 clients and 13 ping tasks remain;
all 14 Agents still report v1.7.30. The panel is additive and compatible with
old Agents, so no Agent rollout was required for this release. The Agent release
remains manual-only; nodes need an explicitly staged upgrade.
HKHKT was reached through MAC-WAN and the OC424 IPv4 jump and its CPU and ping
samples resumed. Task 30 has actual IPv6 latency and loss samples from MegaBox,
Riven and the updated Frankfurt Agent. Task 28's formerly configured reference
was cleared and, following the owner's subsequent choice, its type changed from
dual to ICMP. Both assigned nodes have fresh ICMP samples and stopped producing
TCP and reference samples. The prior protocol and reference history is retained
but hidden in the patched detail chart when current type/role are known.

Remaining acceptance and operational limits:

- The three v1.8.0 verification gates are still open: 64-theme
  browser/hydration/mobile, real external provider delivery, and LAN ICMP /
  `tc netem` loss. An isolated 12-run netem experiment now has predeclared
  loss intervals and independent send/reply counts, but physical LAN latency
  remains open; see [`V1.8.1-NETEM-PRELIMINARY.md`](./V1.8.1-NETEM-PRELIMINARY.md).
  The preliminary 64-theme browser scan identified errors and did not cover
  every mobile interaction or authenticated route; see
  [`V1.8.1-THEME-SMOKE.md`](./V1.8.1-THEME-SMOKE.md).
  **v1.8.1 is a formal patch candidate** that also fixes existing probe
  correctness and visibility defects. Publication remains gated on retained
  acceptance evidence; production deployment is separately recorded. See
  [`PROBE-TRUTH-V1.8-CHECKLIST.md`](./PROBE-TRUTH-V1.8-CHECKLIST.md) and
  [`COMPATIBILITY-V1.8.md`](./COMPATIBILITY-V1.8.md).
- The owner should refresh the signed-in MegaBox and HK04 detail pages to
  confirm that only the ICMP primary appears; mocked browser
  tests and public assets do not substitute for that session. The home-card
  `common:getRecords` aggregate can mix roles, protocols and families while
  historical data remains in its window; use the dimensioned detail chart for
  per-path readings. Reinstalling the online theme overwrites the local patch.
- One recent MegaBox task-30 minute was absent despite the v1.7.30 follow-up,
  with a `permit_write` warning. Missing observations are not packet loss; the
  underlying intermittent skip is not claimed fixed or fully diagnosed.
- MAC-WAN's prior backup and backup-health timer units are absent/inactive by
  owner design. Manual pre/post encrypted archives and isolated drills passed,
  but there is no verified automatic backup schedule or alert in this state;
  do not silently re-enable or assume one. Online archives are not atomic
  snapshots across databases and in-memory queues.
- PZYC's pre-existing `/dev/null` is a regular root-owned mode-755 file, not a
  character device. Its Agent was updated and reports, but the host-level issue
  remains for the owner to assess; the rollout did not alter it.

**Historical IPv6 reliability follow-up (2026-11-08, superseded above):** At that point OC424 ran
the internal `v1.7.30/canary3`, not a GitHub release. From 04:31 through 04:43 UTC
the persisted task 30 loss series had 10/13 complete MegaBox minutes and 13/13
Riven minutes. MegaBox skips at 04:32 and 04:42 followed its normal HTTP basic
info reports; an authenticated POST retires the pinned WS lease by design.
The 04:38 permit failure had no matching observed POST, so the earlier gaps
cannot all be attributed to that cause. Missing minutes are not loss samples.

The local follow-up adds a target-free, best-effort retirement hint: the Agent
cancels the old capability and completes a fresh challenge before any new
dispatch. If the hint cannot be sent, normal lease renewal still recovers.
An additional synchronous 100 ms retry handles transient metric write lock
contention while the existing task, credential and session fences remain held.
This is not proof of the cause of all historic gaps. Bounded, redacted stage
logs distinguish failures without printing targets or credentials, and an old
or disconnected Agent does not create one warning per scheduled minute. The
new source has not yet passed release CI or been formally published/deployed;
the exact source, GitHub assets, production panel and staged Agents all need
separate acceptance. No credentials, task assignments or history were changed.

**Historical IPv6 production canary (2026-11-07 17:25 UTC, superseded above):** At that point OC424 ran
the locally built, non-public candidate `v1.7.30/d1a15af`, not a published
stable release. Exact-commit CI 42393310935 passed 8/8. The arm64 image is
`sha256:7988a75ea14363e4e131ed577946afac27d0d7f69791b47928b73e5cae3b44b7`,
assembled on OC424 from the pinned v1.7.29 base and a Go 1.27.3 bookworm
cross-built binary. The public version endpoint reports the candidate; the
post-cutover panel check passes 9/9, with 14 fresh CPU streams, unchanged 14
client and 13 ping-task counts, and no container restarts. Task 29 is unchanged.
MegaBox and Riven alone run a separately built `v1.7.30-rc.1` Agent from the
same source, both with auto-update disabled; their existing CPU, memory, ping
latency and loss streams resumed. Original Agent binaries and unit copies are
in root-only `pinned-canary-20261107T170131Z` backup directories on each host.

A stopped-old-panel encrypted handoff is on MAC-WAN at
`manual/ipv6-cold-handoff-20261107/cold-handoff.age` (SHA-256
`a12f91567db3e5906fd76d9e8327aea90d8b2512bb38e81a0ad7a1481fdd2443`). The old
panel exited zero, no other holder of either DB was observed, and this archive
boots both old and candidate images in isolated MAC-WAN drills. A separate
isolated no-pinned-task candidate-to-old rehearsal retained all 14 client and
13 original task rows. This is **not** a proven atomic snapshot of every
in-memory sample or external writer, and it is not a downgrade procedure after
pinned tasks exist. Do not copy the archive over live data or blindly start an
old binary. A post-cutover online archive also passes the candidate restore
drill; it has the usual online snapshot RPO limitations.

On 2026-11-08 the owner created IPv6 TCP task 30 assigned to MegaBox, Riven and
Frankfurt. Real `family=ipv6` latency and loss samples are persisted for both
compatible canary Agents. Frankfurt still reports Agent v1.7.1, so it has no
pinned series and must not be presented as a packet-loss measurement. The
owner's task and node configuration must be retained. User-facing chart
rendering still needs acceptance. The old task 29 is no longer present; do not
recreate it or change current data without an explicit owner instruction.

The current candidate has not yet been published as a stable GitHub Release.
The pending release workflow changes stage Agent assets under manual-only names,
keep GitHub's latest release and the Agent image's moving aliases unchanged,
and require exact-commit CI and published-artifact checks. Retain the internal
canary and use restore/forward-fix rather than a blind old-image downgrade.
Configuration import limitations described below apply in this running candidate.

**Earlier IPv6 release-candidate checkpoint (2026-11-07 16:30 UTC):** the
production scheduler and public task creation/edit paths in the local candidate
now call the fenced WebSocket-only pinned dispatcher, and the admin task form
exposes the family selector. Existing legacy tasks cannot be converted in place:
create a new pinned task to keep its measurement history separate. The form now
explains this and says that older or POST-only Agents are skipped rather than
counted as packet loss. Configuration preview/import is also wired to the local
reviewed transactional path; schema 1/2 documents containing tasks or maintenance
windows are refused, and only the site name and description can be changed by
the importer. This is a compatibility restriction, not full legacy import
support. Earlier checkpoints below describing disabled entrypoints are historical.

The browser-to-real-handler test now uses the production RPC and scheduler with
three isolated Agent test processes, fake DNS and IPv6 loopback. It verifies a
successful sample, an absent-family diagnostic without false loss, and that a
disabled Agent receives no pinned probe. Local race suites pass 838 focused
config/task/transport cases and 615 metric/lifecycle/rollback cases; the full
frontend unit suite passes 219, the build passes, and the updated task browser
fixture passes seven cases. A synthetic clean-shutdown encrypted handoff passes;
the v1.7.29 old-image experiment again confirms that parking task rows does not
fence late legacy results or the old metric-delete path. None of these tests is a
production backup or a safe downgrade procedure. At that checkpoint the
candidate had not been committed, CI-tested, published or deployed; the
production-canary record above supersedes that status.

Read-only checks show OC424 healthy on v1.7.29/94e1973 with root disk at 50%,
and MAC-WAN at 38% with both backup timers active. MegaBox and Riven each resolved
the reported hostname to an IPv6 address and made a successful TCP connection
to port 80; their running systemd Agents have automatic updates disabled. This
is reachability evidence, not a pinned-Agent acceptance or proof that future DNS
answers will remain unchanged. Other Agent auto-update flags are not all known.
The fresh 16:31 UTC encrypted online snapshot on MAC-WAN passes checksum,
authenticated verification and a network-free v1.7.29 isolated restore drill;
it does not capture a coordinated cross-store boundary or all in-memory samples.
Before release: establish and verify a non-destructive rollback and coordinated
backup boundary; choose an Agent publication/canary strategy that accounts for
unknown automatic updates; run exact-commit CI; and verify the actual OC424 and
node paths. The existing task 29, all node credentials and production services
remain unchanged.

**Load-rule backup metrics (deployed in v1.7.21 on 2026-11-02):** `backup_age`
and `backup_ok` evaluate end to end with explicit semantics. `backup_age` uses
seconds since the last successful backup; an agent-reported unknown (`-1`) is
excluded from both the ratio count and the baseline window instead of being
read as a zero-age healthy sample. `backup_ok` is inverted into a not-ok
signal for the engine's single `>=` comparison: threshold 1 alerts when the
last run failed or the status is unknown, and never alerts while it is
healthy. `temp` was removed from the metric options because no metric-store
series backs it. The production rule set contains no saved `temp`/`backup_*`
rules, so nothing dormant became active at cutover. This is distinct from the
operational MAC-WAN backup-health watcher. See
[`ALERT-RULE-PREVIEW.md`](./ALERT-RULE-PREVIEW.md).

**CI follow-up (2026-10-31):** exact merge CI41111906239 for the SLA span
clarification failed 7/8: the existing panel-history smoke asserted a
theme-cache sample immediately after startup. The scheduler starts that sample
on a goroutine; the test now waits a bounded 15 seconds for a real observed
pair and still fails if none appears. This addresses a plausible startup race,
not a proven root cause of that particular failure. Test-fix candidate
CI41113452947 and the subsequent exact merge CI41114880987 pass 8/8; the
original failure remains part of the record.

**CI follow-up (2026-11-05):** draft commit `6d765c9` CI42025602346 fails
7/8 in the synthetic backup-health recovery-notice smoke, timing out while
waiting for the second message. A race is possible in that fixture: its fake
receiver appends a message before the panel has persisted `Notified`, while
the fixture immediately terminates the process. The fixture now waits for
both the expected message and persisted `Notified` before each stop; this is
a plausible fix, not a proven root cause of that particular failure. Three
local synthetic runs passed both before and after the change; exact-commit CI
`42027687020` for `804208d` passed 8/8. This does not establish the original
failure's root cause. No production backup or alert state was touched.

**Theme follow-up, 2026-11-06:** OC424 now serves the existing online Emerald
Insights theme, based on official v2.0.6 plus the local compatibility patch in
[`deploy/theme-patches/emerald-insights-v2.0.6`](../deploy/theme-patches/emerald-insights-v2.0.6/README.md).
The upstream update fixes long-window drawing; the patch handles nullable
statistics and separates family/protocol/reference groups. Mocked Chromium
fixtures reproduce both old defects and exercise the repaired component and
the actual served bundle. Signed-in owner acceptance on HK04 and MegaBox is
still open. This is not the unmodified official bundle, and an online theme
update can overwrite the patch. The owner confirms the 14th node was added
intentionally; no node or token was changed here.

**MAC-WAN Agent handover, 2026-11-07:** the owner confirmed Hermes created the
system/root Agent as a replacement for the earlier `macos` user service. The
two services used the same binary, arguments, token file and node identity, but
the old user watchdog kept restarting the user instance. Both instances then
repeatedly displaced one another's WebSocket. The root service retains the
node and supports raw ICMP. At approximately 15:10 UTC the old user Agent and
its watchdog timer were stopped and disabled, with all their unit/script files
retained. A root system watchdog from
[`deploy/mac-wan-agent-watchdog`](../deploy/mac-wan-agent-watchdog/README.md)
was installed and enabled; it checks the system unit PID, rechecks after 30 s
and restarts that unit when its outgoing TLS socket is absent. Seven isolated
stub cases cover its decision logic; a real healthy timer firing succeeded, but
an actual disconnected-socket restart has not been induced. A seven-minute
observation showed one root process, no Agent restarts or further Agent-side
WebSocket disconnects, and fresh CPU/memory plus existing TCP and ICMP
latency/loss samples on OC424. This verifies those paths, not every remote-exec
or failure scenario. The watchdog's 443 check is only a socket heuristic and may
see unrelated Agent connections; panel-side freshness remains necessary.
Backup and health timers remain enabled, no node token or task was changed, and
the old watchdog is not removed. The duration of the connection conflict before
the handover is not established; it may have affected data or notifications and
is not retroactively repaired.

An additional 2026-11-07 check exposed that running the watchdog's diagnostic as
the unprivileged `macos` user could falsely report a healthy root-owned socket as
absent. The timer runs as root and its last firing succeeded; a root check also
passes. The local and installed script now refuse non-root invocation with an
inspection-error exit code, verified by an unprivileged subprocess and a root
host check. The atomic script replacement did not restart the Agent. The next
real timer firing at 23:44:47 Asia/Shanghai succeeds; a real disconnected-socket
restart remains unverified. At 15:56 UTC a further read-only panel-store check
found CPU, memory, ping latency and loss buckets for the same MAC node at 15:55
UTC; this confirms recent samples, not every remote-command or failure path.

**Agent release-policy recheck, 2026-11-07:** a read-only OC424 query finds 14
registered nodes: ten report v1.6.24, one v1.6.25, two v1.7.1 and one v1.7.29.
All persisted rollout targets are empty and none have a pending-clear marker;
reported version and an empty target do not reveal each process's periodic
auto-update flag. The MAC-WAN and OC424 system-unit command lines both include
`--disable-auto-update`, while the other processes' flags are unverified. In
the v1.7.29 source that flag suppresses periodic self-update but still permits
explicit panel-requested updates; automatic stable selection and targeted
updates exclude prereleases. A new stable release with Agent assets can therefore
affect unpinned nodes whose flags are unknown. No Agent update, target, or token
was changed by this check.

**Backup follow-up, 2026-11-06:** the MAC-WAN scheduled 03:30 UTC job failed
because its directory lock was held; a later manual service run succeeded at
05:40 UTC. Fresh manual pre/post theme-change backups are separate from the
scheduled set. On 2026-11-07 the timer actually triggered at 03:30:27 UTC,
completed successfully and produced a ciphertext whose sidecar verifies on
MAC-WAN. The cause of the earlier lock conflict remains unproven; the new run
is not a fresh restore drill and still has the in-memory RPO limitation below.
The then-non-root MAC-WAN Agent also logged `socket: permission denied` for
ICMP tasks 17/18/19 (observed at 11:01 UTC); this was a probe permission
failure, not an actual network-loss measurement. The later system/root handover
and its observed successful samples are recorded above.

**Current phase, updated 2026-11-05:** OC424 runs `v1.7.29/94e1973`.
`v1.7.28` was published but skipped for deployment; its CSV correctness
fixes were released in v1.7.29. OC424 checks pass 9/9 and fresh MAC-WAN
pre/post encrypted backups pass isolated restore drills. The owner still needs
to verify chart and history-bucket CSV downloads in the signed-in UI.
The next bounded releases and operational gates, in order:

1. **v1.7.24 SLA window coverage — done (2026-11-03).** `window_coverage`,
   the inferred cadence and the window-bucket count ship next to the pinned
   observed-span `coverage`; the UI shows both figures side by side in five
   locales. Deployed on OC424 with upgrade9/9 and identical client rows.
   Owner on-page acceptance of the window figure: confirmed 2026-11-04.
2. **v1.7.25 alert-rule test button — delivered (2026-11-03).** One bounded
   real test notification through the existing channel from the saved-rule
   editor; the preview stays read-only and the send is rate-limited globally
   (60 s) and never writes `last_notified`. Deployed on OC424 with
   upgrade9/9, identical client rows, fresh pre/post archives and two
   passing isolated restore drills. Exit closed 2026-11-04: the owner
   sent a test notification from the button and acknowledged the real
   receipt.
3. **v1.8.0 Probe Truth delivered; v1.8.1 is a formal verification
   close-out patch.** The uptime restart-count metric
   (`system.restarts`) shipped in v1.7.26 and the 50-node capacity/memory gate
   passed on 2026-11-04; see [`CAPACITY-50NODE.md`](./CAPACITY-50NODE.md).
   v1.8.0 itself is released and deployed as `v1.8.0/a92a242`; 14 clients and
   13 ping tasks remain, and all 14 Agents still report v1.7.30. The three
   remaining gates are:

   - 64-theme browser/hydration/mobile visual gate. Offline gates are
     64/64 manifests, preflight, install and reinstall; browser rendering,
     hydration diagnostics and mobile interaction remain pending.
   - Real external provider delivery. Channel registry, plugin load/unload and
     fake-channel dispatch are tested; every provider configured in the runtime
     at gate time still needs a real bounded test message and receipt.
   - LAN ICMP and `tc netem` loss. Loopback ICMP/TCP and HTTP TTFB calibration
     pass; LAN ICMP and 0/1/5/20% `tc netem` loss runs remain NOT RUN.

   v1.8.1 includes existing probe correctness fixes; v1.9.0 exec-result history and the other
   v1.9 slices are explicitly out of scope. Agent v1.8.0 staged rollout is
   **not started during v1.8.1**; it will be discussed only after all three
   gates close. Release tag/OC424 deploy mechanics and the defect-fix boundary
   remain TBD. The second off-host destination decision remains with the owner.
4. **Still open acceptance.** Real remote notification receipt (a genuine
   incident, not the acknowledged test send) and MegaBox provider-billing
   equivalence; the latter needs provider-side data and must never be
   claimed from panel numbers alone.
5. **v1.9.0 next (v1.7.27 and v1.7.29 CSV; v1.8.0 Probe Truth released 2026-11-08, deployed 2026-11-09).** Metric CSV export
   on the node chart page — a wide CSV (time plus one column per metric
   series with tags preserved) of the loaded window, built from the same
   `public:queryMetrics` data the charts draw, with a shared quoting util
   and five-locale button. Deployed on OC424 with upgrade9/9, identical
   client rows, fresh pre/post archives and two passing isolated restore
   drills. The v1.7.29 correction replaces misleading v1.7.28 reconstructed
   history rows with retained bucket data and is now deployed. Owner on-page
   acceptance of both Export CSV and Export history-bucket CSV remains open.
   See [`METRIC-CSV-EXPORT.md`](./METRIC-CSV-EXPORT.md). After the v1.8.1
   verification gates: bounded exec-result history, designed separately in
   [`EXEC-HISTORY-DESIGN.md`](./EXEC-HISTORY-DESIGN.md). Later slices include
   the cost-forecast column, roles UI, frontend budgets and an a11y pass.

**Hostname probe family selection (draft, 2026-11-05):** the reported
multi-node IPv6 hostname is rejected by the conservative mixed-family guard.
[`PING-DOMAIN-FAMILY-DESIGN.md`](./PING-DOMAIN-FAMILY-DESIGN.md) records a
proposed end-to-end, fail-closed solution and its Agent compatibility and
rollback gates. The transport-specific race and test plan is in
[`PING-FAMILY-TRANSPORT-DESIGN.md`](./PING-FAMILY-TRANSPORT-DESIGN.md).
An isolated Agent probe engine and WS handshake on both sides exist in draft
PR #170; the production dispatch hook is disabled. POST-only sessions and
production delivery/result integration remain open. The Agent has a
clock-independent, one-use permit/start fence. An isolated panel helper sends
a target-free permit request directly over the current WebSocket, orders its
sequence/write and consumes a bounded response under credential admission;
local race and simulated-peer loopback tests pass. The scheduler does not call
it. Agent probe execution and same-socket result writing now exist behind a
disabled-by-default dispatch hook, tested with injected DNS/dials; no
production probe consumes its ticket. An isolated per-session
registration now bounds pending dispatches and retains completed/canceled
tombstones until expiry; it rejects duplicate legs, expired leases and results
from retired sessions. **Local uncommitted follow-up (2026-11-05):** a draft
task guard, fail-closed mode column, synchronous metric writer and
result-admission helper have been added. A bounded WS-only handler now calls
the fenced admission helper under current-credential/current-socket admission;
the scheduler does not send pinned probes. A random persisted incarnation replaces the earlier
configuration hash, and the default-on assignment path renews it. Private
create/edit helpers and a real SQLite batch test exercise incarnation renewal;
public mutation/import paths remain gated and ID reuse is not yet fully solved
(see the later reservation checks and their limits below).
The helper currently holds the
node-specific session fence through its writer, but releases the global Agent
mutex before calling the store. Local race tests cover blocked injected writes,
other-node progress, replacement, disconnect, reset, rehandshake and expiry.
The synchronous writer now has a local, nonblocking real-store append: it
refuses contended locks and validates the whole small batch before changing
raw/hot data, without SQL or a background writer. A busy store, duplicate
timestamp, missing definitions, disabled metric or minute rollover can drop
an observation rather than block retirement; admitted points follow normal
hot-rollup durability. Real-store lock-contention, cancellation, atomicity,
rollup and concurrent-ingest tests pass locally with `-race`. Panel admission
time rather than dispatch time is used for the point. Isolated tests now also
exercise the real Agent against the panel's actual HTTP authentication and
WebSocket result handler, as detailed below. They do not prove scheduler
integration, hard real-time bounds, or the coverage impact of skipped samples;
integration remains gated. At this earlier checkpoint diagnostics were not yet visible;
see the later diagnostic snapshot follow-up. Nonempty pinned tasks are
deliberately skipped, and add/edit/import do not enable them. Configuration
import identifies ping tasks by the wrong field and must be repaired and
validated. No task, Agent or production configuration was changed, and
neither the designs nor the draft authorize a release.

**Agent execution checkpoint, local only (2026-11-07):** the candidate
Agent's real WebSocket entrypoint now enables the already session- and
one-use-ticket-fenced probe hook. A real production-entrypoint loopback test
performs the handshake, issues a permit, connects to a real IPv6 listener,
checks its IPv6 result and verifies replay does not dial again. The separate
fake-DNS multi-Agent tests use the same production enable function. The panel's
public mutation, production scheduler and family selector remain gated, and
POST-only nodes remain unsupported. No binary or release was pushed. The
focused Agent race suite passes; the unfiltered local suite still fails its
pre-existing public-network ICMP/TCP/HTTP tests because targets/DNS are not
reachable from this machine, so that run is not a full pass. The production
entrypoint loopback passes ten race-enabled repetitions. The isolated
browser-to-three-Agent flow also passes with the existing Playwright 1.58.0
environment after the first attempt failed because system Python lacks that
module; it remains a test-only scheduler and does not prove live reachability.
Another production-entrypoint test now exercises an actual local DNS exchange:
the Agent requests AAAA from its configured resolver and reaches an IPv6 TCP
listener without injected lookup or dial hooks. Five race-enabled repetitions
pass. This remains an isolated loopback test, not an OC424/node acceptance.
Agent rollout,
rollback, restore, configuration import and exact-commit CI remain open.

**Pinned reference timeout fix, local only (2026-11-07):** the independently
resolved reference probe reused its DNS timeout as the parent context for the
actual connection. When an attempted TCP/HTTP connection consumed that deadline,
it could be misclassified as canceled and omitted rather than recorded as a
failed probe. DNS and probe deadlines are now separate while the session context
still cancels both. Race-enabled tests distinguish a timed-out dial from session
cancellation for both protocols, across eight repetitions. This fixes the local
draft only; pinned tasks remain disabled in production.
After this change the executable three-Agent/real-DNS isolated test and the
mounted browser-to-three-Agent test each pass once under `-race`; both use an
injected test scheduler and do not establish production readiness or a real
node acceptance.

**Executable-Agent and restore-snapshot follow-up, local only (2026-11-07):**
an opt-in race-enabled test builds and starts three actual Agent executables in
a loopback-only Linux network namespace. Real local DNS sockets answer AAAA for
two Agents and no AAAA for the third; the first two write IPv6 TCP success
samples and the third records a DNS diagnostic without a false loss sample.
The panel scheduler remains test-injected, and the fixture is not live-node
acceptance. The startup-restore draft now journals the snapshot's SHA-256 as
well as the pending archive's digest; replacing a snapshot with another valid
ZIP is refused on restart. It also refuses a symlinked snapshot directory or
special/symlink entries instead of silently following them. The executable test
passes once under `-race`; the four-package restore/server suite passes 181
tests under `-race`. These are scratch tests, not proof against external writers,
all filesystem failures, or a production backup/rollback procedure. No
production process, task, Agent or backup was changed.

**Configuration review draft, local only (2026-11-07):** an isolated importer
preview now has a process-keyed review token over the parsed document and the
configuration state consulted by the transaction, including node credentials,
reported address-family presence, task incarnations and deletion reservations.
Apply rechecks it in its own transaction and refuses a missing, malformed or
stale token before writes; ordinary telemetry and address-value changes that do
not change family presence do not invalidate it. The key is never returned and
is regenerated on process restart. The focused config/import race selection
passes 72 tests. This is not public RPC/UI wiring: schema 1/2 handling,
sensitive-operation/audit, error semantics, concurrent external writers, browser
integration and release/rollback gates remain open. Preview/import stay gated.
The unregistered fixture subsequently gained actor audit messages and an
explicit committed-but-schedule-pending response. Authenticated HTTP and
WebSocket tests now exercise stale refusal and a newly reviewed import with an
integer above 2^53; the focused race selection passes 75 tests. A later local
change writes the fixture's audit row in the same transaction as its imported
configuration; SQLite fault injection confirms audit failure rolls back the
configuration, and a post-commit reload failure retains both configuration and
audit. Fourteen focused review/transport cases passed under `-race` at that
checkpoint. See the follow-up below for the later sensitive-operation gate;
cross-process isolation and a registered/public import endpoint remain open.

**Configuration import follow-up, local only (2026-11-07):** the public import
remains disabled, but `admin:importConfig` is now marked sensitive even while
disabled. A test temporarily binds the draft handler to that method and checks
real authenticated HTTP and WebSocket 2FA refusal/success and atomic actor
audit; it restores the disabled handler afterwards. The frontend draft requires
the server's 64-character review token, sends it and an optional one-time code,
and discards the review after any import attempt. Schema 1/2 documents containing
tasks or maintenance windows are explicitly refused: their panel-local numeric
IDs cannot establish cross-panel identity. Legacy documents without those
entities can use the reviewed path; the token binds the original schema. A
768 KiB document and 2048-record limit bound this draft, and a concurrent
SQLite test permits only one commit for two identical reviewed updates. Focused
race tests and 15 mounted browser cases pass, including display of implicit
default-on task assignments caused by newly imported nodes. This does not prove MySQL or
PostgreSQL isolation, comprehensive external-writer exclusion, complete public
wiring, or a safe pinned-task downgrade, and does not authorize a release. A
separate SQLite WAL fixture using two connections and the production-style
`_txlock=immediate` shows a preceding external update invalidates the review
and a writer arriving during the import waits for its commit; this does not
prevent a later external write or test other SQLite connection settings. The
main configuration DB currently supports SQLite only; MySQL/PostgreSQL apply
to the separate metric store. The 768 KiB
document limit also remains subject to the existing 1 MiB total RPC frame limit.

**Startup restore follow-up, local only (2026-11-07 09:35 UTC):** startup now
revalidates a pending ZIP, including manually placed archives, before touching
the data directory. A successful, uniquely named and synced pre-restore
snapshot is journaled with the pending archive's SHA-256 before cleanup starts.
Failures abort startup before opening the database. A restart resumes only if
the journal's archive hash and original snapshot still match; a missing archive,
changed archive, corrupt journal or missing snapshot requires operator
inspection. Extraction checks file sync/close and syncs created directories.
Account commands and new restore staging also refuse an outstanding journal.
Scratch tests simulate a destructive-phase interruption, repeated restarts,
changed archive, missing snapshot, archive-free journal and name collision;
the seven-package race suite passes 191 tests. This is not proof of atomic
cross-database backup, immunity to external writers or every power-loss and
filesystem failure. Do not start an older binary over an unresolved journal.
Subsequent focused tests pass 179 cases across four race-enabled packages,
including two fresh-process starts with an invalid archive; earlier inert
pending archives and journal staging evidence survive a later restore. The
directory sync has a Windows-specific implementation that cross-compiles, but
its actual filesystem behavior still needs the Windows CI run.
The current draft remains uncommitted and undeployed. A fresh read-only check
at 09:32 UTC found OC424 on v1.7.29/94e1973, healthy, zero restarts and 50%
root usage. No production restart, backup, task, node or token was changed.

**IPv6 local follow-up (2026-11-07 02:45 UTC):** an opt-in Chromium test
now exercises the mounted family selector through a real administrator session,
REST/RPC bridge, scratch database, isolated scheduler and three real Agent
test processes. Fake DNS and an IPv6 loopback prove a successful IPv6 sample,
a no-family diagnostic without false packet loss, and a disabled Agent that
receives no probe; pinned work does not enter the legacy queue. Anonymous
requests and the real production pinned-add method are rejected. The panel
smoke CI job now invokes this test. This is an isolated test-only path, not
production wiring or live acceptance.

A shutdown audit found that a failed report-batcher flush replied with an
error while the caller waited indefinitely for the retained worker to exit.
The local fix returns the error, retains the accepted queue for retry, and
allows concurrent stop callers to observe completion. Node metric cleanup now
also drains queued records and fences local store transitions. Focused race
tests pass, but cleanup still has no durable node-deletion journal or safe
ID-recreation policy, and the server can continue shutting down after a flush
error or deadline. These changes do not supply a production rollback procedure.
SQLite metric split/merge now rejects physical file aliases as a preliminary
check; it cannot fence other writers or path replacement. Production pinned
creation, scheduling, Agent hook, and import/preview remain gated.

**Backup and shutdown audit, local only (2026-11-07):** a synthetic current
minute reproduced a missing sample in the old panel download snapshot: SQLite
contained no rollup yet even though the live store had accepted the point. A
draft panel-download path now fences process-local report admissions, drains
the accepted queue and snapshots SQLite plus the in-memory summaries; it leaves
the live store unchanged. A subsequent scratch restore exposed a second defect:
sealing an unfinished 5-minute/hour parent in the copy let continued writes
replace its pre-snapshot contribution. The local draft now seals only parents
past their grace window. Open parents remain reconstructible from durable
children; a missing/mismatched child or a pre-existing durable parent/ancestor
fails the snapshot and removes its incomplete destination. Reopening the copy,
continuing the same window, closing it and checking SQLite integrity now pass.
Higher-tier reads include disjoint pending finer parents and reduce rate samples
by source bucket; filtering, aligned windows, percentiles, budgets and mixed
pending levels have local regressions. A separately reproduced imported-parent
collision formerly caused double counting and could overwrite the imported row:
the draft now rejects a conflicting durable ancestor on reads and sealing rather
than guessing which history is complete. These process-local checks do not fence
another Store instance or external SQL, and their cost on the real fleet has not
been measured. Current-minute hot samples have not yet propagated to coarse
parents, so the new reads do not establish real-time completeness. The copy retains
minute aggregates, not the ten-minute exact raw window. Other synthetic tests
cover WAL, queued reports, hot replacements, concurrent batch writes, failure
cleanup and retry. This is not a cross-database or cross-process snapshot, and raw
SQL or a separate writer can bypass its fence. The main DB and filesystem are
still captured independently. Importantly, **MAC-WAN's
installed encrypted producer uses its own external SQLite snapshot, not this
panel-download path**; its database snapshots and prior successful restore
drills do not include or prove capture of queued/hot memory. Its exact RPO and a
coordinated off-host handoff still need design and a real isolated recovery
test. No production backup script or timer was changed.

Store close now leaves hot buckets and the active handle available when a
pre-commit flush fails, and a synthetic lost-acknowledgement test confirms that
an uncertain commit blocks a blind retry. Reload first persists the active hot
buckets and refuses replacement on failure. These are process-local safeguards,
not crash durability: shutdown still proceeds after cleanup errors, and
reopening/replacing a store can reconstruct pending parents only from retained
children. A separate scratch test now reproduces a pre-existing late-upsert
defect across a clean reopen: a persisted 10 followed by a new 20 and its
replacement 30 in the same minute ends as 30, not 40, because the new raw
cache lacks the old point. A subsequent baseline-aware reconstruction now passes
that scratch regression and preserves label transitions and sketches; ambiguous
older identities fail closed rather than being guessed. `PersistHotRollups`
before same-store reload is still not a downgrade protocol. None of this supplies
a production rollback coordinator.

**IPv6 local reliability follow-up (2026-11-07):** filtered series deletion
now preserves other tag sets' cached baselines and partial-deletion markers;
the raw and hot paths share one timestamp. A write that reaches memory but then
fails maintenance returns an explicit accepted outcome so the report/ping
batchers do not replay it; SQLite fault injection covers pre-commit failure,
coarse failure and replay. An ambiguous queued report is retained and blocks
later reports of that node, while other nodes can progress; pings use the same
rule per series. This is bounded memory with error logging, not durable
quarantine or an operator reconciliation workflow. A persistent ambiguous
record can still fill the queue, and a crash can lose accepted in-memory work.
Imports reject overlap with pending coarse windows; coarse-only history and
retention invalidate unsafe minute assumptions. `DeleteBefore` also removes
fully deleted pending parents. Space reclaim, schema migration and restructure
now coordinate with process-local snapshots; a missing or non-SQLite active
metric store no longer silently falls back to a default-path file in UI backups.
Coarse reconstruction now refuses to replace a differing durable parent,
since the finer source may have been expired; imported bucket boundaries and
first/last times are validated. Retention cleanup removes fully expired pending
hot/coarse buckets, but refuses cleanup if a still-retained coarser tier needs
an unflushed source or startup recovery has not run. Tests cover recovery and
subsequent cleanup, but no automatic reconciliation of repeated failures is
claimed. Explicit history discard clears pending parents.
Lost-COMMIT fault injection covers deletion, disabling, retention, discard,
representative/rollup imports and coarse rebuild: an uncertain outcome fences
subsequent supported mutations, flushes, snapshots, discard and reclaim for
that Store instance. Imports and rebuild now take the exclusive retention
fence, and rebuild refuses unrecovered coarse state. This is a fail-closed in-memory alarm, not a
durable transaction journal or reconciliation procedure; process restart,
external SQL/writers, other Store instances, cross-database atomicity and
filesystem replacement remain outside these fences. Legacy migration can now
stop on a conflicting parent rather than overwrite it and needs an operator
recovery workflow. Per-metric cleanup of a disabled definition now removes its
pending raw, hot and coarse data too, preventing re-enablement from reviving
the cleaned history. A conservative store-migration import now rejects a
differing existing aggregate or type/unit/retention instead of overwriting
destination data; identical rows are retained without rewriting. Batches and
metrics still commit separately; status identifies conflict or uncertain
commit without including DSNs. External writers or another Store instance are
not fenced. Conflict resolution and cross-tier reconciliation remain open.
SQLite tests exercise the destination conflict; MySQL/PostgreSQL integration
coverage is added but still requires configured servers or exact-candidate CI.
SQLite-to-SQLite migration now checks filesystem identities before opening the
source as well as after opening it; this avoids opening a hard link under a
second WAL name in the normal stable-path case. A missing, in-memory or
unsupported source URI fails closed rather than creating an empty source.
The checks are not an exclusive cross-process or path-replacement lease, and
the pre-existing source opener can still auto-migrate a distinct source.
The SQLite snapshot draft also refuses to create an archive while startup
coarse recovery remains pending; a fixture verifies cleanup and successful
retry after recovery. The installed MAC-WAN producer still does not use it.
The earlier local nine-package panel race run for this follow-up passed 1,307 tests;
the Agent pinned/local test selection passed 119, and the opt-in isolated
Chromium-to-three-Agent test passed once. These do not enable the feature or
substitute for exact-commit CI and live acceptance. On 2026-11-07 04:11 UTC,
OC424 was healthy and its root disk was 50%. The executor's reproducible Go
build cache was cleared after the tests, reducing its root usage from 81% to
72%; user data and `.runtest` were not removed.
After that run, imported rollup metric/entity names were additionally checked
for valid UTF-8. The latest nine-package race run after the local
durable-ancestor/rate fixes passed 1,320 tests. On 2026-11-07 04:48 UTC a
read-only check found OC424 still healthy on v1.7.29, its site returned HTTP 200,
and its root disk was 50%. Final candidate CI, complete release checks and
live acceptance remain necessary. A subsequent isolated SQL run used the
digest-pinned PostgreSQL 17 and MySQL 8.4 CI images with generated test
credentials and loopback-only disposable containers. It passed the race-enabled
coarse-recovery and aligned-read selection, including a new imported-ancestor
read/seal conflict case on both backends with one database connection. The
containers were removed; this is not exact-commit CI or a production test.
These changes are local only. A five-package race run after
the first retention changes passed 570 tests; focused race tests after the
discard/reclaim changes passed 21. The isolated browser-to-three-real-Agent
fixture and 119 Agent pinned race tests passed earlier. A fresh full run and
exact candidate CI remain necessary; no deployment or live task acceptance is
implied.

**IPv6 local correctness follow-up (2026-11-07):** supported queries and
retention mutations on the same metric Store now serialize across deletion,
flush and uncertain COMMIT boundaries. A nine-package race run after the lock
and SQL predicate regressions passed 1,390 tests. This is process-local: other Store instances and
external SQL bypass it; long reads can delay writes and a nonblocking pinned
append may skip an observation. SQLite recovery and transfer tests also cover
pre-1970 aligned buckets; the signed indexed parent-range query passed against
SQLite and disposable MySQL 8.4/PostgreSQL 17 instances. A separate SQL
predicate test covers the int64 endpoints and deliberately unaligned parent
rows; this does **not** establish extreme timestamps as supported by the rest
of the rollup engine. A synthetic SQLite startup probe with 27 metrics, 594
series, 498,960 rows and a 345 MiB file measured 168 ms to open and 153/164 ms
for warm scans on this executor. It is not a production or concurrent-load SLO.

Agent probes previously inherited their original session deadline even if the
session renewed, causing premature cancellation. The local session timer now
tracks timely renewals, expires without traffic and rechecks a queued callback
against the current session. An additional fault test reproduced a blocked
handshake write holding the session lock past expiry; control writes now wait
only within a bounded deadline and a busy writer can complete first. Unit race
tests cover expiry, replacement, continuation across renewal and the blocked
write. After these changes the isolated real Agent/actual HTTP and WebSocket
transport test and the Chromium-to-three-Agent fixture passed. These fixtures
use fake DNS and opt-in hooks: they do not enable or validate production pinned
scheduling. The Agent full race run failed external ICMP/TCP/HTTP tests whose
public targets or IPv6 paths were unavailable; running the remaining suite
passed 449 tests across 17 packages. These failures are not proof of a product
regression or live target reachability. Read-only OC424 inspection found
v1.7.29/94e1973 healthy and the root filesystem at 50%; no production switch,
node change or backup change was made. The MAC-WAN producer, cross-database
backup boundary, downgrade, import, final candidate CI and live acceptance
gates remain open.

Two further scratch-only rollback experiments passed. After a clean close,
both sides of a metric split can reconstruct an unfinished coarse parent from
their persisted minute children. A separate 48-hour downtime simulation also
reproduced that the experimental merge revives archived rows which normal
retention would have expired. It has neither a cutover time nor a binding to
the effective tier retention policy. These experiments do not authorize a
production split, merge, rollback or retention change.

**Shutdown follow-up, local only (2026-11-07):** stopping the batch worker used
to leave a gap in which a late report could fall back to synchronous ingest.
A terminal shutdown fence now waits for admitted report writes, refuses later
ordinary and pinned writes, and drains the accepted queue before store close.
The scheduler now tracks its callbacks, including replaced/removed jobs, and
waits before plugin and store teardown; it does not track child goroutines
spawned by those callbacks. Critical cleanup failures stop dependent cleanup,
but the caller still exits and an external supervisor may kill the process;
this is not durable recovery or a zero-loss guarantee. An isolated subprocess
with file-backed stores verifies scheduled and queued observations after a
successful close and reopen. Race tests cover a blocked drain, retained queue,
concurrent scheduler registration and timed-out/retried scheduler drain. The
same stopped synthetic databases were also encrypted with a throwaway age
identity, verified and extracted with the real producer, then reopened to
check the synthetic task and the queued and shutdown-time samples. The test uses a detached scratch
copy whose metric DSN is rewritten for the producer; it neither boots the
restored panel nor establishes a live cross-database or filesystem cutover.
Its opt-in CI step is separate from the existing offline-panel restore drill.
The first extraction fixture used a non-private parent and was correctly
refused; after moving the destination under the private directory, three
race-enabled repetitions pass. The old deployed binary lacks these changes, so
its shutdown cannot inherit this
proof. An offline coordinated backup, external writers, complete child-worker
drain, graceful termination budget and restart-on-failure policy remain open.
At 06:07 UTC a read-only OC424 inspection found Docker's default stop grace and
`unless-stopped` restart policy. Those settings do not establish that the old
panel has time to drain before a forced kill or that a failed shutdown stays
stopped. They were not changed.
One scratch plugin experiment confirmed that `unload()` can start an
unawaited `server.call`: the JavaScript runtime closes while that independent
admin RPC is still running. The draft now tracks such calls at manager level,
cancels and waits for them after unload, and refuses further calls during
shutdown; a blocked synthetic RPC proves timeout and retry behavior. Plugin
filesystem work, callbacks that spawn other goroutines, notification workers,
active WebSockets and a durable failure path remain outside that narrow fence.
These changes do not establish coordinated shutdown.

A fresh local 12-package race run passed 1,494 tests after correcting a
time-dependent snapshot fixture: its supposedly closed hourly bucket was still
inside the ten-minute grace immediately after the hour. Five repetitions of the
closed/open-parent snapshot cases pass. The isolated real-Agent transport and
browser-to-three-Agent tests also pass after the latest Agent timer change;
they still use test hooks and fake DNS, not production scheduling or real
reachability. Plugin and scheduler shutdown additions were tested separately;
the entire current candidate has not passed exact-commit CI.

**Shutdown follow-up, local only (2026-11-07 06:50 UTC):** the preceding
paragraphs describe earlier drafts, not the deployed binary. The current
draft now joins ping scheduler child and pinned-dispatch workers, bounds
plugin unload with the cleanup deadline and joins unawaited `server.call`
and `server.getConfig` operations. It closes and joins hijacked WebSocket
handlers and close hooks, joins detached terminal forwarders, and fences
configuration reloads, notification timers/sends, asynchronous metric deletion
and the remote-upload sweeper before their dependencies close. A failed async
metric deletion is retained as a shutdown error, not silently treated as a
successful backup boundary. Background notification sends share the provider
lifetime lock with provider replacement. These are process-local, terminal
fences; timeout still causes process exit and external supervisors can kill or
restart it. They do not cover every plugin-host task, external writer, backup
restore's direct `os.Exit`, guided migration, or all filesystem effects.

In an isolated subprocess, a blocked WebSocket handler wrote a synthetic ping
sample as it closed and a canceled notifier wrote the synthetic task name;
both were present after the queued and scheduled samples had drained, the
file-backed databases had closed, and the real encrypted backup producer had
verified, extracted and reopened them. Three race-enabled repetitions pass.
This fixture rewrites the scratch metric DSN and does not boot the extracted
panel or establish a production dual-store/filesystem boundary. The first
attempt was rejected by the real WebSocket origin check because the fixture
omitted `Origin`; the same-origin fixture passes. Shutdown, WebSocket,
notification, plugin, upload and metric-cleanup timeout/race tests also pass
locally; exact-commit CI and the old arm64 executable remain unverified.
At 06:44 UTC a read-only OC424 recheck showed `v1.7.29/94e1973`, healthy,
zero restarts, default Docker stop timeout, `unless-stopped` and 50% root
usage. No production restart, task, Agent, token, timer or backup was changed.
The pinned feature gates and the old-image rollback/late-result and retention
blockers remain open; this checkpoint is not a release or live acceptance.

**Restore and shutdown checkpoint, local only (2026-11-07):** a failed first
shutdown is now terminal for that `App`: concurrent and subsequent callers
receive the same error and cannot rerun partial cleanup as a successful restore
handoff. Fatal listener errors use the same HTTP-draining path. A scratch
subprocess covers failure, concurrent retry, lease retention and fresh startup
without applying the inert `.backup-pending-*.zip`; another verifies fatal
shutdown waits for an active HTTP request. A guide HTTP/worker drain failure
likewise prevents a later process shutdown from publishing a staged restore.
Uploaded archive data and its staged directory entry are synced; publication
uses an exclusive hard link and directory sync. A
failure after linking is **uncertain**: `backup.zip` may be eligible at the next
startup even though shutdown reports an error. The data lease only coordinates
cooperating new binaries; it cannot fence the deployed old binary, external
SQL, or escaped plugin subprocesses.

The upload validator now rejects unsafe, duplicate or conflicting ZIP paths,
reserved restore paths and database journals, verifies each entry's size and
checksum, and runs SQLite `quick_check` on both the main and optional metric
DB. These checks do **not** establish logical cross-database consistency,
atomic asset capture, or successful extraction under disk/filesystem failure.
The later startup-restore follow-up above also validates a manually installed
`backup.zip`. Account CLI commands acquire
the cooperating data lease, require an existing DB and refuse a pending restore
instead of starting a second writer or accidentally triggering a restore.
Neither these nor migration-worker and panel download changes are deployed.
Failed and uncertain handoffs and retained staging archives need operator
inspection as described in [`ENCRYPTED-BACKUPS.md`](./ENCRYPTED-BACKUPS.md);
no automatic cleanup was run. Local server, backup and command race tests pass;
this is not full exact-commit CI or a real two-database restore drill.

**IPv6 local checkpoint (2026-11-06 21:47 UTC):** no release, Agent update,
live task, backup or OC424 deployment has been made. Public pinned creation,
the production scheduler and Agent probe hook, the family selector, and both
configuration import RPCs remain disabled. A completed rollout and real-node
acceptance are still open.

Report/store transitions now take an admission fence before flushing queued
reports and acquiring the exclusive store lease; synchronous pinned, queued
legacy ping and ordinary report writes fail fast while the fence is held.
Focused race tests cover the drain/fence and unrelated history, but an Agent
WebSocket notification rejected during that interval may lose a sample. This
has not been assessed under production load and the fence does not protect old
binaries, another data directory, external SQL writers or deleted lock files.

Ordinary configuration exports now allow only reviewed scalar setting keys;
database DSNs, migration fingerprints, discovery credentials and unknown
settings require sensitive export. Notification provider credentials remain
excluded even from that export; arbitrary free-text metadata is not guaranteed
secret-free. The disabled importer refuses changes to metric-store or
unreviewed settings. Migration status and errors no longer include DSNs or
driver error text, and writing its fingerprint uses the quiet transactional
settings path. A synthetic failing connection verifies the status behavior.
This is not a security audit of every metric connection/recovery error path.

The local transactional importer is still disabled. Its task-creation fence
now also covers legacy creation, preventing a new task from appearing between
guard enumeration and the import transaction. The standalone document parser
rejects unknown or differently cased envelope fields, duplicate keys, trailing
data and excessive nesting. The draft import binder now uses original RPC
bytes, accepts either an object or untouched `document_json` text, and rejects
ambiguous request/parameter keys. Browser imports send original text; new
separately registered text exports avoid JavaScript integer rounding and the
credential-bearing variant has the same step-up/audit boundary. Synthetic
real HTTP/WS calls to a test-only transaction validate the path, including a
traffic limit above 2^53. A mounted browser fixture confirms untouched text is
handed to the callback; it is not a full live browser-to-RPC import. Schema 1/2
compatibility, settings validation, audit/step-up for import, preview/apply
staleness and real UI/RPC integration remain open; three old success tests
remain skipped. Exports are configuration documents, not database backups.

The config, metricstore and RPC suites passed 606 tests before this checkpoint;
subsequent focused race runs passed 56 tests across config, guards, tasks and
RPC, plus the migration status/source cases. These are local tests, not the
exact release commit or live acceptance. In this follow-up 47 focused race
tests, 467 config/RPC tests, 218 frontend unit tests, lint, TypeScript and 11
mounted config browser cases pass; the isolated old-image delete experiment
also passes. At 21:47 UTC OC424 was healthy on
v1.7.29/94e1973 with root disk at 50%; at 21:47 UTC MAC-WAN was reachable,
root disk 38%, and timers active. The next automatic backup trigger has not
yet established recovery from its previous lock-conflict failure.

The old-image rollback experiment proves the old result handler accepts
late legacy samples for a parked task and its delete path erases persisted
parked metrics before the main-DB trigger rejects deletion; the isolated
archive survives. A previously accepted queued result then repopulated the
old store at shutdown, and scratch merge excluded it. This does not establish
that a live downgrade is safe; parking is not a safe downgrade.
An offline two-store quarantine/merge strategy, failure recovery, isolation
from old/other writers, the actual arm64 image, and its operator procedure
must be designed and tested before treating any downgrade as safe. Stable
release publication may auto-update unpinned Agents, including at startup
before a panel target is received; verify a safe canary plan rather than
assuming a target-version setting controls all nodes.

**IPv6 local deletion/recovery follow-up (2026-11-06 16:00 UTC):** deleting a
task now commits a durable ID reservation with the main-database delete before
touching its separate metric store. Cleanup flushes accepted queued results,
removes each metric and marks the reservation complete; it is retried after
failure or restart. A completed reservation remains, and the supported auto-ID
creation paths reject it. A bounded, oldest-attempt-first retry prevents a
damaged/live ID from starving other pending reservations and logs each failure.
SQLite fixture tests reopen two file-backed stores after interruptions at main
commit, queue flush, partial/full metric deletion and completion; they verify
retry, retained unrelated history and reservation. These are orderly reopen
simulations, not a power-loss or multi-process proof. A real authenticated
legacy result/queue/deletion lifecycle and local race tests also pass.

The reservation is not yet a complete identity boundary: direct SQL, a future
importer, legacy results arriving during a downgrade, and multiple panel
processes do not share its in-process task guards. In particular, changing or
migrating the metric store after a completed cleanup can reintroduce historical
rows for a deleted ID. A future import or migration path must preserve the
reservation and prevent stale metrics from being attached to another task.
Do not enable or deploy this draft until that boundary, configuration import,
rollback, and the other release gates are resolved. No production mutation or
Agent update was made in this follow-up.

The local Agent resolver also now honors that node's configured custom DNS
while still requesting only the pinned address family. A loopback DNS server
test confirms an IPv6 request reaches that configured resolver as AAAA-only;
this does not establish how a live node's DNS is configured or whether it can
reach the target. The panel and Agent reject bracketed hostnames and bracketed
IPv4 HTTP authorities rather than accepting targets the actual dialer cannot
probe. Focused race tests pass locally; all deployment gates remain unchanged.
Pinned create/edit drafts now reject duplicate or malformed assignments, check
assigned nodes exist in the transaction, and acquire the relevant node guards
before task guards. The scheduler defensively deduplicates previously stored
rows. An assignment query initially exposed GORM's `StringArray` SQL-valuer
binding as one parameter rather than an `IN` list; it is converted explicitly to
`[]string` here and in the draft importer, with actual changed-assignment tests.
Node-deletion races, default-on creation coordination and cross-process paths
still require integration review; this is not yet a complete public mutation
path.

**IPv6 local follow-up (2026-11-06 17:00 UTC):** legacy monitoring import
now cleans reserved task history before writing, excludes reserved task IDs
from its source stream, and rechecks before completion. An interrupted SQLite
fixture retries without restoring deleted history and preserves other tasks.
Store-to-store migration also holds a process-local identity fence against
task deletion and drains already accepted report batches before it takes the
exclusive store lease. A race test covers the fence and queued-ping drain;
external writers and other panel processes are not fenced.

Configuration import has a **local, disabled draft**, not a production-ready
replacement. Startup backfills stable per-task/window configuration identities;
schema-3 exports carry them separately from local numeric IDs. The isolated
transactional importer generates destination IDs, refuses a deleted task
identity, preserves pinned measurement semantics, and creates new nodes and
default-on assignments in the same transaction. A SQLite fixture covers numeric
ID collisions, preview rollback, failure atomicity and repeated import. Schema
1/2 imports remain refused by this draft pending an explicit compatibility
policy; public import/preview are still disabled and the existing success tests
still skipped. This adds an as-yet unreleased schema migration and needs further
security, credential, compatibility, event, cross-process and recovery review.
The experimental rollback snapshot was updated to include the new identity,
but is still **not** a safe production downgrade procedure. No production
configuration, token, binary or Agent was changed.

The full local panel suite passes 2,414 tests across 105 packages with only the
existing three external GeoIP tests excluded; an initial run found that the
real-Agent helper fixture had not created the new reservation table, and it was
corrected before the passing rerun. The Agent suite passes 441 tests with the
three CI-excluded live ICMP/TCP/HTTP suites omitted. An earlier Agent command
used the wrong exclusions and failed those external-network cases (20 failures);
that is not evidence of a regression or a successful real-network probe. These
are local results, not exact-commit CI or production acceptance.

At 16:55 UTC OC424 was healthy on v1.7.29/94e1973 and root disk was 51%;
MAC-WAN was reachable at 16:59 UTC, root disk 38%, daily timer active. The
next scheduled backup has not yet run; the prior lock-conflict failure is not
closed. These read-only checks are not a fresh backup or an acceptance test.

**IPv6 local follow-up (2026-11-06 16:30 UTC):** the draft now reconciles
reserved task IDs before activating an existing metric store and filters them
from store-to-store rollup migration. Reconciliation also runs on the old
active store before replacement so its close cannot flush a deleted hot bucket
back into an aliased target. A migration refreshes reservations before it
reports success; a deletion concurrent with that final check still relies on
the durable pending-cleanup retry. SQLite tests cover stale source and target
stores, a live-ID conflict, same-store reload, retained unrelated history and
the ping writer's replacement fence. This does not fence a second panel process,
direct SQL, old binaries or external writers. New-node creation and its
default-on assignments now share a roster/node/task lock order and one main-DB
transaction, including pinned generation renewal; failed assignments roll the
node back rather than leaving a partially configured credential. Focused race
tests cover the roster, deletion ordering and transaction rollback. These are
local draft changes, not a deployed fix; import, multi-process exclusion,
rollback and production transport gates remain open.

**IPv6 follow-up (2026-11-06):** the owner confirms MegaBox or Riven alone
can be selected, while two or three nodes reproduce the guard. The guard is
based on each node's reported addresses, not on the task's name or a live DNS
lookup: one node cannot mix paths *between nodes*, but a dual-stack or
unknown-address node in a multi-node hostname task makes the address family
ambiguous. The executor's lookup returned AAAA and no A for the target at
11:07 UTC, which is only a transient resolver observation and not proof of
the nodes' answers or future behavior.

**IPv6 local checkpoint (2026-11-06 13:43 UTC):** the HTTP probe now binds
the Go HTTP transport's detached DNS/dial context to the probe deadline and
session cancellation. Before the change, a request timeout could return while
a dial remained active and its outcome could lose the fact that an IPv6 dial
had begun. It records the selected family at dial start, treats a timeout after
that point as a probe failure, and does not turn a canceled session into loss.
Isolated DNS-timeout, slow/fast dial-timeout and retirement cases plus the
Agent `TestPinned*` race suite pass. Context cancellation is cooperative and
does not constitute a real-network or production verification. The isolated
real-handler/real-Agent slow-scheduler case also passes after this change,
with fake DNS and a test-only dispatch hook. The feature remains disabled
and all release blockers below remain open.

**IPv6 local checkpoint (2026-11-06 13:28 UTC):** the dispatch ledger now
refuses an overlapping attempt for the same task and session until every
expected leg is consumed or the bounded dispatch expires. Previously each
new dispatch superseded the older result; a slow probe could therefore be
starved indefinitely by a shorter interval. An isolated real-scheduler,
real-Agent/HTTP-handler test holds an IPv6 dial across two one-second schedule
ticks, verifies no second probe is sent, then admits exactly one IPv6 result.
It uses a fake resolver and an explicitly enabled test-only Agent hook; this
does not establish production coverage. Lost results can still hold a slot
until expiry and scheduler overload can skip samples. A Go DNS `IsNotFound`
now remains the generic DNS-failure diagnostic because it cannot reliably
distinguish NXDOMAIN from a missing A/AAAA answer; neither is recorded as
packet loss.

Configuration import **and preview are temporarily disabled for all
documents** in this local draft, including legacy configurations. Export
remains available; a schema-2 pinned export is inspection-only and cannot be
restored by the importer. The UI directs recovery to encrypted database
backups. This is an unresolved user-visible regression, not a deployable
replacement for a transactionally correct importer. Three existing successful
import tests are explicitly skipped; identity mapping, cross-entity atomicity,
configuration change notifications and safe coexistence with pinned tasks
remain to be implemented and tested. No such restriction is running on
OC424.

After the slow-probe change, the focused Agent and panel `-race` suites,
the isolated real-handler/scheduler Agent fixture, and 16 mounted browser
cases pass. The provider browser fixture also verifies that an older successful
schedule response cannot hide a newer pending state. The first full frontend
run failed two fixture contracts (a new
module alias and the added localized keys); those tests were corrected and
the 218-test Node suite passes. The first current-source frontend build
failed because a new TypeScript constructor used parameter properties while
`erasableSyntaxOnly` is enabled; it was corrected. Final-source lint and build
pass (with existing Rollup annotation and large-chunk warnings). The initial wide panel race run
also exposed tests assuming overlapping dispatches and a test waiting forever
after an early delivery failure; the fixtures were corrected and the focused
panel pinned race suite passes. These results are local, not exact-commit CI.

At 13:22 UTC a read-only OC424 check showed v1.7.29/94e1973, a healthy panel
and root disk at 50%; MAC-WAN was reachable at 13:24 UTC with root disk at
37%. This is not a fresh backup or restore proof. No production mutation,
Agent update, release or deployment has been made. The public add/edit path,
production scheduler, Agent probe hook and family selector remain gated.
Safe downgrade over the current two databases, transactional import, bounded
Agent rollout, exact-candidate CI and fresh MAC-WAN pre/post recovery drills
remain release blockers. A delayed/unsolicited legacy Agent result can still
pollute a parked task on v1.7.29, and its delete API can remove metrics before
the main DB trigger blocks the delete; the experimental parking helper is
not a production rollback plan. Never overwrite live databases with a backup.

**IPv6 local follow-up (2026-11-06 12:10 UTC):** the experimental rollback
research now retains and parks pinned task rows rather than deleting them,
preserving parent IDs and avoiding SQLite cascading deletes. The SQLite tests
exercise FK preservation, sealed rows, damaged state and all-or-nothing
restore; an isolated v1.7.29 amd64 image test boots normal service against a
fresh synthetic fixture. That actual old binary still accepts a legacy
`agent.pingResult` for a parked task: parking does **not** fence the separate
metric store, and the old delete path can remove history before a SQL trigger
rejects the delete. This is a confirmed release blocker, not a usable rollback
procedure; details and test limitations are in
[`PING-FAMILY-ROLLBACK-DRAFT.md`](./PING-FAMILY-ROLLBACK-DRAFT.md). There is no
production rollback CLI and no live rows or metrics were changed.

The draft create/edit path now explicitly reports when persistence succeeded
but schedule reload failed, including the created ID and a no-duplicate-retry
message; the old REST bridge displays the message but omits structured RPC
error data. A process-local pending flag triggers a 30-second scheduler retry
only after failure, and successful reconciliation clears it; startup always
attempts an initial load. Focused SQLite/RPC/reconciliation and Agent tests
pass with `-race`. A failed reconciliation-job registration, persistent errors,
full browser localization, and a real end-to-end production rollout remain
open; this is not an assertion that scheduling is healthy. The separate
configuration importer still has identity, mapping and atomicity problems.

Read-only OC424 checks at 12:01-12:10 UTC still show v1.7.29/94e1973,
healthy with zero restarts, 14 reported nodes, root disk 50%, and both SQLite
`quick_check` results `ok`. Agent versions remain 10 x v1.6.24, one v1.6.25,
two v1.7.1, one v1.7.29; version alone does not establish current-session
capability. No new candidate, panel/Agent deployment, backup or live task was
created. The earlier scheduled MAC-WAN lock failure still requires observation
of a later successful scheduled trigger.

**Further IPv6 safety work (2026-11-06 11:35 UTC, local only):** configuration
exports now preserve explicit address families rather than refusing every
export when a pinned task exists. A pinned export uses schema 2, deliberately
refused by both the current schema-1 importer and older panels; it does not
export the runtime generation. Invalid family/generation combinations refuse
the export. Legacy exports remain schema 1. This is an export-only archive,
**not a restorable configuration import**: the planner's identity, node
mapping and partial-application issues remain unresolved, and import/preview
still refuse while pinned tasks exist. The configuration page explicitly
warns in five locales that a schema-2 archive is inspection-only and directs
recovery to an encrypted database backup; its mounted browser fixture passes.

The Agent draft now cancels its old session and immediately requests a new
challenge after a rejected active-session renewal, without retrying an old
panel that says the handshake is unsupported. An isolated, race-enabled real
Agent/real panel-handler/real scheduler fixture with a production 25-second
handshake cadence verifies a no-sample gap following an authenticated POST,
followed by a correctly tagged IPv6 sample after re-handshake. The fixture
uses fake DNS, a test-only probe hook, a one-second task interval and regular
test requests to keep the socket alive; it does not establish production
coverage or remote Agent behavior. An earlier fixture failed because it sent
only WebSocket control frames, which did not reset the panel's application
read deadline; the corrected fixture uses ordered application messages.

At 11:22-11:33 UTC the live panel is still v1.7.29/94e1973, healthy/restart0,
root 50%, both databases pass read-only quick checks, and 14 node UUID/token
pairs match digest `a1dc45812f0174f432eccf185fddd2bc1439fed8965fdf21ec8ece03a83d40d6`.
Reported Agent versions are 10 x v1.6.24, one v1.6.25, two v1.7.1 and one
v1.7.29; no update targets are set. Reported versions are not proof of a
current session's capability. A stable release can cause periodic self-update,
so a staged rollout must not assume only the chosen nodes will update.
No live task, node, token, Agent, backup or panel deployment was changed in
this follow-up. Public creation, production scheduler and probe hook remain
disabled pending safe import/rollback policy, actual Agent canary, exact
candidate CI, fresh MAC-WAN pre/post backup and recovery, and live acceptance.

The local pinned resolver now requests `ip6` or `ip4` DNS for the selected
primary family instead of querying both and filtering afterward. A reference
keeps its independently selected family; a local HTTP redirect regression
also checks its independent lookup. A real-Agent/real-panel-handler fixture
now retires a WS session with an authenticated POST while a probe is in
flight, verifies that the retired sample is not admitted, and obtains a new
IPv6 sample after re-handshake. This is a direct test dispatch with fake DNS,
not the production scheduler or fleet. A further local guard rejects pinned
intervals too large for `time.Duration`; the scheduler skips and logs any
overflowing stored interval instead of letting one bad task prevent other
valid schedules from being reloaded. Unit/create/edit and scheduler tests
cover both overflow and preservation of the valid task.

At that earlier checkpoint, focused `-race` runs passed for the Agent's `TestPinned*` and protocol module,
the panel's `TestPinnedRealAgentProductionTransport`, and the affected
scheduler/task tests, including a final-source transport rerun. A first run of
the new reference redirect test exposed a recursive closure introduced during
this follow-up; the closure now captures the original resolver and the Agent
suite passes. Production creation, Agent execution and scheduling are still
gated: import/export, safe rollback/ID policy, real scheduler coverage after
POST retirement, staged Agent compatibility and exact-candidate CI remain
unresolved at that checkpoint; the later follow-up above supersedes the export
and isolated scheduler evidence, but not the remaining release gates. OC424
was read-only checked at 11:15 UTC: v1.7.29/94e1973,
healthy, root 50%. No live task, Agent, backup or deployment was changed.

**Pinned history-boundary follow-up (2026-11-05):** the private, unwired
create/edit helpers now validate the explicit pin and persist a random
incarnation; the draft edit path rejects changes to the target, protocol,
reference or family within an existing pinned task. A fresh incarnation
prevents stale results from being admitted, but it does not partition stored
metric history, so allowing those edits would still merge incompatible series.
Rename, interval and assignment changes remain testable without changing the
probe's identity; changing its target, protocol, reference or family requires
a new task. This is local draft behavior only: the public mutation API,
scheduler and Agent execution hook are still disabled. The running OC424
panel remains on v1.7.29 and no production task or Agent was changed.

**Lifecycle and delivery follow-up (2026-11-05):** local, still-uncommitted
work adds an unwired direct-WS delivery helper. It requests a one-use permit,
then holds the credential, task and per-node session fences while it reloads
the current assignment and registers the dispatch before a bounded write.
Real SQLite batch-edit tests now exercise atomic rollback and renewed
incarnations. A successful authenticated POST conservatively retires pinned
WS capability; ordinary Agent unlock and exec-result POSTs can therefore skip
some pinned observations while the WS handshake recovers. An isolated loopback
test now runs the real race-instrumented Agent reader, handshake and probe
engine against panel session, direct-delivery and result-admission helpers and
a real SQLite metric store. A dual-answer fixture records an IPv6 TCP success,
an A-only fixture writes no loss point, duplicate replies are rejected, and a
simulated authenticated POST retirement skips a permit then recovers through
Agent re-handshake and records another IPv6 sample. This uses a test-only Agent
hook and a panel handshake harness, not production HTTP authentication, result
intake or scheduler. Separate WS result-handler tests reject malformed frames,
POST, stale sockets and unregistered dispatches.

A separate isolated transport test now runs the real Agent reader, handshake
and probe engine through the panel's actual HTTP credential middleware and
WebSocket handler, using real SQLite and the metric store. A dual-stack DNS
fixture records an IPv6 TCP result; an A-only answer yields a diagnostic but
no loss point. Invalid credentials and WS-only results submitted over POST are
rejected; a successful authenticated POST retires the session and the real
Agent re-handshakes before another result is admitted. A test-only token reset
during a blocked probe verifies that the old credential is rejected and no
point is written after the probe finishes. Dispatch is explicitly invoked by
the test, the Agent probe hook and DNS/dial routes are test-only, and its
wrapped socket does not exercise the production socket registry's transport
failure path. The scheduler and Agent hook remain disabled in production;
POST-triggered gaps need visible diagnostics and coverage assessment before
enabling the feature.

**Isolated scheduler follow-up (2026-11-05):** a non-production scheduler can
now opt in to the direct dispatcher. A real SQLite/metric-store integration test
drives three separate real Agent test processes through the authenticated WS
handler: an IPv6 success is recorded, an A-only response produces a diagnostic
without loss, and a disabled Agent refuses its permit and receives no probe.
Initially the fixture inserted the task directly; the later create/edit
follow-up below uses the draft persistence helper. Neither version covers the
production add API or browser. Production does not opt in. The 64-worker bound applies to delivery,
not Agent execution, so probes may overlap after a successful socket write.
In the same session the ledger now rejects every older leg once a newer
dispatch for that task is registered, even if the newer delivery fails; this
avoids admitting a late sample with a newer admission timestamp at the cost of
potentially skipped observations. A delayed older permit also cannot register
after a newer dispatch for the same task. Per-task rotating start positions avoid a
shared counter's prefix bias under saturation, while cross-task fairness is
not guaranteed. Pinned IP literals no longer get legacy address-family skip
labels. These are isolated draft changes, not a rollout. Session replacement,
coverage impact and dispatch/Agent execution limits still need explicit tests.
Race checks pass for the panel's pinned dispatch/admission, scheduler and
three-Agent fixture, including three repeats of the ordering and scheduler
checks. The first expanded Agent command accidentally matched the existing
`TestHTTPPing` external-network cases and failed on DNS, timeouts and an HTTP
status; a narrower Agent pinned/local-HTTP race run and protocol package pass.
These local results do not replace exact-commit CI. At 17:51 UTC OC424 still
reports v1.7.29/94e1973, healthy with zero container restarts, its local Agent
is active, and its root disk is 50%. No production write, backup or release
was made during this follow-up.

**Create/edit and mounted-UI follow-up (2026-11-05):** the draft create and
batch-edit paths now accept an explicitly injected scratch database and reload
callback. An isolated RPC test binds the requested three-node IPv6 hostname,
checks the persisted family/incarnation and verifies invalid inputs and atomic
mixed legacy/pinned edits. A separate test-only RPC method exercises the
actual REST bridge and its authorization: anonymous calls are refused, the
production add method remains gated, and an admin call to the fixture stores
the explicit IPv6 pin. The isolated real-scheduler/three-Agent test now creates
its task with that same scratch persistence helper instead of inserting the
scheduled task directly. It observes an IPv6 result, an A-only diagnostic
without a loss point and a disabled Agent refusing a permit. These tests use
separate harnesses, a test-only Agent hook and fake DNS: they are not one
end-to-end production browser-to-Agent test and do not prove live readiness.

The add UI has a family selector only when a fixture explicitly opts in;
production still hides it. Mounted Chromium tests at desktop and mobile widths
cover the requested target, three nodes, `auto`, 60 seconds, IPv6 and default
off; pinned task edits keep their target/protocol/reference locked, and the
server-view membership payload preserves the legacy omitted-field contract.
Create/edit/delete now use the existing REST-envelope verifier so an HTTP 200
business refusal is not shown as a successful save; create keeps the form on
failure. Browser tests use a mocked endpoint, not the live panel. Frontend
lint, build and all 217 Node tests pass; the focused browser and Go race tests
pass locally. A mistakenly issued `go test -race ./agent/...` from the root
failed at setup because Agent is a separate module; the intended pinned Agent
and Agent protocol tests pass when run from `agent/`.

Import preflight and apply now hold an exclusive process-local fence against
the draft pinned-create transaction, with concurrency tests. This does not
make import atomic across entities, fix its planner identity/client mapping or
support multiple panel instances. Import/export deliberately refuse when pinned
rows are present. After a create commits, the helper currently ignores reload
failure to avoid falsely reporting creation failure and encouraging a duplicate;
visible failure and a retry/reconciliation path are still needed. Public add,
edit, the production scheduler and Agent execution hook remain disabled.
Current-session diagnostics, safe downgrade with pinned rows, numeric-ID
reuse policy, measured Agent compatibility/rollout, exact-commit CI and a
fresh MAC-WAN encrypted backup and isolated restore are still release gates.
At 18:13 UTC OC424 was healthy/restart0 on v1.7.29/94e1973 and root usage was 50%.
No production mutation, backup, release or deployment was made in this pass.

**Diagnostic primitive follow-up (2026-11-05):** admitted WS results now
update a private, bounded in-memory diagnostic store under the existing task
and session fences. It records non-probe reasons without writing loss, marks
metric-write refusal separately, and records successful ingestion; a
panel-issued permit sequence prevents a late older result from replacing a
newer result in the same session. Dual primary legs and the independent
reference leg have separate state. Entries expire after two minutes, writes
fail immediately on contention or capacity, and a dropped-update counter is
retained. Local race tests cover ordering, expiry, dual-leg capacity,
contention, concurrency and result admission. At this checkpoint it was not
exposed by an API or UI; the later diagnostic snapshot follow-up describes the
administrative exposure and its remaining limits. Public creation and
production probe dispatch remain disabled.

**Diagnostic snapshot follow-up (2026-11-05):** an authenticated, admin-only
single-task/single-node REST/RPC read now provides an on-demand, bounded
point-in-time snapshot for a currently assigned pinned task. It rechecks the
node, task incarnation and current socket under the credential/task/session
fences, omits expired or stale legs, and reports the global dropped-update
count. The mounted UI does not poll and expires responses conservatively based
on server timestamps and request duration; it explicitly warns that a task,
node or connection can change after the snapshot. It distinguishes offline,
POST-only, unknown and active handshake, without treating a handshake as proof
of probe execution. A refused, timed-out or otherwise failed reserved permit is
reported only as `permit_unavailable` (reason unknown, no probe sent): the
same Agent wire error also covers multiple other causes. Its task/session and
per-leg sequence fences prevent a late failed request or old result from
overwriting a newer observation. Canceled or unreserved attempts may still
appear unknown, as can scheduler saturation and Agent-side silent refusal
after a successful write; diagnostic absence is not evidence of a probe.
Local tests cover guard order, assignment/session changes, expiry, contention,
dual legs, cancellation and late recovery; a real-Agent handler fixture checks
the refused-permit status without a loss sample. This is draft work, not
authorization to enable production creation or scheduling.

**Agent capability gate correction (2026-11-05):** the handshake alone is
not evidence that the probe execution hook is enabled. The draft Agent had
issued a valid permit even with its hook disabled; a future scheduler could
therefore send a probe that was silently dropped. Permit issuance now refuses
while the execution hook is nil. A regression test checks the wire error and
that no ticket is issued. A second real-Agent/production-handler fixture
completes the handshake with the probe hook disabled, observes the panel's
permit request and the Agent's rejection, and verifies that no probe frame or
metric is produced. The other isolated transport fixtures pass with their
explicitly enabled test hook. This does not release or roll out an Agent.

An authenticated local POST/real SQLite/metric-store lifecycle test drains
queued legacy results before deletion and checks that the current draft rejects
later legacy results when the ID is missing or its deletion reservation remains,
even after deliberately inserting a legacy or pinned row with that ID. The
pinned result and delivery helpers also check that reservation. This is not a
complete **legacy numeric-ID reuse boundary**: older binaries, a restored main
database lacking the reservation, external SQL and other processes do not
share these checks. Normal SQLite `AUTOINCREMENT` did not reuse the ID in the
fixture, and the live read-only schema was also `AUTOINCREMENT` (sequence/max
29 at 16:41 UTC); neither observation covers those other paths. The unused unguarded `SavePingRecord`/`ingestPingResult` bypass
has been removed. Draft configuration export now refuses pinned rows instead
of silently dropping their family; import/preview refuse before any writes
while pinned rows exist. This is a temporary fail-closed restriction, not a
working import/export. The existing import planner's identity and partial
apply issues still require repair.

The smaller alternative of resolving the hostname on the panel and sending an
IPv6 literal to old Agents is **not equivalent**: it would use the panel's DNS
view rather than each node's, can select a different CDN endpoint, and does
not preserve hostname-dependent HTTP behavior. It was not deployed or used to
change a live task. A read-only OC424 lookup at 16:37 UTC yielded
`240e:904:800:1f80::b00:137`, while a second lookup at 16:48 yielded
`240e:904:800:1f80::b00:123`; neither is a stable or per-node guarantee.
`migrateLegacyPingAllClientsExpansion` also runs at every startup. The local
draft now excludes pinned rows, preserving an intentionally empty `default_on`
assignment across restarts, while still expanding legacy rows; an isolated
SQLite restart test covers both cases. Older panels do not have that exclusion
and could expand or dispatch pinned rows as legacy on rollback. A tested
rollback policy is still required before enabling the mode. Public creation,
scheduler dispatch and Agent execution remain gated; diagnostic display,
real multi-Agent scheduler tests, exact candidate CI and the fresh MAC-WAN
recovery/deployment gates remain open.

**Agent test follow-up (2026-11-05):** an expanded local
`go test -race ./server ./protocol/v2` in the Agent module failed. The three
live-network ICMP/TCP/HTTP tests depend on external destinations and are
normally skipped in CI; several failed on DNS/timeout here. The same run also
reported a race in the pre-existing legacy `httpPing` family variable when
the HTTP client times out while the transport's dial goroutine writes it; the
same code exists in the v1.7.29 source (the running binary is not established).
Separately, `go test -race ./server -run '^TestPinned'` and the Agent v2
protocol package pass locally.
The local draft now protects that variable and adds a deterministic delayed-DNS
race regression and loopback success check; both pass with `-race`, alongside
the pinned tests. They do not constitute an Agent release or a fleet rollout.

**Prior backup close-out (2026-11-03).** The v1.7.22 post and v1.7.23
pre/post archives reached MAC-WAN with matching sidecars, `incoming/` holds
the two older verified copies, and four isolated restore drills pass (see
[`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md) and
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md)). Historical success does not prove
current access. On 2026-11-05 direct MAC-WAN SSH timed out; the pinned
reverse-tunnel route worked and the new v1.7.29 pre/post backups passed
isolated recovery. Validate fresh backups before every subsequent release.

Deferred by owner decision: 200-node capacity work stays on hold; a stable
release may still trigger agent auto-updates. OVH is sold and its disk
sanitization is no longer possible — that item is closed with the residual
credential-exposure risk recorded, not re-opened.

**Prior identity gate (2026-11-04, rechecked 13:38–13:42 UTC):** OC424
was healthy on v1.7.27 with 13 clients. A consistent identity digest over
the live database, v1.7.27 pre/post archives and v1.7.26 post archive matches
(`55bc6191…d2bde`) in all four snapshots; the earlier recorded v1.7.27 digest
`a2737fd0…d0ce` is inconsistent with those archives. Its cause or original
method is not established. The owner is unsure about any intervening change;
the snapshots cannot rule out a change subsequently reversed. Do not overwrite
or rotate live identities. At that checkpoint MAC-WAN SSH worked and the
three older archives passed checksum, decryption and SQLite quick checks;
the later fresh pre/post recovery is recorded above. Stable releases can
trigger Agent auto-update. Client-reported version fields at 02:51 UTC on
2026-11-05 were v1.6.24=10, v1.6.25=1 and v1.7.1=2; these fields may be
stale and do not verify the binaries actually running. Comparison method and
archive details: [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Pre-release recheck (2026-11-05):** OC424 was healthy/restart0 on
v1.7.27, both production databases pass read-only `quick_check`, and the
13-client identity digest still matches the audited snapshots. A fresh
encrypted pre-release archive reached MAC-WAN and passes ciphertext checksum,
authenticated decryption, all 1,345 manifest file checks and both database
integrity checks. The first isolated drill used a mistyped image digest and
failed to start the container; the retry with the inspected pinned v1.7.27
image passes with networking disabled. This verifies the pre-release archive,
not a later post-upgrade backup. The subsequent post-upgrade drill also passes;
see the current checkpoint. No production node or credential was changed.

**Prior operational checkpoint (2026-11-04 05:10 UTC):** OC424 was
healthy/restart0 on **v1.7.27/24faa9d** (metric CSV export on node charts).
Pull-request CI41773313554 passes 8/8, exact merge CI41774384405 8/8,
release41775304251 10/10 and Docker41775869827 2/2; the released arm64 asset
and the image's `/app/nekomari` hash identically (`c6deb156…41ee`) on OC424,
and the image is pinned by the index digest
`sha256:b706f097…bad0`. OC424 upgrade9/9 passes and all 13 clients keep
matching `uuid`/`token` rows (originally recorded as `a2737fd0…d0ce`; the
later audit corrects this to `55bc6191…d2bde`); fresh encrypted
pre (`5c142e5d…4b86`) and post (`9e8a312b…26bf`) archives reached MAC-WAN's
`manual/v1727-recovery-20261104/` with sidecars, and two isolated drills pass
(pre boots v1.7.26, post boots v1.7.27). Owner on-page acceptance of the CSV
export and the second off-host destination decision remain. The alert-rule
send path was regression-checked on v1.7.27: the
owner sent a test notification from the saved load rule and acknowledged the
real `[TEST]` receipt (2026-11-04). The v1.8.0 50-node gate passes as recorded in
[`CAPACITY-50NODE.md`](./CAPACITY-50NODE.md); the `< 1 MiB/day` drift SLO and
the 200-node benchmark stay standing items.

**Prior operational checkpoint (2026-11-04, 50-node gate):** the v1.8.0
50-node capacity/memory gate passes on a scratch panel built from
`2cf1cc8` (v1.7.26 code): 50/50 nodes, 30047 reports accepted, report p95
2.0 ms / p99 2.5 ms, `/api/public` p95 2.6 ms, RSS plateau 92–96 MiB with
flat Go heap and zero ERROR lines. Full method, results and limits in
[`CAPACITY-50NODE.md`](./CAPACITY-50NODE.md). The next bounded slice is the
second off-host destination decision **[Owner]**, then v1.9.0 planning. The
`< 1 MiB/day` drift SLO and the 200-node benchmark stay standing items.

**Prior operational checkpoint (2026-11-04 02:16 UTC):** OC424 is
healthy/restart0 on **v1.7.26/60b4524**. Pull-request CI41756421918 passes
8/8, exact merge CI41757342465 8/8, release41758329063 10/10 and
Docker41759156088 2/2. The released arm64 asset and the extracted image
binary verify natively on OC424; the image is pinned by the index digest
`sha256:7a666e3e…65d6a`. OC424 upgrade9/9 passes and all 13 clients keep
byte-identical `uuid`/`token` rows (digest `55bc6191…d2bde`); the
`system.restarts` definition exists post-switch. Fresh encrypted pre/post
archives exist with matching hashes, both reached MAC-WAN's
`manual/v1726-recovery-20261104/` with sidecars, and two isolated drills
pass (pre boots v1.7.25, post boots v1.7.26). The next bounded slice is the
50-node capacity/memory gate, then the second-destination decision.
See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Previous operational checkpoint (2026-11-03 15:25 UTC):** OC424 was
healthy/restart0 on **v1.7.25/05e7fe2**. Pull-request CI41657862603 passes
8/8, exact merge CI41659496229 8/8, release41660958531 10/10 and
Docker41662404758 2/2. The released arm64 asset and the extracted image
binary verify natively on OC424; the image is pinned by the index digest
`sha256:2f6bbfde…245c1`. OC424 upgrade9/9 passes and all 13 clients keep
byte-identical `uuid`/`token` rows (digest `55bc6191…d2bde`). Fresh
encrypted pre/post archives exist with matching hashes, both reached
MAC-WAN's `manual/v1725-recovery-20261103/` with sidecars, and two isolated
drills pass (pre boots v1.7.24 amd64; post boots and serves
v1.7.25/05e7fe2). Owner-session UI acceptance of the test button, one
owner-acknowledged real test receipt and real remote alert receipt remain
unverified. At that point the next bounded slice was the v1.8.0
remainder: uptime restart-count metric, then the 50-node gate and the
second-destination decision.
See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Previous operational checkpoint (2026-11-03 14:20 UTC):** OC424 was
healthy/restart0 on **v1.7.24/64b8f57**. Pull-request CI41646180928 passes
8/8, exact merge CI41647810219 8/8, release41649086456 10/10 and
Docker41650325053 2/2. The released arm64 asset and the extracted image
binary verify natively on OC424; the arm64 image is pinned by the index
digest `sha256:597f6314…52e6a3`. OC424 upgrade9/9 passes and all 13 clients
keep byte-identical `uuid`/`token` rows (digest `8e0928f3…6790`). Fresh
encrypted pre/post archives exist with matching hashes, both reached MAC-WAN's
`manual/v1724-recovery-20261103/` with sidecars, and two isolated drills pass
(v1.7.23 pre boots v1.7.24 amd64 with the migration applied; post boots and
serves v1.7.24/64b8f57). Owner-session UI acceptance of the window-coverage
figure and real remote alert receipt remain unverified. The next bounded
slice is the v1.7.25 alert-rule test button.
See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Current operational checkpoint (2026-11-03 01:00 UTC):** OC424 is
healthy/restart0 on **v1.7.23/1e4d22a**. Pull-request CI41484802139 passes
8/8, exact merge CI41554085010 8/8, release41554944654 10/10 and
Docker41555850720 2/2. The arm64 image binary matches the release asset
natively on OC424 and the multi-arch OCI index verifies. OC424 upgrade9/9
passes; all 13 clients keep byte-identical `uuid`/`token` rows. Fresh
encrypted pre/post archives exist with matching OC424-root and executor
copies, and the deferred MAC-WAN archive copy and four isolated restore
drills are now complete. Owner-session UI and real remote notification
receipt remain unverified.
See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Current operational checkpoint (2026-11-03 00:08 Asia/Shanghai):** OC424 is
healthy/restart0 on **v1.7.22/5a81d96**. Pull-request CI41468164541 passes
8/8, exact merge CI41469995167 8/8, release41473382220 10/10 and
Docker41474774363 2/2. Both architectures of the assets and images verify and
the arm64 image binary matches the release asset natively on OC424. OC424
upgrade9/9 passes; both DB quick checks pass and all 13 clients (owner's
post-v1.7.21 node changes) keep byte-identical `uuid`/`token` rows. The local
Agent (PID 499631, NRestarts 2) is unchanged. Preserve the live node set and
never restore an older database over it. Owner-session UI and real remote
notification receipt remain unverified.

Fresh manual pre/post encrypted backups have matching OC424-root and executor
ciphertext copies. The MAC-WAN `incoming/` copy and the isolated restore
drills are deferred: MAC-WAN SSH (59.66.23.138:52022) timed out repeatedly
from about 16:00 UTC while its 15:51:49 health report to OC424 was `ok`. Run
both drills and push the post archive to MAC-WAN as soon as the host is
reachable again. The SAO-Nekomari theme premium sync is now verified: the
owner applied the authenticated payload after the v1.7.22 switch, the row
keeps all 45 keys and `/api/public` matches the three live entries; see
[`SAO-CYCLE-ROLLOUT.md`](./SAO-CYCLE-ROLLOUT.md).
Neither online ciphertext copies nor an isolated restore prove
independent-media recovery. OVH is sold; disk sanitization is closed as
unrecoverable. See
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md) and
[`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md).

**SAO cycle-display rollout (2026-11-02):** the owner confirms the active
upstream SAO v1.3.4 upgrade. Its 322 files match the official release. A
separate, unofficial [`v1.3.4-nekomari.1`](https://github.com/Aone2233/Komari-Theme-SAO/releases/tag/v1.3.4-nekomari.1)
variant based on upstream `918fb899` is published from `ed6dd31`: quota,
instance details and mobile mini cards use quality-marked Agent cycle counters,
preserving valid zero and treating missing or invalid values as unknown. The
locally built asset has SHA-256
`9bdacc816dcc195b2592dc9f4eaf663d1f2444f9cd3ebe8c2e44b80f1084f90b`.
Tests (389), typecheck, lint and isolated published-panel/Agent/browser
checks pass; this is not a CI or build-attestation claim. OC424 retains
**upstream SAO intact for rollback**; the owner activated the staged
`SAO-Nekomari` (`v1.3.4-nekomari.2`) on 2026-11-02 through the authenticated
themes UI, with eight older hashed assets retained and no database change.
The separate upstream "today traffic" path now validates historical series
semantics: legacy cumulative series are shown as unknown instead of being
summed. MegaBox's Agent reset day and provider accounting remain unverified:
never change the Agent cycle, reset a ledger, or claim provider-billing
equivalence.

**Historical operational checkpoint (2026-10-31 03:48 Asia/Shanghai):** OC424 is
healthy/restart0 on **v1.7.19/cf221de**. Exact merge CI41114880987 passes 8/8,
release41116050542 10/10 and Docker41117063818 2/2. Linux panel/Agent
checksums and attestations, both architectures of both OCI identities and
the native panel binaries verify. The released panel passes four isolated
MAC-WAN SLA browser cases. OC424 upgrade9/9, public27/27, scoped
forecast13rows and 13 fresh CPU/uptime reporters pass. Node identity digest,
API-key scopes/expiry, SSH keys, GeoIP and local Agent remained unchanged during
the switch. This
clarifies existing observed-span coverage; full-window coverage semantics,
owner-session UI and actual incident receipt remain open.

Fresh manual pre/post encrypted backups have matching MAC-WAN `incoming/` and
online executor copies. Pre restores under pinned v1.7.18/v1.7.19; post under
v1.7.19. Post compares 21,140 archived rows across 12 histories: zero
in-retention rows missing or changed; 48 expired or inactive-tier rows absent.
The 03:35 automatic health report succeeded before manual backups; it checks
`scheduled/` and does not verify those archives or the next daily timer via the
new route (2026-10-31 11:30 Asia/Shanghai). Scheduled retention remains 14/14
pairs; the next successful receive applies existing `keep=14` and retires its
oldest eligible pair, with a matching `incoming/` copy already verified.
Independent-media restore and OVH disk sanitization are unverified. See
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md) and
[`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md).

**Historical operational checkpoint (2026-10-30 22:38 Asia/Shanghai):** OC424 is
healthy/restart0 on **v1.7.18/8dbfd8c**. Candidate CI41066053281 and exact
merge CI41067514587 pass 8/8; release41068843126 passes 10/10 and
Docker41071110757 2/2. Published binary provenance, dual-platform images and
isolated MAC-WAN API and browser acceptance pass. OC424 upgrade9/9,
public27/27, scoped forecast13rows and 13 fresh CPU/uptime reporters pass.
The session-only preview is read-only, not a notification delivery test; owner
login acceptance and actual incident receipt remain open.

Fresh manual pre/post encrypted backups have matching MAC-WAN `incoming/` and
online executor copies. Pre restores under pinned v1.7.17/v1.7.18; post under
v1.7.18. Post compares 21,768 archived rows across 12 histories: zero
in-retention rows missing or changed; 820 expired or inactive-tier rows absent.
The 22:30 automatic health report succeeded before the isolated manual backups;
those backups do not verify the next daily timer via the new route (2026-10-31
11:30 Asia/Shanghai). Scheduled retention remains 14/14 pairs; the next
successful receive applies existing `keep=14` and retires its oldest eligible
pair, with a matching `incoming/` copy already verified. Independent-media
restore and OVH disk sanitization are unverified. See
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md) and
[`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md).

**Historical operational checkpoint (2026-10-30 17:11 Asia/Shanghai):** OC424 is
healthy/restart0 on **v1.7.17/0a9fd1d**. Candidate CI41026965107 and exact merge
CI41028016613 pass 8/8; release41028983238 passes 10/10 and Docker41029876841
2/2. The published binary passes isolated MAC-WAN maintenance/grace tests;
upgrade9/9, public27/27, scoped forecast13rows, and all 13 fresh CPU/uptime
reporters pass. No existing Agent tokens, API-key scope/expiry, database schema
or authorized SSH keys changed. This is a bounded offline-alert correction, not
the v1.8 rule-test button or proof of real incident receipt.

Manual pre/post encrypted backups have matching MAC-WAN `incoming/` and online
executor copies. Pre boots both pinned v1.7.16/v1.7.17; post boots v1.7.17
and compares 20,492 archived rows across 12 histories with zero missing within
retention or changed values. The 17:01 manual pre-backup attempt collided with
the health timer's shared lock and failed without creating an archive; a 17:02
manual retry and 17:08 manual post backup succeeded. A 17:10 manual health
report identifies the post backup. This does not establish the next automatic
daily trigger via the new route (2026-10-31 11:30 Asia/Shanghai), independent
media recovery, actual alert receipt or owner-session acceptance. Scheduled
retention is now 14/14 pairs: a successful next scheduled receive applies the
existing `keep=14` policy and retires its oldest eligible pair; a matching
`incoming/` ciphertext copy was verified. No broad cleanup or schedule change.
See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md) and
[`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md).

**Historical operational checkpoint (2026-10-30 15:59 Asia/Shanghai):** OC424 is
healthy/restart0 on **v1.7.16/e7323c8**. Exact merge CI41021022614 passes 8/8,
release41021806313 10/10 and Docker41022572275 2/2. The published binary's
isolated scheduled-logging test passes, as do upgrade9/9, public27/27, scoped
forecast13rows and 13 fresh CPU/uptime reports. The owner-added QWRT node and
all other current UUIDs/tokens remain. The persistent forecast reset logs one
warning instead of a minute-by-minute error; it still refuses that prediction.

Manual pre/post encrypted backups have hash-matching MAC-WAN `incoming/` and
additional online executor copies. The pre archive restores under pinned old
and new images, the post under the new image; 12 selected histories show no
missing in-retention or changed archived rows. The new proxy's automatic health
timer works, but its next daily backup (2026-10-31 11:30 Asia/Shanghai), actual
alert receipt, independent-media restore, OVH disk sanitization and owner UI
acceptance remain unverified. A stable release may trigger other Agents with
self-update enabled. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Historical operational checkpoint (2026-10-30 15:16 Asia/Shanghai):** OC424 was
healthy/restart0 on **v1.7.15/7c13e50**. Exact push CI41016555952 passes 8/8,
release41017359338 10/10 and Docker41018148539 2/2. Linux asset checksums and
tag/source/workflow attestations, both architectures of both OCI images,
published-binary synthetic forecast6/6, OC424 upgrade9/9 and public27/27 pass.
All 13 registered clients retain their UUID/token digest and have fresh CPU and
uptime rollups; the read-only scoped forecast has 13 rows (10 linear, three
insufficient). Owner-session UI acceptance remains outstanding.

MAC-WAN's repaired proxy passes the actual 14:54 automatic health timer and a
15:15 manual health report for the post-upgrade backup. Fresh manually initiated
pre/post encrypted backups have matching MAC-WAN and additional online-host
copies; the pre archive restores under pinned old/new images and the post under
the new image. The post recovery compares 12 selected metric histories without
missing in-retention or changed archived rows. The **next daily timer via the
new route is still unverified** (2026-10-31 11:30 Asia/Shanghai); manual runs do
not prove it. Independent-media restore, actual alert receipt and OVH disk
sanitization remain unverified. Stable release can trigger auto-updates on
Agents whose local settings allow them. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md)
and [`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md).

**Historical operational checkpoint (2026-10-30 14:40 Asia/Shanghai):** the owner
confirms a deliberate deletion followed by their addition of `QWRT 主路由 | 宿舍`;
13 clients are now registered and all 13 have fresh CPU/uptime rollups. This
replaces the earlier 13-client identity set; do not restore deleted nodes or
overwrite the live database. OC424 remains on v1.7.14/7f94561, healthy. Forecast
fixes are merged in PR128/129; exact merge CI41013963594 passes 8/8, but v1.7.15
has **not** been tagged, published or deployed. PR128's first merge CI failed an
hour-dependent SQL fixture assertion, deterministically corrected by PR129;
it is not erased by the subsequent pass. A stable release may trigger periodic
updates of Agents whose local configuration does not disable them.

MAC-WAN's scheduled 2026-10-30 11:30 backup and recurring health reports failed
after its egress changed and later the direct SSH route stopped delivering new
connections. The now-installed pinned MegaBox proxy was manually verified with a
fresh 13-client encrypted backup, two online ciphertext copies, isolated old
image recovery and a real health report. The 14:35 successful backup and 14:38
health report are **manual**, not proof of the next automatic timer. Check the
next actual health and daily timer; independent-media restore is unverified.
See [`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md).

**Historical security checkpoint (2026-10-28 23:53 Asia/Shanghai):** the owner reports
OVH/KSLE has been relinquished. It is no longer a trusted backup/test host.
Historical plaintext backups held panel secrets; disk sanitization and other
credential exposure are unknown. Three specifically identified OVH backup/
health SSH authorizations are revoked on OC424; other keys are unchanged.
The owner confirms intentional deletion of the OVH node; 13 nodes now form the
approved baseline. MAC-WAN's replacement backup and health timers are active;
fresh 13-node encrypted recovery and an additional online-host ciphertext copy
pass. The first scheduled MAC-WAN daily backup succeeds on 2026-10-29;
independent-media recovery, real incident delivery and OVH disk sanitization
remain unverified. Before each production
rollout, take fresh pre/post backups and verify continuity/recovery. See
[`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md) and
[`OVH-RETIREMENT-2026-10-28.md`](./OVH-RETIREMENT-2026-10-28.md).
All OVH availability/schedule statements below are historical, not current.

**Historical checkpoint (2026-10-29, Asia/Shanghai):** OC424 served
**v1.7.14/7f94561** at `nekomari.orderly2233.org`, healthy/restart0. Candidate
CI40807334416 8/8, exact merge CI40808023833 8/8, release40808682926 10/10,
Docker40809339436 2/2, Linux checksums/provenance, dual-architecture image
identity, isolated MAC-WAN published-binary browser checks, OC424upgrade9/9,
public27/27 and scoped forecast13rows pass. Prior v1.7.6 actual SQL recovery is dated
evidence; no unrelated broad local SQL/capacity rerun.
Earlier v1.7.4 SLA14nodes/58tasks remains dated evidence, not a newly rerun
SLA live comparison. The existing tools and new uptime page remain
in the admin shell. After the earlier removal, Pi adds a node with the owner's
explicit authorization: 14 registered clients were the earlier release baseline;
the owner later intentionally removes OVH, leaving 13 at this historical checkpoint.
Preserve all remaining nodes. Existing UUIDs/tokens and Agent services are
unchanged. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md) for immutable artifacts,
actual evidence and patch rollback to v1.7.13. The earlier v1.7.0->v1.6
auth-migration rollback still requires the paired old binary and DB.

**Historical operational follow-up (2026-10-29, Asia/Shanghai):** `VT01 | vps.taxi`
is observed offline at 11:42, with 12 of 13 nodes reporting. Its last persisted
sample is 11:26:18.909, after the 11:12 panel switch and successful 13-node
acceptance. The scoped live status reports it offline. Its configured SSH
endpoint accepts a TCP connection from MAC-WAN but does not provide an SSH
banner within 20 seconds; OC424's attempt also times out. At 12:15-12:17,
VT01 resumes reporting and all 13 nodes have fresh CPU/uptime readings; at
12:20 it is stale again, last sample 12:17:22.908, with 12 of 13 reporting.
The intermittent cause remains unknown. No node or Agent was modified or
restarted. Check the provider console and host/Agent service if it persists;
retain its registration and credentials. See
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

**Adjusted active objective:** close the bounded v1.7 delivery/handoff, then
continue small v1.8 observability/correctness batches. Do not mark the entire
roadmap or every v1.7 exit criterion complete. All 200-node experiments/benchmarks
and their 26 failed reports remain deferred. Reuse exact-source and unchanged
source-batch checks; no redundant broad local frontend/Go/capacity rerun.

**Prior bounded delivery:** v1.7.14 samples default-theme disk and memory
cache state into two independently retained, session-only panel history series.
The UI uses points instead of asserting exact transitions. Published-binary
browser checks, 13-node pre/post encrypted recovery and OC424 continuity pass;
older-image manual reclaim can delete the new history. See
[`THEME-CACHE-HISTORY.md`](./THEME-CACHE-HISTORY.md).

**Prior bounded delivery:** v1.7.13 adds session-only connection-pool wait
interval history to the existing Performance profiles page. Six independently
retained metrics are sampled naturally on OC424. Published-binary browser and
13-node pre/post encrypted recovery pass; the first post-boot total-row count
assertion failed and its cause was not reproduced. Two subsequent drills on
the same archive compare all selected rows and preserve all retained data, but
do not explain the earlier failure. Keep it as an observation to investigate
if it recurs; totals alone are not a safe recovery invariant. Old-image manual
reclaim can delete the new history. See [`PANEL-POOL-HISTORY.md`](./PANEL-POOL-HISTORY.md).

**Prior bounded delivery:** v1.7.12 avoids false container data-mount
persistence claims. Custom-root anonymous-like paths warn, while other mounts
require runtime configuration verification. Exact publication, four actual
isolated OVH published-image mount scenarios, encrypted pre/post recovery and
OC424 continuity pass. See [`DATA-MOUNT-WARNING.md`](./DATA-MOUNT-WARNING.md).

**Prior bounded delivery:** v1.7.11 adds session-only panel RSS, heap and
goroutine history in the existing Performance profiles page, with independent
one-day default retention and unknown gaps. Published-binary OVH browser/auth
checks, encrypted pre/post recovery and OC424 continuity pass. Older images can
delete panel history during manual reclaim; preserve the post backup. Reboot
counts and full v1.8 remain open. See
[`PANEL-HISTORY.md`](./PANEL-HISTORY.md).

**Prior bounded delivery:** v1.7.10 bounds a successful but completely empty
GeoIP country result to one hour, without changing positive/error results or
provider configuration. Exact CI/publication, OVH synthetic published-binary
checks, encrypted pre/post recovery and OC424 continuity pass. See
[`GEOIP-EMPTY-COUNTRY.md`](./GEOIP-EMPTY-COUNTRY.md).

**Prior bounded delivery:** v1.7.9 adds last-observed built-in theme disk-cache
and in-memory fallback state to the session-only in-panel Performance profiles
page. It is not continuous disk verification, byte usage or historical sampling.
Exact CI/publication, OVH isolated browser/authorization, encrypted pre/post
recovery and OC424 acceptance pass. See
[`THEME-CACHE-DIAGNOSTICS.md`](./THEME-CACHE-DIAGNOSTICS.md).

**Prior bounded delivery:** v1.7.8 adds node uptime history and an
in-panel admin view, one bounded part of v1.8 observability. Historical buckets
retain the last positive valid reading, not reboot events; zero/absent/invalid
readings remain unknown and old history is not backfilled. The new metric has
its own default one-day retention. Publication, OVH published-artifact checks,
encrypted pre/post backup, old/new isolated recovery and OC424 acceptance pass.
Old versions can delete the new metric on manual storage reclaim after rollback;
preserve a post-upgrade encrypted backup before any downgrade or reclaim. See
[`UPTIME-HISTORY.md`](./UPTIME-HISTORY.md).

**Prior bounded delivery:** v1.7.7 fixes missing/invalid paid-node expiry:
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
first-trigger gate for the former OVH receiver; MAC-WAN's replacement first
daily trigger is separately verified on 2026-10-29. Neither proves an
independent-media restore or measured RPO guarantee.
Live incident delivery and independent media remain open. dsh records owner
history/audit/API-key UI acceptance; no new owner session is exercised here.
v1.7.5 provides the SQLite source startup fix; v1.7.6 cross-backend corrections
are released/recovered/live-accepted. No stored history rewrite or production ANALYZE.
Continue one bounded correctness/observability item, with 200-node work deferred.

**Next bounded work:** observe the next actual MAC-WAN daily trigger over the
repaired proxy and its subsequent health report; do not treat a manual run as a
scheduled trigger. The scheduled directory now has 14/14 pairs, and the next
successful receive may remove its oldest eligible pair under the existing
retention policy; the oldest pair has a verified separate incoming copy.
Investigate the earlier post-boot assertion if it
recurs; the v1.7.14 drill separately observes retention cleanup, without
establishing the cause of the earlier failure. The browser jobs passed on their
pinned Ubuntu 24.04 runner in PR121 and exact merge CI. Then select a small correctness/observability batch, with fresh
13-node pre/post backup and isolated recovery gates for any new release. Keep
200-node work deferred. Independent-media recovery, real incident delivery,
complete API-caller inventory and the span/window-coverage decision remain open.
No whole-roadmap app goal change or new owner-session UI acceptance is claimed.

**Delivered in v1.7.15, with limits:** refuse forecast inputs with missing or
invalid required traffic directions, sum overflow or a direction decrease
between selected buckets. An additional unaligned cycle-boundary correction
excludes straddling backing buckets and retains the first partial output bucket;
this can discard valid values from an indivisible backing bucket and does not
prove the node's actual reset timezone. A zero in a refused row is not measured
usage; intra-bucket resets remain undetectable with bucket maxima. See
[`FORECAST-DIRECTIONS.md`](./FORECAST-DIRECTIONS.md).

### Historical v1.7 release readiness checkpoint (2026-10-28)

| Gate | Current state | Required next evidence/action |
|---|---|---|
| Source/publication/deployment | Theme-cache history v1.7.14/`7f94561`; exact merge CI40808023833 8/8, release40808682926 10/10, Docker40809339436 2/2, MAC-WAN published-binary checks, OC4249/9, public27/27, scoped forecast13rows and13-client/auth continuity pass | Retain immutable tags/assets and truthful release evidence; do not rebuild them for documentation |
| Legacy API consumers | Owner-configured `hermes-node-management` matches exactly `nodes:read` + `nodes:write`, active365days until2027-10-27 13:38:02 Asia/Shanghai; actual MAC-WAN disposable create/edit/read/remove passes and original13 metadata is unchanged | Reviewed CLI writes may resume; no token/exec/update grant or key rotation. Other callers/runtime rewiring are not inferred from three reference edits; old30-day read-only key remains |
| Token recovery/edit/import | Owner accepts restrictions and requests no rotation; the remaining 13 UUIDs/tokens and Agent services are unchanged after the deliberate OVH node deletion | No rotation canary now; future rotation requires independent SSH/console and per-node consent |
| D12 custody/recovery | OC424 public recipient only; MAC-WAN receiver and online identity/recovery runner. Fresh current13-node pre/post ciphertext, isolated v1.7.13/v1.7.14 recovery and second online-host copies pass | Owner reports offline copy downloaded; independent-media restore is still unverified. Same-UID Hermes/root/Docker exposure remains explicit |
| Nightly schedule/retention | Enabled daily03:30UTC MAC-WAN timer, keep14 scheduled pairs; first actual daily trigger succeeds 2026-10-29 11:30-11:31 Asia/Shanghai with matching ciphertext/checksum, and the 11:34 health report identifies it | Actual incident delivery remains unverified; changed NAT source can block SSH. No independent-media or measured RPO claim. No old plaintext/manual/local rollback copy is deleted; deletion needs an explicit policy |
| OC424 authenticated acceptance | Owner elects self-run history/audit/API-key UI checks after deployment | **Closed in dsh's 2026-10-27 record**: the owner reported the post-deployment history/audit/API-key pages check out. This review does not repeat an owner session. Public shells, 401 responses and synthetic checks cannot substitute for it; acceptance does not extend to other surfaces or future releases |
| Rollback | Per-DB WAL-safe snapshots/compose + v1.7.13 image in `rollbacks/v1.7.14-20261029T031202Z-ae0f955d` | Patch rollback must keep current data and all 13 remaining nodes; never restore historical snapshots over the live fleet. Manual reclaim on v1.7.13 and older deletes theme history; v1.7.12 and older also deletes pool history, v1.7.10 and older panel history, v1.7.7 and older uptime. Preserve/verify post backup. Across v1.7->v1.6 freeze writers and restore paired old DB + binary, losing newer data/permissions |

**Hermes handoff:** the gateway is active again (PID922780/restart0); AgentPID976/
restart0 is unchanged. The metadata-only CLI stays checksum-matched on MAC-WAN;
three references explicitly forbid old API/token/install examples for this Panel.
Old-key writes are server-denied. Dedicated-key metadata/private storage and
actual disposable CLI writes pass; three reference overrides/marker now permit
that reviewed workflow without a service restart. This is not a complete task
inventory or owner UI acceptance; no key/session is recovered from SQLite,
backups, process args or Hermes history. Installation is owner-UI only; no token-read/exec/update grant is
silently added. See [`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md).

**Backup handoff (historical v1.7.12 release):** fresh v1.7.12 pre/post archives from the pinned installed OVH
receiver pass actual isolated MAC-WAN recovery with pinned v1.7.11/v1.7.12 images
for pre and v1.7.12 for post, both DBs,14 clients/current auth metadata and
cleanup. Post-backup uptime and panel rollups survive the v1.7.12 boot without reclaim.
Independent regular release
copies stay outside14-pair pruning; all older copies remain. Earlier13-client
release backups are historical; never use them to overwrite Pi's added node.
Owner-reported offline download is not independently exercised media recovery.
The former OVH daily timer and its first scheduled backup are historical;
MAC-WAN's replacement timer has now completed its first daily backup. Measured
SLO/RPO/RTO and independent-media restore remain separate. See
[`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md).

**Verification scope:** focused Go races/vet and synthetic browser/auth/restart
tests; required exact-source CI/publication; attested installed-Panel acceptance
on a still-trusted isolated host; encrypted recovery,
scoped forecast and live continuity. Prior SQL/live SLA evidence is reused.
Unchanged backup-health/query/scoped-key/report/queue/rotation/Hermes source
checks are reused, not broadly rerun locally. Only owned disposables are cleaned;
all backups,rollback images and shared caches remain. No fleet upgrade,owner
login,capacity/SLO or unattended decrypted recovery is claimed.

The active app goal is to design the next bounded objective and continue version
delivery; it does not imply that the entire roadmap is complete.
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
| The auto-upgrade archive excludes `metrics.db` by design | `database/dbcore/dbcore.go` | **Accepted**. The upgrade log already says the local metric store is excluded and left in place; the archive is not a full backup |
| Empty-country GeoIP results can hide recovery | `utils/geoip/geoip.go`; `docs/GEOIP-EMPTY-COUNTRY.md` | Fixed/deployed in v1.7.10: successful completely empty results expire after one hour, positive/partial results retain 48 hours, and overlapping lookups cannot extend or overwrite a cached result. No incident had been traced to this cosmetic issue |
| The "persistent data" warning is inverted under a custom docker data-root | `database/dbcore/data_mount.go`; `docs/DATA-MOUNT-WARNING.md` | Fixed/deployed in v1.7.12: anonymous-like paths are not tied to the default root and no mountinfo path is proof of persistence. OVH published-image custom-root cases and OC424 actual bind log pass; runtime configuration still determines persistence |
| Session tokens and the 2FA secret are stored unencrypted at rest | `docs/AUTH-HARDENING.md` §3 | Proposal only; needs a key-management decision before code |

## Numbers and correctness

| Item | Where | Why it is still here |
|---|---|---|
| `Presence.Coverage` is a span, not a window coverage | `internal/sla/availability.go`; `docs/SLA-COVERAGE.md` | The v1.7.19 UI labels the existing span ratio, shows first/last observed bucket starts and explains that node presence uses ping tasks rather than agent uptime. Backend values are unchanged. A separate window-coverage field, including its boundary, cadence and maintenance semantics, remains undecided; an in-place redefinition would move stored figures and is not done |
| `PriceTags` missing/invalid expiry | `frontend/src/components/PriceTags.tsx` | **Fixed/deployed v1.7.7**: neutral localized unset/unknown labels preserve real dates/tags; focused, real embedded-browser, publication and OC424 continuity pass |
| Dragging under a filter reorders only the visible subset | `frontend/src/pages/admin/index.tsx` | Needs a decision about what reordering a filtered list should mean |
| Uptime history / restart count | `docs/UPTIME-HISTORY.md` | Uptime history is deployed in v1.7.8. It records bucketed positive readings, not reboot events; trustworthy restart counting remains open |
| Panel self-metrics history | `docs/PANEL-HISTORY.md`; `docs/PANEL-POOL-HISTORY.md`; `docs/THEME-CACHE-HISTORY.md` | RSS, heap and goroutine history is deployed in v1.7.11; interval pool-wait history in v1.7.13; theme-cache history in v1.7.14. Restart counts and complete v1.8 remain open |
| One bad metric parameter fails a whole batch query | `web/rpc/jsonrpc/public.metric.go` | **Deployed in v1.7.22**: mixed batches keep valid keys and return per-key `errors`; an all-invalid batch is still rejected with the per-key failures in the error data |
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
| Encrypted backup follow-up | `docs/ENCRYPTED-BACKUPS.md`; `docs/MAC-WAN-BACKUP.md` | MAC-WAN's encrypted daily timer completes its first scheduled backup on 2026-10-29 and the subsequent health report identifies it. The 2026-10-28 scheduled success was on the former OVH receiver. Owner-reported offline download is not independent-media restore. Live incident delivery/stronger drill policy remain open; keep all old plaintext/manual rollback copies until explicit deletion approval |
| OIDC + 2FA makes SSO login fail-closed with no way for the browser to answer the challenge | `docs/ROADMAP.md` §F12 | Deliberately shipped as a gate, not half a state machine. Needs a one-time pending token plus a code prompt; production impact is zero while OIDC is off |
| `sso_id` survives a password change, and `DeleteAllSessions()` is global | `database/accounts/` | Policy questions now that the SSO path requires TOTP; recommendation was (b)+(c) in the audit report |
| A committed admin password from the SECRETS incident still has to be changed | `docs/SECRETS.md` incident 4 | An ops action on the live panel, not a repository change |
| A Cloudflare Worker and an `AAAA 100::` record for `monitor.orderly2233.org` remain | `docs/RETIRE-CF-PROBE.md` | Removable only in the Cloudflare dashboard |
| `topology/` is uncommitted and its README says the site was withdrawn the same day it went live | `topology/` (untracked) | Needs a decision: commit it (its secret scanner passes), or delete it. It is currently in neither README's index |
| The connection-pool counters are snapshots, not history | `web/api/admin/pprof.go`; `docs/PANEL-POOL-HISTORY.md` | **Delivered in v1.7.13:** bounded, session-only interval-delta history supplements the v1.6.20 instantaneous snapshots. It is not request latency, a cumulative total, an alert or full observability |

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
