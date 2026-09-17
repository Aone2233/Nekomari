#!/usr/bin/env python3
"""Locate ping-record storage and measure its real depth and resolution.

The API returned ~58 samples for a 24h window on a 60s task, which is far fewer
than 1440. Before treating that as packet loss it has to be established whether
the panel is dropping samples, aggregating them, or simply not storing that much
history. The metric store rolls samples up into tiers, so the answer is probably
the third -- but it needs checking, because "the target is flaky" and "the chart
is coarse" look identical on a graph.

Run on OC424.
"""
import sqlite3

for path in ("/opt/nekomari/data/metrics.db", "/opt/nekomari/data/komari.db"):
    print(f"=== {path} ===")
    try:
        db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    except Exception as e:
        print(f"  cannot open: {e}")
        continue
    q = db.cursor()

    names = [r[0] for r in q.execute(
        "select name from sqlite_master where type='table' order by name")]
    print(f"  tables: {', '.join(names)}")

    for t in names:
        if any(k in t.lower() for k in ("ping", "record", "rollup", "series")):
            try:
                n = q.execute(f"select count(*) from {t}").fetchone()[0]
                print(f"    {t:<26} rows={n}")
            except Exception:
                pass

    # Ping results are likely stored as a metric series.
    if "metric_series" in names:
        print("  --- metric names containing ping ---")
        for name, cnt in q.execute(
                "select metric_name, count(*) from metric_series "
                "where metric_name like '%ping%' group by 1 order by 2 desc"):
            print(f"    {name:<34} series={cnt}")

        print("  --- task 3 series, sample counts per resolution ---")
        try:
            rows = q.execute("""
                select s.metric_name, s.entity_id, r.resolution_id,
                       count(*) as n, min(r.bucket_milli) as lo, max(r.bucket_milli) as hi
                from metric_series s join metric_rollups r on r.series_id = s.id
                where s.metric_name like '%ping%'
                group by 1,2,3 order by n desc limit 12
            """).fetchall()
            import datetime as dt
            for mn, ent, res, n, lo, hi in rows:
                f = lambda ms: dt.datetime.utcfromtimestamp(ms / 1000).strftime("%m-%d %H:%M")
                span = (hi - lo) / 3600000.0
                print(f"    {mn:<26} ent={ent[:8]} res={res} n={n:<6} "
                      f"{f(lo)} .. {f(hi)}  span={span:.1f}h")
        except Exception as e:
            print(f"    query failed: {e}")

    print("  --- resolutions defined ---")
    if "metric_resolutions" in names:
        for r in q.execute("select * from metric_resolutions"):
            print(f"    {r}")
    db.close()
    print()
