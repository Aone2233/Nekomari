# Node trust: one number, computed from what the panel already stores

Status: design for a v1.9 slice. Not implemented, not released, not deployed.
Nothing here changes an existing API, a probe, or the SLA.

## Why this exists

The panel already refuses to invent data point by point: `internal/metricstore/
report_mapping.go` drops a field whose reported `quality` is not `"ok"`, HTTP
and ICMP failures are classified instead of being called packet loss, and the
public metric API already returns a `quality` string per point
(`web/rpc/jsonrpc/public.metric.go`). What is missing is any statement about a
node as a whole. An operator looking at a node sees charts, and the charts do
not say whether the line is trustworthy, stale, or the product of a drifting
clock.

The four things that presently make a node's data unusable are all silent:

- probes that never ran (scheduler lag, a stopped agent, a pinned task whose
  options are ignored);
- probes that ran and were properly classified as unknowable, which look the
  same as "no data" in most views;
- a clock that is minutes off, which misattributes RTT windows, traffic cycle
  buckets and forecast samples, while every individual sample still reports
  `quality: ok`;
- aggregation that no longer matches the raw rows it was built from.

Each is currently discovered by accident. The point of this slice is to compute
the answer once, store it, and show one number.

## What is deliberately not in scope

- No new probe type, no change to probe semantics, no SLA change.
- No per-sample provenance in the interface. The provenance fields exist so the
  score can be recomputed and explained through the API, not so they can be
  browsed.
- No alerting on the score. Alerts stay on the underlying conditions; a formula
  change must not be able to page anyone.
- No public marketing surface. The score is admin data first.

## Definition

For one node and one window (default: the last complete hour, and a rolling 24
hours for the card):

> **trust = the share of the node's scheduled measurement work in that window
> whose result is both present and usable, discounted by every condition that
> makes present data unusable.**

It is computed only from data the panel already has: task definitions, stored
metric points, stored rollups, and the agent's own reports. It is recomputable:
given the same window and `formula_version`, two runs must produce the same
number, and a later formula can be applied to old windows because the raw
inputs are retained.

### The formula

```
score = null                         if any factor is uncomputable
score = round(100 × Π factorᵢ)        otherwise, factorᵢ ∈ [0, 1]
```

Multiplication is the contract, not an implementation detail. With a weighted
mean, a node with a five-second clock error and a perfect completeness rate
still scores in the nineties; with a product, one unusable dimension drives the
number where it belongs. A single zero means "do not use this data", which is
the only honest summary of one broken dimension.

Factors, each with the exact input and the threshold at which it reaches 0:

| Factor | Computed from | 1.0 when | 0.0 when |
|---|---|---|---|
| `completeness` | scheduled slots for each task in the window ÷ points actually stored | every scheduled sample exists | no sample exists |
| `usability` | quality outcomes counted at ingest — `ok`, unknowable, dropped — for the window; a sample the agent classified as unknowable is counted as such and is **not** silently treated as missing | 0 % unusable | ≥ 20 % unusable |
| `clock` | \|offset\| between agent and panel: the smallest observed (panel receive time − agent sampled time) for the window, which is a lower bound on the one-way delay and therefore an upper bound on the offset | ≤ 10 ms | ≥ 200 ms, or no report carried both timestamps |
| `reconciliation` | coarse buckets whose `count`/`sum`/`min`/`max` disagree with the fine rows they cover ÷ compared buckets | 0 mismatches | any mismatch outside the in-flight grace period |
| `schedule` | p95 delay between a task's due time and the timestamp of the sample it produced | ≤ 1 s | ≥ 30 s |

Interpolation between the two thresholds is linear; the thresholds are
constants in one place, not spread through the code, and they change only with
a `formula_version` bump.

`completeness` needs a definition of "scheduled slot" that does not depend on
guessing: a task with interval *i* over a window of length *w* has
`floor(w / i)` slots, minus the slots before the task was created. A task that
was disabled for part of the window has its window shortened accordingly; the
stored definition is what the panel would have run, never what it wishes it had
run.

### The null rule

If any factor cannot be computed — the node has no agent report yet, the window
contains no scheduled work, the store cannot be read — the score is `null` and
the API says which factor was missing. It is never 0 ("everything is broken")
and never 100 ("nothing to check"). This is the same rule the probe path
already applies to a field whose quality is unknown, applied one level up.

## Storage and scheduling

- One table in the metrics database, `node_trust`, written through the existing
  metric store rather than by opening the file directly:
  `(node_id, window_start, window_end, score, components JSON, formula_version,
  computed_at)`, primary key `(node_id, window_start, formula_version)`.
- Hourly rows for every node; the 24-hour card value is a query over them, not
  a second table.
- Retention: 90 days, longer than the metric retention that feeds it, so a
  dispute about last month can still be answered.
- One job in `internal/scheduler`, running once per hour, bounded: aggregation
  queries only, no per-point materialisation in Go, reading through the metric
  store's read pool and writing through its writer path rather than opening the
  file, and no work when the window is already computed. Cost must be measured on a 50-node scratch database before
  the job is enabled.
- Recomputation: a manual admin call may recompute a window with a named
  formula version. Old rows are never rewritten in place under a different
  version.

## API and interface

- `admin:getNodeTrust` (`nodes:read`): `{node, window, score, components,
  formula_version, computed_at, missing_factor?}`. `components` carries the five
  factors plus their inputs, so a support question can be answered without
  guessing.
