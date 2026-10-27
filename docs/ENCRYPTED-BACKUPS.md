# Encrypted off-host backups (v1.7 preparation, unreleased)

This is a bounded, opt-in tooling batch on token-rotation merge `31a5b43`.
PR84 is source-merged as `f567128` after exact candidate `5899614` passes
CI40426994745 (7/7). Source delivery is not production rollout.
**The OC424/OVH backup schedule still writes the existing plaintext archives.**
No production key, forced command, scheduler, retention setting, credential or
Panel/Agent service is changed by this source work. OC424 remains v1.6.29/510f7d3.
See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md) for the retained recovery copies.

## Contract and dependencies

`deploy/encrypted_backup.py` requires Linux, Python 3.10+ and native `age` /
`age-keygen` (tested with age 1.2.1). Python dependencies are standard library
only. `drill` also needs local Docker and a trusted, already-installed,
digest-pinned image containing `/app/nekomari` and `curl`. A local Unix-socket
Docker endpoint is checked **before decryption**; remote/unknown contexts and
SSH/TCP endpoints, including loopback TCP, fail closed.

The new format is `nekomari-offhost-v1`: gzip/USTAR inside native age X25519
encryption, with a `*.tar.gz.age` archive and `.sha256` sidecar. It is not the
Panel's UI ZIP format or the old off-host `*.tar.zst` format.

| Location | Allowed material | Not allowed |
|---|---|---|
| Source/Panel host | Public recipients, restricted pull authorization, source data, private snapshot scratch | Restore private identity |
| OVH receiver | Dedicated restricted SSH pull key, ciphertext/checksums | Restore private identity or decrypted archive |
| Approved restore runner | Temporarily unlocked identity, ciphertext, private scratch, trusted image | Restored Panel reaching production networks |
| Independent escrow | Recovery identity and documented custodians/access procedure | Sole readable key on either backup host |

Age authenticates ciphertext during **decryption**; anyone with a public
recipient can encrypt a replacement archive. Checksums detect accidental
damage, not a malicious writer replacing both files. The receiver trusts its
restricted producer and checks successful exit, native-age header, minimum
size and ciphertext byte limit. It cannot prove decryptability without an
identity. A successful receive is not restore, freshness or RPO/RTO evidence.
Signatures, replay prevention and external freshness alerts are not added here.

Only 1-16 native X25519 recipients/identities per file are supported. Password,
SSH-key, plugin/hardware identities and interactive prompting are unsupported.
The tool does not generate production recovery keys.

## D12: Approved architecture and pending implementation

On2026-10-27 the owner approves the recommended architecture: OC424 holds only
public recipients, OVH holds ciphertext, and the owner controls the private
identity with an independent offline recovery copy. An approved separate restore
environment temporarily unlocks it. No actual personal device, recipient,
offline escrow copy or restore runner is supplied or verified yet. Do not
interpret design approval as deployed custody or permission to generate and keep
a production private identity in the development workspace/OC424/OVH.

Implement the approved design before changing the production backup schedule:

1. Name the primary and independent recovery custodians, escrow locations and
   restore approvers and actual owner-controlled device. Verify public recipient
   fingerprints out of band; transfer public material only to OC424.
2. Put **only public recipients** on OC424; keep identities off OC424/OVH.
   Multiple recipients provide independent decryption, not threshold approval.
3. Authorize a separate restore runner and how it temporarily unlocks an
   identity. Fully offline keys cannot also support autonomous restore drills;
   approve the actual exposure/approval policy rather than putting a key on OVH.
4. Agree which old plaintext copies remain and for how long. Keep known-good
   recovery copies until an actual escrow-path restore passes; source tests do
   not authorize deleting them.

Record the real arrangements, then run one off-host encrypted canary and a restore via
the actual approved recovery path before switching the existing schedule.
Until that evidence exists, v1.7's encrypted-backup exit criterion stays open.

Adding a new public recipient affects future archives only. Verify a new backup
with the new identity before removing a retired public recipient. Keep old
identities for retained archives: removing a recipient cannot revoke access to
existing ciphertext. Re-encryption/deletion needs a separate verified migration.

## Source: Snapshot and encrypt

These commands are **templates**, not executed production configuration.
Install the script at a controlled path and prepare a dedicated `0700` work
directory owned by the snapshot user. Use encrypted storage or sufficiently
large tmpfs according to the threat model.

```bash
python3 /usr/local/lib/nekomari/encrypted_backup.py create \
  --data-dir /opt/nekomari/data \
  --panel-dir /opt/nekomari \
  --compose /opt/nekomari/docker-compose.yml \
  --recipients-file /etc/nekomari/backup.recipients \
  --work-dir /var/lib/nekomari-backup-scratch \
  --output -
```

