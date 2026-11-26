# Family-pinned rollback research (experimental, not a runbook)

The code in `internal/pingrollback` is **not safe to execute on production**.
It has no production CLI, does not stop database writers, and does not protect
the metric store. Do not downgrade an enabled family-pinned panel by simply
changing the image, using an old database backup over live data, or calling
these experimental helpers.

## Two-store isolation experiment (2026-11-06)

An additional unexported scratch-only helper splits the persisted rollups of
explicitly parked task IDs into a separate metric store. It retains unrelated
ping and non-ping rows in the old-panel scratch store, rejects unknown `ping.*`
definitions and malformed/noncanonical ping task tags, and copies complete
rollup values and metric definitions. SQLite fixture tests verify the split,
retention of unrelated rows, and that a simulated old-panel write and delete
of the parked series cannot affect the separate archive. Partial output on
error must be discarded, not resumed. This test uses no old binary and no live
data. It covers only persisted rollups, not unflushed raw/hot/coarse data,
filesystem failures, database metadata or changes made while any writer is
active. A separate physical-identity precheck now rejects open SQLite stores
that resolve to the same file, including hard links and symlinks. It is not a
lease, cannot establish the open file's identity after path replacement, and
cannot stop another writer.

A scratch test confirms that a current-minute hot bucket is absent from the
split until a successful clean close and reopen. The report batcher now keeps
accepted records for a subsequent stop attempt when flushing fails, returns
the error without waiting for a worker that remains alive, and lets concurrent
stop callers observe a completed drain. Node-history cleanup also takes the
process-local transition fence and drains accepted queued samples before
deleting. These are not crash-proof: the server's cleanup continues after a
failed stop, its deadline can expire, and asynchronous node cleanup still has
no durable deletion journal or protection against node-ID recreation. No
result here proves that a production downgrade drained every writer.

An isolated panel-download snapshot prototype now captures the process's hot
and coarse summaries in a separate SQLite snapshot after draining the accepted
report queue. A scratch restore showed that prematurely sealing an open parent
lost its earlier contribution on subsequent writes. The revised draft seals only
parents past their grace window and requires durable matching children for every
remaining open parent; conflicting existing parents or ancestors fail closed.
Local fixtures restore and continue the same window, combine disjoint pending
levels, exercise filters and read budgets, and check SQLite integrity. A related
imported-ancestor conflict is now rejected before a local query can double-count
it or a sealing write can overwrite it. These checks do not fence external SQL
or another Store instance, and live fleet cost is not measured. The prototype
does not modify the live store; raw exact samples are represented only as minute
aggregates. The existing external MAC-WAN encrypted producer does **not** call
this path and can still omit process-memory samples. Neither path is atomic with
the main DB, the filesystem, other panel processes or external writers. It is not
a downgrade archive.

The close/reload prototype retains a store after an ordinary hot-flush failure
and rejects a blind retry after an ambiguous database COMMIT; fault injection
also covers a COMMIT that succeeded but whose acknowledgement was lost.
At the earlier checkpoint shutdown continued other cleanup on error. The local
draft now stops dependent cleanup on critical failure and waits for several
in-process writers; it still cannot prevent process exit or a supervisor kill.
A crash loses memory,
and cross-store coarse and late-upsert semantics are not yet proven. In
particular, do not turn `PersistHotRollups` into an operational cutover based on
these tests. A real release still requires a coordinated, recoverable boundary.

Another unexported scratch helper merges the old panel's current rollups with
that archive: it discards all old-panel rows for parked IDs, preserves other
current rows and definitions, refuses ambiguous IDs and changed metric units,
types or positive retention, and does not restore history for a disabled metric. Fixture
tests also exercise mismatched archives and retention changes. It is not a
general-purpose merge: removed nodes, administrator data-clearing actions and
other policy changes need explicit
reconciliation before a production design could use it.

Two additional scratch tests expose the limits of that approach. Following a
successful clean close, the separate old and archive stores can reconstruct an
unfinished coarse parent from their respective persisted minute children. That
does not prove that all production writers or queues were stopped. In a
simulated 48-hour downtime, normal cleanup expires an archived coarse bucket,
but the experimental merge imports it again. The helper has no cutover time or
binding to the effective per-tier retention policy. A passing test here
documents an unsafe result; it is not approval to merge a production archive.

This is not a safe two-database downgrade: no coordinator binds the parked main
DB rows to the correct physical metrics files, proves all writers stopped,
drains queues, detects divergent histories, seals or authenticates the archive,
or reconciles all changed metric definitions and retention. It has no operator interface or
crash-recovery journal. The original store and archives must not be moved or
deleted by this experiment. The old-image and arm64 gates below remain open.

