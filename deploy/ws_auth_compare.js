#!/usr/bin/env node
// Is the "WebSocket ... HTTP Authentication failed" noise real, or an artefact of
// how the probe logs in?
//
// ip_panel_probe.js signs in with fetch() inside page.evaluate, which sets the
// session cookie but never goes through the app's own login flow. If the app
// stores something else at login time that the WebSocket needs, the failure is
// the harness's fault. If it fails after a real form login too, it is a defect.
//
// Runs the same page load twice -- once each way -- and compares.
//
// Usage: node ws_auth_compare.js <base> <user> <pass>
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];

async function run(mode) {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();

  const wsFail = [];
  const wsOpen = [];
  const consoleErr = [];
  const apiFail = [];

  page.on('console', (m) => {
    const t = m.text();
    if (t.includes('WebSocket') && /fail|error|Authentication/i.test(t)) wsFail.push(t.slice(0, 140));
    else if (m.type() === 'error') consoleErr.push(t.slice(0, 140));
  });
  page.on('websocket', (ws) => {
    ws.on('socketerror', (e) => wsFail.push(`socketerror ${String(e).slice(0, 80)}`));
    ws.on('open', () => wsOpen.push(ws.url().replace(base, '')));
    ws.on('close', () => {});
  });
  page.on('response', (r) => {
    const u = r.url();
    if (u.includes('/api/') && r.status() >= 400) {
      apiFail.push(`${r.status()} ${u.replace(base, '')}`);
    }
  });

  await page.goto(base + '/admin', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2000);

  if (mode === 'form') {
    // Drive the real login UI so whatever the app persists at login is persisted.
    const filled = await page.evaluate(() => {
      const ins = Array.from(document.querySelectorAll('input'));
      return ins.map((i) => i.type + ':' + (i.name || i.id || i.placeholder || ''));
    });
    const userBox = page.locator('input[type="text"], input[name="username"], input:not([type])').first();
    const passBox = page.locator('input[type="password"]').first();
    try {
      await userBox.fill(user, { timeout: 8000 });
      await passBox.fill(pass, { timeout: 8000 });
      await page.keyboard.press('Enter');
      await page.waitForTimeout(4000);
      console.log(`  [form] inputs seen: ${JSON.stringify(filled)}`);
    } catch (e) {
      console.log('  [form] could not drive the form: ' + e.message.split('\n')[0]);
    }
  } else {
    await page.evaluate(async ([u, p]) => {
      await fetch('/api/login', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: u, password: p }),
      });
    }, [user, pass]);
    await page.waitForTimeout(1500);
  }

  // Load a page that holds a live connection, which is where the WS opens.
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(8000);

  const cookies = (await ctx.cookies()).map((c) => c.name);
  await browser.close();
  return { mode, wsFail, wsOpen, consoleErr, apiFail, cookies };
}

(async () => {
  for (const mode of ['fetch', 'form']) {
    const r = await run(mode);
    console.log(`\n=== login via ${r.mode} ===`);
    console.log(`  cookies set        : ${JSON.stringify(r.cookies)}`);
    console.log(`  websockets opened  : ${r.wsOpen.length} ${JSON.stringify(r.wsOpen.slice(0, 3))}`);
    console.log(`  websocket failures : ${r.wsFail.length}`);
    for (const f of r.wsFail.slice(0, 3)) console.log(`      ${f}`);
    console.log(`  api >=400          : ${r.apiFail.length} ${JSON.stringify(r.apiFail.slice(0, 3))}`);
    console.log(`  other console errs : ${r.consoleErr.length}`);
  }
})();
