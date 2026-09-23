# v0.1.23: review and next changes

Baseline: v0.1.22. PRs #12-#15 merged into main at
`7c82708ee45a5b5a6ae08dc06220ec7e8aab5f2f`. The panel was released and
deployed on 2026-09-23; the exact-commit gates, image and binary checks, data
backup, production rollback path and runtime observations are recorded in
`DEPLOY-OC424.md`. Agent, protocol, internal and shared-package source was
unchanged.

## Changes and evidence

- Fixed render-time state updates and effect dependency issues in selected
  frontend paths, including theme settings, pagination and selector state.
  Equivalent parent renders preserve the in-progress selector draft. These
  changes reduced React Compiler advisory findings from 111 to 105; the
  compiler has not been globally enabled.
- Added the admin upload scan-duration regression and retained the last
  successful scan duration in statistics. The value is diagnostic; no
  production load benchmark or upload-lock redesign was performed.
- Moved chart accessibility status into the mounted component tree and
  expanded its browser coverage. The Chromium fixture verifies behavior but
  does not substitute for a screen-reader session with live dashboard data.
- The merged main commit passed all three CI jobs. The Release verification
  downloaded binaries and tested an installed server with a reporting agent
  on linux/amd64. Both image jobs and independent anonymous manifest fetches
  passed. The OC424 arm64 image executable matched the published arm64 binary
  before deployment. PR #16 subsequently made anonymous image pullability a
  failing Docker workflow gate; this workflow change is after the v0.1.23 tag.
- PR #17 subsequently added a mounted file manager/editor Chromium fixture to
  CI. Four cases cover rename/delete refresh, stale responses when switching
  nodes, a delayed editor read and manual refresh after reconnect. The RPCs
  use a fixture, so this is frontend state coverage rather than a live agent
  end-to-end test. This work is also after the v0.1.23 tag.
- PR #18 fixed the release verifier's caller-provided work directory lifecycle.
  Its work products now live in a fresh child directory; temporary directories
  are cleaned on failure while caller data is retained. Shell syntax and
  failure paths were tested. This fix is after the v0.1.23 tag.

## Next changes, in priority order

1. Use the mounted file manager/editor regression to fix selection state and
   then ref lifetimes in small, tested batches. Recheck the remaining React
   Compiler diagnostics before enabling the compiler globally.
2. Exercise chart keyboard and announced status with an actual screen reader
   on dashboard data and role/theme combinations. The isolated browser fixture
   alone cannot establish the experience of an assistive-technology user.
3. Use the admin-only upload statistics under representative concurrent
   uploads to measure scan duration and lock contention before changing the
   shared reservation or upload-lock design.
4. Validate OAuth admission with a controlled real provider and production-like
   redirects. Measure editor bundle load and interaction before adding more
   lazy chunks; the current build still warns about the large editor chunk.

For another release, require exact merged-main CI, release artifact and
checksum verification, image smoke tests and anonymous pull checks before a
tagged rollout. On OC424, pull and inspect arm64 first, stop writes, back up
Compose and all bound data, verify the archive and digest, then switch the
pinned image. Check origin/public version, database integrity, recent agent
metrics, loopback binding, logs and probes after restart.
