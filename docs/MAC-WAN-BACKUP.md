# MAC-WAN backup receiver (updated 2026-11-05)

## 2026-11-09 v1.8.0 production cutover

Pre and post snapshots were produced by the existing OC424 producer and
received into new private `manual/` directories while the panel was online:

- Pre: `manual/v180-pre-20261109T0230Z/oc424-pre.tar.gz.age`,
  245,230,973 bytes, SHA-256
  `d8c5bf371f575578132c056a3372fdca6be0eba428215e91d0d1fe9fbddec40b`.
- Post: `manual/v180-post-20261109T0233Z/oc424-post.tar.gz.age`,
  245,110,016 bytes, SHA-256
  `c8c053e0086f1f5af9e784c0c534bff0c7e2421270c1a72ba466024d40d44b18`.

Both archives authenticated with the MAC-WAN identity; extracted databases
passed `quick_check`. The post archive booted an isolated amd64 v1.8.0
container with 14 clients, 13 tasks, `/api/version` v1.8.0/a92a242 and 0
restarts; the scratch container and plaintext were removed. The decryption
identity stayed on MAC-WAN. These are online snapshots, not cross-store atomic
and not proof that every in-memory sample was captured. Backup/health timers
were not changed.

## 2026-11-07 IPv6 production canary

After an old-panel stop with exit code 0 and a check that neither SQLite file
had another observed process holder, a recipient-only archive was created and
the old panel restarted while it was verified off-host. The MAC-WAN copy is
`manual/ipv6-cold-handoff-20261107/cold-handoff.age`, SHA-256
`a12f91567db3e5906fd76d9e8327aea90d8b2512bb38e81a0ad7a1481fdd2443`.
Authenticated, network-free restore drills booted both v1.7.29 and the local
candidate v1.7.30. A separate scratch rehearsal booted the candidate and then
the old image on the migrated, no-pinned-task data and preserved all client and
original ping-task rows. This stopped-panel snapshot is not a proof that the
old binary drained every asynchronous or external writer, and neither the
archive nor that rehearsal authorizes overwriting current live databases or
downgrading after pinned tasks are created.

After the OC424 candidate cutover, a separate online archive was received at
`manual/ipv6-candidate-post-20261107T1720Z/oc424-20261107T172108698210Z-b3b8c5041a45.tar.gz.age`,
SHA-256 `75a63d6b381bf9c8501ed740e4c7c6dc76cf6f971318b2e3f45af7b1dd5f82c5`.
Its sidecar and authenticated, network-free candidate boot drill passed. Its
online snapshots still omit pending in-memory samples and are not cross-store
atomic. The decryption identity remained on MAC-WAN and the scheduled timers
were not changed. The first attempt to pipe the stopped archive into the
receiver failed because its source process did not inherit stdin; the verified
ciphertext was instead copied to the dedicated manual directory and checked
against the OC424 SHA-256 before both drills.

## 2026-11-07 IPv6 pre-release snapshot

A manual online snapshot was received at
`manual/ipv6-pre-20261107T163138Z/oc424-20261107T163138092977Z-4e0d4fa7bc42.tar.gz.age`
(231,559,535 bytes, SHA-256
`f783ba3b0c1b97f00447ca51d474678cbdb0a2f566f0bebcf47b84a76f922935`).
Its sidecar, authenticated extraction, manifest and SQLite checks passed, as did
a network-free v1.7.29 amd64 isolated boot drill. The first verification attempt
could not start `age` because the interactive PATH omitted the installed private
binary; repeating it with the existing recovery `bin/` in PATH passed. The
identity stayed on MAC-WAN, the scratch directory is empty, and both backup and
health timers remained active. This is an online snapshot, not a coordinated
cross-store snapshot or proof that in-memory measurements were captured.

## 2026-11-07 scheduled trigger

