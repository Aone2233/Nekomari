/**
 * Check the admin panel's "new version" banner.
 *
 * The banner compares the running version against a GitHub release list. Before
 * the fix it queried upstream's repository, so a Nekomari build always looked
 * out of date and the dialog linked to someone else's repo. This logs into the
 * admin panel and reports whether the banner appears, plus which repository the
 * page actually contacted.
 */
const { chromium } = require('playwright');

const BASE = (process.argv[2] || 'https://komari.orderly2233.org').replace(/\/$/, '');
const USER = process.argv[3];
const PASS = process.argv[4];

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });

  const githubCalls = [];
  page.on('request', (req) => {
    const u = req.url();
    if (u.includes('api.github.com')) githubCalls.push(u);
  });

  await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 });
  await page.evaluate(async ([base, user, pass]) => {
    await fetch(base + '/api/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: user, password: pass }), credentials: 'include',
    });
  }, [BASE, USER, PASS]);

  await page.goto(BASE + '/admin', { waitUntil: 'networkidle', timeout: 60000 });
  await page.waitForTimeout(6000);

  const body = await page.locator('body').innerText().catch(() => '');
  const banner = /有新版本|New version|发现新版本/i.test(body);
  const upstream = /1\.5\.0-fix1/.test(body);

  console.log(`admin url            : ${page.url()}`);
  console.log(`github calls made    : ${githubCalls.length}`);
  for (const g of githubCalls) console.log(`   ${g}`);
  console.log(`"new version" banner : ${banner ? 'PRESENT' : 'absent'}`);
  console.log(`mentions 1.5.0-fix1  : ${upstream}`);
  if (banner) {
    const m = body.match(/[^\n]*(有新版本|1\.5\.0-fix1)[^\n]*/g) || [];
    console.log('   banner text: ' + JSON.stringify(m.slice(0, 4)));
  }
  await page.screenshot({ path: '/tmp/admin-version.png' });
  await browser.close();
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
