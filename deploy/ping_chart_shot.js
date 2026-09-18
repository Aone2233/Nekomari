#!/usr/bin/env node
// Screenshot the Ping tab and read what the chart actually rendered.
//
// The theme's own tooltip says holes of 1-2 samples are bridged automatically, and the
// API hands it ~99% coverage with only 1-minute holes -- so a line that still looks
// broken is a rendering or value problem, not a sampling one. This captures the chart
// plus the numbers behind it so the two can be compared directly.
//
// Usage: node ping_chart_shot.js <base> <user> <pass> <instance-uuid> [range] [outdir]
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];
const range = process.argv[6] || '4 小时';
const out = process.argv[7] || '/tmp';

const clickVisible = (page, label) => page.evaluate((text) => {
  const visible = (el) => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  const b = Array.from(document.querySelectorAll('button')).filter(visible)
    .find((x) => x.textContent.trim() === text);
  if (b) { b.click(); return true; }
  return false;
}, label);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1200 } });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);
  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(5000);

  await clickVisible(page, 'Ping');
  await page.waitForTimeout(4000);
  console.log(`  clicked ${range}:`, await clickVisible(page, range));
  await page.waitForTimeout(10000);

  const state = await page.evaluate(() => {
    const meta = document.querySelector('.instance-chart-meta');
    const chart = document.querySelector('[class*="chart"] canvas, canvas');
    return {
      meta: meta ? meta.innerText.replace(/\s+/g, ' ').trim() : '',
      canvas: chart ? { w: chart.width, h: chart.height } : null,
      buttons: Array.from(document.querySelectorAll('button'))
        .filter((b) => b.offsetWidth).map((b) => b.textContent.trim()).filter((t) => t && t.length < 14),
    };
  });
  console.log('  meta       :', state.meta);
  console.log('  canvas     :', JSON.stringify(state.canvas));
  console.log('  buttons    :', JSON.stringify(state.buttons));

  const chartEl = await page.$('.instance-chart-controls, .instance-chart');
  await page.screenshot({ path: `${out}/ping-chart.png`, fullPage: false });
  if (chartEl) await chartEl.screenshot({ path: `${out}/ping-chart-crop.png` }).catch(() => {});
  console.log('  screenshot :', `${out}/ping-chart.png`);

  await browser.close();
})();
