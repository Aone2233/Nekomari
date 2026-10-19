import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

/**
 * The five standalone pages are only usable if the panel's own menu reaches them.
 *
 * Nothing else links to them. They are deliberately *not* routes of the built-in router (see
 * `standalone-pages.test.mjs`: an installed theme replaces that router), a theme cannot be expected
 * to know they exist, and their URLs are fixed by the standalone build — `vite.standalone.shared.ts`
 * sets `base` to `/standalone/${page}/`, and `build-standalone.mjs` copies the document there as
 * `<page>.html`. The sidebar is therefore the only entrance: a page missing from it is a page that
 * shipped and that nobody can open, which is the state these five were in before this test existed.
 *
 * Three properties are pinned per page:
 *
 *   - **the path equals the build's output path**, `/standalone/<page>/<page>.html`. A mismatch is
 *     not a link the browser notices; it is a 404 the operator sees *after* clicking, because the
 *     file is copied verbatim and nothing rewrites the URL.
 *   - **it opens in a new tab.** Navigating the admin's own document to a standalone page would
 *     replace the SPA and leave the operator with no way back. `/terminal` is `newTab` for the same
 *     reason.
 *   - **the icon is a name `iconHelper.ts` maps.** An unknown name is not an error anywhere: the
 *     sidebar falls back to a grey dot, so a typo survives review and a peer review, and only the
 *     person looking at the menu sees it.
 *
 * The entries also have to sit under the "operations" group rather than at the top level: they are
 * operator tools, and the group is the menu an operator opens when looking for one.
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

test('the operations group is where the standalone pages are listed', () => {
  assert.ok(operations, 'menuConfig.json must keep the operations.title group');
  assert.ok(
    Array.isArray(operations.children) && operations.children.length > 0,
    'the operations group must have children: a top-level group with no entries renders nothing to open',
  );
});

test('every standalone page has a menu entry that points at the built document', () => {
  const icons = iconNames();
  const entries = operations.children;
  for (const page of PAGES) {
    const expected = `/standalone/${page}/${page}.html`;
    const entry = entries.find((child) => child.path === expected);
    assert.ok(
      entry,
      `no menu entry points at ${expected}; the page builds, but the menu is the only way to open it`,
    );
    assert.equal(
      entry.newTab,
      true,
      `${expected} must open in a new tab: the sidebar lives in the admin document, and replacing it would strand the operator`,
    );
    assert.ok(
      icons.has(entry.icon),
      `${expected} uses icon "${entry.icon}", which iconHelper.ts does not map; the sidebar would silently fall back to a grey dot`,
    );
  }
});
