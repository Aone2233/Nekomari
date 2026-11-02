# Saved load-alert rule preview

The load-alert page can preview one **saved** rule against recent metric or
ping records. Opening the dialog alone does not query. The administrator must
press **Run preview**. It never sends a notification, edits the rule, reloads
the scheduler or writes `last_notified`; it is not a notification-channel test.
The existing channel test in notification settings is a separate, real send.

Preview uses the same current-window bucket maxima, baseline calculation,
host ratio rounding and ping whole-window comparison as the current scheduler.
It reports each ping task/node pair rather than merging matched tasks by node.
The current maintenance-window flag is independent of the threshold result.
No records, unavailable baseline, missing capacity, invalid or deleted tasks,
unsupported metric and query failure are distinct from a non-match. Even when
there are buckets, this does **not** establish complete time coverage.

It is an observation, not a prediction of delivery. The rule and stored
`last_notified` are read from the database; the running scheduler uses rule
copies captured when the schedule was reloaded and may have older values.
The scheduler updates `last_notified` even when no notification is sent and
its asynchronous sender does not prove receipt. A preview can overlap edits,
new metric samples, maintenance changes or a scheduled evaluation; it is not
a transactionally consistent snapshot or a reservation. The preview reads
maintenance windows directly, whereas the scheduler uses a short-lived cache.
It neither validates notification enablement/provider nor predicts future
condition, cooldown, maintenance or real delivery.

## Baseline floor and editing

The pending load-rule update preserves an explicitly supplied zero threshold in
baseline mode on create and edit. Previously the model default changed it to
80 on create, the editor displayed 80, and a struct update ignored a zero
threshold. An omitted baseline create threshold still defaults to 80 for API
compatibility; fixed mode still requires a non-zero threshold. Existing rules
are not migrated: inspect their saved value before changing them. A zero floor
does not guarantee an alert when the historical P95 is zero: the evaluator
requires at least eight samples and skips when the resulting threshold is not
positive.

Partial edits preserve omitted fields; explicit empty client/task arrays clear
them. The editor serializes each row's saves through a refresh to avoid an
immediate stale edit. Clients can still supply `last_notified` explicitly as before, but the
editor does not submit it and the timestamp is not proof of delivery. An
invalid interval, host ratio, mode or zero fixed threshold is refused; a batch
is atomic. If any legacy rule has a non-positive or overflowing interval, a
create or edit fails until the offending interval is repaired, rather than
discarding existing scheduled jobs. A partial edit may repair that interval.
The changes do not enable unsupported metrics or validate delivery.

The `admin:previewLoadNotification` RPC and REST bridge are administrator
session only; no API-key scope is added. Input is a saved numeric ID, not an
arbitrary query. One preview can run at a time with no queue, a 15-second
deadline, a shared metric read budget, at most 50 expanded targets and a
31-day baseline window. Larger saved rules are not changed, but cannot be
previewed with this bounded endpoint. Empty node IDs are rejected per target
and never trigger an all-node metric query.

Known limitation: `temp`, `backup_age` and `backup_ok` cannot currently be
previewed: the existing single-record-metric lookup used by the scheduler does
not map those UI options. They are shown as unsupported, not healthy. The
backup-age/status ingestion path and the separate MAC-WAN backup health watcher
exist, but that does not mean these load rules fire. Correcting metric mapping,
unknown backup semantics and the single `>=` comparison needs a separate
review; do not silently enable dormant alert rules by changing lookup here.
No offline-rule preview, actual test delivery, real incident receipt, capacity
benchmark or v1.8 completion is implied by this feature.
