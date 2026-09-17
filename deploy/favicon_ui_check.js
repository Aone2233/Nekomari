/**
 * Upload a favicon through the real admin UI and watch what happens.
 *
 * The panel previews /favicon.ico in an <img>. A server-side fix alone does not
 * prove the user-visible behaviour changed, so this drives the actual page:
 * log in, open 站点 settings, upload a distinctive PNG, and report the response,
 * the preview image's natural size, and whether the bytes changed.
 */
const { chromium } = require('playwright');
const fs = require('fs');

const BASE = (process.argv[2] || 'https://komari.orderly2233.org').replace(/\/$/, '');
const USER = process.argv[3];
const PASS = process.argv[4];
const OUT = process.argv[5] || '/tmp';

// A 32x32 solid magenta PNG, so a successful update is unmistakable.
function magentaPng() {
  const zlib = require('zlib');
  const w = 32, h = 32;
  const raw = Buffer.concat(Array.from({ length: h }, () =>
    Buffer.concat([Buffer.from([0]), Buffer.concat(Array.from({ length: w }, () => Buffer.from([255, 0, 255])))])));
  const chunk = (type, data) => {
    const len = Buffer.alloc(4); len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const crc = Buffer.alloc(4); crc.writeUInt32BE(zlib.crc32(body) >>> 0);
    return Buffer.concat([len, body, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; ihdr[9] = 2;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr), chunk('IDAT', zlib.deflateSync(raw)), chunk('IEND', Buffer.alloc(0)),
  ]);
}

(async () => {
  const pngPath = `${OUT}/favicon-magenta.png`;
  fs.writeFileSync(pngPath, magentaPng());

  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });

  const faviconResponses = [];
  page.on('response', (r) => {
    if (r.url().includes('favicon')) faviconResponses.push(`${r.status()} ${r.url()} ct=${r.headers()['content-type']}`);
  });

  await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 });
  await page.evaluate(async ([b, u, p]) => {
    await fetch(b + '/api/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: u, password: p }), credentials: 'include',
    });
  }, [BASE, USER, PASS]);

  await page.goto(BASE + '/admin/settings/site', { waitUntil: 'networkidle', timeout: 60000 });
  await page.waitForTimeout(3000);
  console.log('at: ' + page.url());

  // The 更新 Favicon button opens a hidden file input; arm the chooser first.
  const before = await page.evaluate(async () => {
    const r = await fetch('/favicon.ico?probe=' + Date.now(), { cache: 'no-store' });
    const b = await r.arrayBuffer();
    return { status: r.status, type: r.headers.get('content-type'), len: b.byteLength,
             head: Array.from(new Uint8Array(b.slice(0, 8))) };
  });
  console.log('BEFORE: ' + JSON.stringify(before));

  const chooserPromise = page.waitForEvent('filechooser', { timeout: 20000 }).catch(() => null);
  // The label is rendered lowercase in this build ("update favicon"), so match
  // case-insensitively rather than assuming the locale's capitalisation.
  const btn = page.getByRole('button', { name: /update\s*favicon/i }).first();
  if (await btn.count()) {
    await btn.click();
    console.log('clicked the update button');
  } else {
    console.log('!! update button not found');
  }
  const chooser = await chooserPromise;
  if (chooser) {
    await chooser.setFiles(pngPath);
    console.log('file set');
  } else {
    console.log('!! no file chooser appeared');
  }
  await page.waitForTimeout(6000);

  const after = await page.evaluate(async () => {
    const r = await fetch('/favicon.ico?probe=' + Date.now(), { cache: 'no-store' });
    const b = await r.arrayBuffer();
    return { status: r.status, type: r.headers.get('content-type'), len: b.byteLength,
             head: Array.from(new Uint8Array(b.slice(0, 8))) };
  });
  console.log('AFTER : ' + JSON.stringify(after));

  // What the preview <img> actually rendered.
  const img = await page.evaluate(() => {
    const el = document.querySelector('img[src*="favicon"]');
    if (!el) return null;
    return { src: el.getAttribute('src'), naturalWidth: el.naturalWidth, naturalHeight: el.naturalHeight,
             complete: el.complete };
  });
  console.log('preview <img>: ' + JSON.stringify(img));

  console.log('favicon responses seen: ' + faviconResponses.length);
  for (const f of faviconResponses.slice(-6)) console.log('   ' + f);

  const changed = JSON.stringify(before.head) !== JSON.stringify(after.head) || before.len !== after.len;
  console.log('bytes changed: ' + changed);
  await page.screenshot({ path: `${OUT}/favicon-after.png` });
  await browser.close();
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