Use a **fixed** command in the existing restricted SSH forced-command wrapper.
Never evaluate `SSH_ORIGINAL_COMMAND`, accept caller-selected paths or grant
a general Python/sudo shell. Preserve `restrict` and the dedicated pull key.
Source authorization needs read access to DBs/assets, not a restore identity
or access to the receiver. Stdout carries only ciphertext; diagnostics use
stderr without raw child/parser diagnostics or credentials.

- Both SQLite databases are mandatory. The online backup API captures committed
  WAL pages, followed by full `PRAGMA integrity_check` on each snapshot. Source
  transaction data is not rewritten; SQLite readers may update SHM bookkeeping.
- The **captured main DB's** metrics driver/DSN must identify the supplied local
  SQLite file before metrics are read. Non-SQLite, in-memory, absent, corrupt or
  mismatched sources fail. `--database`, `--metrics-database` and `--panel-dir`
  support actual custom paths/relative DSNs; container absolute paths must be
  accessible in the snapshot namespace. No host/container mapping is guessed.
- Selected assets are `favicon.ico`, `font.ttf`, `theme/`, `plugin/`,
  `plugin-data/`, `backup/upgrade-*.zip` (as `upgrade-archives/`) and optional
  compose. WAL/SHM files, arbitrary siblings, logs and upload fragments are not
  archived. DB-resident secrets, filenames and the manifest are encrypted.
- The two DBs are individually consistent, **not one cross-DB/filesystem
  transaction**. Avoid simultaneous theme/plugin/config changes when coordinated
  recovery matters; this tool does not provide global point-in-time replay.
- Temporary plaintext snapshots live only in private source scratch. No plaintext
  tar is written or sent. Exceptions/timeouts/catchable termination clean scratch;
  SIGKILL/power loss may leave orphans. Inspect before reclaiming them. Cleanup
  is not secure erasure, and swap/filesystem snapshots can retain plaintext.

For a local encrypted file use a **new** safe basename, for example
`--output /private/backups/snapshot.tar.gz.age`. Exclusive publication creates
mode-0600 archive/checksum files without overwriting either name.

## Receiver: Publish, then retain

Prepare a dedicated `0700` destination owned by the receiving user. Provision
restricted SSH credentials and host-key pinning separately, not as inline secrets.

```bash
python3 /usr/local/lib/nekomari/encrypted_backup.py receive \
  --output-dir /srv/backups/oc424-encrypted \
  --prefix oc424 --keep 14 --timeout 300 \
  -- ssh -T -o BatchMode=yes -o StrictHostKeyChecking=yes \
     -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=2 \
     oc424-backup
```

The producer is an argv command, never shell text or unchecked stdin. The tool
locks the destination, stages only ciphertext, waits for successful producer
exit, fsyncs and exclusively publishes the archive/sidecar. Failed, interrupted,
oversized or plaintext producers do not start retention. No scheduler is enabled.

Retention recognizes only complete
`PREFIX-YYYYMMDDTHHMMSSffffffZ-<12 hex>.tar.gz.age` pairs. It reserves one slot
for the newly received archive before choosing remaining survivors by descending
filename. Clock rollback, a future-dated predecessor or nonce order cannot prune
the newly received copy while reporting success. **All selected survivors**
must pass ciphertext checksum checks before any old pair is deleted. Orphans,
other prefixes and legacy plaintext archives remain untouched. Retention failure
returns nonzero and keeps the newly published archive for inspection.

This is not decryption verification, authenticated freshness, a persistent
receive-order ledger or crash-atomic publication/deletion across multiple files.
Do not delete known-good legacy backups because a receive returns zero.

Defaults: 16 GiB ciphertext, 16 GiB expanded payloads, 10,000 filesystem entries,
2 MiB manifest and 300-second snapshot/receive/verify timeout. Configure
`--max-archive-bytes`, `--max-expanded-bytes` and `--timeout` for the host. Budget
source scratch, ciphertext and restore scratch space. Review scheduler logs and
exit status; this batch installs no notifications. Producer/Docker arguments
are trusted operator inputs, not an untrusted-code sandbox.

## Verify, extract and restore drill

Copy both ciphertext and sidecar to the approved runner. Prepare private `0700`
scratch and a `0600` identity owned by that user. Use local Docker, not a remote
daemon/context; socket proxies remain within the trusted operator boundary.

