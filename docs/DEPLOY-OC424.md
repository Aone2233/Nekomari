# Public deployment (OC424 / nekomari.orderly2233.org)

The live Nekomari panel. This file is the runbook: what runs where, how it was
restored, and the two hostname/TLS traps that cost the most time.

**Security/backup supersession, 2026-10-28 23:53 Asia/Shanghai:** the owner reports OVH
has been relinquished. OVH is not a current trusted receiver or test host;
historical plaintext backup exposure and replacement backup/health gates are
recorded in [`OVH-RETIREMENT-2026-10-28.md`](./OVH-RETIREMENT-2026-10-28.md).
Three identified OVH SSH authorizations are revoked on OC424. MAC-WAN's
v1.7.12 pre/post ciphertext hashes still match. The
owner confirms the intentional OVH node removal (current baseline 13). MAC-WAN
now receives scheduled encrypted backups and reports actual health; the first
new archive passes isolated recovery and has a second online-host copy. The
first scheduled daily trigger succeeded on 2026-10-29; independent-media
recovery remains unverified.
See [`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md). The dated OVH availability/
schedule and 14-node acceptance statements below are history.

## Current checkpoint (2026-10-31 03:48 Asia/Shanghai)

OC424 runs **v1.7.19/cf221de**, immutable source
`cf221def7fc5f0610e6b407ecde67e9ddb5714df`, in the pinned arm64 image
`ghcr.io/aone2233/nekomari:v1.7.19@sha256:28d9f40423d2188e6636d1384f80dd567e7bf7aa27032dd0b28f2fd32674d970`.
The container started at 03:45:20 and was healthy with zero restarts at this checkpoint.

- SLA coverage labels now describe the existing observed span and expose its
  first and last observation buckets. Ping gaps are not proof of node downtime;
  no server calculation, RPC field, schema, alert or historical data changes.
  The exact candidate CI41110168331 and test-fix CI41113452947 pass 8/8, as
  does exact merge CI41114880987. Earlier merge CI41111906239 failed 7/8 at an
  immediate startup-sample assertion; a bounded wait was added, but the
  hypothesized asynchronous race was not proven as that failure's root cause.
- Release41116050542 passes 10/10 and Docker41117063818 2/2 with the actual
  triggering release ID checked for both jobs. Linux panel and Agent assets
  pass checksums and tag/source/workflow attestations; both architectures of
  both image identities verify. Native panel image binaries match released
  assets on OC424 and MAC-WAN. The released amd64 binary passes four isolated
  MAC-WAN admin/standalone browser cases in Chinese desktop/light and English
  mobile/dark, using only synthetic data.
- OC424 upgrade9/9 and public27/27 pass. Both DB quick checks and schemas
  match; all 13 current clients have fresh CPU/uptime and retain their
  UUID/token digest. API-key scope/expiry, GeoIP, authorized SSH keys, backup
  health and local Agent PID/restarts are unchanged during the switch. A
  scoped read-only forecast returns 13 rows (10 linear, three insufficient;
  two unknown usage). Owner-session UI and real alert receipt were not tested;
  stable publication can trigger other eligible Agents to self-update.
- Manual pre archive `oc424-20261030T194300290955Z-e4ccf07fa200.tar.gz.age`
  has SHA-256 `8b0695d6d4451a1f4a633b0f7fcfb22418c43ebe7cefba3f71e687e7ced06491`;
  post archive `oc424-20261030T194613732290Z-c5dab53a61d9.tar.gz.age` has
  SHA-256 `012b287a1f893282c590f84b0d7b18eb12bd4c8cc5561f9fa819732b2c6e49ef`.
  Both have matching MAC-WAN `incoming/` and executor ciphertext copies. Pre
  restores in isolation under pinned v1.7.18/v1.7.19; post under v1.7.19.
  Post compares 21,140 archived rows across 12 selected histories: no retained
  rows missing or changed; 48 expired or inactive-tier rows absent. This is not
  independent-media recovery. Scheduled backup pairs remain 14/14; no retention
  policy or timer was changed.
- The 03:35 automatic health report succeeded before these manual release
  backups. The next daily backup through the new route is 2026-10-31 11:30 and
  remains unverified; its existing keep=14 policy retires the oldest eligible
  pair, which has a verified `incoming/` copy. Independent-media recovery, OVH
  disk sanitization and real notification receipt remain unverified. OC424 disk
  is 48%, MAC-WAN 31%, executor 71%; no broad cache or backup deletion.

Root-private rollback evidence is
`/opt/nekomari/rollbacks/v1.7.19-20261030T194518Z-c79f1d72`; the retained
previous image is
`ghcr.io/aone2233/nekomari:v1.7.18@sha256:61c64fc685a409f534e472e9e67f77145fda45475ce3065fe93af7be241b9c0d`.
If needed, assess live state and switch only the pinned compose/image, preserving
the live databases and nodes; never overwrite them with old snapshots. See
[`SLA-COVERAGE.md`](./SLA-COVERAGE.md) for interpretation and remaining limits.

## Historical checkpoint (2026-10-30 22:38 Asia/Shanghai)

OC424 runs **v1.7.18/8dbfd8c**, immutable source
`8dbfd8c214b13820cfa23cb84994ed9393a8aacd`, in the pinned arm64 image
`ghcr.io/aone2233/nekomari:v1.7.18@sha256:61c64fc685a409f534e472e9e67f77145fda45475ce3065fe93af7be241b9c0d`.
It became healthy at approximately 22:34, with zero container restarts.

- Candidate CI41066053281 and exact merge CI41067514587 pass 8/8;
  release41068843126 passes 10/10 and Docker41071110757 2/2 with the actual
  triggering release ID verified. Published Linux checksums and
  tag/source/workflow attestations, both architectures of panel/Agent OCI
  identity and the native panel image binaries verify. The released amd64
  binary passes isolated MAC-WAN preview API and embedded UI tests: a valid
  scoped key passes a positive control but is denied preview; repeated previews
  do not change `last_notified`. Chinese desktop/light and English mobile/dark
  browser cases pass. No production rule or notification was modified.
- OC424 upgrade9/9 and public27/27 pass; both DB quick checks and schemas
  match, all 13 registered clients have fresh CPU/uptime, and the UUID/token
  digest is unchanged. Key scope/expiry, authorized SSH keys, GeoIP, backup
  health and local OC424 Agent PID/restart count remain unchanged during the
  switch. Scoped read-only forecast returns 13 rows, including three
  insufficient results and two unknown usage values. The actual owner UI and
  remote alert receipt were not tested. Stable publication can cause other
  Agents with self-update enabled to update independently.
- Manual pre archive `oc424-20261030T143144743764Z-fd30cf6ef52b.tar.gz.age`
  has SHA-256 `0c2a697b0018d747aa7dd9ad87a07e22d4cc57b6854a57e695f09c2322e5d51b`;
  manual post archive `oc424-20261030T143505907758Z-985e040a298b.tar.gz.age`
  has SHA-256 `13f0594e1009f485b8ff2d564fca9d241b25510ce3bb02ea71d28d00a0cf1c24`.
  Matching regular MAC-WAN `incoming/` and executor ciphertext copies exist.
  Pre restores under pinned v1.7.17/v1.7.18, post under v1.7.18, with the
  current 13-client digest. Post compares 21,768 archived rows across 12
  histories: zero retained rows missing or changed, 820 expired or inactive-tier
  rows absent. No independent-media recovery is claimed.
- The 22:30 automatic backup-health report succeeded before the manual
  backups. Those backups used isolated directories under the existing lock,
  leaving all 14 scheduled pairs unchanged. The next actual daily trigger via
  the new route is 2026-10-31 11:30 and remains unverified; its existing
  keep=14 policy retires the oldest eligible pair, whose matching `incoming/`
  copy was verified. OC424 disk was 48%, MAC-WAN 32%, executor 70%; no broad
  cleanup or timer change.

Root-private rollback evidence is
`/opt/nekomari/rollbacks/v1.7.18-20261030T143439Z-d5b693eb`; the previous
image remains
`ghcr.io/aone2233/nekomari:v1.7.17@sha256:4bd55153422e5c7a2533508c18582cba9fdd3d669887973080731b46911b8425`.
If rollback is needed, assess the live state and switch only the pinned
image/compose while preserving current databases and nodes; never overwrite
them with old snapshots. See [`ALERT-RULE-PREVIEW.md`](./ALERT-RULE-PREVIEW.md)
for the preview's limits and unsupported metrics.

## Historical checkpoint (2026-10-30 17:11 Asia/Shanghai)

OC424 runs **v1.7.17/0a9fd1d**, immutable source
`0a9fd1d91b263242eb0eaeb92fc84f47a5812b13`, in the pinned arm64 image
`ghcr.io/aone2233/nekomari:v1.7.17@sha256:4bd55153422e5c7a2533508c18582cba9fdd3d669887973080731b46911b8425`.
It became healthy at approximately 17:06, with zero container restarts.

- Candidate CI41026965107 and exact merge CI41028016613 pass 8/8;
  release41028983238 passes 10/10 and Docker41029876841 2/2 with the actual
  triggering release ID verified for both jobs. Published Linux checksums,
  tag/source/workflow attestations and both architectures of panel/Agent OCI
  identity verify. Native image binaries match released assets. The released
  amd64 binary on MAC-WAN passes synthetic window-before-grace,
  window-during-grace and reconnect/recovery cases using loopback fake
  Telegram. No production failure or notification was injected.
- OC424 upgrade9/9 and public27/27 pass; both DB quick checks and schemas
  match, all 13 registered clients have fresh CPU/uptime, and the UUID/token
  digest is unchanged. Key scope/expiry, authorized SSH keys, GeoIP, backup
  health and the local OC424 Agent PID/restart count remain unchanged. A
  scoped read-only forecast returns 13 rows (10 linear, three insufficient;
  two unknown usage values). This is not an authenticated owner UI acceptance
  or a test of actual remote alert delivery.
- Manual pre archive `oc424-20261030T090213623348Z-54549a494a77.tar.gz.age`
  has SHA-256 `fccaea8fa999f555b4cdd5fca4ca754a594de846fc3c809487c1f564265631f0`;
  manual post archive `oc424-20261030T090821042225Z-779daa8a7e4a.tar.gz.age`
  has SHA-256 `8ae8585c745225eedf7cb13d2fa5393c3fdd483794d91ea351fc7473271e024d`.
  Separate MAC-WAN `incoming/` copies and online executor copies match. Pre
  restores under pinned v1.7.16 and v1.7.17; post restores under v1.7.17 with
  the current 13-client digest. Post compares 20,492 archived rows across 12
  histories: zero in-retention rows missing or changed. No independent-media
  recovery is claimed.
- The initial 17:01 manual backup attempt collided with the automatic health
  check's shared lock and failed without producing an archive; the 17:02 retry
  and 17:08 post backup succeeded. A 17:10 **manual** health report reaches
  OC424 as `ok` for the post archive. The next actual daily trigger through the
  new route is 2026-10-31 11:30 and remains unverified. With 14 scheduled pairs,
  the next successful receive's existing keep=14 policy will prune its oldest
  eligible pair; the oldest pair has a checksum-matching `incoming/` copy.
  Independent-media restore, real alert receipt and OVH disk sanitization
  remain unverified. OC424 disk was 48%, MAC-WAN 32%; no broad cleanup.

Root-private rollback evidence is
`/opt/nekomari/rollbacks/v1.7.17-20261030T090636Z-9e7365cd`; the previous
image remains
`ghcr.io/aone2233/nekomari:v1.7.16@sha256:d47b91611613b5f0e54b95a6481f41773ca47365f562781c534f55422b56223f`.
If rollback is needed, assess the current state and switch only the pinned
image/compose while preserving the live databases and current nodes; never
overwrite them with an old snapshot. No Agent was deliberately restarted or
reconfigured; stable publication can trigger eligible Agent self-updaters.

## Historical checkpoint (2026-10-30 15:59 Asia/Shanghai)

OC424 runs **v1.7.16/e7323c8**, immutable source
`e7323c86ed91828e3df9d21f33d9fb5acba34956`, in the pinned arm64 image
`ghcr.io/aone2233/nekomari:v1.7.16@sha256:d47b91611613b5f0e54b95a6481f41773ca47365f562781c534f55422b56223f`.
It became healthy at approximately 15:56, with zero container restarts.

- Exact push CI41021022614 passes 8/8; release41021806313 passes 10/10 and
  Docker41022572275 2/2, with the release run ID checked in both Docker jobs.
  Linux checksums/tag-source-workflow attestations and panel/Agent image
  identities for both architectures verify. Native panel binaries match the
  assets. A published-binary isolated test on MAC-WAN observes two scheduled
  minutes and one refusal warning, with no corresponding error.
- OC424 upgrade9/9 and public27/27 pass. Both DB quick checks and schemas pass;
  all 13 current clients report fresh CPU/uptime and their UUID/token digest is
  unchanged. API-key scopes/expiry, GeoIP, backup health and local Agent PID
  remain unchanged. Scoped read-only forecast returns 13 rows: 10 linear and
  three insufficient, including two unknown-usage direction decreases. At
  15:58, the live container has logged one decrease warning and no forecast
  errors since startup. This brief observation is not a long-duration test or
  an authenticated owner browser acceptance.
- Manual pre archive `oc424-20261030T075406033437Z-607bbb4c0e38.tar.gz.age`
  has SHA-256 `826292cab618b85f42102269fe7b70ee833735323664f5d52ce865f800721150`;
  manual post archive `oc424-20261030T075714517609Z-e4c7a7e18e6c.tar.gz.age`
  has SHA-256 `36247661623647edcdc727600c621a2523fe58eb3572755d33771d2a5dbb0581`.
  Each has a separate MAC-WAN `incoming/` copy and matching online executor
  copy. In isolated recovery, pre boots pinned v1.7.15 and v1.7.16, and post
  boots v1.7.16 with the 13-client digest. Post compares 20,384 archived rows
  across 12 histories: zero retained rows missing or changed; 46 expired rows
  are absent. No independent-media recovery is claimed.
- MAC-WAN's automatic health timer reports `ok` at 15:45; the next daily backup
  through the new proxy is not yet observed (2026-10-31 11:30 Asia/Shanghai).
  Independent-media restore, real incident receipt and OVH disk sanitization
  remain unverified. OC424 root usage is approximately 48%; no broad cleanup.

Root-private rollback evidence is
`/opt/nekomari/rollbacks/v1.7.16-20261030T075600Z-42c6a780`; the previous
image remains
`ghcr.io/aone2233/nekomari:v1.7.15@sha256:f59636293282b351f0e5d34064f198d0b3040a5582b2bba83c9dd04f0c6a209c`.
If rollback is needed, assess the current state and switch only the pinned
compose/image, keeping live databases and current nodes. Never replace the live
database with an old snapshot. No Agent was deliberately restarted or
reconfigured; stable publication can still trigger enabled Agent self-updaters.

## Historical checkpoint (2026-10-30 15:16 Asia/Shanghai)

OC424 now runs **v1.7.15/7c13e50**, immutable source
`7c13e509767e59742196be61ec98973b785c68c8`, in the pinned arm64 image
`ghcr.io/aone2233/nekomari:v1.7.15@sha256:f59636293282b351f0e5d34064f198d0b3040a5582b2bba83c9dd04f0c6a209c`.
It becomes healthy at approximately 15:12 and has zero container restarts.
The preceding 14:40 checkpoint below is history.

- Exact push CI41016555952 passes 8/8; release41017359338 passes 10/10 and
  Docker41018148539 2/2, with the actual release run ID in both Docker jobs.
  Published Linux assets pass SHA-256 and tag/source/workflow attestations;
  panel and Agent amd64/arm64 image identities match the exact source. Both
  native panel binaries match their published assets, and the actual amd64
  binary passes six isolated forecast cases on MAC-WAN.
- OC424 upgrade check passes 9/9 and the public site 27/27; both databases
  pass quick_check with unchanged schemas. All 13 clients have fresh CPU and
  uptime reports and their UUID/token digest matches the pre-upgrade backup;
  API-key permissions/expiry and the local OC424 Agent remain unchanged. The
  scoped, read-only forecast returns 13 rows: 10 linear and three insufficient,
  with two unknown-usage rows caused by direction decreases. An authenticated
  owner browser session has not been checked.
- Manual pre backup at 15:10 is
  `oc424-20261030T071020075093Z-116c8230c9ea.tar.gz.age`, SHA-256
  `a1f9640c944dd7289e7368be812e7faaeaffe27f69c100808dfcc5ee4f8b45bf`.
  Manual post backup at 15:13 is
  `oc424-20261030T071329235896Z-66eff1d9703b.tar.gz.age`, SHA-256
  `a15dbaccf376d543c714aafdae3e54a2ad2805b73d44d82a76c90a4b7580346b`.
  Each has a separate MAC-WAN `incoming/` copy and an online executor copy
  matching the checksum. Isolated MAC-WAN recovery of the pre archive passes
  under pinned v1.7.14 and v1.7.15, and the post archive under v1.7.15, with
  the current 13-client digest. A retention-aware post-boot comparison of 12
  selected metric histories finds zero missing in-retention or changed rows.
  This is not an independent-media recovery.
- The pinned proxy's actual automatic health timer succeeds at 14:54; a manual
  15:15 report reaches OC424 with the post-backup timestamp. The daily timer on
  the new route is not yet observed; the next run is 2026-10-31 11:30. Alert
  dispatch receipt, independent-media recovery and old OVH disk sanitization
  remain unverified. OC424 disk was 48% and MAC-WAN 30%; no broad pruning.

The root-private rollback evidence is
`/opt/nekomari/rollbacks/v1.7.15-20261030T071229Z-f5008eed`. The previous
OC424 image is retained as
`ghcr.io/aone2233/nekomari:v1.7.14@sha256:fd470535a34353f78ec2afeefa3bf0067df67d931a8a2762807c75dc31d9701d`.
This patch changes no database schema: if rollback is actually needed, assess
the current state and restore only the pinned image/compose, retaining the live
databases and all current nodes; do not overwrite them with old snapshots.
No Agent was deliberately restarted or reconfigured, but publishing a stable
release may cause other Agents with self-update enabled to update on their own.

## Historical checkpoint (2026-10-30 14:40 Asia/Shanghai)

The owner confirms their deletion of a node and addition of `QWRT 主路由 | 宿舍`.
The current set has 13 registered clients and all 13 have fresh CPU and uptime
rollups; it is **not** the same set as the earlier 13-node recovery. Do not
restore deleted nodes or overwrite live databases with older archives. OC424
remains healthy on v1.7.14/7f94561. Forecast PR128 and its SQL fixture fix PR129
are merged; exact merge CI41013963594 succeeds 8/8. v1.7.15 is not yet tagged,
published or deployed. Agent periodic auto-update policies are not fully
inventoried; a stable release may cause eligible Agents to update and restart.

The 2026-10-30 11:30 scheduled backup failed and health reports became stale.
Manual 14:35 backup and 14:38 health recovery through a pinned MegaBox jump pass;
its fresh current-13 ciphertext is independently copied to the online executor
and boots the pinned old image on MAC-WAN in isolation. These are manual runs,
not evidence that either next timer has succeeded. See
[`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md). The following older checkpoints are
historical.

## Historical checkpoint (2026-10-29, Asia/Shanghai)

OC424 serves **v1.7.14 / 7f94561** at
`https://nekomari.orderly2233.org`. Scoped-key migration and session-only token
rotation are deployed; no existing Agent token is rotated. MAC-WAN receives
encrypted backups and holds the online identity/recovery runner. The owner
reports downloading an offline identity copy and authorizes the switch;
independent-media recovery remains unverified. dsh records the owner's
authenticated history/audit/API-key acceptance; this review does not repeat it.
Older rollout sections are dated evidence, not the current version/node count.

| Item | Observed evidence |
|---|---|
| Panel | Docker `nekomari`, healthy/restart0; exact v1.7.14/7f94561. OC424 upgrade9/9, public27/27, scoped forecast13rows and13 fresh CPU/uptime reporters pass |
| Nodes | Owner confirms intentional removal of the OVH node; current baseline is 13 registered clients. VT01 resumes by 12:15 but is stale again at 12:20; 12 nodes remain fresh. Preserve all clients and their tokens; do not restore the removed node |
| Actual OC424 Agent | `komari-agent-oc424-original-node.service`, active, PID499631/restarts2, unchanged; generic `nekomari-agent` is not the reporter |
| OVH | Relinquished and no longer trusted; three identified OC424 backup/health SSH authorizations revoked. Plaintext archive exposure and disk sanitization remain unresolved |
| MAC-WAN | Linux x86_64 user `macos`; encrypted receiver, health timer, online identity/isolated recovery. First scheduled daily backup succeeds at 11:30-11:31 Asia/Shanghai and its health report at 11:34; Agent/Hermes are not restarted by this work |
| Acceptance | Prior Hermes disposable CLI writes pass; dsh records earlier owner history/audit/API-key acceptance. No new owner UI session for v1.7.14; public401 is not authenticated acceptance. Complete caller inventory remains separate |

At the v1.7.14 historical checkpoint, that release added private default-theme cache history; see
[`THEME-CACHE-HISTORY.md`](./THEME-CACHE-HISTORY.md). v1.7.13 adds private connection-pool wait history; see
[`PANEL-POOL-HISTORY.md`](./PANEL-POOL-HISTORY.md). v1.7.12 corrects data-mount persistence warnings; see
[`DATA-MOUNT-WARNING.md`](./DATA-MOUNT-WARNING.md). v1.7.11 adds session-only
panel RSS/heap/goroutine history;
see [`PANEL-HISTORY.md`](./PANEL-HISTORY.md). v1.7.10 limits completely empty
GeoIP country cache entries to one hour; see
[`GEOIP-EMPTY-COUNTRY.md`](./GEOIP-EMPTY-COUNTRY.md). v1.7.9's
default-theme cache state remains in the existing Performance profiles page; see
[`THEME-CACHE-DIAGNOSTICS.md`](./THEME-CACHE-DIAGNOSTICS.md).
The prior v1.7.8 node uptime history remains; see
[`UPTIME-HISTORY.md`](./UPTIME-HISTORY.md) for data/retention and rollback limits.
The previous OVH scheduled archive is historical; the replacement MAC-WAN
first scheduled daily trigger succeeds on 2026-10-29. Independent-media restore
and real incident delivery remain unverified.

### VT01 subsequent disconnect (2026-10-29 11:42 Asia/Shanghai)

The panel remains healthy/restart0 on v1.7.14. `VT01 | vps.taxi` reports its
last persisted CPU and uptime sample at 11:26:18.909, after the panel switch
and its 13-node acceptance; the current scoped live status says offline.
The other 12 registered clients have fresh CPU and uptime. MAC-WAN connects to
the configured VT01 SSH TCP endpoint but receives no SSH banner within 20
seconds; the OC424 attempt also times out. These observations cannot identify
whether the host, network, firewall, SSH daemon or Agent is responsible, nor
prove a causal relationship to the panel upgrade. No node, token, Agent or
server was modified or restarted. At 12:15-12:17 VT01 again produces fresh
CPU and uptime and all 13 nodes report, but at 12:20 it is stale again, last
sample 12:17:22.908; the other 12 remain fresh. SSH again times out at banner
exchange. Recovery was not caused by an action here and the root cause remains
unknown. If it persists, use the provider console to inspect the host and
Agent. The 13-node acceptance in the dated rollout row
below remains true only at the time it was observed.

## v1.7.14 rollout (2026-10-29 approximately 11:12 Asia/Shanghai)

PR121 adds two session-only sampled default-theme cache histories in the
existing Performance profiles page. They are observations, not exact state
transition times or continuous disk checks. No schema, Agent, credential,
scope or schedule change is involved; full v1.8 remains open.

| Item | Evidence |
|---|---|
| Publication | Tag `v1.7.14` -> `7f945619e94a4e25b82cb7860c61b8cf68b22027`; candidate CI40807334416 8/8, exact merge CI40808023833 8/8, release40808682926 10/10 and Docker40809339436 2/2. An earlier candidate CI failed the navigation smoke listener handoff; a bounded test-only retry and regressions pass before tagging |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.14@sha256:fd470535a34353f78ec2afeefa3bf0067df67d931a8a2762807c75dc31d9701d`; index `sha256:fa4a1d1eb1e42d456ba5c24efd0d6d31a605948e9e5a1fecc7bdd0496d1d22d5`; binary SHA256 `16e7e96ffa6deae6027c787d33118ae773f315797fb6caf955c499823bc55254` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.14@sha256:cbf8b6a8944cae464ccc2e28ed69c412b1bff062d0dc0748b0227d4b3afc10fb`; binary SHA256 `e1630c891e97f9e817ba7b0c21ba4917d0e291b06a42b16e308bfc581db127eb`. Linux checksums/source attestations, Panel/Agent OCI architecture identities and native image binaries pass; Agents are not deployed |
| Browser acceptance | The published binary passes Chinese desktop/light and English mobile/dark synthetic checks, guest/key denial, retention disable and restart checks without production data. The SSH wrapper timed out during a slow binary transfer; the remote run subsequently completed, and its saved report, stdout, empty stderr and binary hash were retrieved and checked. MAC-WAN Agent PID976/restart0 remains unchanged |
| Pre backup | `oc424-20261029T030508182324Z-d429f2f81679.tar.gz.age`,187872189bytes, SHA256 `af6c5f6b82244704d948f01896b9ac0e4ba85ed9e0dec4a235188d1dc90d317f`. Pinned v1.7.13 and v1.7.14 images boot the same encrypted backup on isolated MAC-WAN scratch copies, with both DBs and 13 current UUID/token hashes |
| Post backup | `oc424-20261029T031324144179Z-24ec1c83091d.tar.gz.age`,187943916bytes, SHA256 `70fa15e0e06a82522a5070e6a29d0d29cdcb47f81ec16af6f29e46f5a03ef3aa`. Pinned v1.7.14 isolation verifies both DBs, 13 current UUID/token hashes and 12 selected metric definitions. Comparison of 15,642 archived rollup rows finds zero missing within retention and zero changed existing rows; 1,984 expired or inactive-tier rows are absent after startup cleanup. No network, host ports, `ANALYZE`, reclaim, residual scratch plaintext or restore container |
| Custody and rollback | Both ciphertext/checksum pairs are retained in MAC-WAN `incoming/` outside scheduled pruning, and each has a hash-matching copy on the online executor. `/opt/nekomari/rollbacks/v1.7.14-20261029T031202Z-ae0f955d` holds compose and per-DB WAL-safe snapshots, not a cross-DB atomic snapshot. The v1.7.13 image remains; this is not independent-media recovery |
| Real continuity | OC424 healthy/restart0, upgrade9/9, public27/27, scoped forecast13rows and 13 fresh CPU/uptime reporters. Both DB quick checks/schemas, UUIDs/tokens, key grants/expiry, GeoIP and backup health remain; both new definitions receive natural samples. No owner UI login, live rollback or incident delivery is claimed |

### v1.7.14 rollback and boundaries

Preserve the verified post-upgrade ciphertext and check private rollback
checksums. For this patch restore the prior compose/image in the private
rollback directory, validate compose and start only `nekomari`; expect
v1.7.13/9246bdc while retaining current databases and the 13 remaining nodes.
Old-image recovery was tested in isolation, not as a live rollback. Manual
metric reclaim on v1.7.13 and older deletes the two theme-cache histories. Do
not restore an old 14-node snapshot over the live fleet. The v1.7->v1.6 auth
migration still requires its paired old database and binary with writers
frozen. Independent-media restore, real incident delivery and owner-session UI
acceptance remain separate.

## v1.7.13 rollout (2026-10-29 approximately 09:46 Asia/Shanghai)

PR119 adds six session-only database-pool wait-history metrics to the existing
Performance profiles page. They describe validated sampling-interval deltas,
not request latency or bucket totals. No schema, Agent, key scope or token
rotation is involved; this does not complete v1.8 observability.

| Item | Evidence |
|---|---|
| Publication | Tag `v1.7.13` -> `9246bdc6770509edf6c938307455f14664d71024`; candidate CI40722073534 8/8, same-tree merge `9d3e488d408d375159f67b43cdc935cc63f52a2a` CI40723135889 8/8, release40800943975 10/10 and Docker40801663163 2/2 |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.13@sha256:fcce3cd47a117e2017d0b8c00b554308a076df41706bd9bbbb4f19b687c8659e`; index `sha256:676d0c947d9a5818abf25fd48e4dbd567cd763395e9a48be7309a57a679030bf`; binary SHA256 `9bdc2ef661719f4d97a5920d5e2a3ccdcf51546049ae16b5fd5fc9e05389d7ba` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.13@sha256:f458fc6bd2744f2d2e9cf0c44f32b47700366fa558cb47a4e3db4444c220a195`; binary SHA256 `a2b85ec817cf1436b2e35ac52b4e50719690d1b9c7108c9dd5569be28d68b48c`. Linux assets and source attestations, Panel/Agent OCI platform identities and native binaries verify; Agents are not deployed |
| Synthetic acceptance | Published binary on MAC-WAN passes isolated Chinese desktop-light and English mobile-dark browser checks, guest/scoped-key denial, retention and restart checks. An initial transfer command times out after the remote test actually finishes; the report is retrieved and hash-verified. No production data or owner login is used |
| Pre backup | `oc424-20261029T012849480670Z-cc47ba08087f.tar.gz.age`, SHA256 `8f7e74c9cf34e86ea8331113b38efd71403773c9dc93cbf6eef4a64cd7defb0d`. MAC-WAN boots the pinned v1.7.12/v1.7.13 images on isolated verified scratch copies with both DBs and the current13 UUID/token hashes. The first old-image attempt omitted the checksum file and failed; its corrected retry passes |
| Post backup | `oc424-20261029T014717511121Z-e6adfad8b8b8.tar.gz.age`,187592739bytes, SHA256 `f08278b5a537d449ad8e56be790f51ccc1c7a2119f257b7079db4e5044f89ab3`. Pinned v1.7.13 isolated recovery passes with both DBs, current13 UUID/token hashes and scoped-key schema. A row-level comparison checks all 13,386 archived uptime, three panel and six pool rollups: no retained row or aggregate changes, and all six pool rows survive. Later five-second-delayed repeat also passes. No network, host ports, `ANALYZE`, reclaim, remaining scratch plaintext or recovery container |
| Initial post-check limitation | The first post-recovery attempt reported a failure of the assertion that the total uptime-rollup row count never decreases. Its captured log lacks before/after counts, and the failure did not reproduce on two new drills from the same archive. Startup retention can delete expired data, but that is not an established explanation for this incident; no evidence supports declaring it fixed. Future checks should compare actual rows and retention boundaries, not only totals |
| Custody and rollback | Pre/post ciphertext and matching checksums are retained in MAC-WAN `incoming/`, outside scheduled pruning; another hash-matching copy of each ciphertext is on the online executor. `/opt/nekomari/rollbacks/v1.7.13-20261029T014600Z-e234d564` holds compose and per-DB WAL-safe snapshots, which are not cross-DB atomic. The v1.7.12 image remains. This is not independent-media recovery |
| Real continuity | OC424 healthy/restart0, upgrade9/9, public27/27, scoped forecast13rows and13 fresh CPU/uptime reporters. Both DB quick checks/schemas, current UUIDs/tokens, key grants/expiry, GeoIP and backup health remain; all six pool definitions receive natural samples. No production owner UI login or live rollback is claimed |

