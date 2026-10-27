# MAC-WAN backup custody and recovery (2026-10-27, Asia/Shanghai)

MAC-WAN is the owner-selected Hermes and online identity/recovery host (Linux
x86_64, user `macos`). OC424 now serves v1.7.0/8eb711e; OVH (`KSLE`) stores
nightly ciphertext. Actual fresh 13-node pre/post-cutover old/new-image recovery
passes. The owner reports downloading the offline identity copy and authorizes
cutover; **independent-media recovery is not claimed**. All existing readable
plaintext/manual/local rollback copies are retained; no deletion policy is
inferred from downloading a key.

## Custody and access boundary

| Host | Installed material / access |
|---|---|
| OC424 | Public `/etc/nekomari/backup.recipients`; checksum-matched backup tool and native arm64 age; fixed, restricted ciphertext producer; no recovery identity |
| OVH | `/srv/backups/oc424-encrypted` (`0700`), ciphertext/checksums (`0600`); a dedicated SSH pull key generated and retained on OVH, not a decryption identity |
| MAC-WAN | `/home/macos/.local/share/nekomari-recovery` (`0700`); `identities/oc424-primary.agekey` (`0600`), public recipient, tools, ciphertext and local recovery instructions |
| Independent offline copy | **Downloaded per owner report**; derived-recipient comparison/access instructions/actual independent-media recovery not independently verified. A second online MAC-WAN copy is not an offline drill |

The private identity is generated **on MAC-WAN**, not in the workspace, OC424 or
OVH. Only its public recipient leaves that host. No private identity is placed in
Git, evidence, chat, command arguments or logs. SSH host checking stays strict;
the previously recorded MAC-WAN key on trusted OC424 pins the connection:
`SHA256:uS7MlNlA1Rowjf7XFkr/8HwT7EaX1t+AhNO8Uoc2scE` (ED25519).

Public recipient:

```text
age1exnfzzc9del4hv6k47tp3uhjpxepqcqftpqjdw5sv6dw0ymraymsccvp09
```

SHA-256 of the public recipient file, including its final newline:
`c2e3a6e5fe5cc237e258794cd3dbb1be04b4b6af5a316419d4cb88f27a6d3b54`.
This verifies the operator's SSH-pinned copies, not owner out-of-band approval.

The online identity is **not passphrase-encrypted**. Mode `0700`/`0600` protects
against other ordinary users, not the same UID, root or Docker administrators.
Hermes runs under the same user; a compromise there could expose both the API
credential and recovery identity. Docker access is privileged. MAC-WAN is an
additional sensitive online host, not an offline vault or an untrusted-agent
sandbox. No unattended decrypt/restore scheduler is enabled.

## Installed producer, receiver and nightly schedule

The source and receiver use the unchanged `deploy/encrypted_backup.py`, SHA-256
`0e240418c853ce65cfb2d353c8be55ea2c4853cd3511fad57856d742fd737e78`, installed
at `/usr/local/lib/nekomari/encrypted_backup.py`. MAC-WAN has the same bytes
under its recovery directory. Python dependencies are standard library only.

Static age/age-keygen builds use sum.golang.org-verified `filippo.io/age@v1.2.1`
(upstream `482cf6fc9babd3ab06f6606762aac10447222201`), Go1.26.9, CGO disabled.
No repository dependency/lockfile or system package is changed.

| Executable | SHA-256 |
|---|---|
| MAC-WAN `bin/age` | `cf9fc5d84832380bc9b97f096e16b6c8e563836ed87f715505e63b8c8f7644ab` |
| MAC-WAN `bin/age-keygen` | `ebaffce52fa796ea318e51673da1bcc4a4a8d7923dec9441bd38a568544be263` |
| OC424 `/usr/local/lib/nekomari/bin/age` | `dc5cef05867e7694c6c4e9acd944e9e08a9178402e05383e77a34f509301bca1` |

