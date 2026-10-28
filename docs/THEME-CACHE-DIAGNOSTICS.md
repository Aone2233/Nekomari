# Default-theme cache diagnostics

Published and deployed as v1.7.9 on OC424; not a completed v1.8
observability/history release. The
existing in-panel Performance profiles page gains a small current-process view
of the built-in default theme cache, alongside RSS/heap and database pools.
It does not monitor third-party themes or change cache policy.

## Meaning

Disk state is `disabled`, `uninitialized`, `initializing`, `ready`, `failed`,
or `degraded`. `ready` means initial materialization/reuse passed its existing
verification and no disk fault has since been observed; it is **not** continuous
verification of every file. `degraded` is sticky after a disk failure observed
by a lookup; an ordinary missing asset does not count. Initialization failure
is distinct from an intentionally disabled cache. No path or error text is
returned. Reading the state does not unpack the archive, scan the filesystem or
wait for an in-flight unpack.

The separate memory flag becomes true only after this process has successfully
loaded its in-memory fallback archive. Once loaded, it remains in memory even
if disk requests later succeed. A disabled or failed disk cache does not by
itself prove the archive has already loaded; a successful disk cache does not
prove memory was never needed earlier. The state is per process and resets on
restart; it is not a byte count, a history series, a notification, or a health
check for every theme. Last-known disk status is not proof that the cache has
not been externally deleted since the last lookup.

Only an authenticated administrator session can read the existing
`/api/admin/pprof/summary`; guest, agent and API keys (including
`diagnostics:read`) are denied. No API-key scopes, Agent reports, credentials,
metric definitions, database schemas or retention policies change. The UI
accepts old server responses without the field and maps future unknown disk
states to an unknown label rather than failing the entire diagnostic page.

## Validation and rollback

Unit/race coverage checks cold, in-progress, success, failure, disabled,
deleted-at-runtime, ordinary-miss and fallback states. A synthetic installed
binary verifies real session/key authorization and embedded desktop-light and
mobile-dark UI with the cache enabled and disabled. The exact published binary
passes this isolated check on OVH. Production version/health, public denial,
encrypted backup recovery and node continuity pass; see
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md) for evidence. No owner-authenticated UI
session was performed.

No schema or stored history is added. With the normal fresh encrypted backup and
pinned prior image, an image-only rollback to v1.7.8 keeps current data; do not
restore an older backup over newer nodes. The independent-media restore, owner
UI acceptance, actual alert delivery and full self-metrics history remain open.
