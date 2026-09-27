import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

/**
 * The SLA report's own source-level contract.
 *
 * The rules every standalone page shares — not a router route, its own isolated build, one
 * file, no service worker, nothing in `/assets/` — live in `standalone-pages.test.mjs`, which
 * covers each page in a table. What is here is what is specific to the report:
 *
 *   - it renders the report page and not the whole app, because the app's router is the thing
 *     an installed theme replaces
 *   - it does not point at the removed `pages/status/` path, which is where v0.1.31's
 *     unreachable route used to live
 *
 * The artefact half is in `web/public/standalone_pages_test.go`.
 */

const entry = readFileSync(new URL('../src/entries/sla.tsx', import.meta.url), 'utf8');
const routes = readFileSync(new URL('../src/routes.ts', import.meta.url), 'utf8');

test('the entry mounts the report, not the app', () => {
  assert.match(entry, /SlaReportPage/, 'the entry must render the report page');
  assert.match(
    entry,
    /from ["']\.\.\/pages\/sla["']/,
    'the entry must import the page from its own module',
  );
});

test('the report is not reachable through the built-in router', () => {
  // Belt and braces next to the shared test, because this path is the one that shipped
  // broken: v0.1.31 registered `pages/status` as a route, an installed theme replaced the
  // router, and the panel answered its own 404 for a live feature.
  assert.doesNotMatch(routes, /pages\/sla|pages\/status/, 'the report must not be a route');
  assert.match(routes, /standalone entry/i, 'the reason must stay recorded in routes.ts');
});

test('the report page module is the one the entry imports', () => {
  // A rename that updated one side and not the other would fail at build time, but failing
  // here says which two things disagree rather than pointing at a bundler error.
  const page = readFileSync(new URL('../src/pages/sla/index.tsx', import.meta.url), 'utf8');
  assert.match(page, /export const SlaReportPage/, 'the page must export the component by name');
  assert.match(page, /SlaReportTable/, 'the page must render the report table');
});
