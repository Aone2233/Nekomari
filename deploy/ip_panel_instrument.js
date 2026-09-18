#!/usr/bin/env node
// Who actually calls the ip-info endpoints, and what does the theme do with the
// answer?
//
// The server side is confirmed correct and two hypotheses are already dead (the
// mainland-China region gate, and the WebSocket auth noise). What has not been
// established is whether the lookups even come from the IP panel's own hook --
// they could be fired by another component, which would explain why the panel's
// gate evaluates false while the network looks healthy.
//
// Patching fetch before any app code runs and recording the stack for ip-info
// requests answers that directly.
//
// Usage: node ip_panel_instrument.js <base> <user> <pass> <instance-uuid>
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();

  // Install the interceptor as the very first thing in every document, before the
  // app bundle can capture a reference to fetch.
  await ctx.addInitScript(() => {
    window.__ipInfoCalls = [];
    const orig = window.fetch;
    window.fetch = function (...args) {
      const url = typeof args[0] === 'string' ? args[0] : (args[0] && args[0].url) || '';
      if (url.includes('/ip-info/')) {
        const stack = (new Error().stack || '').split('\n').slice(1, 7).join(' | ');
        const entry = { url: url.split('?')[0], stack, at: Date.now() };
        window.__ipInfoCalls.push(entry);
      }
      return orig.apply(this, args);
    };
    // TanStack Query keeps its cache on the client; if the app exposes it, reading
    // the query state tells us whether the gate saw data or undefined.
    window.__queryClients = [];
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);
  console.log('  login ok');

  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(9000);

  const out = await page.evaluate(() => ({
    calls: window.__ipInfoCalls || [],
    tabs: Array.from(document.querySelectorAll('button'))
      .map((b) => b.textContent.trim())
      .filter((t) => t && t.length < 14),
    hasPanel: !!document.querySelector('.ip-info-panel'),
  }));

  console.log('  ip-info calls and their call sites:');
  const seen = new Set();
  for (const c of out.calls) {
    const key = c.url + c.stack.slice(0, 120);
    if (seen.has(key)) continue;
    seen.add(key);
    console.log(`    ${c.url}`);
    console.log(`      stack: ${c.stack.slice(0, 300)}`);
  }
  console.log('');
  console.log('  buttons on page :', JSON.stringify(out.tabs));
  console.log('  ip-info panel   :', out.hasPanel);

  await browser.close();
})();
