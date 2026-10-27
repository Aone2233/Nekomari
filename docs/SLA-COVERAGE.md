# What `Presence.Coverage` measures

Status: clarification. Date: 2026-10-27. This document changes no behaviour: the
field keeps its current value, its name and its meaning. It exists because the
field's name reads as one thing and its arithmetic computes another, and a
number that is misread is worse than a number that is missing.

## The contract

`Presence` separates "did the node report at all" from "was it up", and
`Coverage` is the first half of that:

```
expected = floor((last_data - first_data) / cadence) + 1     # the observed span
expected = min(expected, floor(window / cadence))           # only when window > 0
expected = max(1, expected, observed)                       # keep denominator valid
coverage = observed / expected                             # clamped to [0, 1]
```

`cadence` is the median gap between consecutive buckets **including the empty
ones**, `observed` counts distinct buckets with data, and both counts come from
the same de-duplicated grid. A single bucket has no inferable cadence and
reports `1` if it carried data and `0` if nothing did.

So the field answers: **"of the period this series has evidence for, how much
carried data"** — not "of the window I asked for, how much carried data".

## Worked examples

All one-minute buckets; `first_data`/`last_data` are the first and last buckets
that carried data.

| Situation | span | expected | observed | `Coverage` | Fraction of window |
|---|---|---|---|---|---|
| 60 of 60 minutes reported | 59 min | 60 | 60 | **1.0** | 1.0 |
| first 10 of 60 minutes reported, then silent | 9 min | 10 | 10 | **1.0** | 0.17 |
| 8 of 10 consecutive minutes, 2 missing in the middle | 9 min | 10 | 8 | **0.8** | 0.8 |
| nothing reported | — | 0 | 0 | **0** | 0 |

The second row is the one that gets misread: a node that stopped after the first
ten minutes still reads **100% coverage**, because its observed span has no
interior gaps. Reporting only the last ten minutes gives the same fraction.
The window fraction is 10/60 in either case; the field reports the span fraction.

## Why the span, and not the window

Two reasons, both of which are load-bearing:

1. The window is only an **upper bound** on what the data can be checked
   against. A series whose window is 24 hours and whose archived data starts six
   hours ago has six hours of evidence; dividing by the full window would report
   a 75% "gap" that no bucket is missing from.
2. `window/step + 1` double-counts the endpoints. A complete ten-minute series
   at one-minute cadence spans nine intervals and holds ten buckets; computing
   `600s/60s + 1 = 11` expected slots reports 91% coverage on a series that
   missed nothing — a phantom 9% failure in every dashboard that aggregates it.

## Reading it correctly

- **Freshness / "is this node still reporting"**: use `first_data`, `last_data`
  and the request time, or a dedicated staleness metric. A node that stopped an
  hour ago still reports `coverage = 1` for the window it did report.
- **"How much of the requested window has data"**: the API does not expose this
  directly, and it does not expose the inferred cadence either. It can be
  derived approximately as `(last_data - first_data + cadence) / window × coverage`
  when the caller knows the cadence from the task's interval; without the
  cadence, compare `first_data`/`last_data` against the requested window instead
  of trying to reconstruct it from the fraction.
- **Availability**: `fraction` is computed over the *reported* buckets only, so
  availability and coverage must be read together. No samples means
  `has_data=false`, `fraction=0`, `coverage=0`; it is not a measured 0% or 100%
  availability. Earlier successful samples may still read `fraction=1` and
  span coverage 1 after reporting stops, which is why the timestamps matter.

## What a semantic change would require

Do not redefine `Coverage` in place. It is pinned by `internal/sla/batch_test.go`
and read by dashboards, the SLA incident list and the RPC surface; changing the
denominator silently moves every historical number a consumer has stored or
alerted on.

If window coverage is worth having, the compatible shape is:

1. A **new** field (for example `window_coverage`) with the window as the
   denominator, reported next to `coverage` rather than replacing it.
2. The inferred **cadence and the window-bucket count** reported alongside it.
   Window coverage computed without them is not reproducible by a reader, which
   is the same problem this document exists to fix.
3. An explicit note that a complete series can read `coverage = 1` while
   `window_coverage < 1` when the requested window starts before the data does,
   so the two fields are not treated as duplicates by a reviewer.
4. A pass over the consumers that currently treat `coverage` as freshness —
   dashboards, alerts, the incident list — before any UI text changes.
5. A minimum two-minor deprecation window for any rename, per the compatibility
   policy in `LONG-TERM-PLAN.md`, and a stated migration note for stored figures.

That is a decision for the owner, tracked in `OPEN-WORK.md` under "Numbers and
correctness", not something this document decides.

## What this document does not claim

No runtime change, no RPC field change, no schema change, no dashboard change.
The field's code comment now states the span arithmetic it always performed. No
production number is re-derived here, and the v1.7.4 delivery in
`SLA-GENERATIONS.md` is unaffected: task-generation de-duplication runs before
presence is computed and does not alter the denominator described above.
