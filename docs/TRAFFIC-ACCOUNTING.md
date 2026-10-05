# Agent/panel traffic accounting — what was wrong, and what changed

The probe's job is to report numbers that are true. This file records a defect that made two of them false, the
diagnosis, and the changes, because the same *shape* of mistake is easy to repeat.

## The symptom

One node's card showed ~39 GB per direction of "today's traffic" while the same node's hover showed ~80 GB, and
the interface itself had carried **0.29 GB since boot** with a measured rate of ~0.05 GB/day.

Two single samples explained it. In the stored series:

```
02:21  − 06:11   readings between 14 KB and 8 MB       (normal)
06:12            single reading of 40,975,184,225 B    (≈ 41 GB)
09:58            single reading of 41,158,277,032 B    (≈ 41 GB)
```

Those two numbers are not traffic. They are the node's **cycle cumulative** recorded as if it were one
interval's traffic.

## Why it happened

`Network.TotalUp/TotalDown` carried **two different meanings depending on the agent's configuration**:

| `--month-rotate` | what the field actually held |
|---|---|
| set | netstatic cycle cumulative since the reset day (~41 GB) |
| unset | kernel lifetime total (~0.29 GB) |
| netstatic failed | fell back to the kernel total mid-stream |

The panel treated the field as a kernel counter and computed a delta against a stored baseline. When the
meaning switched, `current < previous` fired, and `TrafficCounterDelta`'s reset branch returned **the whole
current value as the increment** (it is designed for a counter that restarted from zero; the current value was
below its 1 TB sanity cap, so it was accepted).

The agent never reported the kernel total on its own — it computed it internally for the rate and discarded it.

Two smaller defects compounded it, and one is why this survived so long:

- the cycle cumulative was `sum(samples in [reset day, now])`, and samples are pruned by `DataPreserveDay`.
  A cycle longer than the retention window therefore made the cumulative **shrink**, which reads as a reset.
- netstatic's interface scope (an allowlist) and the kernel totals' scope (`--exclude-nics`) were **different**,
  so the two numbers could not be used to check each other.

## What changed

**The agent** (`agent/`) now reports both quantities separately, so neither has to be guessed:

- `net.total.*` — always the kernel lifetime total. Monotonic, respects `--exclude-nics`, and the only
  legitimate subject of a difference.
- `traffic.*` — always the cycle cumulative, from a persistent per-interface accumulator that is **independent
  of sample retention** (it is updated as samples are taken, so pruning cannot shrink it). It rolls over at the
  reset day, which is now passed into netstatic rather than applied at read time.
- When netstatic is unavailable the cycle fields report `0` with an error, instead of silently substituting the
  kernel total. A missing number is better than a number with a different meaning.

**The panel** (`internal/metricstore/`) stores each as-is:

- `net.total.*` — raw kernel counter.
- `traffic.*` — raw cycle cumulative, no delta. It is a running total, so readers take the **last** value.

**The readers** — a reader has to say which question it is asking, because the two answers come from different
series:

- **"How much moved in this window"** reads the validated interval amounts. The client asks for `traffic.*` with
  aggregation `sum`, which `publicMetricStorageKey` redirects to `traffic.interval.*` (the series
  `reportIntervalPoints` derives on ingest, and refuses to bridge a restart, a gap, a reset or a changed counter
  epoch). These points are additive, so summing buckets is the window's traffic. The admin dashboard, which asks
  for exactly that, is the reader this applies to.
- **"How much has this cycle used so far"** reads the running total itself, with `last`. That is a legitimate
  question and unaffected. What is *not* legitimate is adding those readings up: one per bucket multiplies the
  cycle total by the bucket count.

`theme-luminaplus` answers the same question the other legitimate way: it asks for `last` and differences
adjacent readings itself, treating a decrease as a cycle rollover (`src/utils/trafficStats.ts`), where the admin
dashboard uses the panel's validated interval sums. The instance load chart plots the series under the
aggregation the reader picks, and the backend rejects `delta`/`rate` on these keys with *"use sum for validated
interval amounts"*.

## The same mistake, a second time (2026-10-05)

The 24 h dashboard card asked for `traffic.up` / `traffic.down` with `last` and then added the returned points
together, so the cycle total was counted once per chart bucket. Reported from the live panel: one node showed
**36.59 TB up / 51.16 TB down for 24 h** whose own peak rate was 5.71 MB/s — a full day at that peak is 493 GB,
so the display was ~178× its own physical ceiling, and the fleet total read 86 TB up. The card had kept `last`
from the change above, which predates the interval series; when `sum` was later redirected to those intervals,
the dashboard needed to move back and did not.

Two things came out of it, both in `frontend/src/utils/trafficSummary.ts`:

- The aggregation is `sum` again, and the module is pure and unit-tested so the request and the arithmetic are
  pinned together (`frontend/script/trafficSummary.test.mjs`).
- **`sum` alone is not sufficient**, because the panel answers an entity it has no interval data for with the
  cycle counter read with `last`, marked `semantics: "billing_cycle_cumulative"` and quality
  `traffic_intervals_unavailable_legacy`. The card now checks the marker: only `interval_delta_v2` points are
  summed, and a node whose answer is a cumulative is reported as **unknown** — excluded from the totals, listed
  in the Top-5 as having no data, and counted in a note under the fleet total. Writing zero there would be the
  other half of the same bug, because the node's cycle total is real; it is the window figure that is missing.

## Consequences to be aware of

- **A node running the previous agent will still write a meaningless `traffic.*`.** The panel stores what it is
  given. Deploy the panel first (it tolerates the old agent), then the agents.
- **Cycle totals start from zero** on the node that first runs the new agent, because the accumulator is new.
  The first cycle after the upgrade under-reports; the next one is correct. There is no way to reconstruct the
  prior cycle from a value that was never recorded correctly.
- **The reset-day clamp is 28** for months without a 29th–31st. A reset configured for the 31st fires on the
  28th in February — consistent with `utils.GetLastResetDate`, and asserted in the tests.
- **A reader that differences the counter cannot tell a jump in usage from a jump in meaning**, which is the
  phantom 41 GB sample this document opens with. `theme-luminaplus` takes that route for today's traffic, so an
  old agent still reporting the other quantity is counted as a spike there; the interval series is the only
  reading that can separate the two, which is why the admin dashboard uses it and reports "unknown" where it is
  absent.

## Tests

`agent/monitoring/netstatic/cycle_test.go` pins the properties that failed:

- the cycle total **survives a purge that deletes every sample** (the retention dependency)
- it resets when the reset day passes, without carrying the previous cycle over
- a stale cycle reads as empty and a *read does not mutate state* (zeroing belongs to the sampling path)
- no cycle is recorded at all when `--month-rotate` is unset
- the reset day is the most recent past occurrence and never a future date

`internal/metricstore/report_test.go` keeps its previous intent — one point per minute, restarts, terabyte
scale, counter wrap, tiny dips — but asserts the delta behaviour on `net.total.*`, which is the metric that is
actually a counter, and asserts `traffic.*` is stored raw.

`frontend/script/trafficSummary.test.mjs` pins the reader side: the aggregation asked for is `sum`, interval
amounts add up to the window's traffic, a missing interval contributes nothing, and a series that is a cycle
cumulative — or carries no interval marker at all — is reported as unknown instead of summed. One case computes
what the snapshot reading would have shown (288 buckets × a 25 GiB cycle total ≈ 7 TiB against ~287 MiB of real
growth) so the inflation is a number in a test rather than a surprise in production.
