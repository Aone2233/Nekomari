#!/usr/bin/env node
// Read the theme's own gate values instead of reasoning about minified code.
//
// The bundle has been patched (temporarily) to publish `gn`'s return value and the
// arguments it was called with on window.__gn / window.__gate. That turns "why is
// available false" from a reading exercise into a value dump.
//
// Usage: node ip_gate_dump.js <base> <user> <pass> <instance-uuid>
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const lookups = [];
  page.on('response', async (r) => {
    if (r.url().includes('/ip-info/')) {
      let b = '';
      try { b = (await r.text()).slice(0, 200); } catch (_) {}
      lookups.push({ url: r.url().replace(base, ''), status: r.status(), body: b });
    }
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);

  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(10000);

  const dump = await page.evaluate(() => ({
    gate: window.__gate || null,
    gn: window.__gn
      ? {
          available: window.__gn.available,
          lookupsType: Array.isArray(window.__gn.lookups) ? 'array' : typeof window.__gn.lookups,
          lookupsLen: Array.isArray(window.__gn.lookups) ? window.__gn.lookups.length : null,
          lookups: Array.isArray(window.__gn.lookups)
            ? window.__gn.lookups.map((x) => ({
                hasX: !!x,
                hasData: !!(x && x.data),
                dataType: x && x.data ? typeof x.data : null,
                dataKeys: x && x.data && typeof x.data === 'object' ? Object.keys(x.data).slice(0, 12) : null,
                excluded: x && x.data ? x.data.excluded : 'n/a',
              }))
            : null,
        }
      : null,
    tabs: Array.from(document.querySelectorAll('button')).map((b) => b.textContent.trim()).filter((t) => t && t.length < 14),
  }));

  console.log('  __gate (arguments to gn):');
  console.log('   ', JSON.stringify(dump.gate));
  console.log('');
  console.log('  __gn (return value):');
  console.log('   ', JSON.stringify(dump.gn, null, 2).replace(/\n/g, '\n    '));
  console.log('');
  console.log('  lookups seen by the browser:');
  for (const l of lookups) console.log(`    ${l.status} ${l.url}\n        ${l.body.replace(/\s+/g, ' ').slice(0, 170)}`);
  console.log('');
  console.log('  tabs:', JSON.stringify(dump.tabs));
  await browser.close();
})();