## Why parking rows is not sufficient

The earlier idea of moving pinned rows out of `ping_tasks` by deleting them is
unsafe: installations can have a legacy `ping_records` foreign key with
`ON DELETE CASCADE`, and arbitrary other references may also exist. The new
experiment retains the row and ID, parks its interval and assignments, and
seals it with SQLite triggers. It records an integrity-checked snapshot and
refuses a changed task schema, damaged seal, changed node roster or invalid
incarnation. Restoration renews the incarnation and changes all rows in one
transaction. The node roster digest includes UUID and canonical creation time,
not node tokens; it is an accidental-change guard, not an authentication or
tamper-proof mechanism. The park does not include new nodes in a default-on
assignment, and a changed roster therefore requires explicit reconciliation.

SQLite unit tests cover cascading foreign keys, ID reservation, trigger-protected
update/delete/replace, old startup normalization, invalid or partial state, and
transaction rollback. The opt-in old-image test uses the locally installed
v1.7.29 amd64 image ID `sha256:9155d16695b89a8c46e846ec060c8ca162b102ad29556b4194802dd28a5de692`;
it runs with `--network=none` and a new synthetic SQLite fixture, completes a
synthetic installation, reaches normal service, and restores the parked task.
It deliberately marks the **scratch-only** legacy-monitoring migration as
complete so that the old scheduler starts while a legacy FK row remains for
the test. This is not a production restore drill, not a test of the arm64
image, and not a proof for any other schema or binary.

Critically, that same old image **accepts an authenticated legacy
`agent.pingResult` for the parked task**. A delayed or unsolicited result can
therefore pollute its metric history even if the scheduler does not dispatch
the task. A trigger in the main database cannot stop a write to the separate
metric database. In addition, the old task-delete path deletes metric history
before attempting the SQL deletion that the parking trigger blocks. Other old
admin actions and configuration imports may also change state outside this
boundary. Result fencing, API access and both databases must be covered by a
reviewed downgrade strategy; merely blocking the UI or waiting for queued
results is not a proven fence.

The helpers require every panel and database writer to remain stopped while
they run. A transaction cannot ensure that another process will not start
after it commits. Full rollout also needs cross-process exclusion, handling of
other databases and unknown schemas, a pre/post encrypted backup with isolated
restore, and a tested procedure for retaining live changes without copying an
older database over the current one. No production rollback procedure is
approved yet.

The 2026-11-07 local shutdown follow-up also joins WebSocket handlers, terminal
forwarders, selected notification and scheduler children, plugin host calls,
and asynchronous metric deletions before their stores close. A synthetic
encrypted handoff verifies those selected paths with newly created files, not
the old deployed binary. Other plugin/host and external writes, direct backup
restore exit, guide-mode work, and crash/restart policy are still outside the
proof. The old v1.7.29 panel must not be assumed to have these fences.

A later local draft joins asynchronous plugin filesystem operations and the
guided migration worker before dependent cleanup. UI restore uploads stage an
inert file, and the shutdown result is now terminal across failed or concurrent
retries; fatal paths also drain HTTP. An isolated synthetic ZIP handoff and
restart exercise these boundaries. They do not fence plugin child processes,
external writers or supervisor restarts. Hard-link publication followed by a
failed directory sync is explicitly uncertain and can still lead to restore on
restart. See the current open-work and backup notes; this is not an authorization
to downgrade or a proof of an atomic production archive.

The opt-in old-image test now additionally gives that isolated v1.7.29 amd64
container a freshly split scratch metrics DB and keeps the quarantined scratch
archive outside its only data mount. After an authenticated late legacy result
and a clean old-panel stop, the old store contains that unwanted parked-task
series alongside unrelated history, whereas the separately reopened archive's
parked rollup remains unchanged. The test passes with the race-enabled Go test
runner. A separate scratch merge subsequently restores the archived task row
and excludes the old-panel late result while retaining the unrelated row. An
initial merge fixture failed the new unit check because it did not use the real
`ratio` unit; after correcting the fixture to match both binaries it passes.
The scratch old store was then seeded with persisted parked history and the
actual old-image admin delete was called using a synthetic account/session.
The RPC reports failure because the main-DB trigger preserves the parent,
but the persisted parked rollups have already been deleted from the old store.
A result accepted just before deletion later reappeared on clean shutdown,
showing that delete alone also fails to fence queued/hot data. The separate
archive remains unchanged and a scratch merge excludes the reappearing row.
This verifies synthetic SQLite and the amd64 image, not arm64, a crash, a live
dataset, other writers or an operational cutover. All prohibitions above apply.
