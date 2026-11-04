# 50-node capacity and memory gate

The v1.8.0 exit criterion "50-node capacity/memory gate" from
[`LONG-TERM-PLAN.md`](./LONG-TERM-PLAN.md), measured rather than estimated.

## What is measured

`deploy/capacity50.py` provisions 50 synthetic v2 nodes against a throwaway
panel instance (its own loopback port and data directory, like
`deploy/deploy-verify.sh`), then drives the real ingestion path for a soak:

- each node posts `agent.basicInfo` once, `agent.report` every 3 s with a
  realistic system report, and holds the `agent.pull` event channel. Pull over
  HTTP long-polls server-side for ~25 s, so each node keeps it on its own
  thread — the same shape `agent/fake_agent.py` uses.
- while the fleet reports, the harness samples panel `VmRSS` and
  `/api/public` latency every 5 s and counts every RPC's latency and outcome.

The gates come from the SLO table in `LONG-TERM-PLAN.md`:

| Signal | Gate | Source of the number |
|---|---|---|
| nodes provisioned and listed | 50/50 | `admin:addClient` + `admin:listClients` |
| report success rate | >= 99.9% | RPC outcomes, no queue-full / invalid-token errors |
| panel RSS peak | < 256 MiB | `/proc/<pid>/status` every 5 s |
| report RPC latency | p95 < 300 ms, p99 < 1 s | per-request wall time |
| `/api/public` latency | p95 < 300 ms | per-request wall time |
| server log | no ERROR lines | `server.log` scan |
| post-warmup RSS | no runaway in the plateau region | median(last 5) - median(mid 5) < 8 MiB |

The per-day drift projection is reported but is not a gate: it is too noisy on
a minutes-long window. The `< 1 MiB/day drift after warmup` SLO remains the
standing weekly measurement on OC424, as written in the SLO table.

## Run record, 2026-11-04

Panel binary built from `main` at `2cf1cc8` (the v1.7.26 code plus the v1.7.26
delivery docs), version stamped `v1.7.26`. 50 nodes, 30-minute soak,
4-core/11 GiB host, data directory on tmpfs.

| Signal | Result |
|---|---|
| nodes | 50 provisioned, 50 listed, 0 missing |
| reports | 30047 sent, 30047 accepted (100%) |
| report latency | p50 1.6 ms, p95 2.0 ms, p99 2.5 ms, max 6.5 ms |
| `agent.pull` long-polls | 1503 served, all 200, ~25 s hold (by design) |
| `/api/public` | 360 samples, p95 2.6 ms, max 5.0 ms |
| panel RSS | peak 101.4 MiB (provisioning spike), post-warmup mean 90.4 MiB |
| RSS plateau | 92–96 MiB from minute 12 to minute 30; plateau growth +3.3 MiB |
| Go heap live | flat at 20–21 MB for the last 10 minutes (gctrace) |
| server log | 0 ERROR lines |

Every gate passes.

The early climb from ~69 MiB (post-install) to the ~93 MiB plateau is warmup,
not a leak: Go heap live stays flat while RSS rises, and the curve saturates.
The growth is the allocator's high-water mark plus SQLite page-cache fill
(main DB 8 MiB cache, metric store 4 MiB cache); the steady-state plateau is
the number that matters for capacity.

## Scope and limits

- synthetic nodes exercise report ingestion and the event channel only; no
  ping tasks, exec, terminal, WebSocket push, or plugin traffic.
- the panel runs on one scratch host, not on OC424; this is a capacity
  measurement, not production acceptance.
- the tmpfs data directory changes WAL/page-cache behaviour slightly versus a
  real disk; the memory figure is conservative (tmpfs pages are charged).
- 200-node capacity stays deferred by owner decision; this gate only unlocks
  the revisit, it does not perform it.

## Reproduce

```bash
go build -trimpath -ldflags="-X github.com/Aone2233/nekomari/utils.CurrentVersion=v1.7.26 \
  -X github.com/Aone2233/nekomari/utils.VersionHash=$(git rev-parse --short HEAD)" \
  -o /tmp/nekomari-capacity .
python3 deploy/capacity50.py --binary /tmp/nekomari-capacity --port 25799 \
  --nodes 50 --duration 1800 --workdir /tmp/nek-capacity/gate50
```

Offline unit coverage for the statistics: `python3 -m unittest
capacity50_test` from `deploy/`.
