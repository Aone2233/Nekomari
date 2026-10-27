# SLA task-generation normalization (v1.7.4)

The metric store retains separate series when a task's tags evolve, for example
`{task_id}` then `{task_id,protocol,family}`. Those series may overlap or arrive
in tag order rather than time order. The report previously concatenated them by
task ID after normalizing each individual series. Task availability and latency
could therefore count one instant twice, while incidents could repeat or run
backwards. The coverage calculation already deduplicated its input, so it could
disagree with the report's measured bucket count.

## Bounded correction

After grouping by entity and task, the report sorts loss and latency samples
chronologically and merges equal bucket timestamps before any task arithmetic,
presence union or maintenance exclusions. The existing conservative rule stays:
retain the worst **measured** bucket value, not an average across generations.
Counts establish whether evidence exists; they do not weight availability or
bucket percentiles. Empty placeholders never override measured values, including
the latency `-1` sentinel for an all-failed bucket. A task that genuinely measured
zero latency still counts as a measurement.

This is a policy over the bucket means already stored, not probe-level p95/p99
or a claim that differing tag generations are identical raw observations. Node
and task boundaries stay separate. Historical rollups are neither rewritten nor
deleted, and raw metric queries keep their existing series identity. The SLA
wire shape, permission/visibility rules, maintenance handling, two-read query
count and latency-without-data fallback are unchanged.

The separate known coverage issue remains deliberately open: expected buckets
are derived from the observed span, capped by the requested window, rather than
the entire window. This patch does not redefine coverage, fill trailing time,
change outage duration semantics or invent node uptime.

## Verification and release gates

Focused regressions reproduce incorrect loss, latency and repeated incidents
before the fix. Tests cover overlaps, nonoverlapping out-of-order generations,
empty placeholders, failure sentinels, maintenance, real gaps, stable ordering,
unrelated nodes/tasks and the existing wire contract. The six-group installed
synthetic Panel harness seeds only scratch SQLite, exercises actual SLA and
maintenance RPCs, verifies hidden-node filtering and compares reports across a
restart. GeoIP/login notifications are disabled in the scratch configuration
before login; no production credentials or third-party requests are used.

```sh
GOTOOLCHAIN=go1.27.3 go test -race -count=1 ./internal/sla
GOTOOLCHAIN=go1.27.3 go test -count=1 ./web/rpc/jsonrpc -run 'Sla|SLA'
python3 -W error::ResourceWarning deploy/sla-generations-smoke.py --panel-binary /path/to/nekomari
```

Exact-source CI and immutable release/source attestations remain gates, followed
by the attested binary's bounded OVH harness and OC424 continuity/scoped live SLA
reads. Preserve all 14 clients/reporters, Agent tokens, Hermes grants, GeoIP,
installed backup-health state and encrypted backup schedule. No Agent rollout,
rotation, schema/dependency/frontend change or 200-node testing is included.
Fresh pre/post ciphertext and actual MAC-WAN old/new-image recovery remain
separate from synthetic testing. Rollback uses pinned v1.7.3 compose/image while
keeping current DBs and the existing health environment/report state.

Actual deployment, owner-session UI, first nightly backup, independent-media
restore and production SLO/RPO/RTO evidence must not be inferred from this source
change. See [the live work list](./OPEN-WORK.md) and [runbook](./DEPLOY-OC424.md).

## Actual delivery (2026-10-27 17:23:20 Asia/Shanghai)

PR98/source `b0ea91756e700e871491a4c4388757f65ead263e`: exact CI40476853150
7/7, release40478743592 10/10 (attempt 2), Docker40479934401 2/2.
Source/tag/workflow attestations, Linux checksums and both image architectures
verify. Attested binary passes six installed synthetic-Panel groups on OVH.
OC424 healthy/restart0, upgrade9/9, public23/23, forecast14rows and bounded scoped
live SLA reads for 14 nodes/58 tasks pass. Independent preserve-series bucket
comparisons cover 11 tasks over two reporting nodes; none shows multiple
generations in that window. Synthetic overlaps, not the 44 multi-generation
source group count, prove the overlap-specific case.
All 14 UUIDs/tokens/reporters, schemas, key grants/expiry, GeoIP and quiet backup-health
state remain; no Agent/Hermes restart. Pre/post encrypted old/new-image recoveries
pass; scratch-only planner statistics refresh addresses retained startup timeouts
without changing archives/production data. Existing recurring health checks pass;
first nightly backup, owner UI, independent-media restore and SLOs remain separate.
See the runbook for exact pinned images, archives and v1.7.3 rollback.