The user timer actually triggered at 03:30:27 UTC and its receiver exited
successfully at 03:31:36 UTC. The scheduled archive
`oc424-20261107T033027742410Z-a49ef5da12ee.tar.gz.age` is 230,519,758 bytes;
its SHA-256 sidecar verifies on MAC-WAN. This is a scheduled run, unlike the
previous day's manual retry. It is not a new decryption/restore drill, and the
in-memory observation limitation below remains. MAC-WAN direct SSH timed out
after the run began; the existing host-key-pinned OC424 reverse tunnel provided
the subsequent read-only confirmation. No service, timer or credentials changed.

## 2026-11-07 metric snapshot limitation

The installed OC424 producer snapshots SQLite from outside the running panel.
Those snapshots include committed WAL pages, but not records in the panel's
report queue or its in-memory minute/coarse summaries. Earlier integrity and
network-free boot drills remain valid for the captured files, not proof that
every accepted observation was captured. The draft in-process download fix is
not deployed or connected to this producer. A scratch restore revealed that an
earlier draft prematurely sealed an open aggregation window and could lose its
pre-snapshot contribution after new writes. The local revision keeps unfinished
parents reconstructible from verified durable children and fails on conflicting
ancestors; this is still not a cross-database snapshot or a tested encrypted
handoff. Its real-world cost and RPO remain unmeasured. No timer, retention or
credential was changed.

## 2026-11-06 Emerald theme change

Two fresh manual encrypted archives, distinct from the scheduled retention set,
passed checksum, authenticated extraction, manifest/database verification and
network-free v1.7.29 isolated boot drills:

- Pre: `manual/emerald-pre-20261106T104539Z/oc424-20261106T104540070270Z-a813c8016a06.tar.gz.age`,
  SHA-256 `a89c5aab71d6af6f01f01aeccf0bc361ada246be6a04389569f945812944d79f`.
- Post: `manual/emerald-post-20261106T105436Z/oc424-20261106T105436176539Z-07195dfa2dc2.tar.gz.age`,
  SHA-256 `014baae5644eb846a45b11c1116a86a64b25f48c1c6dd940519c5b57abddf3a8`.

The decryption identity stayed on MAC-WAN; scratch restores were removed. The
patched theme zip, source patch and locally resolved lockfile are also retained
with the pre archive; the non-secret original v2.0.5 theme tree is separately
retained as `original-205.tar.gz` (SHA-256
`4f7b2a712478e59b9e8127ae9f982e8e2bcb720b4ad2397be9ddf8ee0bc40a9a`).
Direct MAC-WAN access timed out after cutover, so the
post archive and drill used the existing host-key-pinned OC424 reverse tunnel.

The scheduled 03:30 UTC backup on 2026-11-06 failed because another operation
held the scheduled directory lock. A later manual service run at 05:38 UTC
succeeded, producing `oc424-20261106T053837007579Z-c5aa8f3aa886.tar.gz.age`
(`8c8f26a630d4a9b06834c10ab03af835338eaa9e218e366f5a740decbdaf803f`).
Investigate the overlap and observe the next automatic trigger; manual success
does not prove the scheduler recovered. At 03:30:17 UTC the health service also
started and it takes a shared lock on the same file; it is a plausible competitor,
but the logs do not prove which process acquired the lock first. A current
read-only health inspection took 0.82 s and returned `ok`. No timer or retention
policy was changed.

This replaces the relinquished OVH receiver, not the separate offline-media
requirement. On MAC-WAN, the `macos` systemd user manager has lingering enabled.
The daily `nekomari-macwan-oc424-backup.timer` starts the bounded encrypted
receiver at 03:30 UTC (11:30 Asia/Shanghai), with persistent catch-up; its first
scheduled trigger succeeded on 2026-10-29. A separate 15-minute health timer checks
the newest ciphertext and pushes the non-secret status to the existing OC424
watcher. The receiver keeps 14 scheduled pairs in a dedicated directory;
`incoming/`, the initial executor copy and prior release copies are not in that
retention set. Actual incident delivery and independent-media restore remain
separate; a manual success is not proof of a scheduled trigger.

