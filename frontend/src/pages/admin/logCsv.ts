/**
 * The audit log's query string and CSV export.
 *
 * In their own module rather than inline in the page: a module exporting both a component and
 * plain functions breaks fast refresh (the same convention as `status/slaFormat.ts`), and these
 * are rules — what a cell has to escape, what "all types" means on the wire — that `npm test` can
 * pin without a browser.
 */

/** One log row, reduced to the columns the CSV carries. */
export interface LogCsvRow {
  time: string;
  ip: string;
  uuid: string;
  msg_type: string;
  message: string;
}

/**
 * The message types the panel writes into `logs.msg_type`.
 *
 * Read off the `database/auditlog.Log` call sites (login/logout/terminal/visitor/info/warn/error).
 * Go stores them as free-form strings with no constant table, so this list is the frontend half of
 * that contract. It is deliberately exact: inventing "warning" beside the backend's "warn" would
 * offer a filter that can only ever return an empty table.
 */
export const LOG_MSG_TYPES = [
  "login",
  "logout",
  "terminal",
  "visitor",
  "info",
  "warn",
  "error",
] as const;

/** The "all types" option. It is never sent as `msg_type`: absent means unfiltered to the server. */
export const ALL_LOG_TYPES = "all";

/** Page size and row ceiling for one export, so a click cannot pull the whole table. */
export const LOG_EXPORT_PAGE_SIZE = 1000;
export const LOG_EXPORT_MAX_ROWS = 10_000;

/** UTF-8 BOM: without it Excel reads a UTF-8 CSV as the local code page and mangles non-ASCII. */
export const CSV_BOM = "\uFEFF";

export const CSV_HEADER = ["time", "ip", "uuid", "msg_type", "message"] as const;

/**
 * logsQuery builds the request for one page of logs.
 *
 * The table and the export share it, so a filtered export cannot disagree with the filtered table —
 * a disagreement there reads as "the export ignores the filter", which is the one bug the filter
 * exists to prevent.
 */
export function logsQuery(limit: number, page: number, msgType: string): string {
  const query = new URLSearchParams({ limit: String(limit), page: String(page) });
  if (msgType && msgType !== ALL_LOG_TYPES) query.set("msg_type", msgType);
  return `/api/admin/logs?${query.toString()}`;
}

/**
 * csvCell escapes one value for CSV.
 *
 * A value is quoted when it contains a comma, a quote or a line break — the three things that make
 * a CSV reader split one row into two — and quotes inside it are doubled, which is the only escape
 * CSV has. Values that need none stay bare so the file remains readable in a terminal.
 */
export function csvCell(value: unknown): string {
  const text = value === null || value === undefined ? "" : String(value);
  if (!/[",\r\n]/.test(text)) return text;
  return `"${text.replace(/"/g, '""')}"`;
}

/**
 * logsToCsv renders rows as a CSV document, BOM included.
 *
 * CRLF line endings because Excel on Windows is the reader this export is for, and the BOM is what
 * makes non-ASCII messages survive that reader. The header is written even for an empty selection:
 * a headerless file looks like a broken download rather than "nothing matched the filter".
 */
export function logsToCsv(rows: LogCsvRow[]): string {
  const lines = [
    CSV_HEADER.join(","),
    ...rows.map((row) =>
      [row.time, row.ip, row.uuid, row.msg_type, row.message].map(csvCell).join(","),
    ),
  ];
  return CSV_BOM + lines.join("\r\n");
}
