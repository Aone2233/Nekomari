import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

/**
 * The node chart's CSV export rules.
 *
 * The page needs a server and a browser, but the things that decide whether the
 * export is usable are pure: column naming (a tagged series must not collide
 * with its untagged twin), cell escaping, and row alignment across series with
 * different sample times. `csvCell` itself is pinned by log-export.test.mjs;
 * this module must share it, so the require shim loads the real csv.ts too.
 */

const srcRoot = fileURLToPath(new URL('../src/', import.meta.url));

const csvCode = ts.transpileModule(
  readFileSync(path.join(srcRoot, 'utils/csv.ts'), 'utf8'),
  {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  },
).outputText;
const csvModule = { exports: {} };
vm.runInNewContext(csvCode, { module: csvModule, exports: csvModule.exports }, { filename: 'csv.ts' });

const code = ts.transpileModule(
  readFileSync(path.join(srcRoot, 'utils/metricCsv.ts'), 'utf8'),
  {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  },
).outputText;
const module = { exports: {} };
vm.runInNewContext(
  code,
  {
    module,
    exports: module.exports,
    require: (id) => {
      if (id === '@/utils/csv') return csvModule.exports;
      throw new Error(`unexpected require in metricCsv.ts: ${id}`);
    },
  },
  { filename: 'metricCsv.ts' },
);
const { metricCsvColumn, metricSeriesToCsv } = module.exports;

const series = (metric_key, tags, points) => ({
  metric_key,
  entity_id: 'node-a',
  tags,
  count: points.length,
  points,
});

test('an untagged series keeps the bare metric key as its column', () => {
  assert.equal(metricCsvColumn(series('cpu', undefined, [])), 'cpu');
  assert.equal(metricCsvColumn(series('cpu', {}, [])), 'cpu');
});

test('a tagged series sorts its tags into the column name', () => {
  assert.equal(
    metricCsvColumn(series('ping.latency_ms', { task_id: '7', family: 'ipv6' }, [])),
    'ping.latency_ms{family=ipv6,task_id=7}',
  );
});

test('rows merge across series by timestamp and leave missing cells empty', () => {
  const csv = metricSeriesToCsv([
    series('cpu', undefined, [
      { time: '2026-11-04T03:00:00Z', value: 12.5 },
      { time: '2026-11-04T03:01:00Z', value: 13 },
    ]),
    series('ram', undefined, [
      { time: '2026-11-04T03:01:00Z', value: 4096 },
    ]),
  ]);
  const lines = csv.replace(/^\uFEFF/, '').split('\r\n');
  assert.deepEqual(lines[0].split(','), ['time', 'cpu', 'ram']);
  assert.deepEqual(lines[1].split(','), ['2026-11-04T03:00:00.000Z', '12.5', '']);
  assert.deepEqual(lines[2].split(','), ['2026-11-04T03:01:00.000Z', '13', '4096']);
});

test('null and non-finite values become empty cells, not strings', () => {
  const csv = metricSeriesToCsv([
    series('load', undefined, [{ time: '2026-11-04T03:00:00Z', value: null }]),
  ]);
  assert.ok(csv.includes('2026-11-04T03:00:00.000Z,'));
});

test('an empty selection still writes the time header', () => {
  const csv = metricSeriesToCsv([]);
  assert.equal(csv.replace(/^\uFEFF/, ''), 'time');
});

test('tagged and unusual headers are CSV-escaped', () => {
  const csv = metricSeriesToCsv([
    series('ping.latency_ms', { task_id: '7', family: 'ipv6' }, [
      { time: '2026-11-04T03:00:00Z', value: 12 },
    ]),
    series('metric,"quoted"', { label: 'first\nsecond' }, [
      { time: '2026-11-04T03:00:00Z', value: 13 },
    ]),
  ]);
  assert.ok(csv.startsWith('\uFEFFtime,"ping.latency_ms{family=ipv6,task_id=7}",'));
  assert.ok(csv.includes('"metric,""quoted""{label=first\nsecond}"'));
  assert.ok(csv.endsWith('2026-11-04T03:00:00.000Z,12,13'));
});

test('ambiguous display names do not merge distinct tag sets', () => {
  const csv = metricSeriesToCsv([
    series('x', { a: 'b,c=d' }, [{ time: '2026-11-04T03:00:00Z', value: 1 }]),
    series('x', { a: 'b', c: 'd' }, [{ time: '2026-11-04T03:00:00Z', value: 2 }]),
  ]);
  assert.ok(csv.endsWith('2026-11-04T03:00:00.000Z,1,2'));
  assert.ok(csv.includes('""b,c=d""'));
  assert.ok(csv.includes('""c"",""d""'));
});

test('duplicate series are rejected rather than silently overwritten', () => {
  assert.throws(() => metricSeriesToCsv([
    series('cpu', undefined, []), series('cpu', {}, []),
  ]), /Duplicate metric series/);
});

test('untrusted metric headers are not evaluated as spreadsheet formulas', () => {
  const csv = metricSeriesToCsv([series('=1+1', {}, [])]);
  assert.equal(csv, "\uFEFFtime,'=1+1");
  const control = metricSeriesToCsv([series('  \ttext', {}, [])]);
  assert.equal(control, "\uFEFFtime,'  \ttext");
});

test('spreadsheet-safe labels cannot collide with each other or the time column', () => {
  const csv = metricSeriesToCsv([
    series('=1', {}, [{ time: '2026-11-04T03:00:00Z', value: 1 }]),
    series("'=1", {}, [{ time: '2026-11-04T03:00:00Z', value: 2 }]),
    series('time', {}, [{ time: '2026-11-04T03:00:00Z', value: 3 }]),
  ]);
  assert.ok(csv.startsWith("\uFEFFtime,'=1,"));
  assert.ok(csv.includes('time ['));
  assert.ok(csv.endsWith('2026-11-04T03:00:00.000Z,1,2,3'));
});
