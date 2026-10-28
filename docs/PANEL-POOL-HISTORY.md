# Panel database connection-pool wait history

Candidate only; this document is not evidence of publication or deployment.
The session-only Performance profiles page extends the existing RSS/heap/
goroutine history with main database, metric write pool and optional metric
read pool wait count and total wait time. This is one part of v1.8, not full
observability, an alert or an SLO. Old servers lacking the new response fields
are shown as unknown, not zero.

`database/sql` wait counters are cumulative per pool instance. A sample is a
difference only between consecutive successful observations of the **same**
pool, 30–90 seconds apart, assigned to consecutive UTC minute buckets. The
sampler uses monotonic elapsed time where available, does not overlap, holds the
metric-store operation lease for pool snapshot and write, and clears its
in-memory baseline after an overlap, cancellation, write failure or restart.
Metric-store replacement invalidates its write and read pool baselines. Main
database handle replacement invalidates the main baseline. A missing dedicated
read pool is unknown, not an idle pool. Counter decreases invalidate that pool
interval. A new process cannot compare to prior-process counters even if they
have already exceeded the old values. No older data is backfilled.

Each displayed bucket contains the **last interval reading** in that bucket,
not the sum, peak, or average of all intervals in it. A stored negative sentinel
means the interval was not established; it is returned as JSON `null` and
preserves unknown through last-value rollups. Coarser buckets can hide earlier
gaps within the same bucket, and bucket labels are not exact timestamps. Total
wait duration is the change in `sql.DBStats.WaitDuration`, including waits
completed during that interval; it is not latency for an individual request,
pool saturation percentage or a total for the shown bucket. Failed/unclean
stops can lose in-memory observations. Recent coarse parents may lag and the
existing bounded live-tail read supplies their latest observed interval.

The six definitions are independently configurable and default to one day,
even on legacy-store migration. Retention zero disables and removes that
metric's data. They remain private to the session-only administrator history
endpoint and are excluded from public metric definitions/queries and the node
retention compatibility summary. The existing fixed windows, concurrency,
deadline and shared read budget still apply. No Agent, public metrics route,
scope, API credential or database schema is added.

## Rollback

Preserve and verify a fresh encrypted post-upgrade backup before downgrade or
manual storage reclaim. The v1.7.12 and older binaries do not recognize these
six definitions; their manual reclaim deletes the new history even when an
image-only rollback otherwise keeps the live database. Do not overwrite the
owner's current 13 nodes with an older 14-node snapshot. The earlier v1.7 to
v1.6 authentication migration has a separate paired binary/database rollback
boundary. Independent-media recovery and owner-session UI acceptance remain
separate; a synthetic browser test is not live owner acceptance.
