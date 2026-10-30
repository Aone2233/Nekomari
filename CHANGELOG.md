# Changelog

Notable changes per release. Dates are UTC.

This fork is based on Komari `1.5.0-fix1` (commit `0ca87aa`, the last release before
upstream was archived); see [FORK.md](./FORK.md) for provenance. Releases below are
Nekomari's own.

## [Unreleased]

## [v1.7.15] - 2026-10-30

- **Published and deployed on OC424 at approximately 15:12 Asia/Shanghai.**
  Immutable `7c13e509767e59742196be61ec98973b785c68c8`; exact push CI41016555952
  8/8, release41017359338 10/10 and Docker41018148539 2/2. Linux checksums,
  tag/source/workflow attestations and both panel/agent image architectures
  verify; the published binary passes six synthetic forecast cases on MAC-WAN.
  OC424 upgrade9/9 and public27/27 pass; 13 clients retain their UUID/token
  digest and have fresh CPU/uptime reports. Read-only scoped forecast returns
  13 rows (10 linear, three insufficient). Owner-session UI is not claimed.
- Exclude backing rollup buckets that straddle a forecast's billing-cycle
  start, and retain the partial output bucket under the cycle-start label.
  Post-boundary data inside a discarded backing bucket remains unavailable;
  the label is not an exact sampling instant. Other metric queries keep their
  existing overlap behavior, and no stored history is rewritten.
- Refuse traffic forecasts when a required direction is absent, invalid,
  overflows a combined total or decreases between selected buckets. One-way
  limits use only their own direction. This avoids treating unknown values as
  zero or allowing one direction's growth to hide another's reset; bucket
  maxima can still hide resets within a bucket. An additive usage-validity flag
  prevents the page from displaying unknown usage as zero. Existing history is
  not rewritten; older API clients must inspect the new flag.
- Pin the two Playwright CI jobs to Ubuntu 24.04: the moving `ubuntu-latest`
  image reached Ubuntu 26.04, which pinned Playwright 1.58.0 cannot install
  browsers on. Test coverage remains enabled.
- Replace the relinquished OVH backup destination with MAC-WAN. After the
  October 30 direct-route failure, use a separately pinned MegaBox jump only
  for the existing restricted backup/health keys. The automatic health timer
  succeeds on the new route; the next daily backup trigger remains unverified.
  Fresh pre/post encrypted backups restore in isolation with the current 13
  clients; separate online ciphertext copies match. Independent-media restore,
  actual alert receipt and historical OVH disk sanitization remain unverified.
  See `docs/MAC-WAN-BACKUP.md` and `docs/DEPLOY-OC424.md`.

## [v1.7.14] - 2026-10-29

- **Published and deployed on OC424 at approximately 11:12 Asia/Shanghai.**
  Immutable `7f945619e94a4e25b82cb7860c61b8cf68b22027`, PR121; candidate
  CI40807334416 8/8, exact merge CI40808023833 8/8, release40808682926
  10/10 and Docker40809339436 2/2. Published Linux checksums/attestations,
  dual-architecture OCI identities and native image binaries verify. The
  published binary passes isolated MAC-WAN browser checks; OC424 upgrade9/9,
  public27/27, scoped forecast13rows and 13 fresh CPU/uptime reporters pass.
  Fresh encrypted pre-backup restores under v1.7.13/v1.7.14 and post-backup
  under v1.7.14; a retention-aware comparison of 12 metric series finds no
  missing in-retention or changed archived rows. Both archives have matching
  online-host copies. Owner-session UI and independent-media restore are not
  claimed. See `docs/DEPLOY-OC424.md`.
- Add session-only default-theme disk and memory-cache observations to panel
  history. Points show sampled bucket values, not continuous disk checks or
  precise transition times. Two independently retained definitions default to
  one day; older-image manual reclaim can delete them. See
  `docs/THEME-CACHE-HISTORY.md`.

## [v1.7.13] - 2026-10-29

- **Published and deployed on OC424 at approximately 09:46 Asia/Shanghai.**
  Immutable `9246bdc6770509edf6c938307455f14664d71024`, PR119; exact
  CI40722073534 8/8, same-tree main CI40723135889 8/8, release40800943975
  10/10 and Docker40801663163 2/2. Linux asset checksums/attestations and
  both OCI platform identities verify. Published-binary isolated browser
  checks run on MAC-WAN. OC424 upgrade9/9, public27/27, scoped forecast13rows
  and 13 fresh reporters pass. Fresh encrypted pre-backup restores under
  v1.7.12/v1.7.13 and post-backup under v1.7.13; a subsequent row-level
  comparison preserves all selected uptime, panel and pool history. The first
  post-boot total-count assertion failed and its cause was not reproduced;
  later drills pass without asserting it was resolved. Owner-session UI and
  independent-media restore remain unverified. See `docs/DEPLOY-OC424.md`.
- Add private, session-only main database and metric write/read pool wait
  histories to the existing Performance profiles page. A reading is the
  delta over the last validated interval, not a cumulative counter or a sum
  over the display bucket. Restart, pool replacement, missing/failed samples
  and absent read pools remain unknown. Six new metrics default to independent
  one-day retention; old-version manual reclaim can delete them. See
  `docs/PANEL-POOL-HISTORY.md`.

## [v1.7.12] - 2026-10-28

- **Published and deployed on OC424 at 23:10:34 Asia/Shanghai.** Immutable
  `b4591d64b6f2916963ffa4f6812cfa984ab397fb`, PR117; exact CI40708356223
  8/8, same-tree main CI40709676910 8/8, release40710985497 10/10 and
  Docker40712289115 2/2. Both Linux assets and OCI platforms verify. The
  published image passes four isolated OVH mount cases; OC424 upgrade9/9,
  public27/27, scoped forecast14rows and 14 fresh reporters pass. Fresh
  pre ciphertext restores under v1.7.11/v1.7.12, post under v1.7.12 with
  uptime and panel rollups retained. Independent-media and owner-session UI
  acceptance are not claimed. See `docs/DEPLOY-OC424.md`.
- Avoid claiming that a container data mount is persistent based solely on its
  mount path. Warn on anonymous-volume-like paths under custom Docker roots,
  and direct operators to verify that the configured mount is reused.

## [v1.7.11] - 2026-10-28

- **Published and deployed on OC424 at 22:19:47 Asia/Shanghai.** Immutable
  `8754747f8be7dd248b23b4305f77f00bd1d165b8`, PR115; exact CI40668605937
  8/8, same-tree merge CI40669574427 8/8, release40670453345 10/10 and
  Docker40671399212 2/2. Linux checksums/source attestations and both image
  architectures verify. The published binary passes isolated OVH browser,
  authorization, retention and restart checks. OC424 upgrade9/9, public27/27,
  scoped forecast14rows and 14 fresh CPU/uptime reporters pass. Fresh pre
  ciphertext restores under v1.7.10/v1.7.11; post restores under v1.7.11 on
  MAC-WAN with uptime and all three panel rollups retained. No owner UI login,
  independent-media restore or complete v1.8 acceptance is claimed.
- Add session-only, bounded panel RSS/heap/goroutine history to the existing
  Performance profiles page. Samples use independent configurable retention,
  preserve unknown gaps and do not expose panel readings through public node
  metrics. This is not a completed v1.8 observability release. See
  `docs/PANEL-HISTORY.md` for semantics and the older-image reclaim boundary.

## [v1.7.10] - 2026-10-28

- **Published and deployed on OC424 at 17:23:20 Asia/Shanghai.** Immutable
  `a2fd46a3205b0a8de7babced44880e4b9703b69f`, PR113; exact CI40660304364
  8/8, same-tree merge CI40661304807 8/8, release40661328099 10/10 and
  Docker40662206501 2/2. Checksums/source attestations and native image
  identity pass. OVH's isolated published Panel passes fake-upstream caching
  and reload checks. OC424 upgrade9/9, public26/26, scoped forecast14rows,
  both DB quick checks and14 fresh CPU/uptime reporters pass. Pre ciphertext
  recovers on MAC-WAN under v1.7.9/v1.7.10, post under v1.7.10; no production
  owner UI login or independent-media restore is claimed.
- **GeoIP empty-country cache.** Cache successful results with neither a
  country code nor name for one hour rather than 48 hours, allowing a later
  lookup to observe recovery while limiting repeated upstream traffic.
  Positive, partial and error results keep their existing behavior. Overlapping
  empty lookups cannot replace or extend another cached result. No provider,
  Agent, schema or permission change. See `docs/GEOIP-EMPTY-COUNTRY.md`.

## [v1.7.9] - 2026-10-28

- **Published and deployed on OC424 at 16:34:31 Asia/Shanghai.** Immutable
  `4e6c4d2b467b5815db656d1bf88746279cf5eee0`, PR111; exact CI40655268852
  8/8, identical-tree merge CI40656067694 8/8, release40656102364 10/10 and
  Docker40656768684 2/2. Both Linux checksums/source attestations and both
  image architectures verify. OVH's isolated published binary passes four
  embedded browser scenarios and guest/scoped-key denial. OC424 upgrade9/9,
  public26/26, scoped forecast14rows, 14 fresh CPU/uptime reporters and both
  database quick checks pass. Pre encrypted backup restores under v1.7.8 and
  v1.7.9; post backup restores under v1.7.9 on MAC-WAN with retained uptime
  rollups. Owner UI login and independent-media restore are not claimed.
- Show built-in theme cache disk state
  and whether its in-memory fallback was loaded in the existing administrator
  Performance profiles page. State is last-observed, not continuous filesystem
  verification; it is not a historical metric. No Agent, database, retention,
  credential or scoped-key change. See `docs/THEME-CACHE-DIAGNOSTICS.md`.
- Synchronize lazy fallback archive initialization so concurrent first requests
  cannot race on the in-memory map.

## [v1.7.8] - 2026-10-28

- **Published and deployed on OC424 at 15:42:01 Asia/Shanghai.** Immutable
  `75168d5b0fe7053b0d65eda71fffb7b0a0acc072`, PR109; exact CI40649551718
  8/8, identical-tree merge CI40650299520 8/8, release40650315129 10/10 and
  Docker40651006736 2/2. Release checksums/attestations, both image architectures
  and native image binaries verify. OVH's isolated published binary passes real
  report, restart, permission and desktop-light/mobile-dark embedded UI checks.
  OC424 upgrade9/9, public24/24 and scoped forecast14rows pass; 14 nodes have
  fresh CPU and uptime reports. Both DBs, identities, scopes and Agent services
  remain intact. New and old pinned images restore fresh pre and post encrypted
  backups on MAC-WAN; post-upgrade uptime rollups survive both boots without
  metric reclaim. Owner UI login and independent-media restore are not claimed.
- Record node uptime history from existing Agent reports and expose a same-shell
  admin view. Each
  historical bucket displays its last accepted sample; missing, zero and
  explicitly bad-quality values are unknown. Coarse buckets can hide drops and
  neither a drop nor its absence proves a reboot count. The new definition
  defaults to one day of retention without changing existing policies; there is
  no backfill. See `docs/UPTIME-HISTORY.md` for permissions and rollback limits.
- Default-theme node charts can optionally add uptime with last-value sampling;
  the new admin view also works when a third-party public theme is installed.
  Focused Agent-ingest, authorization, restart and embedded UI checks use only
  disposable synthetic nodes. No Agent/protocol, token or schema change.

## [v1.7.7] - 2026-10-28

- **Published and deployed on OC424 at 14:30:19 Asia/Shanghai.** Immutable
  `8e1dafa68e201830ec8df48b46eb60a2aa3b8332`, PR107; exact CI40642445800
  8/8, merge CI40643111878 8/8, release40643116847 10/10, Docker40643699302
  2/2. Both Linux checksums/source attestations and both image architectures
  verify. The attested published binary passes ten embedded-admin browser
  scenarios on OVH (five locales, desktop-light/mobile-dark). OC424 is
  healthy/restart0, upgrade9/9, public23/23 and scoped forecast14rows; all14
  UUIDs/tokens/reporters, schemas, grants/expiry, GeoIP and quiet backup-health
  state remain. Fresh pre ciphertext restores with pinned v1.7.6/v1.7.7 in
  8.25/9.42s and post ciphertext restores v1.7.7 in7.81s, all without `ANALYZE`.
  Agents/Hermes are not restarted. First actual scheduled nightly backup is
  verified separately at11:30:23 ->11:31:04; no independent-media/SLO claim.

### Fixed

- Show a neutral **No expiry set** badge for absent, null or blank paid-node
  expiry, and **Expiry unknown** for invalid dates. Do not fabricate a 30-day
  countdown, show `NaN` or imply lifetime service. Preserve valid countdowns,
  urgency colors, long-term dates, price/custom/IP tags and the shared theme.
  Add all five locale strings and regenerate the embedded frontend archive.
- Synchronize the AbortController server-cancellation test with actual handler
  arrival rather than a fixed 10 ms delay. Check deliberately delayed dispatch
  and a separately pre-aborted fetch; no fetch runtime behavior changes.
- Add focused missing/invalid/renewed-expiry fixtures and a real embedded-binary
  browser gate: five locales, desktop-light/mobile-dark, nullable API expiry,
  renewal and clearing. All data/accounts are synthetic and loopback-only.
  No schema, API, Agent, dependency, token or grant change. Publication and live
  deployment are recorded only after their separate gates pass.

## [v1.7.6] - 2026-10-27

- **Published and deployed on OC424 at 21:25:17 Asia/Shanghai.** Immutable
  `f8c6ae1161ce10a7493c454f1543c2cf556fbf44`, PR105; exact CI40506697515 8/8
  including actual MySQL/PostgreSQL recovery, release40507767971 10/10,
  Docker40511319737 2/2, Linux checksums/source attestations and dual-architecture
  image identity pass. Actual published-binary SQL install/recovery passes both
  backends, OVH synthetic SLA6/6 passes. OC424 healthy/restart0, upgrade9/9,
  public23/23, scoped forecast14rows and14/14 fresh CPU reporters; schemas,
  UUIDs/tokens, scopes/expiry, GeoIP and quiet backup-health state unchanged.
  Fresh pre ciphertext restores with pinned v1.7.5/v1.7.6 in8.41/9.74s; post
  restores v1.7.6 in8.71s, all without `ANALYZE`. These are isolated drill times,
  not RTO. Agents/Hermes remain unchanged. Rollback is prior v1.7.5 compose/image
  keeping current DBs; all backups/old rollback assets remain. See the runbook.

### Fixed

- Match the coarse-recovery query's three placeholders with exactly three
  arguments. v1.7.5 passed two unused interval arguments, which SQLite tolerated
  but MySQL and PostgreSQL rejected once a stored fine tier needed a recovery
  scan, preventing metric-store startup. Keep the series-driven SQLite plan.
- Use MySQL integer `DIV` for parent-bucket alignment; `/` retains a fractional
  result and misidentifies an existing sealed parent for a nonaligned child.
  Add actual MySQL/PostgreSQL recovery and sealed-parent regressions, including
  a dedicated bounded CI job with disposable databases. No schema, auth, Agent,
  protocol, frontend or dependency change.
- Reconcile the current deployment/recovery documentation with delivered
  v1.7.5; correct SLA coverage examples without changing its runtime contract.
  Publication and OC424 acceptance are recorded above after completion; no
  independent-media, first nightly, live incident-delivery or SLO claim is added.

## [v1.7.5] - 2026-10-27

- **Published and deployed on OC424.** Source `2d71cbe`; tag `v1.7.5` -> `c84c3cc`
  (the same runtime source plus this changelog). Exact CI40497025299 7/7,
  release40498907426 10/10 including both `verify the release deploys` jobs,
  Docker40499760158 2/2. Linux assets pass SHA256SUMS and provenance attestation;
  image labels report version 1.7.5 and revision `c84c3cc`. Post-switch:
  healthy/restart0, upgrade9/9, 14 clients and 2 API keys unchanged, Agent PID
  and restart count unchanged, task series resumed within two minutes. Released
  binaries were measured against a production-shaped restored store with no
  planner statistics: v1.7.5 opened it in 1,653 ms, v1.7.4 exceeded the 30s
  deadline on the same data. MAC-WAN pinned v1.7.5 recovery passes on both the
  fresh pre-upgrade and post-upgrade ciphertext; the pinned v1.7.4 image times
  out on the same pre-upgrade archive, so an off-host restore of that period
  needs the v1.7.5 image or the documented scratch `ANALYZE` workaround. See
  `docs/DEPLOY-OC424.md`.

### Fixed

- **Bound the boot-time coarse-rollup recovery scan.** The startup pass that
  rebuilds absent coarse parents constrained the child and the parent tier
  through a join on the resolutions table, which left the resolution id unknown
  to the planner as a constant. With stale statistics SQLite drove the scan from
  the resolution index and re-read every series' rows of that tier once per
  metric: 27x the necessary work on a production-shaped store, with a digest
  blob page behind every row. A restored archive is exactly the case with no
  statistics, which is why an isolated recovery boot could miss its 30s
  metric-store deadline until a scratch-only `ANALYZE` changed the plan. The
  statement now resolves the tier id once and binds it, dropping the
  resolutions join, and on SQLite forces the index that leads with `series_id`,
  so the plan is derived from the metric's series and no longer depends on
  statistics. Measured on a production-shaped fixture with stale statistics:
  18.2s -> 0.12s, with the same empty result. No schema, row, archive or
  production database is modified, no rollup is dropped, and absent parents are
  still created while sealed parents are still never overwritten. See
  `docs/METRIC-RECOVERY-STARTUP.md`; `Presence.Coverage` was clarified in the
  same window without changing its value (`docs/SLA-COVERAGE.md`).

## [v1.7.4] - 2026-10-27

- **Published and deployed on OC424.** Source `b0ea917`; exact CI40476853150
  7/7, release40478743592 10/10 (attempt 2), Docker40479934401 2/2.
  Linux checksums/attestations and OCI metadata verify; healthy/restart0,
  upgrade9/9, public23/23, forecast14rows and bounded authenticated SLA reads
  covering 14 nodes/58 tasks pass. Independent preserve-series comparison covers
  11 tasks across two nodes. All 14 UUIDs/tokens/fresh reporters,
  schemas, scopes/expiry, GeoIP, quiet backup-health state and Agent/Hermes services
  remain unchanged. Fresh encrypted pre/post actual old/new-image recovery passes;
  copies stay outside pruning; rollback is pinned v1.7.3 preserving current DBs.
- **SLA task-generation normalization.** Sort/deduplicate after grouping by
  entity/task, before loss, latency percentiles, incidents, presence and maintenance.
  Retain worst measured bucket means; empty placeholders cannot turn all-failed
  latency into 0ms. No stored-data rewrite, wire/schema, frontend, dependency, Agent,
  credential or coverage-definition change. Span-based coverage remains open.
- Focused regression/race tests and six installed synthetic-Panel groups pass
  locally, in required CI and with the attested binary on OVH. Initial release
  OIDC failure retries only failed publication jobs without rebuilding successful
  artifacts. Initial offline recovery startup deadlines resolve after scratch-only
  planner statistics refresh, preserving rollup rows/business schema; production
  DBs/archives/tool unchanged. Recurring health checks now observed; first nightly
  backup, live incident delivery, owner UI, independent media, SLOs and 200-node work
  remain separate. See `docs/SLA-GENERATIONS.md` and `docs/DEPLOY-OC424.md`.

## [v1.7.3] - 2026-10-27