## 2026-11-05 v1.7.29 delivery

Fresh encrypted archives were received with checksum sidecars in separate
private manual directories:

- Pre: `manual/v1729-pre-20261105T0217Z/oc424-20261105T021731029133Z-8f9f93289d16.tar.gz.age`,
  SHA-256 `348223db54dc13d27bfbcb08e8571499a3f100a0f8f6dded2285aa56ae2c4dfd`.
- Post: `manual/v1729-post-20261105T0248Z/oc424-20261105T024834645114Z-102d2ee872c2.tar.gz.age`,
  SHA-256 `864d9c38637a90067196ee80e24a4034c03fb2ee06af45c30a7cbb48bb857c36`.

Both sidecars, authenticated decryption, manifest checks and both database
checks pass. Network-free isolated drills boot v1.7.27 from the pre archive
and v1.7.29 from the post archive; a separate pre-archive drill also boots
v1.7.29. An initial pre-archive attempt used a mistyped digest and could not
start the container; the corrected retries passed. Temporary plaintext and
restore containers were removed; the identity never left MAC-WAN.

Direct SSH to MAC-WAN timed out during this delivery; the existing OC424
reverse tunnel with the pinned host key worked. Both backup and health user
timers were active after the drill; the last observed OC424 health report was
`ok` at 02:45 UTC. Manual backups and active timers do not prove the next
scheduled 03:30 UTC trigger or an independent-media restore. Neither the
schedule nor the keep=14 policy was changed.

## 2026-11-04 v1.7.27 delivery

The v1.7.27 pre archive (`5c142e5d…4b86`) and post archive
(`9e8a312b…26bf`) reached the private `manual/v1727-recovery-20261104/`
directory with sidecars; `sha256sum` matches on arrival. Two isolated
restore drills pass with `--network=none`, no host ports and scratch-only
plaintext: the pre archive (v1.7.26 state) boots the v1.7.26 amd64 image
`sha256:c43743d0…635cf` and serves v1.7.26/60b4524, and the post archive
boots v1.7.27 (`sha256:4379df49…8db`) and serves v1.7.27/24faa9d.
Scratch plaintext, extracted tars and drill containers were removed.

## 2026-11-04 v1.7.26 delivery

The v1.7.26 pre archive (`15f15879…18eb`) and post archive
(`fb4f2233…a24d`) reached the private `manual/v1726-recovery-20261104/`
directory with sidecars; `sha256sum` matches on arrival. Two isolated
restore drills pass with `--network=none`, no host ports and scratch-only
plaintext: the pre archive (v1.7.25 state) boots the v1.7.25 amd64 image
`sha256:e49861c7…5749` and serves v1.7.25/05e7fe2, and the post archive
boots v1.7.26 (`sha256:c43743d0…635cf`) and serves v1.7.26/60b4524.
Scratch plaintext, extracted tars and drill containers were removed.

## 2026-11-03 v1.7.25 delivery

The v1.7.25 pre archive (`dcb1b5a8…b503`) and post archive
(`a81b284e…f596`) reached the private `manual/v1725-recovery-20261103/`
directory with sidecars; `sha256sum -c` passes for both. Two isolated
restore drills pass with `--network=none`, no host ports and scratch-only
plaintext: the pre archive (v1.7.24 state) boots the v1.7.24 amd64 image
`sha256:15ac6807…3ff7a` and serves v1.7.24/64b8f57, and the post archive
boots v1.7.25 and serves `/api/version` v1.7.25/05e7fe2. Scratch plaintext,
extracted tars and drill containers were removed.

## 2026-11-03 v1.7.22/v1.7.23 recovery close-out

