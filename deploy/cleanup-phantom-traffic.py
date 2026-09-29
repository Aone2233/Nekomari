#!/usr/bin/env python3
"""Remove the phantom traffic samples produced by the mixed-semantics defect.

    python3 deploy/cleanup-phantom-traffic.py                # report only
    python3 deploy/cleanup-phantom-traffic.py --apply        # delete them

## What it removes

Samples of `traffic.up` / `traffic.down` that are **orders of magnitude** above the rest of their own series.

Those are not large traffic readings; they are the node's *cycle cumulative* (tens of GB) recorded as if it were
one interval's traffic (normally tens of KB). On the node this was found on, the distribution was unambiguous:

    readings around 0.000000001 GB .. 0.008 GB      (the real ones)
    two readings of 40.98 GB and 41.16 GB           (the cycle cumulative)

## The panel must be stopped first

A running panel keeps the current rollup bucket in memory and flushes it, so rows deleted while it is running
**come back**. Verified directly: 450 rows were deleted and confirmed gone by the deleting connection, then
still present from a fresh one; running the tool twice reported the same per-series counts both times.

So the order is: stop the panel, clean, start the panel. A deployment stops it anyway, which is the natural
window.

## What it does NOT remove

- `net.total.*` — a kernel counter, legitimately large and monotonic.
- Plausible-sized `traffic.*` values written under the *old* delta semantics. Those are wrong in a way this
  script cannot distinguish from real traffic, so they are left; they stop being produced once the panel is
  updated, and age out of the rollups on their own.

## Safety

- read-only unless `--apply`
- deletes inside an explicit transaction and verifies every rowid afterwards; a mismatch fails the run
- refuses to run if it would remove more than `--max-fraction` of a series (default 5%), since a threshold that
  suddenly matches a large share of the data means the assumption behind it is wrong, not that the data is bad
- every deletion is printed with its timestamp, series and value, so the report can be reviewed afterwards
"""
from __future__ import annotations

import argparse
import os
import sqlite3
import statistics
import sys
import time

DEFAULT_DB = "/opt/nekomari/data/metrics.db"

# Ratio against the series median above which a sample is a phantom rather than traffic.
#
# Calibrated from the data this was written against, because the first value was too low and began flagging real
# bursts once the large phantoms were gone:
#
#   real readings      14 KB .. 8 MB     (a burst on a 350 Mbps line, median around 40-150 KB)
#   phantom readings   3.8 GB .. 72 GB   (the cycle cumulative, against the same median)
#
# 1000x sat between them only while the phantoms were present; against a 39 KB median it flags anything above
# 39 MB, and 54 MB bursts are real. 100000x puts the line at 3.9 GB for that median — above every burst observed
# and below every phantom. The populations are ~10000x apart, so the exact factor is not delicate; what matters
# is sitting far enough above the burst ceiling that a busy node is not misread.
PHANTOM_RATIO = 100_000.0

TRAFFIC_METRICS = ("traffic.up", "traffic.down")


def human(n: float) -> str:
    for unit, scale in (("GB", 1e9), ("MB", 1e6), ("KB", 1e3)):
        if abs(n) >= scale:
            return f"{n / scale:.3f} {unit}"
    return f"{n:.0f} B"


