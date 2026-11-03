# What `Presence.Coverage` measures

Status: clarification and UI-only improvement released in v1.7.19. The server
field keeps its current value, its name and its meaning. It exists because the
field's name reads as one thing and its arithmetic computes another, and a
number that is misread is worse than a number that is missing.

As of v1.7.24 the same `Presence` carries three new fields next to the pinned
`coverage`, and no existing field changes meaning. They are the second half
the section below used to say the API did not expose:

| Field | Meaning |
|---|---|
| `cadence_seconds` | The inferred reporting cadence in seconds, from the median gap of the whole bucket grid (empty buckets included). Zero when fewer than two buckets carry cadence evidence. |
| `window_buckets` | `floor(window / cadence)` — how many cadence-sized buckets the requested window holds. The denominator for `window_coverage`, deliberately not the span-based `expected_buckets`. Zero when the cadence or the window is unknown. |
| `window_coverage` | `observed_buckets / window_buckets` over the whole requested window, clamped to `[0, 1]`. Zero when `window_buckets` is 0. |

`coverage` and `window_coverage` answer different questions and are never
duplicates: a series that reported every minute for the first ten minutes of
an hour reads `coverage = 1` (its span has no interior gaps) and
`window_coverage = 10/60` (most of the window carried nothing). Readers on
servers older than v1.7.24 must treat the three fields as absent, not as
zero.

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
- **"How much of the requested window has data"**: use `window_coverage`
  (v1.7.24 onward), with `cadence_seconds` and `window_buckets` so the figure
  is reproducible from `observed_buckets`. On older servers the fields are
  absent: derive it approximately as
  `(last_data - first_data + cadence) / window × coverage` when the caller
  knows the cadence from the task's interval, or compare
  `first_data`/`last_data` against the requested window instead of trying to
  reconstruct it from the fraction.
- **Availability**: `fraction` is computed over the *reported* buckets only, so
  availability and coverage must be read together. No samples means
  `has_data=false`, `fraction=0`, `coverage=0`; it is not a measured 0% or 100%
  availability. Earlier successful samples may still read `fraction=1` and
  span coverage 1 after reporting stops, which is why the timestamps matter.

## The v1.7.24 addition

`Coverage` stays pinned by `internal/sla/batch_test.go` and by dashboards, the
SLA incident list and the RPC surface. The window figure shipped as the three
new fields above rather than a redefinition, for the reasons this section
always required: a new denominator must not silently move historical numbers,
and a ratio whose denominator is not reported is not reproducible by a reader.

The window denominator `window_buckets` is `floor(window / cadence)`; it is not
`floor(window / cadence) + 1`, for the same endpoint double-count that the
span denominator above rejects. `window_coverage` is clamped to `[0, 1]` and
zero whenever the denominator is zero, so a no-data window can never divide
by zero. A minimum two-minor deprecation window applies to any future rename,
per the compatibility policy in `LONG-TERM-PLAN.md`.

## UI clarification

The SLA page labels the existing ratio as **span** coverage and displays the
first and last observed bucket starts for the node aggregate and each task, with
the viewer's timezone. A bucket start is not the actual last report time; it may
be coarse (the page displays the bucket width). A full span can still represent
only part of the selected window, and the last bucket is not a live-status check.
Maintenance buckets are excluded. Zero observed buckets show no data, not a
timestamp or a measured zero. These timestamps are observations, not a derived
full-window fraction. When the server reports the v1.7.24 fields, the page also
shows the window coverage beside the span figure for the node aggregate and
each task, labelled separately; a report without them renders as before.

The node aggregate in this report is the union of its **ping task** results,
not resource-metric or live WebSocket presence. An absence of ping data or a
ping reporting gap is not proof that the agent or host was down; a node with no
configured ping tasks can have no SLA data while reporting normally elsewhere.
Trailing gaps are not filled to the end of the requested window. The text is
shown on both the admin and standalone report, in all supported locales.

No existing calculation, RPC field, schema, saved data or alert changed
meaning: v1.7.24 only adds the three fields documented above. This does not
establish SLO measurement or actual downtime. The v1.7.4 task-generation
correction remains unaffected. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md).
