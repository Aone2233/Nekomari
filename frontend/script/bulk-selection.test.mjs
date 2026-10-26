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
 * The admin node list's selection has to reach the existing bulk edit page.
 *
 * The bulk page is an admin route, with checked uuids in the query, and the page opens
 * with them selected. Three things can silently break that chain, and each is asserted here:
 *
 *   - **an empty selection must not produce an entry.** A button that opens an empty bulk page
 *     teaches the operator the feature is broken, and every visitor sees that case first.
 *   - **the link and the page must agree on the parameter.** They are two halves of one contract;
 *     a rename on either side yields a working-looking link that opens with nothing selected.
 *   - **the page's default must stay empty.** The standalone route has no selection to receive,
 *     and it must not accidentally pick one up from an unrelated query string.
 *
 * Rendered with `renderToString` through the real components, because the guard and the initial
 * state are presentation, not data. The browser fixtures cover the same page's interactive
 * behaviour; this runs in `npm test`.
 */

const requireHere = createRequire(import.meta.url);
// The component modules are executed with `requireHere`, so the provider must come from the same
// registry: react-i18next ships separate ESM and CJS builds and mixing them hands the component a
// different context, which would make `t` return keys and the assertions inspect the wrong render.
const { I18nextProvider } = requireHere('react-i18next');
// Same registry for the Theme: Radix Themes' Select reads its context during SSR, and an ESM
// Theme wrapping a CJS Select is a different context object.
const { Theme } = requireHere('@radix-ui/themes');
const { MemoryRouter } = requireHere('react-router-dom');
const srcRoot = fileURLToPath(new URL('../src/', import.meta.url));
const modules = new Map();

function load(file, globals = {}) {
  file = path.resolve(file);
  // A module that reads `location` is loaded once per location, so both the "opened with
  // ?uuids=…" and the "opened empty" cases can be exercised in one process.
  const cacheKey = globals.location ? `${file}#${globals.location.search}` : file;
  if (modules.has(cacheKey)) return modules.get(cacheKey).exports;
  const module = { exports: {} };
  modules.set(cacheKey, module);
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
      // The vm sandbox has no web globals; the page reads one of these on mount.
      URLSearchParams,
      ...(globals.location ? { location: globals.location } : {}),
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
  showSupportNotice: false,
  resources: {
    en: {
      translation: {
        'bulk.title': 'Bulk edit',
        'bulk.subtitle': 'Apply one change to many nodes.',
        'bulk.fieldsHeading': 'Fields to change',
        'bulk.fieldsHint': 'A field is only sent when its switch is on.',
        'bulk.nodesHeading': 'Nodes',
        'bulk.selectAll': 'Select all',
        'bulk.selectNone': 'Select none',
        'bulk.selected': 'Selected',
        'bulk.node': 'Node',
        'bulk.group': 'Group',
        'bulk.weight': 'Weight',
        'bulk.apply': 'Apply to selection',
        'bulk.selectionCount': '{{count}} selected',
        'bulk.fieldCount': '{{count}} fields to send',
        'bulk.noFieldsHint': 'Switch on at least one field.',
        'bulk.loading': 'Loading nodes…',
        'bulk.error': 'The edit could not be sent',
        'bulk.reportSummary': '{{applied}} applied, {{failed}} failed of {{total}}',
        'bulk.failuresHeading': 'Not applied',
        'bulk.unknownError': 'no reason given',
        'bulk.fieldsApplied': 'Fields sent: {{fields}}',
        'bulk.field.group': 'Group',
        'bulk.field.tags': 'Tags',
        'bulk.field.weight': 'Weight',
        'bulk.field.hidden': 'Hidden',
        'bulk.field.price': 'Price',
        'bulk.field.billingCycle': 'Billing cycle',
        'bulk.field.currency': 'Currency',
        'bulk.field.trafficLimit': 'Traffic limit',
        'bulk.field.trafficLimitType': 'Limit type',
        'bulk.hidden.yes': 'hidden',
        'bulk.hidden.no': 'visible',
      },
    },
  },
});

const NODES = [
  { uuid: 'uuid-a', name: 'Tokyo Relay', group: 'asia', weight: 1, hidden: false },
  { uuid: 'uuid-b', name: 'Frankfurt Box', group: 'eu', weight: 2, hidden: false },
  { uuid: 'uuid-c', name: 'Silent Node', group: 'asia', weight: 3, hidden: true },
];

const renderBulkEntry = (selectedNodes) => {
  const { BulkEntry } = load(path.join(srcRoot, 'components/bulk/BulkEntry.tsx'));
  return renderToString(
    React.createElement(
      I18nextProvider,
      { i18n },
      React.createElement(MemoryRouter, null, React.createElement(BulkEntry, { selectedNodes })),
    ),
  );
};

