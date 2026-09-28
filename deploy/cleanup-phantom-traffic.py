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

A factor of ~10000 between the two populations is why the threshold is a *ratio against the series median*
rather than an absolute size: an absolute cap would either keep the phantoms on a busy node or delete real peaks
on a quiet one.

## Why the ratio and not "bigger than the previous"

Because a phantom is not defined by its neighbours — it is defined by being impossible for one interval on that
interface. The median is used rather than the mean precisely so that the phantoms cannot drag the reference up:
on the affected series the mean was dominated by the two bad points.

## What it does NOT remove

- `net.total.*` — a kernel counter, legitimately large and monotonic.
- Cheap-looking `traffic.*` values that were written under the *old* delta semantics. Those are wrong in a way
  this script cannot distinguish from real traffic (they are plausible-sized), so they are left. They also stop
  being produced once the panel is updated, and the panel's `traffic.*` becomes the cycle cumulative as reported
  by an updated agent — at which point the old points age out of the rollups on their own.

## Safety

- read-only unless `--apply`
- deletes by rowid, inside a transaction, and reports counts per series
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
# One interval is ~30 seconds; the largest honest reading observed was ~8 MB (a burst on a 350 Mbps line).
# A cycle cumulative is tens of GB. A factor of 1000 sits far above the first and far below the second, and the
# observed gap was ~10000, so the exact value is not delicate.
PHANTOM_RATIO = 1000.0

TRAFFIC_METRICS = ("traffic.up", "traffic.down")


def human(n: float) -> str:
    for unit, scale in (("GB", 1e9), ("MB", 1e6), ("KB", 1e3)):
        if abs(n) >= scale:
            return f"{n / scale:.3f} {unit}"
    return f"{n:.0f} B"


def scan(connection: sqlite3.Connection, ratio: float):
    """Yield (series_id, metric, resolution_id, rowid, bucket_milli, value) for each phantom."""
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

    connection = sqlite3.connect(f"file:{args.db}?mode={'rw' if args.apply else 'ro'}", uri=True)
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

        with connection:
            connection.executemany(
                "delete from metric_rollups where rowid=?", [(r[0],) for r in found]
            )
        print(f"\nremoved {len(found)} rows.")
        print("The panel reads these tables directly, so a restart is not required; long-lived in-process "
              "caches, if any, refresh on the next rollup.")
        return 0
    finally:
        connection.close()


if __name__ == "__main__":
    sys.exit(main())
