#!/usr/bin/env node
// 验证两件只能靠看的事：背景到底有没有出来，以及切换 IPv4/IPv6 之后解锁区块有没有变多。
//
// 用法: node verify-theme-fixes.js <base> <user> <pass> <instance-uuid> [outdir]
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];
const uuid = process.argv[5];
const out = process.argv[6] || '/tmp';

const clickVisible = (page, label) => page.evaluate((text) => {
  const visible = (el) => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  const b = Array.from(document.querySelectorAll('button')).filter(visible)
    .find((x) => x.textContent.trim() === text);
  if (b) { b.click(); return true; }
  return false;
}, label);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
  const media = [];
  page.on('response', (r) => {
    const url = r.url();
    if (/\.(mp4|webp|jpe?g|png)(\?|$)/i.test(url) && !url.includes('/assets/Instance')) {
      media.push(`${r.status()} ${url.replace(base, '').slice(0, 80)}`);
    }
  });

  const { login } = require('./pw_login');
  await login(page, base, user, pass);
  await page.goto(`${base}/instance/${uuid}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(9000);

  const background = await page.evaluate(() => {
    const video = document.querySelector('video');
    const info = { hasVideo: !!video, videoSrc: '', videoReady: false, bodyBg: '' };
    if (video) {
      info.videoSrc = video.currentSrc || video.src || '';
      info.videoReady = video.readyState >= 2 && video.videoWidth > 0;
    }
    const bgEl = document.querySelector('[style*="background-image"], .theme-background, [class*="background"]');
    if (bgEl) info.bodyBg = (bgEl.getAttribute('style') || '').slice(0, 140);
    return info;
  });

  // 切到 IP 页签，然后来回切 IPv6 / IPv4 两次，数解锁区块的个数。
  await clickVisible(page, 'IP 信息');
  await page.waitForTimeout(4000);
  const counts = [];
  for (const label of ['IPv6', 'IPv4', 'IPv6', 'IPv4']) {
    await clickVisible(page, label);
    await page.waitForTimeout(3500);
    counts.push(await page.evaluate(() =>
      document.querySelectorAll('.nk-unlock').length));
  }

  await page.screenshot({ path: `${out}/theme-verify.png`, fullPage: false });

  console.log('  背景 video      :', background.hasVideo ? `存在 (readyState>=2: ${background.videoReady})` : '不存在');
  if (background.videoSrc) console.log('  背景 src        :', background.videoSrc.slice(0, 90));
  if (background.bodyBg) console.log('  背景元素 style  :', background.bodyBg);
  console.log('  媒体请求        :', media.length ? media.slice(0, 6).join(' | ') : '(none)');
  console.log('  解锁区块个数    :', JSON.stringify(counts), ' (切 IPv6/IPv4/IPv6/IPv4)');
  console.log('  截图            :', `${out}/theme-verify.png`);

  await browser.close();

  const duplicated = counts.some((n) => n > 1);
  const backgroundOk = background.hasVideo && background.videoReady;
  if (duplicated) console.error('\n  FAIL: 解锁区块出现了重复');
  if (!backgroundOk) console.error('\n  FAIL: 背景视频没有就绪');
  if (duplicated || !backgroundOk) process.exit(1);
  console.log('\n  PASS');
})();