OC424's `/usr/local/sbin/nekomari-encrypted-backup-stream` has fixed source,
compose and recipient paths, and no arguments. Source snapshots use private
per-run `/dev/shm/nekomari-encrypted-snapshot.*` scratch, removed on exit. The
captured SQLite metrics DSN is `./data/metrics.db`, resolving under
`/opt/nekomari` to the actual supplied file. Expanded/ciphertext limits are
2GiB/1GiB; producer/receiver timeouts are240s/300s. Per-DB snapshots are WAL-safe,
not an atomic cross-DB/filesystem transaction.
Its exact source is `deploy/hosts/oc424-encrypted-backup-stream.sh`; the OVH
wrapper is `deploy/hosts/ovh-receive-oc424-encrypted-backup.sh`. These are
host-specific, not universal installation defaults. Installing the files alone
does not authorize SSH access or enable a schedule.

The dedicated OVH public pull key is appended without replacing existing
authorizations. It is limited to OVH's source address, `restrict`, and one fixed
forced command. The associated sudo rule permits only that root-owned wrapper
with **empty arguments**, not general Python/shell access. Original SSH
commands and caller-selected paths are never evaluated. The root-private
pre-append authorization copy remains on OC424 for reversal.
The existing `ubuntu` account already has broader sudo rights; the additional
rule does not revoke them. The new pull key's boundary is the forced command
and the wrapper's own zero-argument check, not a claim to have restricted every
other credential for that account. An unexpected wrapper argument exits64
before reading production data.

OVH's `/usr/local/sbin/receive-oc424-encrypted-backup` uses its dedicated
`/etc/nekomari-backup/oc424-encrypted-pull` key and pinned OC424 host keys.
It runs the checked receiver with prefix `oc424`, keep 14, no shell producer or
unchecked stdin. Existing backup keys/wrappers remain unchanged. The receiver
now runs through the OVH encrypted service/timer; only the legacy backup cron
row is removed.
It cannot authenticate/decrypt the archive without MAC-WAN.

Installed wrapper SHA-256 values are
`569d328d31408a95e667eaad4b345d330d97bc27691841d08a420899a9fd09da`
(OC424 producer) and
`52d8297c2a2b1c5d7faf1bcff3370eb9a6e2affacc1e6b6c3a549fcb19e5e66f`
(OVH receiver). Neither evaluates `SSH_ORIGINAL_COMMAND`; fixed paths and argv
are the privileged trust boundary.

## Earlier real canary (before cutover)

| Evidence | Result |
|---|---|
| Creation | 2026-10-27 10:59:54 Asia/Shanghai, actual OC424 v1.6.29 data/selected assets/compose, encrypted before transmission |
| OVH archive | `/srv/backups/oc424-encrypted/oc424-20261027T025948470471Z-15da04ac5917.tar.gz.age` plus `.sha256` |
| Size / SHA-256 | 183095002bytes; `f8db808750647fafdb229824a016031426d9a77c0153d6b1b0881aaad0a6cf50` |
| MAC-WAN copy | Same basename under `incoming/`; encrypted transfer size/hash matches; private identity never exported |
| Verification | Native age authentication through EOF, all1012 listed hashes/sizes, bounded manifest/USTAR and both SQLite integrity checks pass |
| Actual restore | MAC-WAN boots installed v1.6.29 in local Docker with no network/host ports, read-only rootfs, non-root UID, resource limits and only disposable data mounted |
| Image | `ghcr.io/aone2233/nekomari:v1.6.29@sha256:db0381c4faa97c5f39fe2534a7709eb6f72bd6ae5cb67220d1fb8f89c5a27893`; amd64, source label `510f7d354786f373e5816fa72b295f56d3ec3036` |
| Cleanup | Verify + drill41.21s; no plaintext scratch or restore container remains; ciphertext, identity and instructions retained |
| Continuity | Panel still healthy on v1.6.29, unchanged start time; Hermes PID251037/restart0, MAC-WAN AgentPID976/restart0, OC424 AgentPID499631/restarts2 unchanged |

The digest/source label and binary `--help` are checked separately; the drill
itself checks the reported version and public configuration, not the hash or
authenticated UI. This is an actual **online custody** recovery, not an
independent offline-copy drill, fleet test, production RPO/RTO measurement or
proof of automated freshness. Temporary plaintext uses tmpfs; cleanup is not
secure erasure and swap/system snapshots remain part of the host threat model.

## v1.7 cutover recovery evidence

