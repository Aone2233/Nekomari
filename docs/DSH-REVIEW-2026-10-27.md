# dsh delivery review (2026-10-27, Asia/Shanghai)

Scope: changes after `52c24ce` through `8793351`, including the immutable
v1.7.5 tag `c84c3cc62d5a0d63443c91c1bac367125c56317c`. The existing SQLite
series-driven startup optimization and span-coverage contract are retained.
Live read-only preflight confirms v1.7.5 on OC424, healthy/restart0. This is a
dated review, not another live backlog; current status is in `OPEN-WORK.md`.

## Findings

1. **P1: surplus recovery SQL arguments break MySQL/PostgreSQL startup.**
   `pkg/metric/coarse_rollup.go` in v1.7.5 returns five arguments for three
   placeholders. Once a fine tier exists, both actual drivers reject recovery
   (`expected 3 arguments, got 5`). SQLite tolerates the surplus; the new
   v1.7.5 test even expected five. Correct the argument list, assert all dialects
   and run actual disposable database recovery tests in CI.
2. **P2: MySQL parent-bucket division does not floor.** `/` returns a decimal
   quotient, so a nonaligned child fails to find an existing sealed parent and
   is restaged on each recovery. This predates dsh's change but is exposed by
   the missing cross-backend regression. Use `DIV` for MySQL; verify minute 2
   within a five-minute parent, not only a child exactly at its parent's start.
3. **P2: current runbook/work-list headers still report v1.7.4.** v1.7.5 is
   documented at the bottom, while the top checkpoint and startup guide still
   say deployment is pending. Reconcile those current statements; retain old
   dated evidence and do not retag/rebuild already published assets.
4. **P3: SLA clarification has inconsistent examples.** The six-hour example
   says data begins twenty hours ago; the all-missing example incorrectly
   implies 100% availability. Correct those and document the denominator's
   observed-count guard without changing runtime coverage semantics.

## Bounded correction and evidence

The reviewed v1.7.6 candidate addresses these findings without schema/auth/API/Agent,
frontend or dependency changes. Local old-source overlay tests reproduce both
backend arity errors. After the arity correction alone, a nonaligned child
reproduces the MySQL sealed-parent error. The complete fix passes actual
MySQL 8.4/PostgreSQL 17 recovery under the race detector, preserving value/count
and excluding already sealed parents. The existing SQLite plan and restart
recovery tests pass. The added CI database job is scoped to this recovery path;
existing full release gates remain intact.

Evidence stays private under the local text pointer
`.runtest/review-dsh-current`; it includes failed attempts and successful
regressions. dsh's uncommitted `PROBE_KEEP_DIR`/`PROBE_SKIP_AB` test-helper change
in its separate worktree is intentionally untouched and is not part of this
release. The original uncommitted handoff/README copies in this worktree are
preserved there before switching to the current main baseline.

The existing v1.7.5 tag, release assets, backups and rollback images remain
unchanged. v1.7.6 is subsequently delivered at 21:25:17 Asia/Shanghai with exact
CI 8/8, release 10/10, Docker 2/2, published-binary actual SQL recovery, OVH 6/6,
MAC-WAN fresh pre old/new and post new encrypted recovery and OC424 9/9,
public 23/23, 14 fresh reporters. See `DEPLOY-OC424.md` for immutable identities
and evidence; these separate delivery gates are not inferred from source tests.
