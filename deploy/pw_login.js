// Shared panel login for the Playwright probes, including TOTP 2FA.
//
// The same thing that broke the Python scripts broke these: once 2FA is enabled, a
// password-only login returns 401 and the page renders empty, which looks like a
// broken probe rather than a rejected login. Every browser probe goes through here
// so that fix lives in one place.
//
// Usage:
//   const { login } = require('./pw_login');
//   await login(page, base, user, pass);            // secret from env if set
//
// Set NEKOMARI_2FA_SECRET to the base32 secret from enrolment. Without it the
// helper still works on an account with 2FA disabled, and fails with a clear
// message when the account requires a code.
const crypto = require('crypto');

const B32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

function base32Decode(input) {
  const clean = String(input).toUpperCase().replace(/[^A-Z2-7]/g, '');
  let bits = 0;
  let value = 0;
  const out = [];
  for (const ch of clean) {
    const idx = B32.indexOf(ch);
    if (idx === -1) continue;
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

// RFC 6238, matching the server's pquerna/otp defaults: SHA-1, 6 digits, 30s.
function totp(secret, at = Date.now()) {
  const key = base32Decode(secret);
  const counter = Math.floor(at / 1000 / 30);
  const buf = Buffer.alloc(8);
  buf.writeUInt32BE(Math.floor(counter / 0x100000000), 0);
  buf.writeUInt32BE(counter >>> 0, 4);
  const hmac = crypto.createHmac('sha1', key).update(buf).digest();
  const offset = hmac[hmac.length - 1] & 0x0f;
  const code = ((hmac[offset] & 0x7f) << 24) |
               ((hmac[offset + 1] & 0xff) << 16) |
               ((hmac[offset + 2] & 0xff) << 8) |
               (hmac[offset + 3] & 0xff);
  return String(code % 1000000).padStart(6, '0');
}

async function login(page, base, user, pass, opts = {}) {
  const secret = opts.secret || process.env.NEKOMARI_2FA_SECRET || '';
  const root = (base || '').replace(/\/$/, '');
  const url = root + (opts.path || '/admin');
  await page.goto(url, { waitUntil: 'domcontentloaded' });

  // Log in over the API rather than by driving the form.
  //
  // The form is the obvious approach and it is what an earlier version did, but it
  // is fragile: with private_site on, /admin renders a restricted-login dialog whose
  // inputs are intermittently not actionable, and driving it produced timeouts that
  // looked like the feature under test being broken. A POST carries the same
  // credentials and, unlike the form, can carry a fresh TOTP code computed at the
  // moment of submission.
  if (!secret) {
    // Without a secret the account may still have 2FA disabled; try anyway and let
    // the check below produce the error message.
  }
  const attempt = await page.evaluate(async ([b, u, p, code]) => {
    const body = { username: u, password: p };
    if (code) body['2fa_code'] = code;
    try {
      const r = await fetch(b + '/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      return { status: r.status, body: await r.json().catch(() => null) };
    } catch (e) {
      return { status: -1, body: String(e) };
    }
  }, [root, user, pass, secret ? totp(secret) : '']);

  if (attempt.status !== 200) {
    const msg = attempt.body && attempt.body.message ? attempt.body.message : JSON.stringify(attempt.body);
    const hint = /2FA/i.test(String(msg))
      ? '  <- export NEKOMARI_2FA_SECRET with the base32 secret from enrolment'
      : '';
    throw new Error(`login failed (${attempt.status}): ${msg}${hint}`);
  }

  // Confirm the session actually works before the caller blames the page.
  //
  // Check logged_in, not the status code: /api/me answers 200 for a guest too, with
  // {"username":"Guest","logged_in":false}. Treating 200 as success made a failed
  // login look like a successful one, and the resulting empty page was then blamed
  // on the feature under test.
  const me = await page.evaluate(async (b) => {
    try {
      const r = await fetch(b + '/api/me', { headers: { Accept: 'application/json' } });
      return { status: r.status, body: await r.json().catch(() => null) };
    } catch (_) { return { status: -1, body: null }; }
  }, root);

  const loggedIn = me.status === 200 && me.body && me.body.logged_in === true;
  if (!loggedIn) {
    throw new Error(
      `login did not establish a session (/api/me -> ${me.status}, ` +
      `logged_in=${me.body ? me.body.logged_in : 'n/a'}). ` +
      'If 2FA is enabled, export NEKOMARI_2FA_SECRET with the base32 secret.');
  }
  return true;
}
module.exports = { login, totp };
