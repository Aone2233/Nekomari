export type MetricTags = Record<string, string>;

export type MetricTagged = {
  tags?: MetricTags;
};

export type MetricPoint = MetricTagged & {
  time: string;
  value: number | null;
  count?: number;
  labels?: Record<string, string>;
};

export type MetricSeries = MetricTagged & {
  metric_key: string;
  entity_id: string;
  type?: string;
  unit?: string;
  retention_days?: number;
  downsampled?: boolean;
  downsample_algorithm?: string;
  max_points?: number;
  interval_seconds?: number;
  count: number;
  points: MetricPoint[];
};

export type QueryMetricsResponse = {
  start: string;
  end: string;
  series: MetricSeries[];
  count: number;
};

export type PublicPingTask = {
  id: number;
  weight?: number;
  name: string;
  type?: string;
  interval?: number;
  clients?: string[];
  default_on?: boolean;
  /**
   * Nodes the scheduler will skip on this task because their address family cannot
   * reach the target — an IPv6-only node on an IPv4-literal target, say. Computed
   * server-side so the UI and the scheduler cannot disagree; the public nodes API
   * deliberately does not expose node addresses.
   */
  skipped_clients?: string[];
};

export type PingMetricStat = {
  entity_id: string;
  task_id: string;
  /**
   * The address family this statistic was actually measured over ("ipv4"/"ipv6").
   *
   * A hostname is resolved by each node independently, so a dual-stack target can
   * be measured over IPv4 by one node and IPv6 by another. Those are two different
   * paths with different latency and loss, and the backend now reports them as two
   * statistics instead of one mixed number. Empty when the agent did not report a
   * family, which is the pre-change behaviour.
   */
  family?: string;
  name?: string;
  type?: string;
  interval?: number;
  tags?: MetricTags;
  total: number;
  valid: number;
  loss: number;
  loss_approximate?: boolean;
  min?: number | null;
  max?: number | null;
  avg?: number | null;
  latest?: number | null;
  p50?: number | null;
  p99?: number | null;
  stddev?: number | null;
  p99_p50_ratio?: number;
};

export type PingMetricStatsResponse = {
  start: string;
  end: string;
  interval_seconds?: number;
  stats: PingMetricStat[];
  count: number;
};
