import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const root = fileURLToPath(new URL('../src/', import.meta.url));
function load(relative, environment = {}, cache = new Map()) {
  const file = path.resolve(root, relative);
  if (cache.has(file)) return cache.get(file);
  const exports = {};
  cache.set(file, exports);
  const compiled = ts.transpileModule(readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true },
  }).outputText;
  vm.runInNewContext(compiled, {
    exports, Response, ...environment,
    require(specifier) {
      if (specifier === 'react') return environment.React;
      if (specifier === 'sonner') return { toast: environment.toast };
      if (specifier === 'i18next') return { default: { exists: () => false } };
      const target = specifier.startsWith('@/') ? specifier.slice(2) + '.ts'
        : path.relative(root, path.resolve(path.dirname(file), specifier + '.ts'));
      return load(target, environment, cache);
    },
  }, { filename: file });
  return exports;
}
const { requireApiSuccess, assertApiSuccess } = load('lib/apiResponse.ts');
const response = (body, status = 200) => new Response(typeof body === 'string' ? body : JSON.stringify(body), { status });

test('REST success requires both HTTP success and an explicit success object', async () => {
  for (const status of [200, 201, 202]) {
    const body = { status: 'success', message: '', data: { saved: true } };
    const res = response(body, status);
    const actual = await requireApiSuccess(res);
    assert.equal(actual.data.saved, true);
    assert.equal(res.bodyUsed, true);
  }
  const flat = await requireApiSuccess(response({ status: 'success', token: 'one-time', uuid: 'owned' }));
  assert.equal(flat.token, 'one-time');
  assert.equal(flat.uuid, 'owned');
  assert.equal((await requireApiSuccess(response({ status: 'success' }))).data, undefined);
});

test('HTTP failures never succeed even when the body claims success', async () => {
  for (const status of [400, 401, 403, 409, 429, 500, 502]) {
    await assert.rejects(requireApiSuccess(response({ status: 'success' }, status)), new RegExp(`HTTP ${status}`));
    await assert.rejects(requireApiSuccess(response({ status: 'success', message: 'refused by storage' }, status)), /refused by storage/);
  }
});

test('business errors and malformed or unrelated HTTP 200 bodies fail closed', async () => {
  for (const body of [null, [], ['success'], true, 1, 'null', '"success"', {},
    { status: 'error' }, { status: 'pending' }, { status: true }, { status: 'Success' },
    { success: true }, { code: 200 }, { data: { saved: true } },
    { status: 'success', message: {} }, { status: 'success', message: null },
    '<html>sign in</html>', '', '{']) {
    await assert.rejects(requireApiSuccess(response(body)), /Invalid API response/);
  }
  await assert.rejects(requireApiSuccess(new Response(null, { status: 204 })), /Invalid API response/);
  await assert.rejects(requireApiSuccess(response({ status: 'error', message: 'permission denied' })), /permission denied/);
});

test('only nonempty string error messages override safe fallback text', async () => {
  for (const message of [null, undefined, false, 123, {}, [], '', '  \n ']) {
    await assert.rejects(requireApiSuccess(response({ status: 'error', message }), 'Invalid saved result'), /Invalid saved result/);
    await assert.rejects(requireApiSuccess(response({ status: 'error', message }, 503)), /HTTP 503/);
  }
  await assert.rejects(requireApiSuccess(response({ status: 'error', message: '<script>literal refusal</script>' })), /literal refusal/);
});

test('XHR assertion shares the contract and neither helper retries a mutation', async () => {
  const body = { status: 'success', data: { received: true } };
  assert.equal(assertApiSuccess({ ok: true, status: 200 }, body), body);
  assert.throws(() => assertApiSuccess({ ok: false, status: 502 }, body), /HTTP 502/);
  let reads = 0;
  await assert.rejects(requireApiSuccess({ ok: true, status: 200, json: async () => {
    reads++;
    throw new Error('body interrupted');
  } }), /Invalid API response/);
  assert.equal(reads, 1);
});
