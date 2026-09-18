# Silent failures

A catalogue of places where the system knows something is wrong and does not tell
the user. The symptom is always the same shape: a feature looks broken or missing,
the network panel shows healthy responses, and the only clue is a line in a server
log that nobody reads.

This is a recurring defect class in this codebase, not a one-off. Each entry below
was found by grepping for `logger.Warn` — every warning is a place the system
already has the answer.

## Fixed

| Where | What it did |
|---|---|
| `dbcore.backupOnVersionUpgrade` / favicon / traffic accounting | See `CHANGELOG.md`; each was a case of a wrong value that looked plausible rather than an error. |
| `web/public` favicon | Served the old icon indefinitely with no indication that a replacement had not taken effect. |
| `AdminPanelBar` version banner | Compared against upstream's releases, so it always offered an "update" to a version this fork is ahead of. |
| `utils/pingSchedule` | Skipped address-family mismatches silently; now annotated in the task list. |
| `dbcore.warnIfDataNotPersistent` | Added: says so at startup when the data directory will not survive a container update. |

## Open

### 1. A scheduled job with no next run time never runs, and only logs — **fixed**

`internal/scheduler/scheduler.go`

```go
nextTick := s.Next(time.Now())
if nextTick.IsZero() {
    logger.Warnf("scheduler", "corn job %s has no next run time", name)
    return
}
```

The job returns without ever executing. Whatever it drives — metric rollups, backup
freshness, notification dispatch — simply stops, with no surfaced error. A schedule
expression the parser cannot advance is a configuration mistake that presents as
"that feature does nothing".

**Fixed.** `AddContextFunc` now rejects a spec whose next run is the zero time, so
the mistake is reported to whoever registers it instead of being accepted and then
silently dropped. The reachable case is real, not theoretical: `cronSchedule.Next`
scans a year of seconds and gives up, so `0 0 0 30 2 *` parses cleanly and never
comes due. The runtime guard stays as a safety net and now logs at error level.

Worth knowing about `Next`: the scan is second-by-second, so an impossible spec
costs about 3 seconds of CPU before it gives up. That is why the guard belongs at
registration -- a valid spec finds its match immediately, and an invalid one is
about to be rejected anyway -- but it does mean `Next` is O(31.6M) in the worst
case and should not be called in a loop.

### 2. ip-info degradation is logged, never surfaced

`web/api/ipinfo/handler.go`

```go
// warnIfDegraded 在结果被上游故障降级时留一条日志，方便排查「面板显示不全」。
```

The comment states the intent plainly: the panel showing incompletely is the
expected symptom, and a server log is the only diagnostic. The response does carry
a `meta.warning`, so the information is already on the wire — nothing consumes it.
A theme that rendered a small "some sources unavailable" note would turn an
unexplained blank panel into an explained one.

### 3. Every geoip provider falls back to `EmptyProvider` on init failure

`utils/geoip/geoip.go`, three separate branches

```go
CurrentProvider = &EmptyProvider{}
logger.Warn("geoip", "failed to initialize ip-api service; using EmptyProvider")
```

A failure to initialise — a missing database, a bad config value, a network
dependency — silently degrades geo lookups to returning nothing. Callers get an
empty result, not an error, so nothing downstream can distinguish "no data for this
IP" from "the provider never started".

### 4. A probe knows which address it measured, and never says

Found while investigating reported jitter on a dual-stack target. Task 11/12 point at
`tj-cm-dualstack.ip.zstaticcdn.com` and `tj-ct-dualstack.ip.zstaticcdn.com`, which
resolve to both families. The fleet is mixed:

| probe | families | what it actually measured |
|---|---|---|
| HK04 | v4 + v6 | **IPv6** `2409:8c02:…` — 12.6% loss, p50 200 ms |
| 并行智算云 | v4 only | IPv4 `211.103.90.101` — 0.0% loss, p50 25 ms |
| MAC Server | v4 only | IPv4 — 0.0% loss, p50 9 ms |

One task, two different network paths, plotted as comparable series. The apparent
jitter is the difference between the IPv4 and IPv6 routes to the same hostname, not
instability in either.

The address-family filter added in v0.1.4 does not cover this. It skips a node that
*cannot reach* the target's family; it does nothing about a node that *picks a
different family than its peers*. Both are the same underlying gap: the scheduler
decides per node, but the task is reported as if every node measured the same thing.

**The information exists and is discarded.** The agent resolves the target, connects
to a specific address, and reports only a latency. Nothing carries which address or
family that number came from, so no chart, alert or API consumer can separate them.
Reporting it would let the panel split the series per family and make the comparison
honest — and would make the mixed case visible instead of merely puzzling.

Immediate mitigation, no code required: split such a task into one per family, each
with probes that can only reach that family.

## How to look for more

`logger.Warn` is the index. For each hit, ask:

1. Does the user-visible behaviour change as a result?
2. If so, is there any path by which the user could learn why?

If the answer to (2) is no, it belongs here.

The counterpart is also worth watching: code that *should* warn and does not. The
favicon and traffic-accounting bugs were both of that kind — no warning existed
because nothing had noticed the value was wrong.

## Not in scope

Deliberate quiet paths that are correct as they are: rate-limit cooldowns in the
ip-info cache, per-chunk retry backoff in the file transfer code, and the
JavaScript console bridge. Those degrade gracefully by design and the user is not
expected to act.