The recurring MAC-WAN SSH timeouts cleared long enough to finish the deferred
work. The v1.7.22 post archive (`30c75e1f…095d54`) and the v1.7.23 pre/post
archives (`a3126cc4…85af`, `eaec116f…2b28`) were received into the private
`manual/v1723-recovery-20261103/` directory with sidecars that reference the
actual filenames, and the two older archives also have verified `incoming/`
copies; the partial third `incoming/` upload was removed. Four isolated
restore drills pass with `--network=none`, no host ports and scratch-only
plaintext: v1.7.22 post boots the v1.7.22 amd64 image
(`sha256:fe0e46f3…831ba`), v1.7.23 pre boots both that image and v1.7.23, and
v1.7.23 post boots v1.7.23. Two earlier attempts timed out because the arm64
manifest digest was passed to MAC-WAN's amd64 Docker — an image/architecture
mismatch, not a backup defect, and the archives themselves verified on both
the `verify` path and inside the successful drills. The scheduled 03:30 UTC
receiver, its keep=14 set and the health timer were not touched.

## 2026-11-03 v1.7.24 delivery

The v1.7.24 pre archive (`65e25d3e…a8a9`) and post archive
(`3e01f766…56df`) reached the private `manual/v1724-recovery-20261103/`
directory with sidecars naming the files; both pass `sha256sum -c`. Two
isolated restore drills pass with `--network=none`, no host ports and
scratch-only plaintext: the pre archive (v1.7.23 state) boots the v1.7.24
amd64 image `sha256:15ac6807…3ff7a` with the v1.7.23→v1.7.24 DB migration
applied, and the post archive boots v1.7.24 and serves `/api/version`
v1.7.24/64b8f57. Scratch plaintext, the extracted tars and both drill
containers were removed; the decryption identity never left MAC-WAN. The
scheduled 03:30 UTC receiver and its keep=14 set were not touched.

## 2026-10-31 v1.7.19 recovery

The automatic health service succeeds at 03:35:37-47 Asia/Shanghai; OC424
receives `ok` for the scheduled set before the following manual release backups.
Pre archive `oc424-20261030T194300290955Z-e4ccf07fa200.tar.gz.age` has SHA-256
`8b0695d6d4451a1f4a633b0f7fcfb22418c43ebe7cefba3f71e687e7ced06491`; post
archive `oc424-20261030T194613732290Z-c5dab53a61d9.tar.gz.age` has SHA-256
`012b287a1f893282c590f84b0d7b18eb12bd4c8cc5561f9fa819732b2c6e49ef`.
Each was received in a new private `manual/` directory under the scheduled lock,
with matching regular `incoming/` and online executor ciphertext copies; all
14 scheduled pairs and the timer were unchanged. Pre boots pinned v1.7.18 and
v1.7.19; post boots v1.7.19 with the current 13-client digest. Post compares
21,140 archived rows across 12 histories: zero in-retention rows missing or
changed and 48 expired or inactive-tier rows absent. The identity stays on
MAC-WAN; network-free scratch plaintext and containers were removed.

The health timer checks `scheduled/`, not these manual backups. Neither manual
backup proves the next daily trigger via the new route at 11:30 Asia/Shanghai,
an independent-media restore or real alert receipt. The existing keep=14 policy
will retire the oldest eligible scheduled pair on success; its matching
`incoming/` ciphertext was independently verified. OVH disk sanitization is
also unverified.

## 2026-10-30 v1.7.18 recovery

The automatic health service runs successfully at 22:30:50-57 Asia/Shanghai;
OC424 receives `ok` before the following manual release backups. Pre archive
`oc424-20261030T143144743764Z-fd30cf6ef52b.tar.gz.age` has SHA-256
`0c2a697b0018d747aa7dd9ad87a07e22d4cc57b6854a57e695f09c2322e5d51b`; post
archive `oc424-20261030T143505907758Z-985e040a298b.tar.gz.age` has SHA-256
`13f0594e1009f485b8ff2d564fca9d241b25510ce3bb02ea71d28d00a0cf1c24`.
Each was received in a new private `manual/` directory while holding the
existing scheduled lock, with matching regular `incoming/` and online executor
ciphertext copies; the 14 scheduled pairs and timer were unchanged. Pre boots
pinned v1.7.17/v1.7.18; post boots v1.7.18 with the 13-client digest. Post
compares 21,768 archived rows across 12 histories: zero in-retention rows
missing or changed, 820 expired or inactive-tier rows absent. Identity was not
exported, and isolated scratch plaintext and containers were removed.

