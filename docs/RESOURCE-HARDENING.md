# Resource and security boundaries (v0.1.13)

These limits protect normal monitoring work from duplicate calls, expensive
public queries and persistent downstream failures. They are process-wide;
deployments sharing one database should use a single server process. Direct
out-of-process database edits require a restart to refresh config/visibility
caches. In-process GORM writes invalidate the config and visibility caches, and
the administrator SQL endpoint invalidates all of them. The session cache is
invalidated by the revocation calls in the `accounts` package rather than by a
revision counter (see below); a session row deleted by another process is served
from the cache until its entry expires, at most 30 seconds. New explicit
transactions affecting cached tables must invalidate after commit as well as
after their individual statements.

| Work | Limit / behavior |
|---|---|
| Control HTTP / RPC WebSocket / decoded Agent report | 1 MiB |
| Agent v2 control channel (metadata listings, filesystem results) | 8 MiB, with non-metadata messages still held to 1 MiB |
| File transfer | Separate streaming routes retain their own chunk limits |
| RPC HTTP batch | 32 calls |
| Public historical queries | 4 concurrent; a request that arrives while all four are busy waits up to 10 s for a slot, then has a 15-second context deadline |
| Administrator SQL console (`admin:dbQuery`) | 2 concurrent, separate from public chart queries, 20-second context deadline, no metric read budget |
| Metric query input | 32 metrics, 256 entities, 4096 requested points per series, 366 days |
| Metric work | 250000 samples/rollup rows/dictionary entries across one query |
| Metric response | 250000 total points, including tag series and gap markers |
| RPC / legacy status sockets | 256 / 128 connections, 90-second read idle timeout |
| Report / ping queue | 4096 entries each, including failed pending batches |
| Write batch | 256 reports / 512 ping records |
| Retained report payload | 4096 string bytes and 32 GPU entries |
| IP lookup / latency measurement | Same-IP singleflight, 8 / 2 in-flight jobs |
| Session activity | At most one timestamp write per minute per cached token |
| Factor enrollment | 10-minute token, 5 attempts, 128 pending enrollments |

Exceeding a query budget returns an error instead of silently truncating a chart.
Narrow the time window, metric list or node list. A saturated query pool is a wait
before it is an error: the built-in UI does not replay a failed call over HTTP, so
an immediate rejection would be a chart that never loads. The raw-window read
budget conservatively charges an overlapping compressed series before decoding it.
The returned point count can therefore be smaller than the charged work. Internal
metric maintenance does not inherit public read limits.

The session caches evict rather than clear when they fill. Clearing dropped every
live entry at once, so one full cache became a full miss for every logged-in user
(SELECT burst) and one full activity map lost the once-per-minute coalescing for
every token (UPDATE burst). Expired entries go first; only a map that is full of
live entries drops the ones closest to expiry.

The session cache is keyed on the `users` revision, not the `sessions` revision: the
coalesced activity UPDATE bumps the latter, so keying on it emptied all 4096 entries
about once a minute. Revocations invalidate explicitly (`DeleteSession`,
`DeleteAllSessions`, `DeleteOtherSessions`, `RemoveExpiredSessions`), and account
changes still empty the cache because the cached record is a JOIN against `users`.

When the writer is full, new admissions return an error; already queued records
remain available for retry. Failed writes do not advance traffic baselines.
Automatic failed flushes back off from 3 seconds to at most one minute; explicit
flush and shutdown requests still attempt their work immediately.
The queue is memory-only: a process crash or failed final shutdown flush can
still lose pending samples. This patch does not add a durable spool.

The built-in UI requests `common:getNodesLatestStatus` with `include_ping:false`.
Omitting the flag preserves the existing ping-statistics response. Hidden tabs
still pause polling, and the provider still shares one RPC connection.
After a request is sent over WebSocket, errors are returned to the caller without
an automatic HTTP replay. Disconnected clients can still start calls over HTTP.

The Agent updater consumes the existing GitHub release format, keeps the
`komari-agent-*` filenames and requires `SHA256SUMS.txt`. It checks at most three
pages of releases, caps API pages at 8 MiB and binaries at 64 MiB, verifies the
checksum, then uses the previously transitive `go-update` replacement/rollback
implementation. Checksums protect download integrity; they are not a separate
publisher signature. Containers update by replacing their image: the panel's Docker
install command passes `--disable-auto-update` and uses `--pull always`, so re-running
it is the update path, and its `/data` volume keeps the traffic ledger and the
auto-discovery identity across the container replacement.

## Reproducible evidence

Tests run only against in-memory databases, local fake providers and temporary
files. They do not load-test a deployed server:

- `web/router/hardening_test.go`: 10 warm live-status calls make **0 SQL queries**;
  20 authenticated static requests make **at most 1 activity UPDATE**; revoked
  sockets and newly hidden nodes lose access immediately.
- `web/api/ipinfo/budget_test.go`: 8 concurrent same-IP lookups make **4 provider
  requests**, compared with 32 in the original reproduction.
- `internal/metricstore/report_budget_test.go`: after 5 failed flushes the worker
  retains **4096 reports**, rejects additional admission, and accepts again after
  recovery, compared with 20480 retained reports in the original reproduction.
- `pkg/metric/read_budget_test.go`: raw and rollup reads reject excess work before
  further materialization, honor cancellation, and report a corrupt row as a scan
  failure rather than as an exhausted budget.
- `database/accounts/session_cache_test.go`: an activity UPDATE moves the sessions
  revision without forcing a single session SELECT, a revocation still takes effect
  immediately, and a full cache evicts instead of emptying.
- `web/rpc/jsonrpc/public_history_test.go`: an unknown or deleted client UUID is
  refused by metric entity admission and by the record endpoints, a known node is
  admitted, and a hidden node answers exactly like an unknown one.
- `frontend/script/rpc2.test.mjs`: business errors, timeouts and connection errors
  never replay a sent operation over HTTP.
- `agent/update/security_test.go`: a checksum mismatch leaves the old executable
  unchanged; a verified temporary test executable replaces it successfully.

No production CPU, RSS, database I/O or latency reduction percentage is claimed.
