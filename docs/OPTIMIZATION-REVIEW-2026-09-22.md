# Optimization review — 2026-09-22

What else in Nekomari costs time, CPU, memory, disk, bandwidth or CI minutes, found by a
five-area read-only review of the tree at `bd6580c` and ordered by what to do first.

Method: five independent reviews (panel backend, agent, frontend, data layer, ops/CI/docs),
each required to cite `file:line` and explain the mechanism. The findings that drive the
first batch were then re-read by hand; the **Verified** column says which. Style, naming
and preference items were excluded by construction — everything here has a cost or blocks
future work.

Status values: **todo** · **doing** · **done** · **deferred** (with the measurement that says
why) · **dropped** (with why).

Everything below except C3 landed in v0.1.16; C3 was assessed and deferred, and C7(c) was
dropped, both with the reasoning recorded in place. What each item measured after the
change is in the item itself.

## A — correctness first

These are wrong, not slow.

| # | Item | Verified | Status |
|---|---|---|---|
| A1 | A manual `workflow_dispatch` release stamps `main` as the version | yes | **done** — one `resolve` job feeds all four jobs, and each build asserts the binary carries the tag |
| A2 | `metrics.db` is archived as a raw live-file copy, without its WAL | yes | **done** — `VACUUM INTO` through the metric store's own handle |
| A3 | The pre-upgrade zip is written *after* the destructive migrations it protects | no | **done** — snapshot before `migrations.Run`; the metric store is out of the upgrade archive |

### A1 — the release workflow can publish a version called `main`

`.github/workflows/release.yml:122,135,194` inject the version with
`-X ...CurrentVersion=${{ github.ref_name }}`, while the tag the release is actually built
for is resolved from `github.event.inputs.tag` (`:226`) on a manual dispatch. A tag push
sets `ref_name` to the tag, which is why v0.1.14 and v0.1.15 were fine; a manual dispatch
builds from the default branch and stamps `main` into the server banner and, worse, into
`agent/update.CurrentVersion` — the string the agent's self-updater compares to decide
whether a newer release exists.

Fix: resolve the tag once (there is already a `steps.tag.outputs.tag`), use it in all three
`-ldflags`, and add a step that fails the job when the built binary does not report it.
`deploy/deploy-verify.sh` asserts the server banner but never the agent's version.

### A2 — the metrics database is not backed up consistently

`web/api/admin/backup_whitelist.go:20` lists `metrics.db`, so it is archived by a plain
`copyFile` (`web/api/admin/download.go:164`), while `komari.db` gets a consistent
`VACUUM INTO` snapshot (`:100-121`). The metric store runs in WAL, and `-wal`/`-shm` are
not in the whitelist — `database/dbcore/dbcore.go:476` even deletes them on restore. So a
restored archive silently loses every rollup that was still in the WAL, and the copy can
catch a checkpoint mid-write.

Fix: checkpoint the metric store first (`pkg/metric/maintenance.go:80` already has
`CheckpointWAL` and nothing calls it), or give it its own `VACUUM INTO`; include `-wal` and
`-shm` in the archive.

### A3 — the pre-upgrade backup is taken too late

`database/dbcore/dbcore.go:547` runs `migrations.Run(...)` and only then, at `:554`, writes
`data/backup/upgrade-<ts>.zip`. Migrations drop the `configs` table, rename
`client_infos` and rewrite timestamps, so the archive cannot roll back the upgrade it was
taken for. It also Deflate-compresses the whole `./data` (including the 60-100 MB
`metrics.db`) synchronously during startup, and nothing prunes `data/backup/`
(`deploy/prune-upgrade-backups.sh` now does, outside the process).

Fix: snapshot `./data` before `migrations.Run`, write the version marker after
`config.SetDb`, and stop storing the metric store in that archive.

## B — small changes with measurable wins

