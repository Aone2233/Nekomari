# MAC-WAN backup custody and recovery (2026-10-27, Asia/Shanghai)

The owner designates SSH alias **MAC-WAN** as both the Hermes host and the
backup-identity custodian/recovery runner. This is Linux x86_64, user `macos`,
not macOS. OC424 remains the production Panel; OVH (`KSLE`) stores ciphertext.
This record covers an actual encrypted canary, **not a v1.7 deployment or
independent offline-escrow acceptance**.

## Custody and access boundary

| Host | Installed material / access |
|---|---|
| OC424 | Public `/etc/nekomari/backup.recipients`; checksum-matched backup tool and native arm64 age; fixed, restricted ciphertext producer; no recovery identity |
| OVH | `/srv/backups/oc424-encrypted` (`0700`), ciphertext/checksums (`0600`); a dedicated SSH pull key generated and retained on OVH, not a decryption identity |
| MAC-WAN | `/home/macos/.local/share/nekomari-recovery` (`0700`); `identities/oc424-primary.agekey` (`0600`), public recipient, tools, ciphertext and local recovery instructions |
| Independent offline copy | **Pending** owner-controlled media, access instructions and recovery verification; a second copy on MAC-WAN does not qualify |

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

## Installed, opt-in producer and receiver

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
It runs the checked receiver with prefix `oc424`, keep14, no shell producer or
unchecked stdin. Existing backup keys/wrappers/schedulers are unchanged.
This new receiver is **manual/opt-in**, not the nightly production schedule.
It cannot authenticate/decrypt the archive without MAC-WAN.

Installed wrapper SHA-256 values are
`569d328d31408a95e667eaad4b345d330d97bc27691841d08a420899a9fd09da`
(OC424 producer) and
`52d8297c2a2b1c5d7faf1bcff3370eb9a6e2affacc1e6b6c3a549fcb19e5e66f`
(OVH receiver). Neither evaluates `SSH_ORIGINAL_COMMAND`; fixed paths and argv
are the privileged trust boundary.

## Completed real canary

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

## Manual recovery and remaining gates

On MAC-WAN, the private recovery directory contains `README.md` with the exact
manual drill command. It uses a fresh `0700` tmpfs directory, the retained
ciphertext/sidecar, local identity and the pinned old image. Only scratch settings
are changed to disable production notifications/OAuth/GeoIP. The private identity
is never mounted inside the restored Panel. Do not blindly execute archived
compose or point recovered metrics paths at production.

The owner still needs an **independent protected offline copy** of the identity
and access/recovery instructions, transferred directly from MAC-WAN through an
approved private channel. Compare the copy's derived public recipient with the
value above using `age-keygen -y <offline-copy-path>`; do not print/send its
private contents. Matching public material verifies a key copy, not a full
offline-media recovery; record the latter separately when performed. Keep old
identities while matching archives are retained.

Before switching the nightly schedule: verify independent recovery, review
freshness/retention and explicitly select which old readable plaintext copies
remain. **No old backup or rollback copy is deleted here.** Reverting the
producer/receiver does not decrypt ciphertext. Removing a public recipient
cannot revoke its access to old archives.

Full v1.7 also needs Hermes's write pause, exact consumer/configuration changes,
owner-session scoped-key issuance/secure handoff and a bounded live check before
resume. Agent-token read is a separate fleet-wide grant, absent by default.
The owner will perform authenticated UI acceptance after deployment; existing
Agent tokens/services remain unchanged. See [`API-KEYS.md`](./API-KEYS.md) and
[`V1.7-CUTOVER-READINESS.md`](./V1.7-CUTOVER-READINESS.md).
