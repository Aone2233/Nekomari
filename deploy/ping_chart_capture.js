#!/usr/bin/env node
// Why does the ping chart look "断断续续"? Measure what the chart is actually given.
//
// The theme draws a break wherever a minute has no latency value, and its own tooltip
// says only 1-2 sample holes are bridged automatically. So a choppy line means either
// missing samples or genuine packet loss (a lost ping has no latency to plot). Those
// need opposite responses, so read the data instead of guessing.
//
// Usage: node ping_chart_capture.js <base> <user> <pass> <instance-uuid> [range-label]
//   range-label defaults to "4 小时"
// Set NEKOMARI_2FA_SECRET when the account has 2FA enabled.
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];
const rangeLabel = process.argv[6] || '4 小时';

const clickVisible = (page, label) => page.evaluate((text) => {
  const visible = (el) => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  const b = Array.from(document.querySelectorAll('button'))
    .filter(visible)
    .find((x) => x.textContent.trim() === text);
  if (b) { b.click(); return true; }
  return false;
}, label);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const rpc = [];
  page.on('response', async (r) => {
    if (!r.url().includes('/api/rpc2')) return;
    let body = null;
    try { body = await r.json(); } catch (_) { return; }
    const result = body && body.result;
    rpc.push({
      url: r.url().replace(base, ''),
      keys: result && typeof result === 'object' ? Object.keys(result).slice(0, 8) : [],
      size: JSON.stringify(body).length,
      body,
    });
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);

  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(5000);
  console.log('  ping tab   :', await clickVisible(page, 'Ping'));
  await page.waitForTimeout(4000);
  console.log(`  range ${rangeLabel} :`, await clickVisible(page, rangeLabel));
  await page.waitForTimeout(9000);

  const meta = await page.evaluate(() => {
    const el = document.querySelector('.instance-chart-meta');
    return el ? el.innerText.replace(/\s+/g, ' ').trim() : '';
  });
  console.log('  chart meta :', meta || '(not found)');

  // Walk whatever RPC payload carried a time series and measure the holes.
  const analyse = (body) => {
    const result = body && body.result;
    const points = result && result.points;
    if (!Array.isArray(points) || points.length < 2) return null;
    const first = points[0];
    const timeKey = ['time', 'timestamp', 'ts', 't'].find((k) => k in first) || Object.keys(first)[0];
    const keys = Object.keys(first).filter((k) => k !== timeKey);
    const report = [];
    for (const key of keys) {
      const times = points.filter((p) => p[key] !== null && p[key] !== undefined)
        .map((p) => Number(p[timeKey])).sort((a, b) => a - b);
      if (times.length < 2) continue;
      const step = Math.min(...times.slice(1).map((t, i) => t - times[i]).filter((d) => d > 0)) || 60000;
      const buckets = times.map((t) => Math.round(t / step));
      let holes = 0, missing = 0, longest = 0, visible = 0;
      for (let i = 1; i < buckets.length; i++) {
        const d = buckets[i] - buckets[i - 1];
        if (d > 1) {
          holes++;
          missing += d - 1;
          if (d - 1 > longest) longest = d - 1;
          if (d - 1 >= 3) visible++;   // the theme auto-bridges holes of 1-2 samples
        }
      }
      const span = buckets[buckets.length - 1] - buckets[0] + 1;
      report.push({ key, n: times.length, step, coverage: (times.length / span) * 100, holes, missing, longest, visible });
    }
    return report.length ? { step: report[0].step, report } : null;
  };

  let analysed = 0;
  for (const entry of rpc) {
    const report = analyse(entry.body);
    if (!report) continue;
    analysed++;
    console.log(`\n  --- series payload: keys=${JSON.stringify(entry.keys)} step=${report.step / 1000}s ---`);
    for (const r of report) {
      console.log(`      ${String(r.key).padEnd(18)} n=${String(r.n).padStart(4)} ` +
        `coverage=${r.coverage.toFixed(1)}%  holes=${String(r.holes).padStart(3)} ` +
        `missing=${String(r.missing).padStart(3)}  longest=${r.longest}  visible_breaks(>=3min)=${r.visible}`);
    }
  }
  if (!analysed) {
    console.log('\n  no series payload matched. rpc2 responses seen:');
    for (const e of rpc) console.log(`    size=${String(e.size).padStart(7)} keys=${JSON.stringify(e.keys)}`);
  }

  await browser.close();
})();
