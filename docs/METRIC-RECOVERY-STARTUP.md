# Recovery startup: the boot-time coarse-rollup scan

Status: fixed and delivered in v1.7.5 (see `DEPLOY-OC424.md`). Date: 2026-10-27. Applies to the metric store
opened by `internal/metricstore.InitializeStore`.

This document records an investigation, not a deployment. Nothing here ran
against a production database, changed a stored archive, or altered a schema.
Production acceptance, an OC424 rollout and an independent-media restore are
separate gates and are still open.

## What was observed

On 2026-10-27, a fresh v1.7.4 pre-release archive passed age authentication,
manifest and file digests, and both database checks, but the isolated MAC-WAN
recovery boot with the pinned v1.7.3 image never became ready inside the startup
deadline: metric-store initialization hit its context deadline. Running
`ANALYZE` against the *scratch-only* copy of `metrics.db` made the same archive
boot, with 645,172 rollup rows and the business schema unchanged. Retried
restores with the pre-archive/old image (~8.73 s), pre/new image (~9.60 s) and
post/new image (~8.31 s) succeeded.

"`ANALYZE` made it work" is a property of a query plan, not a property of the
product. The question was which statement, and why its plan was so much worse
before the statistics existed.

## What runs at startup

`InitializeStore` gives the whole open a 30-second context. `metric.Open` then
migrates the schema and calls `recoverPendingCoarseRollups`, which — for every
metric definition, for every adjacent tier pair — runs one statement: the
durable fine-tier buckets whose coarse parent is absent. The pass is what makes
restart recovery idempotent, so it is not optional. It is, however, supposed to
be cheap when there is nothing to repair, and on a healthy store nothing is.

## The shape that matters

Measured read-only from the live OC424 store (counts only, no business data):

| | |
|---|---|
| `metrics.db` | 212,381,696 bytes, WAL |
| rollup rows | 612,362 |
| metric definitions | 27 |
| series | 607 |
| label sets | 1 |
| resolutions | 4 |
| rows per resolution | 223,323 / 219,438 / 159,040 / 10,561 |
| digest blob | mean 233 bytes, max 1,832, 89.2 MB total |

Two numbers explain the failure. First, 27 metrics: a scan that re-reads a whole
tier once per metric does 27× the necessary work. Second, 89 MB of digest blobs:
every one of those re-read rows pulls a blob page behind it.

## Reproducible fixture

`pkg/metric/recovery_scale_probe_test.go` builds that shape at production scale
(27 metrics × 22 series, four tiers, encoded t-digests), then:

1. Optionally runs `ANALYZE` (`PROBE_ANALYZE=1`), which is the difference between
   a store whose statistics exist and a restored one whose statistics are stale
   or absent.
2. Self-checks that the recovery pass finds **zero** children to repair, so the
   measurement is the scan and not a repair of a broken fixture.
3. Times `Open` end to end, and times the pre-fix and post-fix statements over
   every metric and tier on the same data.

It is gated on `NEKOMARI_RECOVERY_PROBE=1` because the fixture is far larger than
the ordinary suite:

```sh
NEKOMARI_RECOVERY_PROBE=1 PROBE_ANALYZE=0 \
  GOTOOLCHAIN=go1.27.3 go test ./pkg/metric -run TestRecoveryStartupProbe -v -timeout 30m
```

## Result

Same 498,960-row, 345 MB fixture; the same empty result in every row.

| Statistics | Pre-fix statement | Post-fix statement |
|---|---|---|
| stale (`PROBE_ANALYZE=0`), pass 1 | **18,213 ms** | **119 ms** |
| stale, pass 2 (warm) | **18,900 ms** | **136 ms** |
| fresh (`PROBE_ANALYZE=1`), pass 1 | 162 ms | 128 ms |
| fresh, pass 2 | 161 ms | 129 ms |

`Open` end to end on the same fixture: 135 ms with stale statistics, 127 ms with
fresh ones — the startup cost no longer depends on whether statistics exist.

The plans say the same thing:

- stale, before: `SEARCH r USING INDEX metric_rollups_resolution_bucket_idx (resolution_id=?)`
  — drive from the wide resolution index, re-reading every series' rows of that
  tier for each of the 27 metrics.
- stale, after: `SEARCH s USING INDEX metric_series_metric_entity_idx (metric_name=?)`
  then `SEARCH r USING INDEX sqlite_autoindex_metric_rollups_1 (series_id=? AND resolution_id=?)`
  — drive from the metric's series, one tier, one series at a time.

## Root cause

The statement filtered both the child tier and the parent tier through a join on
the resolutions table (`JOIN metric_resolutions d ON d.id = r.resolution_id ...
WHERE d.resolution_milli = ?`). That left the resolution id unknown to the
planner as a constant, so the equality was not available as an index constraint.
With statistics present the planner chose the series-driven plan anyway; with
stale statistics it chose the resolution index, and the cost becomes
`metrics × rows in tier`, each row touching a digest blob page. `ANALYZE`
appeared to fix the problem because it changed the choice, which is exactly why
it is a workaround and not a repair.

## The fix

`pkg/metric/coarse_rollup.go`:

1. The resolution row id is resolved once per tier (`resolutionIDFor`) and bound
   as a parameter for both the child scan and the parent probe, so
   `r.resolution_id` / `p.resolution_id` are direct equality constraints and the
   resolutions join disappears. A tier that has never been written resolves to
   id `0`, which matches nothing — the correct answer when no coarse parent
   exists yet.
2. On SQLite the rollup access path is forced to the unique index that leads
   with `series_id` (`INDEXED BY`), so the statement is planned from the metric's
   series outward and cannot depend on statistics that a restore or a large
   recent ingest leaves stale. MySQL and PostgreSQL keep the join-free statement
   and their own optimizer, which has cardinality statistics for the join.

The statement is now built by `coarseRecoveryQuery` so the probe can explain the
exact text that `Open` runs.

## What this does not change

- No schema, index, stored row, archive or encrypted backup is modified, and no
  rollup is dropped: the `NOT EXISTS` predicate and the recovery-and-flush
  sequence are unchanged, so absent parents are still created and sealed parents
  are still never overwritten.
- No production database is analysed or repaired. The `ANALYZE` workaround on the
  scratch copy remains a workaround, and is not recorded as the cause or the fix.
- No RTO, RPO or SLO claim follows from fixture timings.
- MAC-WAN independent-media recovery is still unverified, and this change is not
  a substitute for it.

## Verification

| Check | Result |
|---|---|
| New `TestCoarseRecoveryStatementIsSeriesDriven` | statement binds the resolution as an id, has no resolutions join, forces the series index, and the plan drive order matches |
| New `TestResolutionIDForReportsUnwrittenTierAsZero` | an unwritten tier reports id 0 rather than an error |
| Existing recovery contracts (`TestRestartRecoversUnsealedCoarseParents` and the coarse-rollup/read-index tests) | pass |
| `go vet ./pkg/metric`, `go build ./...` (CGO, Go 1.27.3) | pass |
| Full `pkg/metric` suite | pass |

## Still open

The fix is not production acceptance. Release, OC424 rollout with a fresh
pre-upgrade archive, post-upgrade checks and the still-unverified
independent-media restore remain the gates in
[`HANDOFF-DSH-2026-10-27.md`](./HANDOFF-DSH-2026-10-27.md) §9 and
[`OPEN-WORK.md`](./OPEN-WORK.md).
