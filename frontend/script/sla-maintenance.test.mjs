import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import vm from 'node:vm';
import test from 'node:test';
import React from 'react';
import { renderToString } from 'react-dom/server';
import i18next from 'i18next';
import ts from 'typescript';

/**
 * The SLA report's maintenance explanation (v1.6.18).
 *
 * The server removes window time from both sides of the availability ratio, so a node's coverage
 * can come out lower than the wall-clock window through no fault of the node. `NodeReport`
 * carries `maintenance_excluded_seconds` so the page can say why; without it the shortage reads
 * as a node that stopped reporting. Two properties are asserted, both of which a careless
 * implementation gets wrong:
 *
 *   - **zero and absent render nothing.** "0m excluded" would suggest an exclusion that did not
 *     happen, and the overwhelming majority of reports have no window at all.
 *   - **the label is the existing `maintenance.title` string**, not a new one: the report reuses
 *     the maintenance feature's wording rather than growing a second name for the same thing.
 *
 * Rendered through the real component with `renderToString`, because the guard and the label are
 * presentation, not data. The fixtures under `script/status-sla.browser.*` cover the same page in
 * a browser; this one runs in `npm test`.
 */

const requireHere = createRequire(import.meta.url);
// The component modules below are executed with `requireHere`, so the provider has to come from
// the same registry: react-i18next ships separate ESM and CJS builds, and mixing them hands the
// component a different context object — `t` then returns keys and the assertion below would be
// inspecting the wrong render.
const { I18nextProvider } = requireHere('react-i18next');
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
      process,
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

const i18n = i18next.createInstance();
await i18n.init({
  lng: 'en',
  // The test asserts rendering, not i18next's contribution banner.
  showSupportNotice: false,
  resources: {
    en: {
      translation: {
        // A value no component could produce by accident: the assertion is that the maintenance
        // namespace's key is used, not that some string was printed.
        'maintenance.title': 'MW-LABEL',
        'status.window': 'Window',
        'status.window_24h': '24 hours',
        'status.window_7d': '7 days',
        'status.window_30d': '30 days',
        'status.window_90d': '90 days',
        'status.coverageOf': '{{percent}} ({{observed}}/{{expected}} samples)',
        'status.sampleInterval': 'Buckets of {{seconds}}s',
        'status.noData': 'no data',
      },
    },
  },
});

const reportWith = (nodeFields) => ({
  start: '2026-09-26T12:00:00Z',
  end: '2026-09-26T13:00:00Z',
  window: '24h',
  interval_seconds: 60,
  count: 1,
  window_presets: { '24h': 24, '7d': 168, '30d': 720, '90d': 2160 },
  nodes: [
    {
      entity_id: 'node-a',
      presence: {
        expected_buckets: 60,
        observed_buckets: 60,
        coverage: 1,
        first_data: '2026-09-26T12:00:00Z',
        last_data: '2026-09-26T12:59:00Z',
      },
      has_report: true,
      reporting_gaps: [],
      tasks: [],
      ...nodeFields,
    },
  ],
});

const render = (report) => {
  const { SlaReportTable } = load(path.join(srcRoot, 'components/status/SlaReportTable.tsx'));
  return renderToString(
    React.createElement(
      I18nextProvider,
      { i18n },
      React.createElement(SlaReportTable, { report, window: '24h', nodeNames: { 'node-a': 'Tokyo' } }),
    ),
  );
};

test('the node report types the maintenance exclusion as optional seconds', () => {
  const source = readFileSync(new URL('../src/types/Sla.ts', import.meta.url), 'utf8');
  assert.match(
    source,
    /maintenance_excluded_seconds\?: number;/,
    'the field mirrors NodeReport.MaintenanceExcludedSeconds and is absent when nothing was excluded',
  );
});

test('a positive exclusion is explained with the maintenance wording and a readable duration', () => {
  const html = render(reportWith({ maintenance_excluded_seconds: 600 }));
  assert.match(html, /data-testid="sla-maintenance-excluded"/, 'the badge must be rendered');
  assert.match(html, /data-seconds="600"/, 'the raw figure stays addressable for tests');
  assert.match(html, /MW-LABEL/, 'the label comes from the maintenance.title key');
  assert.match(html, /10m/, '600s is rendered in minutes');
});

test('zero and absent exclusions render nothing at all', () => {
  for (const [label, fields] of [
    ['absent', {}],
    ['zero', { maintenance_excluded_seconds: 0 }],
  ]) {
    const html = render(reportWith(fields));
    assert.doesNotMatch(html, /sla-maintenance-excluded/, `${label}: no badge is expected`);
    assert.doesNotMatch(html, /MW-LABEL/, `${label}: the maintenance label must not appear`);
  }
});

test('the duration helper speaks the report\'s units', () => {
  const { maintenanceExcludedText } = load(path.join(srcRoot, 'components/status/slaFormat.ts'));
  // Seconds on the wire, minutes/hours on screen: forgetting the unit conversion prints "0m".
  assert.equal(maintenanceExcludedText(600), '10m');
  assert.equal(maintenanceExcludedText(5_400), '1.5h');
  assert.equal(maintenanceExcludedText(72 * 3_600), '3.0d');
  assert.equal(maintenanceExcludedText(undefined), '');
  assert.equal(maintenanceExcludedText(0), '');
  assert.equal(maintenanceExcludedText(-30), '');
});
