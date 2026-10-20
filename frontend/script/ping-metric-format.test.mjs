import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import vm from 'node:vm';
import test from 'node:test';
import React from 'react';
import { renderToString } from 'react-dom/server';
import ts from 'typescript';

/**
 * How a latency figure is written on the ping summary.
 *
 * The backend now reports fractional milliseconds, and the presentation has to keep two facts
 * apart that used to collapse into the same number: **"below one millisecond"** and **"zero"**.
 * Rounding both to "0 ms" says a probe had no latency at all, and it also erases the difference
 * between 10.4 and 10.9 ms that a reader is looking at the chart to judge.
 *
 * Rendered through the real component with `renderToString` rather than exporting the formatter:
 * the page file is a component module, and a non-component export would trip the
 * `react-refresh/only-export-components` rule this repository enforces. Rendering also pins the
 * other half of the requirement — a missing measurement leaves no row at all.
 */

const requireHere = createRequire(import.meta.url);
const srcRoot = fileURLToPath(new URL('../src/', import.meta.url));
const modules = new Map();

function load(file) {
  file = path.resolve(file);
  if (modules.has(file)) return modules.get(file).exports;
  const module = { exports: {} };
  modules.set(file, module);
  const code = ts.transpileModule(readFileSync(file, 'utf8'), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      jsx: ts.JsxEmit.ReactJSX,
      esModuleInterop: true,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  vm.runInNewContext(
    code,
    {
      module,
      exports: module.exports,
      console,
      require(spec) {
        if (!spec.startsWith('.') && !spec.startsWith('@/')) return requireHere(spec);
        const target = spec.startsWith('@/')
          ? path.join(srcRoot, spec.slice(2))
          : path.resolve(path.dirname(file), spec);
        const resolved = ['.ts', '.tsx'].map((ext) => target + ext).find(existsSync);
        assert.ok(resolved, `cannot resolve ${spec} from ${file}`);
        return load(resolved);
      },
    },
    { filename: file },
  );
  return module.exports;
}

/** A stat with every latency field absent; each test overrides just the figures it asserts. */
const stat = (overrides = {}) => ({
  entity_id: 'node-a',
  task_id: 'task-1',
  total: 60,
  valid: 60,
  loss: 0,
  ...overrides,
});

const render = (overrides) => {
  const { default: PingMetricStatContent } = load(
    path.join(srcRoot, 'components/PingMetricStatContent.tsx'),
  );
  return renderToString(
    React.createElement(PingMetricStatContent, {
      stat: stat(overrides),
      // Only labels are translated in this component; the assertions read the values.
      t: (key) => key,
    }),
  );
};

test('a sub-millisecond figure reads "<1 ms", not "0 ms"', () => {
  const html = render({ min: 0, max: 0.4 });
  // `<` is escaped in server-rendered HTML; the browser shows it as "<1 ms".
  assert.equal(html.match(/&lt;1 ms/g)?.length, 2, 'both 0 and 0.4 are below the 1 ms step');
  assert.doesNotMatch(html, /\b0 ms\b/, 'zero latency is a different claim and must not be printed');
  assert.doesNotMatch(html, /<1 ms/, 'the raw character would be parsed as markup, not text');
});

test('figures at or above one millisecond keep one decimal', () => {
  const html = render({ avg: 1.2, latest: 123.4 });
  assert.match(html, /1\.2 ms/);
  assert.match(html, /123\.4 ms/, 'rounding 123.4 to 123 would erase the measured fraction');
});

test('the boundary is one millisecond: 0.999 is below it, 1 is not', () => {
  assert.match(render({ latest: 0.999 }), /&lt;1 ms/);
  assert.match(render({ latest: 1 }), /1\.0 ms/);
});

test('percentiles and deviation use the same formatter', () => {
  const html = render({ p50: 3.4, p99: 0.2, stddev: 9.87 });
  assert.match(html, /3\.4 ms/);
  assert.match(html, /&lt;1 ms/);
  assert.match(html, /9\.9 ms/);
});

test('a missing measurement leaves no row at all', () => {
  const html = render({});
  assert.ok(!html.includes(' ms'), 'no latency row should be rendered when the server sent none');
  assert.ok(!html.includes('&lt;1'), 'and no placeholder that looks like a measurement');
  // The rows that do not depend on latency are still there.
  assert.match(html, /chart\.total/);
  assert.match(html, /chart\.valid/);
});
