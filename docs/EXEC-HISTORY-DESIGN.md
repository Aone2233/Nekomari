# Remote execution history: design and safety contract

Status: design for the next v1.9.0 slice, not implemented or released.

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
`exec:write` and step-up checks. Preserve the existing API contract for clients
such as Hermes; avoid an incompatible replacement of `getTasks`.

## Bounded history contract

- Provide a new paginated summary method, mapped explicitly to `tasks:read`
  and available to the existing administrator session. Return task ID, target
  count and counts of pending/finished/failed results, plus the earliest
  available result-creation timestamp. No output or command is in list rows.
  A parent whose results have expired must be identified as `result_expired`
  and its time as unknown, not guessed from its ID or a cleanup time.
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
