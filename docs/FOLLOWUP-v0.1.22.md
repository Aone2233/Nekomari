# v0.1.22 candidate: frontend state and browser review

Baseline: v0.1.21 on main. This batch changes the frontend and its CI coverage;
server, agent, protocol and shared package source remain unchanged. Release and
production rollout evidence belong in `DEPLOY-OC424.md` after those gates pass.

## Implemented

- Keep the admin menu expansion derived from the route while retaining explicit
  user overrides. Scope configuration-tab selection to the active group and
  cancel stale ping requests when the selected tab changes.
- Register terminal search-result listeners before initiating a search, retain
  synchronous first results, and isolate late events from a previous terminal.
  The regression runs the actual hook with a synchronous search-addon event.
- Extract nested Login, layout, clipboard and price components. Price expiry
  uses one clock value per paid mount; a mounted zero-price to paid-price
  transition takes a fresh reading. Preserve both native and custom OAuth
  triggers' keyboard and click behavior.
- Give Recharts SVGs an accessible name from an explicit label or string series
  labels and a visible keyboard focus outline. A real Chromium fixture checks
  animated data and tooltip values, theme and legend colors, and keyboard
  navigation. A second fixture covers the mounted price transition.
- Make both browser fixtures CI checks alongside Linux and Windows build/test.
  The configured lint gate still allows zero warnings.

## Local validation

All 40 frontend unit tests, lint, TypeScript/Vite build and five Chromium
browser tests pass. Root and agent Go suites pass with the documented live
network exclusions; root and agent `go vet` pass. The browser fixtures use
loopback Vite and synthetic data, not production credentials or an authenticated
dashboard session. The large editor chunk remains a build warning, not a new
failure of the bundle gate.

## Next changes, in priority order

The full React Compiler recommendation inventory fell from 126 to 111 without
enabling the compiler or suppressing a rule. `npm run audit:compiler` remains
advisory and intentionally exits nonzero. Current counts:

| Rule | Count | First review target |
|---|---:|---|
| set-state-in-effect | 64 | SettingCard, admin index, LoadChart; test request cancellation and selection changes |
| preserve-manual-memoization | 25 | FileManagerPanel; retain file-operation and tab identities |
| refs | 15 | FileEditorDialog (10), FileManagerPanel (3), RemoteFileTree (2); test reconnect and editor lifecycle |
| purity | 4 | Review time/random reads against actual render behavior |
| immutability | 2 | File editor/manager state ownership |
| incompatible-library | 1 | TanStack table boundary; evaluate with the library's intended APIs |

The terminal file manager/editor is the next coherent unit of work: first add
mounted regression coverage for file operations, stale responses and reconnects,
then change state/ref lifetimes in small batches. Do not turn on every compiler
recommendation or globally enable compilation while those diagnostics remain.

Use the admin-only upload-stats RPC to measure scan duration, reservation
counts and real contention under representative uploads before revising the
shared upload lock. Exercise OAuth admission against a controlled real provider
and a production-like redirect configuration before claiming provider coverage.
Extend chart testing from the isolated component fixture to dashboard data and
role/theme integration. Measure the editor bundle's load and interaction cost
before deciding whether additional lazy chunks improve the actual user path.

Release only after the exact merged main commit passes all three CI jobs. The
tag must point to that commit, the downloaded Release binaries must pass their
deployment check, and both published image smoke tests and anonymous pulls
must succeed. On OC424, pull first, stop writes, back up Compose and all bound
data with a checked archive and SHA-256, then update the pinned image. Verify
origin and public versions, database integrity, agent and metric progression,
loopback binding, logs and the probe timer; retain the old image for rollback.