| # | Item | Verified | Status |
|---|---|---|---|
| B1 | `include_ping` defaults on, and the three in-repo callers discard the result | yes | **done** — the three callers pass `include_ping:false`; the default is untouched (themes depend on it); the ping-task list is cached and the per-node rollup scans became one batched query |
| B2 | Workbox precaches the whole admin console and Monaco (532 entries) | yes | **done** — 532 → 448 entries, 8.83 → 5.12 MiB (−42 %); all 84 editor chunks still emitted, loaded on demand |
| B3 | The agent scans `/proc/net/{tcp,tcp6,udp,udp6}` every report tick to count sockets | yes | **done** — `/proc/net/sockstat` (+ `sockstat6`), with the table scan kept as fallback; `inuse + tw` keeps the old number's meaning (verified byte-for-byte against the table count) |
| B4 | The agent's traffic sampler reads `/proc/net/dev` every 2 s and rewrites a 31-day JSON every 10 min | no | **done** — sampling 2 s → 30 s (43,200 → 2,880 reads/day/node) and the rewrite is dirty-gated to 30 min (144 → 48/day); a config generation migrates the persisted `detect_interval` so existing nodes actually pick it up |
| B5 | Theme/SPA assets are served without `Cache-Control`/`ETag` and re-read from disk per request | no | **done** — `http.ServeContent` with a stable validator: 304 and Range work, and local theme files are streamed instead of read whole |

### B1 — the live-status poll computes a ping map nobody reads

`web/rpc/jsonrpc/common.go:360` treats an absent `include_ping` as *true*, so
`common:getNodesLatestStatus` runs an uncached `tasks.GetAllPingTasks()` (`:363`) and then
one metric-store rollup scan per node via `getPingStatsForNode` (`:372` →
`tasks.GetPingRecords` at `:63`). The built-in callers never pass the flag and never read
the `ping` field: `frontend/src/pages/admin/dashboard.tsx:482`,
`frontend/src/pages/terminal/TerminalResourceMonitor.tsx:167`,
`frontend/src/pages/terminal/EditorResourceMonitor.tsx:99` (the terminal monitors poll every
2 s). Only `frontend/src/contexts/LiveDataContext.tsx:175` passes `include_ping:false`.

The default cannot simply be flipped: `docs/RESOURCE-HARDENING.md` documents "omitting the
flag preserves the existing ping-statistics response", and third-party themes rely on it.
So: pass `include_ping:false` from the three in-repo callers, and make the server side
cheaper for callers that do want it (cache `GetAllPingTasks` on a revision; batch the
per-node rollup query instead of one scan per node).

### B2 — the service worker precaches 8.8 MB, including Monaco

`frontend/vite.config.ts:97-98`: `globPatterns: ["**/*.{js,css,ico,png,svg}"]` with
`maximumFileSizeToCacheInBytes: 6 * 1024 * 1024`. The built `dist/sw.js` carries **532**
precache entries, including `chunk-FileEditorDialog-*.js` at **3019.9 KB** and 216 Monaco
language chunks under 20 KB. Every visitor downloads all of it before the app is
considered installed, for a code editor that most sessions never open.

Fix: `globIgnores` for the editor/Monaco chunks (or an explicit include list), and drop
`maximumFileSizeToCacheInBytes` back to the default.

### B3 — socket counting reads four proc files per tick

`agent/monitoring/unit/net.go:60-61`: on Linux the primary path is
`procNetConnectionsCount` → `countProcNetFiles(root, "tcp", "tcp6")` (plus udp/udp6), a
full parse of four tables whose size grows with the host's connection count — for two
integers. `/proc/net/sockstat` has the same counts in one ~200-byte read.

### B4 — the traffic sampler is finer than the data it keeps

`agent/monitoring/netstatic/static.go:28` samples every 2 s (43,200 `/proc/net/dev` reads
per node per day), and the flush at `:230-246` sums the buffered samples into one bucket,
so the 2 s resolution is not preserved anyway. Every 10 minutes `:142-158` rewrites the
whole 31-day JSON under the same mutex the report path reads through. The 2 s cadence does
buy one thing: a counter reset inside the window loses only one interval. Measure the live
file size before changing this.

### B5 — no conditional or range requests for theme assets

`web/public/public.go:174-178` serves theme and SPA files with `os.ReadFile` and a manual
`Content-Type`; no `ETag`, no `Last-Modified`, no `Cache-Control`, and the whole file is
read per request (the 0.5 MB background included). The file-manager path in the same file
already sets an ETag (`:313`) and advertises `Accept-Ranges` (`:373`).
`http.ServeContent` gives 304s and Range support for free, which also cuts what Cloudflare
has to pull on a cache miss.

## C — medium changes that only hurt at scale

