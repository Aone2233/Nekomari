#!/usr/bin/env node
// Which endpoint does the theme use for account state, and does it answer?
//
// The IP tab's gate is `b = !r && n?.logged_in === true` where `r` is `isPending`
// and `n` is `data` from one specific query. Adding logged_in to /api/public was
// necessary but not sufficient, so the remaining question is what that query is and
// whether it resolves -- it may be an RPC call over the WebSocket client, which
// fails before login and could be cached that way.
//
// Usage: node account_query_probe.js <base> <user> <pass> <instance-uuid>
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();

  const calls = [];
  page.on('response', async (r) => {
    const u = r.url();
    if (!/\/api\/(me|public|rpc2)/.test(u)) return;
    let body = '';
    try { body = (await r.text()).slice(0, 260); } catch (_) {}
    calls.push({ url: u.replace(base, '').slice(0, 90), status: r.status(), method: r.request().method(), body });
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);

  calls.length = 0;
  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(9000);

  console.log('  account/public/rpc calls after navigating to the instance page:');
  for (const c of calls) {
    const hasLoggedIn = /"logged_in"\s*:\s*(true|false)/.exec(c.body);
    console.log(`    ${c.status} ${c.method} ${c.url}`);
    if (hasLoggedIn) console.log(`        logged_in=${hasLoggedIn[1]}`);
    else if (c.body) console.log(`        body: ${c.body.replace(/\s+/g, ' ').slice(0, 110)}`);
  }
  if (!calls.length) console.log('    (none)');

  const tabs = await page.evaluate(() =>
    Array.from(document.querySelectorAll('button')).map((b) => b.textContent.trim()).filter((t) => t && t.length < 14));
  console.log('');
  console.log('  tabs:', JSON.stringify(tabs));
  await browser.close();
})();
