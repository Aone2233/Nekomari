import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { gzipSync } from 'node:zlib';
import { assetBudget } from './asset-budget.mjs';

function fixture(context) {
  const root = mkdtempSync(path.join(tmpdir(), 'nekomari-assets-'));
  context.after(() => rmSync(root, { force: true, recursive: true }));
  for (const [directory, key, source] of [['', 'index.html', 'public'], ['standalone/admin', 'admin.html', 'admin']]) {
    const tree = path.join(root, directory);
    mkdirSync(path.join(tree, '.vite'), { recursive: true });
    writeFileSync(path.join(tree, '.vite/manifest.json'), JSON.stringify({ [key]: { isEntry: true, file: 'entry.js' } }));
    writeFileSync(path.join(tree, 'entry.js'), source);
  }
  return root;
}

test('reports both entries separately and charges each precached URL once', context => {
  const root = fixture(context);
  writeFileSync(path.join(root, 'icon.ico'), 'icon');
  writeFileSync(path.join(root, 'sw.js'), 'wb.precacheAndRoute([{url:"icon.ico",revision:"a"},{url:"entry.js"},{url:"./icon.ico",revision:"a"}]);');
  const report = assetBudget(root);
  assert.equal(report.publicEntry.rawBytes, 6);
  assert.equal(report.adminEntry.rawBytes, 5);
  assert.deepEqual(report.publicPrecache, { distinctUrls: 2, rawBytes: 10,
    gzipBytes: ['icon', 'public'].reduce((total, body) => total + gzipSync(body, { level: 9 }).length, 0) });
});

test('refuses missing, dynamic, ambiguous and nonlocal build inputs', context => {
  const root = fixture(context);
  for (const worker of ['', 'w.precacheAndRoute(list)', 'w.precacheAndRoute([{url:getUrl()}])',
    'w.precacheAndRoute([{url:"entry.js"}]);w.precacheAndRoute([])',
    'w.precacheAndRoute([{url:"entry.js",...extra}])', 'w.precacheAndRoute([{url:"entry.js",revision:stamp}])',
    'w.precacheAndRoute([{url:"entry.js",revision:"a"},{url:"entry.js",revision:"b"}])',
    'w.precacheAndRoute([{url:"../icon"}])', 'w.precacheAndRoute([{url:"missing"}])']) {
    writeFileSync(path.join(root, 'sw.js'), worker);
    assert.throws(() => assetBudget(root));
  }
});