```bash
python3 deploy/encrypted_backup.py verify \
  --archive /private/incoming/snapshot.tar.gz.age \
  --identity-file /run/secrets/nekomari-restore.agekey \
  --work-dir /private/restore-scratch

python3 deploy/encrypted_backup.py drill \
  --archive /private/incoming/snapshot.tar.gz.age \
  --identity-file /run/secrets/nekomari-restore.agekey \
  --work-dir /private/restore-scratch \
  --panel-image 'ghcr.io/aone2233/nekomari@sha256:<approved-64-hex-digest>' \
  --expected-version v1.7.0 --boot-timeout 90
```

`verify` checks ciphertext SHA-256, age authentication through EOF, bounded
manifest, strict USTAR names/types, every listed hash/size, no unlisted/trailing
content and full integrity of both DBs. No `tar.extract*` is used; traversal,
links/devices/PAX and oversized content fail. Temporary plaintext is removed
before return. Wrong keys/damaged archives cannot publish a restore directory.

`drill` first verifies, then requires an installed account and rewrites **only
scratch** metrics paths/driver, disabling notifications, OAuth and GeoIP. It
boots the already-installed digest/image-ID with `--pull=never`, `--network=none`,
no host ports, read-only rootfs, dropped capabilities, no-new-privileges and
CPU/memory/PID limits. A random named container mounts only scratch, runs as
the invoking UID/GID and answers internal `/api/public` and `/api/version`
checks; the latter must match. Cleanup removes the container and plaintext on
success/failure. A cleanup failure is nonzero and requires inspection.

Plugins remain trusted executable code; Docker and its privileged access are
not proof malicious plugins are safe. The drill does not show fleet reconnection,
authenticated UI correctness, production escrow recovery or monthly RPO/RTO.
Schedule only on the approved runner; record archive/image digest, version,
duration and acknowledgement without secrets.

`extract` accepts the same archive/identity/work arguments with
`--destination /private/recovered-data`, a **new** directory under a private
parent. It never overwrites live data or starts/stops services. Before actual
restore, preserve current data, stop the Panel, review stored paths/compose,
map `upgrade-archives/` where needed and install with correct ownership. Never
blindly execute restored compose. Restoring an old auth DB resurrects its old
credentials/revocations; pair it with the old binary and repeat incident rotation.
See [`API-KEYS.md`](./API-KEYS.md) and
[`CLIENT-TOKEN-ROTATION.md`](./CLIENT-TOKEN-ROTATION.md).

## Bounded evidence (2026-10-27, Asia/Shanghai)

- Local real-age/WAL tests pass **22/22 groups**, with ResourceWarnings treated
  as errors: multi-recipient recovery, snapshot/config binding, corruption,
  traversal/link/PAX/size refusals, no overwrite, locks, interruption cleanup,
  fail-safe exact-prefix retention and local-Docker enforcement.
- A real installed synthetic Panel from existing smoke image
  `sha256:75b210579883a67396eed898d20f06299b05c039f31d7475a4c8087884769808`
  passes producer/receiver retention, DB verification and offline Docker restore.
  Wrong-version restore refuses and cleans up. This image has provisional
  `0.0.1/unknown` metadata and is not current-candidate/release evidence.
- OVH runs the exact checksum-matched scripts and temporary age 1.2.1 executables:
  **22/22 groups** plus the real synthetic restore/wrong-version drill against
  its preinstalled amd64 v1.6.27 image
  `sha256:9116f9cc38d6f41586fd18c1fad4fccf289fbf3fd0c38d14da835b4c43eec685`.
  Only throwaway keys/accounts/DBs are used; no production data is read. Agent
  PID495903/restart0 remains unchanged, no restore containers remain, and the
  owned workspace/tools are removed. This is not remote-native compilation,
  current-candidate platform acceptance or production custody acceptance.
- Exact candidate `5899614` CI40426994745 passes **7/7 jobs**, including
  **22/22** real-age groups in `deploy-scripts` and the real synthetic restore /
  wrong-version cleanup in `panel-smoke`, using its **candidate-built** binary
  and image. The existing smoke build is reused, not rebuilt for this drill.
  PR84 is merged as `f567128`; this is not production escrow-path recovery.

No local broad Go/frontend rebuild, fleet update, 200-node run, production
encryption cutover or legacy-copy deletion occurs. D12 decisions, restricted
source/namespace review, actual escrow-path canary/restore, schedule/freshness
review and explicit legacy retention remain rollout gates. Reverting the
schedule/wrapper does not decrypt new archives or erase old plaintext; keep
the last readable key/copy while reverting.
