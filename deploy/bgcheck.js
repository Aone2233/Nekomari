const { chromium } = require('playwright');
const base='https://komari.orderly2233.org', user='AONE2233', pass='KOMT0721@aone2233';
const out = process.env.TEMP;
(async () => {
  const browser = await chromium.launch();
  const results = [];
  for (const scheme of ['dark','light']) {
    const page = await browser.newPage({ viewport:{width:1440,height:900}, colorScheme: scheme });
    const bg = [];
    page.on('response', r => { const u=r.url(); if (u.includes('bg-desktop')||u.includes('bg-mobile')) bg.push(r.status()+' '+u.split('/').pop()); });
    const { login } = require('./pw_login');
    await login(page, base, user, pass);
    await page.goto(base+'/', { waitUntil:'domcontentloaded' });
    await page.waitForTimeout(7000);
    const info = await page.evaluate(() => {
      const found = [];
      for (const el of document.querySelectorAll('*')) {
        const s = getComputedStyle(el).backgroundImage;
        if (s && s.includes('/assets/bg-')) found.push(s.slice(0,120));
      }
      return { found: [...new Set(found)].slice(0,3), appearance: document.documentElement.dataset.appearance };
    });
    await page.screenshot({ path: `${out}/bg-${scheme}.png` });
    results.push({ scheme, appearance: info.appearance, found: info.found, requests: bg });
    await page.close();
  }
  for (const r of results) {
    console.log(`  [${r.scheme}] appearance=${r.appearance}`);
    console.log(`     背景元素: ${r.found.length ? r.found.join(' | ') : '(未找到引用本地背景的元素)'}`);
    console.log(`     请求    : ${r.requests.length ? r.requests.join(' , ') : '(none)'}`);
  }
  await browser.close();
  const ok = results.every(r => r.found.length > 0);
  console.log(ok ? '\n  PASS' : '\n  FAIL');
  process.exit(ok ? 0 : 1);
})();
