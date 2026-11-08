# v1.8 compatibility policy

Baseline: `origin/main` v1.7.30. Branch: `codex/v1.8-probe-truth`.

## Additive-only rule

- No existing field is removed, renamed, or given a new meaning.
- New fields are optional; old Agents omit them and old Panels ignore them.
- New task settings use zero/empty as "legacy single-probe" so existing rows
  do not change behavior.
- No `CheckKomariVersion` change and no version lowering. v1.8.x satisfies the
  same `>=1.6.0` marketplace constraints as v1.7.30.
- Database changes are additive columns and metric definitions. Old binaries
  ignore the new columns; rollback keeps the old data.

## Four-way payload matrix

`protocol/v2/probe_truth_test.go` and `agent/protocol/v2/probe_truth_test.go`
cover:

| Producer | Consumer | Contract |
|---|---|---|
| old Agent | new Panel | legacy fields decode; v1.8 pointers stay nil |
| new Agent | old Panel | old decoder sees `task_id/ping_type/value/value_us/family` |
| new Agent | new Panel | multi-probe/HTTP fields decode |
| old Agent | old Panel | unchanged pre-v1.8 shape |

The shared `PingResultParams`, `PingProbeStats` and `HTTPProbeReport` structs
are compared across both protocol copies by the existing contract tests.

## Upstream gate (policy)

Each release must pin one upstream commit and both market catalog hashes, then
run the 26-plugin / 64-theme fixtures. A failure blocks the release; a plugin
requirement is never lowered.

This branch contains the offline **26-plugin manifest and version gate**
(`internal/plugin/market_manifest_gate_test.go`, pinned 2026-11-08 catalog
fixtures). It validates manifest identity, catalog hashes, manifest rules and
the `CheckKomariVersion` accept/refuse directions at `1.8.0`.

With the hash-verified ZIPs supplied, `TestKomariMarketPluginArchives` installs
26/26. `TestKomariMarketPluginLoadUnload` loads 25/26: all six notification
plugins and the email plugin now load after the additive
`server.registerNotificationChannel` API and the jsruntime TLS module.
`komari-gfw` is skipped as a known synthetic-domain fixture limitation. The
admin channel-selection UI and delivery tests are still pending.

The theme side passes its offline gates: 64/64 manifests match the pinned
catalog, 64/64 archives preflight, and 64/64 install + reinstall in separate
temporary panel directories. Browser rendering, hydration diagnostics and
mobile interaction across those themes remain the pending visual gate.

## Rollback boundary

New binary on an old database: additive columns are ignored by old code.
Old binary on a new database: new columns and metric definitions remain but are
unused; no existing data is rewritten. OC424 acceptance still requires the
pre/post encrypted backup and an isolated restore drill.