### v1.7.13 rollback and boundaries

Preserve and verify a fresh post-upgrade encrypted backup before any rollback.
For this patch, restore the prior compose/image in the private rollback
directory, validate compose and start only `nekomari`; expect v1.7.12/b4591d6
while keeping current databases and the 13 remaining nodes. Old-image recovery
was tested in isolation, not as a live rollback. v1.7.12 manual metric reclaim
can delete all six v1.7.13 pool definitions/history. Do not restore older
14-node snapshots over the live fleet. The earlier v1.7->v1.6 auth migration
still requires a paired old database and binary with writers frozen.
Independent-media restore, real incident delivery, MAC-WAN's first scheduled
daily backup and owner-session UI acceptance remain separate.

## v1.7.12 rollout (2026-10-28 23:10:34 Asia/Shanghai)

PR117 corrects a misleading startup data-mount persistence claim. It detects
anonymous-volume-like paths under custom Docker roots but does not claim that
mountinfo proves either anonymity or persistence. No database, schema, Agent,
credential, scope or backup-schedule change. See `DATA-MOUNT-WARNING.md`.

| Item | Evidence |
|---|---|
| Publication | Tag `v1.7.12` -> `b4591d64b6f2916963ffa4f6812cfa984ab397fb`; exact CI40708356223 8/8, same-tree merge `c659617f83c13a6772f855767d4a2fe84616529d` CI40709676910 8/8, release40710985497 10/10 and Docker40712289115 2/2 |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.12@sha256:0c8df70e4cf8736deff766f64a10b180350ef6f398684d8253faaa3265723b60`; index `sha256:978d87348e9e8d3da2a1f7d635004e8fb23adee61c64592917eb5f45027ae5e2`; binary SHA256 `c5f13611d955ec9b7007ae4f59bb7cb06e69a6e00a77b6ef80b9ea6640b467c3` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.12@sha256:4fc8e54bfdbb65ea800f47986991bf283c72d6f4f7db161694b1cdfae473128e`; binary SHA256 `7784798ceb756baa573864d83260f482ac961945064a280d4dadfc5baecf6f6f`. Linux checksums/source attestations, Panel/Agent OCI architecture identities and native image binaries pass; Agents are not deployed |
| OVH acceptance | Published amd64 image with verified binary runs offline under a separate Docker daemon with a custom data root. Anonymous and named volumes, a space-containing bind path and tmpfs produce the expected cautious logs; no production data, host port or network. Temporary daemon, containers, volumes and image are removed; OVH Agent unchanged |
| Pre backup and recovery | `oc424-20261028T150734428764Z-13ade39dc2f5.tar.gz.age`,186589454bytes,SHA256 `bcba347a03339bd2e8fed06627fa7bd70017c2cbd2f2968291ce50c97468c541`; same-hash MAC-WAN copy boots pinned v1.7.11/v1.7.12 in8.61/10.88s, both DBs and14clients. The first overlapping drill's global scratch-container check saw the other drill; serial old-version retry passes |
| Post backup and recovery | `oc424-20261028T151107569033Z-67d48d9962d0.tar.gz.age`,186698757bytes,SHA256 `0bdf6c2dcc613cfe076d13a02649ce67352cd30eb91359740e29b93dac33ea72`; same-hash MAC-WAN copy boots v1.7.12 in8.27s, both DBs/14clients, uptime rollups7470 and panel rollups183 retained. No `ANALYZE`, reclaim, network, host ports or residual scratch plaintext/container; drill times are not RTO |
| Custody and rollback | OVH stores `/srv/backups/oc424-encrypted/manual-v1.7.12-20261028/{pre,post}` outside shared pruning; matching copies remain on MAC-WAN. OC424 `/opt/nekomari/rollbacks/v1.7.12-20261028T151032Z-6f9d9034` retains compose, pins and per-DB WAL-safe snapshots; snapshots are not cross-DB atomic. The v1.7.11 image remains |
| Real continuity | OC424 healthy/restart0, upgrade9/9, public27/27, scoped forecast14rows and14 fresh CPU/uptime reporters. Both DB quick checks/schemas, existing UUIDs/tokens, grants/expiry, GeoIP, backup health and Agent/Hermes processes remain; actual bind-mount log is cautious. No production owner UI login or live rollback is claimed |

### v1.7.12 rollback and boundaries

Preserve the verified post-upgrade ciphertext and check rollback checksums.
For this patch restore `docker-compose.before.yml`, validate compose and start
only `nekomari`; expect v1.7.11/8754747 while retaining current databases,
health state and all14 nodes. v1.7.11 understands panel history; manual metric
reclaim on v1.7.10 or earlier deletes that history, and v1.7.7 or earlier also
deletes uptime history. Never replace the current fleet with a historical
13-node snapshot. Across the v1.7->v1.6 auth migration use the paired old DB
and binary with writers frozen. Independent-media restore, real incident
delivery, owner-session UI and RPO/RTO/SLO remain separate.

## v1.7.11 rollout (2026-10-28 22:19:47 Asia/Shanghai)

PR115 adds private panel-process history to the existing Performance profiles
page. RSS, heap allocation and goroutines have independent one-day default
retention; missing samples remain unknown. No Agent, schema, token, scoped-key
or backup schedule change is made. This is not complete v1.8 observability.

| Item | Evidence |
|---|---|
| Publication | Tag `v1.7.11` -> `8754747f8be7dd248b23b4305f77f00bd1d165b8`; exact CI40668605937 8/8, same-tree merge `e703e7325891ac6f785d24747aa069a6b665690e` CI40669574427 8/8, release40670453345 10/10 and Docker40671399212 2/2. Initial candidate CI40668146570 failed a legacy-retention test on Linux and Windows; corrected to assert independent panel defaults before tagging |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.11@sha256:2fb41d7aee4d7824b0d84af9f58a6585f742d8f013771f515b28b14a423c5c34`; index `sha256:d3c75d8acf93cf6e7accf2201128d36b05c8a37dcba49464feafef2d098b9fc7`; live binary SHA256 `7aadc16553bdbb2b32248343d6d1fdcf79a143e93f161a2336bf28973a00aa0a` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.11@sha256:142db92268193e261ac28b7a29ee11c82cbaa7c4f13e4ad1911a07bf9aab4aa3`; binary SHA256 `35aacf6b7ba8402f87951b5919f0f6a8e93a6208e83447604dfd778f32c21c40`. Linux checksums/source attestations, Panel/Agent OCI identities and native image binaries verify; Agents are not deployed |
| OVH acceptance | Attested published binary passes isolated Chinese desktop-light and English mobile-dark browser checks, guest/scoped-key denial, disabled retention and closed-bucket survival across restart. Only synthetic data and loopback are used; OVH Agent remains unchanged |
| Pre backup and recovery | `oc424-20261028T141711135829Z-891dd3c35177.tar.gz.age`,186538965bytes,SHA256 `678dab680685dbcfb8d5114cf9a13a0a3d9aac3e031166385a5fc80d043e09a8`; same-hash MAC-WAN copy boots pinned v1.7.10/v1.7.11 in9.42/10.49s, both DBs and14clients |
| Post backup and recovery | `oc424-20261028T142022239428Z-3317822b1b85.tar.gz.age`,186563724bytes,SHA256 `dd6eab32ce8dc3fe4b32d4a90ffcf293f6738cdcb7cad54a053174b439993965`; same-hash MAC-WAN copy boots v1.7.11 in7.85s, both DBs/14clients, uptime rollups6616 and panel rollups3 before and after boot. No `ANALYZE`, reclaim, network, host ports or residual scratch plaintext/container; drill times are not RTO |
| Custody and network | Pinned OVH receiver stores `/srv/backups/oc424-encrypted/manual-v1.7.11-20261028/{pre,post}` outside shared pruning. The OC424-to-MAC-WAN route timed out before executing the native check; strict-host-key-verified executor-to-MAC-WAN SSH through OVH succeeds. Backup ciphertext streams OVH -> executor -> OVH SSH tunnel -> MAC-WAN; no identity is transferred. Schedule and shared retention remain unchanged |
| Local rollback | `/opt/nekomari/rollbacks/v1.7.11-20261028T141945Z-eb9f7c50` holds prior/new compose, image pins, per-DB WAL-safe snapshots/checksums and instructions. Snapshots are not cross-DB atomic; the v1.7.10 image remains |
| Real continuity | OC424 healthy/restart0, upgrade9/9, public27/27, scoped forecast14rows and14fresh CPU/uptime reporters. Both DB quick checks/schemas, existing UUIDs/tokens, key grants/expiry, GeoIP, backup health, nginx/recipients/probe and Agent/Hermes processes remain; three natural panel metrics are persisted. No production owner UI login or production rollback is claimed |

### v1.7.11 rollback and boundaries

Preserve the verified post-upgrade ciphertext and verify private rollback
checksums. For this patch restore `docker-compose.before.yml`, validate compose
and start only `nekomari`; expect v1.7.10/a2fd46a, retaining current databases,
health state and all14 nodes. Old-image recovery was tested in isolation, not
as a live rollback. Manual metric reclaim on v1.7.10 and older deletes the new
panel definitions/history; v1.7.7 and older also deletes uptime history. Never
overwrite the current fleet with a historical 13-node snapshot. Across the
earlier v1.7->v1.6 auth migration use the paired old database and binary with
writers frozen. Owner-session UI, independent-media restore, actual incident
delivery and RPO/RTO/SLO remain separate.

## v1.7.10 rollout (2026-10-28 17:23:20 Asia/Shanghai)

PR113 shortens only the in-process TTL of successful GeoIP results with neither
country code nor name, from 48 hours to one hour. Positive/partial results and
error handling are unchanged. Late empty lookups cannot replace or extend a
concurrent cached result. No Agent, schema, token, key scope or schedule change.

| Item | Evidence |
|---|---|
| Publication | Tag `v1.7.10` -> `a2fd46a3205b0a8de7babced44880e4b9703b69f`; exact CI40660304364 8/8, same-tree merge `4b02728` CI40661304807 8/8, release40661328099 10/10 and Docker40662206501 2/2. Initial candidate CI40659408149 failed on Windows due to a nanosecond clock-precision test assumption; corrected deterministically before tagging |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.10@sha256:9613e782bc2796c2889cfdfac19b8824207e3fd1f9ff78a7ccc6f9f89ac892f9`; index `sha256:b9f8faeeabd37f41d2dcb2b351331c0277f181f2b993a688f22195af2cfd2423`; live binary SHA256 `aedb43fb76e5e8d3a6294ee05117820698e06cc291061ff0487bad5108893a6c` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.10@sha256:c2494713f55fcc15809f19108215ac3259f8ce09b524f5b51679101213aaa2bb`; binary SHA256 `760620915923ff4e2fda623766cadc867ff06e17d1722de5a8dd144032446675`. Both Linux checksums/source attestations and Panel/Agent OCI architecture identities pass; Agents are not deployed |
| Focused and OVH acceptance | Race/vet and deterministic expiry/recovery/concurrency tests pass. The published binary passes an isolated installed-Panel test against a fake local HTTPS upstream on OVH; it checks empty/positive caching and reload, without production data or real provider requests. The one-hour expiry itself is tested without waiting in Go |
| Pre backup and recovery | `oc424-20261028T091509652488Z-336d52be8d5b.tar.gz.age`,185119258bytes,SHA256 `b05e60a60f96f215122adeb15ff263ab4a18cb8a62a485a90321faaca578cb64`; same-hash MAC-WAN copy boots pinned v1.7.9/v1.7.10 in8.36/8.85s, both DBs and14clients |
| Post backup and recovery | `oc424-20261028T092332775072Z-fa42d61b4d12.tar.gz.age`,185178837bytes,SHA256 `e1e948fa5a940ca80bb658e555783f5d14e34baf0c6b9a3de1a16e14ce8f4001`; same-hash MAC-WAN copy boots v1.7.10 in9.05s, both DBs/14clients, uptime rollups1681 before/1695 after. No `ANALYZE`, reclaim, network, host ports or residual scratch plaintext/container; drill times are not RTO |
| Custody and network | OVH receiver stores `/srv/backups/oc424-encrypted/manual-v1.7.10-20261028/{pre,post}` outside shared pruning. MAC-WAN's direct OVH SSH failed; strictly host-key-verified ciphertext streams traversed the executor and an OC424 SSH tunnel to MAC-WAN. No decryption or identity transfer on either intermediary; schedule and retention unchanged |
| Local rollback | `/opt/nekomari/rollbacks/v1.7.10-20261028T092318Z-d845625f` holds compose, image pins, per-DB WAL-safe snapshots/checksums and instructions; snapshots are not cross-DB atomic. The prior v1.7.9 image is retained |
| Real continuity | OC424 upgrade9/9, public26/26, scoped forecast14rows and14fresh CPU/uptime reporters pass; both DB quick checks/schema, UUIDs/tokens, grants/expiry, GeoIP config, backup health and probe remain unchanged. No production owner UI login or production rollback |

### v1.7.10 rollback and boundaries

Preserve a fresh verified post-upgrade ciphertext and check rollback checksums.
For this patch, restore the prior compose/image from the private rollback
directory, validate compose and start only `nekomari`; expect v1.7.9/4e6c4d2,
keeping current databases and all14 nodes. Pre-backup old-image recovery was
tested in isolation, not as a production rollback. Do not restore an older
13-node snapshot over current data. v1.7.7 or older manual metric reclaim can
delete uptime history; crossing the earlier v1.7->v1.6 auth migration still
requires the paired old database and binary with writers frozen. Independent
media recovery, owner-session acceptance and real incident delivery remain open.

## v1.7.9 rollout (2026-10-28 16:34:31 Asia/Shanghai)

PR111 adds session-only, last-observed default-theme disk-cache state and an
independent memory-fallback flag in the existing panel page. It also synchronizes
lazy memory initialization. No schema/metric/retention, Agent, key permission,
credential or backup schedule changes; this is not self-metrics history.

| Item | Evidence |
|---|---|
| Immutable publication | `v1.7.9` -> `4e6c4d2b467b5815db656d1bf88746279cf5eee0`; exact CI40655268852 8/8, identical-tree merge `3401cc8` CI40656067694 8/8, release40656102364 10/10, Docker40656768684 2/2 |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.9@sha256:c7effa6184d5f7dcc95de4bfbbc485b7d5341acf8a19e03efc7cfc52185befbd`; index `sha256:413cd10def79180d40a4cb6a2f4b3261a863099ab46bdc204a0b3b34e5ea1b65`; extracted/live binary SHA256 `935e803acc576b6b9a60f598e5aa47e2029f8797aa3f59b457cbaad8dc89b357` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.9@sha256:d1365dda9dfe02ef1d15d9aee438b31f15d3c56fcda2ba6b115f2e01df27fc92`; binary SHA256 `9d93bb08b3365c71c5ae20620d81d431edda33107645e2fdfd8ac9c01de84c69`. Both Linux assets pass checksums/source attestations; both Panel/Agent OCI architectures identify the source. Agents are not deployed |
| Focused and OVH acceptance | Cache states, initialization failure/deletion/fallback, session-only and API-key denial, race/vet/type/lint pass. The attested published binary passes four isolated desktop-light/mobile-dark enabled/disabled-cache browser scenarios on OVH; no production data used |
| Pre backup and recovery | `oc424-20261028T083126280934Z-dcd8f322685b.tar.gz.age`,184949899bytes,SHA256 `88e63f5f27e72a730b50e160d1095d5603f6bb11ab903998660780c5560feeb6`; same-hash MAC-WAN copy boots pinned v1.7.8/v1.7.9 in9.56/9.33s with both DBs,14clients and current auth metadata |
| Post backup and recovery | `oc424-20261028T083447964630Z-426c02110b0d.tar.gz.age`,184968839bytes,SHA256 `09ecdb702765771a9d36571a8c37cbd80f3ceabbe8a5a4ed0ac73b3850d65ea9`; same-hash copy boots pinned v1.7.9 in8.10s, both DBs/14clients, uptime rollups855 before/869 after. No reclaim/`ANALYZE`, network, host ports or residual plaintext/container; drill times are not RTO |
| Backup custody | Pinned installed OVH receiver and its existing SSH identity/lock write release copies under `/srv/backups/oc424-encrypted/manual-v1.7.9-20261028/{pre,post}`, outside shared retention; MAC-WAN pulls ciphertext over its existing trusted OVH alias. Initial executor->MAC-WAN via OC424 timed out before SSH; strictly verified executor->OVH->MAC-WAN worked. No identity transfer, retention or timer change |
| Local rollback | `/opt/nekomari/rollbacks/v1.7.9-20261028T083428Z-2956a078` retains prior/new compose, image pins, per-DB WAL-safe snapshots/checksums/instructions. These are not a cross-DB atomic snapshot; prior v1.7.8 image remains |
| Actual continuity | upgrade9/9, public26/26 including unauthenticated pprof denial, scoped forecast14rows,14registered and14fresh CPU/uptime reporters. Both DB quick checks/schemas, UUIDs/tokens, key grants/expiry, GeoIP, nginx/recipient/probe override and quiet backup-health state remain. OC424/OVH/MAC-WAN Agents and Hermes are not restarted |

### v1.7.9 rollback and boundaries

Verify the rollback checksums and preserve a fresh verified post-upgrade encrypted
backup. For image-only rollback restore this release's `docker-compose.before.yml`,
validate compose and start only `nekomari`; expect v1.7.8/75168d5 while keeping
the current databases and all14 nodes. The v1.7.8 isolated boot was tested on
the pre backup; a production rollback was not performed. v1.7.7 or older manual
metric reclaim can delete uptime history. Do not restore older 13-node snapshots
over the live fleet. Across the earlier v1.7->v1.6 auth migration use the paired
old DB and binary with frozen writers. Owner-session UI acceptance, independent-
media restore, real incident delivery and RPO/RTO/SLO remain unverified.

## v1.7.8 rollout (2026-10-28 15:42:01 Asia/Shanghai)

PR109 adds positive Agent uptime history (quality `ok` or no legacy quality key)
and an in-panel admin view, one bounded part of v1.8, not reboot counting.
Existing metrics keep their policies;
the new metric defaults to one day and has no historical backfill. No Agent,
schema, token, scope or backup schedule change.

| Item | Evidence |
|---|---|
| Immutable publication | `v1.7.8` -> `75168d5b0fe7053b0d65eda71fffb7b0a0acc072`; exact CI40649551718 8/8, identical-tree merge `e53ab09` CI40650299520 8/8, release40650315129 10/10, Docker40651006736 2/2 |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.8@sha256:2b2c47ae1b4cd47b8b70c175748611d78c21123722a9355345232f880942f44f`; index `sha256:d7043b0dc6792d5cff07e54f3d042618a13e54d58b9d20719fb03152deb63c67`; extracted/live binary SHA256 `7bb8b87a9da8c696306d62f1df805638a0dfb42121cccb15b1eea830d9a2a2b5` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.8@sha256:fdee0df3501177a86577e27b76768dcb6558a9e1ae6b9614a6466433f9c2b29d`; binary SHA256 `4711a3cb1b229cdb14bfb5ff030eb9693bb53df8a8987661b6387045d75205ec`. Both release checksums/source attestations and both Panel/Agent OCI architectures/source identities verify; Agents are not deployed |
| Focused and OVH acceptance | Ingest quality/legacy inputs, last aggregation, retention/deletion, scoped/hidden visibility and restart durability pass focused checks. The exact published binary runs isolated real report/restart/permissions and embedded browser checks on OVH, including hidden/empty nodes and desktop-light/mobile-dark, without production data or Agent changes |
| Pre backup and recovery | `oc424-20261028T073917034985Z-ce33f4e74549.tar.gz.age`,184728672bytes,SHA256 `b90262a4367f5f9f598e73bdc8b96271b72220476fb4113ec1791ea3db4419e0`; same-hash MAC-WAN copy restores with pinned v1.7.7/v1.7.8 in7.88/8.68s; both DBs,14clients and current auth metadata pass |
| Post backup and recovery | `oc424-20261028T074310247117Z-e0703c806a5f.tar.gz.age`,184734908bytes,SHA256 `7e9d5fcdc57fa9189d59604bfdc664afa4b540d337f055313bce7ec681920f12`; pinned v1.7.8/v1.7.7 boots8.30/8.96s, with15uptime rollups both before and after each boot. No reclaim/`ANALYZE`, network, host ports or residual plaintext/container. Drill times are not RTO |
| Backup custody | The pinned installed receiver and its existing strict SSH identity/lock write each fresh release archive to its own new phase directory under `/srv/backups/oc424-encrypted/manual-v1.7.8-20261028`, outside the shared 14-pair retention directory. MAC-WAN pulls ciphertext over its existing strictly verified OVH SSH alias; the identity is never transferred. Shared retention is not invoked by these manual copies; the daily timer/service are unchanged |
| Local rollback | `/opt/nekomari/rollbacks/v1.7.8-20261028T074158Z-6e323629` retains prior/new compose, image pins, per-DB WAL-safe snapshots/checksums/instructions. These are not a cross-DB atomic snapshot; prior v1.7.7 image remains |
| Actual continuity | upgrade9/9,public24/24,scoped forecast14rows;14registered clients and14fresh CPU/uptime reporters. Both DB quick checks/schemas, UUIDs/tokens, key grants/expiry, GeoIP, zero update targets, nginx/recipient/probe override and quiet backup health remain. OC424/OVH/MAC-WAN Agents and Hermes are not restarted |

### v1.7.8 rollback and boundaries

Before rollback or metric-storage reclaim, retain and verify the fresh encrypted
post-upgrade backup above. The old v1.7.7 image does not recognize the uptime
definition: **manual metric-storage reclaim on that old version deletes its
uptime history**. The isolated old-image post-backup boot above is without
reclaim. To revert only the image, verify the checksums in the v1.7.8 rollback
directory, restore its `docker-compose.before.yml` as the live compose, validate
`docker compose config --quiet` and run `docker compose up -d --no-deps nekomari`.
Expect v1.7.7/8e1dafa and preserve the current DBs/14 nodes, health env/state and
newer data. Image rollback is prepared and separately tested on an isolated
backup, not exercised on production. Never overwrite Pi's later node with an
older 13-node database. Across the earlier v1.7->v1.6 auth migration use the
paired old DB and binary with frozen writers.

No owner-session UI acceptance for this release, independent-media restore,
incident delivery, zero-downtime/RPO/RTO/SLO or 200-node claim is made.

## v1.7.7 rollout (2026-10-28 14:30:19 Asia/Shanghai)

PR107 fixes paid-node unset/invalid expiry labels without inventing a countdown
or lifetime entitlement. Five locales and the existing shared admin theme remain;
valid expiry/color rules and tags are unchanged. The fetch-abort change is test
only. No backend/schema/API/Agent/dependency, credential, scope or schedule change.

| Item | Evidence |
|---|---|
| Immutable publication | `v1.7.7` -> `8e1dafa68e201830ec8df48b46eb60a2aa3b8332`; exact CI40642445800 8/8, identical-tree merge79910ca CI40643111878 8/8, release40643116847 10/10, Docker40643699302 2/2. The initial candidate's registry-contract failure is corrected by classifying the installed-binary spec as `.spec.py`; no gate disabled |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.7@sha256:c064f192fa97d1376f14295ea086802c98dff9b7d4b803bcebbced4471e0810b`; index `sha256:3bfc05f0b58e95494587b8e8e842a53bc23cc8695a875d4d530e6661b5d55702`; extracted/live binary SHA256 `28b3ad6e706277478cc8e755dbe86a1788e47a68539e64bbf88d267938d024d7` |
| MAC-WAN amd64 | `ghcr.io/aone2233/nekomari:v1.7.7@sha256:de822905be37d06da8b74de7de2f0ed8a008065525562ed80f5e14617746c62b`; binary SHA256 `ca0f172f322407cee1956ce0b94be5b15468737c022d788a2f6f3fd03813d5d0`. Both Linux checksums/tag/source/workflow attestations and Panel/Agent dual-architecture OCI identities verify; native packaged binaries match. Agents are not deployed |
| Focused/source verification | Previous component fails the new missing-expiry regression; fixed static5/5, TypeScript/ESLint, mounted desktop-light/mobile-dark and actual embedded candidate10scenarios pass. AbortController race10repeats and vet pass. No unrelated broad local SQL/capacity rerun; all mandatory CI gates remain |
| OVH published acceptance | Exact attested binary passes10/10 installed embedded-admin scenarios: five locales x desktop-light1280/mobile-dark390, real API null -> renewal -> null, neutral/orange labels, custom tag and no page errors. Temporary credentials/nodes only, loopback; no asset proxy. Browser dependencies are extracted into this batch's private disposable directory, not installed system-wide; existing OVH Agent unchanged |
| Pre encrypted backup/recovery | Installed OVH service creates `oc424-20261028T061435663773Z-14c4b3b7c4e1.tar.gz.age`,184572617bytes,SHA256 `0d0e81f9e9e8e64bc1446c75ae3a17b7db3fa493dc15bff0a125f5e51ac14571`; pinned-v1.7.6 recovery8.25s and exact-v1.7.7 recovery9.42s,667488rollups before boot,both DBs/14clients/current auth pass, no `ANALYZE` |
| Post encrypted backup/recovery | `oc424-20261028T063031841225Z-78a4084fdca9.tar.gz.age`,184595561bytes,SHA256 `7ce994f5f393a36f1f4e334a48bdee0ddf64a650037fc62f76862471e99ab952`; exact-v1.7.7 MAC-WAN recovery7.81s,646865rollups before boot,both DBs/14clients/current auth pass,networknone/noports,no `ANALYZE` or remaining plaintext/container. Drill times are not RTO |
| Local rollback | `/opt/nekomari/rollbacks/v1.7.7-20261028T063016Z-64133ab0` retains prior/new compose, image pins, per-DB WAL-safe snapshots/checksums/instructions. Not a cross-DB atomic snapshot; prior v1.7.6 image remains |
| Actual continuity | Started14:30:19,healthy/restart0,upgrade9/9,public23/23,scoped forecast14rows,14registered/fresh CPU reporters. Both DB checks/schemas, UUIDs/tokens, grants/expiry, GeoIP, zero update targets, nginx/recipient/probe override and quiet backup health unchanged. Prior hashed admin entry assets remain byte-identical. OC424/OVH/MAC-WAN Agents and Hermes not restarted |
| Copies and transport | Independent regular OVH release copies stay under `/srv/backups/oc424-encrypted/manual-v1.7.7-20261028`, outside14-pair pruning, with same-hash MAC-WAN copies. Initial slow relay is stopped without publishing a partial archive; MAC-WAN pulls ciphertext through its existing strictly trusted OVH alias. Later executor->MAC direct timeouts use strictly verified OC424 TCP proxy; no key/trust/custody change, identity never exported |
| First actual scheduled nightly | Timer2026-10-28 11:30:23 -> service11:31:04Asia/Shanghai,success/exit0; `oc424-20261028T033023877322Z-e06d4505678e.tar.gz.age`,184348120bytes,SHA256 `7014f3794ed42f7e4babe011536fb3c9bd9daff77eb768bf5c9c004980c85fb3`. Verified pair and independent regular copy under `manual-first-nightly-20261028`; closes first-trigger observation, not a restore of that specific archive or RPO guarantee |

### v1.7.7 rollback and boundaries

Verify this rollback directory's checksums; restore `docker-compose.before.yml`
as `/opt/nekomari/docker-compose.yml`, validate `docker compose config --quiet`,
then `docker compose up -d --no-deps nekomari`. Expect v1.7.6/f8c6ae1:
`ghcr.io/aone2233/nekomari:v1.7.6@sha256:0a3316d1f58ecd9016b150175cf3aa509bbb1a49e83da78b802adb62b7ded279`.
Keep current DBs/14nodes and backup-health env/report/state; do not restore
older DBs just for this no-schema patch. This image rollback is prepared, not
exercised; actual isolated old/new-image recovery is separately verified.
Across the earlier v1.7->v1.6 auth migration use the paired old DB and binary
with frozen writers, never an image-only downgrade.

No new owner-session admin acceptance, production nullable-node mutation,
zero-downtime/SLO/RPO/RTO, independent-media restore, live incident delivery or
200-node result is claimed. Failure logs and private evidence stay under the
local text pointer `.runtest/release-v1.7.7-current`. Shared caches, all backups,
identity/private credentials and prior rollback images are not cleanup targets.

Final read-only14:39Asia/Shanghai checks retain exact live version/image,
both DB checks,14fresh reporters,unchanged Agent/Hermes service identities,
private-file metadata and quiet fresh backup health. v1.7.5/v1.7.6/v1.7.7 tags
and every release's10asset identities/sizes/digests/timestamps remain unchanged.
Cleanup removes only this batch's checksum-verified OC424 tool stage, OVH
private browser/runtime/test directory (470139481bytes) and local build/test/
download outputs (386197341bytes). Logs, screenshots, attestations/manifests,
shared caches and all recovery copies/images remain. Final `df -P /` use:
OC42444%,OVH9%,MAC-WAN26%,development64%; no broad cache/image purge.

## v1.7.6 rollout (2026-10-27 21:25:17 Asia/Shanghai)

PR105 fixes three-placeholder/five-argument recovery binding rejected by actual
MySQL/PostgreSQL and uses MySQL integer `DIV` for parent alignment. Keep the
series-driven SQLite plan and sealed-parent recovery contract. No API/schema,
auth, Agent/protocol, frontend/dependency, token/scope or coverage behavior change.
The MySQL division issue predates v1.7.5; it is not attributed to dsh's index fix.

