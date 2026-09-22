# Next improvements after v0.1.19

Application review covers PR #2 (`76e814e`), with the release-toolchain correction
in PRs #3 and #4, and the production baseline on 2026-09-22. Priorities below distinguish
confirmed code behavior from proposals.

## Release toolchain: corrected before production rollout

The v0.1.17 Linux arm64 binary was built with Go 1.26.0 and govulncheck reported
34 standard-library findings. Local source checks had used Go 1.27.1;
their clean results did not establish the safety of published binaries. Binary
scanning is conservative and does not prove exploitability of every finding.
v0.1.19 pins CI and all release builds to Go 1.27.1 and scans every executable
before publishing. It retains symbol tables: v0.1.18's stripped binaries caused
module-level fallback findings for unused OpenPGP packages and blocked publication.
Identical local builds reproduce this distinction; no advisory is suppressed.
Keep compiler maintenance separate from module minimums.
Existing v0.1.16 agents need a staged compiler refresh even though their source
was unchanged by the application review; verify service flags and rollback copies
per host before upgrading the fleet.

## Priority 1: make health probes detect failure

`deploy/panel-probe.sh` lines 32-49 records time_starttransfer but does not inspect
HTTP status or curl exit status. A fast HTTP 500 counts as a successful sample;
a connection failure can return 0.000000 and also pass the latency threshold.
Require curl success, expected HTTP status and a valid API envelope before a
sample can count as healthy. Preserve separate origin and edge timing.
Acceptance: deterministic local tests for 200, 500, refusal and timeout; failed
samples must produce a nonzero exit even when their elapsed time is near zero.

## Priority 1: account for real upload disk use

`web/upload/lifecycle.go` reserves declared payload, not allocated disk bytes.
The 8 GiB limit does not include assembled archives, interrupted temporary files,
extracted content or a staged restore. Production currently has ample free space,
but that is not a portable safety guarantee. Add a platform-specific free-space
check with a reserve floor, a staging expansion budget and startup reconciliation
of orphan temporary files. Use explicit allowlisted files; never delete unknown
directories. Test ENOSPC and interrupted merge/restart with an injected filesystem.

`RunCleanup` currently discards cleanup errors. Emit rate-limited logs and expose
session count, reserved bytes, oldest session and last successful cleanup. Tests
should verify that permission errors are visible without retry/log storms.

## Priority 2: tighten upload client failure handling

`frontend/src/lib/chunkUpload.ts` retries every chunk failure, including permanent
400/404 responses. Cancel uses an unobserved fetch and can lose cleanup when the
store returns 429. Retry only transient failures, honor Retry-After, report failed
cancellation and reconcile it with expiry. Keep init and merge requests bounded;
do not replay a merge after an uncertain response because installation has effects.
Add mocked-XHR/fetch tests for timeout, cancellation, 429 and permanent errors;
the current four frontend tests cover RPC replay, not uploads.

## Priority 2: reduce global upload contention with measurements

One TryLock protects all disk writes and finalizers, including plugin/theme
installation. This is bounded and safe but an unrelated administrator can receive
429 for the whole finalization duration. Instrument duration/rejection counts
first, then separate per-session exclusion from a small global I/O semaphore.
Preserve the invariant that cancel cannot remove an archive in use and quotas
cannot be overbooked. Load-test concurrent independent uploads before changing it.

## Priority 2: complete OAuth compatibility and identity work

Verify real GitHub/generic/QQ flows on an isolated configured instance, including
QQ callback-query preservation, provider changes mid-login and back-button replay.
Production OAuth is disabled, so deployment validation cannot prove those paths.
Generic numeric IDs retain historical float formatting for existing bindings;
move toward exact string IDs only with an explicit migration/rebind policy.
Add per-IP admission to OAuth start (in addition to its global state cap), so one
caller cannot fill all pending slots. Test expiry, fairness and bounded rejection.

## Priority 3: frontend maintenance and accurate operational docs

The first Linux CI race run exposed background notification config reads crossing
test cleanup. The release fixes dispatch ordering and skips background work when
notifications are disabled. Repeated `-count=10` authentication runs also expose
shared rate-limiter/database fixtures (later login runs return 429); fresh-process
runs pass. Isolate these fixtures and add repeatability checks before treating
repeated in-process runs as a stable stress suite.

Resolve hook dependency warnings by checking actual stale closures and request
counts, rather than blindly adding dependencies that may create polling loops.
Separate refresh-only warnings from correctness issues. Upgrade deprecated
Recharts 2 and ESLint 9 in independent changes with chart and rule regression
coverage; keep Vite/TypeScript major migrations separate.

Some historical deployment notes describe old 2FA/production versions in present
tense. Mark historical sections explicitly and maintain one dated current-state
table. Keep credentials out of all reports. Add release checks for both embedded
default UI and the installed production theme, plus authenticated admin smoke
tests when credentials are available through the existing secret mechanism.

## Proposed order

1. Stage the patched agent executables host by host, preserving rollback copies.
2. Health probe correctness and tests.
3. Upload cleanup observability, actual disk budget and client failure tests.
4. OAuth integration matrix and numeric identity migration design.
5. Measured upload concurrency improvements and separate frontend major upgrades.

Do not increase SQLite connections without evidence of pool waits. Existing
live-status/cache optimizations already passed regression coverage; a guest API
latency sample is not evidence that the database is a bottleneck.