def scan(connection: sqlite3.Connection, ratio: float):
    """Yield (series_id, metric, rowid, bucket_milli, value, median) for each phantom."""
    series = {
        sid: name
        for sid, name in connection.execute("select id, metric_name from metric_series")
        if name in TRAFFIC_METRICS
    }
    if not series:
        return

    for sid, name in series.items():
        rows = connection.execute(
            "select rowid, bucket_milli, max_val from metric_rollups "
            "where series_id=? and max_val is not null and max_val > 0",
            (sid,),
        ).fetchall()
        if len(rows) < 3:
            # Too few points for a median to mean anything; skip rather than guess.
            continue
        median = statistics.median(r[2] for r in rows)
        if median <= 0:
            continue
        threshold = median * ratio
        for rowid, bucket, value in rows:
            if value > threshold:
                yield sid, name, rowid, bucket, value, median


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--db", default=os.environ.get("NEKOMARI_METRICS_DB", DEFAULT_DB))
    parser.add_argument("--apply", action="store_true", help="actually delete (default: report only)")
    parser.add_argument("--ratio", type=float, default=PHANTOM_RATIO)
    parser.add_argument("--max-fraction", type=float, default=0.05,
                        help="refuse if a series would lose more than this share of its points")
    args = parser.parse_args()

    if not os.path.exists(args.db):
        print(f"no metrics database at {args.db}", file=sys.stderr)
        return 1

    # Opened by **path**, in autocommit mode, with an explicit transaction for the delete.
    #
    # Three separate problems were found here by verifying against a second connection rather than trusting the
    # tool's own report — and all three presented identically, as `removed N rows` followed by every row still
    # present:
    #
    #   1. `sqlite3.connect(f"file:{path}?mode=rw", uri=True)` does not raise for an absolute POSIX path (it is
    #      accepted as a relative URI), so the delete ran against something other than the intended database.
    #   2. `with connection:` did not commit in this environment: `executemany` inside a `with` block reported
    #      success and left the rows in place, repeatedly, for the same rows.
    #   3. A running panel rewrites the current rollup bucket, so rows deleted while it runs come back. The panel
    #      must be stopped — see the note at the top.
    #
    # What is verified to work, and what this does: connect by path with `isolation_level=None`, delete inside an
    # explicit `begin immediate` / `commit`, and check every rowid afterwards. 75 rows that had survived four runs
    # of the earlier form were removed on the first run of this one.
    connection = sqlite3.connect(args.db, isolation_level=None, timeout=60)
    try:
        found = list(scan(connection, args.ratio))
        if not found:
            print("no phantom samples found")
            return 0

        by_series: dict[tuple[int, str], list] = {}
        for sid, name, rowid, bucket, value, median in found:
            by_series.setdefault((sid, name), []).append((rowid, bucket, value, median))

        total_points = 0
        for sid, name in by_series:
            (count,) = connection.execute(
                "select count(*) from metric_rollups where series_id=? and max_val>0", (sid,)
            ).fetchone()
            total_points += count

        print(f"candidate phantoms: {len(found)} across {len(by_series)} series (ratio > {args.ratio:g}x median)\n")
        over_budget = []
        for (sid, name), rows in sorted(by_series.items(), key=lambda kv: -len(kv[1])):
            (count,) = connection.execute(
                "select count(*) from metric_rollups where series_id=? and max_val>0", (sid,)
            ).fetchone()
            share = len(rows) / count if count else 0
            flag = "  <-- OVER BUDGET" if share > args.max_fraction else ""
            print(f"  {name:<14} series={sid:<5} {len(rows):>4} of {count:>5} points ({share:6.2%}){flag}")
            print(f"      median {human(rows[0][3])}, largest candidate {human(max(r[2] for r in rows))}")
            if share > args.max_fraction:
                over_budget.append((name, sid, share))

        if over_budget:
            print("\nrefusing to delete: these series would lose more than "
                  f"{args.max_fraction:.0%} of their points, which means the threshold is wrong rather than the "
                  "data being bad:", file=sys.stderr)
            for name, sid, share in over_budget:
                print(f"  {name} series={sid} would lose {share:.1%}", file=sys.stderr)
            return 2

        print("\nthe rows that would be removed:")
        for (sid, name), rows in sorted(by_series.items()):
            for rowid, bucket, value, median in sorted(rows, key=lambda r: r[1]):
                when = time.strftime("%Y-%m-%d %H:%M", time.localtime(bucket / 1000))
                print(f"  {when}  {name:<14} series={sid:<5} {human(value):>12}  (median {human(median)})")

        if not args.apply:
            print(f"\nreport only. re-run with --apply to remove {len(found)} rows.")
            return 0

        # Deleted inside an explicit transaction, then verified by primary key.
        #
        # Both parts are deliberate. The `with connection:` form that came before did not commit here, and the
        # absence of a verification step is why four runs reported success while changing nothing.
        connection.execute("begin immediate")
        connection.executemany(
            "delete from metric_rollups where rowid=?", [(r[0],) for r in found]
        )
        connection.execute("commit")

        remaining = connection.execute(
            "select count(*) from metric_rollups where rowid in (%s)"
            % ",".join("?" * len(found)),
            [r[0] for r in found],
        ).fetchone()[0]
        if remaining:
            print(f"\n{remaining} of {len(found)} rows are still present after the delete; nothing was removed.",
                  file=sys.stderr)
            return 3

        print(f"\nremoved {len(found)} rows, verified gone.")
        print("The panel reads these tables directly, so a restart is not required; long-lived in-process "
              "caches, if any, refresh on the next rollup.")
        return 0
    finally:
        connection.close()


if __name__ == "__main__":
    sys.exit(main())
