import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

/**
 * The SSO bind/unbind requests, checked on the real module rather than on the rendered page.
 *
 * `/api/admin/oauth2/bind` and `/unbind` sit behind `api.RequireSensitive2FA()` (web/router/
 * router.go), and this page answered both with a bare request — which is a 401 as soon as the
 * account has a factor. The URL a click produces is the whole contract, and it is invisible in a
 * rendered page (the bind one leaves the SPA entirely), so it is asserted on the module's output:
 * a code present when there is a factor to verify, and *nothing* added when there is not.
 *
 * The last point is the one that decides whether this is usable: the server lets an account with
 * no factor through (`VerifySensitive2FACore`), so appending a parameter unconditionally would
 * put a meaningless value on every bind and unbind of the majority of accounts.
 *
 * The module is transpiled and imported rather than string-matched: the assertion is about what
 * the functions return, not about how they are written.
 */
const compiled = ts.transpileModule(
  readFileSync(new URL('../src/lib/oauth2Binding.ts', import.meta.url), 'utf8'),
  { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } },
).outputText;

const module = await import(
  `data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`
);

const { oauth2BindUrl, oauth2UnbindUrl } = module;

test('an account without a factor gets exactly the requests that worked before the gate', () => {
  for (const code of [undefined, null, '', '   ']) {
    assert.equal(
      oauth2BindUrl(code),
      '/api/admin/oauth2/bind',
      `bind with ${JSON.stringify(code)} must stay the bare URL`,
    );
    assert.equal(
      oauth2UnbindUrl(code),
      '/api/admin/oauth2/unbind',
      `unbind with ${JSON.stringify(code)} must stay the bare URL`,
    );
  }
});

test('a collected code rides the query string on both endpoints', () => {
  // The GET bind is a top-level redirect to the provider, so the query string is the only place
  // the code can travel; unbind carries it the same way /2fa/disable does.
  assert.equal(
    oauth2BindUrl('123456'),
    '/api/admin/oauth2/bind?2fa_code=123456',
  );
  assert.equal(
    oauth2UnbindUrl('123456'),
    '/api/admin/oauth2/unbind?2fa_code=123456',
  );
});

test('the code is percent-encoded and trimmed, and cannot inject a second parameter', () => {
  assert.equal(oauth2BindUrl('  123456  '), '/api/admin/oauth2/bind?2fa_code=123456');
  assert.equal(oauth2UnbindUrl('12 34'), '/api/admin/oauth2/unbind?2fa_code=12%2034');
  // A pasted value like `1&admin=1` must stay a value, not become a parameter of its own.
  assert.equal(
    oauth2BindUrl('1&admin=1'),
    '/api/admin/oauth2/bind?2fa_code=1%26admin%3D1',
  );
});

test('the page uses those builders and keeps the code behind the 2fa gate', () => {
  // The regression this guards is the page going back to a bare request, or collecting a code
  // from an account that has no factor to verify.
  const page = readFileSync(new URL('../src/pages/admin/account.tsx', import.meta.url), 'utf8');
  assert.ok(page.includes('oauth2BindUrl('), 'the bind navigation must come from the builder');
  assert.ok(page.includes('oauth2UnbindUrl('), 'the unbind POST must come from the builder');
  assert.ok(
    !page.includes('"/api/admin/oauth2/bind"') && !page.includes('"/api/admin/oauth2/unbind"'),
    'the endpoint literals belong to the module, so a bare request cannot reappear unnoticed',
  );
  assert.match(
    page,
    /Boolean\(account\?\.\["2fa_enabled"\]\)/,
    'the gate has to read the same flag the 2FA buttons use',
  );
  assert.match(
    page,
    /ssoNeedsCode && !ssoCode/,
    'with a factor enabled an empty code must be refused before the request',
  );
  assert.ok(
    page.includes('t("account.otp_empty_error")'),
    'and refused with the same prompt key the 2FA buttons use',
  );
});
