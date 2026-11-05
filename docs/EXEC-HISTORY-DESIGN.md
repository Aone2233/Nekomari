# Remote execution history: design and safety contract

Status: implementation in progress on a separate development branch, not
released or deployed. CSV owner browser acceptance remains a release gate.

## Existing behavior and the actual gap

`admin:exec` creates a `tasks` row containing the command and its target list,
plus a `task_results` row per target. Results can contain arbitrary secrets.
The execution page displays the current invocation but has no history entry
point. `admin:getTasks` loads every task and then every result (an unbounded N+1
query); it is not suitable as a history-list backend. The existing result
cleanup deletes rows older than 30 days by `created_at`, but does not delete the
parent task/command, and it can expire a still-running result. Therefore this
is neither a 30-day command-retention guarantee nor a source of a definitive
execution status. Do not change cleanup or delete historical commands as a
side-effect of adding the UI.

The current APIs grant `tasks:read` access to commands and outputs. The next
slice must not accidentally broaden that scope or make history public; reading
output is sensitive even though executing a command requires separate
`tasks:exec` and step-up checks. Preserve the existing API contract for clients
such as Hermes; avoid an incompatible replacement of `getTasks`.

## Bounded history contract

- Provide a new paginated summary method, mapped explicitly to `tasks:read`
  and available to the existing administrator session. Return task ID, target
  count and counts of pending/finished/failed results, plus the earliest
  available result-creation timestamp. No output or command is in list rows.
  A parent without retained results is `no_retained_results` with an unknown
  time: cleanup, deleted targets and failed creation cannot be distinguished.
- Use stable keyset pagination, a small fixed maximum page size, deterministic
  tie-breaking and bounded database queries. Current `tasks` rows lack a
  creation timestamp, and orphan parents are not chronologically sortable;
  choose and document a truthful ordering before implementation. Do not add a
  schema migration or expensive unindexed full-table aggregation without a
  measured query plan and rollback design. Deleted nodes must not become an
  authorization bypass or crash the UI.
- Fetch one selected task's command and outputs only after explicit user action,
  with separately bounded target pagination and total response bytes. Do not
  silently truncate; expose truncation and retain a way to read a bounded
  portion. Keep null `finished_at` as pending/unknown: a local polling timeout
  is not a failure or proof the command ended. Do not re-run a command when
  opening a history item. No command or output in logs, URL, localStorage,
  clipboard, telemetry or notifications without the user's explicit action.
- Show a clear warning that commands/results may contain secrets and that
  results can expire after 30 days while command metadata persists. Use the
  existing admin shell and theme, text-only rendering, accessible keyboard/focus
  behavior, responsive layout and all five supported locales. Avoid rendering
  untrusted output as HTML.
- Existing task results arrive asynchronously and can change while paging;
  document consistency and do not claim the history is a frozen snapshot.
  Fetches must fail closed on authorization or errors; `Cache-Control: no-store`
  must apply to sensitive REST/RPC responses.

## Implementation and operational limits

The implementation exposes three JSON-RPC methods through the existing
no-store `/api/rpc2`: `admin:listExecHistory`, `admin:getExecHistoryDetail`
and `admin:readExecHistoryChunk`, all explicitly scoped to `tasks:read`.
Each list is limited to 20 rows; a summary examines at most 1,000 results
per task and labels partial counts and timestamps. Command or output text is
only read on a separate user action in byte-bounded 16 KiB chunks, encoded as
base64. The browser renders decoded text, never HTML; invalid UTF-8 bytes are
shown as replacement characters, while the API preserves the original bytes.
Batch JSON-RPC requests containing any of these methods are rejected, so the
transport cannot amplify the response budget 32-fold. History has its own two
query slots, a bounded queue and a ten-second work deadline. Only the current
chunk remains rendered; cursor navigation retains at most 100 page positions.