- CSV and the metric API gain a per-row `quality` column (already present in one
  API, missing in the export), because downstream consumers such as Hermes
  should not have to infer trust from a chart.
- Interface: one number on the node card, and on the node detail page a single
  line naming the dominant factor when the score is not 100 — for example
  "时钟偏移" rather than a dashboard. No factor table, no provenance panel.
  `null` renders as an em dash, never as 0.

## Prerequisites: four things that already make data untrustworthy

These are not optional polish; each one either feeds a factor or contradicts the
definition above.

1. **Windows load average is reported as 0.** A value that cannot be measured is
   being published as a measurement. It must report `unknown` (and be dropped by
   the existing quality gate) before `usability` can mean anything on Windows.
2. **The panel's traffic cycle day is global; agents rotate on their own day.**
   `internal/config/settings.go` (default 1) is one number for every node, while
   `--month-rotate` is per agent, and the deployment uses 1/7/10/11/23 by node.
   Cycle-scoped completeness and forecast attribution are therefore computed
   against the wrong boundary for most nodes.
3. **`disk` unknown has no reason.** The quality gate correctly drops the field,
   but nothing records *why*, so `usability` cannot distinguish a broken agent
   from an empty optical drive.
4. **The report carries the timestamps and the clock is still unobserved.** The
   wire format already has `sampled_at` (agent clock) and `received_at` (panel
   clock), and `Quality` per field — but the store keeps neither, so neither the
   clock factor nor the usability factor can be computed from stored data. They
   have to be counted at ingest, which is a schema and write-path change, not an
   agent change. The v1.8.1 calibration record lists clock changes as an open
   limitation for the same reason.

## Measurement (2026-11-17)

`deploy/trust-budget-probe.py` builds a throwaway database with the store's own
schema and times the candidate queries: 50 nodes × 6 metrics (300 series), one
minute rollups for a day, five minute rollups for a week, 1.04 M rollup rows,
95 MB, SQLite on the same host as the panel.

| Query | Window | Time |
|---|---|---|
| completeness | 1 hour, all series | 7 ms |
| completeness | 24 hours, all series | 184 ms |
| schedule lag (average and maximum first-sample offset) | 1 hour | 9 ms |
| reconciliation, bucket-aligned join | 1 hour | 1.26 s |
| reconciliation, totals-only join | 1 hour | 16 s |
| reconciliation, coarse-driven, planner's index | 1 hour | 1.28 s |
| reconciliation, coarse-driven, forced unique index | 1 hour | 0.51 s |

Three things follow, and two of them change the design above:

1. Completeness and schedule lag are cheap enough to run hourly over every
   series. The production fleet is about 100 series, so these numbers are an
   upper bound rather than a forecast.
2. Reconciliation is not cheap and its cost is a property of the query shape,
   not of the data: the totals-only join is thirty times the bucket-aligned one
   because the planner multiplies the window against itself, and the
   coarse-driven shape halves again once the inner lookup is forced onto the
   unique `(series_id, resolution_id, label_id, bucket_milli)` index. The job
   therefore compares a rolling two-hour slice every hour with the coarse-driven
   query and a forced index, and sweeps retention in nightly chunks — never a
   whole retention window inside one request.
3. `clock` and `usability` cannot be computed from anything the store keeps
   today: no rollup row records when the panel received a sample, and none
   records the quality outcome of a report field. Both exist on the wire only.
   Recording them at ingest is a prerequisite, listed above as item 4.

## Failure modes this must not introduce

- **A green badge that hides a red reason.** Mitigated by `components` in the
  API and by the single dominant-factor line; the score must never be the only
  way to learn that a node's clock is wrong.
- **Formula churn.** Thresholds live in one table with a version; changing them
  bumps the version, and old windows keep the version they were computed with.
- **Alert coupling.** No alert reads `score`. An alert on `clock` or
  `reconciliation` is fine and desirable; an alert on the score turns a
  threshold edit into a page storm.
- **Cost on the single writer.** The job is bounded and measured before it is
  enabled; if it cannot meet its budget, it runs less often rather than holding
  the connection longer.

## Acceptance

Synthetic fixtures only, never production data:

- a perfect window scores 100 with all factors at 1;
- each factor independently driven to 0 produces a score of 0, and the API names
  that factor;
- a window with no scheduled work, or a node that has never reported, returns
  `null` with `missing_factor`, never 0 or 100;
- an agent that reports no clock offset is treated as `clock = 0`, not as
  perfect;
- the same inputs and `formula_version` produce byte-identical rows on two runs,
  including after a restart;
- a rollup mismatch inside the in-flight grace period does not fail the window,
  and one outside it does;
- a formula-version bump leaves old rows untouched and recomputes only on
  request;
- cost on a 50-node, 30-day scratch database stays within the measured budget
  recorded with the implementation;
- the node card shows the number, the detail page names one factor, and `null`
  renders as an em dash;
- nothing in the alerting path reads the score.

Deployment follows the usual gates: exact-commit CI, fresh off-host backup,
release artifact, OC424 verification, and owner acceptance of the rendered
number on a real node.

## Open questions

- Should `schedule` use the agent's own clock for the due time, or the panel's?
  Using the panel's folds clock error into schedule lag twice; using the agent's
  trusts the clock that `clock` is measuring. Suggested: panel clock for the due
  time, and let `clock` catch the rest.
- Should a node with a score below a threshold be excluded from SLA and
  forecast automatically, or only labelled? This design labels; automatic
  exclusion changes historical SLA numbers and needs its own decision.
