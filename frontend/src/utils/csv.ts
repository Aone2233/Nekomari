/**
 * CSV primitives shared by the log export and the metric export.
 *
 * Kept in one module so "what a cell has to escape" is defined exactly once —
 * the log and metric exports must not drift apart on quoting rules.
 */

/** UTF-8 BOM: without it Excel reads a UTF-8 CSV as the local code page and mangles non-ASCII. */
export const CSV_BOM = "\uFEFF";

/**
 * csvCell escapes one value for CSV.
 *
 * A value is quoted when it contains a comma, a quote or a line break — the three
 * things that make a CSV reader split one row into two — and quotes inside it are
 * doubled, which is the only escape CSV has. Values that need none stay bare so
 * the file remains readable in a terminal.
 */
export function csvCell(value: unknown): string {
  const text = value === null || value === undefined ? "" : String(value);
  if (!/[",\r\n]/.test(text)) return text;
  return `"${text.replace(/"/g, '""')}"`;
}