- **Published and deployed on OC424.** Source `0fe0b37`; exact CI40471914791
  passes 7/7, release40472663920 10/10, Docker40473430351 2/2. Linux checksums,
  source attestations and OCI metadata verify. Healthy/restart0, upgrade9/9,
  public23/23 and current-key forecast read (14 rows) pass. Both DB schemas and
  integrity, 14 UUIDs/tokens, all 14 fresh reporters, key scopes/expiry, GeoIP
  and Agent/Hermes services remain unchanged. Fresh encrypted pre/post backups
  pass actual old/new-image MAC-WAN recovery; copies stay outside pruning.
  Rollback uses pinned v1.7.2 compose/image, preserving current DBs.
- **Encrypted backup freshness/reporting alerts.** OVH checks newest ciphertext
  checksum/header/time and pushes bounded metadata via a dedicated restricted
  SSH key, not a Panel grant. Linux opt-in watcher detects 26-hour backup or
  45-minute report age; persistent incident/recovery dedupe, 15-minute retries,
  quiet healthy checks and no overlapping notification queue. Single-attempt
  operational sends sanitize provider failures; Telegram uses 30-second/64KiB bounds.
- Nine Python offline groups, lifecycle/notification races/vet and six actual
  synthetic-Panel/fake-Telegram groups pass; the attested binary repeats that
  bounded acceptance on OVH. Installed service, initial automatic health-timer
  trigger, fresh report, quiet healthy state and environment opt-in verify.
  No production fake outage/test message, API/schema/Agent/token/scope/identity
  or nightly retention change. Later recurring checks, first nightly backup,
  live Telegram, owner UI, independent-media recovery, SLOs and 200-node work
  remain separate. See `docs/BACKUP-HEALTH.md`.

## [v1.7.2] - 2026-10-27

- **Published and deployed on OC424.** Source832b528, exactCI40464125160 7/7, release40464858298 10/10, Docker40465511917 2/2.
  Native/source/checksums/attestations verify; healthy/restart0,upgrade9/9,
  public23/23 and actual scoped-key forecast14rows pass. Both DB schemas and
  integrity,14 registered UUIDs/tokens (including Pi's owner-approved addition),
  all13 previously fresh reporters,key permissions/expiry,GeoIP settings and
  Agent services remain unchanged. Fresh encrypted pre/post archives pass actual
  MAC-WAN old/new-image recovery and are retained outside pruning; rollback is
  pinned v1.7.1 by image preserving current DBs.
- **Optional GeoIP safety/hot reload.** Guard enabled/provider types and
  settings-read errors with warning + EmptyProvider, not a background panic.
  Both settings keys reload; disabling replaces the provider and clears stale
  cache. Serialize constructors/database updates/reload, protect in-flight
  lookups until close, and prevent post-shutdown reload publication.
- Add lifecycle race tests and an eight-group actual installed synthetic-Panel
  harness in CI; the attested release binary passes the same bounded OVH
  acceptance. No third-party lookups or production settings toggles for tests.
  Reload is asynchronous and existing provider timeouts apply. No Agent,
  protocol,frontend,dependency,auth/schema,permission or token change; owner UI,
  independent-media recovery and200-node work remain separate. See
  `docs/GEOIP-RELOAD.md`.

## [v1.7.1] - 2026-10-27

- **Published and deployed on OC424.** Immutable source3b7e300, exactCI40458344881
  passes7/7, release40459059246 passes10/10 and Docker40459610734 passes2/2.
  Native arm64 binary/source/checksums/attestation match; healthy/restart0,
  upgrade9/9, public23/23 and actual scoped-key forecast read13/13 pass. Both DB
  schemas/integrity,13 registered clients,12 previously fresh reporting nodes,
  Agent tokens/services and key scopes/expiry remain unchanged. Fresh encrypted
  pre/post archives pass actual MAC-WAN old/new-image recovery and remain
  retained; patch rollback is pinned v1.7.0 by
  image, not a v1.6 auth downgrade. Owner UI acceptance remains separate.

- **Bounded traffic-forecast correctness patch.** Reject a counter reset in any
  chronological interval, even when later usage recovers above the initial
  total, and reject negative totals. Keep observed usage and an explained
  `insufficient` result without a misleading projection or warning.
- Use checked integer multiply/divide and rounding for projected byte growth;
  retain low bytes near the int64 limit and refuse overflow rather than wrap
  negative or silently cap. Compute limit headroom before float conversion;
  omit crossing dates outside duration/JSON time ranges instead of wrapping
  into the past. Ordinary and exact-cycle-end crossing behavior is retained.
- Record owner-configured Hermes key/private storage and actual disposable
  MAC-WAN create/edit/read/remove acceptance, preserving all13 original node
  metadata. Exactly `nodes:read` + `nodes:write`, owner-selected365days; no
  Agent-token, exec/update grant or rotation. Other callers and authenticated
  owner UI acceptance remain separate. No Agent/protocol/frontend/dependency,
  auth/schema or backup policy change; no fleet rollout or200-node work.

## [v1.7.0] - 2026-10-27

- **Security release and OC424 cutover.** Immutable tag/source `8eb711e`;
  exact-source CI 40446315166 passes7/7, release 40450095088 passes10/10 and
  Docker 40450707458 passes2/2. Linux checksums/provenance and both image
  architectures/source labels verify. OC424 switches at12:44:18 Asia/Shanghai:
  healthy/restart0, upgrade check 9/9 and public checks 23/23. The owner deliberately
  deletes one node before this release;13 registered UUIDs/tokens and all12
  previously fresh reporting nodes are preserved, not a claim of13/13online.
- **Live credential boundary.** One legacy global key migrates to 30-day
  `nodes:read`; its plaintext settings row is removed. Hermes is briefly stopped
  for the switch, three old references are redirected to the staged metadata
  client, and its gateway resumes. Node writes remain unavailable with the old
  key; owner-issued dedicated read/write key and actual live handoff/UI acceptance
  are pending. No existing Agent token is rotated or Agent service restarted.
- **Encrypted nightly backups.** OVH's daily 03:30 UTC systemd timer replaces only
  the plaintext cron row. Its installed sandboxed service successfully receives
  ciphertext; fresh pre/post-cutover archives pass actual isolated MAC-WAN
  old/new-image recovery with both DBs and 13 clients. Keep14encrypted pairs;
  release recovery copies are outside automatic retention. All existing plaintext,
  keys and local paired old-image/DB rollback assets remain. The owner reports
  downloading an offline identity copy; independent-media recovery is not claimed.
  This is not unattended decrypt scheduling, RPO/RTO/SLO proof or full roadmap exit.

The following entries retain the source-batch verification and boundaries at
preparation time. Their no-deployment statements describe those earlier batches;
the release/cutover record above supersedes their operational status.

- **Stage a metadata-only Hermes v1.7 consumer and private owner handoff.**
  Add a standard-library HTTPS CLI for node reads/create/edit/explicit removal,
  stable-v1.7+ guard, bounded allowlisted data and no token/install/exec or
  cookie/proxy/redirect/retry fallback. Owner-only hidden TTY input verifies a
  new scoped key before exclusive0600/0700 storage. Stage it on MAC-WAN without
  a production key or existing workflow/service changes; local/MAC-WAN17-group
  regressions and real synthetic HTTPS/Panel6-group checks pass. Live workflow
  pause/key issuance/handoff, independent offline recovery and gated v1.7
  deployment/owner acceptance remain. See `docs/HERMES-NODE-CLIENT.md`.

- **Establish owner-authorized MAC-WAN online backup custody.** Hermes runs on
  MAC-WAN; bounded gateway/reference inspection does not expose credentials or
  establish a complete consumer inventory. Generate the recovery identity there
  only, provision OC424 public recipients and an opt-in restricted OVH receiver,
  then verify an actual encrypted production canary and isolated pinned-v1.6.29
  restore on MAC-WAN. Source/restore scratch and the container are cleaned;
  Panel/Hermes/Agents are not restarted. Same-UID Hermes/root/Docker can reach
  the online identity; independent offline-copy recovery, nightly encryption/
  freshness/retention and the scoped-key handoff remain open. Existing nightly
  plaintext/rollback copies stay intact. See `docs/MAC-WAN-RECOVERY.md`.

- **Record the owner's v1.7 cutover choices and Hermes transition.** Hermes on
  another machine is the main known node-management caller. Prepare a dedicated
  read/write key, explicit create-response compatibility, brief write pause and
  owner-session issuance/secure handoff; token-read access is not granted by
  default. A focused REST/RPC regression exercises synthetic create/read/edit/
  remove and privileged-operation denials. The owner accepts token edit/import
  restrictions without rotation, approves the recommended backup architecture
  and chooses self-run UI acceptance. That source preparation did not establish
  the Hermes host/configuration or custody; the subsequent MAC-WAN checkpoint
  above supplies host/online recovery, not a scoped-key handoff or offline copy.
  No runtime, live API credentials, backup schedule or OC424 app update changes.

- **Encrypted off-host backup tooling (bounded v1.7 preparation).** Add opt-in
  native-age producer/receiver, WAL-safe local SQLite snapshots bound to captured
  metrics configuration, encrypted selected assets/manifest, exclusive publication
  and exact-prefix retention that preserves the just-received copy. Verification
  bounds paths/size and checks full authentication, hashes and both DBs; recovery
  targets only new directories or a local-only, digest-pinned, offline Docker
  container. Real-age/WAL regressions and synthetic restore drills run locally,
  on OVH and in CI. No production keys, schedule, plaintext retention, credentials,
  app deployment or 200-node work changes; custody/escrow and actual production
  recovery remain D12 release gates. See `docs/ENCRYPTED-BACKUPS.md`.

- **Session-only Agent-token rotation (bounded v1.7 preparation).** A dedicated
  REST/RPC/WebSocket operation and in-panel node dialog replace credentials with
  256-bit random tokens after session/optional existing TOTP authorization. Old
  HTTP/WebSocket admissions, queued work, presence, terminals and file operations
  are retired without deleting measurements; long polls do not block retirement.
  Generic edits and existing-node imports can no longer replace credentials
  outside this boundary, and Agent basic-info maps are limited to host facts.
  The UI requires independent SSH/console recovery consent, clears transient
  secrets on dismissal and never automatically replays uncertain writes. Includes
  transport/race/browser regressions and a refreshed embedded theme. Legacy
  plaintext Agent-token storage/read behavior remains; already running remote
  jobs are not undone. No automatic rotation, Agent/protocol/schema/dependency
  change, release or OC424 app update. See `docs/CLIENT-TOKEN-ROTATION.md`.

- **Scoped, expiring API keys (bounded v1.7 preparation).** Eight exact grants
  replace implicit administrator access. The server generates one-time tokens,
  stores only digests, supports immediate rotation/revocation, requires sessions
  and existing TOTP step-up for management, and records non-secret key UUIDs in
  audits. Both REST and RPC/WebSocket enforce grants; node reads strip agent
  credentials and node writes cannot change tokens/version/report fields. The
  sign-on card uses the existing panel theme and supports five languages.
  Legacy keys migrate once to 30-day read-only monitoring; settings/import cannot
  revive them. Operational helpers require explicit secret provisioning, never
  SQLite recovery. This intentional compatibility change is **not deployed**:
  live consumer inventory/replacements and paired-DB rollback are release gates.
  See `docs/API-KEYS.md`. No Agent/protocol/dependency or later roadmap feature
  is bundled in this batch.

- **Agent disk-footprint pruning**: `deploy/prune-agent-backups.sh` keeps one rollback
  binary per node and removes the rest; OC424's agent directory went from 231 MB to
  39 MB, and the fleet-wide cleanup reclaimed ~300 MB. It only matches
  `komari-agent-*`, so credential backups are untouched. Measurements and long-term
  bounds are in `docs/AGENT-FOOTPRINT.md`.

## [v1.6.29] - 2026-10-27

- **Compatible maintenance release while full v1.7 remains gated.** PR86
  backports only report/queue repairs and image source labels onto v1.6.28,
  through `codex/v1.6-maintenance`, not current main. Immutable tag points to
  exact tested `510f7d3`. Scoped API keys, session-only client-token rotation
  and encrypted-backup tools remain unreleased; no credential/schema migration,
  Agent/protocol/frontend/dependency change or automatic fleet action occurs.
- **Report and event-queue invariants.** Validate every report field before
  clamping only RAM/Swap/disk used above total. Other invalid inputs still
  reject; accepted anomaly episodes warn without logging report payloads.
  Successful removal and the existing idle scheduler retire event queues,
  isolate/wake old-generation polls and release discarded payloads without
  changing newest-128 limits, command TTLs, acknowledgements or reconnect delivery.
- **Published images identify the release source.** Verify the resolved release
  tag against checkout HEAD, and explicitly label both images with that commit
  instead of the default-branch `workflow_run` SHA. Real-Git regressions cover
  distinct commits, annotated tags and mismatch/missing/ref refusals. Actual
  anonymous registry checks pass for Panel/Agent on amd64/arm64: revision510f7d3,
  OCI version `1.6.29`. Existing tag/`latest` policy and v1.6.28 artifacts stay unchanged.
- **OC424 update and real recovery.** Exact-source CI7/7, release10/10 including
  native amd64/arm64 deployment verification, Docker2/2 and upgrade check 9/9
  pass. The native/live arm64 binary matches verified checksums/provenance.
  OVH retains a fresh paired backup after actual isolated old-image recovery;
  local image-only rollback assets are prepared, not exercised live. Both DB
  schemas/integrity,14registered clients and all13 previously fresh reporting
  nodes are preserved; both hosts' Agents stay unchanged. Public shell/assets
  and unauthenticated denial checks pass; authenticated owner acceptance remains
  pending. See `docs/DEPLOY-OC424.md` and `docs/V1.7-CUTOVER-READINESS.md` for
  recovery, API-consumer/custody/acceptance gates. No plaintext backup deletion,
  broad cache pruning or200-node work occurs.

## [v1.6.28] - 2026-10-26

- **Bound legacy history and audit-log queries.** The first bounded v1.7 source
  batch ships early as a compatible 1.6 patch on exact commit `502a269` (PR78).
  `common:getRecords` validates its 366-day window before duration conversion
  and shares historical admission, deadlines, row/byte budgets and cancellation
  across load/ping adapters. Guests cannot read hidden/deleted nodes' retained
  history; administrators retain historical access. Audit pages are limited to
  1-1000 rows with strict binding, checked offsets, no fetch beyond total and
  SQL cancellation. Defaults, response shapes, filters and CSV remain compatible.
- **Panel-only release and OC424 delivery.** Exact-source CI7/7, release10/10
  (native amd64/arm64 deploy verification), both Docker jobs and OC424 upgrade
  check 9/9 pass. A verified paired backup is retained on OVH after an isolated
  old-image restore drill. Public assets/private-site denial and both DB
  integrity/schema checks pass; authenticated live history/audit owner acceptance
  remains pending. See `docs/DEPLOY-OC424.md` for immutable digests and rollback.
- No Agent/protocol, frontend source, dependency, schema or credential behavior
  change from v1.6.27. In-panel navigation is retained. Scoped API keys,
  report/queue changes, client-token rotation, backup encryption and 200-node
  work are **not** included; full v1.7 exit criteria remain open.

## [v1.6.27] — 2026-10-26

- **Keep operations tools inside the admin panel.** Status/SLA, bulk edit,
  maintenance windows, configuration export/import and traffic forecast now use
  admin child routes instead of opening new tabs. They share the panel's active
  light/dark appearance, accent, sidebar and topbar. The node-list bulk action
  retains selected UUIDs, and SLA window links preserve browser history, query
  parameters and hashes. Existing standalone links remain available. This is
  a frontend-only patch on v1.6.26; backend, schemas and Agents are unchanged.
  See `docs/ADMIN-TOOLS-NAVIGATION.md`.

## [v1.6.26] - 2026-10-24

- **Build with the patched Go 1.27.3 compiler.** CI and release builds use the
  fix for GO-2026-6809 in `crypto/x509`. Module minimums, dependencies, application
  source, credential/database formats and frontend source are unchanged. This
  compiler-only patch does not require a fleet-wide Agent update. See
  `docs/COMPILER-SECURITY-1273.md` for the retained candidate evidence.

## [v1.6.25] — 2026-10-20

- **Ping summaries show fractions.** The public ping records, the node-card `pingStat` and the
  frontend formatting now use the `value_us` precision from v1.6.24: `records[].value`,
  `basic_info`/tasks min/max/avg and the node-card latest/avg/min/max are `float64`, and the UI
  renders below 1 ms as `<1 ms` instead of `0 ms`.
- **`admin:getClientToken` is a sensitive operation.** A normal session now needs the sensitive
  step-up to read an agent token; API keys keep their existing exemption by design. API-key actions
  are attributed to `api_key` in the audit log instead of the all-zero placeholder UUID.
- **The installer supports OpenRC**, and `upgrade-agent.sh` now uses `--version` for idempotence
  (SHA-256 fallback for old agents), restores file capabilities, restarts under systemd or OpenRC
  (including `zap` for crashed services), verifies the running inode, and prunes rollback binaries
  to the current + newest copy. Both scripts have offline test harnesses in CI.
- **The protocol copies are guarded by a contract test** comparing the root and agent
  `protocol/v2` structures and the ping payload keys, so a one-sided field change fails CI.
- Operations: the OVH backup was restored into a scratch panel and served `/api/public` 200 on
  v1.6.24; the nightly stream now also carries the three upgrade archives (~160 MB per backup).

## [v1.6.24] — 2026-10-20

Ping gets sub-millisecond precision without a new metric or series.

- Agent measures ICMP/TCP/HTTP in microseconds, keeps the legacy `value` in whole milliseconds,
  and adds `value_us`. Old panels ignore the new field; old agents keep working.
- The panel stores the fractional millisecond value in the **same** `ping.latency_ms` and
  `ping.success_latency_ms` points, with the same tags, so the number of points, series and
  buckets does not change. The only storage change is the JSON field on the wire (about 20 bytes
  per ping result; roughly 0.1–0.2 MB/day for a five-task node) — the metric store already stores
  float64 values.
- Reading a point back reconstructs `value_us`, so a 401 µs probe is no longer displayed as 0 ms.
- Compatibility: a missing `value_us` falls back to the old integer milliseconds.

## [v1.6.23] — 2026-10-20

Agent diagnostics and update semantics; panel unchanged.

- **`komari-agent --version` prints the version and exits.** It was an unknown flag, so every
  upgrade check had to parse the journal for the startup line instead.
- **`--disable-auto-update` is now described accurately.** It disables the agent's *periodic*
  self-update; panel-requested `agent.update` events still work. The v1.6.22 rollout had to be
  manual because the fleet was on pre-H6 binaries that did not understand that event, not because
  the flag blocks it. The flag's help text and the startup log say so.

## [v1.6.22] — 2026-10-20

Agent-side fixes; the panel is unchanged.

- **The traffic plausibility ceiling follows the real link speed.** It was hard-coded at 1 Gbps, so a
  10G/25G node's busy intervals were silently discarded. It now reads
  `/sys/class/net/<iface>/speed` (Mbps) and uses that rate with no burst headroom; an unknown speed
  keeps the old 1 Gbps assumption and its 3x headroom, so the 59.5 GB restart phantom is still
  rejected. Rejection logs name the measured/assumed rate and its source.
- **The NIC whitelist is refreshed before every sample** instead of being a startup snapshot. A
  bridge, container veth or hot-plugged NIC that appears later is now counted; a failed or empty
  enumeration keeps the previous list, and stale counters for removed NICs are pruned.
- **Explicit `--include-nics` can select `lo`, `br0`, `veth*` and `tap*`.** Order is now explicit
  exclude > explicit include > default virtual/loopback exclusion > default include; without an
  include list the default behaviour is unchanged.
- Agent tests cover measured 10G/2.5G ceilings, unknown-speed fallback, provider refresh/failure,
  include/exclude precedence and the existing delta-guard cases; `go build ./...` and `-race` pass
  in the agent module.

## [v1.6.21] — 2026-10-20

- **Audit logs can be filtered by message type and exported as CSV.** `/api/admin/logs` now forwards
  `msg_type` (the RPC already supported it), the page has a type selector that resets paging, and
  the export follows the active filter: 1000 rows per request, capped at 10,000, UTF-8 BOM and
  CRLF for Excel, with quotes/commas/newlines escaped. A truncated export says so; the existing
  table, paging and detail dialog are unchanged.
- **Windows frontend builds are reproducible too.** Two builds with a project-local
  `TEMP`/`TMP` produced the same 819 files (hash diff 0). The system temp directory can make
  esbuild fail to delete its temp file ("Access is denied"); that is a local environment issue,
  documented in `OPEN-WORK.md`, not a source change.
- **Nightly off-host backup to OVH.** The OVH KS-LE-B pulls ~98 MB/day (SQLite backups of
  `komari.db` and `metrics.db`, compose file, metadata) over a restricted forced-command key and
  keeps 14 archives; 366 GB of the 410 GB RAID1 was available at setup. See
  `docs/DEPLOY-OC424.md` for the storage table, restore steps and the at-rest encryption caveat.

## [v1.6.20] — 2026-10-20

The panel-upgrade hardening batch, plus the last two visibility gaps.

- **Upgrade backups are pruned daily**, not only when the next upgrade happens. A scheduled job keeps
  the three newest `upgrade-*.zip` files and logs what it removed; it never deletes `pre-restore-*`
  snapshots or `.partial` files.
- **Every upgrade now has one read-only check.** `deploy/panel-upgrade-check.sh` verifies the
  expected version and commit hash, container health and restart count, `/api/public` and the home
  document, `PRAGMA quick_check` (read-only), the upgrade archives, and recent ERROR lines. It has
  explicit SKIPs for a host without docker/sqlite3, exits 2 on bad configuration, and never writes.
  Its offline harness (69 assertions, including a byte-for-byte check that `DATA_DIR` is unchanged)
  runs in CI. `docs/DEPLOY-OC424.md` now starts with this command for every rollout.
- **pprof shows the metric store's connection pools**: `runtime.metrics_db` has the write pool and,
  when a dedicated read pool exists, a `read` sub-object; the page renders both and highlights
  waits. Old responses still parse.
- **Selecting nodes in the admin table opens the bulk editor with that selection** instead of only
  changing a count. The page is the existing `/standalone/bulk/bulk.html`; the selection is kept
  across polling and filtering.
- A source-level regression test pins the reproducible-build contract (`__BUILD_TIME__` resolution
  and the `export-subst` stamp) so the wall-clock build time cannot return unnoticed.
- **The default-theme cache retries a briefly locked Windows rename** instead of falling back to the
  33 MiB in-memory archive. CI hit the sharing violation during a prewarm; the retry keeps the disk
  cache, which is the reason it exists.

## [v1.6.19] — 2026-10-20

The last follow-up batch from the 2026-10-18 review. Panel-side only: `agent/**/*.go`
and `protocol/` are untouched.

- **A cleared H6 rollout target is now delivered to a node that was offline.** The panel stores a
  pending-clear marker, re-sends an empty target once when the node connects, and clears the marker
  only after the write succeeds. `agentVersions` exposes `pending_clear` when set.
- **Releases carry an SBOM and build provenance.** The release job now emits `sbom.spdx.json`
  (included in SHA256SUMS and the Release assets) and attests the built binaries with
  `actions/attest-build-provenance`; attestation runs before the Release is created, so a failure
  blocks publication. See `docs/RELEASING.md`.
- **The pprof page reports process and database-pool data**: RSS (Linux), uptime, and the main
  database pool's open/in-use/idle/wait counters. A non-zero wait count is highlighted.
- **Frontend builds are reproducible.** `__BUILD_TIME__` used to be the wall clock and was inlined
  into the bundle, so every build produced different chunk hashes. It now comes from
  `VITE_BUILD_TIME`, `SOURCE_DATE_EPOCH`, a stamped git-archive commit time, or the checkout's
  commit time; two builds of the same archive are byte-identical on Linux.
- **The authenticated deploy check handles root-served themes.** It judges an asset by being a
  same-origin JavaScript or stylesheet rather than requiring `/assets/` or `/themes/`; the CRT theme
  serves `/app.js` and `/style.css`. A production run against OC424 now passes 3/3.

## [v1.6.18] — 2026-10-20

The maintenance and verification follow-up. Panel-side only: `agent/**/*.go` and
`protocol/` are untouched.

- **Maintenance windows are excluded from SLA.** A sample bucket that overlaps a window is removed
  from availability, latency percentiles and outage incidents; the coverage denominator drops the
  same slots, a fully covered window reports "no data" instead of 100%, and an outage is not
  merged across a window. `NodeReport.maintenance_excluded_seconds` reports how much of the
  report window was excluded. The notifier's cached window list is reused, so this adds no
  database query path.
- **The main database is checked daily.** A scheduled `PRAGMA quick_check` plus the
  authentication-index check runs every 24 hours and logs its result; it never blocks serving.
  This complements the startup check (advisory since v1.6.17).
- **Deployment verification can now check an authenticated panel and a loaded theme.**
  `deploy/panel-auth-smoke.py` uses `KOMARI_BASE_URL` and `KOMARI_API_KEY` to call
  `admin:getDatabaseSize` (a JSON-RPC error is not success) and to fetch the bundle the document
  references, so a theme that did not load is a failure. Missing variables are a SKIP. Its offline
  decision table runs in CI's `deploy-scripts` job.
- The SLA page shows `maintenance_excluded_seconds` beside a node's figures, so a lower number is
  explained rather than silently surprising.

## [v1.6.17] — 2026-10-19

Hotfix for the startup integrity gate introduced in v1.6.16.

- **A failed `PRAGMA quick_check` no longer refuses to start.** The reference database has a benign
  freelist accounting mismatch (`Freelist: size is 20 but should be 21`) that SQLite reports while
  every query keeps working. v1.6.16 turned that finding into a restart loop on OC424 and was rolled
  back to v1.6.15. The finding is now logged as Error with the guidance to back up and run
  `PRAGMA integrity_check`; `KOMARI_STRICT_DB_CHECK=1` restores the fail-fast behaviour for
  operators who want it. Missing authentication indexes remain warnings.
- Everything else in v1.6.16 is unchanged and included here.

## [v1.6.16] — 2026-10-19

**Published but not deployed**: the startup integrity gate made it restart-loop on the reference
database (see v1.6.17). The rest of the batch is included in v1.6.17.

The operational and correctness follow-up to the review's first three batches.
Panel-side only: `agent/**/*.go` and `protocol/` are untouched, so the fleet
does not move.

- **A version upgrade no longer spends ~90 seconds and 1.6 GB archiving temporary data.** The
  pre-upgrade snapshot excluded only `metrics.db` and `data/backup`, so it copied
  `db-maintenance/` (2.4 GB on the reference host), `theme-backups/`, `theme-assets-backup-*`
  and `.uploading`. Those are now excluded. The archive keeps the three newest `upgrade-*.zip`
  files and leaves `.partial` and `pre-restore-*.zip` alone; the first upgrade also prunes the
  5.5 GB already accumulated in `data/backup`.
- **The main database is checked at startup.** `PRAGMA quick_check` runs after AutoMigrate, and
  the columns authentication relies on (`sessions.session`, `clients.token`, `users.username`,
  `users.uuid`) are checked through `sqlite_master`/`PRAGMA index_info`; a missing index is
  warned about with the consequence named (a full table scan). A failed `quick_check` stops
  startup rather than serving a damaged database.
- **The default theme's disk cache is warmed at startup** when the active theme is the built-in
  one, so the first visitor no longer pays the ~300 ms extraction. Installed themes skip it: they
  are served from `data/theme/<short>/`, not from the embedded archive.
- **H6 rollout targets now survive a panel restart.** The target is persisted per node
  (`clients.target_version`), written for every selected node including offline and already-at-
  target ones, reported as `saved` instead of `offline`, exposed through `agentVersions`, and
  re-sent when a v2 agent connects. A dry run never writes. Clearing a target while a node is
  offline is not re-sent as an explicit clear (the agent keeps its in-memory target until it
  restarts); that edge is recorded in `OPEN-WORK.md`.
- **Maintenance windows appear in node status.** `common:getNodesLatestStatus` carries an optional
  `maintenance {name, reason, until}` object while a node is inside a window, and the dashboard
  card renders an amber maintenance badge instead of treating the node as plainly offline. SLA
  still counts window'ed samples as normal operation; that half remains in `OPEN-WORK.md`.
- The embedded archive was rebuilt (1,075 files, 32.93 MiB → 5.09 MiB).

## [v1.6.15] — 2026-10-19

The 2026-10-18 improvement review, in three batches. Panel-side only:
`agent/**/*.go` and `protocol/` are untouched, so the fleet does not move.

The low-risk half of the 2026-10-18 improvement review
([`docs/audits/2026-10-18/SUMMARY.md`](./docs/audits/2026-10-18/SUMMARY.md)). Panel-side only:
`agent/**/*.go` and `protocol/` are untouched, so the fleet does not move.

- **Two fleet-impacting RPCs were outside the sensitive-operation gate.** `admin:exportConfig` with
  `include_secrets=true` exported every agent token, and `admin:rollAgentUpdate` could make the whole fleet
  install an arbitrary release; neither was marked sensitive, while `admin:exec` was. A stolen admin session
  on an account with 2FA could do both without a second factor. Credential export now has its own method,
  `admin:exportConfigSecrets`, registered with `rpc.MarkSensitive`; the safe export refuses
  `include_secrets` instead of silently dropping it, and the refusal names the method that can do it. The
  reviewed allowlist grows to five methods and a test walks the list itself, so a method that is marked but
  not reviewed fails. API keys remain exempt, as they are for every sensitive method (`OPEN-WORK.md`).
- **Five delivered pages had no entrance.** The SLA report, bulk edit, maintenance windows, configuration
  import/export and traffic forecast are standalone documents under `/standalone/<page>/<page>.html` — they
  cannot be routes (a theme replaces that router, roadmap H0) and no theme can know they exist. The panel's
  operations menu now lists all five (new tab), and `script/menu-reachability.test.mjs` asserts every page in
  the build table has a menu entry with the build's exact path, a valid icon and `newTab`.
- **A maintenance window only silenced offline alerts.** `sendLoadNotification` — the shared exit for
  host-metric and ping rules — had no window check, so a planned reboot still woke the operator for the ping
  loss it caused; roadmap H3's acceptance names offline and ping. Alerts for clients inside a window are now
  dropped (not deferred: a point threshold is about a moment, and a late "CPU was high" is not actionable),
  logged with the window's name, and collection is untouched.
- **Static assets were served uncompressed, and the embedded theme stored the admin twice.** `serveAsset`
  now gzips compressible assets above 1 KiB when the client offers it (`Vary: Accept-Encoding`, no
  `Content-Length`, Range stays identity, 304 stays bodyless) and the panel's own `/admin` assets use the
  same path. On a self-hosted panel without a compressing proxy this is the difference between ~1.6 MiB and
  ~0.4 MiB on a first visit; through Cloudflare nothing changes because the edge already compresses. The
  embedded archive contained `standalone/admin/**` and `admin/**` byte-for-byte identical (~250 files,
  8.3 MiB); loading keeps one copy and `/standalone/admin/...` falls back to it, and only when the bytes
  are equal, so a future divergence degrades to "no deduplication" rather than serving the wrong file.
- **Idle keep-alive connections were never retired.** The server set `ReadHeaderTimeout` but not
  `IdleTimeout`, and Go's zero value falls back to `ReadTimeout` — also zero — so an idle connection could
  hold a goroutine and its buffers for the process's lifetime. Both the normal and the guided servers now
  share one constructor with a 120 s idle timeout; `WriteTimeout` stays unset because it would cut downloads
  and terminal streams.
- **The public dashboard downloaded recharts to render a hover-only chart.** The mini ping chart is behind a
  three-second hover, but a static import put recharts (363 KiB raw / ~107 KiB gzipped) in the first visit.
  The float and the chart are both lazy now; the float's own chunk is 2.2 KiB and recharts is fetched only
  when the popover opens. The service worker still precaches the chart chunk under the existing policy
  (ROADMAP B1), so this is a first-paint and parse saving, not a reduction in total bytes over a visit.
- **The node list was refetched every five seconds.** Its fields (name, price, limits, the online set)
  barely change, and the two-second live status has its own path; the interval is now 60 s, with the
  existing visibility pause unchanged. Every open panel makes 12× fewer list requests per minute.
- **The configuration export asks for the second factor where it needs one.** The export page collects a
  one-time password when "include secrets" is checked and calls the new sensitive method; an account
  without 2FA still exports with the field empty, because the server is the authority on whether a factor
  is enrolled.

The embedded default theme archive (`web/public/defaultTheme/dist.tar.zst`) was rebuilt from this batch's
frontend build, so the shipped binary serves the new menu, the lazy chart and the 2FA prompt.

The second batch of the same review:

- **The entry no longer ships four languages nobody asked for.** `i18n/config.ts` imports English
  statically and loads the other four with `import()`; the seven entries await `initI18n()` before
  rendering, so the first frame is in the detected language rather than keys. The entry bundle fell
  from 804 KiB raw / 269 KiB gzip to 508 KiB / 165 KiB (gzip −104 KiB); the four locale chunks
  (24–27 KiB gzip each) are fetched when the language is actually selected. They are also excluded
  from the service-worker precache: precaching them would download the same ~100 KiB in the
  background and cancel the saving. Offline, a language that was never loaded falls back to the
  current one instead of showing an empty screen. The five standalone pages do not benefit: their
  builds inline dynamic imports by design, so each still contains every language.
- **Ingest no longer re-reads and re-hashes on every batch.** `pkg/metric` keeps an immutable
  snapshot of the definitions table (invalidated by every definition write; maintenance paths read
  the committed table directly) and a bounded content-addressed cache for tag fingerprints.
  `Point.normalized` no longer allocates empty maps on this path. A 180-point batch with no tags
  fell from 165.5 KiB / 2,692 allocations to 55.0 KiB / 153 (−67% bytes, −94% allocations); with
  tags, 203 KiB / 3,563 to 108 KiB / 2,325. The benchmark is `BenchmarkWriteBatchIngest`.
- **RPC2 reconnect backs off instead of giving up.** A fixed 3 s retry that stopped after five
  attempts left every call on the HTTP fallback once a panel restart lasted longer than 15 s. The
  client now retries indefinitely by default (`maxReconnectAttempts: 0`), with exponential backoff
  capped at 30 s and ±20% jitter, and waits for the tab to be visible rather than retrying in the
  background. A positive attempt cap is still honoured for callers that want one.
- The embedded archive was rebuilt again for this batch (1,083 files, 32.96 MiB → 5.07 MiB), and a
  v1.6.14 + batch A arm64 build was exercised on OC424 against a copy of the production data: the
  theme entry serves 292,808 B as 92,984 B gzip, the admin entry 803,226 B as 277,776 B, conditional
  requests still answer 304 without a body, range requests still answer 206 identity, all five
  standalone pages answer 200 and the served admin chunk carries all five menu paths.

The third batch of the same review:

- **The default theme no longer lives in memory.** The embedded archive used to be unpacked into a
  `map[string][]byte` during `init` — 32.96 MiB of files, ~33 MB resident, ~70 MB at startup peak.
  The first request for a default-theme asset now streams the archive to
  `data/.theme-cache/<sha256-of-archive>/` (staging directory, entry/byte-count verification, then a
  `\.complete` marker and rename) and serving reads files from disk. A complete cache means a miss is
  a real 404, so a stale chunk does not unpack the whole archive again. If the directory cannot be
  written, the loader falls back to the lazy in-memory copy; `KOMARI_THEME_CACHE=0` forces that path.
  The cost is about 300 ms on the first default-theme request of a fresh data directory; the tests
  compare every extracted file with the archive and assert the in-memory map stays nil.
- **Plugin response hooks are bounded at 2 MiB and no longer leave a timer behind.** The buffer cap
  was 32 MiB (and the body was copied once more for JavaScript), so one hooked large response could
  pin ~64 MB; the cap is now 2 MiB and larger responses take the existing passthrough. The per-request
  `time.After` became a stopped `time.NewTimer`.
- **A soft memory limit follows the container's own ceiling.** `KOMARI_MEMORY_LIMIT` (bytes or
  `512MiB`/`1GiB`, `off`/`0` to disable) wins, an existing `GOMEMLIMIT` is respected, otherwise cgroup
  v2 `memory.max` or cgroup v1 `memory.limit_in_bytes` is used. The installed limit is 90% of the
  ceiling with a 64 MiB floor, logged once; an unparseable override warns and falls through.
