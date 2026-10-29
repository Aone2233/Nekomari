# MAC-WAN backup receiver (updated 2026-10-29)

This replaces the relinquished OVH receiver, not the separate offline-media
requirement. On MAC-WAN, the `macos` systemd user manager has lingering enabled.
The daily `nekomari-macwan-oc424-backup.timer` starts the bounded encrypted
receiver at 03:30 UTC (11:30 Asia/Shanghai), with persistent catch-up; its first
scheduled trigger is still pending. A separate 15-minute health timer checks
the newest ciphertext and pushes the non-secret status to the existing OC424
watcher. The receiver keeps 14 scheduled pairs in a dedicated directory;
`incoming/`, the initial executor copy and prior release copies are not in that
retention set. The first scheduled trigger and actual incident delivery must be
observed independently; a manual success is not proof of those events.

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
- The 09:47 manually started backup and 09:48 health report are not a daily
  timer test. The first daily timer remains scheduled for 11:30 Asia/Shanghai
  on 2026-10-29 and must be observed separately.

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