| # | Item | Verified | Status |
|---|---|---|---|
| C1 | Legacy history reconstructs per metric per node: ~170 store queries for one fleet request | no | **done** — one `SeriesBatch` per tier; a test pins that entities are not merged |
| C2 | Rollup reads are pinned to `(resolution_id, bucket_milli)` by an `INDEXED BY` hint | yes | **done** — entity-scoped reads (≤64 series) force the series unique index instead; plan flips to `SEARCH … (series_id=? AND resolution_id=?)`, benchmark 165.7 → 2.6 ms/op |
| C3 | The main database is pinned to one connection, and its hot reads are uncached | no | **deferred** — see below |
| C4 | Every report body is parsed three times before it is stored | no | **done** — `RawRequest`/`json.RawMessage` on the ingest path: 29.1 → 8.4 µs and 7376 → 1575 B per report |
| C5 | The ping scheduler re-scans `clients` once per task per tick | no | **done** — one lookup per scheduler pass (`sync.Once`), shared by the task goroutines |
| C6 | `GetConnectedClients()` copies the whole connection map, and one caller loops it twice | no | **done** — single-key `GetConnectedClient(uuid)`; the map accessor stays for callers that need it |
| C7 | Frontend: a terminal monitor requests every node, `useSettings()` refetches per mount, five locales load eagerly | no | **done (a, b)** / **dropped (c)** — the terminal monitors pass `uuid`/`uuids`, and `useSettings()` is one shared store (N consumers → 1 request, 0 while cached). (c) is dropped: see below |

### C3 — assessed and deferred

