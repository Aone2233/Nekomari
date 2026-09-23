# Structural and optimization review — 2026-09-23

This builds on `docs/OPTIMIZATION-REVIEW-2026-09-22.md` rather than repeating
it. That review listed correctness items (A1–A3), small wins (B1–B5) and a
deferred group (C); this one asks a different pair of questions:

1. What is the frontend's **engineering margin** now — measured, not asserted?
2. Which **structure** is carrying the most risk?

Everything below is measured on `07ae2af` unless stated otherwise. Where I did
not check something, it says so rather than implying coverage.

## What changed since the previous review

The React Compiler migration landed (PRs #19–#26): the advisory audit went from
77 findings to 3, and the three that remain are each recorded as not fixable as
scoped in `docs/COMPILER-MIGRATION-2026-09-23.md`.

One item from the previous review can be closed: **B2 is fixed.** The service
worker no longer precaches the editor. `frontend/vite.config.ts` excludes the
editor chunks and caps `maximumFileSizeToCacheInBytes` at 2 MB, and the built
`dist/sw.js` contains no reference to `FileEditorDialog`. The 3.09 MB editor
chunk is still built and still downloaded on demand, but it is out of the
precache.

The other items in that review (A1–A3, B1, B3–B5, C) were **not re-checked** in
this pass; they are outside the frontend and I did not want to mark them done or
open without verifying.

## Measured margin

### Test reachability — 23 % of the source is reachable from a test

Measured by walking the import graph from every test entry point
(`script/*.test.mjs`, `script/*.browser.tsx`) through relative and `@/` imports:

| | Files | KB |
|---|---:|---:|
| Source files under `src/` | 194 | 1 702 |
| Reachable from a test entry point | **45** | 468 |
| Not reachable | **149** | 1 233 |

**Reachability is an upper bound, not coverage.** Two examples from the
migration make the gap concrete: `RemoteFileTree.tsx` is reachable because
`FileEditorDialog` imports it, but the file-manager fixture never opens the
tree; and `dashboard.tsx` is reachable because a fixture mounts it, but that
fixture never renders `MiniMetricChart`. Both were changed during the migration
and neither change is exercised by a passing test.

### Compound risk — large *and* untested

Size alone is not risk and lack of tests alone is not risk; together they are:

| | Count | Lines |
|---|---:|---:|
| Files over 500 lines | 30 | — |
| …of which no test entry point reaches | **21** | **18 607** |

The largest untested files are the ones the migration touched most:

| Lines | File |
|---:|---|
| 3 004 | `pages/admin/index.tsx` |
| 1 799 | `pages/instance/LoadChart.tsx` |
| 1 196 | `pages/admin/settings/metrics.tsx` |
| 961 | `pages/database_migration.tsx` |
| 933 | `components/admin/AdminPanelBar.tsx` |
| 933 | `pages/terminal/useTerminalPage.ts` |
| 931 | `pages/admin/themes.tsx` |
| 816 | `components/admin/SettingCard.tsx` |

`pages/admin/index.tsx` is the outlier in both dimensions at once: the largest
file in the frontend, and one that no test reaches.

### Bundle

| Artifact | Size |
|---|---:|
| `dist/` total | 9.05 MB |
| `chunk-FileEditorDialog-*.js` (on demand, **not** precached) | 3.09 MB |
| `index-*.css` | 782 KB |
| `entry-index-*.js` | 774 KB |
| `chunk-index-*.js` | 705 KB |
| `chunk-encoding-indexes-*.js` | 530 KB |
| `editor.worker-*.js` | 274 KB |
| Service worker precache | 449 entries, 5.2 MB |

The editor is the single largest artifact and is correctly excluded from the
precache. The next thing worth a look is the **782 KB stylesheet**, which is
precached and paid for by every visitor, including sessions that never open a
terminal or an admin page.

### CI

Three jobs per pull request, all green on every PR in this session:

| Job | Typical duration |
|---|---:|
| `frontend browser (ubuntu)` | ~1 min |
| `build & test (ubuntu-latest)` | ~2.5 min |
| `build & test (windows-latest)` | ~3–4 min |

Total wall clock is about four minutes, which is a healthy margin for adding a
job (see the first recommendation).

## The margin is thin, and here is what that cost

`/admin/settings/sign-on` looped on every visit — React's "Too many re-renders"
(error #301) — because two render-time state adjustments used `null` both as
their "not yet run" sentinel and as a legitimate key value, so the guard was
true on every render. Fixed in PR #26.

It was green on the audit, `tsc`, `eslint`, all 40 unit tests and all five
browser specs. Every one of those checks passed because **no test mounts that
page**, and it is one of the 149 unreachable files above. It was found only by
building the server, running it against a fresh database, installing an account
through the real installer, and walking the routes in Chromium.

That is the argument for the first recommendation below: the migration's
verification was as good as its weakest-covered file, and a whole class of bug
(render loops, bad wiring, crashes on mount) is invisible to component fixtures
by construction.

## Structural observations

1. **Size is concentrated in a few page components.** 30 files hold more than
   500 lines; the top four hold nearly 9 000. These are the files that are
   hardest to review, hardest to test, and most often changed.
2. **Routing is file-based** (`vite-plugin-pages`), which is a good default, but
   it means a page's route is implicit in its filename. Two routes are special —
   `/admin/database-migration` and `/database-recovery` are the only ones not
   reachable from the navigation, which matches their purpose but is not
   discoverable from the page files.
3. **Every admin page load calls `https://api.github.com/.../releases`.** This
   is the panel's update check. Where GitHub is blocked or rate-limited, it
   produces a `403` and a console error on every admin page — which is how it
   surfaced here. It also puts a third-party request in the admin path.
4. **Test hooks have started appearing in production JSX** (`data-log-id` on log
   rows, added so a fixture can address them). Cheap and inert, but worth a
   convention rather than ad-hoc attributes.
5. **The frontend has 6 unit-test files and 5 browser fixtures for 194 source
   files.** The unit tests are genuinely useful (chunk upload, RPC2, frame
   throttling, terminal search effects) but they cover libraries and hooks
   rather than pages, and the pages are where the risk is.

## Recommendations, in order

1. **Make the real-environment walk a CI job.** The script is in this change
   (`frontend/script/panel-smoke.spec.py`); the workflow job that runs it is in a
   separate pull request, because a change to `.github/workflows/` needs a token
   scope this one does not have. The job builds the panel, the embedded theme and
   the server, boots it against a fresh database, installs an account through the
   real installer, and then drives the panel with Playwright over every route the
   navigation exposes, failing on any same-origin console error, same-origin
   failed request, or route that renders nothing. It is the only check that would
   have caught the sign-on loop, and at ~4 minutes of existing CI headroom it
   fits — measured rather than estimated, since it ran green on the pull request
   that adds it. It is also the cheapest durable fix for the coverage gap, because
   it needs no per-page fixture. External origins — the panel's update check calls
   `api.github.com` — are reported as warnings and never fail the run, since a
   blocked third party is not a regression here.
2. **Add fixtures for the largest untested files, starting with the ones the
   migration touched** — `admin/index.tsx`, `LoadChart.tsx`,
   `settings/metrics.tsx`. Each is large enough that its state wiring deserves a
   mounted test, and each was changed recently without one. PR #27 added two
   fixtures elsewhere (`number-picker`, `RemoteFileTree`) but not these three.
3. **Split `pages/admin/index.tsx`.** At 3 004 lines it is larger than the next
   two files combined and is the single worst compound-risk item. The sections
   inside it (`AutoDiscoverySection`, `GenerateCommandButton`, `NodeTable`,
   `EditButton`) already look like separate components.
4. **Take the release check off the page-load path** — cache it, or make it
   explicit. It currently costs a third-party request and a console error per
   admin page in restricted environments, and it is what made the first version
   of the smoke test look like it had 25 failing routes.
5. **Look at the 782 KB stylesheet.** It is the largest precached asset and the
   one every visitor pays for. (For scale: deleting the unreferenced admin node
   table subtree removed 622 bytes of it, which is what a dead component's
   Tailwind classes were costing.)

## What this review did not check

- The Go server and the agent (`agent/`), apart from confirming the server
  builds, installs and serves the panel.
- The previous review's A1–A3 (release workflow, metrics backup, pre-upgrade
  backup timing), B1, B3–B5 and the C group.
- Deployment, Docker, and the release pipeline.
- Anything about runtime performance under load; this review is about structure,
  coverage and artifact size, not about throughput.