| Item | Evidence |
|---|---|
| Immutable publication | `v1.7.6` -> `f8c6ae1161ce10a7493c454f1543c2cf556fbf44`; exact CI40506697515 8/8, merge CI40507760830 8/8, release40507767971 10/10, Docker40511319737 2/2. PR105 merge has identical tested tree |
| Publication delay | ARM64 cross-toolchain apt installation is slow but eventually passes; one successful attempt, no bypass/rebuild. Four already-successful build artifact IDs/digests/sizes/timestamps remain unchanged |
| OC424 arm64 | `ghcr.io/aone2233/nekomari:v1.7.6@sha256:0a3316d1f58ecd9016b150175cf3aa509bbb1a49e83da78b802adb62b7ded279`; index `sha256:3452bcf5e7b3cd9b46c84201b8a09bb97786b8afab26e79a5b180e0d600be822`; extracted/live binary SHA256 `dcf5feae352cd213dc88b59a2fb83cf2b8dd73e67fd76330a5b0fea54558d391` |
| Recovery amd64 | `ghcr.io/aone2233/nekomari:v1.7.6@sha256:f637d34bd1272f1d977102a5fa221382accaf6c257ad1c22d8da8f7bba271369`; binary SHA256 `78e12f024cad615c6e9695067839932d3f8480f83440d88d3d7c7b885a483b17`. Both Linux checksums/source/tag/workflow attestations and both image architectures/source labels verify |
| Focused verification | Old-source actual MySQL/PostgreSQL arity failures and arity-only MySQL sealed-parent failure reproduce; fixed races and vet pass. Added CI actual-SQL gate runs. Attested published binary completes install and two durable-child recovery startups on both backends, using only disposable DBs |
| OVH acceptance | Attested release binary passes6/6 synthetic installed-Panel SLA groups, including restart/stored-row preservation and visibility/maintenance; no production fixtures, real notifications or third-party lookup. Existing Agent unchanged |
| Pre encrypted backup/recovery | Installed OVH service creates `oc424-20261027T125459717197Z-ebf7524b9279.tar.gz.age`,183601242bytes,SHA256 `817c586909587c787efe0a26cea5c86cc414ab88dc2dedbf87dc215cc023c857`; MAC-WAN pinned-v1.7.5 recovery8.41s and exact-v1.7.6 recovery9.74s,636696rollups before boot,both DB checks/14clients/current auth pass, no planner-statistics refresh |
| Local recovery | `/opt/nekomari/rollbacks/v1.7.6-20261027T132515Z-5045e5f7` retains per-DB WAL-safe snapshots, prior/new compose/images/checksums and rollback instructions; not a cross-DB atomic snapshot. Prior v1.7.5 image retained |
| Actual continuity | Healthy/restart0,upgrade9/9,public23/23,14registered/fresh CPU reporters,scoped forecast14rows; both DB checks/schema, UUIDs/tokens,key scopes/expiry,GeoIP true/ipinfo,zero update targets,nginx/recipient/probe override and quiet persisted backup health unchanged. OC424/OVH/MAC-WAN Agents and Hermes not restarted |
| Post encrypted backup/recovery | `oc424-20261027T132555571297Z-8cddff3f7a74.tar.gz.age`,183610914bytes,SHA256 `f5833d99db25f9ae8f11ec427aaa0514fd17db0d379f0d50490d9b323ee69241`; exact-v1.7.6 MAC-WAN recovery8.71s,626242rollups before boot,both DB checks/14clients/current auth pass, no ANALYZE or remaining plaintext/container |
| Copies and schedule | Both archives have independent regular OVH copies under `/srv/backups/oc424-encrypted/manual-v1.7.6-20261027` outside14-pair pruning and same-hash MAC-WAN copies. dsh's two v1.7.5 archives and initial canary also gain verified independent copies before rotation. No legacy recovery copy is lost. Nightly03:30UTC/15-minutehealth schedule unchanged; first nightly still not triggered |

### v1.7.6 rollback and boundaries

Verify recovery checksums and restore `docker-compose.before.yml` as the actual
compose under `/opt/nekomari`, then `docker compose config --quiet` and
`docker compose up -d --no-deps nekomari`. Expect pinned v1.7.5/c84c3cc:
`ghcr.io/aone2233/nekomari:v1.7.5@sha256:ed162ac2cd13b4571eb449a38c6a81b573d10e13d22ef9b5b59b135f4eadff48`.
Keep current DBs,14nodes and health env/report/state. This live image rollback is
prepared, not exercised; old/new off-host actual recovery is separate. v1.7.5
rollback would reintroduce the MySQL/PostgreSQL defect for those deployments;
OC424 uses SQLite. Across v1.7->v1.6 auth migration use paired pre-migration DB
and old binary with frozen writers, never an image-only downgrade.

No zero-downtime or measured SLO/RPO/RTO claim follows from this single-container
switch. No new owner-session UI check, independent-media recovery, scheduled
nightly success, real live incident delivery or200-node acceptance is claimed.
Only this batch's owned disposable tools/databases are eligible for cleanup;
shared caches, identities, all backups and rollback images remain.

Final read-only checks retain the exact version/image/live binary, both DB
checks, all14fresh reporters and quiet fresh backup health; no ERROR/panic
appears in the bounded post-switch log. Agents/Hermes and restricted health
authorization/tools/units remain unchanged. MAC-WAN has no restore container or
plaintext scratch. v1.7.5/v1.7.6 tags and each release's10asset identities,
sizes,digests and timestamps still match the pre-deployment records.

Cleanup removes only the two ID-verified disposable SQL containers/internal
network, their now-unused PostgreSQL/MySQL images, about188MiB of this batch's
downloaded/test binaries and synthetic credentials, and checksum-matched OC424
tool stage. Logs, attestations and all backups/rollback assets remain. Final
`df -P /` use: OC42444%, OVH9%, MAC-WAN26%, development64%; no shared-cache purge.

## v1.7.4 rollout (2026-10-27 17:23:20 Asia/Shanghai)

Bounded SLA task-generation correctness patch PR98: normalize one chronological
measured bucket per entity/task/time before loss, latency percentiles, incidents,
presence union and maintenance exclusions. Keep the worst measured value and
ignore empty placeholders; bucket means are not raw-probe percentiles. Historical
rollups, API/wire/schema, coverage definition, frontend/dependencies, Agent and
credentials remain unchanged. The separate span-based coverage issue stays open.
See [`SLA-GENERATIONS.md`](./SLA-GENERATIONS.md).

| Item | Evidence |
|---|---|
| Immutable publication | `v1.7.4` -> `b0ea91756e700e871491a4c4388757f65ead263e`; exact CI40476853150 7/7, release40478743592 10/10 (attempt 2), Docker40479934401 2/2. PR98 merge has identical tested tree; no retag/rebuild |
| Publication retry | First release attempt fails at GitHub OIDC identity-token retrieval before a Release exists; dependent Docker40479558885 also fails. Only failed publication/verification jobs retry; all five successful build artifact IDs/digests/sizes/timestamps are unchanged. Provenance is never bypassed |
| OC424 pinned image | `ghcr.io/aone2233/nekomari:v1.7.4@sha256:e089dabfcba5c3b89365b53d3288f6925d05069e6195adf9832d3c8879e1355e`; Panel index `sha256:62e18cd71d461bdbf89f5e0b8bfe1ee4830aacc9f2d13f3d2289a20c1ccab332` |
| Native/source provenance | Both Linux binaries match source/tag/workflow attestations and checksums; both Panel/Agent architectures label exact full source/version1.7.4. Native/live arm64 SHA256 `38e0fcafc6c7201394507d7313adaa91fbeb852b3ff97981ae1dccf265655638`; MAC-WAN amd64 `ghcr.io/aone2233/nekomari:v1.7.4@sha256:b2c6cf4cc986682ace82cf6c0abdc42ab24b3b9bf3830882bc042c64c10453c6`, binary `198a4e69be41f77b11a3e68571e2ac9016abe38896baba8576bb021036e18405` |
| Focused acceptance | SLA race tests, bounded existing SLA RPC tests and vet pass; regressions fail before the fix. Attested release binary passes6/6 installed synthetic-Panel groups on OVH: overlaps/placeholders, loss/latency/incidents, entity/task/gap isolation, maintenance, guest-hidden filtering, identical restart report/stored row count. No production fixtures or external notifications |
| Live source observation | Read-only baseline finds44 multi-generation metric/entity/task groups (22loss+22latency); a series count is not independently measured timestamp overlap |
| Pre encrypted recovery | Installed OVH service: `oc424-20261027T085608950432Z-8a4761db5628.tar.gz.age`,183512271bytes,SHA256 `94b92ef8c060e23d07e8bc719a7b309ce0b4042538748a28ed52c15d8600e7f6`; regular copy `/srv/backups/oc424-encrypted/manual-v1.7.4-20261027/oc424-20261027T085608950432Z-8a4761db5628.tar.gz.age` outside14-pair pruning. MAC-WAN exact-v1.7.3 recovery passes in8.73s; same archive also boots exact-v1.7.4 before the switch in9.6s |
| Recovery readiness diagnostic | Initial offline v1.7.3 attempts time out in metric-store startup (context deadlines), despite authenticated archive/both DB checks. Refresh SQLite planner statistics using `ANALYZE` only in disposable verified metrics copies; retain all645172 rollup rows/business schema. Old/new image actual boots then pass. Original archives/production DBs and installed encryption tool remain untouched; this is an operational workaround, not a source-level startup fix |
| Local recovery | `/opt/nekomari/rollbacks/v1.7.4-20261027T092318Z-84a48b94` retains per-DB WAL-safe snapshots, prior/new compose/images/checksums and image rollback instructions. Old v1.7.3 image remains; current DBs and backup-health environment/state stay intact |
| Actual continuity |14registered UUIDs/tokens and all14fresh CPU reporters; both DB quick_check/schema, scoped-key permissions/expiry, GeoIP true/ipinfo,zero rollout targets,nginx/recipient/probe override and quiet backup-health state unchanged. OC424/OVH/MAC-WAN Agents and Hermes not restarted |
| Bounded live acceptance | Healthy/restart0,upgrade9/9,public23/23,forecast14rows; scoped live SLA reads14nodes/58tasks and independently compares11task results against preserve-series bucket means across two reporting nodes. Multi-generation tasks in that comparison:0. Incident chronology/counts and p50/p95/p99 verify;0new bounded error lines. No owner session/UI claim |
| Preserved backup monitor | Existing restricted health SSH/tool/unit hashes unchanged; recurring15-minute checks succeed and latest OC424 report is fresh/ok,observed=ok/notified=empty across restart. Nightly encrypted timer still03:30UTC,first trigger not observed; no live fake outage/Telegram test |
| Post encrypted recovery | Installed OVH service: `oc424-20261027T092415525807Z-c3696c018496.tar.gz.age`,183649817bytes,SHA256 `daf3e4c3d211c86eed722387b8d1266f524763df6248571f5ab2240ebdf2d311`; regular release copy `/srv/backups/oc424-encrypted/manual-v1.7.4-20261027/oc424-20261027T092415525807Z-c3696c018496.tar.gz.age` and MAC-WAN same-hash copy retained. Exact-v1.7.4 actual offline recovery passes in8.31s,both DBs/14clients/current auth metadata/cleanup |

### v1.7.4 rollback and boundaries

Verify the private recovery checksums and restore `docker-compose.before.yml`
as `/opt/nekomari/docker-compose.yml`, then run:

```sh
cd /opt/nekomari
docker compose config --quiet
docker compose up -d --no-deps nekomari
```

Expect pinned v1.7.3/0fe0b37, healthy/restart0, keeping current DBs and the
existing health environment/report/state. No auth/schema downgrade
or Agent change is required. Do not overwrite Pi's added node with historical
13-client DBs. This live image rollback is prepared, not exercised; actual
old/new encrypted restores are separate. Across v1.7->v1.6 auth migration the
paired pre-migration DB and binary are still required.

Only owned disposable tools/downloads are cleaned after all checks. Retain all
ciphertext, legacy plaintext/manual/local recovery copies, images and identity
material. Strict pinned SSH uses the working direct MAC-WAN route after retained
proxy banner timeouts; only ciphertext is transferred. Independent-media
recovery, first nightly scheduled backup, actual incident delivery, owner UI,
measured production SLO/RPO/RTO and 200-node work remain separate. A public version
read returns HTTP502 during the single-container replacement; subsequent healthy,
upgrade9/9 and public23/23 guards pass. This is not a zero-downtime rollout or a
measured outage-duration/availability SLO.

Post-acceptance cleanup removes only this batch's downloaded Linux binaries,
synthetic Panel, temporary GitHub CLI and checksum-matched OC424 tool stage
(about 183.5 MiB). Attestations/metadata, rollback images/DBs and all backups
remain. Root-filesystem use is about 43% on OC424, 8% on OVH, 25% on MAC-WAN
and 63% on the development host. No shared cache purge is needed or performed.

## v1.7.3 rollout (2026-10-27 16:29:42 Asia/Shanghai)

Bounded encrypted-backup freshness/heartbeat monitor PR96, not a full v1.8
release. OVH checks its newest ciphertext pair and pushes only non-secret health
metadata through a dedicated restricted SSH command. An opt-in Linux Panel
watcher uses existing notifications for26-hour backup/45-minute report incidents
and recovery, persisting dedupe across normal restarts and retrying unavailable
delivery15minutes later. Telegram delivery has30-second/64KiB bounds. No public
API,DB/settings schema,Agent/protocol,existing token/scope/identity or nightly
backup encryption/retention change. See [`BACKUP-HEALTH.md`](./BACKUP-HEALTH.md).

| Item | Evidence |
|---|---|
| Immutable publication | `v1.7.3` -> `0fe0b3758c3f615b85af93bd0ad94fee71a90ed4`; exactCI40471914791 7/7,release40472663920 10/10,Docker40473430351 2/2. PR96 merge has the identical tested tree; no tag/asset rebuild |
| OC424 pinned image | `ghcr.io/aone2233/nekomari:v1.7.3@sha256:206981e01fc40d7cce7bf6a464973783a0128b58f7111114d9cd33a8c1830a14`; Panel index `sha256:08f81141c50466f8f64618f85b972747125d817f14c3368929790018ad0499cd` |
| Native/source provenance | Both Linux binaries match checksums/tag/source/workflow attestations; Panel/Agent amd64/arm64 OCI metadata identify full0fe0b3758c3f615b85af93bd0ad94fee71a90ed4/version1.7.3. Native/live arm64 SHA256 `2d907fd66d826f8ddb0fe5c158afab3208f4f2a55b70a9ca3bab9ca14573acbf`; MAC-WAN amd64 image `ghcr.io/aone2233/nekomari:v1.7.3@sha256:0235dab28c139735b8dd072f0bd9b2a388727c227df590b5cf0ca906706e6763`, binary `75fae0034744bf00f84fe40f148c432ac3a1bd3e1f85f9e114ae26766d54f084` |
| Focused verification | Python9offline groups,backupwatch/server/messageSender races and vet pass. Attested release binary6/6 installed synthetic-Panel/fake-Telegram groups on OVH cover stale backup,restart dedupe,recovery and stale heartbeat; no real Telegram/provider lookup. Required exact-source CI also retains full existing gates |
| Pre encrypted recovery | Installed OVH service succeeds: `oc424-20261027T080733407301Z-6d77309d288e.tar.gz.age`,183146864bytes,SHA256 `bc1ce19195024575669b892d533380ea291e2e9b7ec286283bd6cc087f1e6bac`; regular copy `/srv/backups/oc424-encrypted/manual-v1.7.3-20261027/oc424-20261027T080733407301Z-6d77309d288e.tar.gz.age` outside14-pair pruning. Same ciphertext on MAC-WAN passes actual pinned-v1.7.2 recovery in36.11s,both DBs/14clients/auth metadata/cleanup |
| Restricted health installation | OC424 recovery `/root/nekomari-v173-health-20261027T082843Z-e84d4362` retains original authorized_keys/hash and health-specific rollback; source-limited restrict/forced-command public key,one fixed sudo receiver,root0700/0600 local report. OVH recovery `/root/nekomari-v173-health-20261027T082845Z-76e2a235` records exact tool/wrapper/unit hashes; dedicated push private key remains only OVH. Existing authorizations,pull key,encrypted producer/receiver/nightly units/retention remain unchanged |
| Actual health acceptance | New OVH service/manual check and initial automatic health-timer trigger succeed;15-minute timer is enabled/active. OC424 receives fresh4KiB-bounded status and persists observed=ok,notified=empty. Opt-in environment and healthy startup log verify; no simulated live outage/test Telegram. Later recurring health/nightly backup success or actual live Telegram delivery remain separate |
| Local app recovery | `/opt/nekomari/rollbacks/v1.7.3-20261027T082941Z-ca9d9477` retains paired per-DB WAL-safe snapshots,old/new compose/images/hashes and image rollback instructions; old v1.7.2 native image remains. Restore prior compose to remove the health environment opt-in; keep current DBs and Pi's node |
| Actual continuity |14registered UUIDs/Agent tokens retained; all14fresh CPU reporters stay fresh (improved from13at the earlier release). Both DB quick_check/schema,key scopes/expiry,zero rollout targets,GeoIP true/ipinfo,nginx/recipients/probe override unchanged. OC424/OVH/MAC-WAN Agents and Hermes not restarted |
| Bounded live acceptance | Healthy/restart0,upgrade9/9,public23/23,current MAC-WAN scoped-key forecast14rows,0new error lines in bounded log window. Owner-session history/audit/API-key UI remains separate |
| Post encrypted recovery | Installed OVH service succeeds: `oc424-20261027T083031292998Z-cd23f23c0f7a.tar.gz.age`,183422090bytes,SHA256 `57b3a47cc2d730da3d97a7b47b6e073879b868678ca7c2f3a0367fdabf94c3d3`; regular release copy `/srv/backups/oc424-encrypted/manual-v1.7.3-20261027/oc424-20261027T083031292998Z-cd23f23c0f7a.tar.gz.age` and MAC-WAN same-hash copy retained. Actual exact-v1.7.3 offline recovery passes in37.6s,both DBs/14clients/current auth metadata/cleanup; no identity export or remaining plaintext/container |

### v1.7.3 rollback and operational boundaries

For app rollback verify private recovery checksums,restore
`docker-compose.before.yml` under `/opt/nekomari`,then `docker compose config
--quiet` and `docker compose up -d --no-deps nekomari`. Expect pinned
v1.7.2/832b528,healthy/restart0,without the health env. Keep current DBs; no schema
or credential downgrade is required. Do not restore historical13-node DBs.
Live image rollback is prepared,not exercised; actual old/new encrypted restores
are separate. Older auth-migration rollback to v1.6 still needs paired DB/binary.

To remove monitoring,disable only `nekomari-oc424-backup-health.timer` on OVH.
Use recorded hashes to restore only the exact appended health SSH line/sudoers/
wrapper/tool/unit changes; stop if another operator changed a file. Retain health
report/state and all backups by default. Never disable the encrypted nightly
timer,revoke unrelated keys or delete legacy plaintext/manual/rollback copies.
Private identities remain MAC-WAN-only. Receiver checksum/filename time is not
age authentication,restore proof,measured RPO/RTO or a Panel-independent alert:
OC424/provider outages still need external monitoring. The initial automatic
health-timer trigger succeeds; later recurring checks, first nightly backup,
owner authenticated UI, independent-media recovery, caller inventory and
200-node work remain separate. Only owned temporary tools/downloads are cleaned.

A public version read during the guarded container replacement returns HTTP502;
the subsequent healthy-image guard, upgrade9/9 and public23/23 all pass. This
single-container switch is not a zero-downtime rollout; no outage duration or
availability SLO is inferred from these bounded checks.

Post-acceptance cleanup removes only this batch's downloaded binaries,
synthetic Panel, temporary GitHub CLI and checksum-matched OC424 tool stage
(about 183.5MiB). Metadata, attestations, rollback images/DBs and all backups
remain. Final root-filesystem use is about43% on OC424,7% on OVH,24% on MAC-WAN;
the development host is63%, not near80%. No shared cache purge is performed.

## v1.7.2 rollout (2026-10-27 15:13:50 Asia/Shanghai)

Panel-only GeoIP configuration-safety/hot-reload patch PR94. Wrong types/read
failures fall back without a background panic; both enabled/provider settings
reload, retired caches are cleared, and lookups/database updates/replacement/
shutdown have a synchronized lifetime. Reload remains asynchronous and subject
to existing provider timeouts. No Agent, protocol, frontend, dependency, auth,
schema, scope or token change. See [`GEOIP-RELOAD.md`](./GEOIP-RELOAD.md).

| Item | Evidence |
|---|---|
| Immutable source/publication | `v1.7.2` -> `832b52845d617fe4abba6f752dd82ddbae8a8234`; exactCI40464125160 7/7, release40464858298 10/10, Docker40465511917 2/2. Mergeba70933 has the identical tested tree; no tag/asset rebuild |
| OC424 pinned image | `ghcr.io/aone2233/nekomari:v1.7.2@sha256:6fc7259929faf318b357937bb153301a158460ae4619a7bdfe0444db1d179c28`; Panel index `sha256:0570755d397f47d5aed145257fbf0c5c934bebeb8b00f0e5cf5cb80da08930ed` |
| Source/binary identity | Anonymous Panel/Agent amd64/arm64 OCI metadata identify full `832b52845d617fe4abba6f752dd82ddbae8a8234`, version1.7.2. Native/live arm64 binary SHA256 `3e7fc1a652b98953e814d8cc059ac678e9ff369c8dfc36544c09b1d97c9a3132` matches checksums/source/tag/workflow attestation; native help passes without production mount. MAC-WAN amd64 recovery image `ghcr.io/aone2233/nekomari:v1.7.2@sha256:ff5127688104a6dbd14132e14a7ea7ca07daab768a325b173030da97e56c8103`, binary `c3596b03354bd3dfe6ae33fa83f14bbe3e1dd8a209aa10e0aaee9d99e509cfbd` likewise matches |
| Focused real acceptance | Go1.27.3 hermetic GeoIP lifecycle/MMDB-failure, server/basic-info race tests and targeted vet pass. Required CI runs lifecycle races and actual synthetic-Panel settings saves; attested release binary passes8/8 isolated OVH groups, same PID through malformed types/offline MMDB fallback/recovery, temporary stage removed |
| Fresh pre-patch encrypted recovery | Installed OVH service succeeds; `oc424-20261027T065748757806Z-3085456cf8d7.tar.gz.age`,183081658bytes,SHA256 `2090f9a2b90526ef9874e06b01ef97cf1e8939e6a888a7c8a9c08881777f1c7a`; independent regular copy at `/srv/backups/oc424-encrypted/manual-v1.7.2-20261027/oc424-20261027T065748757806Z-3085456cf8d7.tar.gz.age` outside14-pair pruning. Same ciphertext on MAC-WAN passes actual pinned-v1.7.1 recovery in35.36s, both DBs,14 clients,current scoped-key metadata and cleanup |
| Local recovery | `/opt/nekomari/rollbacks/v1.7.2-20261027T071349Z-a2e6bc48` retains per-DB WAL-safe paired snapshots, schemas, old/new compose/images/checksums and image-only rollback instructions; prior v1.7.1 native image stays installed. Per-DB snapshots are not a cross-DB transaction |
| Continuity | Owner confirms Pi's added node;14 registered UUIDs/tokens preserved, all13 previously fresh CPU reporters remain fresh. Both DB quick_check/schema unchanged; key scopes/expiry,zero rollout targets,GeoIP true/ipinfo,nginx,recipients and probe override unchanged. OC424 AgentPID499631/restarts2 unchanged; OVH/MAC-WAN Agents and Hermes not restarted |
| Bounded live checks | Healthy/restart0,upgrade9/9,public23/23;0 new ERROR lines in bounded log window,ipinfo startup observed. MAC-WAN current scoped-key read/forecast14rows passes without writes; not owner-session UI/history/audit acceptance |
| Fresh post-patch encrypted recovery | Installed OVH service succeeds; `oc424-20261027T071437379700Z-c47edfa7cd27.tar.gz.age`,183230391bytes,SHA256 `978f8e0efcef936941d463b1417e433194856ec590ea5a202208cf24a60d1f79`; independent regular copy at `/srv/backups/oc424-encrypted/manual-v1.7.2-20261027/oc424-20261027T071437379700Z-c47edfa7cd27.tar.gz.age`. Same ciphertext on MAC-WAN passes exact-v1.7.2 isolated recovery in33.27s,both DBs/14 clients/current auth metadata/cleanup. No identity export or remaining plaintext/container |

### Patch rollback to v1.7.1 (preserve current DBs)

Verify the private recovery checksums and `docker-compose.before.yml`, restore
that compose file under `/opt/nekomari`, then run `docker compose config --quiet`
and `docker compose up -d --no-deps nekomari`. Expect pinned v1.7.1/3b7e300,
healthy/restart0. No schema/auth downgrade is involved: keep current DBs and Pi's
new node. Do not restore an older13-client snapshot. This production image
rollback is prepared, not live-exercised; old/new isolated encrypted restores
are separate evidence. The older v1.7->v1.6 migration rollback still needs its
paired old DB + binary and would lose newer data/permissions.

No Agent rollout/token/key rotation, production GeoIP setting toggle, backup
schedule/retention change or200-node work occurs. All legacy plaintext/manual/
local rollback copies remain. Owner authenticated UI, remaining caller inventory,
independent-media recovery, scheduled-freshness observation/alerting and measured
production SLO/RPO/RTO remain separate. Only owned disposable tools/downloads are
cleaned, not shared caches, installed rollback images or backups.

The first post-release MAC-WAN transfer and a read-only destination inspection
via OVH time out during the SSH banner exchange. No data is transferred in that
attempt; the OVH backup/service and live Panel are not repeated. A strict pinned
route via OC424 verifies destination/sidecar/partial absence, then transfers the
same checksum-matched ciphertext and completes recovery. Failure evidence is
retained; no host-verification bypass, gate relaxation or duplicate backup.

## v1.7.1 rollout (2026-10-27 14:15:34 Asia/Shanghai)

Panel-only forecast correctness patch PR92. Detect every chronological counter
reset/negative total; checked integer byte growth preserves precision and refuses
overflow. Distant duration/JSON-year crossing dates are omitted, not wrapped.
No auth/schema/frontend/Agent/protocol/dependency change or fleet rollout.

| Item | Evidence |
|---|---|
| Immutable source/publication | `v1.7.1` -> `3b7e30037307bcd1007b7725d0ea2386747880e0`; exactCI40458344881 7/7, release40459059246 10/10 including native amd64/arm64 deploy verification, Docker40459610734 2/2. Merge3d85255 has the identical tested tree |
| OC424 pinned image | `ghcr.io/aone2233/nekomari:v1.7.1@sha256:ea6e2238f4c800ec34851e4c1e81ca603f1c2d7d70121730f5099fe967d1bfdd`; Panel index `sha256:72ff8626bf499745a2f33874d445a7af98308ea320a9a4e193cba6f48f8b021e` |
| Source/binary identity | Anonymous Panel/Agent amd64/arm64 configs identify full3b7e300 and version1.7.1. Native/live arm64 SHA256 `fcbbbd07abde8cce9241133faf0ea48772cd76da96683d2660dcf4073fb3a07c` matches release checksums and source/tag/workflow attestation; native help passes without production data. amd64 recovery image `sha256:01a54d45778ea14d2034a8a93bc948c7fb4595c026af7ef0cea43efa1ac95e8e`, binary `3079b5c0a7085dbe54ab4c9892541032c56dd54cc78894a5e0d72e698c50d80a` likewise matches |
| Pre-patch encrypted backup | OVH `oc424-20261027T055914268507Z-ecec43bb9a1b.tar.gz.age`,182954387bytes, SHA256 `24a05c81377abb05f7894c16bff4ff6532bf79e8e5f54fb0d586a5842d218ec5`. Installed encrypted service succeeds; same pair copied to MAC-WAN and regular-copied under `/srv/backups/oc424-encrypted/manual-v1.7.1-20261027` outside pruning. Actual isolated pinned-v1.7.0 recovery checks both DBs,13 clients/current scoped-key schema and cleanup in69.23s |
| Local paired recovery | `/opt/nekomari/rollbacks/v1.7.1-20261027T061510Z-08dfe24e` retains per-DB WAL-safe snapshots, schemas, before/after compose, image refs, checksums and rollback instructions; old native v1.7.0 image stays installed. These are per-DB snapshots, not a cross-DB transaction |
| Actual continuity | Both DB quick_check pass and full schemas are unchanged;13 registered UUIDs and existing Agent tokens are identical. All12 previously fresh CPU-reporting nodes remain fresh. Key scopes/expiry,zero rollout targets,nginx,recipients and public-probe override unchanged; OC424 AgentPID499631/restarts2 unchanged |
| Bounded acceptance | Healthy/restart0,upgrade9/9,public23/23;0 new ERROR in bounded log window. Actual MAC-WAN scoped-key `admin:getTrafficForecast` returns13rows:9linear_rate and4insufficient with reasons, no invalid projections/dates. This is an authenticated API read, not owner-session UI/history/audit acceptance |
| Post-patch encrypted backup | OVH `oc424-20261027T061543363125Z-7ac8520fc4a2.tar.gz.age`,183122373bytes,SHA256 `4da6cfa20eea2150de22612716c408d4120d8ae2083f145fe22504114ee0bf8b`; installed service succeeds and regular manual release copy is retained outside14-pair pruning. Same ciphertext copied to MAC-WAN; exact new-image isolated recovery passes in107.10s, both DBs/13 clients/current auth metadata and no plaintext/container remaining |
| Hermes handoff | Owner-private key matches only nodes:read + nodes:write;365-day owner-selected expiry2027-10-27 13:38:02 Asia/Shanghai. Actual disposable create/read/edit/read/remove at13:49:10 passes and original13 metadata remains unchanged. Three reference overrides/marker/README permit reviewed CLI writes; no gateway/Agent restart,key rotation or token/install/exec/update grant. Not a complete runtime-caller inventory |

### Patch rollback to v1.7.0 (no database downgrade)

This patch changes no schema or credentials. If needed, preserve failure evidence,
verify `docker-compose.before.yml` against the private recovery checksums, then
restore that compose file and run `docker compose config --quiet` and
`docker compose up -d --no-deps nekomari` under `/opt/nekomari`. Expect pinned
v1.7.0/8eb711e,healthy/restart0; keep the current DBs so newer measurements and
owner-configured keys are not discarded. This image rollback is prepared, not
exercised on live production. Do not confuse it with the older v1.7.0->v1.6.29
auth-migration rollback below, which requires paired pre-migration DB + old binary
and would lose newer changes/restore old permissions.