const renderBulkPage = (props, globals = {}) => {
  const { BulkEditPage } = load(path.join(srcRoot, 'components/bulk/BulkEditPage.tsx'), globals);
  return renderToString(
    React.createElement(
      I18nextProvider,
      { i18n },
      React.createElement(Theme, null, React.createElement(BulkEditPage, { nodes: NODES, ...props })),
    ),
  );
};

/** rowTag returns the rendered <tr> for one uuid, so an assertion reads one row, not the page. */
const rowTag = (html, uuid) => {
  const match = html.match(new RegExp(`<tr[^>]*data-uuid="${uuid}"[^>]*>`));
  assert.ok(match, `the page must render a row for ${uuid}`);
  return match[0];
};

test('the link and the parser agree on the selection', () => {
  const { BULK_EDIT_PATH, bulkEditHref, selectionFromSearch } = load(
    path.join(srcRoot, 'components/bulk/bulkSelection.ts'),
  );
  assert.equal(BULK_EDIT_PATH, '/admin/bulk');
  assert.equal(bulkEditHref([]), BULK_EDIT_PATH, 'no selection, no query string');
  assert.equal(bulkEditHref(['uuid-a', ' uuid-b ', 'uuid-a']), `${BULK_EDIT_PATH}?uuids=uuid-a,uuid-b`, 'trimmed and de-duplicated');
  // Spread into this realm: the module is evaluated in a vm context, so its arrays carry another
  // realm's Array.prototype and deepStrictEqual would compare prototypes rather than contents.
  assert.deepEqual([...selectionFromSearch('?uuids=uuid-a,uuid-b')], ['uuid-a', 'uuid-b']);
  // The property that keeps the two halves from drifting: whatever the entry writes, the page
  // reads back as the same selection.
  assert.deepEqual(
    [...selectionFromSearch(new URL(bulkEditHref(['uuid-a', 'uuid-b']), 'http://panel').search)],
    ['uuid-a', 'uuid-b'],
  );
  // A hand-written or older link with the comma percent-encoded must parse too.
  assert.deepEqual([...selectionFromSearch('?uuids=uuid-a%2Cuuid-b')], ['uuid-a', 'uuid-b']);
  assert.deepEqual([...selectionFromSearch('')], []);
  assert.deepEqual([...selectionFromSearch('?uuids=,,')], []);
});

test('the entry appears only with a selection and links to the checked nodes', () => {
  assert.doesNotMatch(
    renderBulkEntry([]),
    /bulk-entry/,
    'an entry with nothing selected would open an empty bulk page',
  );

  const html = renderBulkEntry(['uuid-a', 'uuid-b']);
  assert.match(html, /data-testid="bulk-entry"/);
  assert.match(
    html,
    /href="\/admin\/bulk\?uuids=uuid-a,uuid-b"/,
    'the checked uuids travel in the in-panel link',
  );
  assert.doesNotMatch(html, /target="_blank"/, 'bulk edit must keep the admin document and its theme');
  assert.match(html, /2 selected/);
});

test('the bulk page opens with the selection it was handed', () => {
  const html = renderBulkPage({ initialSelection: ['uuid-b'] });
  assert.match(rowTag(html, 'uuid-b'), /data-selected="true"/);
  assert.match(rowTag(html, 'uuid-a'), /data-selected="false"/, 'other nodes are untouched');
  assert.match(
    html,
    /data-testid="bulk-selection-count"[^>]*data-count="1"/,
    'the count reflects the initial selection',
  );
});

test('the standalone page takes the selection from its URL, and stays empty without one', () => {
  const fromUrl = renderBulkPage({}, { location: { search: '?uuids=uuid-c' } });
  assert.match(rowTag(fromUrl, 'uuid-c'), /data-selected="true"/);
  assert.match(fromUrl, /data-testid="bulk-selection-count"[^>]*data-count="1"/);

  // Default behaviour of the independent route: nothing selected.
  const empty = renderBulkPage({});
  assert.match(empty, /data-testid="bulk-selection-count"[^>]*data-count="0"/);
  assert.match(rowTag(empty, 'uuid-a'), /data-selected="false"/);
});

test('the admin node list renders the entry from its own selection state', () => {
  const page = readFileSync(new URL('../src/pages/admin/index.tsx', import.meta.url), 'utf8');
  assert.match(
    page,
    /const \[selectedNodes, setSelectedNodes\] = useState<string\[\]>\(\[\]\);/,
    'the page owns the selection the table updates',
  );
  assert.match(
    page,
    /<BulkEntry selectedNodes=\{selectedNodes\} \/>/,
    'the entry must receive that selection, not keep one of its own',
  );
});
