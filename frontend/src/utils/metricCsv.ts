/**
 * CSV export for the metric series a node chart displays.
 *
 * A wide table: the first column is the sample time, then one column per series
 * (a metric key plus its tags, so ping families or GPU devices stay separable).
 * Values are not display-formatted; they may already be aggregated by the
 * server. The file carries the data the chart itself drew from.
 */

import type { MetricSeries } from "@/types/metrics";
import { CSV_BOM, csvCell, csvSpreadsheetText } from "@/utils/csv";

/**
 * The column name for one series: `metric_key` when untagged, otherwise
 * `metric_key{tag=value,...}` with sorted tag keys. Machine-readable and stable,
 * unlike the localized labels the chart legend shows.
 */
export function metricCsvColumn(series: MetricSeries): string {
  const tags = series.tags;
  if (!tags || Object.keys(tags).length === 0) return series.metric_key;
  const suffix = Object.keys(tags)
    .sort()
    .map((key) => `${key}=${tags[key]}`)
    .join(",");
  return `${series.metric_key}{${suffix}}`;
}

/** Stable identity separate from the display header, including tag boundaries. */
function metricCsvSeriesKey(series: MetricSeries): string {
  const tags = series.tags ?? {};
  return JSON.stringify([
    series.metric_key,
    Object.keys(tags).sort().map((key) => [key, tags[key]]),
  ]);
}

/**
 * metricSeriesToCsv renders the loaded series as a CSV document, BOM included.
 *
 * CRLF line endings because Excel on Windows is the reader this export is for.
 * Rows are merged by timestamp across series: a series with no sample at a given
 * time leaves that cell empty. The header is written even for an empty selection.
 */
export function metricSeriesToCsv(series: MetricSeries[]): string {
  const identities = series.map(metricCsvSeriesKey);
  const seen = new Set<string>();
  for (const identity of identities) {
    if (seen.has(identity)) throw new Error("Duplicate metric series in export");
    seen.add(identity);
  }
  const baseColumns = series.map(metricCsvColumn);
  const headerCounts = new Map<string, number>();
  for (const column of baseColumns) {
    headerCounts.set(column, (headerCounts.get(column) ?? 0) + 1);
  }
  const used = new Set(["time"]);
  const columns = baseColumns.map((column, index) => {
    const base = headerCounts.get(column)! > 1 ? `${column} [${identities[index]}]` : column;
    let label = csvSpreadsheetText(base);
    let suffix = 1;
    while (used.has(label)) {
      label = csvSpreadsheetText(`${base} [${identities[index]} #${suffix++}]`);
    }
    used.add(label);
    return label;
  });
  const rows = new Map<string, Array<number | null>>();

  series.forEach((item, columnIndex) => {
    for (const point of item.points ?? []) {
      const timestamp = new Date(point.time).toISOString();
      const row = rows.get(timestamp) ?? Array<number | null>(series.length).fill(null);
      row[columnIndex] =
        typeof point.value === "number" && Number.isFinite(point.value)
          ? point.value
          : null;
      rows.set(timestamp, row);
    }
  });

  const ordered = Array.from(rows.entries()).sort(([a], [b]) => a.localeCompare(b));
  const lines = [
    ["time", ...columns].map(csvCell).join(","),
    ...ordered.map(([time, row]) =>
      [time, ...row]
        .map(csvCell)
        .join(","),
    ),
  ];
  return CSV_BOM + lines.join("\r\n");
}