These manually initiated backups are not an automatic daily trigger or an
independent-media recovery. The scheduled health service checks the scheduled
directory, not these manual release archives. The next daily backup via the
new route is 2026-10-31 11:30 Asia/Shanghai and remains unverified; its existing
keep=14 policy will retire the oldest eligible scheduled pair, whose independent
`incoming/` copy was verified. Actual alert receipt and OVH disk sanitization
remain unverified.

## 2026-10-30 v1.7.17 recovery

At 17:01:20 Asia/Shanghai a manually started backup collides with the recurring
health inspection's shared lock and exits 1 without creating an archive. The
17:01 health run succeeds; the backup retry at 17:02 succeeds, as does the
17:08 post-upgrade backup. A 17:10 **manual** health run reports `ok` for the
post backup and OC424 receives it. Neither manual operation proves the next
automatic daily trigger through the repaired proxy (2026-10-31 11:30).

Pre archive `oc424-20261030T090213623348Z-54549a494a77.tar.gz.age` has SHA-256
`fccaea8fa999f555b4cdd5fca4ca754a594de846fc3c809487c1f564265631f0`; post
archive `oc424-20261030T090821042225Z-779daa8a7e4a.tar.gz.age` has SHA-256
`8ae8585c745225eedf7cb13d2fa5393c3fdd483794d91ea351fc7473271e024d`.
Separate regular `incoming/` and online executor copies match both hashes.
Pre boots pinned v1.7.16 and v1.7.17 in isolated recovery; post boots v1.7.17
with the current 13-client digest. Post compares 20,492 archived rows across
12 histories: zero in-retention rows missing or changed. The identity remains
on MAC-WAN; this is not independent-media recovery or actual alert receipt.

The scheduled directory now has 14 pairs (the existing `keep=14` limit). The
next successful receive automatically removes the oldest eligible scheduled
pair under that pre-existing policy; its `incoming/` regular copy independently
matches SHA-256 `2275911a697d18b79741cfcc1bb3b592e2e588da92bf518ffc52ab0fcd8dad82`.
No historical backup was manually deleted and the timer/retention policy is
unchanged. A manually initiated release backup can overlap the health timer: if
the exclusive receive lock is busy, allow the health check to finish and retry
without removing locks. Conversely, a health inspection while a receive holds
the lock reports deferred rather than fabricating freshness.

## 2026-10-30 v1.7.16 recovery

The automatic health timer through the pinned proxy reports success at 15:45
Asia/Shanghai and OC424 receives `ok`. The next daily backup on this route is
2026-10-31 11:30 and remains unverified; the following backups are manual.
Pre archive `oc424-20261030T075406033437Z-607bbb4c0e38.tar.gz.age` has SHA-256
`826292cab618b85f42102269fe7b70ee833735323664f5d52ce865f800721150`; post
archive `oc424-20261030T075714517609Z-e4c7a7e18e6c.tar.gz.age` has SHA-256
`36247661623647edcdc727600c621a2523fe58eb3572755d33771d2a5dbb0581`.
Separate regular `incoming/` and online executor copies match each checksum.
Pre recovers under pinned v1.7.15/v1.7.16, post under v1.7.16, with both DBs
and the 13-current-client UUID/token digest. Post compares 20,384 rows across
12 histories: no retained rows missing or changed; 46 expired rows are absent.
No network or host ports are used and scratch plaintext/container is removed.
This is not an independent-media recovery or proof of real incident delivery.

