/**
 * Capture exactly what the theme receives from the ip-info endpoints.
 *
 * Installs a fetch interceptor before any app code runs, so the response bodies
 * the theme's strict schemas actually parse are recorded verbatim. This is the
 * only way to tell "the server sent something the theme rejects" apart from
 * "the theme gates the UI on something else".
 */
const { chromium } = require('playwright');

const BASE = (process.argv[2] || 'https://komari.orderly2233.org').replace(/\/$/, '');
const USER = process.argv[3];
const PASS = process.argv[4];

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });

  await page.addInitScript(() => {
    window.__ipinfo = [];
    const orig = window.fetch;
    window.fetch = async function (...args) {
      const url = typeof args[0] === 'string' ? args[0] : (args[0] && args[0].url) || '';
      const res = await orig.apply(this, args);
      if (url.includes('ip-info')) {
        const clone = res.clone();
        let body = null;
        try { body = await clone.json(); } catch { /* not json */ }
        window.__ipinfo.push({ url: url.split('/api')[1] || url, status: res.status, body });
      }
      return res;
    };
  });

  await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 });
  await page.evaluate(async ([base, user, pass]) => {
    await fetch(base + '/api/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: user, password: pass }), credentials: 'include',
    });
  }, [BASE, USER, PASS]);
  await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 });
  await page.waitForTimeout(3000);

  // Go to the node detail.
  const node = page.getByText(/OC424/i).first();
  if (await node.count()) await node.click({ timeout: 10000 }).catch(() => {});
  await page.waitForTimeout(8000);

  const captured = await page.evaluate(() => window.__ipinfo || []);
  console.log(`\n=== captured ip-info responses: ${captured.length} ===`);
  for (const c of captured) {
    console.log(`\n--- ${c.status} ${c.url}`);
    const b = c.body;
    if (b && b.data) {
      console.log('    ok=' + b.ok + ' available=' + b.data.available +
                  ' excluded=' + b.data.excluded + ' family=' + (b.data.address && b.data.address.family));
      if (b.data.capabilities) console.log('    capabilities=' + JSON.stringify(b.data.capabilities));
      if (b.data.location) console.log('    country=' + b.data.location.country_code + ' city=' + b.data.location.city);
      if (b.data.network) console.log('    asn=' + b.data.network.asn + ' route=' + b.data.network.route);
      if (b.meta) console.log('    meta.cache=' + b.meta.cache + ' warning=' + b.meta.warning);
    } else {
      console.log('    body=' + JSON.stringify(b).slice(0, 200));
    }
  }

  // Also report which tabs rendered.
  const tabs = await page.evaluate(() => Array.from(
    document.querySelectorAll('.instance-segmented button')).map((b) => b.textContent.trim()));
  console.log(`\ntabs rendered: ${JSON.stringify(tabs)}`);

  await page.screenshot({ path: '/tmp/ip-panel-detail.png' });
  await browser.close();
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
