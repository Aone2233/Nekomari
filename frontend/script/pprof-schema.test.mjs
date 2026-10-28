import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

/**
 * The pprof summary schema is a runtime gate, and nothing else checks it.
 *
 * The page does not trust the shape of `/api/admin/pprof/summary`: it runs the payload through
 * `pprofSummaryResponseSchema.safeParse` and shows "invalid summary" when that fails. So a schema
 * regression — a newly added field marked required, or an old panel talking to an older server —
 * turns the whole diagnostics page into an error, and `tsc` cannot see it because the schema is a
 * value, not a type.
 *
 * What is asserted is the compatibility the field addition depends on: **a response without
 * `rss_bytes`, `uptime_seconds`, `db_pool` or `metrics_db` still parses** (that is what an older
 * server sends), the same response with them parses too, a metric store with a dedicated read pool
 * parses both with and without it (absent means reads share the write pool, which is a different
 * fact from a read pool with nothing open), and a malformed pool is rejected rather than rendered
 * as undefined numbers.
 *
 * The declarations are read out of `pprof.tsx` and executed rather than exported from it: the page
 * is a component module, and exporting plain values from it would break fast refresh (the
 * `react-refresh/only-export-components` rule this repository enforces). Extracting the top-level
 * `*Schema` constants keeps this test on the real definitions instead of a copy that could drift.
 */

const source = readFileSync(
  new URL('../src/pages/admin/pprof.tsx', import.meta.url),
  'utf8',
);
const requireHere = createRequire(import.meta.url);

const file = ts.createSourceFile(
  'pprof.tsx',
  source,
  ts.ScriptTarget.ES2022,
  true,
  ts.ScriptKind.TSX,
);
const declarations = [];
for (const statement of file.statements) {
  if (!ts.isVariableStatement(statement)) continue;
  const names = statement.declarationList.declarations.map((declaration) =>
    declaration.name.getText(file),
  );
  if (!names.some((name) => name.endsWith('Schema'))) continue;
  declarations.push(statement.getText(file));
}
assert.ok(
  declarations.length >= 3,
  'the pprof schemas must stay top-level const declarations for this contract to be checked',
);

const zod = requireHere('zod');
const z = zod.z ?? zod.default?.z;
assert.ok(z, 'zod must expose the z namespace');

const module = { exports: {} };
vm.runInNewContext(
  [...declarations, 'module.exports = { pprofSummaryResponseSchema };'].join('\n'),
  { module, exports: module.exports, z },
  { filename: 'pprof.schema.js' },
);

const { pprofSummaryResponseSchema } = module.exports;

/** The summary as an older server sends it: no process, pool or uptime fields. */
const legacySummary = {
  status: 'success',
  data: {
    profiles: [],
    runtime: {
      goroutines: 12,
      memory: { heap_alloc: 1024, heap_inuse: 2048, heap_objects: 3, sys: 4096 },
    },
    duration: { default_seconds: 10, min_seconds: 5, max_seconds: 30 },
  },
};

const withMetrics = () => {
  const summary = structuredClone(legacySummary);
  Object.assign(summary.data.runtime, {
    rss_bytes: 134_217_728,
    uptime_seconds: 3_725,
    db_pool: { open: 5, in_use: 1, idle: 4, wait_count: 3, wait_duration_ms: 42 },
    metrics_db: { open: 2, in_use: 0, idle: 2, wait_count: 0, wait_duration_ms: 0 },
  });
  return summary;
};

/** The same response, plus the metric store's dedicated read pool nested under metrics_db. */
const withReadPool = () => {
  const summary = withMetrics();
  summary.data.runtime.metrics_db.read = {
    open: 4,
    in_use: 2,
    idle: 2,
    wait_count: 1,
    wait_duration_ms: 7,
  };
  return summary;
};

test('a response from a server without the new runtime fields still parses', () => {
  const parsed = pprofSummaryResponseSchema.safeParse(legacySummary);
  assert.ok(parsed.success, 'an older server must not turn the page into "invalid summary"');
  assert.equal(parsed.data.data.runtime.rss_bytes, undefined);
  assert.equal(parsed.data.data.runtime.db_pool, undefined);
});

test('the new process and pool fields parse and keep their values', () => {
  const parsed = pprofSummaryResponseSchema.safeParse(withMetrics());
  assert.ok(parsed.success);
  assert.equal(parsed.data.data.runtime.rss_bytes, 134_217_728);
  assert.equal(parsed.data.data.runtime.uptime_seconds, 3_725);
  assert.equal(parsed.data.data.runtime.db_pool.wait_count, 3);
  assert.equal(parsed.data.data.runtime.metrics_db.idle, 2);
});

test('a metric store without a read pool parses and reports no read pool', () => {
  const parsed = pprofSummaryResponseSchema.safeParse(withMetrics());
  assert.ok(parsed.success);
  // Absent, not an object of zeroes: the page must be able to tell "reads share the write pool"
  // from "a read pool that happens to be idle".
  assert.equal(parsed.data.data.runtime.metrics_db.read, undefined);
});

test('a dedicated read pool parses beside the flat write pool', () => {
  const parsed = pprofSummaryResponseSchema.safeParse(withReadPool());
  assert.ok(parsed.success);
  assert.equal(
    parsed.data.data.runtime.metrics_db.wait_count,
    0,
    'the write pool keeps the five flat fields it always had',
  );
  assert.equal(parsed.data.data.runtime.metrics_db.read.open, 4);
  assert.equal(parsed.data.data.runtime.metrics_db.read.wait_count, 1);
  assert.equal(parsed.data.data.runtime.metrics_db.read.wait_duration_ms, 7);
});

test('a malformed pool is rejected rather than rendered with undefined numbers', () => {
  const broken = withMetrics();
  broken.data.runtime.db_pool = { open: 'many' };
  assert.equal(pprofSummaryResponseSchema.safeParse(broken).success, false);
});

test('a malformed read pool is rejected too', () => {
  const broken = withReadPool();
  broken.data.runtime.metrics_db.read = { open: 'many' };
  assert.equal(pprofSummaryResponseSchema.safeParse(broken).success, false);
});

test('theme-cache diagnostics are optional and validate their payload', () => {
  const summary = withMetrics();
  summary.data.runtime.theme_cache = { disk: 'ready', memory_loaded: false };
  const parsed = pprofSummaryResponseSchema.safeParse(summary);
  assert.ok(parsed.success);
  assert.equal(parsed.data.data.runtime.theme_cache.disk, 'ready');
  summary.data.runtime.theme_cache.disk = 'future-state';
  assert.ok(pprofSummaryResponseSchema.safeParse(summary).success, 'future states must not break the entire diagnostics page');
  summary.data.runtime.theme_cache.memory_loaded = 'yes';
  assert.equal(pprofSummaryResponseSchema.safeParse(summary).success, false);
});
