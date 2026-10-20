import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

/**
 * The audit log's export and filter rules.
 *
 * The page itself needs a server and a browser, but the two things that decide whether an export is
 * usable are pure: **what a CSV cell has to escape** (a comma, a quote or a line break in a log
 * message is exactly what makes a spreadsheet show one row as two, or two as a mangled one), and
 * **what the filter sends** (the table and the export share `logsQuery`, so a filtered export that
 * disagrees with the filtered table would read as "the export ignores the filter").
 *
 * The module is evaluated in a vm context like the other TypeScript helpers in this suite; the
 * arrays it returns are spread into this realm before `deepEqual`, which compares prototypes too.
 */

const srcRoot = fileURLToPath(new URL('../src/', import.meta.url));
const code = ts.transpileModule(
  readFileSync(path.join(srcRoot, 'pages/admin/logCsv.ts'), 'utf8'),
  {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
      esModuleInterop: true,
    },
  },
).outputText;

const module = { exports: {} };
vm.runInNewContext(
  code,
  { module, exports: module.exports, URLSearchParams },
  { filename: 'logCsv.ts' },
);
const {
  ALL_LOG_TYPES,
  CSV_BOM,
  LOG_EXPORT_MAX_ROWS,
  LOG_EXPORT_PAGE_SIZE,
  LOG_MSG_TYPES,
  csvCell,
  logsQuery,
  logsToCsv,
} = module.exports;

const row = (overrides = {}) => ({
  time: '2026-10-20T13:39:22Z',
  ip: '10.0.0.1',
  uuid: 'uuid-a',
  msg_type: 'warn',
  message: 'clear all records',
  ...overrides,
});

test('csvCell quotes only what would break the row, and doubles the quotes it keeps', () => {
  assert.equal(csvCell('plain'), 'plain');
  assert.equal(csvCell('with,comma'), '"with,comma"');
  assert.equal(csvCell('with "quotes"'), '"with ""quotes"""');
  assert.equal(csvCell('two\nlines'), '"two\nlines"');
  assert.equal(csvCell('crlf\r\nline'), '"crlf\r\nline"');
  assert.equal(csvCell(''), '');
  assert.equal(csvCell(null), '');
  assert.equal(csvCell(undefined), '');
  assert.equal(csvCell(12), '12');
  // A value that needs both: quoted once, doubles inside.
  assert.equal(csvCell('a,"b"'), '"a,""b"""');
});

test('logsToCsv writes the BOM, the header and CRLF rows', () => {
  const csv = logsToCsv([row(), row({ message: 'hello, "world"' })]);
  assert.ok(csv.startsWith(CSV_BOM), 'Excel needs the BOM to read UTF-8 as UTF-8');
  assert.equal(CSV_BOM, '\uFEFF');
  const [header, first] = csv.slice(CSV_BOM.length).split('\r\n');
  assert.equal(header, 'time,ip,uuid,msg_type,message');
  assert.equal(first, '2026-10-20T13:39:22Z,10.0.0.1,uuid-a,warn,clear all records');
  assert.ok(csv.includes('"hello, ""world"""'), 'commas and quotes stay inside one cell');
  assert.ok(!csv.includes('\n\n'), 'no blank rows between records');
});

test('logsToCsv writes the header even when nothing matched', () => {
  const csv = logsToCsv([]);
  assert.equal(csv, CSV_BOM + 'time,ip,uuid,msg_type,message');
});

test('a message with a line break stays one CSV record', () => {
  const csv = logsToCsv([row({ message: 'line one\nline two' })]);
  const body = csv.slice(CSV_BOM.length);
  // The quoted newline is inside the cell, so the document still has one record line plus header.
  assert.equal(body.split('\r\n').length, 2);
  // Count the bare newlines only: the two record breaks are "\r\n", so a plain /\n/ count would
  // include them and hide whether the embedded one is there at all.
  assert.equal((body.match(/(?<!\r)\n/g) ?? []).length, 1);
});

test('the table query and the export query are built the same way', () => {
  assert.equal(logsQuery(10, 1, ALL_LOG_TYPES), '/api/admin/logs?limit=10&page=1');
  assert.equal(logsQuery(10, 1, ''), '/api/admin/logs?limit=10&page=1');

  const filtered = new URL(logsQuery(1000, 2, 'warn'), 'http://panel');
  assert.equal(filtered.pathname, '/api/admin/logs');
  assert.equal(filtered.searchParams.get('limit'), '1000');
  assert.equal(filtered.searchParams.get('page'), '2');
  assert.equal(filtered.searchParams.get('msg_type'), 'warn');
  // The page size used by the table's own fetch must survive the shared builder.
  assert.equal(new URL(logsQuery(10, 3, 'login'), 'http://panel').searchParams.get('page'), '3');
});

test('the filter list is the set of types the backend writes', () => {
  // A comment in logCsv.ts names the call sites; this pins the eight values so an accidental edit
  // (or an invented "warning" beside the backend's "warn") fails here rather than in the panel as
  // a filter that can only return an empty table.
  assert.deepEqual([...LOG_MSG_TYPES], [
    'login',
    'logout',
    'terminal',
    'visitor',
    'info',
    'warn',
    'error',
  ]);
  assert.equal(new Set(LOG_MSG_TYPES).size, LOG_MSG_TYPES.length, 'no duplicates');
  assert.ok(!(LOG_MSG_TYPES.includes('warning')), 'the backend writes warn, not warning');
});

test('the export ceiling and page size are the ones the page advertises', () => {
  assert.equal(LOG_EXPORT_PAGE_SIZE, 1000);
  assert.equal(LOG_EXPORT_MAX_ROWS, 10_000);
  // Ten pages of a thousand rows: the loop in log.tsx cannot outrun the ceiling.
  assert.equal(Math.ceil(LOG_EXPORT_MAX_ROWS / LOG_EXPORT_PAGE_SIZE), 10);
});
