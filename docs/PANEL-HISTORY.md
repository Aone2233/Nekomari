# Panel process history

Candidate for v1.7.11; deployment is not yet claimed. This is one bounded part
of v1.8 observability, not a complete v1.8 release or an SLO measurement.

The existing Performance profiles page shows the panel process's RSS, Go heap
allocation and goroutine readings as a selectable 1-hour or 24-hour trend.
These are instantaneous gauges, not node metrics. A background sampler runs
approximately once a minute, including once on startup; it never overlaps
itself. Each window bucket reports the last observed value, not a peak or an
average. History starts after the feature is enabled; no backfill is invented.
RSS is omitted where `/proc/self/statm` is unavailable, rather than reported as
zero. Missing buckets are unknown. Bucket labels are not exact sample times.

The three definitions default to one day each, independently configurable in
the existing monitoring database retention settings. Zero disables that metric
and clears its historical data. Panel definitions are excluded from the
legacy node-record retention summary. Samples share the existing metric store,
one fixed internal entity and existing compaction/retention policies. Current
and closed minute buckets persist on normal store close, while an unclean stop
may lose the latest in-memory observations. Partial coarse buckets can lag
until the next rollup; the current bucket prefers a bounded recent minute-tier
read and otherwise retains any existing coarse observation.
Reload switches to the configured store without copying history; missed or
failed samples and restarts can leave gaps. Sampling failures are logged with
the configured DSN redacted. This is neither a durable audit log nor a guarantee
of availability or exact 24-hour coverage.

The endpoint is `/api/admin/pprof/history`, under the same session-only
administrator route as the existing profiles. Guests, Agent tokens and API
keys including `nodes:read` and `diagnostics:read` are denied, even when a
cookie is present alongside the bearer. The public definition/query methods
do not disclose the panel metric definitions or accept those names. The query
permits only the two fixed windows, two concurrent reads, a 10-second deadline,
and a shared 10,000-row/8 MiB estimated read budget. Sampling has a 5-second
deadline and holds a shared store-operation lease across the write, as does the
read, preventing replacement or maintenance from closing the store mid-call.
No Agent, credential, key scope or external metrics endpoint is added.

Only RSS, heap allocation and goroutines are covered. Theme-cache history,
database wait counters, restart counting, real alerts and SLOs are separate
work. In particular, pool waits are process-cumulative and must not be
interpreted as continuous increments across restarts.

## Rollback boundary

The feature uses existing metric tables, but v1.7.10 and earlier do not know
these three definitions. An image-only rollback can keep the current database,
but **manual metric-store reclaim on an older image deletes unrecognized
definitions and this history**. Preserve a fresh verified encrypted
post-upgrade backup before any downgrade or reclaim. Do not restore an older
13-node database over the current fleet. The earlier authentication migration
across v1.7 to v1.6 still needs its paired database and binary with writers
frozen. Isolated restore, actual publication, production acceptance, owner UI
acceptance and independent-media recovery are separate evidence gates.
