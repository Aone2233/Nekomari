import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

/**
 * Operator tools stay inside the panel-owned /admin shell, not standalone documents.
 * Public themes can replace / but cannot replace /admin; standalone builds remain
 * available for old links. Keep normal router navigation and the mapped icons.
 */

const menuConfig = JSON.parse(
  readFileSync(new URL('../src/config/menuConfig.json', import.meta.url), 'utf8'),
);

// Kept in step with `standalone-pages.test.mjs`, which holds the build-side contract. A page in
// that table and not in this one is not checked here at all.
const PAGES = ['sla', 'bulk', 'maintenance', 'config', 'forecast'];

const operations = menuConfig.menu.find((item) => item.labelKey === 'operations.title');

/** The icon names `iconMap` actually exports, read from its object literal. */
function iconNames() {
  const source = readFileSync(new URL('../src/utils/iconHelper.ts', import.meta.url), 'utf8');
  const literal = source.match(/export const iconMap[^=]*=\s*\{([\s\S]*?)\n\};/);
  assert.ok(literal, 'iconHelper.ts must keep the iconMap object literal the menu resolves through');
  return new Set(
    [...literal[1].matchAll(/^\s*([A-Za-z][A-Za-z0-9]*)\s*,?\s*$/gm)].map((match) => match[1]),
  );
}

test('the operations group is where the operator pages are listed', () => {
  assert.ok(operations, 'menuConfig.json must keep the operations.title group');
  assert.ok(
    Array.isArray(operations.children) && operations.children.length > 0,
    'the operations group must have children: a top-level group with no entries renders nothing to open',
  );
});

test('every operator page uses same-shell navigation instead of a new document', () => {
  const icons = iconNames();
  const entries = operations.children;
  for (const page of PAGES) {
    const expected = `/admin/${page}`;
    const entry = entries.find((child) => child.path === expected);
    assert.ok(
      entry,
      `no menu entry points at ${expected}`,
    );
    assert.equal(
      entry.newTab === true,
      false,
      `${expected} must keep the current tab and its theme provider`,
    );
    assert.notEqual(entry.reloadDocument, true, `${expected} must use the SPA router`);
    assert.ok(
      icons.has(entry.icon),
      `${expected} uses icon "${entry.icon}", which iconHelper.ts does not map; the sidebar would silently fall back to a grey dot`,
    );
  }
});

test('external documentation and the terminal keep their existing new-tab policy', () => {
  for (const key of ['operations.workbench', 'common.documentation', 'common.home']) {
    assert.equal(menuConfig.menu.find((item) => item.labelKey === key)?.newTab, true, key);
  }
});
