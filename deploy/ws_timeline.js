#!/usr/bin/env node
// When do the theme's WebSocket 401s happen relative to login?
//
// ws_upgrade_probe.js showed an authenticated upgrade to /api/rpc2 succeeds and
// returns a real reply. So the failures seen on page load must be attempts made
// while still unauthenticated. This records the order of events to confirm it,
// and checks whether the client recovers afterwards -- a 401 that is retried and
// then succeeds is cosmetic noise; one that never recovers is a real defect.
//
// Usage: node ws_timeline.js <base> <user> <pass>
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const events = [];
  const t0 = Date.now();
  const stamp = () => `+${((Date.now() - t0) / 1000).toFixed(1)}s`;

  page.on('websocket', (ws) => {
    if (!ws.url().includes('/api/rpc2')) return;
    events.push(`${stamp()} WS OPEN   ${ws.url().replace(base, '')}`);
    ws.on('close', () => events.push(`${stamp()} WS CLOSE`));
  });
  page.on('response', (r) => {
    if (r.url().includes('/api/rpc2') && r.status() >= 400) {
      events.push(`${stamp()} HTTP ${r.status()} ${r.request().method()} /api/rpc2`);
    }
  });
  page.on('console', (m) => {
    const t = m.text();
    if (/WebSocket/i.test(t) && /fail|error|Authentication/i.test(t)) {
      events.push(`${stamp()} CONSOLE ${t.slice(0, 90)}`);
    }
  });

  await page.goto(base + '/admin', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3000);
  events.push(`${stamp()} --- logging in ---`);

  try {
    await page.locator('input[type="text"], input[name="username"]').first().fill(user, { timeout: 8000 });
    await page.locator('input[type="password"]').first().fill(pass, { timeout: 8000 });
    await page.keyboard.press('Enter');
  } catch (e) {
    events.push(`${stamp()} login form failed: ${e.message.split('\n')[0]}`);
  }
  await page.waitForTimeout(6000);

  const before = events.filter((e) => e.includes('401') && e.indexOf('logging in') > e.indexOf('401')).length;
  console.log('  === event timeline ===');
  for (const e of events) console.log('    ' + e);

  // Does a fresh page load after login still fail?
  events.length = 0;
  events.push(`${stamp()} --- reloading while logged in ---`);
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(8000);
  for (const e of events) console.log('    ' + e);

  const after401 = events.filter((e) => e.includes('401')).length;
  const opened = events.filter((e) => e.includes('WS OPEN')).length;
  console.log('');
  console.log(`  failures BEFORE login : ${before}`);
  console.log(`  failures AFTER  login : ${after401}`);
  console.log(`  sockets opened after  : ${opened}`);
  console.log(after401 === 0 && opened > 0
    ? '  => pre-login noise only; the client recovers'
    : '  => still failing while authenticated; real defect');
  await browser.close();
})();