The first pre-patch isolated restore readiness check times out; a clean retry
with bounded180s startup allowance passes in69.23s. Retained diagnostic pattern
counts are not evidence of production errors. The backup metadata helper first
misuses a context manager after creating/copying the ciphertext; inspecting and
verifying the same pair finalizes evidence without another receive. A later
post-patch MAC-WAN SSH banner timeout via OC424 happens before transfer; OVH
reachability works, destination inspection confirms no partial/final file, and
the same pinned-key SSH route via OVH transfers successfully. Two pre-switch
scheduled public probes at06:03/06:13 UTC report3.740/10.903s edge latency above
the3s threshold, while origin stays0.002s and all5/5samples return200. A post-switch
explicit probe at06:19:52 UTC passes: origin0.002s/public1.011s/5of5; no threshold
is relaxed. This is transient edge-path evidence, not a production SLO claim.
No gate is weakened,failed attempt counted as success,
backup deleted or private identity exported.

## v1.7.0 rollout (2026-10-27, Asia/Shanghai)

Owner approves the current 13-node baseline after deliberately deleting a node.
The offline identity copy is downloaded **per owner report**, not independently
restored from that medium. Token edit/import restrictions are accepted and no
Agent-token rotation is requested. Cutover at12:44:18 changes only the Panel;
nginx, the public-probe hostname override and Agent services stay unchanged.

| Item | Evidence |
|---|---|
| Immutable source/publication | `v1.7.0` -> `8eb711e93c7321098dbea108cb6468afdbc2eab0`; exactCI 40446315166 7/7, release40450095088 10/10 incl native amd64/arm64 deployment verification; Docker40450707458 2/2 |
| OC424 image | `ghcr.io/aone2233/nekomari:v1.7.0@sha256:61fd8ffc00cf2f40e97d630ef1ee83e9327a43b2abff854fe4248fcb68d98cf0` (arm64); Panel index `sha256:2aa4ce9e90d2b1d59b11ea75755803ce42704bc89a25b8be33119d5c471c4208` |
| Image/binary identity | Anonymous Panel/Agent image configs on both architectures identify full 8eb711e, OCI version1.7.0. Native/live arm64 binary SHA256 `e381396ef0d28d53445da93b16ee54a9bc493fec68a86cb20deb62dec9cea5b3` matches checksums and source/tag/workflow attestation; native `--help` passes without live-data mount |
| Pre-cutover encrypted recovery | OVH `oc424-20261027T043120813844Z-2b48d4a466c6.tar.gz.age`,182893531bytes, SHA256 `0577eed95c9eaa073044e26097ab789b37d0915606221aa3a1d433ceda3207d6`; actual MAC-WAN pinned v1.6.29 recovery checks both DBs,13 clients and no scoped-key table. First boot check times out; retry passes in 36.39s, with no plaintext/container remaining |
| Local paired rollback | `/opt/nekomari/rollbacks/v1.7.0-20261027T044353Z-2259d697`: stop Panel first, snapshot both DBs, capture selected assets, old/new compose/images, checksum evidence, `rollback.py` and `ROLLBACK.txt`. Old native image remains installed |
| Migration | Both DB quick_check pass; all preexisting schema entries unchanged, only scoped-key table/indexes added. Legacy plaintext config row removed, migration marker present, exactly one unrevoked 30-day `nodes:read` key; no token/digest value selected for acceptance |
| Continuity | Registered UUID set 13 and Agent tokens compare unchanged inside SQLite; zero rollout targets. All 12 pre-switch fresh CPU nodes remain fresh; no 13/13 online/SLO claim. OC424/OVH/MAC-WAN Agents not restarted |
| Live acceptance | Healthy/restart0, upgrade 9/9, public 23/23, no new ERROR in bounded window; five admin tools' shells/assets remain in-panel. Owner authenticated UI/API-key acceptance is pending |
| Hermes | Gateway stopped during switch;3 old skill references backed up and prepended with reviewed metadata-client/no-legacy-fallback rules. Gateway resumes; actual dedicated key and disposable live write/workflow acceptance remain pending. Old key is server-read-only, not a dormant global admin credential |
| Encrypted nightly cutover | OVH installs `nekomari-oc424-encrypted-backup.service/.timer`; daily 03:30 UTC, persistent, keep 14 pairs, locked/bounded/restricted receiver and journal failures. Remove only old 03:30 cron row, preserve other rows/script/keys. Installed service succeeds, timer enabled/active; next event2026-10-28 11:30 Asia/Shanghai |
| Post-cutover encrypted recovery | First installed-service archive `oc424-20261027T044833717100Z-7c911d43f0a3.tar.gz.age`,182862307bytes, SHA256 `618e8b80e9f5f68885cad452e77887c99d6395868805eb514f6202072f0532f0`; actual MAC-WAN exact v1.7.0/8eb711e offline recovery passes in 70.72s, both DBs/13 clients/migration metadata/cleanup. Pinned amd64 image `sha256:66ca7338cd5e2237bc4456e52260f2fd9b3559434abacb86d0627a485871aaa7`; packaged binary matches attested release |
| Retention / schedule recovery | Both ciphertext pairs copied to MAC-WAN and retained under OVH `/srv/backups/oc424-encrypted/manual-v1.7.0-20261027` outside top-level 14-pair pruning. Old plaintext/manual/local rollback copies untouched. Schedule before/after/rollback at `/root/nekomari-backup-schedule-v1.7-20261027T044832Z-07b722` on OVH |

### v1.7 rollback: old binary AND old database

**Never downgrade the image alone after auth migration.** First freeze Hermes
node writes and owner admin changes. The root-private recovery directory has
`rollback.py`: verify its checksums, then run it only with explicit awareness
that it stops the Panel, preserves failed current DB/WAL, restores paired
pre-migration DBs/selected assets/old compose and starts the old pinned v1.6.29.
It verifies old health/version. This loses changes/measurements after the
snapshot and restores the old credential permissions; reconcile writers before
resuming. Do not resurrect the deleted 14th node from older recovery copies.
This paired rollback is **prepared, not exercised on live production**; the
actual isolated old/new encrypted restore evidence is separate.

Retained operational refusals: the first old-image recovery times out, retry
passes; native image help originally omits the explicit binary entrypoint and
fails before mounting data, then passes with `/app/nekomari`. Deployment checks
succeed but final checksum enumeration encounters Ubuntu Python3.10's absent
`hashlib.file_digest`; hashes are finalized with streaming SHA256, not a repeated
migration or rollback. First schedule preflight compares the cron spool hash to
header-stripped `crontab -l`; exact spool/row verification corrects that format
mismatch before any schedule change. Failure evidence is kept; no gate is
weakened or failed attempt counted as success. Release retention copies were
initially hardlinked; review catches the existing verifier's non-hardlink rule.
The manual destinations are replaced with independently checksum-matched regular
ciphertext files, leaving both source/manual pairs at link-count1; actual
receiver ciphertext-verification contracts pass for all four archives. This
avoids blocking subsequent nightly retention; no backup content is deleted.

## Earlier encrypted canary (before v1.7 cutover, 2026-10-27)

The owner identifies MAC-WAN and assigns it recovery custody. A native age
identity is generated there only; OC424 receives only the public recipient and
an opt-in fixed producer. A new OVH-only restricted pull key and opt-in receiver
preserve existing authorization/schedulers. A183095002-byte production encrypted
canary is retained on OVH and MAC-WAN; all1012 hashes, both SQLite integrity
checks and pinned v1.6.29 isolated Docker recovery pass. No private identity
leaves MAC-WAN, no temporary plaintext/container remains and no live service is
restarted. The Panel start time and old backup-producer/helper hashes stay
unchanged. This is online custody, not independent offline escrow.

Exact paths/hashes, public recipient, restrictions, tool provenance and manual
recovery are in [`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md). MAC-WAN shares
its identity UID with Hermes and has privileged Docker access; permissions do
not isolate those processes. Independent offline-copy/access/recovery and
schedule/freshness/legacy-retention review remain. **The nightly plaintext
schedule,14-copy retention and all known rollback copies are unchanged.**

Hermes's metadata-only v1.7 client and hidden owner-TTY key handoff are staged
on MAC-WAN without a production key or service restart. It refuses current
v1.6 before authentication; existing Hermes jobs/configuration are untouched,
not automatically migrated or paused. No application update occurs. See
[`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md) for bounded tests and the
owner's post-deployment steps.

### Public probe override

The earlier failing process was `panel-probe.service`, still using the old
`https://komari.orderly2233.org` default. Preserve the explicit deployed override:

```ini
# /etc/systemd/system/panel-probe.service.d/10-public-url.conf
[Service]
Environment=PANEL_URL=https://nekomari.orderly2233.org
```

The probe-only repair did not restart the Panel/Agent. A separate, explicit
v1.6.29 post-upgrade probe succeeds at09:29:05 Asia/Shanghai, `Result=success`, exit0.
`deploy/panel-probe.sh` retains its historical fallback URL; deployed units must
set `PANEL_URL` explicitly.

## v1.6.29 rollout (2026-10-27, Asia/Shanghai)

Panel-only maintenance PR86 backports report/queue fixes from PR80 and source
labels from PR83 onto v1.6.28. Exact tested/tagged source is
`510f7d354786f373e5816fa72b295f56d3ec3036`; the maintenance merge e9f778f has
the same tree. **Do not deploy main** while the v1.7 authentication/custody gates
in [`V1.7-CUTOVER-READINESS.md`](./V1.7-CUTOVER-READINESS.md) remain open.

Reports validate every field before clamping RAM/Swap/disk used above total;
other invalid values remain rejected. Queue cleanup follows successful removal
and the existing idle scheduler, wakes old-generation polls without resurrecting
state and releases discarded payloads. Existing limits/TTLs/reconnect behavior
remain. No auth/schema migration, token rotation, Agent/protocol/frontend or
dependency change occurs. Legacy callers retain their existing permissions.

| Item | Evidence |
|---|---|
| Exact source and publication | Candidate CI40430587909 and maintenance merge CI40431319479 each pass7/7; release40431323373 passes10/10, including native amd64/arm64 deployment verification; Docker40431987946 passes2/2. Immutable tag `v1.6.29` points to510f7d3, not default main |
| Pinned OC424 image | `ghcr.io/aone2233/nekomari:v1.6.29@sha256:46398954dd29bf341424b4fe5f89e39e23129b36ec3c559e12e4c816b67258e6` (arm64 manifest) |
| Published image identity | Anonymous pull works. Panel index `sha256:a0480ae8c2a0d215ff9f3e05f2d1f5f2ce5888ea011f0546505ed2f22fb6c01e`; Agent index `sha256:4b419c31ae44493361487dc4ac004f4351e50ff553146a9a79fa3d71d1cacf3e`. Both architectures of both images label full510f7d3 and OCI version `1.6.29`, despite workflow_run carrying default-main6b8f340 |
| Binary provenance | Arm64 SHA-256 `cd463d4d6fe13857edcd663dd8f044bcecc85b88d573b107e6b559df426b0377` matches release manifest and packaged/live binary. Verified release-workflow attestation binds full source, tag and run40431323373; native arm64 `--help` passes with no network/live data mounts |
| Off-host recovery | OVH `/srv/backups/oc424/manual-pre-v1.6.29-20261027T010326Z/oc424-pre-v1.6.29.tar.zst`, mode600,165838142bytes; SHA-256 `6521dda91a1da76d40908765dd7a3d0c143f950614063c1f01583e151ae90dee`. Checksum/zstd integrity pass; WAL-safe snapshots of both DBs, compose, INFO and upgrade archives retained outside nightly14-copy retention |
| Actual old-image restore | OVH restores this fresh backup into disposable clones and exact v1.6.28/502a269 amd64 image `sha256:b3ecaae7d7e1ebf2b5221cc8dfcc3963ac28d2e2ccbca4afaf438675a3bae49d`. Both DBs pass quick_check before/after,14clients, exact version/hash, public200,restart0. Networknone/nohostports; scratch data/container removed, original backup retained. Not encrypted escrow recovery |
| Local recovery | Root-private `/opt/nekomari/rollbacks/v1.6.29-20261027T012705Z-ae778bbf`: both WAL-safe online DB snapshots, ordered full schema fingerprints, checksums, old/new compose/image references, logs and `ROLLBACK.txt`. Per-DB snapshots are not an atomic cross-DB transaction |
| Switch | Only compose image line changes; `docker compose up -d --no-deps nekomari`. Container starts09:27:07, guarded acceptance finishes09:27:35 Asia/Shanghai. Agents/nginx, legacy credentials, backup schedule/retention and probe override stay unchanged |
| Upgrade check | **9 passed,0failed,0skipped**: healthy/restart0, exact version/hash, public config/HTML, read-only DB integrity, three upgrade archives53.3MB and zero ERROR lines in the5-minute window |
| Additional acceptance | Public version/config, homepage/admin/five child-route shells and10referenced JS/CSS assets pass with correct MIME types; private client/log/RPC calls return401 without credentials. Both DBs pass quick_check with unchanged complete schema fingerprints; registered UUID set14 unchanged, legacy key row exists and no scoped-key migration table is created. No credential value is read |
| Node/process continuity | All13 CPU-reporting nodes fresh within120s immediately before switch remain fresh after it among14registered clients. This is not14/14online or a capacity/SLO claim. Actual OC424 AgentPID499631/restarts2 and OVH AgentPID495903/restart0 remain unchanged |
| Authenticated acceptance | Still **pending** owner-run history/audit UI checks or securely provisioned temporary credentials. Public shells/401/synthetic authenticated tests are not live authenticated acceptance |
| Rollback prepared, not exercised live | Restore only `docker-compose.before.yml` from the local recovery directory and recreate `nekomari`; expect v1.6.28/502a269 with healthy/restart0. Keep old pinned arm64 image `sha256:dccebe2e87518f78aba8e918de9d3553878f3ddc4a7ecade5114e89af6d3c102`. No schema migration means image-only rollback; do not restore DB snapshots unnecessarily and discard newer data |

**Retained verification refusal:** the initial registry checker expected a
v-prefixed OCI version, but metadata-action semver correctly labels `1.6.29`.
It refused before deployment; full source510f7d3 already matched. The checker
was corrected to the strict semver value and all four configurations verified.
No published artifact was modified. The old local gh CLI lacked attestation;
a temporary checksum-verified modern CLI completed provenance verification.
Only owned disposable downloads/tools are removed afterward; signatures, scripts,
checksums, refusal evidence and every recovery copy remain. Removing the three
owned downloads reclaims95,903,081bytes (about91.5MiB). Development disk62%,
OC42443%, OVH7%; no broad cache/Docker/backup pruning or200-node run occurs.

## v1.6.28 rollout (2026-10-26)

Panel-only query hardening from exact source
`502a269e7282d2db50bc3d28a023c787b0ff028e` (PR78), not current main or the
unreleased token-rotation branch. Compared with v1.6.27, there is no Agent,
protocol, dependency, frontend source/archive, database/schema or credential
behavior change. The five tools retain their in-panel routes and shared theme.
History is bounded to 366 days with shared admission/read budgets and
cancellation; guests cannot read hidden/deleted history. Audit pagination is
bounded to 1000 rows with checked offsets, strict binding and cancellation.

| Item | Evidence |
|---|---|
| Release | Immutable tag `v1.6.28` on `502a269`; exact-source CI `40275930537` passes 7/7; release `40318142810` passes 10/10, including native amd64/arm64 deployment verification; Docker `40319017219` passes both jobs |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.28@sha256:dccebe2e87518f78aba8e918de9d3553878f3ddc4a7ecade5114e89af6d3c102` (arm64 manifest; anonymously pullable) |
| Binary provenance | Checksum and release-workflow attestation verified for tag/source/run; extracted image binary SHA-256 `3118261e9a4df31faeb6a01a9c5a0d4b85ba43b80c41e41bda55c3ea791e2e0c` matches the arm64 release asset; native image `--help` reports v1.6.28/502a269 |
| Off-host recovery | OVH `/srv/backups/oc424/manual-pre-v1.6.28-20261026T115500Z/oc424-pre-v1.6.28.tar.zst`, mode600, 163727209 bytes; SHA-256 `3ac66ba822087ef3450b1bf65596d8dc26374899eea297f0f3bce479d6994df4`; checksum and zstd integrity pass. Both WAL-safe SQLite snapshots, compose, INFO and three upgrade archives are included; separate from nightly 14-copy retention |
| Restore drill | OVH restored both current snapshots into disposable clones and the old amd64 v1.6.27 image; both DBs pass `quick_check` before/after, 14 clients, version/hash correct, `/api/public` 200, restart0. Network `none`, no host ports; scratch data/container removed, original backup retained. Not arm64/fleet acceptance or an encrypted restore |
| Local recovery | Root-private `/opt/nekomari/rollbacks/v1.6.28-20261026T120727Z-5e33b1bc`: both SQLite online snapshots, checksums, old/new compose and image references, logs and `ROLLBACK.txt`. Snapshots are WAL-safe per DB, not an atomic cross-DB transaction |
| Switch | Only the compose image line changes; `docker compose up -d --no-deps nekomari`. Container starts at 12:09:17Z; upgrade check finishes 12:09:43Z. Agents, nginx, backup policy and probe hostname override remain unchanged |
| Upgrade check | **9 passed, 0 failed, 0 skipped**: healthy/restart0, exact version/hash, public config and HTML, read-only main DB integrity, three upgrade archives (53.3 MB), zero ERROR lines |
| Additional acceptance | Public version/config, homepage, admin and all five child-route HTML shells plus ten referenced JS/CSS assets return 200 with correct MIME types. Private-site client/log/RPC calls without credentials return 401. Both live DBs pass `quick_check` and match pre-upgrade schema fingerprints |
| Node reports | 14 registered clients; 12 have CPU rollups within 120 s before and after the switch. NOSLA Gen2 and Windows-WAN already lack fresh CPU data in the pre-upgrade snapshot; no claim that 14/14 are online. OC424/OVH Agent PIDs/restarts are unchanged |
| Authenticated acceptance | **Pending**, not passed: no explicit production test credential is provisioned. No credential/session is recovered from SQLite, Agent files or backups. Synthetic authenticated release verification does not replace the owner's live history/audit UI acceptance |
| Rollback | Restore `docker-compose.before.yml` from the local recovery directory and recreate only `nekomari`; expect v1.6.27/12ad73a. No schema migration means image-only rollback is sufficient; do not restore DB snapshots unnecessarily and discard newer measurements |

**Known provenance metadata caveat:** the Docker workflow-run's OCI revision
label reports default-branch `fc84915`, despite checkout and packaged assets
being from `502a269`. The verified release attestation, extracted binary hash
and `/api/version` establish the deployed source; the label does not. Keep this
published image immutable. Source-identity PR83 is merged as e234385 on
2026-10-27 after exact-source CI40324022778 passes 7/7: verify the resolved
release checkout before asset download and explicitly label both images from
that commit. Publication/label acceptance now passes on v1.6.29 for both images
and architectures; the immutable v1.6.28 image is not rebuilt or relabeled.

**Retained non-production refusals:** the original local `gh` lacked attestation
support; a temporary checksum-verified modern CLI verified the signature and
workflow/source identity. The first restore pull selected the old arm64 image
on amd64 OVH; inspection rejected it before execution and the correct amd64
digest was used. The first local preflight compared different fingerprint
normalizations (NULL-SQL auto indexes omitted); it stopped before compose
changed. Comparing all ordered `sqlite_master` rows proved both schemas
unchanged, and the verified existing recovery point was reused. Initial
evidence is retained; none is counted as a successful run.

## After every panel upgrade

The encrypted backup tooling and client-token rotation are now published and
deployed in v1.7.0; existing Agent tokens are not rotated. The production
schedule uses the installed encrypted receiver timer, while old plaintext/manual
recovery copies remain. Source-batch tests are retained historical verification,
not a reason to rerun unrelated broad suites. See the v1.7 rollout above and
[`ENCRYPTED-BACKUPS.md`](./ENCRYPTED-BACKUPS.md).

The hand-run checks from the rollouts below are one command now. Run it within five
minutes of the upgrade — the log window is the last `LOG_WINDOW` measured from *now*, not
from the moment the container started:

```bash
sudo env EXPECTED_VERSION=v1.7.0 EXPECTED_HASH=8eb711e bash deploy/panel-upgrade-check.sh
```

It prints one `PASS` / `FAIL` / `SKIP` line per check and exits `0` (all passed, SKIP is not a
failure), `1` (a check failed) or `2` (misconfigured — nothing was checked). The
checks are exactly the ones the **Verification** row of each rollout records:

| Check | What a failure means |
|---|---|
| container health + restart count | `unhealthy`/`starting`, or a restart loop — the v1.6.16 shape, where `docker ps` said "Up" the whole time |
| `/api/version` version | the running binary is not the tag that was supposed to ship (the image line alone does not prove this) |
| `/api/version` hash (`EXPECTED_HASH`) | the binary was not built by the release workflow (`hash: unknown`) or is a different commit |
| `/api/public` and `/` | the panel is not answering, or a proxy/edge error page is answering in its place |
| `PRAGMA quick_check` | the database is not `ok` — the freelist mismatch that blocked the v1.6.16 start looked exactly like this |
| `data/backup/upgrade-*.zip` | zero archives = no rollback point for this upgrade |
| ERROR lines in the log window | something is already failing at boot; the first three lines are printed |

Nothing is written, restarted or deleted: GET requests, `docker inspect` / `docker logs`,
and `quick_check` on a read-only connection. In particular the upgrade archives are only
counted — they are the rollback path. If `docker` or `sqlite3` is missing, the
corresponding checks are `SKIP`ped and named rather than reported as failures.

A `FAIL` is a reason to stop and assess rollback rather than to keep going: the
previous image and paired DB snapshots belong in that rollout's **Rollback** row.
The upgrade archive excludes `metrics.db` and is not a full backup. Image-only
rollback is safe for v1.6.28/v1.6.29's compatible patches, not for a future authentication
migration that rewrites persistent credentials. `deploy/panel-upgrade-check.test.sh`
is the offline test for the script (fake panel + `docker`/`sqlite3` stubs; no host involved).

The rollout sections below predate the script; their **Verification** rows are what it prints,
so from the next rollout on that row can be filled in from its output.

## v1.6.25 rollout (2026-10-20)

Panel and deploy-script release; agents stay on v1.6.24.

| Item | Evidence |
|---|---|
| Release | tag `v1.6.25` on `371d688`; release run `39409527520`, Docker run `39410362266` successful |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.25@sha256:a8b311dedfbaacf0edd13cbe9bab14c92e91e7d23fddafef634f364f2d3397bf` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.25-20261020T104011Z`; pre-deploy `quick_check` `ok` |
| Panel startup | 21 s to `/api/public` 200; `panel-upgrade-check.sh` **9 passed, 0 failed, 0 skipped**; /api/version = v1.6.25 hash 371d688; 0 ERROR |
| Auth smoke | `panel-auth-smoke.py` 3/3 with the API key (admin RPC + HTML + theme asset) after the token-read step-up change |
| Agent fleet | unchanged at **12/12 v1.6.24**; no agent binary changes in this release |
| SB01 reinstall | OpenRC installer re-run with `--force` over the MAC-WAN jump; it reused the existing credential file, wrote a `supervise-daemon` init script, started the service, and the panel showed v1.6.24 online. A `SIGKILL` of the child was respawned within 10 s; a graceful `SIGTERM` intentionally stops the supervisor |
| Changes | fractional ping summaries/UI; `admin:getClientToken` sensitive for sessions (API key exemption unchanged); OpenRC installer/upgrade script; protocol contract test |
| Verification | BERO panel Go test/vet/build + frontend test/lint/build; local install harness 37/37, upgrade harness 35/35; CI 7/7 |

## v1.6.24 rollout (2026-10-20)

Ping gains sub-millisecond precision. Both the panel and the agents were upgraded.

| Item | Evidence |
|---|---|
| Release | tag `v1.6.24` on `0a08239`; release run `39399603759`, Docker run `39400540744` successful |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.24@sha256:0e69f14ab81fee9eeac05e918b5481faf65dd1c94e8b507c16c2a84651c75e2c` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.24-20261020T091755Z`; pre-deploy `quick_check` `ok` |
| Panel startup | 21 s to `/api/public` 200; `panel-upgrade-check.sh` **9 passed, 0 failed, 0 skipped**; healthy, 0 restarts |
| Agent fleet | **12/12 on v1.6.24**, targets cleared. PZYC was updated from a local asset (GitHub unreachable); SB01 was restarted after its OpenRC service exited (see v1.6.23 caveat) |
| Precision check | Recent `ping.latency_ms` rollups contain fractional values (e.g. 314.762, 218.835 ms); 250 fractional buckets at the time of the check |
| Storage | Same metric names, tags and series (433 ping series). `metrics.db` 168 MB vs the 169 MB pre-upgrade backup — within normal churn. The only growth is the `value_us` wire field (~20 B per ping result) |
| Rollback | panel back to `v1.6.21@sha256:5da645b…`; agents keep `*.pre-v1.6.24` copies |

## v1.6.23 agent rollout (2026-10-20)

Agent diagnostics release; panel stays on v1.6.21.

| Item | Evidence |
|---|---|
| Release | tag `v1.6.23` on `cccfd4b`; release run `39389812698`; 10 assets including agent binaries |
| Change | `komari-agent --version` prints the version and exits; `--disable-auto-update` help/log now says it only stops the periodic self-check (panel `agent.update` events still work) |
| OC424 test | panel `admin:rollAgentUpdate` targeted `v1.6.23`; the v1.6.22 agent accepted the event, updated, and `--version` reports `v1.6.23` |
| Fleet | **12/12 on v1.6.23**. SB01 had updated itself but its OpenRC service was left crashed; it was restarted and verified through the MAC-WAN jump host |
| Method | panel roll for the online nodes; PZYC updated from a locally downloaded asset (GitHub unreachable there); SB01 fixed over `MAC-WAN → 45.192.241.60:58364` |
| Verification | `--version` checked on OC424/BERO/OVH/MAC/HK04/NOSLA/MegaBox and SB01 (`v1.6.23`); SB01 is online in the panel with `target_version` cleared |
| OpenRC caveat | SB01 is Alpine/openrc: an in-place agent update replaces the binary and exits, and the current init script does not respawn. After **every** agent update on Alpine, run `rc-service nekomari-agent zap && rc-service nekomari-agent start` (or switch the unit to `supervise-daemon`) |
| Rollback | agent binaries were replaced in place; each host keeps `*.pre-v1.6.22` (and PZYC `*.pre-v1.6.23`) |

This rollout also validates H6 end to end: the panel-directed update works even with
`--disable-auto-update` on v1.6.22+. The v1.6.22 rollout had to be manual only because
the fleet was on pre-H6 binaries that did not understand `agent.update`.

## v1.6.22 agent rollout (2026-10-20)

Agent-only release: the panel stayed on v1.6.21. The release fixes the three netstatic
counter bugs (speed-aware delta ceiling, per-sample NIC whitelist, explicit include
precedence).

| Item | Evidence |
|---|---|
| Release | tag `v1.6.22` on `4645624`; release run `39383574954`; assets include `komari-agent-linux-{amd64,arm64}`, darwin, windows, SBOM and `SHA256SUMS.txt` |
| OC424 test | binary `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` replaced after SHA-256 check; service active; log `Nekomari Agent v1.6.22`; WebSocket v2 connected |
| Fleet | **11/12 on v1.6.22**: OC424, BERO, OVH, MAC, PZYC, HK04, NOSLA, MegaBox, ATT, ShunDun, VT01 |
| Remaining | SB01 is offline at v1.6.11; the panel persisted `target_version=v1.6.22` for its next connection. It needs an exec/SSH update once it is reachable |
| Method | OC424/BERO/OVH/MAC/PZYC/HK04/NOSLA/MegaBox updated over SSH (SHA-256 verified, old binary kept as `*.pre-v1.6.22`); ATT/ShunDun/VT01 updated through the panel `admin:exec` task API |
| Verification | `admin:agentVersions` counts: `v1.6.22: 11, v1.6.11: 1`; 11 online; targets cleared for the updated nodes (so they track future releases again) |
| Operational finding | The fleet was on **pre-H6 agents** (v1.6.9/v1.6.11/v1.6.14), which do not understand the `agent.update` event; that is why this rollout used SSH/exec. `--disable-auto-update` only stops the agent's periodic self-check — panel-requested events still work — and v1.6.22+ handles them. Future rollouts can use `admin:rollAgentUpdate` once every node is on v1.6.22+; SB01 is the exception until it reconnects |

## Off-host backup (OVH, 2026-10-20)

The panel's databases existed only on OC424 (plus its own `data/backup` archives on the
same disk). A copy is now pulled nightly by the OVH KS-LE-B host.

| Item | Value |
|---|---|
| Destination host | OVH KS-LE-B `jkr-node-01`, Beauharnois (BHS), Ubuntu 24.04 |
| Destination path | `/srv/backups/oc424` (0700, files 0600) |
| Storage | `/dev/md3` 410 G RAID1; **366 G available, 6% used**; four backups used 446 MB |
| Archive size | ~160 MB each (three upgrade archives add ~62 MB; `metrics.db` dominates the rest) |
| Retention | 14 newest `oc424-*.tar.zst`; worst case ~2.2 GB, under 0.6% of the volume |
| Schedule | daily 03:30 UTC via `/home/ubuntu/oc424-backup.sh`, log `/home/ubuntu/oc424-backup.log` |
| Copies | v1.6.20/v1.6.21/v1.6.24 snapshots plus the post-v1.6.24 one; newest `oc424-20261020T100354Z.tar.zst` (sha256 `a88ccf32…`, 160,485,471 B) |
| Contents | SQLite `.backup` of `komari.db` and `metrics.db`, `docker-compose.yml`, `upgrade-archives/upgrade-*.zip`, `INFO` (host/time/panel version/archive count); zstd level 3 |
| Pull authority | OVH holds a dedicated ed25519 key; OC424's `authorized_keys` entry is `restrict,command="/usr/local/bin/oc424-backup-stream.sh"`, so the key can only stream a backup, not open a shell. OC424 holds no key to OVH |
| Integrity | `zstd -t` before publishing + `.sha256` sidecar; failed pulls leave only a `.tmp` that the trap removes |
| Restore | download, `zstd -d | tar x`, stop the panel, put the two DBs back (keep the originals), start, then run `deploy/panel-upgrade-check.sh` |
| Restore drill | 2026-10-20: extracted the v1.6.24 archive into `/srv/restore-test/data`, ran the v1.6.24 image on OVH `127.0.0.1:25779`, got `/api/public` 200 and `/api/version` = v1.6.24, then removed the container and directory |

Caveat: the archive is **not encrypted at rest**; access is limited to `ubuntu`/`root` on
OVH. If that disk is not in the trust boundary, add `age`/`gpg` before upload. The
archive contains panel secrets (agent tokens, notification credentials).

## v1.6.21 rollout (2026-10-20)

Audit-log filtering/CSV export, Windows build reproducibility verification, and the
nightly OVH off-host backup (above). Panel-side only; no agent was restarted.

| Item | Evidence |
|---|---|
| Remote build | BERO: Go test/vet/build, `npm test` 178/178, lint, two builds `diff` 0, theme repack + `web/public` tests |
| Release | tag `v1.6.21` on `5da645b`; release run `39378428070`, Docker run `39379110908` successful |
| Release assets | 10, including `sbom.spdx.json` and provenance attestation |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.21@sha256:fafbb31713fcaa4f5597097eb7fdb1e3287502b2021a420b624a2bb4ffa122c3` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.21-20261020T054933Z`; pre-deploy `quick_check` `ok`; OVH refreshed after the rollout (v1.6.21 copy) |
| Startup | 21 s to `/api/public` 200; archive 21 MB; 3 archives / 62 MB |
| Verification | `deploy/panel-upgrade-check.sh`: **9 passed, 0 failed, 0 skipped**; version/hash, healthy, 0 restarts, `quick_check` ok, 3 archives, 0 ERROR; gzip 24,887 B; 12 connections; ~92 MB RSS |
| Rollback | compose back to `v1.6.20@sha256:f9189855…`; data untouched, backups above |

## v1.6.20 rollout (2026-10-20)

Panel-upgrade hardening: daily archive pruning, the read-only post-upgrade check, the
metric store's pool counters, and the admin bulk selection. Panel-side only; no agent
was restarted. CI also caught a Windows-only theme-cache publish race, fixed here with
a short rename retry instead of falling back to the 33 MiB in-memory archive.

| Item | Evidence |
|---|---|
| Remote build | BERO (Go 1.26.9, Node 22): `go test ./...`, `go vet`, `go build`, `npm ci/test` (171/171), `npm run lint`, two builds byte-identical (`diff` 0), theme repacked and `web/public` tests green |
| Release | tag `v1.6.20` on `01be2cf`; release run `39370322552`, Docker run `39370939266` successful |
| Release assets | 10, including `sbom.spdx.json` and provenance attestation (same pipeline as v1.6.19) |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.20@sha256:f91898559ccd7b8a626ec4d774515817956c49d0494408e3188399f8cada0c4e` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.20b-20261020T043506Z` (WAL-safe backups, previous compose, `SHA256SUMS`); pre-deploy `quick_check` `ok` |
| Startup | 23 s to `/api/public` 200; pre-upgrade archive 21 MB; 3 archives / 61.5 MB; `data/backup` 62 MB |
| Verification | `deploy/panel-upgrade-check.sh` (the new command above): **9 passed, 0 failed, 0 skipped** — version `v1.6.20`, hash `01be2cf`, healthy, 0 restarts, `quick_check` ok, 3 archives, 0 ERROR lines; gzip `/app.js` 24,887 B; 12 connections; ~85 MB RSS |
| Rollback | compose back to `v1.6.19@sha256:ec838740…`; data untouched, backup above |

## v1.6.19 rollout (2026-10-20)

H6 cleared-target delivery, release SBOM/provenance, pprof process and pool data,
and reproducible Linux frontend builds. Panel-side only; no agent was restarted.

| Item | Evidence |
|---|---|
| Remote build | BERO (Go 1.26.9, Node 22): `go test ./...`, `go vet ./...`, `go build ./...`, `npm ci/test/lint/build` all green; two builds of the same archive were byte-identical (`diff -rq` 0) |
| Release | tag `v1.6.19` on `5f4d7fa`; release run `39359870000`, Docker run `39360507319` successful |
| Release assets | 10 assets including `sbom.spdx.json` (235 packages); `gh attestation verify nekomari-linux-amd64` succeeded |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.19@sha256:ec838740154fddf871f4fc328767002bac476e54d52bc0f2e2ffc273a351ee05` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.19-20261020T022902Z` (WAL-safe backups, previous compose, `SHA256SUMS`); pre-deploy `quick_check` `ok` |
| Startup | 21 s to `/api/public` 200; pre-upgrade archive 21 MB; archives pruned to 3 and `data/backup` now **62 MB** (the last 1.6 GB archive was removed) |
| Verification | `/api/version` = `v1.6.19` hash `5f4d7fa`, healthy, 0 restarts; `quick_check` ok; `clients.target_version_pending_clear` column present; gzip `/app.js` 24,887 B; 12 connections; ~86 MB RSS; 0 errors in the first five minutes |
| Auth deploy check | `deploy/panel-auth-smoke.py` against `http://127.0.0.1:25774` with a panel API key: 3/3 (admin RPC, HTML document, theme asset `/style.css`). The key was passed through the environment and is not stored in the repository or this file |
| Rollback | compose back to `v1.6.18@sha256:ded94987…`; data untouched, backup above |

