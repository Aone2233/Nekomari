#!/usr/bin/env node
// Does an authenticated WebSocket upgrade to /api/rpc2 actually succeed?
//
// Established so far: the route exists (GET upgrades), an unauthenticated GET gets
// 401 from the private-site middleware, and an authenticated non-upgrade GET gets
// 400 "require websocket upgrade" -- so the handler is reachable. The browser's
// upgrade fails with 401, which means it arrives without a usable session.
//
// This opens the socket from inside the page, after a real login, and reports the
// close code and reason, which distinguishes "server rejected" from "cookie not
// sent" from "blocked before reaching the server".
//
// Usage: node ws_upgrade_probe.js <base> <user> <pass>
const { chromium } = require('playwright');

const base = (process.argv[2] || '').replace(/\/$/, '');
const user = process.argv[3];
const pass = process.argv[4];

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();

  await page.goto(base + '/admin', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2000);

  // Real form login.
  try {
    await page.locator('input[type="text"], input[name="username"]').first().fill(user, { timeout: 8000 });
    await page.locator('input[type="password"]').first().fill(pass, { timeout: 8000 });
    await page.keyboard.press('Enter');
    await page.waitForTimeout(4000);
  } catch (e) {
    console.log('  form login failed: ' + e.message.split('\n')[0]);
  }

  const cookies = await ctx.cookies();
  const session = cookies.find((c) => c.name === 'session_token');
  console.log('  session cookie present:', !!session,
              session ? `(len ${session.value.length}, sameSite=${session.sameSite}, secure=${session.secure})` : '');

  // Confirm the session actually works over REST before blaming the socket.
  const rest = await page.evaluate(async (b) => {
    const r = await fetch(b + '/api/rpc2', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', method: 'common:getVersion', params: {}, id: 1 }),
    });
    return { status: r.status, body: (await r.text()).slice(0, 120) };
  }, base);
  console.log('  authenticated POST /api/rpc2:', rest.status, rest.body.replace(/\s+/g, ' '));

  // Now the socket, from the page, same origin, cookies included by the browser.
  const ws = await page.evaluate(async (b) => {
    return await new Promise((resolve) => {
      const url = b.replace(/^http/, 'ws') + '/api/rpc2';
      let settled = false;
      const done = (o) => { if (!settled) { settled = true; resolve(o); } };
      let sock;
      try { sock = new WebSocket(url); } catch (e) { return done({ ok: false, threw: String(e) }); }
      const timer = setTimeout(() => done({ ok: false, timeout: true, state: sock.readyState }), 12000);
      sock.onopen = () => {
        clearTimeout(timer);
        sock.send(JSON.stringify({ jsonrpc: '2.0', method: 'common:getVersion', params: {}, id: 1 }));
      };
      sock.onmessage = (m) => {
        clearTimeout(timer);
        done({ ok: true, reply: String(m.data).slice(0, 140) });
        try { sock.close(); } catch (_) {}
      };
      sock.onerror = () => { /* close carries the detail */ };
      sock.onclose = (e) => {
        clearTimeout(timer);
        done({ ok: false, code: e.code, reason: e.reason, clean: e.wasClean });
      };
    });
  }, base);

  console.log('  WebSocket result:', JSON.stringify(ws));
  await browser.close();
})();
