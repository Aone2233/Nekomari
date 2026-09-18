#!/usr/bin/env node
// Which theme chunks does the instance page actually load?
//
// Every probe injected into Instance-B37568w_.js came back null, which means the
// component holding the IP-tab code never runs on this route. Either that chunk is
// never fetched, or a different component renders the instance view. The list of
// loaded chunks distinguishes the two.
//
// Usage: node chunk_list.js <base> <user> <pass> <instance-uuid>
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();

  const js = new Set();
  page.on('response', (r) => {
    const u = r.url();
    if (u.endsWith('.js')) js.add(u.split('/').pop());
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);

  js.clear();
  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(10000);

  console.log('  chunks loaded on the instance page:');
  for (const f of [...js].sort()) console.log('    ' + f);

  const hasInstance = [...js].some((f) => f.startsWith('Instance-'));
  console.log('');
  console.log('  Instance-*.js loaded:', hasInstance);

  const tabs = await page.evaluate(() =>
    Array.from(document.querySelectorAll('button')).map((b) => b.textContent.trim()).filter((t) => t && t.length < 14));
  console.log('  tabs:', JSON.stringify(tabs));
  await browser.close();
})();
