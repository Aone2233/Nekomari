import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';
import i18next from 'i18next';

const resources = Object.fromEntries(['en', 'zh_CN', 'zh_TW', 'ja_JP', 'id_ID'].map((language) =>
  [language, { translation: JSON.parse(readFileSync(new URL(`../src/i18n/locales/${language}.json`, import.meta.url), 'utf8')) }]));
const i18n = i18next.createInstance();
await i18n.init({ lng: 'en', fallbackLng: 'en', resources });
let answer;
let requests = 0;
const context = vm.createContext({
  Error, Response, AbortController, AbortSignal, DOMException, setTimeout, clearTimeout, console,
  fetch: async () => { requests++; return answer; },
});
const states = { DISCONNECTED: 'disconnected', CONNECTED: 'connected' };
const cache = new Map();
function load(relative) {
  if (cache.has(relative)) return cache.get(relative);
  const exports = {};
  cache.set(relative, exports);
  const compiled = ts.transpileModule(readFileSync(new URL(`../src/lib/${relative}.ts`, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  vm.runInContext(`(function(exports, require) { ${compiled}\n})`, context)(exports, (name) => {
    if (name === '../i18n/config' || name === 'i18next') return { default: i18n };
    if (name === '../types/rpc2') return { RPC2ConnectionState: states };
    if (name === './serverError') return load('serverError');
    throw new Error(`Unexpected dependency: ${name}`);
  });
  return exports;
}
const helper = load('serverError');
const localizeServerError = (payload, fallback) => helper.localizeServerError(payload, fallback, i18n);
const { assertApiSuccess, requireApiSuccess } = load('apiResponse');
const json = (body, status = 200) => new Response(JSON.stringify(body), { status });

test('every explicit handler key has distinct nonempty text in all five locales', () => {
  const files = ['admin.client.go', 'admin.apikey.go', 'bridge.go'];
  const used = new Set();
  for (const file of files) {
    const source = readFileSync(new URL(`../../web/rpc/jsonrpc/${file}`, import.meta.url), 'utf8');
    for (const match of source.matchAll(/rpc\.MakeLocalizedError\(rpc\.\w+,[^\n]*, "([a-z0-9_]+)"\)/g)) used.add(match[1]);
  }
  assert.equal(used.size, 12);
  const english = resources.en.translation.server_errors;
  assert.deepEqual(Object.keys(english).sort(), [...used].sort());
  for (const [language, resource] of Object.entries(resources)) {
    const messages = resource.translation.server_errors;
    assert.deepEqual(Object.keys(messages).sort(), [...used].sort(), language);
    for (const key of used) {
      assert.equal(typeof messages[key], 'string');
      assert.ok(messages[key].trim().length > 10, `${language}/${key}`);
      assert.doesNotMatch(messages[key], /<[^>]*>|\{\{|\$t\(/, `${language}/${key}`);
      if (language !== 'en') assert.notEqual(messages[key], english[key], `${language}/${key}`);
    }
  }
});

test('REST and JSON-RPC metadata use the current language, never raw English matching', async () => {
  for (const language of Object.keys(resources)) {
    await i18n.changeLanguage(language);
    for (const [key, message] of Object.entries(resources[language].translation.server_errors)) {
      assert.equal(localizeServerError({ error_key: key, message: 'diagnostic' }, 'fallback'), message);
      assert.equal(localizeServerError({ data: { error_key: key }, code: -32602 }, 'fallback'), message);
    }
  }
  await i18n.changeLanguage('zh_CN');
  assert.equal(localizeServerError({ message: 'Invalid or missing UUID' }, 'unchanged'), 'unchanged');
  await i18n.changeLanguage('en');
});
test('unknown/malformed metadata preserves the caller fallback without dynamic paths or interpolation', () => {
  for (const key of [undefined, null, true, 17, {}, [], '', 'absent_key', '__proto__', 'constructor',
    '../common.error', 'common.error', 'key:namespace', '<b>unsafe</b>', 'key\n', 'a'.repeat(65)]) {
    for (const payload of [{ error_key: key }, { data: { error_key: key } }]) {
      assert.equal(localizeServerError(payload, '<b>literal fallback</b>'), '<b>literal fallback</b>');
    }
  }
  for (const payload of [null, false, 'invalid_params', [], { data: [] }, { data: 'invalid_params' }]) {
    assert.equal(localizeServerError(payload, 'fallback'), 'fallback');
  }
  // A malformed explicit REST key cannot fall through to a conflicting nested key.
  assert.equal(localizeServerError({ error_key: {}, data: { error_key: 'invalid_params' } }, 'fallback'), 'fallback');
});

test('missing, empty or non-string translations preserve the original diagnostic', () => {
  const payload = { error_key: 'invalid_params' };
  assert.equal(helper.localizeServerError(payload, 'diagnostic', {
    exists: () => false, t: () => assert.fail('missing translation was resolved'),
  }), 'diagnostic');
  for (const message of [undefined, null, {}, [], 17, '', '   ', 'server_errors.invalid_params']) {
    assert.equal(helper.localizeServerError(payload, 'diagnostic', {
      exists: () => true, t: () => message,
    }), 'diagnostic');
  }
});

test('REST translation never acknowledges a failure or changes a successful result', async () => {
  await i18n.changeLanguage('zh_CN');
  const message = resources.zh_CN.translation.server_errors.api_key_id_invalid;
  for (const status of [200, 400, 401, 403, 409, 500]) {
    await assert.rejects(requireApiSuccess(json({ status: 'error', message: 'Invalid or missing UUID', error_key: 'api_key_id_invalid' }, status)), { message });
  }
  assert.throws(() => assertApiSuccess({ ok: false, status: 500 }, { status: 'success', error_key: 'api_key_id_invalid' }), { message });
  const body = { status: 'success', error_key: 'api_key_id_invalid', token: 'not logged' };
  assert.equal(assertApiSuccess({ ok: true, status: 200 }, body), body);
  await assert.rejects(requireApiSuccess(json({ status: 'error', message: 'Custom plugin diagnostic', error_key: 'future_key' })), { message: 'Custom plugin diagnostic' });
  await assert.rejects(requireApiSuccess(json({ status: 'error', error_key: 'future_key' }, 503)), { message: 'HTTP 503' });
  await i18n.changeLanguage('en');
});
