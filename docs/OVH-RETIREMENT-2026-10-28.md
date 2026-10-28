# OVH retirement security and backup follow-up (2026-10-28)

The owner reports that OVH/KSLE has been sold or relinquished. Treat the host,
its address, disks and all private credentials formerly held there as outside
the trust boundary. Do not connect to it or assume its data was wiped. This
record supersedes older claims that OVH is a current backup or test host. The
owner confirms the OVH node was intentionally deleted from the panel; do not
restore it from a historical backup. The current approved baseline is 13 nodes.

## Known exposure

- The legacy `/srv/backups/oc424` archives were not encrypted and contained
  both panel databases, including Agent tokens and notification credentials.
  Existing release records also document plaintext manual archives. Their
  final deletion, disk wiping, access, contents and current status are unknown.
- The later `/srv/backups/oc424-encrypted` archives were age ciphertext. The
  restore identity was generated and retained on MAC-WAN, not on OVH; the owner
  also reports a downloaded offline copy. This does not establish that no
  other sensitive material existed on OVH, that disks were sanitized, or that
  the encrypted backup alone exhausts the exposure.
- OVH held dedicated legacy plaintext pull, encrypted pull and health-report
  SSH private keys. At 2026-10-28 around 15:38 UTC, the corresponding three
  exact fingerprint/forced-command entries were removed from OC424's ubuntu
  `authorized_keys`, after inspecting and backing up the unchanged file. The
  legacy pull had no source-address restriction and could stream current
  unencrypted database snapshots; the other two had source-address and forced-
  command restrictions. All other authorization bytes were preserved; a new
  OC424 SSH connection and the healthy v1.7.12 container were verified.
  Root-private rollback evidence is `/root/nekomari-retired-ovh-20261028-7a7cjzht`.
  **Do not restore those authorizations.** This does not revoke other keys or
  credentials that may have existed on the relinquished host.
- User-requested earlier non-rotation is not evidence that historical tokens
  are safe. If sanitization cannot be established, assess exposed credentials
  and plan coordinated rotation, including per-node recovery/owner consent.
  No token, API key or service was changed as part of the SSH containment.

## Recovery and current gates

At 15:37 UTC both MAC-WAN v1.7.12 release pre/post encrypted copies still
matched their recorded SHA-256 hashes, and the local identity file was present
with mode 0600. Both copies had previously been actually restored and checked
in isolation with 14 clients; that earlier drill is not a new restore. The
latest checked post archive in that earlier pair is from 15:11 UTC. OC424 remains on v1.7.12 and its
disk is 45% used; MAC-WAN has 27% used. No backup or recovery artifact was
deleted. The encrypted archives cannot be decrypted solely with OVH's documented
backup material, but the historical plaintext archives are a separate concern.

The former OVH receiver and reporter are no longer trusted. Their replacement
on MAC-WAN uses two new locally generated, source-limited, fixed-command keys;
neither key is an unrestricted SSH login or a decryption identity. At 15:44 UTC
the installed receiver created `oc424-20261028T154414282891Z-46156c63199b.tar.gz.age`
(186863796 bytes, SHA-256 `2275911a697d18b79741cfcc1bb3b592e2e588da92bf518ffc52ab0fcd8dad82`).
At 15:46 UTC the MAC-WAN health service reported this actual archive as `ok` to
OC424; the enabled 15-minute timer independently fired at 15:47 UTC. MAC-WAN's
enabled daily persistent timer is set to 03:30 UTC (11:30 Asia/Shanghai); its
first **scheduled** backup has not yet fired. User lingering is enabled. A
changed NAT source can cause safe SSH denial and stale-heartbeat alerts; the
first health attempt failed for this reason and a second observed address was
added after verification. Never suppress or forge stale reports.

On the owner's confirmation, the fresh archive's 13 UUID/token pairs match the
current OC424 database by comparison without disclosing values. On MAC-WAN the
actual isolated v1.7.12 recovery passes both database integrity checks, scoped
key schema, 13 nodes, uptime/panel history and networkless boot/cleanup. An
initial attempt had correctly stopped on its outdated 14-node assertion; its
failure is retained. The same ciphertext is independently copied, hash-checked
and kept outside pruning on this executor under `/root/.local/share/nekomari-recovery/macwan-ciphertext/`;
this is a second online host, **not** independently exercised offline media.
No key, identity, token or plaintext is exported. No Agent/Hermes restart,
credential rotation or old backup deletion occurs. Full runbook and source IP
limitations are in [`MAC-WAN-BACKUP.md`](./MAC-WAN-BACKUP.md).
Independent-media recovery, actual incident notification delivery and first
scheduled nightly success remain unverified.

Owner decision needed: whether OVH was securely wiped before transfer and
whether credentials associated with its legacy plaintext backups and any
other services should be treated as compromised. Do not send secrets in chat.
