# v0.1.25: post-release review and next work

Baseline: merged main and tag `v0.1.25` at
`5373c3700f125fe30b5832b1f3b199ada9140c90` (PR #36). The exact-commit
CI, Release and Docker jobs passed; the published arm64 image and binary matched.
The dated OC424 backup, rollback and runtime evidence are in `DEPLOY-OC424.md`.

## What this release addressed

- Admin node polling now keeps its last valid state across HTTP and malformed
  responses, reports errors and recovers on a good poll. Older in-flight polls
  cannot replace newer results.
- Rejected node mutations and offline-notification saves keep the dialog and
  draft available for retry. A previously missing node ID is sent when a node
  gets its first offline-notification configuration.
- Mounted browser fixtures exercise these failure and retry paths in CI. The
  unused `@tanstack/react-table` package was removed.

These fixtures use controlled responses. The panel smoke test walks real
installed routes but does not execute every authenticated admin mutation. The
release checks therefore establish build, startup, navigation, packaging and
the specific mounted regressions; they do not establish every write-path or
assistive-technology behavior in production.

## Next checks and improvements

1. **Admin write paths and structure.** Add a small authenticated server-backed
   regression for a representative node edit and offline-notification save:
   reject, preserve the draft, retry, then verify persisted state after a fresh
   read. Continue splitting `frontend/src/pages/admin/index.tsx` by existing
   sections with behavior held by tests. Its 3,004-line size in the structural
   review was measured before this release; the new fixtures cover specific
   branches, not the whole file.
2. **Largest uncovered frontend paths.** Add mounted state regressions for
   `pages/instance/LoadChart.tsx` and `pages/admin/settings/metrics.tsx`, with
   stale-response and navigation cases. Recompute test reachability after the
   new fixtures; the structural review's 52/189 figure is a historical import
   graph measurement and an upper bound on coverage, not a current percentage.
   Keep React Compiler advisory checks in CI, but do not enable the compiler
   globally on the strength of zero diagnostics alone.
3. **Accessibility and integration checks.** Exercise chart keyboard and
   announced status with a screen reader and live dashboard data. Validate
   OAuth admission against a controlled real provider with production-like
   redirects. The existing browser fixtures do not prove either integration.
4. **Measure before performance changes.** Use concurrent admin uploads to
   measure scan duration and reservation-lock contention. For a first visit,
   measure the service worker's network transfer and cache contents before
   narrowing its route precache. The editor is already excluded and loaded on
   demand; the previous review measured roughly 1.9 MB gzipped across 540
   non-editor files, so splitting the editor or trimming the CSS is not the
   first performance change to make.

For another tagged rollout, require exact merged-main CI, release checksum and
downloaded-binary checks, Docker startup and anonymous-pull checks. On OC424,
pull and inspect arm64 before stopping writes, back up Compose and all bound
data, verify the archive and digest, switch the pinned tag, then check origin
and public behavior, database integrity, fresh agent samples, loopback binding,
logs and the panel probe.
