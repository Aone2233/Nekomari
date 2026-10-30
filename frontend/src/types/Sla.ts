/**
 * Types for the SLA report (roadmap H1).
 *
 * They mirror `internal/sla`'s report shape. The server defines the meaning of every
 * field — availability over measured buckets, coverage beside it, incidents as
 * contiguous runs — and this file only describes the wire format, deliberately without
 * re-deriving any of it. A figure the panel recomputed would be a second definition
 * that could disagree with the first.
 */

/** Coverage within the first-to-last observed bucket span, not the full request window. */
export interface SlaPresence {
  expected_buckets: number;
  observed_buckets: number;
  /** Observed/expected within the span, 0..1. Zero when no data was reported. */
  coverage: number;
  first_data: string;
  last_data: string;
}

/**
 * An availability figure with the evidence behind it.
 *
 * `has_data` is what separates "100% available" from "nothing was measured": the
 * fraction is 0 in both cases, and rendering it without checking has_data is how a
 * silent node would read as a dead one.
 */
export interface SlaAvailability {
  fraction: number;
  has_data: boolean;
  buckets: number;
  lost: number;
  presence: SlaPresence;
  window: number;
}

export interface SlaLatency {
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  buckets: number;
  has_data: boolean;
}

/** A contiguous run of failing buckets. */
export interface SlaIncident {
  Start: string;
  End: string;
  buckets: number;
  duration: number;
  peak_loss: number;
}

export interface SlaTaskReport {
  task_id: string;
  tags?: Record<string, string>;
  loss: SlaAvailability;
  latency: SlaLatency;
  outages: SlaIncident[] | null;
}

export interface SlaNodeReport {
  entity_id: string;
  presence: SlaPresence;
  tasks: SlaTaskReport[] | null;
  /** Gaps in the union of ping-task results, not proof of node downtime. */
  reporting_gaps: SlaIncident[] | null;
  has_report: boolean;
  /**
   * How many seconds of the requested window fell inside maintenance windows for this node.
   *
   * Omitted by the server when nothing was excluded. Excluded buckets are removed from
   * the span calculation; this duration is not a denominator for full-window coverage.
   */
  maintenance_excluded_seconds?: number;
}

export interface SlaReport {
  start: string;
  end: string;
  window: string;
  interval_seconds: number;
  /** Set when the requested window was longer than the store retains. */
  clamped?: string;
  nodes: SlaNodeReport[];
  count: number;
  window_presets: Record<string, number>;
}

/** The windows the page offers, in the order it offers them. */
export const SLA_WINDOWS = ["24h", "7d", "30d", "90d"] as const;
export type SlaWindow = (typeof SLA_WINDOWS)[number];
