const { chromium } = require('playwright');
const fs = require('fs');
const base='https://komari.orderly2233.org', user='AONE2233', pass='KOMT0721@aone2233';
const uuid='d67e6b38-7b98-4b18-900e-85f7c820f81e';
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const seen = [];
  page.on('response', async (r) => {
    if (!r.url().includes('/api/rpc2')) return;
    let b=null; try { b=await r.json(); } catch(_) { return; }
    if (b && b.result && Array.isArray(b.result.series)) seen.push(b.result);
  });
  const { login } = require('./pw_login');
  await login(page, base, user, pass);
  await page.goto(base+'/instance/'+uuid, {waitUntil:'domcontentloaded'});
  await page.waitForTimeout(5000);
  await page.evaluate(()=>{const b=[...document.querySelectorAll('button')].find(x=>x.textContent.trim()==='Ping'); if(b)b.click();});
  await page.waitForTimeout(12000);
  fs.writeFileSync('dump-series.json', JSON.stringify(seen, null, 1));
  for (const res of seen) {
    console.log('=== count=%s default_points=%s server_downsample_default=%s series=%d start=%s end=%s ===',
      res.count, res.default_points, res.server_downsample_default, res.series.length, res.start, res.end);
    const s0 = res.series[0];
    console.log('  series[0] keys:', Object.keys(s0));
    console.log('  series[0]:', JSON.stringify(s0).slice(0, 700));
  }
  await browser.close();
})();
