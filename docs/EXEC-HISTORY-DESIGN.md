# Remote execution history: design and safety contract

Status: PR #165 merged after the owner authorized advancing the panel deployment
following the synthetic history review handoff. This does not claim an observed
production history-page session. See [open work](./OPEN-WORK.md) for rollout state.

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
  This replaces the earlier draft's `result_expired`: the cleanup path cannot
  establish that a missing result expired, so the status names only what
  the database still holds.
- A row that cannot be addressed must not take the page with it. Skip it and
  count it in `skipped_malformed` instead of failing the response. Unaddressable
  means: an empty value; a task ID longer than 36 bytes, invalid UTF-8 or a NUL;
  a client longer than 128 bytes, invalid UTF-8 or a NUL; a result identity that
  is not 32 lowercase hex characters; or, in the list, a `history_order` of zero
  or less. Rows whose `history_order` is NULL are excluded by the query and are
  not counted. A client between 37 and 128 bytes stays addressable and is shown
  with `client_truncated`, because the cursor carries its full value.
- Paging must survive its own skipped rows. The next cursor is minted from the
  last row actually fetched, not the last addressable one, so a page made
  entirely of unaddressable rows still advances. Cursor values travel as bytes
  up to 512 octets, the encoded cursor is capped at 1024 characters, and the
  count is per page. Rows after the last travelable position are fetched and
  counted again on the next page, so a row can be counted twice; it is never
  displayed twice. When the boundary row's
  own key cannot travel at all — a task ID or client longer than that, or one
  carrying a NUL — no cursor is minted and `blocked` is set, so the reader is
  told that more rows exist behind an unpageable boundary instead of being
  stranded in silence. A cursor minted from a row that cannot be displayed is
  boundary-only: it names a position, never a row. The detail page accepts it
  only as a position, and chunk reads refuse it outright.
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
Each list is limited to 20 rows, and a page's counts come from one aggregate
statement over that page's task IDs, so they are exact. The earlier per-task
`LIMIT 1001` did bound how long one parent could cost, but it did so by making
the numbers wrong; the aggregate trades that bound for one that holds per page:
twenty indexed seeks, index-only counting, the ten-second work deadline and the
driver's interrupt handler. The earliest retained time is chosen by
`min(julianday(created_at))`, so it orders instants rather than text and is
reported to the millisecond, and a summary whose results all have unusable
timestamps reports `time_incomplete` instead of omitting the time quietly.
Measured on a scratch database of 20 tasks x 1,000 results, a page took
31-38 ms with the previous one-query-per-parent path and 3.8-6.6 ms with the
aggregate; since the history request leases the single main connection
(`SetMaxOpenConns(1)`), that duration is also the window in which other database
work waits. Command or
output text is only read on a separate user action in byte-bounded 16 KiB
chunks, encoded as base64. The browser renders decoded text, never HTML; invalid UTF-8 bytes are
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
can invalidate the guarantee. A detail-page error offers both retry and an
explicit refresh from the first result page, so an expired cursor does not
trap the operator in repeated retries. This rereads the current task's result
metadata and does not rerun the command or automatically display its output.
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

**Production-version rollback drill (2026-11-23).** Three independent Linux
amd64 runs used the checksum-pinned v1.8.1 release panel and a CGO build of
`9ee5a8b`. Each booted v1.8.1 → candidate → v1.8.1 → candidate → candidate
on one fresh synthetic database. The old panel accepted a legacy-shaped SQL
insert while running against the migrated schema: the new result got a unique
identity immediately and the unnumbered task was ordered on the next upgrade.
Both task markers, outputs, orders and identities were retained on the final
restart. `PRAGMA quick_check` and graceful shutdown passed on all five boots.
The harness also refused a non-isolated network and detected substitution of
the old executable for the candidate. This checks clean process transitions,
not production data, Agent execution, a crash, or an off-host restore.

**v1.8.2 rollback check (2026-11-26).** After integrating main `7fccca6`,
the same five boots passed three times against the published Linux/amd64
v1.8.2 panel (`d58ec32`, SHA-256 checked against the release manifest). Each
run used a fresh synthetic database, an unprivileged account and a loopback-only
network. The candidate was built with CGO and Go 1.27.4 from Git tree
`2034764d5c72` (a local tree stamp, not a release commit). Separate browser
checks against that same embedded binary exercised both admin CSV downloads
under a replacement public theme and the read-only history view in English
desktop and Chinese mobile layouts. These checks did not use production data,
dispatch a command or constitute owner acceptance of the history view.

The next exact commit, `d6f1b5b`, passed PR CI 8/8 but only five of six local
rollback drills: the first candidate stop after rollback exited on SIGTERM
instead of draining. Both the normal and guide servers launched the listener
before subscribing to SIGTERM. A Linux subprocess test sending SIGTERM at the
startup announcement reproduced the unclean exit in both modes; it does not
locate the precise delivery instant in the original drill. Registering before
the announcement and listener makes both modes reach cleanup at that boundary.
This change does not provide graceful shutdown during earlier bootstrap phases
or change the published v1.8.2 executable used for the rollback boots.

To repeat with the [drill](../deploy/experiments/exec-history-rollback.py),
use Python 3.11+ and Linux/amd64. Supply the official v1.8.2 `nekomari-linux-amd64`
and a candidate built with CGO and `utils.VersionHash` set to its commit. Run as
an unprivileged user in a fresh network namespace with only loopback enabled
(on hosts allowing unprivileged user namespaces):

```sh
unshare -Urn sh -ec 'ip link set lo up; exec python3 deploy/experiments/exec-history-rollback.py "$@"' \
  sh /path/to/v1.8.2/nekomari-linux-amd64 /path/to/candidate "$(git rev-parse --short=7 HEAD)"
```

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

The [embedded-panel drill](../frontend/script/exec-history-panel.spec.py)
seeds 22 tasks, with 21 results on the oldest task. It interrupts the first
request for the next task page and the next result page, then checks that
**Retry** sends the same cursor and displays the missing rows. It also navigates
back without fetching command or output chunks. **Refresh**, unlike Retry,
returns to the first page when a cursor no longer works. The earlier happy-path
checks did not distinguish retrying a failed page from reloading the previous
one. To save synthetic screenshots for owner review, pass `--review-dir` with
a new directory path alongside `--panel-binary`. Do not use a production
database or mistake these screenshots for owner sign-off.

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