The chunk response limit is not a SQLite memory limit: SQLite materializes a
text field before applying `substr(CAST(... AS BLOB))`. History requests lease
the main SQLite connection exclusively and temporarily set its
`SQLITE_LIMIT_LENGTH` to 16 MiB, restoring the original value even after an
error or cancellation. Existing panels use one main SQLite connection, so
history still competes with other main-database operations; its two admission
slots do not imply separate database connections. The request deadline can
interrupt a running statement but is not a hard wall-clock bound for every
SQLite operation.

`octet_length` checks command/output field sizes without reading their contents.
For TEXT or BLOB, a small C-only SQLite function opens a read-only incremental
blob on the **same connection and statement snapshot**, reads at most 16 KiB,
then closes the handle. Only the two fixed history columns are allowed; the
calling RPC still applies authorization and identity checks. The function is
registered on new SQLite connections and marked `SQLITE_DIRECTONLY`, so stored
views, triggers and schema expressions cannot invoke it. Administrators with
the separate sensitive arbitrary-SQL capability can call the function
directly, but that capability can already read these tables. Empty and NULL
values remain readable as empty; malformed non-TEXT/non-BLOB values return
unavailable without modifying data. `output_available` and `available` expose
that decision to the UI. The extension relies on SQLite's implicit rowid and
the bundled SQLite ABI; Windows and ARM builds remain release gates. Only one
chunk has a statement-level snapshot; separate chunk requests can see updates.

The connection-wide 16 MiB limit still makes other malformed, materialized
fields fail closed. This limits individual SQLite values, not total process RSS
or all possible CPU work. On scratch SQLite 3.46.1, the original `substr` query
over a synthetic 64 MiB output peaked around 75 MiB RSS; a separate incremental
blob prototype read the first and last 16 KiB at around 13 MiB RSS. A canceled
1 ms synthetic query had taken around 16 ms with the original full-field read,
so cancellation must not be presented as an instantaneous guarantee. Local
WAL tests also exercise a concurrent writer, read-snapshot consistency,
read-only connections, DIRECTONLY, malformed values and length-limit recovery.
These are local measurements, not production performance or memory SLOs.

SQLite's implicit `rowid` is not stable across `VACUUM`, and legacy task IDs
are random. The upgrade adds nullable `tasks.history_order` and indexes,
backfills previously unnumbered parent rows transactionally on startup using
their rowid rank rather than adding potentially extreme rowids. New tasks get a
monotonic order in their creation transaction. Integer exhaustion fails the
transaction rather than persisting a floating-point order. After an older-image
rollback, tasks created by that older image are reconciled on the next startup
and sorted after already numbered tasks; they cannot be placed precisely
among those tasks. Parent pages use
`(history_order, task_id)` keysets; no timestamp is invented. Result pages use
the `(task_id, client)` index plus a rowid tie-breaker; zero and negative
SQLite rowids remain valid, while a missing or null rowid in a cursor is
rejected. The upgrade also adds a nullable, uniquely indexed 128-bit random
`task_results.history_identity`; startup backfills existing rows, and an
insert trigger assigns identities to new rows including those written by an
older image after rollback. Startup reconciles missing identities if the
trigger was absent during a restore or rollback. Each cursor binds task,
client, rowid and this identity, so rowid reuse or VACUUM cannot silently
retarget it to a different result even if duplicate clients have the same
timestamp. The index makes random collisions fail closed; a collision or other
backfill error aborts startup rather than silently selecting an ambiguous row.
The identity is internal and omitted from existing task JSON; its value is not
an authorization credential. Missing/malformed identities fail closed. A
privileged administrator who deliberately rewrites identities through raw SQL
can invalidate the guarantee. Refresh the list after an invalidated cursor.
Results can update or expire between requests, and output can change between
chunks without its cursor changing; the UI warns that chunk reads are not a
snapshot. Old parent commands do not gain a retention guarantee.
Unknown/malformed legacy client identities fail closed.