## 2026-10-30 automatic health and v1.7.15 recovery

The first actual health timer through the pinned jump fires at 14:54:00 and
finishes successfully at 14:54:07 Asia/Shanghai; OC424 receives `ok` for the
14:35 backup. The daily timer via this route has **not** yet been observed; its
next scheduled run is 2026-10-31 11:30. Two additional backups at 15:10 and
15:13 are manual, not scheduled runs.

For the OC424 v1.7.15 rollout, the pre archive
`oc424-20261030T071020075093Z-116c8230c9ea.tar.gz.age` has SHA-256
`a1f9640c944dd7289e7368be812e7faaeaffe27f69c100808dfcc5ee4f8b45bf`;
the post archive `oc424-20261030T071329235896Z-66eff1d9703b.tar.gz.age` has
SHA-256 `a15dbaccf376d543c714aafdae3e54a2ad2805b73d44d82a76c90a4b7580346b`.
Separate regular copies in `incoming/` and the online executor match each
ciphertext. The pre archive boots pinned v1.7.14 and v1.7.15 in network-free
isolated scratch stores; the post boots v1.7.15 and compares 12 selected
histories with zero missing in-retention or changed archived rows. Both DBs
and the 13 current UUID/token digest verify; no restore plaintext or container
remains. At 15:15 a manual health push reaches OC424 with the post-backup
timestamp and `ok`. These online copies are not independent media, and actual
alert delivery remains unverified. Release/rollback details are in
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md).

## 2026-10-30 interruption and pinned proxy repair

The 11:30 Asia/Shanghai scheduled backup fails; health reports fail from 10:05.
The prior MAC-WAN public egress was outside the two keys' source restriction. At
12:56, `203.218.171.130` is added to those two forced-command keys only; a manual
12:56 backup and health report work, and the then-current 12-client archive
boots v1.7.14 in isolation. The owner confirms the deliberate deletion. The owner
then adds `QWRT 主路由 | 宿舍`; the resulting 13-client set is different.

New direct MAC-WAN-to-OC424 SSH connections subsequently terminate before
server identification. A bounded OC424 packet capture sees no new SYN from the
reported direct egress during one successful client-side TCP connection; this
does not identify which network device terminates it. The existing OC424 alias
uses the owner's MegaBox jump. A separate fixed config, with strictly pinned
OC424 and MegaBox host keys and no agent forwarding, directs only the backup
and health scripts through that jump. The actual jump exit `144.34.224.86` was
added to the two existing `restrict`/forced-command keys, not a wildcard; all
other authorization bytes, including `bero-backup-pull`, remain unchanged.
This adds a dependency and expands those keys' permitted source to the shared
MegaBox exit; the jump sees encrypted SSH but no backup identity or plaintext.
Snapshot authorization material is root-private in
`/root/nekomari-macwan-source-20261030-hrlhqkke` and
`/root/nekomari-macwan-proxy-source-20261030-dzwht697`. Previous MAC-WAN scripts
are at `~/.local/share/nekomari-recovery/macwan-backup-proxy-20261030-vgkoxp_u`.
Do not restore whole authorization snapshots over unrelated edits.

The manual 14:35:09-14:36:24 backup succeeds through the proxy.
`oc424-20261030T063509678495Z-c86b90219196.tar.gz.age` is 191250046 bytes,
SHA-256 `94ae94f950318996d760b66ccf97d5e1803b775cc492676ca00383c6a3820854`.
Its separate `incoming/` copy and executor copy match; the executor is online,
not independent media. Isolated pinned-v1.7.14 recovery validates both DBs and
the 13 current UUID/token hashes, with network disabled and no scratch plaintext
remaining. The initiating SSH observation was lost, but systemd/journal and the
archive were checked later through the pre-existing pinned reverse tunnel. A
manual 14:38:39 health run succeeds and OC424 receives the matching fresh `ok`.
MAC-WAN Agent/Hermes and panel remain running; no node, credential, schedule or
retention change. At this checkpoint the first actual automatic health run on
the new route and tomorrow's daily run were not yet observed; see the newer
checkpoint above for the subsequent health timer. Real alert dispatch was
logged but receipt remains unverified.