- **Read admission now has a byte budget as well as a row budget.** Each row is charged at the
  measured 534 B, and a public query is capped at 96 MiB in addition to 250,000 rows; four concurrent
  chart slots therefore have a ~384 MiB envelope instead of ~533 MiB.
- **The coarse rollup tree was measured instead of estimated.** A 24 h simulation (injectable clock)
  puts it at ~30.5 KiB/series (200-series run), i.e. ~5.5 MiB for 9 nodes × 21 series and ~61 MiB for
  100 nodes — lower than the 55–70 KiB estimate because a 24 h parent keeps ~23 one-hour children,
  not 24–48, and the live digests are smaller than their encoded form. It remains linear, has no leak,
  and a test fails above 80 KiB/series.
- **PWA updates ask instead of replacing the page.** `registerType` is now `"prompt"`; a banner offers
  Update / Later and reloads when the waiting worker takes over, with an hourly `registration.update()`
  for long-lived tabs. The prompt moved out of the shared `App` into the main entry, so the admin and
  standalone builds no longer import the PWA virtual module. With prompt registration there is no
  generated `registerSW.js`; the archive contract now asserts the registration code exists in the
  built JavaScript instead.
- The embedded archive was rebuilt for this batch (1,075 files, 32.93 MiB → 5.09 MiB), and the live
  OC424 panel was switched back to the built-in default theme at the owner's request (recorded in
  `docs/DEPLOY-OC424.md`; the v1.6.14 image was not replaced).

## [v1.6.13] — 2026-10-17

- Keep ChatGPT/Claude region-only results `unknown`: observing an exit country does
  not establish the service's country policy or actual IP availability. YouTube
  consent/login pages without expected markers also stay unknown instead of green.
- This release includes agent changes. Upgrade the panel first, then canary the
  matching agent before planning a fleet rollout; optional route targets remain off
  unless explicitly configured by the operator.

