import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

/**
 * The standalone SLA report's wiring, checked at the source level.
 *
 * This is a *contract test*, and the contract it guards is the one that failed in
 * production: v0.1.31 shipped the report as a route in the built-in router, an installed
 * theme replaced that router, and the panel answered its own 404 for a feature whose
 * server half was live and tested. Every unit test passed, because nothing asserted that
 * the page could be *reached*.
 *
 * Two halves, deliberately in different places:
 *
 *   - here: the entry exists, mounts into #root, and is built by its own config
 *   - `web/public/standalone_test.go`: the built page is embedded, references only its own
 *     prefixed assets, and bundles React into one file
 *
 * The split is not arbitrary. This file has no build output to inspect, so it catches a
 * wiring mistake early and cheaply; the Go test has the artefacts and catches a build or
 * packaging mistake, which no source-level test can see.
 */

const entry = readFileSync(new URL('../src/entries/sla.tsx', import.meta.url), 'utf8');
const html = readFileSync(new URL('../sla.html', import.meta.url), 'utf8');
const config = readFileSync(new URL('../vite.standalone.config.ts', import.meta.url), 'utf8');
const routes = readFileSync(new URL('../src/routes.ts', import.meta.url), 'utf8');

test('the standalone entry mounts the report into #root', () => {
  assert.match(entry, /getElementById\(["']root["']\)/, 'the entry must find #root');
  assert.match(entry, /SlaReportPage/, 'the entry must render the report page');
  assert.match(
    entry,
    /from ["']\.\.\/pages\/sla["']/,
    'the entry must import the page from its own module',
  );
  assert.match(html, /<div id="root">/, 'sla.html must provide the mount point');
  assert.match(html, /src="\/src\/entries\/sla\.tsx"/, 'sla.html must load the entry');
});

test('the entry renders without the app router', () => {
  // The whole point of the standalone page: it must not depend on the router a theme
  // replaces. A Router import here would compile, run, and be useless in a themed
  // deployment — which is the failure being prevented.
  //
  // Matched on the import, not on the word: an earlier version asserted /\broutes\b/ and
  // failed on the English word in a comment, which is a test that would have been
  // "fixed" by deleting the comment.
  assert.doesNotMatch(entry, /from ["']react-router/, 'must not use the app router');
  assert.doesNotMatch(entry, /from ["'][^"']*\/routes["']/, 'must not import the route table');
  assert.doesNotMatch(entry, /useRoutes/, 'must not resolve routes');
});

test('the report is not a route in the built-in router', () => {
  // Belt and braces: if someone adds it back, this says why not, next to the reason.
  assert.doesNotMatch(
    routes,
    /pages\/sla|pages\/status/,
    'the SLA report must not be a router entry: an installed theme replaces that router',
  );
  assert.match(routes, /standalone entry/i, 'the reason must stay recorded in routes.ts');
});

test('the standalone build is isolated from the main one', () => {
  // Shared output would put its chunks in the /assets/ namespace the theme owns, and
  // public.go prefers the theme directory over the embedded bundle.
  assert.match(config, /outDir:\s*["']dist-standalone["']/, 'its own output directory');
  assert.match(config, /inlineDynamicImports:\s*true/, 'one file, no chunks to resolve');
  assert.match(config, /base:\s*["']\/standalone\/["']/, 'its own asset prefix');
  assert.match(config, /copyPublicDir:\s*false/, 'it does not need the flags and OS logos');
  assert.doesNotMatch(config, /VitePWA/, 'it must not register the panel service worker');
});

test('the prefix is one a theme cannot claim', () => {
  // `standalone/` is the directory the built page is published under. A theme may
  // legally contain a file at any other path, and public.go prefers the theme's copy.
  for (const field of [entry, html, config]) {
    assert.doesNotMatch(
      field,
      /["']\/assets\//,
      'nothing new may live in /assets/, which an installed theme owns',
    );
  }
});
