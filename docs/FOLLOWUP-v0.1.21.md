# v0.1.21 follow-up to DSH's v0.1.20

Baseline: main e1902ae, PRs #6/#7 merged, v0.1.20 latest. Work is isolated in
a separate Git worktree; the original checkout is not switched or modified.

## Delivered

- Split eleven context/hook modules from their component providers, plus App
  from the bootstrap and the file-menu hook from its component. Existing provider
  requests, polling and singleton semantics are preserved. Fourteen Fast Refresh
  warnings are eliminated, the rule is an error, and lint allows zero warnings.
- Replace the sole lodash throttle import with a local animation-frame scheduler.
  A burst uses its latest pointer position; drag end flushes before clearing the
  dragging flag; unmount cancels pending work. Remove direct lodash/type packages.
- Add admin:getUploadStats through the existing RPC2 HTTP/WebSocket dispatcher.
  Guest/client roles are denied. It reads a cached snapshot without filesystem
  work or the upload lock. Counts describe the last successful scan, not allocated
  disk bytes. last_scan gives freshness; durations use nanoseconds, timestamps
  RFC3339. last_error can contain filesystem diagnostics and remains admin-only.
  Missing-store scans now clear stale reservation counts.
- Isolate authentication test database records and default limiter state. Named
  in-memory DB flags previously did not reset the process singleton. Install
  revision callbacks once, clean accounts/sessions with t.Cleanup, and restore
  cache maps. CI runs authentication with race detection, shuffle and count=10.
- Adopt seven clean compiler rules: use-memo, globals, error-boundaries,
  set-state-in-render, unsupported-syntax, config and gating. No compiler plugin
  is enabled and no diagnostic is suppressed.

## Validation

Local frontend lint: zero warnings/errors. All 35 frontend tests pass, including
11 real React SSR provider/consumer identity tests and 3 deterministic frame
scheduler tests. SSR does not establish effect or HMR lifecycle behavior.
TypeScript/Vite production build passes. Production dependency audit: zero.
Root/agent suites and vet pass with the existing documented live-network skips.
Authentication race tests pass ten shuffled iterations; upload/RPC race tests pass.
An isolated real backend plus Vite rendered the guest homepage in a browser.
No production credentials or production data were used for these tests.

## Remaining compiler migration

Run npm run audit:compiler from frontend. It intentionally exits nonzero while
unadopted rules have diagnostics and is separate from the release lint gate.
The current full recommended audit has 126 diagnostics:

| Rule | Count | Starting points |
|---|---:|---|
| set-state-in-effect | 70 | AdminPanelBar, ConfigFormTabs |
| preserve-manual-memoization | 25 | FileManagerPanel |
| refs | 19 | FileEditorDialog, useTerminalPage |
| purity | 6 | PriceTags, dashboard |
| static-components | 3 | Login, CommandClipboard, pages/_layout |
| immutability | 2 | FileEditorDialog, FileManagerPanel |
| incompatible-library | 1 | NodeTable / TanStack table |

Move nested components with mount/state regression coverage first. Audit terminal
ref synchronization against reconnects, tab switching and concurrent rendering.
For effect changes, preserve request counts and cancellation, especially TOTP
generation and release checks. Do not enable all rules merely by adding timers,
ref writes or blanket eslint disables.

## Remaining chart coverage

ChartLegend is not used by the application. Animated series, dark-theme rendering
and keyboard navigation still need a dedicated browser fixture and assertions.
Do not claim this release covers those paths. Keep this work separate from the
context split and the existing Recharts 3 migration.

The shared upload lock remains intact; use admin:getUploadStats to measure actual
contention before changing its concurrency model. The panel probe's public-edge
threshold is also unchanged: the earlier distant-POP latency is not evidence of
an origin performance regression.
