#!/usr/bin/env node
// Does the theme's IP panel appear on a node that is definitely not CN?
//
// The theme gates it as `x.available && <button>IP 信息</button>`, and inside its
// status hook it computes `l = T(region) === 'CN'` and hides the panel when true.
// A run against a Hong Kong node still showed no tab, so the question is whether
// that gate is the cause or something else is.
//
// Usage: node ip_panel_probe.js <base> <user> <pass> <instance-uuid> [outdir]
const { chromium } = require('playwright');
const fs = require('fs');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];
const out = process.argv[6] || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const seen = [];
  page.on('response', async (r) => {
    const u = r.url();
    if (u.includes('/ip-info/')) {
      let body = '';
      try { body = (await r.text()).slice(0, 400); } catch (_) {}
      seen.push({ url: u.replace(base, ''), code: r.status(), body });
    }
  });
  const errors = [];
  page.on('console', (m) => {
    if (m.type() === 'error' || m.type() === 'warning') errors.push(m.text().slice(0, 160));
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);

  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(9000);

  const info = await page.evaluate(() => {
    const btns = Array.from(document.querySelectorAll('button')).map((b) => b.textContent.trim());
    return {
      ipTab: btns.filter((t) => t.includes('IP')),
      allTabs: btns.filter((t) => t.length > 0 && t.length < 12).slice(0, 14),
      panel: !!document.querySelector('.ip-info-panel'),
    };
  });

  console.log('  instance     :', uuid);
  console.log('  IP-ish tabs  :', JSON.stringify(info.ipTab));
  console.log('  all buttons  :', JSON.stringify(info.allTabs));
  console.log('  ip-info panel:', info.panel);
  console.log('  ip-info calls:', seen.length);
  for (const s of seen) console.log(`    ${s.code} ${s.url}`);
  const st = seen.find((s) => s.url.includes('/status'));
  if (st) console.log('  status body  :', st.body.replace(/\s+/g, ' ').slice(0, 220));
  console.log('  console errs :', errors.length, errors.slice(0, 2).join(' | '));
  await page.screenshot({ path: `${out}/ip-probe.png`, fullPage: false });
  await browser.close();
})();
