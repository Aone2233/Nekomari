#!/usr/bin/env node
// Regression check: the theme's "IP 信息" tab must exist and render its panel.
//
// Why this is a browser check and not an API check
// -----------------------------------------------
// The tab was missing for four rounds of investigation while every endpoint answered
// 200 with correct-looking data. The theme parses each response with a strict zod
// schema and renders the tab only when at least one address survives it, so a single
// missing field (`classification.source`) hid the whole panel without producing any
// error a server-side check could see. The only honest test is the one a user performs:
// open the page and look.
//
// Companion: deploy/theme-contract-check.mjs checks the same contract from the API side
// and names the offending field. This one proves the UI consequence.
//
// Usage:
//   node ip_panel_probe.js <base> <user> <pass> <instance-uuid> [outdir]
//
// Needs an instance whose region is not CN: the theme deliberately hides the tab for
// mainland-China nodes, so those will report a missing tab even when everything works.
// Set NEKOMARI_2FA_SECRET when the account has 2FA enabled.
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];
const out = process.argv[6] || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const calls = [];
  page.on('response', async (r) => {
    if (r.url().includes('/ip-info/')) {
      calls.push(`${r.status()} ${r.url().replace(base, '')}`);
    }
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);

  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(6000);

  const tabs = await page.evaluate(() =>
    Array.from(document.querySelectorAll('button')).map((b) => b.textContent.trim()));
  const hasTab = tabs.some((t) => t.includes('IP'));

  // The theme hides the tab for mainland-China nodes on purpose, so a missing tab there
  // is correct behaviour rather than a regression. Read the node's region to tell the
  // two apart -- otherwise this check cries wolf on exactly the nodes it should ignore.
  const region = await page.evaluate(async (id) => {
    try {
      const r = await fetch('/api/rpc2', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'common:getNodes', params: {} }),
      });
      const body = await r.json();
      const nodes = body.result || {};
      const node = Array.isArray(nodes) ? nodes.find((n) => n.uuid === id) : nodes[id];
      return (node && node.region) || '';
    } catch (_) { return ''; }
  }, uuid);
  const isCN = /🇨🇳/.test(region);

  // The panel only mounts once the tab is selected, so "panel: false" on the default
  // 负载 tab is expected and says nothing. Click it before judging.
  let panel = null;
  let content = '';
  let unlock = null;
  if (hasTab) {
    await page.evaluate(() => {
      const button = Array.from(document.querySelectorAll('button'))
        .find((b) => b.textContent.trim().includes('IP'));
      if (button) button.click();
    });
    await page.waitForTimeout(4000);
    panel = await page.evaluate(() => !!document.querySelector('.ip-info-panel'));
    content = await page.evaluate(() => {
      const el = document.querySelector('.ip-info-panel');
      return el ? el.innerText.replace(/\s+/g, ' ').slice(0, 300) : '';
    });
    // 解锁区块是附加脚本挂上去的（主题自己没有渲染它的代码），所以单独断言一次：
    // 它没出现时要能分清是「主题结构变了」还是「探针还没上报」。
    unlock = await page.evaluate(() => {
      const el = document.querySelector('.nk-unlock');
      return el ? el.innerText.replace(/\s+/g, ' ').trim().slice(0, 400) : null;
    });
    await page.screenshot({ path: `${out}/ip-panel.png`, fullPage: false });
  }

  console.log('  instance      :', uuid);
  console.log('  region        :', region || '(unknown)');
  console.log('  tabs          :', JSON.stringify(tabs.filter((t) => t && t.length < 12)));
  console.log('  "IP 信息" tab :', hasTab ? 'present' : 'MISSING');
  console.log('  panel renders :', panel === null ? 'n/a' : String(panel));
  if (content) console.log('  panel text    :', content);
  console.log('  unlock block  :', unlock === null ? 'ABSENT' : unlock);
  console.log('  ip-info calls :', calls.length);
  for (const c of calls) console.log('    ' + c);
  if (panel) console.log('  screenshot    :', `${out}/ip-panel.png`);

  await browser.close();

  if (!hasTab && isCN) {
    console.log('\n  SKIP: this node is in mainland China, where the theme hides the tab by design.');
    process.exit(0);
  }
  if (!hasTab || !panel) {
    console.error('\n  FAIL: the IP 信息 tab is not rendering.');
    console.error('  Run deploy/theme-contract-check.mjs to see which field the theme rejects.');
    process.exit(1);
  }
  console.log('\n  PASS');
})();