## Initial evidence

- 2026-10-28 23:44 Asia/Shanghai: the user service successfully pulls ciphertext
  `oc424-20261028T154414282891Z-46156c63199b.tar.gz.age` (186863796 bytes,
  SHA-256 `2275911a697d18b79741cfcc1bb3b592e2e588da92bf518ffc52ab0fcd8dad82`).
- The separate regular MAC-WAN `incoming/` copy matches. After the owner confirms
  intentional OVH node removal, isolated exact-v1.7.12 recovery passes both DB
  integrity checks, current 13 UUID/token comparison, scoped-key schema,
  uptime/panel rollups and networkless/no-host-port boot and cleanup. The prior
  attempt with the obsolete 14-node assertion fails before boot and is retained.
- The executor holds another hash-matching encrypted copy, outside automated
  retention. It is a separate online host, not offline or independent-media
  recovery. The age identity never leaves MAC-WAN during either copy or drill.
- The first manual health push is denied because the MAC-WAN egress changed
  from `218.102.124.1` to `82.152.161.202`. Both source addresses are independently
  observed through the pinned OC424 connection before being added. Retry succeeds
  and the enabled health timer fires automatically at 23:47 Asia/Shanghai. A
  later NAT change deliberately fails closed; a missing report is an incident,
  not a reason to silently widen source access or forge freshness.
- Existing OC424 backup receiver/wrapper/age hashes match the established
  installation. Each new SSH key is `restrict`, source-address-limited and
  forced to exactly one sudo command with no caller-supplied arguments. The
  health wrapper writes the existing `backup-health/ovh.json` path solely for
  compatibility; it now represents MAC-WAN observations, not OVH availability.
  Both keys are generated and stored on MAC-WAN; no private key is exported.
- OC424's root-private authorization snapshots are
  `/root/nekomari-macwan-backup-20261028-0mrz4m5q` and
  `/root/nekomari-macwan-source-20261028-3wdpo4y0`. Before rollback, compare the
  current exact authorization bytes and preserve unrelated edits. Never restore
  previously revoked OVH entries or remove recovery material automatically.

## 2026-10-29 source-address repair and v1.7.13 backups

- At approximately 09:28 Asia/Shanghai the MAC-WAN backup-health push had
  stopped because the observed egress had changed again. A pinned, actual SSH
  connection established `218.102.124.113`; only the two existing restricted,
  forced-command backup/health keys gained that exact source. The previously
  observed `218.102.124.1` and `82.152.161.202` remain. No wildcard was added;
  all other authorization bytes were preserved. In particular, the owner
  confirmed the independent `bero-backup-pull` key was installed by Hermes at
  their request and it remains untouched. The prior authorization snapshot is
  `/root/nekomari-macwan-source-20261029-d9dkhawf` on OC424. Manual retry and
  the 09:45 scheduled health trigger succeed; source changes may recur.
- Release pre-backup `oc424-20261029T012849480670Z-cc47ba08087f.tar.gz.age`
  (SHA-256 `8f7e74c9cf34e86ea8331113b38efd71403773c9dc93cbf6eef4a64cd7defb0d`)
  boots the pinned v1.7.12 and v1.7.13 images in isolated MAC-WAN recovery.
  Post-backup `oc424-20261029T014717511121Z-e6adfad8b8b8.tar.gz.age`
  (SHA-256 `f08278b5a537d449ad8e56be790f51ccc1c7a2119f257b7079db4e5044f89ab3`)
  later boots the pinned v1.7.13 image. Both archives have separate matching
  ciphertext copies on the executor, outside the automatic retention set;
  this executor is online and is not independent offline media. The MAC-WAN
  online age identity was not exported. See the rollout record for the first
  failed post-boot assertion and the scope of the subsequent comparisons.