| Evidence | Pre-migration | Post-migration / first installed service run |
|---|---|---|
| Archive basename | `oc424-20261027T043120813844Z-2b48d4a466c6.tar.gz.age` | `oc424-20261027T044833717100Z-7c911d43f0a3.tar.gz.age` |
| Bytes / SHA256 | 182893531; `0577eed95c9eaa073044e26097ab789b37d0915606221aa3a1d433ceda3207d6` | 182862307; `618e8b80e9f5f68885cad452e77887c99d6395868805eb514f6202072f0532f0` |
| Restored image/source | v1.6.29/510f7d3, existing pinned amd64 digest `sha256:db0381c4faa97c5f39fe2534a7709eb6f72bd6ae5cb67220d1fb8f89c5a27893` | v1.7.0/8eb711e, amd64 digest `sha256:66ca7338cd5e2237bc4456e52260f2fd9b3559434abacb86d0627a485871aaa7` |
| Actual restore | First boot timeout retained, retry passes in 36.39s | Passes in 70.72s |
| Metadata/data | Both DB integrity,13 clients, old auth schema/no scoped-key table | Both DB integrity,13 clients, migrated auth schema/marker, no legacy plaintext config row |
| Isolation/cleanup | Networknone/noports, readonly root/nonroot/bounded resources; no scratch plaintext/container remains | Same isolation/cleanup; identity never mounted in Panel |

Both pairs live under `/srv/backups/oc424-encrypted` on OVH and recovery
`incoming/` on MAC-WAN. OVH also retains them under
`/srv/backups/oc424-encrypted/manual-v1.7.0-20261027`, outside top-level 14-pair
pruning (hardlinks, not duplicate data blocks). Native image source and packaged
binary checksums/attestations are verified separately; the recovery harness
also checks exact reported version/hash. Never use an old 14-client backup for
this cutover rollback: the owner intentionally deleted one client.

OVH's enabled persistent `nekomari-oc424-encrypted-backup.timer` runs daily
03:30 UTC (11:30 Asia/Shanghai); the installed service has already succeeded.
Only its predecessor plaintext cron row is removed, leaving other rows/script/
keys unchanged. Encrypted keep 14 does not prune legacy plaintext or manual
release copies. Schedule before/after/rollback evidence is root-private at
`/root/nekomari-backup-schedule-v1.7-20261027T044832Z-07b722` on OVH.
This is scheduled ciphertext reception plus manual real recovery, **not** a
scheduled decryption drill, independent-media recovery or measured RPO/RTO/SLO.

Hermes gateway is stopped during the Panel switch and resumed afterward with
3 known references redirected to the metadata client. It is nowPID922780/
restart0; AgentPID976/restart0 is unchanged. No key is configured yet and node
writes wait for owner issuance/private TTY configure/disposable live acceptance.

## Manual recovery and remaining gates

On MAC-WAN, the private recovery directory contains `README.md` with the exact
manual drill command. It uses a fresh `0700` tmpfs directory, the retained
ciphertext/sidecar, local identity and the pinned old image. Only scratch settings
are changed to disable production notifications/OAuth/GeoIP. The private identity
is never mounted inside the restored Panel. Do not blindly execute archived
compose or point recovered metrics paths at production.

The owner reports the **independent protected offline copy downloaded** and
authorizes cutover. Compare the copy's derived public recipient using
`age-keygen -y <offline-copy-path>` without sending its private contents.
Matching public material checks a key copy, not a full offline-media recovery;
record an actual drill separately. Keep old identities while matching archives
are retained. Online MAC-WAN restore does not prove independent-media recovery.

Nightly encryption is switched only after fresh actual online recovery;
**all old readable plaintext/manual/local rollback copies remain**. Explicit
deletion/longer-term retention still needs approval. Reverting the scheduler
or removing a recipient does not decrypt/revoke old archives. Observe later
scheduled success; freshness alerts/periodic drill policy are separate work.

The release is deployed, with authenticated owner UI acceptance and Hermes's
actual dedicated-key/live workflow handoff still pending. Agent-token read is
a separate unapproved fleet-wide grant; existing Agent tokens/services remain
unchanged. See [`API-KEYS.md`](./API-KEYS.md) and
[`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md).