The claim is real: `database/dbcore/dbcore.go` sets `SetMaxOpenConns(1)` deliberately
("SQLite has one writer. Keeping exactly one durable connection also keeps connection-local
cache and WAL limits stable"), and `GetAllClientBasicInfo()` is a full-table read with no
cache on the 2-second live-status path. But at this fleet's size it is not measurable, so
changing the riskiest thing in the list would be change for its own sake. Measured on the
live panel (v0.1.15, single connection), 20-way concurrent requests straight to the
container:

| Endpoint | median | p95 | max |
|---|---|---|---|
| `/api/public` (reads config + sessions) | 6.7 ms | 10.7 ms | 15.0 ms |
| `/api/version` | 3.8 ms | 6.7 ms | 7.8 ms |

No queueing cliff, so there is nothing to fix yet. The other half of the item is already
gone: B1 cached the ping-task read, and C1/C5/C6 removed most of the repeated client
queries. Revisit when the node count is an order of magnitude larger, or when a profile
shows a request waiting on the pool — and if the pool is changed, note that
`sqlitetune` applies its PRAGMAs per connection, while `:memory:` databases would silently
become one-database-per-connection, which is why the metric-store tests pin
`WithMaxOpenConns(1)`.

### C7(c) — dropped

Splitting the locale bundles needs the detected language *before* `i18next.init()`, because
an absent bundle resolves to `fallbackLng: "en-US"` and renders English; and
`addResourceBundle()` emits `added`, which react-i18next's default
`bindI18n: 'languageChanged'` binding does not listen to, so the late bundle would not
re-render anything. Doing it properly means deferring `createRoot().render()` until the
locale chunk arrives (changed initialisation semantics) and rebuilding the language menu,
which reads the module-scope `resources` export. Not worth it for ~300 KB of JSON that
compresses to a fraction of that; the `globIgnores` from B2 already keeps the first install
small.

- **C1** `internal/metricstore/legacy_records.go:62-104` calls `s.Series` once per metric
  (17 of them, `internal/metricstore/metrics.go:31-36`) and `GetRecordsByTime`
  (`:34-55`) loops that per entity: one all-clients request is ~170 queries (~1547 at 90
  nodes) while holding one of the four public query slots. `SeriesBatch` already groups by
  resolution and scans each tier once.
- **C2** `pkg/metric/rollup_read_sql.go:96` adds
  `INDEXED BY rollups_resolution_bucket_idx`; that index is `(resolution_id, bucket_milli)`
  (`pkg/metric/migrations.go:95`), so an entity-scoped read range-scans every series in the
  window and filters afterwards, and can never use
  `UNIQUE(series_id, resolution_id, label_id, bucket_milli)`. Cost grows with series count.
- **C3** `database/dbcore/dbcore.go:531-532` sets `SetMaxOpenConns(1)` for the main
  database; `GetAllClientBasicInfo` (every column, including tokens and long text) and
  `GetAllPingTasks` are full-table reads with no cache, called from read paths.
- **C4** `web/rpc/jsonrpc/report_v2.go:58-64` marshals the generic `Params` back to JSON to
  unmarshal it into a typed struct, after `extractClientToken` already read the body once to
  find a token (`web/api/Auth.go`). `json.RawMessage` avoids both.
- **C5** `utils/pingFamily.go:20-34` runs a `clients` query per task per scheduler tick.
- **C6** `web/agent/connections.go:28-36` copies the connection map on every call; callers
  only need one key, and `adminExec` calls it twice per request.
- **C7** `TerminalResourceMonitor.tsx` / `EditorResourceMonitor.tsx` omit the `uuid` the
  server can use for a single-node fast path; `useSettings()` is called from ~12 mount
  points, each issuing its own full settings request; all five locale files are statically
  imported (~300 KB raw JSON).

## D — operations, observability, documentation

| # | Item | Verified | Status |
|---|---|---|---|
| D1 | A failed `verify` job leaves a published Release with no image and only a skipped job | no | **done** — the image job now gates on the Release existing, not on the whole workflow's conclusion, and `deploy-verify.sh` asserts the agent's version end to end |
| D2 | Nothing probes the live panel's latency or errors | yes | **done** — `deploy/panel-probe.sh` every 10 minutes: origin TTFB (tight threshold) plus public TTFB with the `cf-ray` POP and cache status, one line per run in `/var/log/nekomari-panel-probe.log` |
| D3 | `docs/DEPLOY-OC424.md` tells the reader to copy files from `deploy/oc424/`, which is not in the tree | yes | **done** — points at `deploy/nginx-nekomari.conf` and `deploy/hosts/oc424.service`, one unit name, and the v0.1.15 panel upgrade recorded |

- **D1** `.github/workflows/docker.yml` gates on the whole `release` workflow conclusion,
  which includes the `sleep 20` verification job; one flake there means no image for a
  published Release. Nothing asserts the agent's version either (see A1).
- **D2** `deploy/healthcheck.py` and `deploy/stability-sweep.py` are manual scripts with no
  timer, and the server exposes no `/healthz`. The 2026-09-21 slowness was found by feel.
  A timer that records TTFB (and the `cf-ray` POP) from a couple of vantages turns the next
  occurrence into an alert. `docs/PERFORMANCE.md` records the three commands to run.
- **D3** `docs/DEPLOY-OC424.md:115,121` reference `deploy/oc424/`, which `git ls-files`
  shows is empty and untracked; the same file names the panel unit
  `komari-agent-oc424.service` (`:17`) and `komari-agent-oc424-original-node.service`
  (`:192`). `docs/OPEN-WORK.md:18` still calls production v0.1.9.

## How this was carried out

The review was produced by five independent read-only passes (panel backend, agent,
frontend, data layer, ops/CI/docs), each required to cite `file:line`; the findings that
drive the first batch were re-read by hand, and the **Verified** column records which. The
work was then implemented in the order above — A, B, C, D — by four agents working in
disjoint file scopes, with the release and ops items done directly.

Every item carries its own evidence:

- **Build/test**: `go vet ./...` and `go test ./... -count=1` clean at the repository root
  (40 packages) and in `agent/` (6 packages); `npx tsc -b`, `npx eslint .` (0 errors) and
  `npm run build` clean in `frontend/`.
- **Fail-without-the-fix checks**: the backup test fails with `metrics.db` back in the
  whitelist, the asset test fails with `serveAsset` back on `c.Data`, the rollup-plan test
  fails when the access path is forced unconditionally, and B3's table-scan equivalence test
  fails if `tw` is dropped from the TCP count.
- **Measured outcomes**: precache 532 → 448 entries (−42 %); rollup read 165.7 → 2.6 ms/op;
  report decode 29.1 → 8.4 µs; `/proc/net` reads 43,200 → 2,880 per node per day; the
  settings request count N → 1 per page; the socket count verified byte-for-byte against the
  old table scan on the live host.

Then one release, the panel image and the agent binaries deployed to the fleet, and the live
deployment verified (see `docs/RELEASING.md` and `docs/PERFORMANCE.md`).
