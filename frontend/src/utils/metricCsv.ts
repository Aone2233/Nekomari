/**
 * CSV export for the metric series a node chart displays.
 *
 * A wide table: the first column is the sample time, then one column per series
 * (a metric key plus its tags, so ping families or GPU devices stay separable).
 * Values stay raw numbers; the chart's aggregation is whatever the user picked,
 * and the file carries the data the chart itself drew from.
 */

import type { MetricSeries } from "@/types/metrics";
import { CSV_BOM, csvCell } from "@/utils/csv";

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

/**
 * metricSeriesToCsv renders the loaded series as a CSV document, BOM included.
 *
 * CRLF line endings because Excel on Windows is the reader this export is for.
 * Rows are merged by timestamp across series: a series with no sample at a given
 * time leaves that cell empty. The header is written even for an empty selection.
 */
export function metricSeriesToCsv(series: MetricSeries[]): string {
  const columns = series.map(metricCsvColumn);
  const rows = new Map<string, Record<string, string | number | null>>();

  series.forEach((item, columnIndex) => {
    const column = columns[columnIndex];
    for (const point of item.points ?? []) {
      const timestamp = new Date(point.time).toISOString();
      const row = rows.get(timestamp) ?? { time: timestamp };
      row[column] =
        typeof point.value === "number" && Number.isFinite(point.value)
          ? point.value
          : null;
      rows.set(timestamp, row);
    }
  });

  const ordered = Array.from(rows.values()).sort(
    (a, b) => String(a.time).localeCompare(String(b.time)),
  );
  const lines = [
    ["time", ...columns].join(","),
    ...ordered.map((row) =>
      [row.time, ...columns.map((column) => row[column] ?? null)]
        .map(csvCell)
        .join(","),
    ),
  ];
  return CSV_BOM + lines.join("\r\n");
}
