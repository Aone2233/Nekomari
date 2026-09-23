# React Compiler migration ledger

Baseline for this entry: `415ff6e` (main after PR #19 and #20), 2026-09-23.

This document tracks the advisory React Compiler migration. It is a work ledger,
not a release record: it exists so a later session can resume from the exact
inventory, the batches already attempted, and the evidence behind each claim.

`npm run audit:compiler` (from `frontend/`) runs `script/audit-compiler.mjs`,
which lints `src/` with `eslint-plugin-react-hooks` recommended rules. It is
**advisory and deliberately separate** from the adopted CI lint gate: the audit
exits nonzero while recommendations remain, while `npm run lint` must pass with
`--max-warnings 0`. The compiler is not enabled globally, and no rule is
suppressed to move these numbers.

## Inventory at the baseline

77 findings = 76 errors + 1 warning, across 43 files. Counts are measured, not
estimated, by linting with the same engine the audit uses and reading the JSON
result (the `stylish` output interleaves primary and related locations, so it
miscounts by eye).

| Rule | Count | Severity |
|---|---:|---|
| `react-hooks/set-state-in-effect` | 62 | error |
| `react-hooks/refs` | 12 | error |
| `react-hooks/immutability` | 2 | error |
| `react-hooks/incompatible-library` | 1 | warning |

Migration thread so far, per `docs/FOLLOWUP-v0.1.22.md` and
`docs/FOLLOWUP-v0.1.23.md` plus the measurements above:

| Milestone | Findings | What moved it |
|---|---:|---|
| v0.1.22 baseline | 126 | — |
| v0.1.22 follow-up | 111 | render-time state and effect dependency fixes |
| v0.1.23 | 105 | theme settings, pagination and selector state |
| **`415ff6e`** | **77** | file-manager selection held in state (PR #20) |

The PR #20 change was worth 28 findings by itself: `FileManagerPanel.tsx` fell
from 29 findings to 1, which retired the last `preserve-manual-memoization`
(25 at the v0.1.22 baseline) and all of its `refs` findings. `purity` (4 at the
v0.1.22 baseline) is likewise at zero now.

## Scope of the current batch

The v0.1.22 ledger named the terminal file manager/editor as the next coherent
unit and listed per-rule review targets. This batch follows that list.

| Target file | Rule | Findings |
|---|---|---:|
| `src/pages/terminal/FileEditorDialog.tsx` | `refs` | 10 |
| `src/pages/terminal/FileEditorDialog.tsx` | `immutability` | 1 |
| `src/pages/terminal/RemoteFileTree.tsx` | `refs` | 2 |
| `src/pages/terminal/FileManagerPanel.tsx` | `immutability` | 1 |
| `src/components/admin/NodeTable.tsx` | `incompatible-library` | 1 |
| 8 leaf files (see below) | `set-state-in-effect` | 8 |

The leaf set is a deliberate low-risk probe into the largest category before
committing to it at scale: `src/hooks/use-mobile.ts`, `src/hooks/usePWA.ts`,
`src/components/InlineSvgIcon.tsx`, `src/components/MiniPingChart.tsx`,
`src/components/ui/number-picker.tsx`,
`src/components/admin/DatabaseMaintenanceCard.tsx`, `src/pages/admin/pprof.tsx`,
`src/pages/admin/exec.tsx`.

The two `refs` shapes present in `FileEditorDialog.tsx` are worth recording,
because they recur elsewhere:

- writing a "latest value" ref **during render** (`documentsRef.current =
  documents`, and the same for `activePathRef` / `openChangeRef` /
  `saveActiveRef` / `saveAllRef` / `switchDocumentRef`);
- **calling a ref-reading helper during render** (`buildTabContextMenuItems`,
  `buildStatusContextMenuItems`, `buildTextContextMenuItems`, `renderNode`) and
  reading a ref during render (`documentsRef.current` inside the close dialog
  description).

## What `set-state-in-render` actually rejects (probed, not assumed)

The audit runs `eslint-plugin-react-hooks` **recommended**, which contains 16
rules. All of the following are errors, not warnings:

`rules-of-hooks`, `static-components`, `use-memo`,
`preserve-manual-memoization`, `immutability`, `globals`, `refs`,
`set-state-in-effect`, `error-boundaries`, `purity`, `set-state-in-render`,
`config`, `gating`. Warnings: `exhaustive-deps`, `incompatible-library`,
`unsupported-syntax`.

Because `set-state-in-render` is an error, this batch initially assumed that
React's documented remedy for "reset state when a value changes" — the
render-time adjustment pattern — was unusable, and that a prop-change reset of
locally-mutated state therefore had **no** in-file remedy. **That assumption was
wrong, and acting on it cost several fixable findings.** It was corrected by
probing the rule with three synthetic components rather than by reading about
it:

| Probe case | Result |
|---|---|
| unconditional `setState` during render | `react-hooks/set-state-in-render` **error** |
| guarded prop-keyed: `const [prev, setPrev] = useState(v); if (v !== prev) { setPrev(v); setState(derived); }` | **clean** |
| guarded state-keyed adjustment with a fallback write-back | **clean** |

So the rule rejects only *unguarded* render-phase `setState`; it permits the
documented adjustment pattern, provided the guard makes it converge. The
practical remedy table for a `set-state-in-effect` site is therefore:

| Shape of the effect | Available remedy |
|---|---|
| The state is purely a function of props/other state | delete it, derive during render |
| External store / browser API | `useSyncExternalStore`, or subscribe and only `setState` from the callback |
| Triggered by an event, not a prop change | move the `setState` into the event/async continuation |
| Async load that raised a flag in its own body | pure request + applier; the effect awaits before touching state |
| Prop-change reset of state that is *also* locally mutated | **guarded render-time adjustment**, keyed on the same dependency the effect used |

The last row is the one that was wrongly written off. Its one inherent
difference, which must be stated wherever it is used: the effect painted one
frame with the stale state before the reset committed, whereas the render-time
adjustment applies inside the same render pass, so that stale frame disappears.
That is the same class of change already accepted for `use-mobile`, `usePWA` and
`MiniPingChart`. It is an improvement, but it is a difference, and any *other*
semantic difference means the site must be left alone instead.

A second assumption was also corrected in the same pass: this repo's audit
counts are easy to misread. `stylish` interleaves each finding's primary and
related location, so eyeballing it reads as 81 errors where the true figure is
76. Counts here come from linting with the same engine and reading the JSON.

### Using the guarded adjustment correctly

The pattern needs a "previous value" sentinel, and the obvious spelling —
`const [prev, setPrev] = useState(current)` — is wrong whenever the effect's
**mount** invocation was not a no-op. The guard is then false on the first
render, so that mount write is silently dropped. It is safe only when the mount
run wrote back the value the state already had.

Three sites in this batch were caught by exactly that mistake, and each is a
different flavour of "the mount run mattered":

| Site | Why skipping the mount run changed behaviour |
|---|---|
| `admin/index.tsx` `EditButton` | its state starts at `false` / `0` / `"sum"`, *not* at the node's fields, so the effect's mount run is what seeds the form. Skipping it leaves `hidden` false for a node that is hidden. |
| `dashboard.tsx` `MiniMetricChart` | on a cache hit the fetch effect returns early without writing state, so the mount run is the only thing that can leave the loading state. Skipping it leaves the chart loading forever on any remount that hits the cache. |
| `admin/_layout.tsx` EULA dialog | when settings are already loaded at mount, the mount run is what *opens* the dialog. Skipping it means the EULA prompt never appears on in-app navigation into the admin area. |

The fix is to start the sentinel at a value the dependencies can never equal —
`null` with an explicit `synced === null ||` term — so the adjustment runs once
on mount and then only on real changes. Before converting a site, ask what its
effect did on mount; if the answer is anything other than "wrote the value it
already had", force the first run.

Two related checks are worth making on every site, because they are invisible
otherwise:

- **Does the guard cover the effect's whole dependency set?** `LoadChart`'s
  request key was built from `start|end`, but `metricRangeParams` is memoised on
  `queryRangeSignature`, which also carries `customQueryRevision`. Re-applying an
  identical custom range therefore refetched while the guard stayed false, so
  the series and the loading flag were not reset.
- **Does the guard stay false when the effect re-runs for a reason the key does
  not capture?** Both `MiniPingChart` and `LoadChart` depend on `call`, which is
  memoised and stable today, so a refetch without a reset is currently
  unreachable — but it is the same shape as the `customQueryRevision` gap and
  should be re-checked if that identity ever changes.

## Verification protocol

Every batch must pass all of the following before it is claimed as done, from
`frontend/`:

```bash
node script/audit-compiler.mjs   # scoped findings gone; project total not increased
npm run lint                     # exits 0, --max-warnings 0
npm test                         # 40 tests
npm run build                    # tsc -b && vite build
python script/file-manager.browser.spec.py
python script/selector-state.browser.spec.py
python script/chart-a11y.spec.py
python script/admin-clock.browser.spec.py
python script/compiler-static.browser.spec.py
```

Two caveats that matter when reading results:

- On the clean baseline **all nine checks pass**. The audit's nonzero exit is the
  only "failure" in the set, and it is advisory by design. So a batch that
  leaves the audit nonzero is still a green batch; what must not happen is the
  total going *up*, or a rule that was at zero acquiring findings.
- The browser specs are the only behavioral guard for most of these files. They
  mount real components in Chromium against a loopback Vite server with
  synthetic data; they do not cover a production session.
- This is a client-only SPA (`src/main.tsx` uses `createRoot`; there is no SSR,
  hydration or `getServerSnapshot` anywhere), so reading `window`/`navigator`
  during render is safe here. `StrictMode` is on, so effects mount twice in dev.

Per-rule counts in this document are measured by linting with the same engine
the audit uses and reading the JSON result, because the `stylish` output
interleaves each finding's primary and related locations and miscounts by eye
(it reads as 81 errors where the true figure is 76).

## What the `set-state-in-effect` category actually is

The dominant shape is *"seed local state from a prop (or from an async result),
then let the user mutate it"* — a settings field, a switch with rollback, a
drag-reordered node table, a chart layout. An earlier pass over 15 of these sites
concluded that almost none could be fixed in-file, because the two remedies it
considered — a parent-supplied `key` reset, and the render-time adjustment
pattern — were respectively out of scope and believed to be an error. The second
belief was wrong (see the probe above), and with the guarded adjustment pattern
available this shape is fixable in-file after all.

What genuinely resists, and why:

| Site | What the effect does | Why it resists |
|---|---|---|
| `admin/_layout.tsx:19` | drives the blocking EULA dialog's `open` | `open` is locally mutated **and latching** — once closed nothing re-opens it while settings are unchanged. A latch is not a function of the current dependency, so the guarded pattern does not reproduce it. |
| `dashboard.tsx:567` | `setRefreshing(true)` on the initial-load effect | a spinner flag on an effect-triggered load, not a seed; moving it to the click handler changes the button's state during initial load |
| `dashboard.tsx:1620` | `MiniMetricChart` reads a module-level cache synchronously | the cache is not a subscribable store, so `useSyncExternalStore` does not apply without converting it into one |
| `metrics.tsx:553`, `sign-on.tsx:29`, `:53` | spinner flags raised synchronously by refetch loaders | observable during effect-triggered refetches (language switch, provider switch); deriving means re-encoding the loader's dependency identity in state |
| `number-picker.tsx:28` | mirrors `defaultValue` into `value` **and** calls `onChange` | a genuine prop-change side effect on the parent, so it cannot become pure derivation. Its only call site passes an inline arrow, so the effect also re-runs on every parent render. |
| `NodeTable.tsx:222` | `useReactTable()` | not fixable at all: the rule `throw`s unconditionally at the hook call site (see Status). |
| `RemoteFileTree.tsx:927` | ref read reachable from render | needs a directory-cache restructure, not a state-shape change (see Status). |

Two pre-existing bugs surfaced while reading these sites, both worth their own
change: `RemoteFileTree`'s selection highlight and `FileEditorDialog`'s
cut/copy/undo/redo enabled-state were read from refs during render, so they did
not update until some unrelated re-render (both are fixed in this batch); and
`number-picker`'s `onChange` echo re-fires on every parent render, which resets
the admin log page to page 1 whenever any page other than 1 is selected —
static analysis only, no fixture covers that page, and the correct fix is in
`log.tsx` (memoise `onChange`, or move `setPage(1)` into the picker's own change
handler) rather than in `number-picker.tsx`.

## Status

Three pull requests, each measured against the 77-finding baseline on its own
branch and each verified independently of the agents that wrote its commits:

| Pull request | Scope | Findings |
|---|---|---|
| #21 `codex/compiler-terminal-refs` | terminal editor ref lifetimes — `refs` 12 → 1, `immutability` 2 → 0, `set-state-in-effect` 62 → 59 | 77 → **61** |
| #23 `codex/compiler-effect-setstate` | hooks, charts and components — `set-state-in-effect` 62 → 55 | 77 → **70** |
| #22 `codex/compiler-admin-setstate` | admin and settings pages — `set-state-in-effect` 62 → 44 | 77 → **59** |

Combined, the three branches retire 34 of the 62 `set-state-in-effect` findings,
11 of the 12 `refs` findings and both `immutability` findings, leaving **36**
findings against the original 77. All three branches pass lint, the 40 frontend
tests, the production build and all five browser specs.

Two changes carry a stated one-frame improvement (the two hooks now report the
correct value on first render rather than `false`, and every guarded adjustment
removes a stale frame the effect used to paint before its reset committed), and
one carries a stated edge (an A→B→A `src` flip in `InlineSvgIcon` reuses
already-fetched markup instead of re-showing `<img>`, rendering identical
content). Those are the only accepted differences.

Two findings are recorded as **not fixable as scoped**, rather than as pending
work:

- `NodeTable.tsx:222` (`incompatible-library`, the project's only warning).
  Reading the rule source in `eslint-plugin-react-hooks` 7.1.1 shows the check
  at the hook call site is followed by an **unconditional `throw`**, so it fires
  whenever `useReactTable()` is called in a compiled component body; moving it
  into a custom hook or a child component only relocates it. Removing it means
  abandoning TanStack Table for hand-rolled sorting/filtering/faceting/selection.
  The compiler is not enabled globally, so there is no runtime impact today.
- `RemoteFileTree.tsx:927` (`refs`). Bisected to
  `buildTreeContextMenuItems → buildContextMenuItems → loadDirectory`, whose
  `childrenRef.current[path]` cache read is reachable from render. Reading the
  `children` state instead makes `loadDirectory` unstable, and the reset effect
  keyed on `[loadDirectory, refreshToken, rootPath]` would then re-run on every
  children update and loop. The clean fix is a restructure of the directory
  cache, which is larger than a local change.

### Coverage gap found during review

`RemoteFileTree.tsx` has **no direct browser coverage**: it is rendered only by
`FileEditorDialog`, while `file-manager.browser.tsx` mounts `FileManagerPanel`
and `FileEditorDialog` without opening the tree. The five specs therefore guard
`FileManagerPanel` but never exercise the tree, so those changes rest on
reasoning rather than on a passing test. Every admin page in the
`set-state-in-effect` sweep is in the same position, and `number-picker.tsx`
likewise. Any further work in those files should add a fixture first — the
terminal unit is the model here, since it was only refactored after PR #17
added mounted coverage.

## Remaining inventory

After this batch lands, re-measure rather than extrapolating. The `refs` work is
the part that still matters most, because those are the findings that reflect
real render-phase ref access rather than advisory state-shape preferences:
`FileEditorDialog.tsx` (10 at the baseline) is the bulk of it, and the terminal
unit is where the mounted coverage already exists.