On local scratch SQLite with 10,000 synthetic tasks and 300,000 results
(including one task with 100,020), measured migration time for the added
column/indexes and rank-based backfill was approximately 68 ms; an indexed result seek
returned 21 rows in 0.06 ms and a capped 1,001-result summary in 0.94 ms.
These are small-host measurements, not production latency guarantees. The
parent keyset, result keyset and capped summary query plans are asserted to use
their respective indexes without a temporary sort. Upgrade tests must confirm existing data,
restart, VACUUM and rollback behavior; index construction is a real schema
and startup cost, not a no-migration change. A rollback to the prior panel
must retain the live databases and must be tested on an isolated copy first;
newly created tasks while the old version is running have no order value until
the next startup reconciliation.

A later scratch SQLite 3.46.1 test added the result-identity column, unique
index, transactional backfill and insert trigger to 300,000 synthetic rows in
about 854 ms. The database's logical page footprint grew from approximately
28.5 MB to 54.9 MB; WAL and backup size can add temporary overhead. A repeated
no-op reconciliation took about 0.05 ms and used the identity index to find
NULL values. This is a local synthetic measurement, not a production startup
SLO; a real migration must check free space and restore the fresh backup if
the startup fails. Do not overwrite a live database with an old snapshot
without explicit recovery authorization. A separate
v1.7.29/candidate/v1.7.29/candidate fixture confirms
the old binary retains the trigger, new inserts get distinct identities and
the retained rows survive restart with `quick_check` returning `ok`.

A network-isolated synthetic old/new/old/new startup drill used the verified
v1.7.29 amd64 release binary and the development candidate. It started the old
panel, migrated its fixture with the candidate, started the old binary against
the migrated database, wrote three synthetic old-schema tasks while it ran,
then restarted the candidate twice. The original and new orders, synthetic
commands and outputs survived, and SQLite `quick_check` returned `ok`. The
rollback writes were SQL fixtures matching the old schema, not actual commands
dispatched through an agent; production data was not used. This drill was
repeated after switching the chunk reader to incremental blobs, using the
checksum-verified v1.7.29 amd64 binary and the rebuilt candidate. The first
attempt timed out because its readiness helper kept polling the install-only
route after installation; the corrected helper accepts the version route and
the complete old/new/old/new/restart sequence passes.

A separate network-isolated disposable panel was built with the current
embedded frontend and served real backend responses for synthetic task rows.
The administrator history page opened and read command/output as text in
English desktop/light and Chinese mobile/dark, including the first and final
chunks of synthetic fields larger than 16 MiB; no execution RPC was dispatched.
The first Chinese attempt was blocked by the fixture's unaccepted EULA, not a
history error; setting acceptance on that disposable panel allowed the retry.

The new read paths do not log their SQL parameters. This is not a blanket
secrecy claim: existing Agent code logs commands during execution, and older
task APIs/write paths predate this feature and retain their existing behavior.
The new UI does not dispatch or rerun commands, copy text automatically, or
persist it in URLs/localStorage.

The checked-in Go-only embedded frontend fallback is also regenerated from the
current frontend source. CI and release rebuild their own archive; rebuilding
was done in an isolated scratch tree so the packer's staging-directory cleanup
could not remove unrelated workspace files.

## Acceptance

Use synthetic tasks and outputs, never a real production exec. Cover admin,
`tasks:read`, unrelated scoped key and guest access; pagination and ties; empty,
expired, deleted-client and unfinished results; output containing HTML/control
characters and secrets; oversized output; concurrent updates; cancellation and
query/response limits. Test desktop and mobile UI, both themes, keyboard
navigation and recovery after page refresh. Measure a realistically populated
scratch database before deciding indexes or an optional schema migration.

Deploy only after the CSV correction's owner acceptance, the normal exact-CI,
release-artifact, fresh off-host pre/post backup and OC424 verification gates.
The feature changes neither node tokens nor Agent commands automatically.
