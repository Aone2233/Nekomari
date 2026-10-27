# Off-host backup health (v1.7.3)

This optional Linux monitor checks encrypted backup freshness on OVH and missing
reports on OC424. It does not decrypt backups, prove authenticated freshness,
replace actual restore drills or establish production RPO/RTO/SLOs.

## Flow and limits

1. OVH's separate `nekomari-oc424-backup-health.timer` runs every 15 minutes.
   `backup_health.py inspect` shares the existing receive lock, reads only the
   newest exact-prefix top-level ciphertext pair, checks its SHA-256/native-age
   header and parses its filename time. Missing/bad latest archives are reported,
   not hidden behind an older valid archive. Receive-in-progress defers without
   refreshing the heartbeat. Directory entries, bytes and time are bounded.
2. A separate SSH push key, held on OVH only, sends four non-secret JSON fields
   (version,checked_at,status,backup_at). It cannot read Panel APIs, tokens, DBs,
   backups or identities. OC424 pins it with `restrict`, a source-address rule
   and a fixed sudo/forced-command wrapper. Ingest is limited to4KiB/10seconds,
   validates UTC times within5minutes, ignores duplicate/older checks and
   atomically writes one fixed private file with OC424's receive time.
3. `NEKOMARI_BACKUP_HEALTH_FILE=/app/data/backup-health/ovh.json` opts the Panel
   into a startup check and minute scheduler. A26-hour-old backup or45-minute-old
   checked/received heartbeat is an incident; future/malformed/missing files fail
   closed. Unconfigured installations do nothing. No API or settings schema is
   added; this environment variable is not a credential.
4. The Panel uses the already configured notification provider. Healthy checks
   stay quiet; incident changes and recovery send once. Persistent private
   `alert-state.json` suppresses duplicates across normal restarts. A disabled/
   missing/failed provider is not acknowledged; retry is at most every15minutes.
   Send errors are sanitized and never stored. An operational send holds provider
   lifetime stable; overlapping watcher checks skip, rather than queue goroutines.
   Telegram uses the existing shared outbound transport with a30-second timeout
   and64KiB response limit. Other providers keep their existing timeout behavior.

The producer/receiver encryption tool, nightly03:30UTC schedule and14-pair
retention are unchanged. No API key, Agent token, decryption identity, fleet
restart or Hermes grant is needed. All old plaintext/manual/rollback copies stay.

## Provisioning and rollback boundary

The wrappers/units in `deploy/hosts` are templates, not auto-install scripts.
On OC424 create root-owned0700 `/opt/nekomari/data/backup-health`, install the
checksum-reviewed Python tool and root-owned fixed receiver wrapper, and grant
only that wrapper in a separate sudoers file. Append only the dedicated health
public key to ubuntu's authorized_keys with the fixed command and source limit;
preserve all existing authorization. OVH generates/keeps only the dedicated push
private key0600; it reuses pinned OC424 known-hosts, not the encrypted pull key.

Run the OVH service once and verify OC424's bounded metadata before enabling its
15-minute timer or opting the Panel in. A fresh report must be present before
the app switch to avoid a synthetic missing-report alert. Back up original
compose,authorization,sudoers/tool/unit state and hashes in root-private release
recovery directories. Check exact preexisting bytes before each change.

To roll back, restore pinned v1.7.2 compose/image, preserving current DBs and
Pi's added node. Disable only the new health timer; restore/remove only the
reviewed health-specific authorization line,sudoers,units and tools using the
recorded checksums. Do not disable the encrypted nightly backup timer, delete
backups, restore old13-client DBs, revoke unrelated keys or rotate credentials.
If another operator changed a file, stop and reconcile rather than overwrite it.

## Verification

```sh
python3 -W error::ResourceWarning deploy/backup_health_test.py
GOTOOLCHAIN=go1.27.3 go test -race ./internal/backupwatch ./internal/server ./utils/messageSender/...
python3 -W error::ResourceWarning deploy/backup-health-smoke.py --panel-binary /path/to/nekomari
```

Hermetic tests cover checksum/header/latest failure,clock bounds,receive locks,
size/path/atomic writes,replay ordering,threshold boundaries,unavailable delivery,
persistent dedupe/recovery,overlapping checks and provider-error redaction.
The6-group real installed synthetic Panel harness uses loopback fake Telegram:
stale backup -> restart dedupe -> recovery -> stale heartbeat. It is not a live
Telegram delivery test and never uses production secrets or fakes a live outage.
Required exact-source CI also retains all existing gates. Run the attested
release binary on OVH before OC424's real continuity acceptance.

Publication, actual deployment, scheduled events and authenticated owner UI
are separate evidence. See [the live work list](./OPEN-WORK.md),
[OC424 runbook](./DEPLOY-OC424.md) and [custody](./MAC-WAN-RECOVERY.md).

## Actual delivery (2026-10-27 16:29:42 Asia/Shanghai)

PR96/source `0fe0b37`: exact CI40471914791 7/7, release40472663920 10/10,
Docker40473430351 2/2. Native Linux checksums/source attestations and OCI versions
verify; six actual synthetic-Panel groups pass on OVH. OC424 is healthy/restart0;
upgrade9/9 and public23/23 pass. All 14 UUIDs/tokens/fresh reporters, DB schemas
and integrity, keys/scopes/expiry and Agent/Hermes services remain unchanged.

The installed OVH service/manual check and initial automatic health-timer
trigger succeed; the 15-minute timer is enabled/active. OC424 receives the
restricted SSH report and persists `observed=ok`, `notified=""`, with no live
test alert. Fresh old/new-image encrypted recovery passes; all backup/identity
boundaries remain. Later recurring checks, first nightly backup, live delivery,
owner UI, independent-media restore and measured SLO/RPO/RTO remain unclaimed.

Root-private operational recovery: OC424 `/root/nekomari-v173-health-20261027T082843Z-e84d4362`,
OVH `/root/nekomari-v173-health-20261027T082845Z-76e2a235`; app snapshots/compose/image rollback
`/opt/nekomari/rollbacks/v1.7.3-20261027T082941Z-ca9d9477`. Exact hashes/images/archives are in the runbook;
no private identity, API token or SSH private key is included in repository evidence.

## v1.7.4 continuity observation (2026-10-27 17:23:20 Asia/Shanghai)

The existing restricted authorization, receiver/tool/unit hashes, nightly timer and
15-minute health timer remain unchanged. Later recurring health checks now pass;
OC424 fresh report/quiet `observed=ok`, `notified=""` state and environment opt-in
survive the v1.7.4 image-only restart. No repeated healthy notification/startup
log is required when persisted state is unchanged. First nightly encrypted
backup is due 2026-10-28 11:30 Asia/Shanghai, not yet observed; manual release service
runs are not scheduled-success evidence. No fake production outage/test Telegram
is sent. Actual incident delivery, external Panel-independent monitoring, owner UI,
independent-media recovery and measured SLO/RPO/RTO remain separate.