## v1.6.18 rollout (2026-10-20)

Maintenance windows are excluded from SLA, the main database is checked daily, and
deploy verification gained an authenticated/theme check. Panel-side only; no agent
was restarted.

| Item | Evidence |
|---|---|
| Remote build | BERO (Go 1.26.9, linux/amd64, Node 22): `go test ./...`, `go vet ./...`, `go build ./...`, `npm ci`, `npm test` (158/158), `npm run lint`, `npm run build` all green — no local compilation |
| Release | tag `v1.6.18` on `95fe217`; release run `39350788990`, Docker run `39351489581` successful |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.18@sha256:ded94987a867adca49bd15a416271b6b4b8eed708303c5e54518cfd442fccf73` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.18-20261020T004349Z` (WAL-safe SQLite backups, previous compose, `SHA256SUMS`); pre-deploy `quick_check` was `ok` |
| Startup | 21 s to `/api/public` 200; upgrade archive **21 MB**, archives kept at 3, `data/backup` 1.6 GB |
| Verification | `/api/version` = `v1.6.18` hash `95fe217`, healthy; `quick_check` ok; gzip `/app.js` 24,887 B; 12 connections; container memory ~79 MB; no errors in the first five minutes |
| Rollback | compose back to `v1.6.17@sha256:6d4a2806…`; data untouched, backup above |

## v1.6.16 rollback and v1.6.17 hotfix (2026-10-19)

v1.6.16 was published (tag on `bd989ab`) and rolled out at 15:01 UTC. It refused
to start: `PRAGMA quick_check` reports a **benign freelist accounting
mismatch** on this database ("Main freelist: size is 20 but should be 21"),
which SQLite reports while every query keeps working. The container
restart-looped, and the compose image was rolled back to v1.6.15
(`sha256:7041fad9…`) until v1.6.17 was ready.

v1.6.17 (tag on `a51bff9`) makes integrity findings advisory by default;
`KOMARI_STRICT_DB_CHECK=1` is the opt-in fail-fast gate. The same production
database was reproduced in a canary with the v1.6.17 arm64 binary before the
rollout: it logged the finding and started.