- The 09:47 manually started backup and 09:48 health report were not a daily
  timer test; the actual scheduled trigger is recorded below.

## v1.7.14 release backups (2026-10-29)

- The 11:05 manually started pre-backup
  `oc424-20261029T030508182324Z-d429f2f81679.tar.gz.age` has SHA-256
  `af6c5f6b82244704d948f01896b9ac0e4ba85ed9e0dec4a235188d1dc90d317f`.
  It boots pinned v1.7.13 and v1.7.14 in isolated MAC-WAN recovery with both
  databases and the current 13 UUID/token hashes.
- The 11:13 manually started post-backup
  `oc424-20261029T031324144179Z-24ec1c83091d.tar.gz.age` has SHA-256
  `70fa15e0e06a82522a5070e6a29d0d29cdcb47f81ec16af6f29e46f5a03ef3aa`.
  It boots pinned v1.7.14 in isolation. A retention-aware comparison of 12
  metric series finds no missing in-window or changed archived rollups; 1,984
  expired or inactive-tier rows are absent after startup cleanup. This does
  not establish the cause of the earlier v1.7.13 assertion failure.
- Both archive/checksum pairs have regular MAC-WAN `incoming/` copies outside
  the scheduled retention set and matching ciphertext copies on the online
  executor. The age identity remains on MAC-WAN; no independent-media restore
  is claimed. These are manual backups, not evidence of a scheduled trigger.

## First scheduled daily backup (2026-10-29)

- The MAC-WAN timer actually triggers at 11:30:23 Asia/Shanghai. Its backup
  service starts at 11:30:23 and completes successfully at 11:31:00, exit 0.
  The journal identifies `oc424-20261029T033023889469Z-1c83ce6b7922.tar.gz.age`
  (187995679 bytes); its ciphertext header, checksum sidecar and independently
  computed SHA-256 `28d7954ef6e3eee0161289c5db401c74dc388ee887b44165610d61156ccbe5a5`
  match. This was the timer, not a manual service start.
- Initial evidence collection had an assertion mismatch against systemd's
  actual journal wording; the preserved log and corrected read-only check
  verify the same trigger and file. This observes one scheduled backup, not a
  restore of that archive, guaranteed RPO or independent-media recovery.
- The separate health timer runs at 11:34:20 and finishes at 11:34:23,
  exit 0. OC424 receives `ok` at 11:34:23 with `backup_at` matching the
  scheduled archive's 11:30:23.889469 timestamp. This verifies actual success
  reporting, not delivery of a real failure notification.

## Operations

The installed scripts and user units correspond to `deploy/hosts/mac-wan-*`
and `deploy/hosts/nekomari-macwan-oc424-*`; tests are
`deploy/macwan_backup_schedule_test.py`. Check state with:

```sh
systemctl --user status nekomari-macwan-oc424-backup.timer nekomari-macwan-oc424-health.timer
journalctl --user -u nekomari-macwan-oc424-backup.service -u nekomari-macwan-oc424-health.service --since '24 hours ago'
```

The report verifies ciphertext format/checksum and freshness, not age
authentication, independent-media recovery or guaranteed RPO/RTO. Each release
still needs a fresh verified pre/post backup, independent regular copy, isolated
recovery and production continuity evidence. MAC-WAN combines ciphertext and
an unencrypted online age identity under the same UID as Hermes; same-UID,
root and Docker administrators are trusted. A lost/changed external address,
home Internet outage, shutdown, full disk or broken user manager can interrupt
the schedule. No OC424/Agent/Hermes service is restarted or token rotated here.
Legacy OVH plaintext exposure remains unresolved until the owner confirms disk
sanitization and decides on coordinated credential rotation.