- Add optional [Nekomari Mint](https://github.com/Aone2233/komari-theme-nekomari-mint)
  theme support without replacing the default theme or admin panel: a visibility-gated
  `public:getNodeInsights` read endpoint provides stale-aware probe evidence and
  redacted IP network metadata, with no raw egress IP or per-hop trace disclosure.
- Preserve IPv4/IPv6 family in public ping history and expose quality-checked
  billing-cycle traffic plus nullable host TCP retransmission in latest status.
- Agent: collect Linux TCP counter deltas and support explicitly configured,
  low-frequency public route targets. Route probing remains disabled by default;
  ASN labels are observations, not route or unlock guarantees. See
  [`docs/MINT-THEME.md`](./docs/MINT-THEME.md) for installation, limits and privacy.
- These changes include agent source changes; adopting the full extension needs a
  separately planned server/agent rollout. Installing the theme ZIP alone does not
  upgrade either component.

## [v1.6.12] — 2026-10-17

The rest of the 2026-10-13 audit batch
([`docs/IMPROVEMENT-AUDIT-2026-10-13.md`](./docs/IMPROVEMENT-AUDIT-2026-10-13.md)) — its **section A is not
here**: those six changes shipped as `v1.6.11` below, released on their own because two of them are
anonymous-reachable. Everything below is panel-side only: `agent/**/*.go` and `protocol/` are untouched, so
the fleet does not move.

The work is 25 themed commits rather than one lump, so a later bisect lands on a subject. It was planned as
several releases, and it is one because of the gate `v1.6.11` introduced: a tag is refused unless the commit it
points at has a green `ci` run, and CI runs once per push at the branch tip — the intermediate commits were
never tips of `main`, so tagging one of them is a refusal, not a split. The split applies from here on: each
batch gets its own push and therefore its own tag.

- **Fixed: the quota warning and the traffic forecast read the kernel's since-boot counter** (`net.total.*`) as
  if it were the billing cycle's usage, while the plan limit is a per-cycle quota. Both now read
  `traffic.up` / `traffic.down`. A node that rebooted mid-cycle restarted its projection from a fraction of its
  real usage. The series is pinned by a test that hands the reader both series at once.
- **Fixed: the plugin version gate failed open on both ends.** An unparseable server version made the panel and
  the server *both* allow every constrained plugin — silently disabling the gate — while `FORK.md` described the
  opposite. Both now refuse and say why. `build.sh` no longer stamps `git describe --tags --always`
  (`v1.6.10-3-g4e7d389`) into a local build, and the release workflow refuses a tag that is not plain `vX.Y.Z`.
- **Fixed: `build.sh` built the agent without a version**, leaving `agent/update.CurrentVersion` at `0.0.1` —
  the value the agent uses to decide whether a newer release exists, so a locally built agent would replace
  itself with the official asset and `os.Exit(42)`. `agent/build_all.sh` / `.ps1` injected that version into the
  **pre-fork module path**, where `-X` is a silent no-op; both are corrected (verified by building a binary and
  grepping for the version).
- **Fixed: the release workflow had no CI gate.** It triggered on any `v*` tag and ran no tests, so a tag on a
  commit whose `ci` run was failing published a release — the incident `RELEASING.md` documents as having
  happened once. A `gate` job now requires a green `ci` conclusion on the resolved commit before anything is
  built. The checksum step no longer pipes through `tee` (which swallowed `sha256sum`'s failure and could
  publish an incomplete `SHA256SUMS.txt`, stopping every agent's self-update), and a manually dispatched release
  now gets its image: `workflow_run` carries the *branch* in `head_branch` for a dispatch, which the
  tag-resolution step used to reject.
- **Fixed: release verification only ever exercised linux/amd64** while the reference panel host is arm64, and
  the arm64 cross-build path ran for the first time at release. `deploy-verify.sh` now takes the architecture
  (defaulting to the host's) and the `verify` job runs on both `ubuntu-latest` and `ubuntu-24.04-arm`.
- **Fixed: `audit:compiler` was failing and in no workflow.** `ROADMAP.md` said it "is in CI and currently
  reports 0"; it reported 1 error and was in no workflow, and `npm run lint` cannot see it because `eslint`
  enables only 8 react-hooks rules. The `setState`-in-effect in `pages/sla/index.tsx` is fixed (the request now
  runs from the promise callbacks, which also cancels a stale response) and the audit is a CI step.
- **Fixed: three of five UI languages were missing 134 keys each.** `bulk`, `status`, `maintenance`, `config`
  and `forecast` — five whole feature areas — fell back to English silently. All five locales now carry all
  1461 keys, a parity test runs in `npm test`, and `i18n:sync` merges as a union: it used to drop every
  target-only key, so running it without `OPENAI_API_KEY` deleted data.
- **Fixed: a corrupt archive blocked the backup prune.** The zip check ran bare under `set -euo pipefail` inside
  the "keep" loop, so one unreadable file aborted the whole prune and the disk kept filling. It is now reported
  and the expired backups are still removed.
- **Fixed: the session cache held a process-wide lock across its database query**, and never cached a miss — so a
  flood of forged `session_token` cookies forced a query per request *and* serialised every other session lookup
  behind that lock. The query now runs outside the lock, and a miss is cached briefly (and still reports
  `ErrRecordNotFound`, not an empty session).
- **Fixed: the docs site had no CI.** `site/README.md` calls the navigation check "not optional" and the site is
  published at a public URL, but nothing ran it on push. It runs in `workflow-contracts` now, alongside
  `check-action-inputs.py` — which could not fail until this change (it always exited 0).
- **Fixed: the terminal dialog's Enter key could confirm a deletion.** The key handler sits on the
  full-screen backdrop and the **Cancel** button is its child, so Enter on the focused Cancel button was
  intercepted by the backdrop — `preventDefault()` suppressed the button's own click — and the confirm action
  ran instead. The dialog is used with `destructive` to delete files, so a keyboard user tabbing to Cancel to
  abort deleted the file. The decision moved into `frontend/src/pages/terminal/dialogKeys.ts`, which is
  unit-tested (`npm test`): Enter from a `<button>` is never a confirmation, Enter on the dialog still is.
- **Fixed: the remote-exec page invented a completion time for tasks that had not finished.** After 60 s the
  poller wrote `finished_at: now`, `exit_code: -1` and overwrote `result` with a timeout marker — so the page
  showed a finish time the server never reported, next to a plausible-looking exit code, on the page used to
  run commands across the fleet, and a task that finished at 70 s was never fetched. It now sets one flag
  (`polling_stopped`), leaves the server's fields alone, and offers "Resume polling".
- **Fixed: a 2 Hz request loop that could not be cancelled.** After a settings save the migration-guide poll
  self-rearmed every 500 ms with no attempt cap, no backoff, no handle and no cancel path — so a panel that
  never came back left the tab requesting twice a second indefinitely, including after the reader left. It now
  backs off to 5 s, gives up after a three-minute budget, and stops on `pagehide`.
- **Fixed: one failed poll replaced the public dashboard with a raw English error.** `NodeListProvider` cleared
  the node list on any error, and the page then rendered `<div>Error: …</div>` — hardcoded, unannounced, with no
  way back. The list is now kept (a transient failure shows a translated "showing the last known state" banner),
  and a first-load failure shows a translated `role="alert"` panel with a Retry button.
- **Fixed: the admin node table re-allocated and re-sorted on every render.** `filteredNodes` was built inline,
  so a new array identity every render defeated `NodeListProvider`'s reference-stability work and made
  `NodeTable`'s reference comparison fire an extra render pass on every 5 s poll. It is memoized.
- **Fixed: the service worker precached every country flag and OS logo.** Measured on a clean build:
  **451 precache entries / 5257 KiB → 164 / 4449 KiB**, i.e. ~287 files (258 flags, 33 logos) and ~800 KiB
  removed from every install and every update. The B1 measurement priced the first visit (480 KiB) but never
  this. They are still available offline once used: a `CacheFirst` rule covers `/assets/flags/` and
  `/assets/logo/`.
- **Changed: SLA incident times carry their timezone.** `formatTime` now takes the UI language and appends the
  zone, because the SLA report's whole point is a URL you send to someone — without a zone, the same report
  reads as two different moments on two machines. The remaining `toLocaleString*` sites are listed as open
  work; the helper is there for them.
- **Changed: `tools/zstdpack` no longer writes file mtimes into the tar header.** Packing the same
  `frontend/dist` twice is now byte-identical. It does **not** make the committed theme archive equal to a
  rebuild: measured while trying to assert exactly that, rollup's chunk *assignment* differs between builds —
  the same source produced a `chunk-_layout.js` with 1 import and one with 20+, changing 143 chunk filenames.
  Every per-entry difference between two archives is a referenced chunk name, so the split itself is the only
  structural difference. The planned CI assertion was therefore **not** added (it would fail on every build);
  the committed archive stays authoritative and its *contents* are asserted by the Go tests under
  `web/public/`. Recorded as open work in `docs/OPEN-WORK.md`.
- **Changed: the repository no longer ships `install-komari.sh` or `agent/.github/workflows/`.** The first
  installs **upstream's** binaries under upstream's asset names (it would have installed a different product),
  its error branch was unreachable, and nothing referenced it; the second is inert (GitHub reads only the root
  `.github/workflows`) and still named the pre-fork module path.
- **Changed: `deploy/docker-compose.yml` requires `NEKOMARI_VERSION`** instead of pinning `v0.1.14`, about
  thirty releases behind a file `deploy/README.md` calls the reference setup.
- **Withdrawn after checking: two audit findings were wrong.** The rollup-retention "whole tier deleted"
  behaviour and the `metric_migration_target` write on store recovery are both deliberate — the first is a
  designed "disabled metric" plus derived-tier cleanup (both pinned by existing tests), and the second only
  means the key's *documentation* was narrower than its behaviour. The code was left alone; the doc comment and
  the refusal message were corrected.

## [v1.6.11] — 2026-10-13

**Security batch** — section A of the 2026-10-13 audit
([`docs/IMPROVEMENT-AUDIT-2026-10-13.md`](./docs/IMPROVEMENT-AUDIT-2026-10-13.md)), released on its own because
two of its six changes are reachable without a session. Panel-side only: `agent/` did not change, so **the fleet
does not move** (`git diff --stat v1.6.10..v1.6.11 -- 'agent/**/*.go' protocol/` is empty).

- **Fixed: an anonymous caller could enumerate hidden nodes.** `public:getPublicPingTasks` echoed every task's
  `clients` array verbatim, and `common:getNodeRecentStatus` / `common:getRecords` answered *differently* for a
  hidden uuid than for one that does not exist — so the uuid could be read in one place and confirmed in the
  other, without a session, on any panel with the default `private_site=false`. All three now share the
  visibility rule the history endpoints already use, and hidden answers exactly as unknown does.
  `common:getRecords` had a **second** copy of the same leak in its per-task `clients` echo, found while fixing
  the first. A new test enumerates every registered method and fails when one becomes reachable by guests
  without being reviewed — `Allow("common:*", RoleGuest)` makes that automatic, which is how this survived: the
  test found three more guest-reachable methods on its first run, and one of them
  (`common:getNodesLatestStatus`) was itself a hidden-node oracle by the same "different answer" rule.
- **Fixed: arbitrary SQL over RPC skipped the sensitive-operation step-up.** `admin:dbQuery` and `admin:dbExec`
  only required the admin role, so a stolen session could read `configs.api_key` or rewrite `users.passwd`
  without a TOTP code — while `admin:exec` already required one. Both are now marked sensitive, the read path
  writes an audit record too, and the sensitive list is asserted against an explicit allowlist so that adding
  one is a decision rather than an omission.
- **Fixed: plugin storage was silently excluded from backups.** The whitelist entry was spelled
  `plguin-data/`; the runtime directory is `./data/plugin-data` (`internal/plugin.StorageDir`). A missing entry
  is skipped silently, so backups reported success while dropping every plugin's persistent state — and the
  whitelist was never cross-checked against the runtime's own directory name, which is why a typo could sit
  there. It now is, and against a table of every path the runtime writes under `./data`.
- **Fixed: the traffic-limit form field evaluated its input as JavaScript.** `stringToBytes` used
  `new Function("return " + numericPart)`, so text pasted into the node editor ran in the panel's origin — on
  the page that edits the fleet. It is now a regex-validated product of numeric factors, and a test asserts that
  a payload like `1;(globalThis.__pwned = 1)` neither parses nor executes. The documented example for
  `6,222,765 MB` was also simply wrong (it had never been tested) and is corrected.
- **Fixed: SSRF protection for market downloads shipped disabled**, and the fallback wrote `false` into the
  config when the setting was absent — so "not configured" meant "no protection" for a fetch whose target is
  chosen by the remote catalog rather than by the operator. Both ends are now fail-closed.
  **Note for an existing installation:** the setting may already be stored as `false`; changing a default does
  not rewrite a stored value, so this must be switched on in the panel (see `docs/OPEN-WORK.md`).
- **Fixed: credential comparisons were not constant time** (the temporary share token, the API key, and the
  auto-discovery key, which is unauthenticated and creates a client row on success). They share one
  `pkg/secretcmp` helper.
- **Changed: the pinned Go toolchain moved to 1.27.2, and `golang.org/x/{net,crypto,sync,sys,text}` moved up
  with it.** This release is why. Its first attempt **failed before producing anything**: every build job runs
  `govulncheck -mode=binary` over the binary it just built, and Go 1.27.1's standard library now carries ten
  vulnerabilities (net/http, net/http/internal/http2, mime/multipart, crypto/tls) — all fixed in `go1.27.2`,
  and the http2 ones also by `golang.org/x/net@v0.60.0`. The tag existed and no Release did, so the tag was
  recreated on the commit that fixes it rather than left as a marker for nothing.
  **The gate itself was the other half of the problem:** it runs only in `release.yml`, so the first place a
  vulnerability that affects every release can be discovered was a release. `ci.yml` now has a
  `vulnerability-scan` job (source mode, both modules, the same `govulncheck` version), so the next one is
  found on `main` instead.

## [v1.6.10] — 2026-10-05

- **Fixed the 24 h traffic card reading a billing-cycle cumulative as if it were the window's traffic.**
  `traffic.up` / `traffic.down` are bytes since the cycle's reset day; the card asked for them with aggregation
  `last` and then added the returned points together, which counts the cycle total once per chart bucket. The
  live panel showed one node at **36.59 TB up / 51.16 TB down for 24 h** whose own recorded peak was 5.71 MB/s —
  a day at that peak is 493 GB — and a fleet total of 86 TB up. The card now asks for `sum`, which the panel
  redirects to the validated interval series (`traffic.interval.*`): additive per-interval amounts, where a gap
  contributes nothing rather than an invented number. `sum` alone is not enough, because an entity with no
  interval data is answered with the cycle counter again, marked `billing_cycle_cumulative`; those nodes are
  reported as unknown — excluded from the totals, listed as having no data, and counted under the fleet total —
  rather than summed or written as zero, since the cycle total is real and it is the window figure that is
  missing. Panel-side only; agent source unchanged. Pinned by `frontend/script/trafficSummary.test.mjs` and
  documented in `docs/TRAFFIC-ACCOUNTING.md`.

- **Fixed: a working node was reported as ICMP-broken.** The fleet ICMP check treated "no reply from this
  address family" as a fault even when the node answers on the other family, so a healthy single-stack node
  appeared in the broken list. The check now asks whether *any* family answers.

- **Noted, not fixed: changing a node's timezone requires an agent restart.** Go reads `/etc/localtime` once at
  startup, so a process running from before a zone change keeps printing the old zone *and* keeps cutting the
  `--month-rotate` window at the old zone's day boundary — a quota figure wrong by up to a day, not only a log
  line. No code change: the behaviour is Go's. Documented in `docs/DEPLOY-OC424.md`.

## [v1.6.9] — 2026-10-02

**The security batch**, picked in the order the 2026-10-01 audit set: security first, then data safety, then
wrong numbers. It **moves the fleet**, because `agent/` changed.

- **Fixed: `/api/public/ip-info/v1/lookup` let an anonymous caller read any node's egress IP by uuid.** A
  non-empty `uuid` is now resolved and checked against the same visibility rule the history endpoints use — the
  node must exist and not be hidden, or the caller must be an administrator — and a hidden node answers
  *identically* to one that does not exist (404, echoing the uuid). `unlock.Load` is unexported, so the public
  path has no second unguarded reader.
- **Fixed: an SSO login bypassed the second factor.** The OAuth callback now requires the same second factor
  `login.go` requires — same rules, same wording, same rate limiter — *before* `CreateSession`, and
  `/oauth2/bind` and `/unbind` carry `RequireSensitive2FA`. Fail-closed: on a panel with OIDC enabled **and**
  2FA on, SSO login is refused rather than silently skipping the factor. OIDC is disabled in this deployment,
  so production impact was zero; making it usable needs the follow-up in `docs/ROADMAP.md` §F12.
- **Fixed: a reachable but slow target was reported as 100 % packet loss.** `measureWithRetries` returned
  `(-1, false)` whenever no attempt answered within the timeout, so a target whose RTT is consistently above
  1 s read as a dead link. Four successful probes now report the last measured latency; real loss is still loss.
- **Fixed: the released agent image could update itself.** `Dockerfile.agent` — the file `docker.yml` actually
  builds — was missing the `/.komari-agent-container` marker, which only the unused `agent/Dockerfile` carried.
  A manually run container would have replaced its own binary and `exit(42)`, leaving the node offline for good
  under `restart: no`.
- **Fixed: the agent's `-t` warning missed spellings pflag accepts.** `-tv` is `-t v`, and `-token-file x` is
  `-t oken-file`; both slipped past the deprecation warning.
- **Fixed: binding or unbinding SSO from the panel did not send the sensitive 2FA code**, so the new
  `RequireSensitive2FA` on those routes would have refused the panel's own request.
- **Changed: `latest` follows only a release that verified.** A release whose own verification fails gets its
  images under the version tag but does not move `latest`. v1.6.6 is why: its verification failed (13 passed,
  2 failed — the agent's WebSocket handshake refused with `403 Forbidden`) and the image had moved `latest`
  anyway. The truth table is in `.github/workflows/docker.yml`; the policy in `docs/RELEASING.md`.

## [v1.6.8] — 2026-10-01

- **Changed: the embedded default theme is the panel's own front end, not a third-party theme.** The archive the
  binary carries is now assembled from `frontend/dist` (panel shell, `admin/`, `standalone/`, service worker and
  manifest) rather than a rebuilt copy of an installed theme, so a fresh installation gets the interface this
  repository builds. `web/public/.gitignore` had been ignoring the archive and the manifest, so CI created both
  on the spot and the embedded theme was quietly *not* what the repository held.
- **Fixed: a request for a missing asset was answered with the SPA shell.** A 404 now stays a 404 instead of a
  200 HTML document, which is what made a missing chunk look like a working page.
- **Added: a test that asserts the embedded document is the panel's own**, positively, rather than by absence.

## [v1.6.7] — 2026-10-01

- **Fixed: the WebSocket origin check refused a legitimate non-browser client.** `ws_origin_check_enabled` treats
  a request that sets its own `Origin` as browser-driven, but a client presenting `Authorization: Bearer <api
  key>` is not a browser and sets no `Origin` at all — the check now accepts a Bearer credential as the
  non-browser signal. This is what failed v1.6.6's own release verification (the agent's handshake got `403`).

## [v1.6.6] — 2026-10-01

The first batch from the 2026-10-01 audit. Panel-side and agent-side both, so the fleet moves.

- **Fixed: `common:getMe` answered an agent with its token where a uuid was expected.** The agent branch now
  returns the resolved client uuid and leaves the field empty when the lookup fails.
- **Fixed: `public:getSlaReport` returned the whole fleet when asked for an empty entity list.** An empty list
  is now an empty report, not "everything".
- **Fixed: legacy agents lost their cycle traffic.** The panel's traffic read fell back to the cycle counter, and
  the fallback now honours its own contract — it looks the metric up by `spec.metricKey` and downsamples the
  same way the primary path does.
- **Fixed: the pre-upgrade archive could be left truncated, and a bad archive stopped the restore path.** The
  archive is written to a temporary name, verified, then renamed; the restore path refuses to delete the
  `backup.zip` it could not read, and `ValidateArchive` checks the SQLite magic, page count and `quick_check`
  before a restore.
- **Fixed: the mmdb update leaked its write lock on every error path.** The download now goes to a temporary
  file with a 60 s timeout, is validated, and is renamed into place under a `defer`red unlock.
- **Fixed: a deferred offline alert was forgotten before it was delivered**, and its recovery notification was
  swallowed. Both are pinned by positive tests.
- **Fixed: an installed theme took over the panel's own routes.** `/install` and `/database-recovery` come from
  the panel again (`/plugin/*` is still the theme's, deliberately).
- **Fixed: billing cycles in the bulk editor were counted in months** rather than days, and the `htoplike`
  memory figure now says what it actually measures.
- **Fixed: `pkg/jsruntime`'s console writes raced** under `-race`.
- **Fixed: the agent put its credential in a URL in two more places**, and wrote its identity file world-readable.

## [v1.6.5] — 2026-09-30

- **Fixed recent traffic queries crossing partial minute buckets.** Verify raw
  completeness against the same full-bucket coverage as durable rollups, but
  return only samples in the requested window. Chart point budgets no longer
  force complete raw windows into broader minute totals.
- **Fixed late compressed inserts after a panel restart.** New samples merge
  into persisted minute buckets instead of rebuilding from incomplete volatile
  raw history. Coarser parents receive the full merged minute snapshot, not just
  the latest delta. Existing timestamp replacements retain their rebuild path.
- Panel-only hotfix discovered by the MAC-WAN v1.6.4 canary; agent source is
  unchanged. Regression tests cover partial windows, restart and minute/5-minute
  additive totals. Historical raw data already lost remains unrecoverable.

## [v1.6.4] — 2026-09-30

- **Fixed monitoring accuracy across sampling, ingestion, rollups and display.**
  - Verified interval bytes are additive; billing-cycle counters remain cumulative.
    Counter resets, changed interface scope, duplicate/out-of-order reports and sampling gaps
    no longer become fabricated traffic. Legacy history is explicitly marked unknown.
  - Ping latency includes successful attempts only, while loss includes every attempt.
    Whole-window mean, standard deviation and approximate quantiles merge distributions,
    not per-bucket summary values. Failed-only and unknown windows stay missing.
  - CPU, memory, swap and disk validation preserves valid zero readings and distinguishes
    unavailable readings. Latest status exposes sampling time, quality and counter epoch.
  - Traffic window totals no longer depend on chart point count; historical rollups disclose
    effective coverage. Existing sealed history is not rewritten as invented raw samples.
- **Fixed panel navigation:** server names open the detail drawer without a Radix slot crash;
  `/terminal` is served by the standalone admin application regardless of the active theme.
  Regression coverage includes the real embedded bundle and authenticated navigation.
- Both the panel and agent change. Upgrade the panel first; validate a new-agent canary
  against kernel counters before rolling out the rest of the fleet. See
  `docs/MONITORING-ACCURACY-FIXES-2026-09-30.md` for acceptance gates and known limits.

## [v1.6.3] — 2026-09-29

- **Fixed: a cycle cumulative was being summed, so traffic figures scaled with the sampling rate.** `traffic.*`
  is bytes since the billing cycle's reset day; requesting `sum` over it multiplied one running total by the
  number of samples. One day across the fleet read **3.87 PB** where the real figure was under **1 TB**.
  - new `delta` aggregation: the sum of positive differences, i.e. how much the cumulative grew. A decrease is
    a reset and contributes the amount since the reset, never a negative or the whole total.
  - `sum` on `traffic.*` is remapped to `delta`, so existing clients — including a third-party theme with no
    source available — get the right number without changing. The only meaningful reading of "summing a
    monotonic cumulative" is its growth, so the remap is the semantics rather than a compatibility shim.
  - `last` is unaffected, so "this cycle so far" still works as before.
  - both storage paths implement it: raw series, and rollups via each bucket's first and last values.

## [v1.6.2] — 2026-09-29

- **Fixed: the cycle traffic total lost the history the new accumulator could not see.** v1.6.1 replaced the
  retention-dependent `sum(samples in [reset day, now])` with a persistent accumulator, which removed one defect
  and introduced another: an accumulator only counts from **its own first sample**, so period usage collapsed to
  0.14 GB on a node whose provider billed **57 GB** for the same cycle. The two sources cover different spans —
  the accumulator from its first sample onward, the ledger the history before it — so they are now summed with
  `StartedAt` as the boundary, and samples at or after it are excluded to avoid double counting. An accumulator
  written before `StartedAt` existed is re-baselined rather than trusted, since a zero boundary would inflate.

  Measured on the node that prompted it: kernel lifetime 108.6 GB over 42 days of uptime, provider's own cycle
  figure 57 GB, and the panel showing ~101 GB because the card falls back to the lifetime totals.

## [v1.6.1] — 2026-09-28

**Traffic accounting.** The probe reported two different quantities through one field and the panel
differentiated it, which recorded a node's cycle cumulative as a single interval's traffic. On the node this was
found on, the interface had carried **0.29 GB since boot** (measured ~0.05 GB/day) and two samples of **41 GB**
were stored; its card showed ~39 GB per direction of "today" against a hover of ~80 GB. The scan found 450 such
samples across every node with `--month-rotate` configured.

- **The agent reports both quantities separately.** `net.total.*` is now always the kernel lifetime total
  (monotonic, same interface scope as the rate, so the two can be cross-checked); `traffic.*` is always the
  cycle cumulative. Previously one field held either, and fell back from the first to the second mid-stream
  when netstatic was unavailable. When netstatic is unavailable the cycle fields now report 0 **with an error**
  instead of substituting a value with a different meaning.
- **The cycle cumulative no longer depends on sample retention.** It was `sum(samples in [reset day, now])`
  while samples are pruned by `DataPreserveDay`, so a cycle longer than the retention window made it *shrink* —
  indistinguishable from a counter reset. It is now a persistent accumulator updated as samples are taken, and
  the reset day is passed into netstatic so the rollover happens on the sampling path.
- **The panel stores each quantity as-is.** `net.total.*` the raw counter, `traffic.*` the raw cumulative; the
  delta machinery is no longer applied to traffic.
- **Readers take the last value** of `traffic.*` instead of summing it — `sum` multiplied one running total by
  the sample count, which is where the ~80 GB figure came from. Changed in the admin dashboard, the load chart
  and the built-in theme.
- `deploy/cleanup-phantom-traffic.py` removes the samples already recorded (stop the panel first; a running
  panel rewrites the current bucket).

**Consequence to plan around:** a node's cycle total starts from zero the first time it runs this agent, so its
first billing cycle under-reports and the next one is correct. See `docs/TRAFFIC-ACCOUNTING.md`.

## [v1.6.0] — 2026-09-28

**The version now continues the upstream line, which is what makes the inherited plugin market work.**

Every plugin is gated on the server's version, and 15 of the 19 official entries require more than `0.1.x`
claimed — all seven notification plugins require `>=1.6.0`. Because the fork numbered its releases on a fresh
sequence unrelated to its lineage, every one of them was judged incompatible and the market was unusable by
construction. Releases from here are `1.6.0` onwards, against an upstream base of `1.5.0-fix1` and a last
upstream release of `1.5.1`. The number is a compatibility statement rather than a lineage claim; `FORK.md`
records that, and the residual risk that a plugin requiring `>=1.6.0` may use an API this fork's base predates.

The version must also be a **plain** `X.Y.Z`: both ends split it on `.` and parse each part as an integer, and
they fail *differently* on a suffix — the panel marks every plugin incompatible while the server silently
allows them. `internal/plugin/version_market_test.go` holds the version to both properties against the
captured market index.

## [v0.1.47] — 2026-09-28

- **Fixed: an asset with no content hash could be cached for four hours by an intermediate cache.** `serveAsset`
  sent a long `immutable` lifetime for build outputs and *no `Cache-Control` at all* for everything else, so
  Cloudflare chose — four hours. The theme's background images carry a manual version marker rather than a
  content hash, and the 404 from the moment they were briefly missing got cached at the edge: after the files
  were restored the page still had no background until a hard refresh. Assets without a content hash now state
  `no-cache` (revalidated with the ETag already sent), and `index.html`/`sw.js` state `no-store`.

## [v0.1.46] — 2026-09-28

- **Fixed: a targeted agent update refused a release with no checksum by name, and said what it chose.** The
  refusal used to be silent, so a node that declined to update looked like a node that had nothing to do.

## [v0.1.45] — 2026-09-28

- **Fixed: targeted agent updates never worked.** The checksum asset was looked up under the wrong name, so the
  verification step failed for every targeted update — the feature was inert from the day it shipped.

## [v0.1.44] — 2026-09-28

- **Added: `deploy/fleet/upgrade-agent.sh` finds the running agent instead of assuming its layout.** The older
  one-shot script assumed a path and a unit shape; the fleet script asks the host where the agent actually is,
  which is what made per-node upgrades repeatable across the five different layouts the fleet turned out to have.
- **Fixed: an agent refused a release download without saying which asset or which URL.** The message now names
  both, which is the difference between "the update failed" and "the update looked for
  `komari-agent-linux-arm64` at this URL".

## [v0.1.43] — 2026-09-27

- **Fixed: the front page's background image, which a theme rebuild had removed.** The theme's saved
  configuration points at `/assets/bg-desktop-light.v2.jpeg` and three siblings; those eight images had been
  added to the installed deployment by hand and were absent from the theme's source, so building the theme from
  source and replacing the installed copy deleted them. A missing background and one console 404, with nothing
  in any log to explain it. The images are now part of the theme's source, the deployed copy was restored from
  the pre-replacement backup, and a Go test asserts the embedded archive carries the four paths the
  configuration names.

## [v0.1.42] — 2026-09-27

**Agent and panel.** H6: the panel can point a node at a version.

- **`admin:rollAgentUpdate`** asks nodes to run a chosen agent release, or clears the target so they track
  releases again. Per-node outcomes distinguish `dispatched`, `at_target`, `offline` and `failed` — the first
  three want different actions, and a fleet-wide verdict would hide which. `admin:agentVersions` reports each
  node's version and connection state.

  The channel is the v2 event queue that already carries exec, ping and terminal requests. The target is
  re-sent on every dispatch, so a node restarted mid-rollout re-learns it rather than resuming its own tracking
  of the newest release. The panel's target wins over `AGENT_TARGET_VERSION`, which remains the
  deployment-level default and the way to pin a node the panel cannot reach.

- **The agent installs a named release, including an older one.** A rollback that cannot go backwards is not a
  rollback, and "update to the latest" can never express one. Drafts and prereleases are refused, as is a
  release with no asset for the node's platform, with the asset named in the message.

## [v0.1.41] — 2026-09-27

- **Fixed: the theme page's preview image, properly this time.** v0.1.40 put the image in the archive; the route
  still could not see it, because the server's embedded lookup only considered names under `dist/` and the
  preview sits at the archive root. Every file at that root — the preview, and the theme manifest itself — was
  invisible to `/themes/<short>/...`. There is now a regression test that reads the preview out of the embedded
  manifest and fetches it through the route, which is the assertion that would have caught both attempts.

## [v0.1.40] — 2026-09-27

- **Fixed: a broken image and a console 404 on the theme page.** The embedded theme's `preview` field named a
  file from the theme's own build, which resolves under the theme's path rather than where that file actually
  lives, so the panel's theme page asked for a URL that does not exist. The theme's `preview.png` is now in the
  archive and named.

## [v0.1.39] — 2026-09-26

**Panel only.** H5: traffic forecasting, and the default theme is now LuminaPlus.

- **New: traffic forecast** at `/standalone/forecast/forecast.html`. For each node, where this cycle's
  traffic is heading, when it would reach the plan limit, and — the part that makes it actable — the data
  behind it: how many samples, what fraction of the cycle they cover, and how scattered they were.

  **A projection that cannot be made is not made.** A cycle with under a day of data, or fewer than five
  observations, reports "not enough data" with the reason rather than extrapolating two points into a
  confident number. A node with no plan limit is projected but not projected against infinity.

  **A new notification warns when a node is projected to reach its limit**, once per cycle, alongside the
  existing "you have used N%" warning rather than replacing it: a node at 60% on day five of a cycle is a
  problem and one at 60% on day twenty-nine is not, and the percentage alone cannot tell them apart.

  The cycle is computed from a configurable cycle day, matching the agent's `--month-rotate`. The panel
  computes it in its own timezone because the agent does not report one, and the page states which
  timezone was used — a fleet on UTC agrees exactly.

- **The built-in default theme is now Komari-Theme-LuminaPlus**, replacing `komari-web`. This changes what
  a *fresh* installation looks like; an installed theme still takes precedence, so an existing panel sees
  no difference. The archive also carries all five standalone pages, and the theme's licence and copyright
  notice travel with it in `docs/THIRD-PARTY-LICENSES.md` and in the embedded manifest.

- **Fixed: every content-hashed asset was served without `Cache-Control`.** The theme's build did not emit
  `.vite/manifest.json`, which the panel uses to identify hashed asset names and give them a long-lived
  cache header. Without it, Cloudflare re-fetched files that never change on every POP, on the provider's
  default (measured at four hours). Now `public, max-age=31536000, immutable`. This affected anyone
  running LuminaPlus, not only this deployment.

- **The panel's admin interface is now its own build, served at `/admin`**, instead of coming from whatever
  theme is embedded as the default. A theme may decide what the *public* page looks like; whether the panel can
  be administered should not depend on which theme is installed. This existed as a latent problem before: the
  server has always forced `/admin` to the embedded default theme, so replacing that theme with a front-end-only
  one left a fresh installation with no admin at all. Found while releasing this version, which is why it is
  described in the same entry — see roadmap H7.

- **Fixed: every stylesheet was served without a long-lived `Cache-Control`.** A Vite manifest lists a build's
  CSS in a separate `css` array rather than as its own entry, and the server only read the `file` field, so no
  stylesheet was ever recognised as content-hashed. Affected the theme and the panel, on every deployment.

## [v0.1.38] — 2026-09-26

**Panel only.** H4: the panel's configuration as one file, and one file applied back.

- **New: configuration export and import** at `/standalone/config/config.html`. Export nodes, ping
  tasks, maintenance windows, settings and notification policies as one JSON document; paste a
  document and see exactly what applying it would do, per record, before anything is written.

  **Credentials are excluded by default.** A document that carried every column would eventually
  carry a token, and a config file is a new place for a sixth credential incident — so the exported
  fields are written out by hand and a new column is invisible until someone names it. An explicit
  "include credentials" export says so in the document and in the audit trail.

  **Import is locked until a check has been shown for the document in the box**, and editing the
  document withdraws the check: a plan for a different document is worse than no plan, because it
  looks like one. The report names each record and, for an update, which fields would change.

  **An import never deletes.** A record the panel has and the document does not mention is reported
  and left alone; absence in a snapshot is not intent, and a node added since the export would
  otherwise vanish on re-import. A document from a newer schema is refused rather than partly
  applied.

## [v0.1.37] — 2026-09-26

**Panel only.** H3: maintenance windows, so a planned reboot does not page anyone.

- **New: maintenance windows** at `/standalone/maintenance/maintenance.html`. Create a window with
  a name, a start and an end, optionally scoped to specific nodes (or every node), and while it is
  open those nodes' alerts are suppressed. The page marks an open window per row and distinguishes
  a fleet-wide window from a scoped one, because "every node" and "these three" are different claims
  and the fleet-wide one is easy to create by accident.

  **Suppression is scoped to notification, never to collection.** The metrics keep recording through
  a window, because the point of the window is that the node is doing something real — a gap in the
  charts afterwards would be a lie about what happened.

- **A suppressed alert is deferred, not dropped.** A node that goes offline inside a window and is
  still offline when it closes is reported then: the maintenance did not fix it, and silence there
  would be this feature's worst failure. A node that came back is not reported, because a late alert
  about an outage that has ended is a false alarm. The sweeper requires all three of: the window
  closed, the deferral not stale (30 minutes), and the node still offline under the same connection.

  Boundaries are stated and tested: half-open windows, so two back-to-back windows neither overlap
  nor leave a gap; the soonest-ending matching window is reported, so "when do alerts come back"
  does not depend on storage order; an empty client list means every node rather than none.

## [v0.1.36] — 2026-09-26

**Panel only.** H2: one change applied to many nodes, with a per-node outcome.

- **New: bulk edit** at `/standalone/bulk/bulk.html`. Multi-select the node table, switch on
  the fields you want to change — group, tags, weight, hidden, price, billing cycle, currency,
  traffic limit, limit type — and apply. **Only the fields you switch on are sent**, because
  sending every field would overwrite each node's unmentioned fields with the form's defaults:
  the first bulk edit anyone tried would have set a whole fleet's group to the empty string.

  A partial failure is reported **per node, with the applier's own reason**, rather than as a
  count. The edit is deliberately not transactional: all-or-nothing rollback means one bad node
  costs the operator the fifteen that would have worked, and hides which node is the problem.
  Every uuid gets an outcome; the successes stay applied.

  The selection is a list of uuids rather than row indices, so the list polling every few
  seconds cannot silently move it onto different nodes, and a node that has disappeared drops
  out instead of being applied to.

  `admin:bulkEditClients` runs each update through the **same** `clients.SaveClient` that a
  single-node edit uses, which a test proves by establishing that a value the single-node path
  refuses is refused in bulk too. An empty update is refused outright rather than reported as
  N failures, since it would stamp `updated_at` across the fleet while changing nothing.

## [v0.1.35] — 2026-09-26

**Panel only.**

- **Fixed: the report never read the latency series, so every task showed "no data".** The
  store held **29 478 minute rows** of `ping.latency_ms` with values up to 513 ms;
  `LatencyFromSamples` was written, tested and never called, so `TaskReport.Latency` stayed
  at its zero value and `has_data` was false everywhere. An always-empty field is
  indistinguishable from a target that never answered, so the report was quietly asserting
  "no latency measured" about a fleet that had weeks of it. The second read costs one query
  per request, and its failure is swallowed rather than failing the report — coverage is the
  reason the report exists.

## [v0.1.34] — 2026-09-26

**Panel only.** Fixes the SLA report's last wrong number.

- **Fixed: a task's coverage could report `expected 1` against 160 observed.** A task's
  series are split by their tags, and the tags have grown over time — one `task_id` in this
  fleet carries `{task_id}`, then `{protocol, task_id}`, then
  `{family, protocol, task_id}` — over *overlapping* windows. The report groups by
  `task_id`, so the same bucket arrived once per tag generation: **327 of 489 adjacent gaps
  were zero** on the live store. `cadence()` takes the median of the gaps, so a majority of
  zeros made it 0, and the "unknown cadence" branch reported `expected 1, observed 163,
  coverage 100%`. A denominator of "unknown" was rendered as "one", and 100% hid the gaps
  that coverage exists to expose. Duplicate timestamps now merge before anything measures
  them, combining counts and keeping the worst loss.

  Found by a read-only script printing the report's numbers beside the buckets the store
  holds for the same series. Three tests and two hours of theory had not located it, and
  the reason is worth keeping: the bug lived in the *relationship between series*, so every
  unit test — each building its own samples — was blind to it by construction.

## [v0.1.33] — 2026-09-26

**Panel only.** Makes the SLA report reachable on a themed deployment, which is what
v0.1.31 and v0.1.32 could not do.

- **The report is now a standalone page** at `/standalone/sla.html` instead of a route in
  the built-in router. v0.1.31 added it as a route, and the production panel answered its
  own 404: **LuminaPlus is an installed theme whose compiled `index.html` and `assets`
  replace the panel's built-in UI**, and `web/public/public.go` prefers the theme
  directory over the embedded bundle. A path the theme does not claim falls through to the
  embedded bundle, so `/standalone/` works in every deployment, themed or not — at the
  cost of not inheriting the theme's chrome. Roadmap H0 records the finding and the three
  options it rules out.

  Its own Vite config and its own output directory, deliberately: a shared build splits
  the page's dependencies into `chunk-*.js` files inside the `/assets/` namespace the
  theme owns, where a name collision resolves to the theme's file and the theme's
  `index.html` starts preloading chunks built for the UI it replaced. The page is one
  595 KB bundle with React inside it and no chunk imports, plus a stylesheet. No service
  worker, no `public/` (the several hundred flags and OS logos belong to the main UI).

- **Two contract tests, and the reason they exist.** Every unit test passed while the page
  was unreachable, because nothing asserted that it could be *reached* — so the contract is
  now tested from both ends. `script/standalone-sla.test.mjs` (5 cases, in `npm test`)
  checks the wiring at source level: the entry mounts into `#root`, does not import the app
  router, and the build config is isolated. `web/public/standalone_test.go` (5 cases)
  checks the artefact: the page is in the embedded bundle, references only its own
  prefixed assets, does not register the service worker, and its bundle contains React and
  no chunks. The Go half was written first and **failed before the archive was
  regenerated** — the build step that packs `frontend/dist` had never been run in this
  session — which is exactly the class of mistake it is there to catch.

## [v0.1.32] — 2026-09-26

**Panel only**, a fix for v0.1.31. Found by calling the deployed panel, which is the
slowest way to find it.

- **Fixed: the SLA report marshalled its nested fields as PascalCase.** `Presence`,
  `Availability` and `Incident` were declared without `json` tags, so the API answered
  `{"ExpectedBuckets":288,"Coverage":0.97}` while the page read `expected_buckets` and
  `coverage`. Every nested figure — coverage, availability fraction, bucket counts,
  incident times — arrived as an absent key, so v0.1.31's status page rendered with its
  numbers missing.

  **The tests were blind to it and so was the fixture.** The Go tests exercise values, not
  encoding; the browser fixture supplies hand-written JSON that already matched the
  TypeScript types. The page and the API could disagree while both suites passed.
  `internal/sla/wire_test.go` closes that gap: it marshals a fully populated report,
  walks the result generically, and asserts that every key the frontend reads is present
  and that none is PascalCase — the signature of an untagged field, so a field added later
  fails the same test.

## [v0.1.31] — 2026-09-26

**A panel-only release.** The agents are untouched —
`git diff --stat v0.1.30..v0.1.31 -- agent/ protocol/ pkg/` is empty — so the fleet
stays where it is and only the panel container is restarted.

- **New: a public status page with an SLA report** (`/status`), the first of the six
  features planned in `docs/ROADMAP.md` section H. Over a window of 24 hours, 7, 30 or
  90 days it reports, per node and per ping task: **availability** computed from measured
  loss, **coverage** beside it, **latency percentiles** (p50/p95/p99 across buckets), the
  **outage list** with each run's start, end, duration and peak loss, and the node's own
  **reporting gaps** — listed separately, because the panel knows a node stopped reporting
  and does not know it was down. The window is in the URL, so a report can be sent to
  someone along with the window that produced it, and the page is guest-readable like the
  public dashboard.

  The definitions are the feature. `internal/sla` holds them with no database in it, each
  stated in the doc comment of the function implementing it and each covered by a test:
  no data is neither 0% nor 100%; coverage is never folded into availability; the bucket
  cadence comes from the series grid rather than from the buckets that happened to carry
  data (or a series missing every third minute reads as a slower cadence and reports full
  coverage); a gap ends an outage instead of joining the failures around it; and a single
  lost probe is not an outage. Two defects were found this way while writing it — the
  cadence one above, and a bucket width that put a 24-hour window into 720 buckets against
  a 500 target.

  Deliberately not claimed: **node-level uptime**. The panel does not persist an
  online/offline history — `resp.Online` is built from live WebSocket connections and
  nothing writes it down — so a node-level percentage derived from reporting would be
  coverage dressed up as availability, and a node hard down for a week would read 100%.
  The page reports coverage for the node and availability per task, where loss is actually
  measured.

  i18n: `status.*` is in `zh_CN` and `en`; `ja_JP`, `zh_TW` and `id_ID` fall back to
  English until `npm run i18n:sync` runs with an API key.

## [v0.1.30] — 2026-09-26

**This release moves the fleet**, because — despite the heading above — the agent
changed after v0.1.29 was built. The change is one assignment, and the section below
explains why it is worth a release of its own.

- **Fixed: the traffic baseline reached disk up to 30 minutes late.** v0.1.29
  persisted the counter baseline and restored it on start, which is the fix for the
  impossible-delta bug. But startup writes the ledger *before the first sample*, while
  the baseline is still empty — and the rewrite limiter then treated that write as
  recent, so the real baseline waited a full `DefaultRewriteInterval` (30 minutes)
  before it could be written. For that window the baseline existed only in memory,
  which is exactly the state a restart loses. That is what deploying v0.1.29 and
  reading the ledgers showed: fourteen minutes after every node came back, none of
  them had `last_counters` in `net_static.json`.

  `restoreLastCountersLocked` now clears the rewrite timestamp, so the next save tick
  writes the baseline. Verified on the fleet afterwards: all six reachable nodes
  report the baseline present, in a ledger rewritten within one save interval.

  Worth recording as a process note rather than a code note: the first version of
  this fix **passed every test it had** and did nothing in production. The tests used
  a controlled clock, so they could not see a limiter interaction. The test added here
  pins the *sequence* instead — save, confirm nothing is due, load, record a baseline,
  then require that a rewrite is due and that the field reaches disk.

## [v0.1.29] — 2026-09-26

**This release moves the fleet**, because the agent changed. It fixes a bug that
inflated a node's reported traffic by orders of magnitude; see the entry below for
the measurement that found it.

- **Fixed: a node could report an impossible traffic delta, inflating the panel's
  daily and monthly traffic by orders of magnitude.** The counter baseline was kept
  in memory only, so a restart lost it and the next sample compared a live
  cumulative counter against nothing; on one node that produced a single **59.5 GB**
  delta in the minute the agent started, against a real rate near 7 GB/day and an
  interface whose lifetime counter read 187 MB. The panel's "today" for that node
  read ~160 GB. The baseline is now persisted in `net_static.json`
  (`last_counters`) and restored on start, and a delta is discarded — with one log
  line — when it is not physically possible for the elapsed time, when the baseline
  is older than the bucket it would be attributed to, or when there is no elapsed
  time at all.

  The same bug had been writing impossible samples since **2026-06-29**: cleaning the
  store removed 4 minute samples (largest 60.9 GiB) and 63 coarser rows that contained
  them or were of the same class (largest 321 GiB in a single hourly bucket). After
  the cleanup NOSLA's recorded traffic was 3.8 GB and MAC Server's 0.5 GB, both in
  line with their providers' own meters — which is the check that the numbers are now
  real rather than merely smaller.

## [v0.1.28] — 2026-09-26

**This release moves the fleet**, because the agent changed (per the cadence rule in
`docs/RELEASING.md`): it gains a credential file and reports its ICMP capability.
Both additions are additive on the wire, so an older panel ignores the new field and
an older agent simply omits it.

- **A node now reports whether it can do ICMP at all.** The agent probes its own
  raw and unprivileged ping sockets once at startup and sends the answer with the
  basic-info upload; the panel shows it beside the node's version and, when the
  answer is "neither socket opens", says that loss on that node's ICMP tasks is the
  tool's limit rather than the target's behaviour. An agent that has not reported is
  shown as **unknown**, not as unavailable — every not-yet-upgraded node is in that
  state, and painting them as broken would be the same misreading the feature
  exists to remove. This is phase 1 of `docs/ROADMAP.md` E2; it changes no
  scheduling.
- **The agent reads its token from a file, not the command line.** `--token-file`
  (or `AGENT_TOKEN_FILE`, or an `AGENT_TOKEN=` line in a systemd `EnvironmentFile`)
  keeps the node's identity out of `/proc/<pid>/cmdline` and
  `systemctl show -p ExecStart`. `-t` still works and the agent warns once at
  startup when it is used; a credential file that group or other can read is
  refused rather than accepted quietly. Both installers now write the credential
  file and pass `--token-file`, and the OpenRC template does the same.
- **Fixed: a failed release-list refresh renewed the cached list for six hours.**
  The successful list and the failed attempt shared one timestamp, so a failure
  past the six-hour TTL rewrote it as "now" and the stale list was served from the
  success path. The two windows are now separate, the cache key moved to v3 (a v2
  entry cannot be aged, so it is discarded rather than trusted), and the service
  worker no longer caches `api.github.com`.
- **Fixed: the auto-discovery panel's "go to general settings" button did not
  navigate.** The module imported `Link` from `lucide-react` — an icon — and used it
  as a router link, so the button rendered inside an `<svg to="…">`: visible,
  styled, and inert.

## [v0.1.27] — 2026-09-25

**This release moves the fleet**, because the agent changed (per the cadence rule in
`docs/RELEASING.md`): the agent gains ICMP without root, reconnect backoff, and a
traffic ledger that reports its own save failures. The panel half adds the
mixed-family task refusal.

- **The agent no longer needs root or `CAP_NET_RAW` for ICMP.** It asked pro-bing for
  a raw socket unconditionally; the kernel's unprivileged ICMP socket is enough to
  measure echo latency, and nothing is lost by using it because the agent sets no TTL,
  traffic class, mark or source address. It now falls back to that socket when the raw
  one cannot be opened. The order is raw first on purpose: every existing node's
  history comes from the raw socket, so a node that already has the privilege keeps
  the path it has — the point is that a node *without* it works instead of reporting a
  permanent phantom loss. Verified on a host where the raw socket is refused outright:
  the agent measured 1 ms over the fallback.
- **Reconnect and retry attempts back off.** Every retry used the fixed
  `--reconnect-interval` (5 s) forever — the WebSocket loop, the POST fallback's
  recovery attempts, the POST report loop and the task pull loop — each retrying and
  logging every few seconds for the whole outage. Now 5 s doubling to a 2-minute cap
  with ±25 % jitter, reset on success, and failure logs folded to one line per burst
  plus one on recovery. The jitter matters as much as the backoff: a fleet that lost
  the panel together would otherwise return in lockstep.
- **The traffic ledger reports its save failures.** `saveToFileLocked()` was discarded
  in both the periodic rewrite and the immediate flush, so a full disk stopped
  persisting traffic accounting silently and the first symptom was wrong numbers after
  a restart.
- **A ping task whose probes would measure different address families is refused.**
  A hostname is resolved per node, so one dual-stack target could be measured over
  IPv4 by one node and IPv6 by another — two different paths reported as one number
  (measured: 12.6 % loss against 0.0 %). An address literal is always allowed; a
  hostname only when every probe is known to use the same family, and `default_on` is
  refused for hostnames because every future node would decide for itself. Enforced on
  create and on edit.

## [v0.1.26] — 2026-09-24

- **A ping task now reports the address family it actually measured.** A hostname is
  resolved by each node independently, so a dual-stack target can be measured over
  IPv4 by one node and IPv6 by another. Those are two different paths with different
  latency and loss, and the panel reported them as one task with one number — measured
  on the live fleet, HK04 read **12.6 % loss** while the two v4-only probes read
  **0.0 %**, describing different routes to the same hostname. The agent now returns
  the family of the address it used, the metric store tags it beside `protocol` and
  `role`, and the statistics group by task + family so the two render as two series.
  Every layer is additive, and an empty family keeps the historical series identity:
  nothing changes for an existing deployment or a third-party theme until that
  deployment's own agents are upgraded. This closes option C of `docs/OPEN-WORK.md`
  and entry 4 of `docs/SILENT-FAILURES.md`.
- Close the Cloudflare IPv6 firewall gap, and cache build artifacts properly in CI.
- **Admin write paths gained a server-backed regression.** A node edit and an
  offline-notification save are driven against a real panel rather than a stub: the
  first attempt is forced to fail, the dialog and the draft must survive it, the retry
  must succeed, and persistence is proven with a fresh server read plus a page reload.
- **The two largest uncovered frontend paths gained mounted state regressions**
  (`pages/instance/LoadChart.tsx` and `pages/admin/settings/metrics.tsx`), each
  covering a stale response overtaking a newer one and an unmount/remount.
- **Fixed: the metrics migration status could move backwards.** The two-second status
  poll applied whatever came back with no sequence guard, so an older poll landing late
  rewound the rendered progress — measured at 10/10 replaced by 2/10, with the migrated
  points counter following it. The same window was open to the manual Refresh button.
  It now carries the `requestSequence` guard already used by the node-details provider.
- [docs/AGENT-FOOTPRINT.md](./docs/AGENT-FOOTPRINT.md) records what the agent costs the
  host it runs on, measured across the fleet: **15.8-24.3 MiB resident, 0.05-0.18 % of
  one core, 4-7 MB/day outbound, 7-11 threads and a single socket.** It also records
  that the agent has no inbound listener anywhere, which is why a NAT'd host needs
  outbound 443 and DNS and nothing else, and that ICMP tasks are the only ones needing
  privileges (`CAP_NET_RAW`, best granted with systemd `AmbientCapabilities`).

## [v0.1.25] — 2026-09-23

- Cache the GitHub release check across admin pages, including failed attempts,
  so navigation no longer repeats a third-party request on every page.
- Recover the admin node list after a transient request failure. A successful
  refresh now clears the error; non-2xx or malformed responses report an error
  without overwriting the last valid state. Late responses from older polls
  cannot replace newer results. Mounted browser regressions cover offline,
  HTTP 503, malformed entries, recovery and out-of-order completion in CI.
- Keep the admin node mutation and offline-notification dialogs open after
  rejected HTTP or RPC saves, preserving the user's draft and showing the
  server's error instead of claiming success. Mounted browser regressions
  cover failed requests followed by a successful retry.
  Single-node offline notification editing now sends the node ID even if no
  notification configuration exists yet.
- Remove the unused `@tanstack/react-table` dependency and its license-list
  entry. Correct the v0.1.24 structural-review counts and the deployment
  runbook's recorded image version.

## [v0.1.24] — 2026-09-23

The advisory React Compiler audit reaches **zero findings**, down from 77 at the
start of this batch. Nothing here enables the compiler; the migration removed the
diagnostics themselves, and every change kept lint, the 40 unit tests, the
production build and the browser specs green. See
[the migration ledger](./docs/COMPILER-MIGRATION-2026-09-23.md) for the inventory,
the probes behind each claim about how the rules behave, and the two ways the
render-time adjustment pattern is easy to get wrong.

- Fix `/admin/settings/sign-on`, which looped on every visit (React error #301).
  Both of the page's render-time adjustments used `null` as their "not yet run"
  sentinel, and both keys are legitimately `null` while their effect's early-return
  condition holds, so the guard stayed true on every render. The sentinels now
  start at `undefined`. Found by booting the server and walking every route — the
  audit, `tsc`, `eslint`, all 40 unit tests and all five fixtures were green on
  that file, because nothing mounts that page.
- Hold file-manager and file-tree selection in state instead of a ref, so a
  selection change re-renders: the highlight, the single-item context menu and the
  delete target had been lagging behind the actual selection. The editor's
  cut/copy/undo/redo flags had the same defect.
- Fix `number-picker`'s `onChange` echo, which made the admin log page's page 2
  unreachable. The effect echoed the clamped value on every run, and the only call
  site passed an inline arrow, so the dependency changed on every parent render and
  the echo re-fired the page's `setPage(1)`. The picker now adjusts during render
  without echoing, and no longer reports an external `defaultValue` change.
- Fix `LoadChart` refetching without resetting when an identical custom range was
  re-applied, and move the terminal file tree's directory cache out of a ref into a
  module-level `WeakMap` keyed by the file service.
- Delete the unreferenced `components/admin/NodeTable*` subtree, which clears the
  last finding. Its `useReactTable()` call had been making the compiler skip the
  whole component, which hid a second finding in the same file; the subtree was
  already tree-shaken out of the bundle, so removing it changes no behaviour.
- Add a real-environment smoke test to CI. It builds the panel, the embedded theme
  and the server, boots it against a fresh database, installs an account through the
  real installer, and walks every route the navigation exposes with Playwright,
  failing on a same-origin console error, a same-origin 4xx/5xx, or a route that
  renders nothing. External origins are warnings, never failures.
- Add browser fixtures for `number-picker` and `RemoteFileTree`, both verified able
  to fail; a mounted file manager regression covering rename/delete refresh and
  stale responses; and an anonymous GHCR pull requirement in the Docker workflow.
- Add [the structural review](./docs/STRUCTURAL-REVIEW-2026-09-23.md), which
  measures the frontend's margin: 52 of 189 source files are reachable from a test
  entry point, and 19 of the 30 files over 500 lines are not. These corrected
  counts include tests that load source through `readFileSync` rather than imports.
- `@tanstack/react-table` has no importer left under `src/` but stays in
  `package.json`: the lock file cannot be regenerated in the environment this was
  prepared in, and shipping `package.json` without a matching lock would break
  `npm ci`.

## [v0.1.23] — 2026-09-23

Frontend render-time state and effect-dependency fixes in theme settings,
pagination and selector state, taking the advisory audit from 111 to 105; an admin
upload scan-duration regression; and chart accessibility status moved into the
mounted component tree. See
[follow-up scope and evidence](./docs/FOLLOWUP-v0.1.23.md).

## [v0.1.22] — 2026-09-23

A frontend state and browser review — server, agent, protocol and shared-package
source unchanged — taking the advisory audit from 126 to 111. See
[follow-up scope and evidence](./docs/FOLLOWUP-v0.1.22.md).

## [v0.1.21] — 2026-09-22

- Split React providers from context/hook exports and move App out of bootstrap;
  eliminate all 14 Fast Refresh warnings and enforce zero-warning lint.
- Replace terminal drag lodash throttling with an animation-frame scheduler,
  flushing the final position and cancelling work on unmount; remove the dependency.
- Expose cached upload cleanup/contention statistics through admin-only RPC2
  admin:getUploadStats, including scan freshness; clear stale counts for a missing store.
- Isolate authentication fixtures and run ten shuffled race-test iterations in CI.
- Add provider identity and frame scheduling tests. Adopt seven audited compiler
  rules and provide a separate full compiler audit command. See
  [follow-up scope and remaining work](./docs/FOLLOWUP-v0.1.21.md).

## [v0.1.20] — 2026-09-22

Follow-up hardening from [the v0.1.19 review](./docs/NEXT-REVIEW-v0.1.19.md), plus the
fleet agent compiler refresh and two deprecated frontend dependencies.

### Fixed

- **The panel health probe could not detect failure.** `deploy/panel-probe.sh` recorded
  `time_starttransfer` without checking curl's exit status or the HTTP status, so a fast
  HTTP 500 — or a connection refused, which reports `0.000000` — was logged as a healthy
  sample and exited 0. A sample now counts as healthy only when curl succeeded, the status
  is 200 and the body is a valid success envelope; failures name their reason
  (`refused` / `timeout` / `status-NNN` / `no-timing` / `envelope`) in the log line and the
  alert, and exit nonzero regardless of elapsed time. The healthy log line is byte-identical
  to the old format. Deployed to OC424: 88 offline assertions pass on that host, and a
  refused-origin run now exits 1 where the old script reported health.
- **An abandoned upload reservation directory could take uploads down for a day.** `Init`
  creates the session directory and then writes `upload.json`; a process killed between the
  two left a canonical directory with no metadata, and `scan()` then failed on the missing
  file, refusing *every* later admission until that directory's mtime passed the 24-hour
  TTL. Startup reconciliation now reclaims a metadata-less session directory once it is
  empty, and never deletes one that still holds unrecognised content.
- **An abandoned backup staging file was never reclaimed.** `SaveUploadedBackup` removes
  its temporary file on every error path, but a process killed mid-upload left
  `./data/.backup-upload-*.zip` (up to `MaxArchiveSize` each) behind, and nothing collected
  it: the startup restore only clears `./data` when a `backup.zip` is actually waiting.
  Bootstrap now reclaims those files by exact name while holding the restore lock, and
  leaves every other file alone.
- **Upload retry classification treated 507 as transient.** The client retried
  `Insufficient Storage` like any other 5xx. The store's only transient refusal is 429
  (another write holds the lock, with `Retry-After`), so a disk-full refusal now fails on
  the first response instead of burning the retry budget.
- **Four React effects that fetched in a loop or not at all.** Resolving the
  `react-hooks/exhaustive-deps` warnings meant checking each one rather than adding the
  missing name: `theme_managed` rebuilt `theme_settings` inline on every render and issued
  unbounded requests (40+ and still looping), the themes list needed a `useCallback` before
  it could take its dependency, the admin node list rebuilt its 5-second interval on every
  poll response, and the log page never refetched when the page size changed while its
  pagination already used the new value. Two more would have been made *worse* by the naive
  fix: adding `t` to the 2FA effect would re-run `/api/admin/2fa/generate`, which mints a new
  TOTP secret under a user who already scanned the QR, and adding the release-check helper
  would have re-fetched the GitHub API on every render.
- **Eight findings ESLint 10 adds**, all fixed rather than suppressed: six dead stores (an
  initialiser every path overwrites) and two rethrows in `rpc2.ts` that dropped the original
  error, which now carry `cause`.

### Added

- **A real disk budget for archive uploads.** Admission reserved the declared payload while
  the flow holds several payload-sized copies of it. It now also requires room for the merge
  scratch, the staged copy and bounded extraction expansion on top of a reserve floor —
  queried per platform (`unix.Statfs` `Bavail` on unix, `GetDiskFreeSpaceExW` on Windows) and
  injectable for tests. A platform with no query refuses visibly instead of assuming
  infinite space, and the refusal is HTTP 507 carrying the measured numbers.
- **Upload cleanup observability.** `RunCleanup` no longer discards its errors: failures and
  recovery are logged once per burst (with folded repeat counts) through the shared logger,
  and `Store.Stats()` exposes session count, reserved bytes, oldest session age, last
  success, last error and reclaimed totals without doing I/O.
- **Upload lock-contention measurement.** The review proposed splitting the single store
  lock into per-session exclusion plus a global I/O semaphore, but said to instrument
  duration and rejection counts first, and that is what was done. Measured on the production
  panel before changing anything: **zero** archive uploads across every retained nginx log,
  an empty upload store, and not one 429 on an upload route — so there was no contention to
  find and the lock was deliberately left exactly as it was. `Store.Stats()` now reports
  refusals and hold durations per operation (logged hourly, and only when caller work
  actually happened) so the next decision rests on data.
- **Per-IP admission on OAuth start**, alongside the existing global pending-state cap, so
  one caller cannot fill every slot; refusals use the login limiter's 429 + `Retry-After`
  convention.
- **OAuth integration coverage** for the paths production cannot exercise: QQ callback-query
  preservation, a provider changed mid-login, and back-button replay.
- [docs/OAUTH-INTEGRATION.md](./docs/OAUTH-INTEGRATION.md) — the numeric provider-ID
  migration and rebind policy, including the evidence that two ids above 2^53 currently
  collapse onto one stored binding.

### Changed

- **Recharts 2.15.4 → 3.10.1.** The 2.x branch is no longer maintained. Eight API changes
  across the four call sites; two were behavioural and would have shipped silently —
  v3 defaults `CartesianGrid`'s axis ids to `0`, and both multi-axis charts use their own
  ids, so the horizontal grid lines would have disappeared while everything still compiled.
  Verified by rendering all three chart call sites in a real browser against a throwaway
  panel, not by compiling alone.
- **ESLint 9.39.5 → 10.11.0**, with `@eslint/js` 10, `eslint-plugin-react-hooks` 7 and
  `eslint-plugin-react-refresh` 0.5. `react-hooks` v7 folds the React Compiler rules into
  `recommended`, which flag 133 pre-existing patterns in this app; adopting those is its own
  change, so the two rules this project has always enforced are named explicitly in
  `eslint.config.js`. Nothing is silenced — the compiler rules are simply not enabled yet.
  Lint is now 14 warnings and 0 errors, down from 27, and the 14 left are all
  refresh-ergonomics rather than correctness.
- **`lodash` is now a declared dependency.** `src/pages/terminal/useTerminalPage.ts` imports
  `lodash/throttle` and it resolved only because Recharts 2 hoisted it; Recharts 3 dropped
  lodash for `es-toolkit`, npm pruned it, and the build failed to resolve the import. This
  makes an implicit dependency explicit rather than adding a new library. A local or
  `requestAnimationFrame` throttle would remove it entirely and is a reasonable follow-up.
- TypeScript `target`/`lib` moved ES2020 → ES2022 so a rethrow can carry `cause`. This is
  type-check-only: `noEmit` is true and Vite owns the real build target.
- The nine production agents were refreshed to the v0.1.19 binaries (Go 1.27.1, symbol
  tables retained) — a compiler refresh, not a behaviour change; the agent source is
  identical between v0.1.16 and v0.1.19. See
  [the rollout record](./docs/DEPLOY-OC424.md#fleet-agent-refresh-2026-09-22-v0119-toolchain).

## [v0.1.19] — 2026-09-22

- Retain executable symbol tables while omitting DWARF debug data. Removing
  symbols with `-s` made govulncheck fall back to module-wide findings, including
  unused OpenPGP packages. Reproduced locally with identical code: stripped scan
  fails, symbol-preserving scan passes. No vulnerability exclusions are added.
- v0.1.18 publication was blocked by the new gate; no release assets were published.

## [v0.1.18] — 2026-09-22

- Build every server and agent release with Go 1.27.1, matching CI. The previous
  workflow selected Go 1.26.0 from the module minimum; scanning the v0.1.17 Linux
  server artifact found vulnerable standard-library symbols despite clean local
  checks with a patched compiler. Deploy v0.1.18 instead of v0.1.17.
- Require a pinned govulncheck binary scan for all eight release executables
  before they can be uploaded and published.

## [v0.1.17] — 2026-09-22

- Bound archive uploads to 16 sessions and 8 GiB of reserved payload, with
  24-hour expiry, hourly cleanup and restart-persistent quota accounting.
  Serialize writes/finalization, reject conflicting operations with 429 and
  use sequential frontend chunks with bounded timeout and retry backoff.

- Harden OAuth callbacks: honor disabled login, require matching state for every
  provider, bound pending states and make them expire and consume atomically.
  QQ authorization embeds state in the callback URL; aggregators must preserve it.
- Bound OAuth HTTP requests to 15 seconds and 1 MiB; reject failed responses,
  missing tokens and invalid user identities. Send GitHub credentials in the POST
  body. Keep a working provider when a replacement configuration cannot load.
- Preserve uploaded chunks after an incomplete merge so the upload can resume.
- Refresh frontend dependencies within declared ranges; upgrade Go crypto/network
  and compression dependencies, including the theme packer. Server Go minimum is
  now 1.26, matching the agent.
- Add lint, vet and authentication race checks to CI. See
  [the review](./docs/REVIEW-2026-09-22.md) for verification and remaining work.

## [v0.1.16] — 2026-09-22

Everything here comes from the five-area optimization review in
[docs/OPTIMIZATION-REVIEW-2026-09-22.md](./docs/OPTIMIZATION-REVIEW-2026-09-22.md), which
records the evidence for each item, including the two that were deliberately not done.

### Fixed

- **A manual release could publish a version called `main`.** The build jobs injected the
  version from `github.ref_name`, which is the tag on a tag push but the *branch* on a
  `workflow_dispatch` run, while the tag the release is created for is resolved separately.
  A manually released agent would have reported `main` as its version — the string its own
  self-updater compares to decide whether a newer release exists. All four jobs now take the
  tag from one `resolve` job, and each build fails if the binary does not carry it. The
  end-to-end deploy check asserts the agent's reported version too, not just the server
  banner.
- **`metrics.db` was archived as a raw copy of a live WAL database.** The admin backup
  copied the main file while the metric store ran in WAL, and `-wal`/`-shm` were neither
  archived nor restorable (the restore path deletes them), so a restored archive silently
  lost every rollup still in the WAL. It is now snapshotted with `VACUUM INTO` through the
  metric store's own handle, exactly like `komari.db`.
- **The pre-upgrade archive was written after the migrations it exists to undo.** It was
  taken after `migrations.Run` had already dropped the legacy `configs` table, renamed
  `client_infos` and rewritten timestamps, so it could not roll back the upgrade it was
  named for. The snapshot now precedes the migrations; the version marker is read with raw
  SQL (moving `config.SetDb` earlier would make its `AutoMigrate` panic on a legacy schema),
  and the metric store is left out of that archive, which is what made it 60-100 MB.
- **A published Release could end up with no container image.** The image workflow gated on
  the whole `release` workflow's conclusion, which includes the verification job; one flake
  there meant a Release with no image and only a skipped job. It now checks that the Release
  exists, which is what it actually depends on.
- **The panel's live-status poll computed a ping map that the built-in UI throws away.**
  Omitting `include_ping` selects the expensive path (an uncached ping-task read plus one
  metric-store rollup scan per node), and all three in-repo callers omitted it and then
  never read the field — including a terminal monitor that polls every 2 seconds. They now
  pass `include_ping: false`. The default is unchanged, because third-party themes rely on
  it, and the server side got cheaper for callers that do want it: the task list is cached,
  and the per-node rollup scans became one batched query.
- **Theme and SPA assets had no validator at all.** Every request re-read the file from disk
  and sent it whole, so no client could revalidate and Cloudflare could never get a 304 on a
  cache fill. They are now served with `Last-Modified`/`ETag` and Range support, with the
  local theme files streamed rather than read into memory; the favicon's deliberate
  `no-store` is untouched.
- **The frontend asked for the same settings once per mounted consumer** (~13 call sites):
  a page with N consumers made N requests, and every navigation re-issued them. They now
  share one store — one request per page, none while it is cached — with saves, `refetch()`
  and logout publishing through it so a change is still visible without a reload.

### Changed

- **The agent samples traffic every 30 seconds instead of every 2.** The flush already sums
  the window into one bucket, so the finer cadence bought no resolution: it bought 43,200
  `/proc/net/dev` reads per node per day, now 2,880, and a counter reset inside a bucket
  loses at most one interval. The 31-day ledger is also no longer rewritten on every save
  (every 10 minutes) but when it has actually changed and at most every 30 minutes. Because
  the interval is persisted in `net_static.json`, a config generation migrates the old
  default — otherwise the change would have been a no-op on every deployed node.
- **Socket counting reads `/proc/net/sockstat`** (and `sockstat6`) instead of parsing four
  `/proc/net/{tcp,tcp6,udp,udp6}` tables whose size grows with the host's connection count.
  `inuse + tw` keeps the number meaning what it meant — all TCP sockets, TIME_WAIT included —
  which was verified against the old table count on the live host; the table scan remains as
  a fallback.
- **Entity-scoped rollup reads seek the series index.** Forcing
  `(resolution_id, bucket_milli)` made every per-node read range-scan all series in the
  window; dropping the hint alone does not help (the planner cannot see `json_each`
  cardinality), so small series sets now force the series unique index, looked up from the
  catalogue rather than hardcoded. Measured 165.7 ms → 2.6 ms per query on 200 nodes × 17
  metrics.
- **The panel decodes report bodies once instead of three times.** The ingest path
  round-tripped the generic params through JSON to reach the typed struct; it now decodes
  straight from `json.RawMessage` (29.1 → 8.4 µs, 7.4 → 1.6 KB per report). The wire format
  is unchanged, so agents and the protocol's `Request` type are unaffected.
- **Legacy history reconstruction batches its queries.** One all-clients request used to run
  one metric-store scan per metric *per node* (~170 queries at nine nodes, ~1500 at ninety)
  while holding one of the four public query slots; it is now one batched query per tier.
- **The ping scheduler reads `clients` once per pass** instead of once per task, and
  `GetConnectedClients()` gained a single-key accessor for the callers that were copying the
  whole connection map — one of them twice per request.
- **The admin console no longer precaches the code editor.** The service worker precached
  532 entries / 8.83 MiB, including a 3 MB editor chunk and 216 Monaco language chunks that
  most sessions never open; the editor and its languages are excluded (532 → 448 entries,
  −42 %) and still load on demand.
- **The terminal resource monitors ask for the node they are watching** instead of the whole
  fleet every 2 seconds, and a node that has never reported renders as offline rather than
  as an error.

### Added

- `deploy/panel-probe.sh` with a 10-minute systemd timer: records the panel's origin TTFB
  (the only number that implicates the panel) plus the public TTFB with the `cf-ray` POP and
  cache status, one line per run, and exits non-zero past a threshold. The 2026-09-21
  slowness was found by feel; `docs/PERFORMANCE.md` records what it turned out to be.
- `deploy/prune-upgrade-backups.sh` with a weekly timer: keeps the newest few pre-upgrade
  archives and verifies each survivor as a readable zip before deleting anything. Four days
  of upgrades had left 17 files and 1.4 GB beside the databases.
- `docs/PERFORMANCE.md`: how to measure the panel's load path in three commands, the two
  causes found, and the numbers behind them.

### Not done, on purpose

- **The main database still uses a single connection.** The review flagged it, but at this
  fleet's size it is not measurable: 20-way concurrent requests straight to the container
  answer in 6.7 ms median / 10.7 ms p95. Changing the riskiest setting in the list for no
  measured gain would be change for its own sake; the review records what to measure before
  revisiting it.
- **The locale bundles still load eagerly.** Deferring them would mean resolving the
  detected language before `i18next.init()` and re-rendering on a late bundle, which
  react-i18next's default binding does not do — a changed bootstrap for ~300 KB of JSON that
  compresses well. The precache change above already keeps the first install small.

## [v0.1.15] — 2026-09-20

### Added

- **A container image for the agent**, `ghcr.io/aone2233/nekomari-agent`, built for
  `linux/amd64` and `linux/arm64` by the same workflow that builds the panel image
  and from the same release assets. The panel's install dialog offers a Docker
  option, and it used to run `ghcr.io/komari-monitor/komari-agent:latest` — another
  project's agent, without this fork's flags, and depending on `--auto-discovery` for
  the node's identity. It now runs this repository's image, and a release publishes
  both images under the same tag scheme. The generated command is re-runnable
  (`docker rm -f` + `--pull always`), which is the update path for a container, and
  it mounts `/data` as a named volume so `net_static.json` (the `--month-rotate`
  traffic ledger) and `auto-discovery.json` survive the container replacement.
  `--disable-auto-update` is forced, because the agent cannot replace its own binary
  inside a container.

### Fixed

- **The panel handed out upstream's agent installer.** Two of the three
  install-command generators still pointed at `komari-monitor/komari-agent`, which
  installs `/opt/komari` + a `komari-agent` unit + `--auto-discovery` from an
  archived project, without the flags this fork added — the shape the NOSLA node was
  first installed with. Both now point at `deploy/install-node-agent.sh` (and
  `.ps1`). Related fixes in the same path: the version option emitted
  `--install-version`, which neither installer accepts (they use `--version`), so it
  would have been passed to the agent and failed the unit; and the PowerShell
  installer only understood the `--flag=value` spelling, silently dropping the
  panel's space-separated `--install-dir`, `--install-service-name`,
  `--install-ghproxy` and `--version`. `deploy/install-node-agent.sh` also accepts
  `--auto-discovery` now, because the "add node" dialog has no token yet and the
  installer required `-t`.

## [v0.1.14] — 2026-09-20

### Security

- **An unknown or deleted node's retained history was readable by anyone.** The
  public history endpoints checked only the node's hidden flag. The visibility map is
  keyed by the client rows that exist, so "absent from the map" was read as "exists
  and is not hidden": an anonymous caller could name a deleted node's UUID and
  receive the metric and ping history the metric store still holds for it. A UUID
  must now exist in the client table, and a hidden node still answers exactly like an
  unknown one, so the response cannot be used to probe which UUIDs exist.
  `public:queryMetrics`, `public:getPingMetricStats`, `public:getRecordsByUUID`,
  `public:getPingRecords` and `public:getClientRecentRecords` all go through the same
  rule. Found by the Copilot review on #1 and still present in the merged code.

### Fixed

- **A saturated password-hashing pool reported a correct password as wrong.** At
  most two Argon2id evaluations run at once, and the third concurrent check was
  refused outright — through the same path as a failed comparison. With two logins
  in flight, every further caller, the administrator with the correct password
  included, was told `Invalid credentials`. A check now waits up to five seconds for
  a slot and a saturated pool answers `503` with `Retry-After`, without consuming a
  login attempt. See [docs/AUTH-HARDENING.md](./docs/AUTH-HARDENING.md).
- **A full chart-query pool rejected the fifth request instead of queueing.** The
  built-in UI no longer replays a failed call over HTTP, so "query capacity reached"
  was a chart that simply did not load. A request now waits up to 10 seconds for a
  slot, and the queue wait plus the 15-second work deadline stay inside the
  transport's 30-second request context. `admin:dbQuery` moved to its own pool
  entirely: an admin query during an incident no longer queues behind four public
  charts (or blocks them), it has no metric read budget, and its 20-second deadline
  fits under the transport's.
- **A large directory listing could not be returned.** The Agent's v2 control
  channel reused the 1 MiB browser control-body limit, so a listing above it was
  answered `413`, the Agent treated the 4xx as final, and the operator saw a
  file-operation timeout. The Agent channel now has its own 8 MiB bound, enforced on
  both the POST and the WebSocket path; reports and every other non-metadata message
  keep the 1 MiB limit on both. The Agent refuses a listing that would exceed the
  bound with an explicit error instead of letting the transport drop it.
- **Filling the session caches cleared them.** One full cache became a full miss for
  every logged-in user at once (a SELECT burst), and one full activity map lost the
  once-per-minute coalescing for every token (an UPDATE burst). Both now drop
  expired entries first and evict only the entries closest to expiry. The session
  cache also stopped keying itself on the sessions revision: the coalesced activity
  UPDATE bumps that revision, so every activity write wiped all 4096 entries and
  made each logged-in user's next request run its own SELECT. Revocations
  invalidate explicitly instead, and account changes still invalidate the cache.
- **`dbcache.Revision` panicked on an unregistered table.** It indexed the map
  directly, so any caller reaching a table nobody had registered took the process
  down. An unwatched table now reports 0.
- **`HiddenClients` returned the cached map itself.** The "immutable snapshot" was a
  convention, not a property: one caller's write changed what every later caller saw.
  Callers now own the map they get.
- **A corrupt rollup row was reported as a budget error.** `SeriesBatch` checked the
  read budget before the scan error, so a failing scan surfaced as "narrow the
  query". The scan error is now authoritative.

### Added

- **2FA re-enrollment from the panel.** With the authenticator lost, `/2fa/disable`
  is unusable — it needs a code from the device that is gone — so recovery meant the
  `disable2FA` CLI command on the host. `POST /api/admin/2fa/rebind` takes the
  account password as the step-up, checks it against the same buckets as login so a
  stolen session cannot grind passwords for free, and replaces the factor only after
  a code from the new authenticator verifies. The current factor keeps working until
  then, other sessions are revoked, and the change is audited. The account page
  offers it next to Disable.

  This is a deliberate reduction: a session plus the account password is now enough
  to replace the factor, where before neither alone was. An account whose session has
  also expired still needs the CLI command. The tradeoff and its mitigations are
  recorded in [docs/AUTH-HARDENING.md](./docs/AUTH-HARDENING.md).

### Documentation

- [docs/AUTH-HARDENING.md](./docs/AUTH-HARDENING.md) now separates what is
  implemented from what is only proposed, and cites a file and a test for every
  implemented claim. The previous revision presented the throttling policy as a
  proposal under an "implemented" heading, and the code did not match it — that
  mismatch is what hid the login-lockout defect (H1).

## [v0.1.13] — 2026-09-20

### Security

- Bind 2FA enrollment to an expiring, single-use server-side token and prevent
  overwriting an existing factor. Revalidate session identity on every RPC
  WebSocket request and refresh node visibility for existing guest connections.
- Use Argon2id with random salts for new passwords; migrate legacy SHA-256 hashes
  on successful login. Password hashing has a two-worker memory budget.
- Bound HTTP control bodies, decompressed Agent reports, WebSocket frames,
  RPC batches and historical metric query work. Private sites now protect IP
  information endpoints as well.
- Remove the Agent updater's deprecated OpenPGP dependency. Retain the existing
  cross-platform binary replacement library, verify SHA256SUMS.txt before
  replacement, bound downloads and release pagination, and avoid modifying
  http.DefaultClient. Custom release repositories must publish checksums.

### Performance

- The built-in live view skips unused ping statistics; configuration and compact
  node-visibility caches invalidate on database writes. Session activity writes
  coalesce to once per minute while revocation remains immediate.
- Report and ping queues count retrying records against their 4096-entry limits;
  writes use fixed-size batches and rejected admissions are reported to callers.
- Coalesce simultaneous IP lookups and cap in-flight upstream work. Do not replay
  failed or timed-out WebSocket operations over HTTP.
- Avoid copying every node report for a subset request, and reuse the recent
  report slice when trimming its window. Correct PWA icon URL interpolation.

See [resource boundaries and verification](docs/RESOURCE-HARDENING.md) and
[password migration/rollback](docs/AUTH-HARDENING.md).

## [v0.1.12] — 2026-09-19

### Added

- **Login throttling.** `/api/login` had no attempt limit at all, so passwords and
  6-digit 2FA codes could be tried as fast as the network allowed. Two token buckets
  now gate each attempt — per source IP (burst 10, one token per 90s) and per account
  (burst 5, one token per 5 min) — and a failed password or 2FA step consumes from
  both. Exceeding a bucket answers `429` with `Retry-After`. It is a bucket, not a
  lock, so a legitimate user recovers without an operator, and a successful login
  clears the account's failures. The IP bucket is deliberately not cleared on
  success, and an unknown username is throttled exactly like a real one, so the
  response cannot be used to probe which accounts exist.

### Documentation

- `docs/AUTH-HARDENING.md` — login throttling is implemented; the password-hash
  migration remains open, with its plan unchanged.

## [v0.1.11] — 2026-09-19

### Fixed

- **`fs.writeFileSync(path, data, "utf8")` created an unreadable file on Unix.** The
  third argument is an encoding, but `fsMode` treated any string as an octal mode: it
  parsed `"utf8"`, failed, and fell through to `ToInteger()`, which is `0`. The file
  was created with mode **0000** — even its owner could not read it back. Windows
  ignores Unix mode bits, so only the Linux run caught it, and the two `pkg/jsruntime`
  tests that cover it had been excluded from CI by the old `-run` allowlist. A string
  that is not a valid octal mode now falls back to the caller's default mode.

  Found by the v0.1.10 CI change itself: widening the suite from 79 tests to the whole
  hermetic set turned these two red on Linux, and they had been failing there all along.

- **The `BaseDir` confinement check rejected Windows 8.3 short paths.** A temp directory
  can be named `RUNNER~1` while its long form is `runneradmin`. `resolveRoot`
  canonicalises `BaseDir` with `EvalSymlinks`, which expands the short name, but an
  absolute path passed to `require`/`fs` was compared lexically — so `filepath.Rel` saw
  two different directories and rejected a path that was inside `BaseDir`. Both
  `WithinBase` and `RelativeToBase` now re-check with both paths resolved, on the mismatch
  path only, and the `require` loader shares the same check. Windows-only; the lexical
  fast path is unchanged. Also found by the widened CI, on the `windows-latest` runner.

- **`child_process.spawn` could drop a child's output.** The stdout/stderr readers ran
  in goroutines while `cmd.Wait()` closed the pipes concurrently. Go documents that
  reads from a `StdoutPipe` must finish before `Wait`; doing them concurrently can lose
  the last chunk, so a `close` handler could observe an empty buffer. The process is now
  waited for only after both readers reach EOF. Found by the widened CI on the
  `ubuntu-latest` runner, where it failed and on a quiet machine it passes by timing.

## [v0.1.10] — 2026-09-19

### Fixed

- **The unlock probe reported a region block as "unknown".** `agent/unlock` treated a
  non-200 title page as a transient failure: it retried and, when the retries failed,
  returned an error, so `probeNetflix` mapped a definitive 404 to `unknown` instead of
  `blocked` / `partial`. A 404 is a definitive answer, not a network failure. The two
  hermetic tests that covered this (`TestProbeNetflixOriginalsOnly`, `TestProbeNetflixBlocked`)
  had been failing since the commit that added the feature — because CI's `-run` allowlist
  never ran them.

### Security

- **Forwarded headers are honoured only from trusted peers.** Gin's default is to trust
  every proxy, so anyone able to reach the panel directly could forge `ClientIP` with an
  `X-Forwarded-For` header — and `ClientIP` feeds session records, audit logs and the
  visitor-audit rate limiter. The engine now trusts only loopback and private ranges
  (`127.0.0.0/8`, `::1/128`, `10/8`, `172.16/12`, `192.168/16`, `fc00::/7`), which covers
  the reference nginx-on-loopback deployment and Docker/LAN proxies while ignoring a
  public client's forged header.
- The main and guide HTTP servers now set `ReadHeaderTimeout` (10s), so a half-open
  connection cannot pin a worker. WebSocket upgrades are unaffected.
- `/api/login` request bodies are capped at 1 MiB instead of being read unbounded.

### Changed

- **CI runs the whole test suite again.** The `-run` allowlist matched 79 of 552 root
  tests (and 6 of 81 agent tests, in two packages only), so every test not named in it
  silently did not run. It is replaced by `-skip`, which excludes only the tests that
  genuinely need network, root or IPv6. `docs/TESTING.md` documents the exact commands.
- Server builds carry `VersionHash` again. It was read in six places but never injected,
  so every build — including official releases — logged at `Debug` level and reported
  `hash: unknown` through `/api/version`, `/api/public`, RPC and the DB version marker.
  `release.yml` and `build.sh` now inject it.
- The frontend no longer declares itself as a dependency (`"komari-web": "file:"`).
  npm installed it as a junction pointing back at `frontend/`, which made any recursive
  tool that follows junctions (git, find, backups, editor indexers) loop on
  `node_modules/komari-web/node_modules/komari-web/...`.

## [v0.1.9] — 2026-09-18

### Added

- **流媒体 / AI 解锁, shown in the IP information panel.** Unlock depends on the IP that
  makes the request, so it is measured by the agent on the node (every 6 h), stored in
  `unlock_reports`, and returned with `/api/public/ip-info/v1/lookup`. The panel block is
  a companion script — see `deploy/theme-unlock-panel/README.md` for why the theme cannot
  render it and why patching the bundle was rejected.

  What is reported, and what is not, was decided by probing the endpoints rather than
  assuming: `chatgpt.com` and `claude.ai` answer 403 to a datacenter IP regardless of
  country, and the Disney+/Prime Video pages contain "unavailable" in their own bundle, so
  status codes and substring tests both lie. Each result therefore carries its basis
  (`probe` or `region`), and anything undetermined is reported as `unknown` rather than
  guessed. Netflix and YouTube Premium are probe-based, calibrated against a real blocked
  and a real unblocked sample; ChatGPT and Claude are region-based and say so.

  The measured egress address travels with the results, because it is not always the
  node's own: one host in this fleet reaches the internet through another node.

### Fixed

- **The agent reported a completed TCP handshake as packet loss.** When the first
  handshake took longer than 1 s, the agent retried and, if the retry came back more
  than 800 ms faster, concluded the first SYN had been retransmitted and reported the
  whole measurement as lost. But by then the handshake had already completed and the
  retry had measured the real RTT. The effect on the panel was large: every retransmit
  became a full minute of 100% loss, because each 60 s bucket holds exactly one ping.
  On OC424 the 天津电信 task read 12% loss while a direct 30-connection test to the same
  target lost nothing — and 14 of the agent's 15 failures in four hours were this
  branch, the fifteenth being a genuine timeout.

  Measured on OC424, replaying the agent's own constants over 40 connections: 0 real
  timeouts, 5 flagged as loss, 12.5% reported — matching the 12% on the panel. The
  retransmit is still logged, but the retry's latency is now what gets reported, so
  loss means what it says.

  Only high-latency probes were affected. Probes with a single-digit RTT to the target
  never reached the 1 s threshold, which is why the same task read 0% from China and
  11–18% from Hong Kong, the US and Singapore.

## [v0.1.8] — 2026-09-18

### Fixed

- **The IP 信息 panel in LuminaPlus never appeared, and every server-side check said it
  should.** The theme parses each ip-info response with a strict zod schema in which
  `classification.source` is a required string; the server did not send it, so `/lookup`
  and `/latency` were rejected inside the browser. The endpoints answered `200` with
  correct data, the server's own contract tests passed, and React Query does not log
  query errors by default — so the failure was invisible from every direction except the
  one place nobody could see. `classification.source` is now set to `country_comparison`
  or `unavailable`, matching the reference implementation's values.

### Changed

- `deploy/validate_contract.py` is **removed**. It was a hand-written Python mirror of
  the theme's schemas and it required `classification` to contain only `type` and
  `label` — exactly what the server sent — so it validated the implementation against
  itself and reported success while the theme rejected the same payload. Replaced by
  `deploy/theme-contract-check.mjs`, which extracts the schema block from the installed
  theme bundle and runs it with the theme's own bundled zod.

### Documentation

- `docs/IP-INFO-API.md` — the IP panel investigation is closed with the proven root
  cause, and the four hypotheses that were tested and eliminated are recorded so they
  are not retried.
- `docs/SILENT-FAILURES.md` — new entry for this case, which is the purest example of
  the class: the server had no way to know it was wrong.

## [v0.1.7] — 2026-09-18

### Fixed

- **A scheduled job could be accepted and then never run.** A cron spec can parse
  cleanly and still have no future occurrence — `Next` scans a year of seconds and
  gives up, so `0 0 0 30 2 *` (30 February) is valid to the parser and never comes
  due. That was caught only inside the runner, which logged a warning and returned:
  `AddContextFunc` reported success to its caller, and the job then silently never
  executed. Whatever it drove — metric rollups, notification dispatch, retention —
  simply stopped, with nothing surfaced anywhere an operator would look. It is now
  rejected at registration. The runtime guard remains as a safety net and logs at
  error level.
- **A ping task could measure two different network paths and plot them as one.**
  A dual-stack hostname in a mixed fleet is resolved per probe: the dual-stack probes
  dial IPv6 and the v4-only probes dial IPv4. One task therefore reported HK04 at
  12.6% loss against 0.0% from its peers — the difference between the IPv6 and IPv4
  routes to the same hostname, not instability in either.

### Documentation

- `docs/SILENT-FAILURES.md` — a catalogue of places where the system knows something
  is wrong and does not say so, with each entry checked against the running instance
  so observed problems are distinguishable from latent ones.
- `docs/OPEN-WORK.md` — unfinished work, decisions waiting, and the traps that cost
  time, so a later session does not re-derive them.

## [v0.1.6] — 2026-09-18

### Fixed

- **The admin panel handed out upstream's agent installer.** All three platform
  commands pointed at `komari-monitor/komari-agent`, so anyone copying the command
  out of the panel installed the upstream agent — from an archived project, without
  the flags this fork added. The panel's own `--disable-web-ssh` checkbox did not
  even exist upstream.
- **`logged_in` was missing from `/api/public`.** Third-party themes read account
  state from the public settings rather than from `/api/me`, so a theme that gates
  a panel on `logged_in === true` never showed it — while every API call it depends
  on returned 200 with correct data. Necessary but not sufficient for the LuminaPlus
  IP panel; see [docs/IP-INFO-API.md](./docs/IP-INFO-API.md).

### Added

- **One-line agent installer** (`deploy/install-node-agent.sh`) that accepts the
  agent flags the panel generates, so the panel's command works verbatim. Consumes
  the installer-only options, passes the rest through to the agent and into the
  unit, resolves the latest release instead of a hardcoded version, verifies
  `SHA256SUMS.txt`, and handles macOS as well as Linux.
- **Windows installer** (`deploy/install-node-agent.ps1`). PowerShell cannot name a
  parameter `--disable-web-ssh`, so unrecognised arguments are collected and
  translated — that is what lets the same panel command work there too.
- **macOS agent builds.** The panel has always offered a macOS option and there was
  never an asset to install. The agent is pure Go, so it cross-compiles from the
  existing Linux runner at no extra cost.

### Documentation

- `docs/IP-INFO-API.md` now records the IP-panel investigation: what was ruled out
  (the mainland-China region gate, WebSocket authentication noise, three separate
  caching layers) and the two hypotheses that remain, so none of it is redone.

## [v0.1.5] — 2026-09-17

### Added

- **The task list now shows which nodes are being skipped and why.** The
  address-family filter stopped dispatching to a node that cannot reach a target,
  which was correct but invisible — the only symptom was a node with no curve, which
  reads as a broken target. The skip set is computed server-side and returned as
  `skipped_clients`; the public nodes API deliberately does not expose node
  addresses, and recomputing the rule in the browser would let the display drift
  from what the scheduler actually does.

### Fixed

- **The 2FA issuer said `Komari Monitor`.** That string is stored permanently in the
  user's authenticator app, so every user who enabled 2FA saw the upstream project's
  name rather than this fork's.
- **The PWA manifest said `Komari Monitor`**, in both `public/manifest.json` and the
  `VitePWA` block — the name shown when the panel is installed as an app.

### Documentation

- `CHANGELOG.md` and `CONTRIBUTING.md` added; the repository had seven docs and no
  changelog and no contributing guide, so the release history was only readable on
  GitHub.
- `docs/TESTING.md` names the two `pkg/jsruntime` tests that fail when the test
  process cannot write its own temp directory, and how to confirm it is the
  environment rather than the change under test.
- `deploy/README.md` documents that enabling 2FA breaks any script that logs in with
  only a password, and points at `nekomari_auth.py` as the single login path.

## [v0.1.4] — 2026-09-17

### Added

- **Address-family aware scheduling.** A node with no IPv4 address is no longer sent
  an IPv4-literal target, where it could only ever report a permanent 100% loss.
  Hostname targets are never filtered (the agent resolves them per family), and a
  node with no recorded addresses yet is not filtered either, so a newly added node
  is not silently dropped from monitoring. Skips are logged once per schedule
  reload. Fixes a target that looked unstable when the real problem was structural.

### Fixed

- **The 2FA issuer said `Komari Monitor`.** That string is stored permanently in
  the user's authenticator app, so every user who enabled 2FA saw the upstream
  project's name.
- **The PWA manifest said `Komari Monitor`**, in both `public/manifest.json` and the
  `VitePWA` block — the name shown when the panel is installed as an app.

### Changed

- CI actions moved to the majors that ship a Node 24 runtime (checkout v7,
  setup-go v7, setup-node v7, upload-artifact v7, download-artifact v8, docker
  actions v4/v6/v7, action-gh-release v3). The Node 20 deprecation warning is gone.
- `setup-go` now reads `agent/go.mod` rather than the root `go.mod`. The agent is a
  separate module requiring a newer Go (1.26.0 vs 1.25.0) and both are built from
  the same job; the older action tolerated the mismatch and v7 correctly did not.
- README documents the container deployment, which it previously did not mention at
  all — including that `-v` is required and why.

## [v0.1.3] — 2026-09-17

### Added

- **IP information API** (`/api/*/ip-info/v1`): geo, ASN, network type and Globalping
  latency, for themes that render an IP panel. Multiple upstreams with caching; see
  [docs/IP-INFO-API.md](./docs/IP-INFO-API.md).

### Fixed

- **A replaced favicon did not take effect**, for three independent reasons: the
  response type came from the file extension so a PNG was served as
  `image/vnd.microsoft.icon` and discarded by browsers; no cache directives were
  sent; and the admin preview used a fixed URL. Now the type is sniffed from
  content, `no-store` is sent, and the served HTML versions the favicon URL by file
  mtime so a changed icon bypasses every cache layer — including Cloudflare, which
  overrides the origin's `Cache-Control` with its own.
- **Traffic was reported as since-boot rather than per billing cycle**, because the
  installer never set `--month-rotate`. A node up 151 days reported 599 GB against a
  26 GB plan.
- The admin version banner compared against upstream's releases, so it always
  offered an "update" to a version this fork is ahead of.
- Web SSH was disabled on every node by the installer's default flag, which also
  disables remote command execution.

### Added (infrastructure)

- `deploy/deploy-verify.sh`, run in CI as the `verify` job after every release: it
  downloads the published assets, checks them against `SHA256SUMS.txt`, starts the
  server, completes the first-run install, connects an agent and confirms the node
  reports. `build` only proves it compiles and `release` only proves it uploads;
  neither would have caught the v0.1.2 container bug below.

## [v0.1.2] — 2026-09-17

### Fixed

- **The container image could not start at all.** The binary was linked against
  glibc but the image was based on Alpine (musl), so every `docker run` died with
  `exec /app/nekomari: no such file or directory` while all three pipelines were
  green. The base is now `debian:bookworm-slim`, and the Docker workflow smoke-tests
  that the image actually starts before pushing.

## [v0.1.1] — 2026-09-17

### Fixed

- `COPY --chmod` required BuildKit, which not every build host has.

## [v0.1.0] — 2026-09-16

First Nekomari release, forked from Komari `1.5.0-fix1`.

### Added

- `netcheck` — probes a target with DNS, ICMP and TCP and reports which protocol it
  actually answers, so a monitoring task can be configured around reality instead of
  guesswork. It also distinguishes "no permission to send ICMP" from "target
  unreachable", because conflating those leads to the opposite conclusion.
- Ping task type `auto` — the agent picks a protocol the target answers.
- Ping task type `dual` — measures ICMP and TCP in the same cycle, so a target that
  only answers TCP reads as `ICMP 100% / TCP 2ms` rather than a flat outage.
- Reference target on a ping task — probe a gateway alongside the target to tell a
  local problem from an upstream one.
- Baseline alerting — alert on deviation from a rule's own P95 history instead of a
  hand-tuned absolute threshold.
- Backup freshness metric (`backup.age_seconds` / `backup.ok`).
- Monorepo layout (server + `frontend/` + `agent/`), a bundled theme packer that
  needs no `zstd` binary, and `build.sh`.

[v0.1.7]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.7
[v0.1.6]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.6
[v0.1.5]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.5
[v0.1.4]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.4
[v0.1.3]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.3
[v0.1.2]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.2
[v0.1.1]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.1
[v0.1.0]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.0
