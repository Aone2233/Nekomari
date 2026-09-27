/**
 * Formatting for the traffic forecast page (roadmap H5).
 *
 * In its own file because a module exporting both a component and plain functions breaks fast refresh —
 * the same convention as `slaFormat.ts`, `maintenanceFormat.ts` and `configFormat.ts`.
 */

/**
 * formatBytes renders a byte count for a table cell.
 *
 * Binary units, matching the panel's own `humanBytes` and the agent's, so a number on this page and the
 * same number in a notification read alike. One decimal below a hundred so a value near a limit is not
 * rounded into looking safe.
 */
export const formatBytes = (bytes: number): string => {
  if (!bytes || bytes <= 0) return "0";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value >= 100 || unit === 0 ? 0 : 1)} ${units[unit]}`;
};