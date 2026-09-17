/**
 * End-to-end check of the LuminaPlus "IP 信息" panel on the live panel.
 *
 * Why a browser: the API can be validated with curl, but the panel is rendered by
 * a strict-schema client. Only a real browser proves the theme actually consumes
 * the new endpoints, and it surfaces anything the API check cannot see (a thrown
 * parse error, a console error, a panel stuck on "刷新失败").
 *
 * Run on the host that can reach the public URL:
 *   node ip_panel_check.js <base-url> <username> <password> [outdir]
 */
const { chromium } = require('playwright');

const BASE = (process.argv[2] || 'https://komari.orderly2233.org').replace(/\/$/, '');
const USER = process.argv[3];
const PASS = process.argv[4];
const OUT = process.argv[5] || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });

  const consoleErrors = [];
  const consoleAll = [];
  const failedRequests = [];
  const ipInfoCalls = [];

  page.on('console', (msg) => {
    const line = `[${msg.type()}] ${msg.text()}`;
    consoleAll.push(line);
    if (msg.type() === 'error' || msg.type() === 'warning') consoleErrors.push(line);
  });
  page.on('pageerror', (err) => consoleErrors.push('pageerror: ' + err.message));
  page.on('response', (res) => {
    const url = res.url();
    if (url.includes('ip-info')) {
      ipInfoCalls.push({ url: url.replace(BASE, ''), status: res.status() });
    }
    if (res.status() >= 400) failedRequests.push(`${res.status()} ${url.replace(BASE, '')}`);
  });

  console.log(`== opening ${BASE}`);
  await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 });
  console.log(`   title: ${await page.title()}`);

  // --- log in ---
  // Two different front-ends live here: "/" renders the active theme (LuminaPlus,
  // where the IP panel is), while "/admin" always renders the built-in default
  // theme. So authenticate against the API directly and then view "/" as a
  // logged-in visitor, instead of driving /admin's separate login UI.
  console.log('== logging in via the API');
  const loginResult = await page.evaluate(async ([base, user, pass]) => {
    const res = await fetch(base + '/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: user, password: pass }),
      credentials: 'include',
    });
    let body = null;
    try { body = await res.json(); } catch { /* non-JSON */ }
    return { status: res.status, body };
  }, [BASE, USER, PASS]);
  console.log(`   /api/login -> ${loginResult.status} ${JSON.stringify(loginResult.body).slice(0, 160)}`);

  await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 });
  await page.waitForTimeout(3000);
  console.log(`   after login url: ${page.url()}`);
  const homeText = await page.locator('body').innerText().catch(() => '');
  console.log(`   logged-in home text: ${homeText.replace(/\n+/g, ' | ').slice(0, 200)}`);
  await page.screenshot({ path: `${OUT}/ip-panel-1-home.png`, fullPage: false });

  // --- open a node detail page ---
  // The IP panel lives on the instance detail view, so click the node's name.
  console.log('== opening a node detail');
  const nodeName = page.getByText(/OC424|MAC Server|HK04/i).first();
  if (await nodeName.count()) {
    await nodeName.click({ timeout: 10000 })
      .catch((e) => console.log('   node click failed: ' + e.message.split('\n')[0]));
    await page.waitForTimeout(3000);
    console.log(`   url: ${page.url()}`);
  } else {
    console.log('   !! no node name found');
  }

  // --- open the IP tab ---
  // The detail view has three chart tabs: 负载 / Ping / IP. The IP panel only
  // renders while its tab is active, though the lookups are fetched eagerly.
  // The segmented control can scroll, so enumerate rather than guess a selector.
  console.log('== selecting the IP tab');
  const tabInfo = await page.evaluate(() => {
    const segs = Array.from(document.querySelectorAll('.instance-segmented'));
    return segs.map((s) => ({
      cls: s.className,
      labels: Array.from(s.querySelectorAll('button, a, [role="button"]')).map((e) => (e.textContent || '').trim()),
      html: s.outerHTML.slice(0, 400),
    }));
  });
  console.log('   segmented groups: ' + JSON.stringify(tabInfo.map((t) => t.labels)));

  const clicked = await page.evaluate(() => {
    const els = Array.from(document.querySelectorAll('button, a, [role="button"]'));
    const hit = els.find((el) => ['IP', 'ip'].includes((el.textContent || '').trim()));
    if (!hit) return false;
    hit.scrollIntoView({ block: 'center' });
    hit.click();
    return true;
  });
  console.log(`   clicked IP tab: ${clicked}`);
  if (!clicked && tabInfo[0]) console.log('   first group html: ' + tabInfo[0].html.replace(/\s+/g, ' ').slice(0, 300));
  // Globalping measurements are slow; give the panel room to settle.
  await page.waitForTimeout(30000);

  // The IP panel sits below the charts, so scroll to it before capturing.
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight)).catch(() => {});
  await page.waitForTimeout(2500);
  await page.screenshot({ path: `${OUT}/ip-panel-2-detail.png`, fullPage: false });

  // Try to locate an element whose text starts the IP panel, and screenshot just it.
  const ipPanel = page.locator('[class*="ip-info-panel"]').first();
  if (await ipPanel.count()) {
    console.log('   found .ip-info-panel element');
    await ipPanel.scrollIntoViewIfNeeded().catch(() => {});
    await page.waitForTimeout(1500);
    await ipPanel.screenshot({ path: `${OUT}/ip-panel-3-closeup.png` }).catch(() => {});
    console.log(`   panel text: ${(await ipPanel.innerText().catch(() => '')).replace(/\n+/g, ' | ').slice(0, 400)}`);
  } else {
    console.log('   no .ip-info-panel element in the DOM');
  }

  const bodyText = await page.locator('body').innerText().catch(() => '');
  const panelPresent = /IP 信息|IP信息|IP Info/.test(bodyText);
  const hasGeo = /地理信息|位置|ASN|归属/.test(bodyText);
  const failureText = (bodyText.match(/[^\n]*(刷新失败|暂无数据|请求失败)[^\n]*/g) || []).slice(0, 4);

  await page.screenshot({ path: `${OUT}/ip-panel-2-detail.png`, fullPage: false });

  console.log('\n===== RESULT =====');
  console.log(`ip-info API calls seen : ${ipInfoCalls.length}`);
  for (const c of ipInfoCalls) console.log(`   ${c.status} ${c.url}`);
  console.log(`panel text present     : ${panelPresent}`);
  console.log(`geo/ASN text present   : ${hasGeo}`);
  console.log(`degraded-text matches  : ${failureText.length ? JSON.stringify(failureText) : 'none'}`);
  console.log(`console errors/warnings: ${consoleErrors.length}`);
  for (const e of consoleErrors.slice(0, 10)) console.log(`   ${e.slice(0, 300)}`);
  console.log(`failed requests        : ${failedRequests.length}`);
  for (const f of failedRequests.slice(0, 8)) console.log(`   ${f}`);

  // Compare the endpoint result for both of the node's address families, exactly
  // as the theme requests them. If one comes back excluded, the theme's
  // `available` set is empty even though both calls were HTTP 200.
  const nodeLookups = await page.evaluate(async (base) => {
    const id = location.pathname.split('/').pop();
    const out = { id, calls: [] };
    for (const ip of ['213.35.99.48', '2603:c024:4522:5139:0:9a53:c2bb:ca43']) {
      const r = await fetch(`${base}/api/public/ip-info/v1/lookup?uuid=${id}&ip=${encodeURIComponent(ip)}`,
        { credentials: 'include' });
      const b = await r.json().catch(() => null);
      out.calls.push({
        ip,
        code: r.status,
        excluded: b && b.data ? b.data.excluded : null,
        reason: b && b.data ? b.data.excluded_reason : null,
        family: b && b.data && b.data.address ? b.data.address.family : null,
      });
    }
    return out;
  }, BASE).catch((e) => ({ error: String(e) }));
  console.log('node lookups: ' + JSON.stringify(nodeLookups));

  // Raw /status as the browser sees it -- compared against what the theme requires.
  const status = await page.evaluate(async (base) => {
    const r = await fetch(base + '/api/public/ip-info/v1/status', { credentials: 'include' });
    return { code: r.status, body: await r.json().catch(() => null) };
  }, BASE).catch((e) => ({ error: String(e) }));
  console.log(`/status from the browser: ${JSON.stringify(status).slice(0, 400)}`);

  console.log(`screenshots            : ${OUT}/ip-panel-1-home.png, ${OUT}/ip-panel-2-detail.png`);

  await browser.close();
})().catch((err) => {
  console.error('FATAL', err);
  process.exit(1);
});
