/**
 * Presentation helpers for the maintenance window page (roadmap H3).
 *
 * In their own file rather than beside the component, because a module exporting both a component
 * and plain functions breaks fast refresh — the same reason `status/slaFormat.ts` sits outside
 * `SlaReportTable.tsx`, and eslint enforces it.
 *
 * `toRFC3339` is exported because it is a rule, not a format: the server refuses a naive timestamp
 * deliberately (a fleet spans timezones, and a window that starts at the wrong hour suppresses the
 * wrong hour), so the browser has to attach its own offset rather than letting the server guess.
 */

/** remainingText renders a duration in seconds the way a person reads it. */
export const remainingText = (seconds: number): string => {
  if (!seconds || seconds <= 0) return "";
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = minutes / 60;
  if (hours < 48) return `${hours.toFixed(1)}h`;
  return `${(hours / 24).toFixed(1)}d`;
};

/** localInputValue renders an RFC3339 instant for a datetime-local input, in local time. */
export const localInputValue = (iso: string): string => {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}`;
};

/** toRFC3339 turns a datetime-local value into an RFC3339 instant with a timezone. */
export const toRFC3339 = (local: string): string => {
  if (!local) return "";
  const date = new Date(local);
  if (Number.isNaN(date.getTime())) return "";
  return date.toISOString();
};