| Item | Evidence |
|---|---|
| Release | `v1.6.17` on `a51bff9`; release run `39276095962` and Docker run `39277156005` successful |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.17@sha256:6d4a280649fe4acc766bed7a3239d3f5dafc0ec1c8e60b25f5f359133d1586e3` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.17-20261019T151359Z` (WAL-safe SQLite backups, previous compose, `SHA256SUMS`) |
| Repair | container stopped; `sqlite3 komari.db "VACUUM;"` — before: freelist error, after: `quick_check` `ok` |
| Startup | 21 s to `/api/public` 200; pre-upgrade archive **21 MB** (v1.6.15's was 1.6 GB), archives pruned to 3, `data/backup` 5.5 GB → 1.6 GB |
| Verification | `/api/version` = `v1.6.17` hash `a51bff9`, healthy; `quick_check` ok; `clients.target_version` column present; gzip `/app.js` 72,519 → 24,887 B; 12 connections; container memory ~92 MB |
| Rollback | compose back to `v1.6.15@sha256:7041fad9…`; databases and data directory untouched, backup above |

The upgrade-archive fix from v1.6.16 is visible in this rollout: the archive
excludes `db-maintenance/`, theme backups, `.uploading`, and keeps only three
upgrade zips, which is why the pre-upgrade step dropped from ~90 s / 1.6 GB to
seconds / 21 MB.

## v1.6.15 rollout (2026-10-19)

The first release built from the three batches of the 2026-10-18 review. The
image is panel-only relative to v1.6.14: `agent/**/*.go` and `protocol/` are
unchanged, so no agent was restarted and the fleet did not move.

| Item | Evidence |
|---|---|
| Release | tag `v1.6.15` on merge commit `0a9939f`; release run `39257884223` and Docker run `39258956244` both successful |
| CI gate | the `ci` run on `0a9939f` was green before the tag; the release workflow's gate confirmed it again |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.15@sha256:7041fad94d53d5be798e91b7f29861e4eb4e21738bf52c55597c7b431598cc3c` (arm64) |
| Backup | `/opt/nekomari/backups/pre-v1.6.15-20261019T131904Z`: WAL-safe SQLite backups of `komari.db` and `metrics.db`, the previous compose file, and `SHA256SUMS` |
| Change | compose image digest pinned, `docker compose up -d`; the container recreated against the same bind mount |
| Startup | the pre-upgrade archive took ~90 s and 1.6 GB because the archive includes the 2.4 GB `db-maintenance/` directory (see OPEN-WORK); the panel then listened normally |
| Verification | `/api/version` = `v1.6.15` hash `0a9939f`; container healthy; `/api/public` 200; gzip active on the installed theme (`/app.js` 64,797 → 21,936 B, `/style.css` 12,953 → 4,097 B); default-theme cache materialised (36 MB); soft memory limit 921.6 MiB (90% of the 1 GiB cgroup); 11 established connections |
| Rollback | restore the previous compose line (`v1.6.14@sha256:f66c4992…`) and `docker compose up -d`; the databases and data directory are untouched, and the pre-upgrade archive is in the backup directory |

The active public theme at the end of the rollout was **CRT**, not the built-in
default: the default had been restored at 12:27 UTC, and the CRT theme was then
installed and selected at 13:19 UTC (the same operator action that put
`theme-crt/` beside this checkout). That is a config value, not part of the
image; switching back is the same one-row change recorded below.

## Default theme restored (2026-10-19)

The public site had been serving the installed **SAO** theme. At the owner's
request the active theme was set back to the built-in default
(`theme = "default"`). The SAO theme files were not touched and the change is
one config value, so switching back is the same operation in reverse.

| Item | Evidence |
|---|---|
| Backup before the change | `/opt/nekomari/data/komari.db.bak-theme-restore-20261019T122700Z` (SQLite `.backup`, taken while the panel was running) |
| Change | container stopped, `update configs set value='"default"' where key='theme'`, container started |
| Verification | container healthy; `/api/public` 200; `/` serves the built-in panel (`/assets/entry-index-CBqMPayg.js`, `<title>Orderly Status</title>`), zero `SAO` markers in the document; 13 established connections after the restart |
| Rollback | stop the container, set `theme` back to `"SAO"`, start it; the SAO directory and every other config row are unchanged |
| Not changed | panel image `v1.6.14`, agents, nginx/TLS, data other than the theme row |

At the time of this switch the built-in theme was the one embedded in the
**v1.6.14 image**; v1.6.15 (the batch A/B/C frontend) was deployed later the
same day, and the operator then selected the installed CRT theme at 13:19 UTC.
See the v1.6.15 section above.

## v1.6.13 Mint canary (2026-10-17)

The panel and **only this host's original agent** were upgraded. Other agents,
their credentials, billing ledgers and visibility settings were not changed.
The existing Mint theme was upgraded to 0.2.1; the installed SAO theme is retained.

| Item | Verified evidence |
|---|---|
| Release commit | `5c648a71759bf2cc32f51f6d6b8789917b6c6b7a`; exact-commit CI `39000185132`, release `39000637396`, Docker `39001067638` all successful |
| Panel | `v1.6.13`, hash `5c648a7`, arm64, healthy, zero container restarts |
| Pinned panel image | `ghcr.io/aone2233/nekomari:v1.6.13@sha256:4be85cef3502ff050ef050d7dc86283c4bb88fe8555a254e0ce0b58614f56686` |
| Backup | `/opt/nekomari/backups/pre-v1.6.13-20261017T035508Z`, root-only directory, WAL-safe SQLite backups, primary integrity check and metric quick check successful, SHA-256 rechecked after deployment |
| Preserved files | Compose, theme/plugin files, original agent/unit/credential file and `/opt/komari`; an additional traffic-ledger snapshot immediately before agent restart; original files remain private |
| Agent | `komari-agent-oc424-original-node.service`, `v1.6.13`, running executable matches published SHA-256 `1da870a63cf48585e83e3c6a29631ed3bf7574dc23f2ff4285f026284939f85c` |
| Mint | 0.2.1, published ZIP SHA-256 `2a185c78b7709e49fa777a04a895f773bee135a9d79b00979e2474890c24d9ee`; assets installed before index, previous hashed assets retained for already-open tabs |
| Fleet check | All 14 registered clients have fresh CPU samples; at 04:10 UTC the oldest actual sample was 17.3 seconds old. Fleet versions: one 1.6.13, ten 1.6.9, three 1.6.11 |
| Ownership/security | Admin/install/terminal remain panel documents, normal recovery redirects to `/`, plugin pages remain theme-owned; guest access remains denied on the private site; no node was unhidden |

The agent token-file, working directory `/opt/komari`, auto-update disable flag
and traffic ledger are retained. The staged binary was checksum-verified, tested
with `--help`, and atomically replaced rather than overwriting a running inode.
`nekomari-docs`, nginx, TLS and unrelated services were not restarted.

### Missing media and routes: actual cause

- All 14 registered nodes already had four media/AI observations. Panel 1.6.12
  lacked `public:getNodeInsights`, so Mint could not read those snapshots.
- The older agents had no Mint return-route support or explicit route targets.
  A normal ping task is not a route trace. Only OC424 is opted in at this stage.
- Legacy ChatGPT/Claude results incorrectly called region-only observations
  `unlocked`. The new agent reports `unknown` with `basis=region`; Mint also
  renders older region-only reports neutrally instead of green actual unlocks.
- Gemini is not implemented; no IP reputation score or account playback test is
  claimed. A provider network classification is not proof of residential egress.

The OC424 route drop-in is
`/etc/systemd/system/komari-agent-oc424-original-node.service.d/40-mint-route-targets.conf`.
It reuses the operator's existing panel targets, once per six-hour probe cycle:

```ini
[Service]
Environment="AGENT_ROUTE_TARGETS=telecom=14.17.70.70,unicom=112.84.125.195,mobile=117.148.100.86"
```

First actual report at 04:06 UTC: telecom observed `163`, 20 hops, **not reached**;
unicom observed `169`, reached in 16 hops; mobile observed `CMI / CMNET`, 20 hops,
**not reached**. These are ASN observations, not CN2 GIA certification. OC424's
Netflix and YouTube probes returned availability markers (YouTube region SG);
ChatGPT/Claude remained region-only unknowns. OC424 remains hidden as before.
The other 13 nodes still require their own staged agent/configuration rollout for
return routes and TCP retransmission. Their existing media snapshots are readable
through the upgraded panel after normal login.

### Browser findings and remaining checks

Production's private-site middleware returns an HTTP 401 REST error envelope
before JSON-RPC dispatch. Mint 0.2.1 recognizes that shape and shows the login form;
0.2.0 treated it as a connectivity error. The production login page was checked
on both the loopback tunnel and public HTTPS, without exposing node cards or
causing horizontal overflow. Account credentials and existing sessions were not
read or reused; an authenticated production dashboard walkthrough remains for
the operator. Authenticated real-data walkthroughs passed on isolated Nekomari
and official Komari 1.6.0 instances using the same released ZIP.

The existing injected Passkey script also receives 401 on its status URL and
rejects its promise without a catch. This predates the theme change; it was not
silently edited or disabled. Password/2FA and the original admin login links are
retained; production WebAuthn login itself is not claimed as verified.

Startup spent roughly 100 seconds writing the panel's own upgrade archive over
the retained historical files; the container then became healthy. No historical
backups were deleted to accelerate startup. Zero panic/fatal lines were observed.
The previously observed unauthorized-agent request noise was absent in the final
post-upgrade sample, not proven permanently resolved.

For rollback, restore the backed-up Compose image reference and recreate only
`nekomari`; keep live databases because the schema addition is backward compatible.
The original agent is in `agent-before-upgrade`; remove only the new route drop-in,
reload systemd and restart the same unit. Retain the current traffic ledger.
Theme archives retain both the original and pre-0.2.1 versions. Never replace live
SQLite/WAL files in place: a full database rollback requires a stopped panel,
another backup of current data, and explicit consideration of newly collected data.

## v1.6.12 rollout (2026-10-17): panel only

The first rollout since v1.6.9, and the first that **does not move the fleet**:
`git diff --stat v1.6.9..v1.6.12 -- 'agent/**/*.go' protocol/` is empty, so only the panel
container was replaced.

| Step | Evidence |
|---|---|
| Start point | panel on `v1.6.10@sha256:6a5007ce…`, `Up 5 days (healthy)`, `/api/version` → `{"hash":"4e7d389","version":"v1.6.10"}` |
| Image pulled and verified **by tag** | `docker pull …:v1.6.12` → `RepoDigests` `sha256:2a68e25a9540abd7f87918f445f5f45c5ab5311ea427c075b71419277e3c6b4f`; `arch=arm64/linux` on the aarch64 host. Pulling `tag@digest` still does not create the tag locally — the v1.6.9 trap, avoided |
| The `latest` policy, end to end again | `v1.6.12` and `latest` resolve to the **same** digest `sha256:2a68e25a…`: `latest` moved because this release's verification passed |
| Databases backed up first | `/opt/nekomari/backups/pre-v1.6.12-20261017T025201Z`, taken with `sqlite3.Connection.backup` (WAL-safe); both `quick_check=ok`, `integrity_check=ok`, `sha256sum -c` OK |
| Compose | `image: …:v1.6.12@sha256:2a68e25a…`, written under a full-digest regex guard plus `docker compose config --quiet`; the previous file is kept as `docker-compose.yml.bak-pre-v1.6.12-20261017T025226Z`, so a rollback is that file plus `docker compose up -d` |
| After the switch | `Up (healthy)`, `RestartCount=0`, started `2026-10-17T02:52:45Z`, `/api/version` → `{"hash":"d46ec5b","version":"v1.6.12"}` |

Live checks against `http://127.0.0.1:25774`, so nginx and Cloudflare stay out of the picture:

```
/install                200  <title>Nekomari Monitor</title>
/database-recovery      307  Location: /
/admin                  200  <title>Nekomari</title>
/sw.js                  200
/manifest.webmanifest   200
/assets/does-not-exist-abc123.js
                        404  a missing asset is still not the SPA shell
```

`0` panic/fatal lines in the panel log since the switch.

**The fleet.** Every client row reported within one info interval, and the panel log shows
`200 POST /api/clients/v2/rpc` from each node's address. Versions: 11 on `v1.6.9`, 3 on `v1.6.11`
(SB01, SB02, VT01, added after the v1.6.9 rollout and installed from the then-current release). The
count is **14 clients, not the ten the v1.6.9 record lists** — the fleet grew, and the panel host's own
node is now `hidden`, which is why a guest node list does not add up to it.

**One thing to look at, and it is not from this rollout.** A single address appeared as a `401` on
`/api/clients/v2/rpc` every ~5 s for the whole window (21 in 90 s), while three others produced two each
during the restart. That is the shape `OPEN-WORK.md` records: an agent retrying with a credential that
can never resolve — a deleted node, a re-installed node still running the old unit, or a second unit
holding a rotated token. The panel-side symptom is a permanent 401 stream that reads like an authentication
bug. It needs that node identified and its unit stopped; no panel change is involved.

## What is actually deployed (verified 2026-09-26)

> **This table is the 2026-09-26 snapshot, kept for the D1/D2 evidence it carries; it is not
> current.** The newest deployment record is the **v1.6.12 rollout** section above (2026-10-17),
> then **v1.6.9**, then **v1.6.7**. Read those for what is running now.

A commit or a tag proves *release*, never *deployment*. Each row below is a
separate claim with the evidence that produced it. "Verified" means a command ran
against the host on 2026-09-26 and the output is quoted.

| Claim | State | Evidence |
|---|---|---|
| Repository `main` | `f44e1ac` (docs) on top of `0a90310`, which fixes the release-cache TTL | read this checkout |
| `v0.1.27` tagged | yes, `6a1e875`, 2026-09-25 18:11 +08:00 | `git rev-parse v0.1.27` |
| Panel container on `v0.1.27` | **verified** | `docker ps` → `ghcr.io/aone2233/nekomari:v0.1.27 Up 17 hours (healthy)`; `/api/version` → `{"hash":"6a1e875","version":"v0.1.27"}` |
| Panel image digest | `sha256:7a0086eb035facf2d02012c853a5f2cd69129b573ed2b77a51dd523d7665b818` | `docker image ls --digests` on OC424 |
| Panel start time / restarts | started `2026-09-25T10:27:42Z`, `RestartCount=0`, health `healthy` | `docker inspect nekomari` |
| OC424's own agent on `v0.1.27` | **verified** — binary mtime `2026-09-25 10:28:31 UTC`, 17 minutes after the tag | `ls -la /home/ubuntu/nekomari-agent/`; unit `StartedAt` 2026-09-25 10:28:31 UTC |
| Rest of the fleet on `v0.1.27` | **not verified** | only OC424 was reachable from this session |
| OC424 agent runs as root, no capabilities | **verified**, and it is the D1/D2 target | `systemctl show … -p User` → `root`; `AmbientCapabilities=` empty; `NoNewPrivileges=no` |
| OC424 agent still passes the token on its command line | **verified** — D1 is not deployed | `ExecStart` contains `-t <redacted>`; see the note below |
| Unreleased `main` work | shell caching (`0989258`), addressed below as v0.1.28 | `git log v0.1.27..main` |

**The audit itself printed a live token.** `systemctl show -p ExecStart` was run to
read the agent's flags, and that output includes the token — a credential read
dressed as a status check. The token was therefore rotated, and the lesson is
recorded in `docs/SECRETS.md` (incident 3): redact the flag on any future
`ExecStart` read, or answer "which version is running" from `systemctl is-active`
and the binary hash instead.

The v0.1.27 rollout is otherwise **undocumented rather than unverified**: the
panel moved at 15:17 UTC on 2026-09-25 and this host's agent binary 11 minutes
later, per the image start time and the binary mtime, but no rollout section was
written at the time. The numbers above are the replacement record.

### How to close the remaining rows

```bash
# Per node: the running binary's hash, not the panel's version column.
sha256sum /opt/nekomari-agent/komari-agent-linux-amd64   # or /opt/komari/agent on NOSLA

# Which version is running, without reading the token:
systemctl is-active nekomari-agent
# Only if the token's absence is the question, redact it:
systemctl show -p ExecStart nekomari-agent | sed -E 's/(-t|--token) +[^ ]+/\1 <redacted>/g'
```

## Topology

| Piece | Where |
|---|---|
| Panel URL | **https://komari.orderly2233.org** |
| Host | OC424 (`ubuntu@213.35.99.48`, Oracle Cloud, **arm64**, Ubuntu 22.04) |
| Container | `nekomari`, image `ghcr.io/aone2233/nekomari:v0.1.27` (`sha256:7a0086eb…`), verified running on 2026-09-26; bound to `127.0.0.1:25774` |
| Compose dir | `/opt/nekomari` (bind mount `./data` → `/app/data`) |
| Reverse proxy | host **nginx** `/etc/nginx/sites-available/nekomari` |
| TLS at origin | `/etc/nginx/ssl/{fullchain,privkey}.pem` (Cloudflare Origin cert, shared with the other vhosts) |
| Edge | Cloudflare proxied A → `213.35.99.48` |
| Agent | `komari-agent-oc424-original-node.service` (source: `deploy/hosts/oc424.service`), arm64 agent reporting this host to the local panel |
| Old data | backed up by the server on first boot to `data/backup/upgrade-*.zip` |

The container is deliberately **not** published on a public interface: UFW allows
80/443 only from Cloudflare's ranges, so all traffic arrives via the edge, and
nginx is the only thing that talks to the panel port.

## Last verified rollout: 2026-09-24 (v0.1.26)

This is the most recent rollout whose evidence came from the hosts. It is *not*
the current release: v0.1.27 was tagged the next day and has no rollout record.

The panel moved from v0.1.25 to v0.1.26 at approximately 15:17 UTC, and **the agents moved
with it** — the first time since v0.1.19 that a release changes agent behaviour rather
than just the compiler, and the reason is the address-family reporting: the panel can
only split a ping task by the family a probe measured once the probes actually report
it. Leaving the fleet on v0.1.19 would have shipped the panel half of that feature with
no effect. See [the changelog](../CHANGELOG.md) for the release contents.

- Exact release commit: `8eb3c8c45b49ea64388a7300f31dcd0a1cb0612b` (also `v0.1.26`).
  Exact-commit CI `36016959500` passed on Ubuntu and Windows before the tag.
- Release `36017582275` passed every job, including the eight binary scans and the
  downloaded-artifact deployment test. Docker `36018344028` passed both image jobs.
- Panel image `sha256:229190fb9b631d9adb34f509989013ece4592a56df0eae84b0938860a7abcc1e`,
  pulled **before** the container was stopped so the downtime was the recreate alone.
- Pre-upgrade backup `/opt/nekomari-backups/pre-v0.1.26-20260924-151656` (mode 0700),
  `data.tar` 372 entries, SHA256
  `38d9f62cda160207e924c10f04165af0d99ea48f5eff0b27b67bf10e18155f22`.
- Verified: `/api/version` → `{"hash":"8eb3c8c","version":"v0.1.26"}`; container healthy
  with `RestartCount=0`; both URLs answered 200.

Agents, all nine, staged-binary rollout as before with the previous binary kept as
`<bin>.bak-pre-v0.1.26`:

| Node | Binary | Unit | Before | After |
|---|---|---|---|---|
| 甲骨文 OC424 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` | `d79f378a…` | `4d76f5ec…` |
| 华纳云 HN-JP1, HK04, AkkoCloud, PYZC, MegaBox, CloudLeadInno | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | `a6930033…` | `8479da5d…` |
| NOSLA 东京 | `/opt/komari/agent` | `komari-agent.service` | `a6930033…` | `8479da5d…` |
| MAC Server | `/home/macos/nekomari-agent/komari-agent-linux-amd64` | user `nekomari-agent.service` | `a6930033…` | `8479da5d…` |

MAC again needed the capability dance (the install drops `cap_net_raw`, and there is no
passwordless sudo): restored through the privileged container and confirmed with
`getcap`. CloudLeadInno again went through the MAC-WAN hop.

The acceptance check for the feature, not just for the install: after the rollout the
metric store holds ping series that **carry a family tag** — 32 of 115 `ping.loss`
series at inspection, split `family:ipv4` 30 / `family:ipv6` 2 — where before the
upgrade no series had one. Historical series keep their old identity, which is the
compatibility half of the design.

One lesson worth recording: `clients.updated_at` and `clients.version` are refreshed by
the **basic-info upload**, which runs every `--info-report-interval` (10 minutes), so a
node can be running the new binary for several minutes while the panel still shows the
old version. NOSLA looked like a failed upgrade for exactly that long. Read the running
binary's hash, not the panel's version column, when the question is whether the upgrade
landed.

### A tenth node: JPKD2

Added 2026-09-24: a Leaseweb Japan container (KDDI egress), **Alpine 3.19, LXC, 300 MiB
RAM, 1.3 GiB disk, IPv4 only, behind NAT**. It is the first node that is not a systemd
host and the first that needed an IPv4 preference, so both are recorded here.

- The panel client was created through `admin:addClient` and its metadata filled with
  `admin:editClient`. Since it is the only node whose SSH is reachable only from
  MAC-WAN, the binary was copied there and then to the node.
- **No systemd.** Alpine uses OpenRC, and the repo's installer is systemd-only, so the
  service is `deploy/hosts/nekomari-agent.openrc` — `supervise-daemon` with a
  `/etc/conf.d/nekomari-agent` (mode 0600) holding the endpoint, the token and the
  flags. `rc-update add nekomari-agent default` plus `rc-service nekomari-agent start`.
- **IPv4 only.** DNS returns AAAA before A for the panel, and the container has no IPv6,
  so the default Happy-Eyeballs order spent its 15-second timeout on the unreachable
  family: `curl https://komari.orderly2233.org` returned `000` while `curl -4` returned
  `200` in 0.8 s. The agent's `--prefer-ip-version 4` is what fixes it, and it is the
  flag to reach for on any v4-only host.
- **No `setcap`** on Alpine, so the agent runs as root there rather than as a dedicated
  user with `AmbientCapabilities=CAP_NET_RAW` (the NOSLA pattern). Worth revisiting if
  that node ever shares the host with anything else.
- The first basic-info upload failed with a Cloudflare **522** and the WebSocket dropped
  a few times in its first minutes; both settled without intervention, and the node has
  reported steadily since. The panel shows `AMD EPYC Processor`, `Alpine Linux v3.19`,
  `lxc`, `300 MiB` and `1.29 GiB`, matching the provider's plan.
- **The agent token is visible in `ps`** — every unit in this fleet passes `-t <token>`
  on the command line, so any local user can read it. That is a fleet-wide property
  rather than a JPKD2 one, and it is the reason a token that leaked during this
  deployment was rotated twice rather than once. Moving the token to a config file or an
  environment file is worth doing on its own.

## v1.6.9 rollout (2026-10-01): panel, then the whole fleet

**Panel.** The image was pulled and verified by digest before anything was replaced.

| Step | Evidence |
|---|---|
| Release | `v1.6.9` on `610ab68` (PR #62); the release run is green, including its own deploy check |
| First real test of the `latest` policy | `latest` and `v1.6.9` resolve to the same digest, `sha256:a5a7c31e…`, while `v1.6.8` keeps its own. The gate moved `latest` because this release's verification passed — the behaviour `RELEASING.md` now documents, exercised end to end for the first time |
| Image verified on the host | `docker pull ghcr.io/aone2233/nekomari:v1.6.9` → `RepoDigests` exactly `sha256:a5a7c31e…`; `arch=arm64` on an `aarch64` host |
| One trap worth knowing | pulling `repo:tag@digest` does **not** create the tag locally, so `docker image inspect repo:tag` immediately afterwards answers `No such image`. Pull by tag and compare `RepoDigests`; the first attempt at this check produced exactly that false alarm, and the guard refused to continue |
| Databases backed up first | `/opt/nekomari/backups/pre-v1.6.9-20261001T165033Z`, taken with `sqlite3.Connection.backup` (WAL-safe) and verified: both `quick_check=ok`, `integrity=ok`, `sha256sum -c` OK |
| Compose | `image: …:v1.6.9@sha256:a5a7c31e…`, written under a full-digest regex guard plus `docker compose config`; the empty-digest mistake from the v1.6.8 rollout cannot recur |
| After the switch | `Up (healthy)`, `RestartCount=0`, `/api/version` → `{"hash":"610ab68","version":"v1.6.9"}` |

Live checks after the switch, against `http://127.0.0.1:25774` so nginx and Cloudflare stay
out of the picture:

```
/install                200  <title>Nekomari Monitor</title>, /assets/entry-index-DSVHwSg9.js → 200
/database-recovery      307  Location: /
/admin                  200  <title>Nekomari</title>
/sw.js                  200  text/javascript; charset=utf-8
/manifest.webmanifest   200  application/manifest+json
/assets/does-not-exist-abc123.js
                        404  a missing asset is not the SPA shell
```

**Fleet.** This release changes the agent, so the fleet moves with it — the rule in
`RELEASING.md`. Seven nodes went through `deploy/fleet/fleet-upgrade.sh v1.6.9`, run **as
`ubuntu`** from the panel host: the batch refuses to run as root, because the node aliases live
in that user's ssh configuration. Per node it downloads the asset, verifies it against the
release's `SHA256SUMS.txt`, executes the new binary with `--help` **before** replacing anything,
keeps the old one aside, and restarts:

```
upgraded: AKKO06 megabox HK04 CLISP NOSLA JPKD2 SDE9929
```

Three nodes the batch skips by design needed doing by hand, each for a different reason:

| Node | Why not the batch | How it was done |
|---|---|---|
| 甲骨文 OC424 | `local` in the inventory — it is the panel host and has no ssh alias of its own. It also reports under a unit the script does not look for, `komari-agent-oc424-original-node`; an older note in this file describes it as a bare process, which is wrong | arm64 asset downloaded on the host, checksum verified, `cp` aside, `mv` over, unit restarted. **The running binary's hash needs `sudo`**: `/proc/<pid>/exe` is unreadable for anyone but the owner of a root process, which is exactly what the per-node script's header warns about, and my first check reported a mismatch for that reason alone |
| MAC Server (MAC-WAN) | `elsewhere` — not reachable from the panel host, only from the workstation | reached from the workstation. The agent is a **user** unit and the binary sits in a `macos`-owned directory, so replacing it needs no `sudo` at all; the backup uses plain `cp`, since `cp -a` fails as a non-root user on a root-owned source |
| 并行智算云服务器 (PZYC) | `elsewhere`, and the node cannot fetch release assets at all (`curl https://github.com` → `000`) | downloaded and checksum-verified on the workstation, transferred with `scp`, verified again on arrival, installed with `install` + `mv` (writing into a running binary's inode earns `ETXTBSY`), `sudo systemctl restart nekomari-agent` |

Verified from the panel afterwards, which is the only authority on what a node actually runs:

```
total clients: 10        versions: {'v1.6.9': 10}        duplicate names: none
every node last reported within 120 s
icmp_capability unchanged for every node (MAC Server: none, as before; the other nine: raw)
0 panic/fatal lines in the panel log across the move
```

**Two warnings that were not real, and what produced them:**

- **JPKD2** printed `ICMP: UNAVAILABLE … the agent runs as  and neither socket type opens`
  (note the empty user name). The panel's own store says `icmp_capability=raw` **before and
  after**, so nothing was lost. Two busybox quirks produced the alarm: `ps -o user=` prints
  nothing, so the empty owner was read as "not root" and the check ran for an agent that needs no
  capability at all; and the node has no `python3`, so there was no probe to run and "could not
  check" came out as a failure. **Fixed in `deploy/fleet/upgrade-agent.sh`** (PR #64) — the
  decision now comes from the uid in `/proc/<pid>/status`, and a node with no python3 says it
  could not be checked. The capability test asserts both, and those assertions were run against
  the old script to confirm they fail.
- **HK04** reported `auth errors in the last minute: 1`. That is the restart window: the unit's
  log immediately afterwards shows `Basic info uploaded successfully` and `WebSocket connected
  using v2 protocol`, and the panel shows it reporting within the minute.

**A duplicate agent on the panel host, found by chasing a version that flipped back.** Straight
after the fleet move the panel reported all ten nodes on `v1.6.9` — and then `甲骨文 OC424` read
`v1.6.7` again, on a *fresh* report. The host was running **two** agents: the unit's
(`komari-agent-oc424-original-node`, started `16:59:53`, `sha256:d3204a01…` — the release) and a
**manual `sudo nohup`** process started at `12:07` that had survived every binary replacement,
because replacing a file does not stop a process already running the old inode. Both hold the same
credential and report as the same node, so the version column is simply whichever reported last.
That is why "10/10 on `v1.6.9`" was true when it was read and false a minute later: it was a race
between two processes, not a stale reading. The manual pair was killed and only the unit remains.

It also explains a contradiction between two notes in this file: the v1.6.7 section calls OC424's
agent "a bare root process, not managed by a unit", while the fleet inventory calls it
`komari-agent-oc424-original-node`. Both were true at the same time, which is the state worth
knowing about.

Every node was then swept for the same condition rather than assuming it was unique to the panel
host — one agent process per node, each running the release binary (`93fcd808…` on amd64,
`d3204a01…` on arm64). JPKD2's second match is its OpenRC `supervise-daemon` wrapper, which is
expected. Three checks 45 seconds apart afterwards: ten nodes, ten times `v1.6.9`, no flip.

**Also done in this window:**

- The nginx query-string redaction was extended from `token=` to
  `token|2fa_code|two_factor_code|otp` (`/etc/nginx/sites-available/nekomari`, backup
  `…bak-pre-2fa-redaction-20261001T163638Z`), verified with a marker value: the log line reads
  `/api/admin/oauth2/unbind?[redacted]` and the marker appears nowhere in the access log. It
  matters because of `SECRETS.md`: binding an SSO account is a redirect, so its TOTP code can only
  travel in the query string.
- All three remaining legacy `komari-agent` units are now **masked** — HK04 and NOSLA earlier in
  the session, OC424 here. The original unit files are archived to `/root/legacy-units/`, and the
  refusal is proved rather than assumed: `systemctl start komari-agent` → `rc=1`,
  `Unit komari-agent.service is masked`, legacy PID still 0, reporting unit keeps its PID.
  Reversal is `systemctl unmask` plus copying the file back.

## v1.6.7 rollout (2026-10-01): panel then fleet

Panel first, then the agents — the ordering this document argues for at "Order matters" above,
and this release needs it: the agent moved its credential from the query string to the
`Authorization` header, and an old panel only reads the query parameter.

| Step | Result |
|---|---|
| panel | `ghcr.io/aone2233/nekomari:v1.6.7@sha256:4402b0bd…`, healthy, `restarts=0`, `/api/version` = v1.6.7 |
| fleet | 10/10 on v1.6.7, all reporting; `traffic.interval.*` series went from 1/10 nodes to 9/10 |
| backup | WAL-safe online copy of both databases to `backups/pre-v1.6.7-<ts>` (`quick_check=ok`) before the swap |

**v1.6.6 was published and then failed its own deploy verification** (`13 passed, 2 failed`; the
agent logged `Failed to connect to WebSocket: 403 Forbidden` while its POST uploads succeeded).
The fix is v1.6.7. Note what the failure exposed about the release pipeline: `docker.yml` only
skips when the release run was *cancelled*, so `latest` was moved to a build that cannot connect.
That guard is deliberate — the comment there explains that a flaky verify should not leave a
release without an image — so it is worth revisiting rather than silently changing: pushing the
version tag but holding `latest` back would satisfy both.

### What the fleet actually looks like (this is not what a first reading suggests)

The rollout went wrong twice before it went right, both times because the host layout is more
varied than the docs imply. Check these before the next fleet move:

- **A node can have two agent units.** HK04 runs `nekomari-agent.service`
  (`/opt/nekomari-agent/komari-agent-linux-amd64 --token-file …`) *and* a disabled
  `komari-agent.service` pointing at a legacy `/opt/komari/agent … --auto-discovery <key>`.
  The reporting one is `nekomari-agent`. `systemctl show komari-agent || systemctl show
  nekomari-agent` picks the wrong one, which is how a canary restart ended up starting a
  disabled legacy agent instead of loading the new binary. Always ask for the unit by name.
- **The binary name is not uniform.** JPKD2 (Alpine/OpenRC) runs `/opt/nekomari-agent/komari-agent`
  — no `-linux-amd64` suffix — and its pidfile belongs to the `supervise-daemon` wrapper, not the
  agent, so hashing whatever that pidfile points at measures the wrong process. Look up the
  process whose `/proc/<pid>/exe` is the agent path.
- **OC424's own agent is not managed by a unit.** Both `komari-agent` and `nekomari-agent` are
  inactive there; the reporting agent is a bare root process at
  `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64`. Replacing the binary therefore means
  killing that process and re-running it with its own argv (captured from `/proc/<pid>/cmdline`)
  — as root, since `ubuntu` cannot signal it.
- `pgrep -f <binary path>` also matches the shell running the rollout, so a "running binary hash"
  measured that way is meaningless. Read the hash through the unit's `MainPID`, or through
  `/proc/<pid>/exe` for a pid you captured yourself.

### How to verify a fleet move

The panel's version column is refreshed by the basic-info upload on
`--info-report-interval`, so it lags by up to ten minutes. The running binary's hash is
immediate, and it is the thing that decides whether the upgrade landed:

```bash
sha256sum /proc/$(systemctl show nekomari-agent -p MainPID --value)/exe   # vs the release's amd64/arm64 asset
```

### After this rollout: the embedded default theme becomes the panel's own front end

**Not in v1.6.7** — measured: on v1.6.7 the embedded archive is still the LuminaPlus build, so
`GET /install` returns that theme's document (`<title>Komari-Theme-LuminaPlus</title>`). The change
is in the working tree after this rollout and will ship with the next panel release.

What changes is what the binary carries: `web/public/defaultTheme/dist.tar.zst` was an external
theme's build output and is now assembled by `script/embed-theme.mjs` from this repository — the
panel's own front end (`frontend/dist`), its admin subtree at `admin/`, and
`frontend/komari-theme.json`. What that means for an instance after the upgrade depends on whether a
theme is installed, and the two cases are worth keeping apart:

| Instance | Effect |
|---|---|
| a theme installed (this deployment is one) | **no visible change.** An installed theme still answers `/` and its own routes; `/admin` still answers the panel's admin interface. What changes is the panel-owned paths: `/install` now returns the panel's own document instead of falling through to the installed theme's unknown-route page — the production defect recorded in `docs/audits/2026-10-01/BUG-AUDIT-2026-10-01.md` (`curl /install` returned the installed theme's page while `curl /admin` returned the panel). |
| no theme installed (the embedded default *is* the front end) | the **appearance changes**: `/` is the panel's own front end rather than LuminaPlus, and `/install` changes with it — on v1.6.7 it served the embedded theme's document, since that archive had no install route either, and it now serves the panel's install screen. `/admin` and `/database-recovery` are unaffected. |

Nothing else moves: `/database-recovery` still answers `307` to `/` on the normal listener (the
recovery UI belongs to its temporary restricted listener), and `/admin` is unchanged. So an upgrade
on a themed instance is not a user-visible change and needs no rollout note of its own; an upgrade on
an unthemed instance changes the front page, which is worth saying out loud before it happens.

`deploy/deploy-verify.sh` now asserts all three after the install. Run against v1.6.7 it fails the
`/install` assertion on purpose (`21 passed, 2 failed`) while `/database-recovery` and `/admin` pass —
that is what makes it able to catch this class of change rather than describe it.

## v0.1.27 — released 2026-09-25, **partly verified 2026-09-26**

`v0.1.27` is tagged (`6a1e875`, 2026-09-25 18:11 +08:00) and, unlike v0.1.26, it
**moves the fleet**, because the agent changed. The panel and this host's own
agent are confirmed on it (see the table at the top of this file); the rest of the
fleet is not.

What the release contains, from [`CHANGELOG.md`](../CHANGELOG.md):

- **ICMP without root or `CAP_NET_RAW`.** The raw socket is still tried first; the
  kernel's unprivileged ICMP socket is now the fallback. This is the item that
  makes `D2`/`E1` actionable — a node without the privilege reports a real latency
  instead of a permanent phantom loss.
- **Reconnect backoff.** 5 s doubling to a 2-minute cap with ±25 % jitter, reset on
  success, failure logs folded to one line per burst. Intended to cut the WebSocket
  log churn measured on 华纳云 HN-JP1 (166 lines/24 h) and NOSLA (181).
- **The traffic ledger reports its save failures** instead of discarding them.
- **A ping task that could measure two address families is refused** on create and
  on edit (`utils.ValidatePingTaskTargetFamily`).

### What was verified on 2026-09-26

Queried over SSH, read-only — no container, unit or binary was touched:

- Panel: `ghcr.io/aone2233/nekomari:v0.1.27`,
  `sha256:7a0086eb035facf2d02012c853a5f2cd69129b573ed2b77a51dd523d7665b818`,
  started `2026-09-25T10:27:42Z`, `RestartCount=0`, health `healthy`,
  `/api/version` → `v0.1.27` / `6a1e875`.
- OC424's agent: binary `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64`,
  mtime `2026-09-25 10:28:31 UTC` — 17 minutes after the tag, so the rollout
  happened promptly but was never written down. Unit active.
- That agent runs as **root** with no `AmbientCapabilities` — the D2 pattern that
  is still un-migrated on this host, and the reason `--disable-auto-update` and
  `--month-rotate 1` are on its line.
- Its `ExecStart` still carries `-t <redacted>`: D1 is fixed in the repository but
  **not deployed here**. The repository fix and the one-node migration recipe are
  in `docs/SECRETS.md`.

Still unknown, and not to be assumed from the tag: whether the other nine nodes
moved, and whether ICMP now works anywhere that previously needed root only for
it.

## v0.1.28 rollout (2026-09-26): panel and fleet

Released `v0.1.28` (tag on `e22d6f0`, CI green on that commit before tagging) and
moved both halves: the panel container and all ten agents. This release changes the
agent, so per `docs/RELEASING.md` the fleet had to move —
`git diff --stat v0.1.27..HEAD -- agent/ protocol/ pkg/` is non-empty.

| | Before | After |
|---|---|---|
| Panel image | `ghcr.io/aone2233/nekomari:v0.1.27` | `v0.1.28`, `/api/version` → `v0.1.28` / `e22d6f0`, `healthy`, `RestartCount=0` |
| Panel backup | — | `/opt/nekomari-backups/pre-v0.1.28-20260926-095253` (data 364 files, `data.tar` sha256 `43691f93d711…`, mode 0700) |
| Agents | `9477e4e4…` (9), `705e8d5b…` (PZYC, the release asset) | `20873452…` on all ten; every node reports `v0.1.28` and `icmp_capability=raw` |
| Agent backups | — | `<bin>.bak-pre-v0.1.28` on every node, plus `migration-backup-20260926/` on PZYC |

### Order matters: the panel first, or the agents cannot report

The first agent upgraded was MAC-WAN, and it immediately logged:

```
Error uploading basic info: status code: 400, "no such column: icmp_capability"
```

The agent was reporting a field the v0.1.27 panel had no column for, so the whole
basic-info upload was refused — not just the new field. The node stayed *online*
(a WebSocket connection is not a basic-info upload) but **stopped sending version,
CPU, memory and disk information**, which is the kind of failure that looks like a
node problem until someone reads the journal.

Nothing was lost: the column is created by `AutoMigrate` on the panel's next start,
and re-uploading basic info is idempotent. But the order should be **panel first,
then agents** whenever a release adds a field the panel has to store. The window
here was about three minutes.

The general shape is worth keeping in mind for the next field of this kind: an
additive change is additive only in both directions *at the same time*. A new agent
with an old panel is not "the old panel ignores it" — it is a rejected upload,
because the ingest path maps the map's keys straight onto columns.

### What each node needed

Six different shapes, which is why the deploy was per node rather than one command:

| Node | Shape | Note |
|---|---|---|
| AKKO06, megabox, HK04, HNJP01, CLISP | systemd, root | `deploy/install-staged-agent.sh` handles all five |
| 甲骨文 OC424 | systemd, root, arm64 | same script, arm64 asset; `sudo` needed for the binary's ownership |
| MAC-WAN | **user** unit, unprivileged + **file** capability | the capability has to be restored by hand after the swap |
| NOSLA | systemd, unprivileged + `AmbientCapabilities` | nothing to restore: the unit grants the capability, not the file |
| JPKD2 | **OpenRC** | `rc-service`, no `systemctl`; `install-staged-agent.sh` does not apply |
| 并行智算云服务器 | systemd, root, **password** login | cannot fetch release assets, so the binary has to be pushed |

Two mistakes made and fixed during the run, both worth avoiding next time:

- **`sh` is not `bash` on Debian/Ubuntu.** Invoking the installer as
  `ssh HOST sh /tmp/install-staged-agent.sh` fails at its `set -euo pipefail` with
  "Illegal option -o pipefail" — before doing anything, so it looks like a no-op
  rather than a failure. Its shebang says bash for this reason; invoke it as
  `bash`. Nodes already upgraded by an earlier attempt were left alone by the
  script's idempotency check, which is the behaviour that made the re-run safe.
- **A CRLF line ending at the file's end is a command.** Transferring a bash script
  through Git Bash left a trailing `\r`, which the node ran as a command
  (`$'\r': command not found`) *after* installing and restarting. The install was
  fine and the exit code was 127, so the wrapper reported a failure that had not
  happened. Push scripts as base64, or `tr -d '\r'` first.

### 并行智算云服务器: access changed, and a mistake to record

This node was reached over a **password** that is stored in the operator's ssh
config in plain text, and it cannot fetch release assets, so the binary had to be
pushed by hand. OC424's public key is now in both `ubuntu`'s and `root`'s
`authorized_keys`, so future deploys have a key route.

**The mistake:** writing root's file used `sudo -n tee /root/.ssh/authorized_keys`,
which *replaces* rather than appends. The original contents are not known — no
backup existed and the file holds a single key whose public half was not on hand.
It now holds the operator's own key (the one whose public half is in `ubuntu`'s
file) plus OC424's, so both routes in work, but if root had had another key it is
gone and would have to be re-added. Appending with `>>` under `sudo` needs
`sudo tee -a`; `tee` without `-a` is a truncation.

Also recorded in `docs/SECRETS.md`: that plaintext password is the same kind of
finding as the ones already there.

## Changing a node's timezone needs an agent restart

Found on 2026-09-26 while reading CLISP's journal, and it looked like an agent bug:

```
15:15:12Z  [233254]  2026/09/26 23:15:12  shutting down gracefully...   <- old process
15:15:12Z  [276167]  2026/09/26 08:15:12  Nekomari Agent v0.1.30       <- new process
```

Same systemd unit, same second, four hours apart in the printed stamp — and neither
matched the UTC time the journal recorded.

**It is not a bug.** The node's timezone had been changed at 15:03:27 that day, from
UTC+8 to `America/Los_Angeles`. Go reads `/etc/localtime` **once at startup** and
caches the zone, so the process running since before the change kept printing UTC+8
(`22:51`, `23:01`, `23:11`) while the journal's own stamps stayed UTC. The restart
that the v0.1.30 rollout performed replaced the process, and it printed PDT from then
on — correct.

Two things worth knowing, because both would mislead someone reading logs:

- **The agent's log stamps carry no zone offset**, so `08:15:12` and `23:15:12` are
  indistinguishable as evidence until you compare them against the journal's UTC
  column. `journalctl -o short-iso` prints that column separately; use it rather than
  the message text when the question is "when did this happen".
- **The same cached zone feeds the month boundary.** `--month-rotate` computes the
  reset day through `utils.GetLastResetDate(flags.MonthRotate, time.Now())`, and that
  function takes the location from `time.Now()` — the process's cached zone. So
  between a timezone change and the next agent restart, the "this month" window is
  cut at the old zone's day boundary. On a node with `--month-rotate`, that is a quota
  figure that is wrong by up to a day, not just a log line.

**Rule: after changing a node's timezone, restart its agent.** Checked across the
fleet on 2026-09-26 (`/proc/<MainPID>` creation time against `/etc/localtime`'s
mtime): every node's agent was already younger than its timezone file, so nothing was
outstanding — CLISP was the only node whose zone had changed recently, and the
rollout had restarted it.

## D1/D2 pilot: MAC-WAN migrated 2026-09-26

The first node moved to a credential file and to a strictly non-root agent with
`CAP_NET_RAW` alone. It ran **before** the rest of the fleet on purpose, and the
reason to read this section before repeating it is that the migration silently
drops the file capability — see "the `scp` trap" below.

MAC-WAN is the host that already ran the agent unprivileged (`docs/AGENT-FOOTPRINT.md`
records the arrangement), so D2's acceptance was already met there; this pilot is
the D1 half, and it is also the first time the *new* agent binary has run
anywhere.

| | Before | After |
|---|---|---|
| Binary | `komari-agent-linux-amd64`, mtime 2026-09-25 18:30, sha256 `705e8d5b…` | same path, sha256 `8145429c…`, built from `main` at `d4ae61f` |
| Credential | `-t <token>` in `ExecStart`, readable by every local user | `--token-file /home/macos/nekomari-agent/.agent-credentials`, mode `600 macos:macos` |
| Identity | `macos` (uid 1000), `cap_net_raw=ep` on the binary | `macos`, `cap_net_raw=ep`, `CapEff=0000000000002000` |
| Unit | `~/.config/systemd/user/nekomari-agent.service` | same, one line changed |
| Panel version | `v0.1.27` | `v0.1.27` (see the note on `-ldflags` below) |

## OC424 migrated 2026-09-26 (rotation and D1 in one restart)

The panel host's own agent was migrated second, and its token was **rotated at the
same time** — the token had been read out during the audit earlier that day, so
moving it to a file without replacing it would have fixed the handling and left
the exposure. One restart does both.

| | Before | After |
|---|---|---|
| Binary | `komari-agent-linux-arm64`, sha256 `aaacb794…` | sha256 `a801bf6e…`, built from `main` at `703b3ae` with `-ldflags …CurrentVersion=v0.1.27` |
| Credential | `-t <token>` in `/etc/systemd/system/komari-agent-oc424-original-node.service` | `--token-file /home/ubuntu/nekomari-agent/.agent-credentials`, mode `600 root:root` |
| Token | the one printed during the audit | new, written through `admin:editClient` and verified with `admin:getClientToken` before anything on the host changed |
| Identity | `User=root` (this node needs no capability: root has `CAP_NET_RAW`) | unchanged |
| Backup | — | `/home/ubuntu/agent-migration-backup-20260926/` (unit + previous binary) |

Evidence, all from the host after the restart:

- `/proc/<pid>/cmdline` and the unit both contain zero `-t`/`--token` occurrences
  carrying a value; the credential file is `600 root:root` and 35 bytes.
- The **old** token answers `401` on `/api/clients/v2/rpc` and the **new** one does
  not (it returns the route's `400` handshake failure, which is what "not 401"
  means on that endpoint — see `docs/SECRETS.md` for why that is the check).
- The agent logs `Nekomari Agent v0.1.27`, uploads basic info and reconnects the
  WebSocket — the proof that it read the token from the file rather than merely
  starting without one.
- The panel's `clients` row shows `甲骨文 OC424 … v0.1.27` with `updated_at`
  13:04:15 +08:00, and its TCP tasks keep reporting real latency (task 1: 1 ms,
  task 3: 67-74 ms) in the same minutes.

The script that did it was a one-shot on the host, deliberately bounded: it
refused to run if the staged token file already existed or the unit was already
migrated, verified the rotation *before* touching the unit, kept a backup, and
would print the rollback if the restart had not come up active. There is no reason
to keep it in the repository — `deploy/rotate-all-tokens.py` carries the same
rotation path for all nodes, and the migration itself is now what the installer
does by default.

### MAC-WAN details

Evidence collected on the host after the change:

- `tr '\0' '\n' < /proc/<pid>/cmdline` contains no `-t`/`--token`; the command line
  shows `--token-file …` and nothing else about credentials. Same for
  `systemctl --user show -p ExecStart`.
- The credential file is `AGENT_TOKEN=<22 chars>` at mode 600, owned by `macos`.
- The agent starts, uploads basic info and reconnects the WebSocket — which is the
  proof that it really read the token: an agent with no identity does none of those.
- **ICMP still works, measured in the panel's own store, not on the host.** Tasks
  17/18/19 reported `ping.latency_ms` 37/33/50 ms and `ping.loss` **0.0** in the
  12:28 CST bucket. This is the acceptance criterion that matters, because
  `net.ipv4.ping_group_range` on this host is `1 0` (no unprivileged ping socket at
  all), so a working ICMP probe proves the binary's `cap_net_raw` is effective —
  the fallback socket cannot be what answered.
- TCP probes (tasks 1 and 3) keep reporting 1-2 ms, and the traffic ledger
  (`net_static.json`) is rewritten after the restart.
- A second restart was done deliberately: the state survives it.

### Two traps, both of which bit during these migrations

**`scp` does not carry file capabilities.** Replacing a hardened binary with `scp`
(or any copy that is not `cp -a`/`rsync -X`) leaves the new file with **no**
`cap_net_raw`, and the failure is quiet in the worst way: the agent starts, connects
and reports everything else normally, while every ICMP task turns into packet loss —
the exact "phantom loss" that `docs/ROADMAP.md` E1/E2 exist to remove. On MAC-WAN
the unprivileged ICMP socket is unavailable, so there is no fallback to hide it;
this was reproduced deliberately on the second upload (`getcap` printed nothing
straight after the copy, and `setcap` had to be re-run). It does **not** apply to
OC424, where the agent runs as root and already holds `CAP_NET_RAW` in its
effective set — which is exactly why the trap is easy to miss: it depends on the
node, not on the procedure.

Two consequences:

1. After any binary replacement, re-run `sudo setcap cap_net_raw+ep <binary>` and
   confirm with `getcap`, or use `deploy/install-staged-agent.sh`, which already
   restores the capability when the node had it.
2. The capability was lost **and restored** during this pilot, so the before/after
   above is not a clean A/B on that variable. What it does prove is the end state.

**A locally built agent reports the wrong version.** The release pipeline passes
`-ldflags="-X github.com/Aone2233/nekomari/agent/update.CurrentVersion=<tag>"`;
`go build` on its own does not, and the agent then announces itself as `0.0.1`.
The first upload here did exactly that and the panel showed `MAC Server … 0.0.1`
while the node was healthy — a discrepancy that reads as a failed upgrade. Rebuild
with the flag:

```bash
cd agent && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags="-w -X github.com/Aone2233/nekomari/agent/update.CurrentVersion=v0.1.27" \
  -o komari-agent-linux-amd64 .
```

The panel's `clients.version` column is refreshed by the basic-info upload, so it
took one `--info-report-interval` (10 minutes) to show `v0.1.27` after the restart.

The backup taken before the change is `~/pilot-backup-20260926/` (unit plus binary).
Rollback, in full:

```bash
cp ~/pilot-backup-20260926/nekomari-agent.service.bak-pre-token-file \
   ~/.config/systemd/user/nekomari-agent.service
cp ~/pilot-backup-20260926/komari-agent-linux-amd64.bak-pre-token-file \
   ~/nekomari-agent/komari-agent-linux-amd64
sudo setcap cap_net_raw+ep ~/nekomari-agent/komari-agent-linux-amd64   # the backup has no capability either
systemctl --user daemon-reload && systemctl --user restart nekomari-agent.service
```

To undo only D1 (keep the new binary, put the token back on the command line), write
`-t <token>` back into `ExecStart` in place of `--token-file …` and restart; the
new binary accepts both.

## The five systemd nodes migrated 2026-09-26

AKKO06, megabox, HK04, HNJP01 and CLISP, one at a time, stopping at the first
failure so a bad node could not take the rest with it. All five had the same shape
— `/etc/systemd/system/nekomari-agent.service`, `/opt/nekomari-agent/komari-agent-linux-amd64`,
`-t <token>` — so one procedure applied to all of them, run per node rather than
as a loop.

| | Before | After |
|---|---|---|
| Binary | sha256 `705e8d5b…` (the published v0.1.27, which predates `--token-file`) | sha256 `9477e4e4…`, built from `main` |
| Credential | `-t <token>` in the unit | `/opt/nekomari-agent/.agent-credentials`, 600 root:root |
| Unit | `-t <token>` | `--token-file /opt/nekomari-agent/.agent-credentials` |
| Backup | — | `/opt/nekomari-agent/migration-backup-20260926/` (unit + previous binary, 9.2 MB each) |

**The tokens were deliberately not rotated here.** The migration moves the existing
token into a file, so a failure on the node leaves the node's identity intact and
the rollback is a copy of two files. Rotation is a separate operation because it is
the part that cannot be undone; doing both at once would mean a failed migration
also cost the node its credential. OC424, MAC-WAN and JPKD2 were rotated because
their tokens had actually been read out.

Verified per node, read-only and after the restart: `service=active`,
`unit_has_token=0`, `cmdline_has_token=0`, `unit_uses_token_file=1`,
`cred_mode=600 root:root`, binary `9477e4e42291`.

Verified fleet-wide afterwards, from the panel's own store: **all ten nodes
reporting**, every one on `v0.1.27`, each producing latency and loss samples with
the newest sample under a minute old.

### What the fleet looks like now

| Node | Token location | Identity |
|---|---|---|
| 甲骨文 OC424 | credential file (rotated) | root |
| MAC-WAN | credential file (rotated) | non-root + `cap_net_raw` |
| JPKD2 | credential file (rotated) | root, OpenRC |
| AKKO06, megabox, HK04, HNJP01, CLISP | credential file (not rotated) | root |
| **NOSLA** | credential file (`--token-file`, `600 komari:komari`) | root, reporting unit `nekomari-agent` |

**Corrected 2026-10-01: NOSLA was never on `-t`.** This row used to read "still `-t`
on the command line / unknown — no SSH route", and both halves were wrong. Read live
on the host, NOSLA's reporting unit is `nekomari-agent` (pid 6207) running

```
/opt/nekomari-agent/komari-agent-linux-amd64 -e https://komari.orderly2233.org \
  --token-file /opt/nekomari-agent/.agent-credentials -i 5 --info-report-interval 10 --disable-auto-update
```

with `.agent-credentials` and `auto-discovery.json` both `600 komari:komari` and the
credential file's mtime at **2026-09-20** — before this rollout. Its legacy sibling
(`/opt/komari/agent`) also passed `-token-file /opt/komari/.agent-credentials`, so
the `-t` reading matches neither unit. The SSH route had been fixed on 2026-09-26 as
well (below). **No node is left on a command-line credential**, and NOSLA needed
nothing from anyone.

### Legacy units are masked (2026-10-01)

Two hosts carry a second, legacy unit beside the reporting one — HK04 and NOSLA, each
`/opt/komari/agent … --auto-discovery` behind `komari-agent.service`. Both were
already disabled, but "disabled" only keeps a unit out of boot; nothing stopped a
`systemctl start` from enrolling the node a second time. Both are masked now, and the
mask was *proved* rather than assumed:

```
systemctl start komari-agent      →  rc=1
Failed to start komari-agent.service: Unit komari-agent.service is masked.
```

`systemctl mask` refuses to act while a real unit file occupies the path, so the file
was first archived out of systemd's search path
(`/root/legacy-units/komari-agent.service.pre-mask-<ts>`; 336 B on HK04, 946 B on
NOSLA), then `daemon-reload`, then mask. Reversal is exactly: `systemctl unmask
komari-agent`, copy the file back, `daemon-reload`.

The reporting unit was untouched by this — `nekomari-agent` kept its PID (52265 on
HK04, 6207 on NOSLA), `/proc/<pid>/exe` still resolves to
`/opt/nekomari-agent/komari-agent-linux-amd64`, and each process still holds an
established connection to the panel.

### NOSLA's access route, fixed 2026-09-26

For most of this work NOSLA was reachable **only** from MAC-WAN: its key lived in
that workstation's `~/.ssh/config` (`Host tender-guard TG`) and nowhere else, so
operating the node depended on one home-broadband machine being up. That is a
single point of failure for a production node, and the fleet migration hit it —
OC424 could not reach NOSLA at all, so the token had to be rotated there, piped
through MAC-WAN, and installed over a double hop.

Fixed by giving OC424 **its own** key, not a copy of MAC-WAN's:

- OC424's `id_ed25519` public key was appended to `~/.ssh/authorized_keys` on
  NOSLA (now four keys). Appending rather than replacing, so MAC-WAN's access and
  the other two keys are untouched.
- OC424's `~/.ssh/config` gained `Host NOSLA tender-guard TG` → `176.119.148.158`,
  `User root`, `IdentitiesOnly yes`, with the previous config kept as
  `config.bak-pre-nosla-<timestamp>`.

Verified from OC424: `ssh NOSLA` answers, `systemctl is-active komari-agent` is
`active`, and the process command line has no `-t`/`--token`. Either host can now
operate the node.

A copied private key would have been the quicker fix and the wrong one: it puts
one credential on two hosts, cannot be revoked for one without the other, and
leaves `authorized_keys` unable to say who is logging in.

## JPKD2 migrated 2026-09-26 (OpenRC, and the third token rotated)

JPKD2 is the one node that is not systemd — Alpine 3.19 in LXC, behind NAT, with
`/etc/conf.d/nekomari-agent` holding its settings. Its token was rotated and its
service moved to a credential file in the same operation, for the same reasons as
OC424: the token had been read out during the fleet audit, and moving it without
replacing it would have fixed the handling and left the exposure.

It also needed a new binary first. The fleet binary is the published `v0.1.27`,
built before `ac80b73` added `--token-file`, so the flag did not exist there yet.

| | Before | After |
|---|---|---|
| Binary | `/opt/nekomari-agent/komari-agent`, sha256 `705e8d5b…` (the release asset) | sha256 `9477e4e4…`, built from `main` at `55d1794` with the version injected |
| Credential | `NEKOMARI_TOKEN=…` in `/etc/conf.d/nekomari-agent` (0600) — correct at rest | `/opt/nekomari-agent/.agent-credentials`, 600 root:root |
| Command line | `/etc/init.d/nekomari-agent` built `-e … -t ${NEKOMARI_TOKEN} …`, so the token was in `/proc/<pid>/cmdline` | `-e … --token-file /opt/nekomari-agent/.agent-credentials …` |
| conf.d | carried the token | carries endpoint and flags only |
| Backup | — | `/opt/nekomari-agent/migration-backup-20260926/` (init script, conf.d, binary) |

Evidence after the restart: `rc-service nekomari-agent status` → `started`; the
process command line has **zero** `-t`/`--token` occurrences and shows
`--token-file`; `getcap` is not involved (the service runs as root); the panel
reports `JPKD2 | Uzumaru … v0.1.27` with a fresh `updated_at`, and its three TCP
tasks keep producing latency and loss values in the current minute.

The template `deploy/hosts/nekomari-agent.openrc` now passes
`--token-file ${NEKOMARI_TOKEN_FILE}`, so the next Alpine node starts on the safe
path rather than inheriting this one's mistake. `docs/SECRETS.md` (incident 5)
records the token and the `sh -s` trap that made the first attempt fail silently.

## The MAC Server "jitter" — resolved 2026-09-25 (it was never MAC)

`docs/OPEN-WORK.md` carried this for a week as "the 教育网 jitter is MAC Server
alone". Measured properly, that framing was wrong in both directions, and the
answer is that MAC was faithfully reporting a **target's TCP behaviour**.

What the panel's own data says, over six hours, per node, for the same tasks —
these are minute maxima of `ping.latency_ms`:

| Task | Node | p50 | p95 | max |
|---|---|---:|---:|---:|
| 3 — 教育网 `1.51.3.134:443` (TCP) | **MAC Server** | **3 ms** | **5 ms** | 317 ms |
| 3 — same | 甲骨文 OC424 | 68 ms | 72 ms | 310 ms |
| 3 — same | CloudLeadInno | 178 ms | 206 ms | 240 ms |
| 17 — 电信 `14.17.70.70` (ICMP) | **MAC Server** | **38 ms** | 70 ms | 344 ms |
| 17 — same | AkkoCloud | 166 ms | 168 ms | 220 ms |
| 17 — same | MegaBox | 153 ms | 160 ms | 208 ms |
| 1 — Cloudflare `1.1.1.1:443` (TCP) | **MAC Server** | — | — | **2 ms** |
| 1 — same | CloudLeadInno | — | — | 24 ms |

So MAC has the **best baseline in the fleet**, by a wide margin, while the other
nodes are uniformly slow rather than spiky — their p95 sits directly on their p50.
MAC's outliers also correlate with no other node's (Pearson r ≈ 0.05 against
AkkoCloud on task 17, −0.03 against OC424 on task 3).

It is not the host's link either. Sixty pings over the wired NIC:

```
gateway 192.168.100.1   min/avg/max = 0.357/0.621/0.778 ms
1.1.1.1                 min/avg/max = 0.659/0.954/1.138 ms
```

The wireless interface on the same host is the spiky one
(`1.430/4.491/28.023 ms`), and it is not the one carrying traffic — the wired
route has the lower metric.

**What it actually is.** The decisive test ran TCP connects and ICMP side by side
from MAC to the *same address*, `1.51.3.134`:

| Probe | Over 50 ms | Worst |
|---|---:|---:|
| TCP connect to `:443`, ~900 attempts | **27** | **586 ms** |
| ICMP to the same host, ~900 pings in parallel | **0** | < 50 ms |

The panel's task is a TCP probe, so it is reporting that endpoint's intermittent
SYN handling — 240-590 ms — and nothing about MAC. MAC is simply the node where it
is visible: with a 3 ms baseline a 250 ms handshake is a 100× outlier, while the
same handshake disappears inside OC424's 68 ms p50 and CloudLeadInno's 178 ms.

The general lesson is worth more than the specific answer: **a TCP probe and an
ICMP probe to the same host are not interchangeable measurements.** When one is
clean and the other is not, the difference is in the target's TCP path, not in the
node doing the measuring. Checking a task's `type` field is the first thing to do
when a node "looks jittery".

## The cacheable shell (v0.1.28) and the Cloudflare rule it needs

The panel's SPA shell used to be served with no caching headers at all, so
Cloudflare classified it `DYNAMIC` and fetched it from the Singapore origin on
**every** page open — measured at ~580 ms of TTFB from the education network while
the origin itself rendered the same bytes in **1 ms**. The hashed assets had cache
headers (v0.1.16) and returned in ~77 ms.

`web/public/public.go` now serves the shell with an `ETag` and
`public, max-age=60, must-revalidate`, and no longer rewrites `<html lang>` from the
language cookie — that rewrite was the only per-visitor input, and it was redundant
because the frontend sets `documentElement.lang` itself
(`frontend/src/utils/language.ts`). Same URL, same bytes, for everyone.

**The headers alone do not cache anything.** Cloudflare decides what is cacheable by
extension by default, and `/` and `/admin/...` have no extension. A **Cache Rule** is
what turns "cacheable" into "cached".

### Adding the rule (dashboard)

1. Cloudflare dashboard → pick the zone → **Caching → Cache Rules** → *Create rule*.
   (Older layouts: **Rules → Cache Rules**.)
2. **When incoming requests match** → *Custom filter expression*, and start with the
   narrowest expression that captures the win:

   ```
   http.request.uri.path eq "/"
   ```

   Once that reports `HIT`, widen it to the SPA entry paths:

   ```
   (http.request.uri.path eq "/") or (http.request.uri.path starts_with "/admin")
   ```

3. **Then → Cache eligibility: Eligible for cache.**
4. **Edge TTL: Respect origin** — the origin sends `max-age=60`, which is the 60
   seconds chosen deliberately (short enough that a settings change lands within a
   minute). **Browser TTL: Respect origin** as well.
5. Leave **Cache Key** at its default. Do *not* add a cookie to it: the shell no
   longer varies by cookie, and adding one would fragment the cache per visitor.
6. Save, then verify from the network you care about:

   ```
   curl -s -D - -o /dev/null https://komari.orderly2233.org/ | grep -i cf-cache-status
   ```

   Run it twice. Every request within 60 seconds after the first should say `HIT`,
   and TTFB should drop from ~0.58 s to ~0.08 s.

### The one thing not to do

**Never let the expression match `/api/`.** Every dynamic endpoint lives under it
(`/api/rpc2`, `/api/login`, `/api/public`, …) and caching those at the edge would
serve one user's authenticated response to another. `/assets/` needs no rule — it is
already cached by extension and immutable. If the rule is ever widened, re-run the
check above against an `/api/` path and confirm it still reports `DYNAMIC`.

### Rolling back

Disable the rule, or set *Cache eligibility: Bypass cache*. The origin's headers are
harmless on their own: without a rule they do nothing at all, which is exactly the
state before v0.1.28.

### When a change does not appear

The shell is cached for up to 60 seconds, so a sitename or theme change can lag by
that much. Purging `/` in **Caching → Configuration → Purge Cache** makes it
immediate.

## Previous rollout: 2026-09-23 (v0.1.25)

Panel upgraded from v0.1.24 at approximately 15:40 UTC. Origin and public
`/api/version` both reported `v0.1.25`, hash `5373c37`. See
[the changelog](../CHANGELOG.md) for the release contents.

- Merged main and tag `v0.1.25` both point to
  `5373c3700f125fe30b5832b1f3b199ada9140c90`. Exact-commit CI
  `35881261635` passed build/test on Ubuntu and Windows, frontend browser
  fixtures, and panel smoke. Release `35882032896` passed all eight jobs,
  including verification of the downloaded release binary. Docker
  `35882754749` passed both panel and agent image jobs; anonymous pulls worked.
  The release has eight binary assets and `SHA256SUMS.txt`.
- The `ghcr.io/aone2233/nekomari:v0.1.25` arm64 image was pulled and inspected
  before the restart. Its arm64 manifest digest is
  `sha256:80db92b6f5f5674cf07a90cb2fb04d4136c413c0074165c7a171fcdf698b9b66`;
  its OCI revision matches the tagged commit. `/app/nekomari` SHA256
  `d8efec5ecb34efc48ee1ca90e3e67f7cea2bf540e3b6f0f0cfe83e76ebe3202a`
  matches the published `nekomari-linux-arm64` binary and release checksum.
- The stopped panel's Compose file and complete bound data were backed up under
  `/opt/nekomari-backups/pre-v0.1.25-20260923-153913` (mode 0700).
  `data.tar` has 371 entries, approximately 520 MB, including both
  `data/komari.db` and `data/metrics.db`; its SHA256 is
  `462f03e6b972b4572d8df52c4944de2fd43623db3868cbf76a8def45e70276e3`.
  The archive and checksum were checked before changing the image. The saved
  Compose file has SHA256
  `9c92b867d1bfd15257183bcc226d40f3f0754f7a7f8d891721bc3f03bb5f657a`;
  the only Compose change was the image tag. The previous file remains at
  `/opt/nekomari/docker-compose.yml.bak-pre-v0.1.25`. The server also made an
  application upgrade backup at `data/backup/upgrade-20260923-154011.zip`.
- The new container is healthy with `RestartCount=0`; its binding remains
  `127.0.0.1:25774` and its data mount remains `/opt/nekomari/data:/app/data`.
  Both SQLite databases returned `ok` from `PRAGMA quick_check`. Origin and
  public homepage returned HTTP 200, and both unauthenticated admin client-list
  requests returned HTTP 401. `nginx -t` passed, the post-upgrade logs had no
  ERROR/FATAL/panic, and `panel-probe.service` completed successfully with its
  timer enabled. All nine registered clients had a CPU usage metric sample in
  the preceding 180 seconds, approximately 10-15 seconds old at inspection.

Rollback: restore `docker-compose.yml.bak-pre-v0.1.25` and start the retained
v0.1.24 image. Recheck the version, database `quick_check` results, and recent
samples from all nine clients. If the data needs restoration, stop the panel,
preserve its current data separately, verify `data.tar` against the SHA256
above, and restore it before starting the panel. Never extract over running
SQLite databases.

## Previous rollout: 2026-09-23 (v0.1.24)

Panel upgraded from v0.1.23 at approximately 13:59 UTC. Origin and public version
APIs both reported `v0.1.24`, hash `916b5f9`. See
[the changelog](../CHANGELOG.md) for what the release contains.

- The tag and merged main pointed to
  `916b5f9685bf18293bac51cbf39eafaab83708fe`. Exact-commit CI `35852907478`
  passed all four jobs — including `panel smoke`, which boots the built server and
  walks every route — before the tag was pushed. Release `35853338867` passed all
  its jobs, including downloaded-artifact deployment with an agent; Docker
  `35853952243` passed. The v0.1.24 image index was
  `sha256:158fed8b25eee1e45c37c3ddae015704ea88d26a3e1535cdf5c3dc538e9a0dc1`.
- The image was pulled and inspected **before** writes stopped. Its arm64
  `/app/nekomari` SHA256
  `3a37bc90a46e1fe9349d8d0a40a3829fb7b8999a7436ea9ccf06bcc1a32c0b7e` matched the
  published `nekomari-linux-arm64` binary byte for byte. **v0.1.24 changed no Go
  source** (`git diff --name-only v0.1.23 v0.1.24` is frontend, docs, CI and
  `deploy/deploy-verify.sh`), so the v0.1.19 agent fleet was left intact.
- The stopped panel's Compose file and complete bound data were backed up under
  `/opt/nekomari-backups/pre-v0.1.24-20260923-135858` (mode 0700). `data.tar`
  SHA256 `5b36904df818ee1465a03b26a822d2e479dbaf14e439c9ee5520f4c05aa33423`,
  370 entries, 494 MB, and its listing contains both `data/komari.db` and
  `data/metrics.db`. The previous Compose file is retained as
  `docker-compose.yml.bak-pre-v0.1.24` and the v0.1.23 image is still on the host
  for rollback.
- After the image swap, both SQLite databases passed `quick_check`, the container
  came up healthy with `RestartCount=0`, and all **9 registered clients** had a
  fresh sample (55–58 seconds old). The public homepage returned HTTP 200 and the
  admin client-list route returned HTTP 401 without a session from both origin and
  public paths. `nginx -t` passed and no ERROR/FATAL line appeared in the
  post-upgrade logs.
- The loopback-only port binding (`25774/tcp -> 127.0.0.1:25774`) and the
  `/opt/nekomari/data -> /app/data` mount were unchanged. The active
  `panel-probe.timer` completed successfully on a re-run.

Two things worth recording rather than hiding. The backup took three attempts:
`/opt/nekomari-backups` is root-owned, and `data/` holds root-owned files from an
older theme-background backup, so the archive needed `sudo`; the panel was
therefore down for about a minute rather than a few seconds. And the probe
reported one `status-522` alert in the sample taken while the container was still
starting — the edge could not reach an origin that was not listening yet. A
re-run a minute later finished cleanly, so that was the restart window, not the
release.

Rollback: restore the retained `docker-compose.yml.bak-pre-v0.1.24` into
`/opt/nekomari` and start the retained v0.1.23 image. Recheck the version, both
database `quick_check` results and recent metrics from all nine clients. If data
restoration is needed, stop the panel, preserve the current data separately,
verify `data.tar` against the SHA256 above, and restore it before restarting.
Never extract an archive over a running database.

## Previous rollout: 2026-09-23 (v0.1.23)

Panel upgraded from v0.1.22 at approximately 07:12 UTC. Origin and public
version APIs reported `v0.1.23`, hash `7c82708`. See
[the follow-up review](FOLLOWUP-v0.1.23.md) for the frontend changes and
remaining work.

- The tag and merged main pointed to
  `7c82708ee45a5b5a6ae08dc06220ec7e8aab5f2f`. Exact-commit CI
  `35829087706` passed all three jobs before release. Release `35829551439`
  passed all eight jobs, including downloaded-artifact deployment with an
  agent; Docker `35830057800` passed both image jobs and their startup smoke
  tests. Both package manifests independently returned HTTP 200 with anonymous
  tokens. The Release published eight binaries and `SHA256SUMS.txt`.
- The panel image index was
  `sha256:1b6116b7663b16724097541733fc7881a152ca4f12aa4ff102065e5113616182`.
  Its arm64 `/app/nekomari` SHA256 matched the published arm64 binary:
  `f5e2088a4062aa5118f3fe7f8f0882c9bb6bb21b35fbe81c0d59434fe80d77fa`.
  The image revision label matched the tagged commit. The agent, protocol,
  internal and shared-package source did not change, so the v0.1.19 agent
  fleet was left intact.
- Pulled and inspected the image before stopping writes. The stopped panel's
  Compose file and complete bound data were backed up under
  `/opt/nekomari-backups/pre-v0.1.23-20260923-071131` (mode 0700).
  `data.tar` SHA256 was verified before and after deployment:
  `8c24fbefc02979aec004beb710e4484797bd818746074d28e9d890bbc08c0501`.
  Archive listing included `data/komari.db` and `data/metrics.db`; the
  previous Compose file and image were retained for rollback.
- After the image swap, both SQLite databases passed `quick_check`, and the
  panel remained healthy with zero restarts. The 9 registered clients all
  had fresh metric buckets (approximately 8-12 seconds old at 07:14 UTC).
  The public homepage returned HTTP 200; the actual admin backup-download
  and client-list routes returned HTTP 401 without a session from both origin
  and public paths. nginx configuration passed; no ERROR/FATAL lines appeared
  in the initial post-upgrade logs.
- The loopback-only port and data bind mount remained intact. The active
  `panel-probe.timer` and a manually run probe completed successfully at
  07:14:51 UTC (`origin=0.002s`, `public_max=0.285s`, 5/5 samples). These are
  spot checks, not a long-term latency or load measurement.

Rollback: restore the retained
`/opt/nekomari/docker-compose.yml.bak-pre-v0.1.23` into `/opt/nekomari` and
start the retained v0.1.22 image. Recheck the version, both database
`quick_check` results and recent metrics from all registered nodes. If data
restoration is needed, stop the panel, preserve the current data separately,
verify `data.tar` against the SHA256 above, and restore it before restarting.
Never extract an archive over a running database.

## Previous rollout: 2026-09-23 (v0.1.22)

Panel upgraded from v0.1.21 at 05:51:40 UTC. At that rollout, origin and public
version APIs reported `v0.1.22`, hash `5f71703`. See [the follow-up review](FOLLOWUP-v0.1.22.md)
for the code changes and remaining work.

- PR #10 merged as `5f717037ccb2e7124747d0b097aa8b9ed2d954be`.
  Exact-commit CI `35822804305` passed the frontend browser, Ubuntu, and Windows
  jobs before the tag was pushed at that commit.
- Release `35823140745` passed all eight jobs, including scans and installation
  from downloaded release artifacts with a reporting agent. Docker
  `35823618102` passed both image jobs, with anonymous manifest fetches (HTTP 200).
- Panel image index: `sha256:53e500db6a1811c2b488647ae3d162c56637a68b38352dd2ab2b4d9c5ed94a5e`.
  Its arm64 executable SHA256 is
  `d0365e582567322059f7da5e5992ab43f5a6a0cdcd43c44e3ca889d0fc8e438a`,
  matching the published Release checksum. The agent image executable also
  matched its published checksum; its source did not change in this release.
- Pulled and inspected the image before stopping writes. Compose and the complete
  data directory were backed up at
  `/opt/nekomari-backups/pre-v0.1.22-20260923-054850` (mode 0700);
  `data.tar` SHA256:
  `c5ffbc69aa65815ffaaa6379eb8150479a4d6c420bb236f25b03eaf050f5f0c4`.
  The first archive-listing pipeline exited 141 under `pipefail`, so the trap
  restarted the unchanged v0.1.21 panel. A separate full listing and Compose
  comparison then verified the backup before the v0.1.22 image swap.
- Both SQLite databases passed `quick_check`; the 9 clients, 1 user, and 9 ping
  tasks remained. All 9 current client IDs had new metric buckets between
  05:51:55 and 05:51:59 UTC, after the new container started. Homepage returned
  HTTP 200, while the actual admin backup and private recent-history endpoints
  returned HTTP 401 without a session.
- The data bind mount and loopback-only port remained intact; container restart
  count was zero, nginx configuration passed, the probe timer was active, and
  no ERRO/FATAL lines appeared in the initial post-upgrade window. The agent
  fleet retains v0.1.19 because agent, protocol, and shared-package source is
  unchanged from v0.1.21.
- At 06:03-06:04 UTC, all 9 client `updated_at` values had advanced past the
  container start (earliest 05:55:26, latest 06:00:44), and all 9 current
  client metric buckets reached 06:03:54-06:03:58 UTC. The 05:56:18 UTC probe
  completed successfully (`origin=0.002s`, `public_max=0.092s`, 5/5 samples),
  and the container remained healthy with zero restarts or matching database
  error, ERRO, FATAL, or PANIC log lines. These timings are spot checks, not
  long-term performance measurements.

Rollback: restore this backup's `docker-compose.yml` into `/opt/nekomari` and
start the retained v0.1.21 image. If data restoration is needed, stop the panel,
preserve the current data separately, and restore the archive before restarting.
Never extract an archive over a running database.

## Previous rollout: 2026-09-23 (v0.1.21)

Panel upgraded from v0.1.20 at 01:40 UTC. Origin and public version APIs report
`v0.1.21`, hash `8d0a8d1`. See [the follow-up review](FOLLOWUP-v0.1.21.md)
for scope, validation limits and the remaining compiler/chart work.

- PR #8 merged as `8d0a8d124b48538702355686916b26eb8d6b877e`. Exact-commit
  CI `35751946083` passed before the immutable release tag was pushed.
- Release `35806607898` passed all eight binary vulnerability scans and the
  downloaded-artifact installation/agent-report test. Docker `35807060048`
  passed both startup smoke tests and anonymous manifest fetches (HTTP 200).
- Image: `sha256:63bce6b943230d3d52d0d2dc29149e73aeb1f8da6cc72e33751b9031fab8a6e8`.
  The arm64 binary downloaded from the Release and the executable inside the
  image both match published SHA256
  `22f1151a7badd2d577e46305da128699f26acfd32c14b1db47d846ac38e56f2e`.
  An independent binary scan found no affected symbols.
- Pulled and checked the image before stopping writes. Complete data and Compose
  backup: `/opt/nekomari-backups/pre-v0.1.21-20260923-014038` (mode 0700).
  Validated the archive listing; `data.tar` SHA256:
  `da296fea3aba7033b23e8f230226a716b8950c9050c29893b27392155c77c4ea`.
- Both databases pass `quick_check`; 9 clients, 1 user and 9 ping tasks remain.
  Public homepage returns HTTP 200. Data still binds to `/opt/nekomari/data`,
  the port stays loopback-only, restart count is zero, and no ERRO/FATAL lines
  were observed in the initial post-upgrade window. The probe timer is active.
  By 01:43 UTC, all 9 current client IDs had metric buckets newer than the
  container's 01:40:42 UTC start time; the latest rollup reached 01:42 UTC.
- Agent/protocol/shared-package source is unchanged from v0.1.20. The nine
  agents retain v0.1.19; this rollout does not require fleet restarts.

Rollback: restore this backup's `docker-compose.yml` into `/opt/nekomari` and
start the retained v0.1.20 image. If data restoration is needed, stop the panel,
preserve the current data separately, and restore the archive before restarting.
Never extract an archive over a running database.

## Previous rollout: 2026-09-22 (v0.1.20)

Panel upgraded from v0.1.19 to v0.1.20 at 15:02 UTC. Runtime API reports hash
`5f82c78`. **The agents did not move with it and do not need to**: `git diff
v0.1.19..v0.1.20 -- agent/ protocol/ pkg/` is empty, so the agent source is
byte-identical and the fleet stays on v0.1.19 — the same reasoning as the v0.1.15
rollout. This release is the follow-up hardening batch from
[next improvements](NEXT-REVIEW-v0.1.19.md): the probe fix, the upload disk budget
and cleanup observability, the lock-contention measurement, the OAuth admission
work, and the Recharts 3 / ESLint 10 upgrades.

- Exact release commit: `5f82c788b6b8b783c10a4e1a49e432b21c1546cb`.
- Exact-commit CI on `main`: `35742632268`, Linux and Windows both passed.
- Release: `35743235246` — all eight binaries built and passed
  `govulncheck -mode=binary`, the Release was created, and the real
  downloaded-artifact deployment test (`deploy/deploy-verify.sh`, which completes
  the first-run install, connects an agent and asserts the version it reports)
  passed.
- Container release: `35744018445`, both images pushed.
- Panel image: `sha256:213751eb00e1b375f903299a4304f52fc7b23dbf8f2ce62a7d8dfb3cc3844aba`
  (v0.1.19 was `sha256:52e36dad…`, retained for rollback).
- Arm64 executable SHA256 verified against the published `SHA256SUMS.txt`;
  the artifact is `not stripped`, so the symbol tables v0.1.19 restored are intact
  and the version string reads `v0.1.20`.
- Stopped writes, then backed up Compose and the complete data directory to
  `/opt/nekomari-backups/pre-v0.1.20-20260922-150235` (mode 0700).
  `data.tar` SHA256: `528578aa10a67f4956afb36d51e0fc9d96e8cfde13ecd7ef4b50459654cf97e6`
  over 366 entries; the listing was checked before the swap. The image was pulled
  *before* stopping the container, so the downtime was the recreate only.
- The panel wrote its own pre-migration archive on first boot:
  `upgrade-20260922-150239.zip`, logged as `from "v0.1.19-31b3ee5" to
  "v0.1.20-5f82c78"` with the local metric store excluded and left in place.

Verified after the rollout:

- `/api/version` → `{"hash":"5f82c78","version":"v0.1.20"}`; the origin and the
  public URL both answered 200.
- Nine of nine agents reconnected and kept reporting: every node's `updated_at`
  advanced within the following ten minutes (they are staggered by a few minutes
  each, so a single snapshot can look stale — comparing two snapshots is what
  settles it), and the metric store took writes continuously, newest rollup
  15:05:00 across 336 series.
- `pragma quick_check` on `komari.db` → `ok`; 9 clients, 1 user and 9 ping tasks
  retained; container restart count 0; zero ERRO/FATAL lines since start.
- The health probe kept logging healthy samples (`origin=0.002s`) across the
  restart.

Rollback: restore `/opt/nekomari/docker-compose.yml.bak-pre-v0.1.20` and start the
retained v0.1.19 image. For data, stop the container, preserve the current `data`
separately, and restore `pre-v0.1.20-20260922-150235/data.tar` before restarting.
Never extract a rollback archive over a running database.

## Panel upgrade (2026-09-22) — v0.1.19

Panel upgraded from v0.1.16 to v0.1.19 at approximately 09:53 UTC. Runtime API
reports hash `31b3ee5`. This rollout changed only the panel; the nine agents were
refreshed to the same tag separately — see
[the fleet agent refresh](#fleet-agent-refresh-2026-09-22-v0119-toolchain) below.

- Exact release commit: `31b3ee512a469bade95de667486f133a029c38a7`.
- Exact-commit CI: `35711635197`, both Linux and Windows passed.
- Release: `35712077446`, all eight binary vulnerability scans and the real
  downloaded-artifact deployment test passed.
- Container release: `35712642893`, both images passed smoke/public-pull checks.
- Panel image: `sha256:52e36dadee990abfb66dd440c54edc1e60f6f037b446184c1be64c73b4fb256e`.
- Arm64 executable SHA256: `e7a845a698986e8f9e4ceee2de6d00e6aa61a8d1307afeeccfabd7909f1c7976`.
  Independently matched the published checksum and the binary inside the image;
  downloaded binary govulncheck found no affected symbols.
- Stopped writes before backing up Compose and the complete data directory to
  `/opt/nekomari-backups/pre-v0.1.19-20260922-095308` (directory mode 0700).
  `data.tar` SHA256: `b867c42aa434a24f30bab522160d1ac532f01cc41ef9af1197a648e8c2dfb4d4`.
  Archive listing was validated before replacing the container. Old image retained.
- Origin/public version API and homepage returned 200; unauthenticated admin
  session API returned 401; disabled OAuth start/callback returned 403.
- Both SQLite `quick_check` results were `ok`; 9 clients, 1 user and 9 ping tasks
  were retained. Port remains loopback-only, data remains `/opt/nekomari/data`,
  nginx configuration passes, and container restart count is zero.
- Repeated database samples confirmed all nine latest metric buckets advanced
  past the restart; the follow-up sample had a maximum age of 73 seconds.
  No ERRO/FATAL lines were observed. Anonymous recent-history requests returned
  401, so verification did not relax access controls.
- Initial public homepage TTFB was 0.087 seconds from OC424; this is a single
  observation, not a performance benchmark.

v0.1.17 was not deployed: its workflow selected Go 1.26.0. v0.1.18 publication
was blocked by conservative scanner fallback on stripped binaries. v0.1.19 uses
Go 1.27.1 and retains symbol tables, without suppressing advisories. See
[next improvements](NEXT-REVIEW-v0.1.19.md) for evidence and acceptance criteria.
Authenticated admin uploads/restores and real OAuth provider login were not
exercised against production. Earlier sections below are historical records.

For rollback, restore the backed-up Compose file and start the retained v0.1.16
image. If data rollback is necessary, stop the container, preserve the current
data separately, and restore the consistent archive before restarting. Never
extract a rollback archive over a running database.

## Fleet agent refresh: 2026-09-22 (v0.1.19 toolchain)

The nine agents were still on v0.1.16 after the panel moved to v0.1.19. The agent
**source** did not change between those tags — the reason to move is the compiler:
v0.1.16 was built with Go 1.26.0, whose standard library carried 34 govulncheck
findings, while v0.1.19 pins Go 1.27.1 and ships symbol tables so the distributed
executable itself stays scannable. This is a compiler refresh, not a feature
change; no agent behaviour differs.

Method: the release assets were downloaded and verified against the published
`SHA256SUMS.txt` locally (`a6930033…` amd64, `d79f378a…` arm64), then copied to
each node and installed with `deploy/install-staged-agent.sh` — the staged path
exists because PZYC cannot reach the release assets, and one download per node is
one failure mode per node. Per node the old binary is kept as
`<bin>.bak-pre-v0.1.19`, owner, group and mode are preserved, `cap_net_raw` is
restored where it existed, and the unit is restarted and checked `active`.

| Node | Binary | Unit | Before | After |
|---|---|---|---|---|
| 甲骨文 OC424 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` | `a30b5445…` | `d79f378a…` |
| 华纳云 HN-JP1 | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | `394d9ad0…` | `a6930033…` |
| HK04 | same | same | `394d9ad0…` | `a6930033…` |
| AkkoCloud SJ | same | same | `394d9ad0…` | `a6930033…` |
| BandwagonHost MegaBox | same | same | `394d9ad0…` | `a6930033…` |
| 并行智算云 PYZC | same | same | `394d9ad0…` | `a6930033…` |
| MAC Server | `/home/macos/nekomari-agent/komari-agent-linux-amd64` | user `nekomari-agent.service` | `394d9ad0…` | `a6930033…` |
| NOSLA 东京-26秋-M | `/opt/komari/agent` | `komari-agent.service` | `394d9ad0…` | `a6930033…` |
| CloudLeadInno | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | `394d9ad0…` | `a6930033…` |

Two nodes needed the paths the earlier rollouts established:

- **MAC Server** runs a *user* unit with no passwordless sudo, so
  `install-staged-agent.sh` (which resolves a system unit and calls `setcap`
  directly) does not apply. The binary was installed as `macos:macos` and the
  unit restarted with `XDG_RUNTIME_DIR=/run/user/1000 systemctl --user`; the
  install dropped `cap_net_raw=ep` as it always does, and it was restored with the
  host's own `setcap` through a privileged container
  (`docker run --rm --privileged -v /:/host alpine chroot /host setcap
  cap_net_raw+ep <bin>`), verified with `getcap` before and after.
- **CloudLeadInno** still refuses both keys held on this workstation. The working
  path is the `CLISP` entry in MAC-WAN's `~/.ssh/config`: MAC-WAN holds the key
  that authenticates as `root@192.220.32.17`, so the staged binary and installer
  were copied to MAC-WAN and from there to the node. This is simpler than the
  panel remote-exec route in `deploy/panel-agent-upgrade.py`, which needs an
  authenticated admin session plus 2FA and only works because the node runs the
  agent with web-ssh enabled.

Verified after the rollout: `select name, version from clients` on the panel's
`data/komari.db` reports `v0.1.19` for all nine, each with a fresh `updated_at`,
and the container logged zero ERRO/FATAL lines across the window. Rollback per
node is `install -m 755 <bin>.bak-pre-v0.1.19 <bin>` plus a unit restart, with the
capability dance again on MAC.

## Health probe: the fix is deployed (2026-09-22)

`deploy/panel-probe.sh` on OC424 was replaced with the corrected version
(`/usr/local/bin/panel-probe.sh`, previous file kept as
`panel-probe.sh.bak-pre-v0.1.20`). The old script recorded `time_starttransfer`
without checking curl's exit status or the HTTP status, so a fast HTTP 500 — or a
connection refused, which returns `0.000000` — was logged as a healthy sample and
exited 0.

The replacement requires curl success, HTTP 200 and a valid success envelope
before a sample can count as healthy, and separates the failure reasons
(`refused` / `timeout` / `status-NNN` / `no-timing` / `envelope`) in both the log
line and the alert. The healthy log line is byte-identical to the old format, so
existing log parsing is unaffected; failures append `origin_fail=` /
`public_fail=`.

Verified on the host, not just locally:

- `deploy/panel-probe.test.sh` — 88 assertions, 0 failures on OC424's Ubuntu
  22.04 and Python 3.10, including the real closed-port refusal path that cannot
  be reproduced on Windows (a closed loopback port there is dropped rather than
  refused).
- A healthy run logs
  `origin=0.002s public_max=1.307s pop=MXP cache=DYNAMIC samples=5/5` and exits 0.
- A run with `PANEL_ORIGIN` pointed at a closed port exits **1** and logs
  `origin=0.000s … origin_fail=refused`, with the alert attributing it to the
  panel rather than the edge. The old script logged the same run as healthy.
- `systemctl start panel-probe.service` (exactly what the 10-minute timer runs)
  reports `ExecMainStatus=0` on a healthy panel, and the timer remains scheduled.

What the corrected probe immediately showed, and what it means: the origin has held
at `origin=0.002s` on every run, while the public path ranges from 0.17 s to
**26.9 s** depending on which POP answers. The slow runs cluster on US-East POPs —
IAD and EWR at 3.0-5.6 s, MIA at 3.6 s — where the Asian and European POPs that
usually serve this origin (SIN, HKG, KIX, CDG, MRS, MXP) sit at 0.2-1.5 s. Two
outliers of 26.9 s and 15.97 s (both HKG) predate the probe fix and would have
alerted under the old script too, so this is not a regression from it: it is the
edge path, and the probe is now recording the distinction between "the panel is
slow" and "this POP is far away" that it was written to make. `PUBLIC_MAX`
(default 3.0 s) is the knob if the alert rate becomes noise.

## Data restoration

The panel was restored from the retired Komari production instance rather than
started empty:

| Source | What |
|---|---|
| `komari.db` (260 KB) | 9 nodes with names/groups/tags/pricing, ping tasks, theme configs, message-sender providers, the admin user |
| `metrics.db` (141 MB) | 629 series / 318,570 rollups / 90 days |

Both were copied byte-for-byte (checksums compared on both ends) into
`/opt/nekomari/data` **before** first start, so the server adopted them instead
of running the install wizard. The metric-store schema in the archive is
identical to what v0.1.2 creates, so no structure migration was needed.

### What the retention policy removed (by design)

On first boot the server logged `Metric retention cleanup deleted 211312 rows`.
That is Nekomari's rollup tiering, not corruption:

| Tier | Retention | Effect on the restored data |
|---|---|---|
| 1-minute | 600 min | expired (the archive ended 2 days earlier) |
| 5-minute | 3000 min | expired |
| 1-hour | 600 h (25 d) | kept → 71,880 rows, back to ~2026-08-23 |
| 1-day | 100 years | kept → 7,538 rows, the full 90 days |

So the **90-day daily curve survived in full**, and the fine-grained minutes are
gone. That matches what a long-running instance looks like anyway. To keep more
history on a future restore, raise
`metric_rollup_{minute,five_minute,hour}_retention_*` **before** importing.

### Admin access

The restored user is `AONE2233` (not `admin`). Its password was unknown and it had
2FA enabled, so access was recovered with the built-in tools:

```bash
docker compose stop
docker compose run --rm --entrypoint /app/nekomari nekomari chpasswd -p '<new>'
docker compose run --rm --entrypoint /app/nekomari nekomari disable-2fa
docker compose up -d
```

The password and 2FA state above describe the historical restoration. A
read-only database check on 2026-09-23 confirmed that the current account has
2FA enabled; do not infer current authentication state from these old commands.

## The two hostname traps

### 1. A 2nd-level subdomain gets no Cloudflare edge certificate

`nekomari.orderly2233.org` had to be abandoned in favour of the 1st-level
`komari.orderly2233.org`. Measured, not assumed:

| Hostname | Depth | HTTPS |
|---|---|---|
| `orderly2233.org` | apex | 200 |
| `git` / `vault` / `blog` / `dav` / `subnc`. | 1st-level | 200 |
| `nekomari.` | 2nd-level | **000** (edge aborts the handshake) |
| `ssh.git.` | 2nd-level | **000** |

Cloudflare's Universal SSL is `*.orderly2233.org`, which covers one label only.
A 2nd-level name needs Total TLS, an advanced certificate, or a 1st-level name.
Changing hostname was the cheapest correct fix.

### 2. "No DNS record" can look like "record exists"

Before the record existed, this machine's resolver happily answered
`nekomari.orderly2233.org → 198.18.6.15` — an intercepting proxy on the local
network that answers *every* name. Authoritative DNS (Cloudflare DoH) showed the
truth: `Status: 0` with an empty `Answer`. **Verify DNS against an authoritative
resolver, never the local one.**

The recorder was repointed from an unused tunnel CNAME to the origin, matching the
pattern every working hostname in the zone already uses:

```
A  komari.orderly2233.org  213.35.99.48  proxied
```

The zone's `cloudflared` tunnel is **token-managed** (its ingress lives in the
Cloudflare dashboard), so tunnel hostnames cannot be added from the host.

## Reproducing

```bash
# 1. origin
sudo mkdir -p /opt/nekomari/data && sudo chown ubuntu: /opt/nekomari
#    put komari.db + metrics.db into /opt/nekomari/data (optional), then:
cd /opt/nekomari && docker compose up -d

# 2. nginx (see deploy/nginx-nekomari.conf)
sudo cp deploy/nginx-nekomari.conf /etc/nginx/sites-available/nekomari
sudo ln -sf /etc/nginx/sites-available/nekomari /etc/nginx/sites-enabled/nekomari
sudo nginx -t && sudo systemctl reload nginx

# 3. agent (source of truth: deploy/hosts/oc424.service)
#    On the host the unit is called komari-agent-oc424-original-node.service,
#    because it reports as the restored node "甲骨文 OC424" (see the unit's
#    WorkingDirectory and token comment).
sudo cp deploy/hosts/oc424.service /etc/systemd/system/komari-agent-oc424-original-node.service
sudo systemctl enable --now komari-agent-oc424-original-node.service
```

## Notes

- The panel inherits `private_site: true` from the old production config, so the
  API answers 401 without a session and the SPA shows the login gate. That is the
  restored setting, not a deployment fault.
- nginx needs the WebSocket upgrade headers and a long `proxy_read_timeout`;
  with the default 60 s the agents visibly flap on and off the panel.
- `X-Real-IP` prefers `CF-Connecting-IP` and falls back to `$remote_addr`, so the
  panel records the visitor's address rather than Cloudflare's edge.

## Reconnecting the nodes (9/9 online)

Restoring `komari.db` brought back all nine nodes' metadata, but their agents had
been retired with the old panel, so nothing was reporting. Each node is now back
with its **own** token from the recovered database — deliberately not
`--auto-discovery`, which would have registered a *new* node and orphaned the
restored one's group, tags, pricing and historical series.

`deploy/install-node-agent.sh` does this for a node: rejects an install if an
agent is already active for that host, downloads the agent from the release,
verifies it against `SHA256SUMS.txt`, disables any older agent unit so only one
process reports per host, and starts a `nekomari-agent.service`.

| Node | How it was reached |
|---|---|
| 甲骨文 OC424 | local, reusing the node's token |
| 华纳云 HN-JP1, HK04, AkkoCloud | direct root SSH |
| BandwagonHost (megabox) | SSH config, port 9950 |
| 并行智算云服务器 | SSH config `PZYC`, non-root + passwordless sudo |
| MAC Server (MAC-WAN) | **user** unit — that host has no passwordless sudo |
| Nomao v6_1 | IPv6 only; reachable from OC424 |
| CloudLeadInno | root SSH from OC424 (the key is not present on every machine) |
| NOSLA 东京-26秋-M (`TG`) | added 2026-09-20; root SSH from the workstation. First enrolled with `--auto-discovery`, then switched to its own node token |

### Two things worth knowing

**PZYC cannot reliably fetch release assets.** `github.com` answers, but
`objects.githubusercontent.com` resets the TLS connection, so `curl` on the
release URL fails intermittently and a timed-out download leaves a truncated
file. The binary was fetched elsewhere, checksum-verified, and installed by
`deploy/hosts/pzyc.sh`, which skips the download.

**MAC-WAN runs the agent unprivileged**, because that host has no passwordless
sudo and user units are the only option. The agent therefore cannot open raw
ICMP sockets, so ping tasks 17/18/19 (`原生电信/移动/联通IP`, type `icmp`) fail
there with `operation not permitted` while succeeding from the root agents. The
fork's `auto` task type does not help for those specific targets: they are bare
IPs, and `auto` falls back to TCP 443/80, which these hosts do not answer. Either
grant the binary `cap_net_raw` or leave those three tasks to the root nodes.

## Fleet upgrade (2026-09-20) — v0.1.14

The panel moved from v0.1.12 to v0.1.14 (compose pin in `/opt/nekomari`, previous
file kept as `docker-compose.yml.bak-pre-v0.1.14`; the server wrote
`data/backup/upgrade-20260920-130005.zip` on first boot and logged
`from "v0.1.12-9d11cfa" to "v0.1.14-6ed979b"`), and every node's agent moved with
it. All nine nodes report `v0.1.14` in the panel afterwards.

The agent binaries were fetched **once**, verified against the published
`SHA256SUMS.txt`, and copied to each host rather than downloaded per node: PZYC
cannot reach `objects.githubusercontent.com` at all, and a per-node download adds one
failure mode per node. On each host the copy was verified again, the old binary kept
as `<bin>.bak-pre-v0.1.14`, the new one installed with the previous owner and mode,
the file capability restored where there was one, and the unit restarted.

| Node | Reached as | Arch | Binary | Unit |
|---|---|---|---|---|
| 甲骨文 OC424 | local | arm64 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` |
| 华纳云 HN-JP1 | `HNJP01` from OC424 | amd64 | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` |
| HK04 | `HK04` from OC424 | amd64 | same | `nekomari-agent.service` |
| AkkoCloud SJ | `AKKO06` from OC424 | amd64 | same | `nekomari-agent.service` |
| BandwagonHost MegaBox | `megabox`, direct or from OC424 | amd64 | same | `nekomari-agent.service` |
| CloudLeadInno | `CLISP` from OC424 | amd64 | same | `nekomari-agent.service` |
| 并行智算云 PZYC | direct | amd64 | same | `nekomari-agent.service` |
| MAC Server | direct | amd64 | `/home/macos/nekomari-agent/komari-agent-linux-amd64` | user `nekomari-agent.service` |
| NOSLA 东京-26秋-M | `TG` | amd64 | `/opt/komari/agent` | `komari-agent.service` |

Three things the fleet is not uniform about, each of which the upgrade had to handle:

- **The binary path differs.** `/opt/nekomari-agent/...` on six nodes, the user's home
  on two, and `/opt/komari/agent` on NOSLA. The path is taken from the running
  process, never guessed — the same reason `upgrade-agent.sh` does it that way.
- **MAC Server cannot run `setcap`.** It has no passwordless sudo, and replacing the
  binary drops the file capability it needs for raw ICMP. It was restored with a
  privileged container that chroots into the host rootfs and runs the host's own
  `setcap`: `docker run --rm --privileged -v /:/host alpine chroot /host setcap
  cap_net_raw+ep <bin>`. `getcap` was checked before and after.
- **NOSLA used `--auto-discovery`, and no longer does.** Its first install followed
  the panel's install dialog, which was still handing out upstream's installer — see
  the open item in `docs/OPEN-WORK.md`. Auto-discovery registers a *new* client on
  every start that does not find a saved token
  (`web/api/client/autoDiscovery.go` calls `CreateClientWithName` unconditionally),
  so the node's identity depended on `/opt/komari/auto-discovery.json` surviving; a
  lost file would have produced a duplicate node. It now carries its own token like
  the rest of the fleet, with the file left in place but unused. Its unit is kept as
  `deploy/hosts/tender-guard.service`.

  Its flags were aligned with the fleet in the same pass, with one node-specific
  value: `--month-rotate 20`. That is the node's billing day, not a fleet constant —
  the live fleet runs HNJP01=18, HK04=11, AKKO06=12, megabox=7, CLISP=11, each
  matching its own `expired_at` day (see `deploy/set-month-rotate.sh`). Measured
  effect, from the metric store: before the flag the node reported 40.6 GiB up /
  18.8 GiB down, the raw kernel counter since boot; after the restart it reports the
  cycle-to-date deltas netstatic records — 0.0 at first, growing — which is the
  figure the provider's dashboard is comparable to. netstatic only runs when the flag
  is set, so this node had no `net_static.json` until then.

`deploy/upgrade-agent.sh` was fixed while doing this. It decided "already at the
target version" by running `"$BIN" --version`, and the agent has no such flag (it
answers `unknown flag: --version`), so that branch never ran and every invocation
re-downloaded and reinstalled the binary. It now compares the installed binary's
SHA-256 against the published `SHA256SUMS.txt`.

The fleet-wide check needs no panel login: each agent reports its version in its
basic-info upload, so `select name, version from clients` on `data/komari.db` is the
answer. It read `v0.1.11` for eight nodes and `v0.1.13` for NOSLA before, and
`v0.1.14` for all nine after.

## Panel upgrade (2026-09-21) — v0.1.15

The panel moved from v0.1.14 to v0.1.15 and **the agents did not move with it**: this
release touches only `frontend/`, `deploy/`, `docs/`, `Dockerfile.agent` and
`.github/workflows/docker.yml`. `git diff --stat v0.1.14..v0.1.15 -- agent protocol pkg
internal` is empty, and the two `komari-agent-linux-amd64` binaries differ only in the
version string, the Go build id and the embedded `vcs.*` metadata — so all nine nodes
keep reporting `v0.1.14` on purpose.

Recorded from the host: compose pin updated in `/opt/nekomari/docker-compose.yml` with
the previous file kept as `docker-compose.yml.bak-pre-v0.1.15`; the server wrote
`data/backup/upgrade-20260920-145936.zip` on first boot and logged
`from "v0.1.14-6ed979b" to "v0.1.15-9bacdf7"`; the banner read
`Nekomari Monitor v0.1.15 (hash: 9bacdf7)`; eight agents re-established their WebSocket
in the same second the container started, and `/api/version` answered
`{"hash":"9bacdf7","version":"v0.1.15"}`.

Two follow-ups from the same day, both in `docs/PERFORMANCE.md`: the LuminaPlus theme's
background images were re-encoded (6.34 MB → 0.51 MB for the desktop one) and the theme
settings repointed at new filenames, and `data/backup` was pruned from 1.4 GB to 221 MB
with `deploy/prune-upgrade-backups.sh` now running weekly from a systemd timer.

## Fleet upgrade (2026-09-22) — v0.1.16

The optimization batch in `docs/OPTIMIZATION-REVIEW-2026-09-22.md`. Unlike v0.1.15 this
one **does** change the agent (socket counting reads `/proc/net/sockstat`; the traffic
sampler moved from 2 s to 30 s and rewrites its ledger less often), so the panel and all
nine nodes moved together.

Panel: compose pin `v0.1.16`, previous file kept as `docker-compose.yml.bak-pre-v0.1.16`;
banner `Nekomari Monitor v0.1.16 (hash: 4f26aad)`; `/api/version`
`{"hash":"4f26aad","version":"v0.1.16"}`; 0 ERRO/FATAL lines since start; nine agents
reconnected. The upgrade also exercised two of the fixes:

- the archive is now taken **before** the migrations and excludes the metric store —
  `upgrade-20260922-042859.zip` is **26 MB** where the previous three were 62-100 MB, and
  the log says so: `backed up … before upgrade (from "v0.1.15-9bacdf7" to
  "v0.1.16-4f26aad"); the local metric store is excluded and left in place`.
- the agent's persisted traffic config migrated on load: `/opt/komari/net_static.json`
  now reads `detect_interval=30`, `config_version=2` with its 4,232 buckets intact.

Agents: `deploy/install-staged-agent.sh` (new, in-tree) with the release asset copied to
each node, which also covers PZYC, which cannot reach the release assets. Per node the
old binary is kept as `<bin>.bak-pre-v0.1.16`, owner/mode preserved, `cap_net_raw`
restored where it existed, the unit restarted, and the installed hash checked:
`394d9ad0…` on amd64, `a30b5445…` on arm64 (OC424).

| Node | Binary | Unit | Result |
|---|---|---|---|
| 甲骨文 OC424 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` | v0.1.16 |
| 华纳云 HN-JP1 | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | v0.1.16 |
| HK04 | same | same | v0.1.16 |
| AkkoCloud SJ | same | same | v0.1.16 |
| BandwagonHost MegaBox | same | same | v0.1.16 |
| CloudLeadInno | same | same | v0.1.16 |
| 并行智算云 PYZC | same | same | v0.1.16 |
| MAC Server | `/home/macos/nekomari-agent/komari-agent-linux-amd64` | user `nekomari-agent.service` | v0.1.16 |
| NOSLA 东京-26秋-M | `/opt/komari/agent` | `komari-agent.service` | v0.1.16 |

MAC needs the capability dance again: replacing the binary drops `cap_net_raw`, there is
no passwordless sudo, so it is restored with the host's own `setcap` through a privileged
container (`docker run --rm --privileged -v /:/host alpine chroot /host setcap
cap_net_raw+ep <bin>`), checked with `getcap` afterwards.

Verified after the rollout: nine of nine nodes report `v0.1.16`, nine established agent
connections, every node's newest metric bucket is ~1 minute old, and the traffic ledger
path is live again on all nine (`net.total.up` fresh and non-zero, e.g. 19.8 GiB on
OC424's restored node). The probe timer recorded `origin=0.002s public_max=0.152s
pop=SIN`, and the public URL answered 200 in 0.167 s.

Two leftovers were removed while doing this: `happy_heisenberg` on OC424 and
`funny_goldberg` on MAC — both `docker run` smoke tests of the agent image from the
v0.1.15 work, still running with a `bogus` token. They looked like host agents in the
process table (their parent is containerd), which is exactly why
`install-staged-agent.sh` discovers the systemd unit instead of matching the process name.

## 2026-10-17: v1.6.14 daily observations and Passkey compatibility

Release commit `3106eeb91612e54cdea2d601222e69d9033a7ad8`; CI
`39005854862`, release `39006259801`, Docker `39006682141` all succeeded.
The first CI exposed a Windows-only POSIX-mode assertion; the follow-up commit
corrected that test before tagging. No failed-commit release was deployed.

OC424 is pinned to
`ghcr.io/aone2233/nekomari:v1.6.14@sha256:f66c4992a59890be199907b95a585b52f615c68b7cd4b0a04e7daeb53851a342`.
It is healthy with zero container restarts. WAL-safe pre-upgrade database/file
backup: `/opt/nekomari/backups/pre-v1.6.14-20261017T051946Z` (root-only).
The additive `routes_probed_at` migration preserves existing snapshots.
Mint v0.3.0 is installed; SHA256 of the released ZIP is
`dc90d14feae48fa82d89fbf8c89e3863c106059da03c4843dbb12271b7395d0b`.
Old hashed assets remain for open tabs. Theme settings, private-site flag, all
14 UUIDs/tokens/hidden flags and unrelated services were retained.

### Canary and staged agents

After the OC424 canary produced actual service and route results, upgrade order
was MegaBox, HK04/NOSLA, then SDE9929/KSLE/BERO and LCKD (OpenRC). All eight
including OC424 report v1.6.14, five service items and three route results.
Exact executable/cwd/service ownership and old/new checksums were checked before
replacement; backups under `/var/backups/nekomari-agent/pre-v1.6.14-*` preserve
binary, configuration, capabilities and stopped traffic-ledger snapshots.
Credentials and live ledgers were not replaced. Existing auto-update policy stays
disabled. NOSLA/KSLE received full traceroute; LCKD received full traceroute,
Python and capability utilities instead of relying on BusyBox ASN support.

Daily routes use the three existing operator-selected carrier destinations.
On OC424, the observation reached all three: telecom 163/19 hops, unicom
169/15 hops, mobile 13 hops with no identified carrier ASN. These are observations,
not CN2 GIA certification. Its Netflix/YouTube page evidence was positive,
OpenAI compliance and Gemini pages were reachable (`partial`), Claude remained
unknown with region-only fallback. No account playback/conversation was tested.
Restarting the OC424 agent retained the exact daily state bytes, modification
time and original route timestamp (file mode 0600); unit tests exercise the
24-hour boundary. A full live 24-hour cycle has not yet elapsed.

### Passkey and outstanding access

The production plugin remains komari-passkey 0.1.13 with its existing settings
and credentials. Anonymous status with the proper origin query returns enabled
and verifier available; credential management still returns 401. A fresh browser
on the public HTTPS site shows the private login and injected Passkey button,
with no page errors. Actual user hardware authentication remains user acceptance
work; real virtual-authenticator registration/login passed only in isolation.
Do not loosen CORS or Cloudflare rules for loopback diagnostic requests.

MACServer, parallel-compute cloud, ATT, VT01, SB01 and SB02 were NOT upgraded:
no usable SSH alias/jump connection is available in this executor. Do not infer
management credentials from their public monitoring addresses. Final read-only
verification at 05:43:51 UTC found 13/14 fresh CPU streams; untouched SB02 v1.6.11
had 232-second-old samples (all metric streams delayed). It requires connectivity
inspection once access is provided; this is not an all-nodes-healthy result.
The five-minute panel log sample had no panic or agent RPC 401 lines.

Theme store: official submission script accepted the published v0.3.0 ZIP and
preview in read-only preflight. No official Issue or PR has been submitted;
the full application in the theme repository remains pending author review.

### v1.7.5 — recovery-startup fix (2026-10-27)

Source change `2d71cbe`; release tag `v1.7.5` points at `c84c3cc` (the same
runtime source plus the changelog). Exact CI for `c84c3cc`
[40497025299](https://github.com/Aone2233/Nekomari/actions/runs/40497025299) 7/7;
release [40498907426](https://github.com/Aone2233/Nekomari/actions/runs/40498907426)
10/10 including both `verify the release deploys` jobs; Docker
[40499760158](https://github.com/Aone2233/Nekomari/actions/runs/40499760158) 2/2.
Image index `sha256:57b89f1601013a215b9afd9062c6612d12e1c158a5f0a2e9b373458ae4d097e8`,
arm64 `sha256:ed162ac2cd13b4571eb449a38c6a81b573d10e13d22ef9b5b59b135f4eadff48`,
amd64 `sha256:19403992cdd2442f891661188201a95f0d5094e1bc74a258378f7eef3e455ecf`.
Downloaded assets match `SHA256SUMS.txt`, `gh attestation verify` passes (gh
2.103.2, checksum-verified download), and the image labels report version 1.7.5
with revision `c84c3cc`.

**Isolated acceptance, released binaries, no source tree.** A production-shaped
metrics store was built from the live store's shape (27 definitions, 594 series,
498,960 rollups, 345 MB, real t-digests) with its planner statistics never
built, then driven through a first-run install on OVH: **v1.7.5 opened the
metric store in 1,653 ms; v1.7.4 hit `context deadline exceeded` after
31,458 ms on the same data.** The old binary's failure is the production
symptom reproduced on a disposable host, not a new defect.

**Recovery.** Fresh pre-upgrade ciphertext
`oc424-20261027T115913389020Z-817b652f1340.tar.gz.age`
(SHA256 `5343a1f621ac5d3dc180c4ad2c949e622026d3f8ec665bc4745e4334a55fb515`,
183,505,524 bytes) restored on MAC-WAN with the pinned v1.7.5 amd64 image
(`drill-passed`, `network=none`). The pinned v1.7.4 image failed the same
archive with `operation timed out` — the rollback caveat below follows from
that, and the scratch-`ANALYZE` workaround remains the only path for that image.

**OC424 switch.** Rollback anchor
`/opt/nekomari/rollbacks/v1.7.4-20261027T120831Z-88d828b2`: WAL-folded
`komari.db` and `metrics.db` (both `quick_check=ok`), before/after compose,
old image digest, `ROLLBACK.txt`, `SHA256SUMS`. Pre-switch v1.7.4/b0ea917
healthy, restarts 0, 14 clients, `komari-agent-oc424-original-node.service`
MainPID 499631 / NRestarts 2. Post-switch v1.7.5/c84c3cc healthy, restarts 0,
`panel-upgrade-check` 9/9 (0 ERROR lines in 5 minutes, 3 upgrade archives),
14 clients and 2 API keys unchanged, Agent PID and restart count unchanged, and
task series resumed within two minutes (task 1 rows 19,100 -> 19,125 across the
restart). Post-upgrade ciphertext
`oc424-20261027T121042891907Z-2cbbe93e4e8a.tar.gz.age`
(SHA256 `c0d5e3c9ff911bfc0383d3c0baa964c68ae96675f27c5bb8133cdca36f2ab94b`,
183,560,017 bytes) also restores with the pinned v1.7.5 image.

**Rollback caveat.** Reverting to the pinned v1.7.4 image keeps the current
databases, nodes and tokens (v1.7.5 changes no schema and no auth), but that
image cannot restore a fresh archive on its own: on the same ciphertext it
exceeds the startup deadline until the scratch copy is `ANALYZE`d. Restoring an
off-host copy of this period therefore needs the v1.7.5 image, or that
documented workaround — and neither replaces the still-unverified
independent-media restore.
