/**
 * Presentation helpers for the SLA report (roadmap H1).
 *
 * In their own file rather than beside the component: a module that exports both a
 * component and plain functions breaks fast refresh, which is the same reason
 * `nodeTable/useIsSnapshotBackend.ts` lives outside `NodeTable.tsx`.
 *
 * The first two are exported because they are rules, not formatting. Each encodes a
 * decision the server already made — `has_data` separates "100%" from "nothing was
 * measured", and coverage is reported beside availability rather than folded into it —
 * and a rule that can only be checked by reading a component is a rule that will be
 * undone by the next change to that component. The fixture asserts on them directly.
 */

import type { SlaAvailability } from "@/types/Sla";
import { SLA_WINDOWS, type SlaWindow } from "@/types/Sla";

export const DEFAULT_WINDOW: SlaWindow = "24h";

/** percent renders a 0..1 fraction as a percentage with one decimal. */
export const percent = (fraction: number): string => `${(fraction * 100).toFixed(1)}%`;

/** duration renders a Go-style nanosecond duration in a readable unit. */
export const duration = (nanoseconds: number): string => {
  if (!nanoseconds || nanoseconds <= 0) return "—";
  const minutes = nanoseconds / 60_000_000_000;
  if (minutes < 60) return `${Math.round(minutes)}m`;
  const hours = minutes / 60;
  if (hours < 48) return `${hours.toFixed(1)}h`;
  return `${(hours / 24).toFixed(1)}d`;
};

export const formatTime = (value: string): string => {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString();
};

/**
 * availabilityText returns what the availability cell should say.
 *
 * A figure of zero with no evidence behind it must never be rendered as "0.0%": that is
 * the shape a dead node takes, and the silent case is the one nobody notices.
 */
export const availabilityText = (
  availability: SlaAvailability | undefined,
  t: (key: string) => string,
): string => {
  if (!availability || !availability.has_data) return t("status.noData");
  return percent(availability.fraction);
};

/** coverageText says how much of the window the figure was computed from. */
export const coverageText = (
  observed: number,
  expected: number,
  coverage: number,
  t: (key: string) => string,
): string => {
  if (!observed) return t("status.noData");
  return t("status.coverageOf")
    .replace("{{observed}}", String(observed))
    .replace("{{expected}}", String(expected))
    .replace("{{percent}}", percent(coverage));
};

/**
 * windowFromLocation reads the window out of the URL, falling back to the default.
 *
 * In the URL so a report can be shared with its window rather than only its host —
 * the point of a status page is that it can be sent to someone.
 */
export const windowFromLocation = (search: string): SlaWindow => {
  const requested = new URLSearchParams(search).get("window");
  const match = SLA_WINDOWS.find((window) => window === requested);
  return match ?? DEFAULT_WINDOW;
};
