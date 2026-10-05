import type { QueryMetricsResponse } from "@/types/metrics";

/**
 * The home page's traffic card derives "how much moved in the window" from one
 * `public:queryMetrics` response. Two things have to agree for that to be true:
 * what is asked for here, and how the response is added up below.
 *
 * `traffic.up` / `traffic.down` are the agent's **billing-cycle cumulative** —
 * bytes since the cycle's reset day. Their values are not window traffic, so the
 * aggregation requested is `sum`, which the panel remaps to the
 * `traffic.interval.*` series it derives on ingest: continuity-validated,
 * additive per-interval amounts, where an unmeasurable interval contributes
 * nothing instead of an invented number (`internal/metricstore/interval_traffic.go`,
 * and `publicMetricStorageKey` in `web/rpc/jsonrpc/public.metric.go`).
 *
 * Asking for `last` returns the running total itself, and adding one of those per
 * chart bucket multiplies the cycle total by the bucket count: the 24 h card read
 * a ~25 GB cycle total across 288 buckets as ~7 TB. The backend rejects `rate`
 * and `delta` on these keys for exactly this reason, and its error text names the
 * one to use instead — "use sum for validated interval amounts".
 *
 * `sum` alone is not enough, because the panel answers an entity it has no
 * interval data for with the cycle counter read with `last`, and says so with
 * `semantics: "billing_cycle_cumulative"`. Those points are the same running
 * total in a different field, so they are reported as *unknown* here rather than
 * added up: the series marker decides, not the shape of the numbers.
 */
export const TRAFFIC_TOTAL_METRIC_KEYS: string[] = ["traffic.up", "traffic.down"];

/** The only aggregation whose `traffic.*` points are additive. */
export const TRAFFIC_AGGREGATION_BY_METRIC: Record<string, string> = {
  "traffic.up": "sum",
  "traffic.down": "sum",
};

/** What the panel labels a series built from validated interval amounts. */
export const TRAFFIC_INTERVAL_SEMANTICS = "interval_delta_v2";

/** Instantaneous bytes per second, used for the chart's rate lines. */
export const NET_METRIC_KEYS: string[] = ["net.in.rate", "net.out.rate"];

const TRAFFIC_UP_KEY = "traffic.up";

export type TrafficSummaryPoint = {
  time: number;
  upRate: number;
  downRate: number;
  upCum: number;
  downCum: number;
};

export type TrafficNodeTotals = {
  uuid: string;
  up: number;
  down: number;
  total: number;
  peakRate: number;
  peakTime: number;
  /**
   * True when the window's traffic for this node is not measurable — it has no
   * validated interval series, so any number shown would be a cycle cumulative
   * or a zero dressed up as an answer.
   */
  unknown: boolean;
};

export type TrafficSummary = {
  points: TrafficSummaryPoint[];
  nodeTotals: TrafficNodeTotals[];
  totalUp: number;
  totalDown: number;
  /** Nodes whose window traffic is unknown, and therefore excluded from the totals. */
  unknownTrafficNodes: string[];
};

// 首页所有指标卡共用一个 24h 查询（流量/CPU/内存/延迟），
// 这里从响应中分别派生流量汇总与 TOP p95 排行。
export const computeTrafficSummary = (
  res: QueryMetricsResponse | null,
): TrafficSummary | null => {
  if (!res) return null;

  const byTime = new Map<
    number,
    { upRate: number; downRate: number; upDelta: number; downDelta: number }
  >();
  const byEntity = new Map<string, { up: number; down: number }>();
  const byEntityRate = new Map<
    string,
    Map<number, { up: number; down: number }>
  >();
  const unknownTraffic = new Set<string>();
  for (const series of res.series ?? []) {
    const isRate = NET_METRIC_KEYS.includes(series.metric_key);
    const isTraffic = TRAFFIC_TOTAL_METRIC_KEYS.includes(series.metric_key);
    if (!isRate && !isTraffic) continue;
    const entity = series.entity_id;
    if (isTraffic && series.semantics !== TRAFFIC_INTERVAL_SEMANTICS) {
      // A cycle cumulative, not a window amount. Summing it is the bug this module
      // exists to avoid; calling it zero would be the other half of that bug.
      unknownTraffic.add(entity);
      continue;
    }
    const isUp =
      series.metric_key === "net.out.rate" ||
      series.metric_key === TRAFFIC_UP_KEY;
    for (const point of series.points ?? []) {
      if (point.value == null) continue;
      const ts = new Date(point.time).getTime();
      const entry =
        byTime.get(ts) ?? { upRate: 0, downRate: 0, upDelta: 0, downDelta: 0 };
      if (isRate) {
        // Rates are instantaneous, so a bucket's value is not added to the totals.
        if (isUp) entry.upRate += point.value;
        else entry.downRate += point.value;
        const rateMap = byEntityRate.get(entity) ?? new Map();
        const rateEntry = rateMap.get(ts) ?? { up: 0, down: 0 };
        if (isUp) rateEntry.up += point.value;
        else rateEntry.down += point.value;
        rateMap.set(ts, rateEntry);
        byEntityRate.set(entity, rateMap);
      } else if (isUp) {
        // Already a measured interval amount; summing buckets is the total.
        entry.upDelta += point.value;
      } else {
        entry.downDelta += point.value;
      }
      byTime.set(ts, entry);
      if (!isRate) {
        const entityEntry = byEntity.get(entity) ?? { up: 0, down: 0 };
        if (isUp) entityEntry.up += point.value;
        else entityEntry.down += point.value;
        byEntity.set(entity, entityEntry);
      }
    }
  }
  const rate = Array.from(byTime.entries())
    .map(([time, value]) => ({ time, ...value }))
    .sort((a, b) => a.time - b.time);
  const points: TrafficSummaryPoint[] = [];
  let totalUp = 0;
  let totalDown = 0;
  for (const point of rate) {
    totalUp += point.upDelta;
    totalDown += point.downDelta;
    points.push({
      time: point.time,
      upRate: point.upRate,
      downRate: point.downRate,
      upCum: totalUp,
      downCum: totalDown,
    });
  }
  const unknownTrafficNodes = Array.from(unknownTraffic).filter(
    (uuid) => !byEntity.has(uuid),
  );
  const uuids = new Set<string>(Array.from(byEntity.keys()));
  for (const uuid of unknownTrafficNodes) uuids.add(uuid);
  const nodeTotals: TrafficNodeTotals[] = Array.from(uuids)
    .map((uuid) => {
      const value = byEntity.get(uuid) ?? { up: 0, down: 0 };
      let peakRate = 0;
      let peakTime = 0;
      for (const [ts, rateEntry] of byEntityRate.get(uuid) ?? []) {
        const combined = rateEntry.up + rateEntry.down;
        if (combined > peakRate) {
          peakRate = combined;
          peakTime = ts;
        }
      }
      return {
        uuid,
        up: value.up,
        down: value.down,
        total: value.up + value.down,
        peakRate,
        peakTime,
        unknown: !byEntity.has(uuid),
      };
    })
    .sort((a, b) => b.total - a.total);
  return { points, nodeTotals, totalUp, totalDown, unknownTrafficNodes };
};
