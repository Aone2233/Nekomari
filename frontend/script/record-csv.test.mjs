import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const source = readFileSync(new URL('../src/utils/recordCsv.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const module = { exports: {} };
vm.runInNewContext(code, { module, exports: module.exports, URLSearchParams });
const { recordCsvUrl } = module.exports;

test('custom export sends the exact selected start and end rather than a relative duration', () => {
  const url = new URL(recordCsvUrl('load', 'node/a', {
    start: '2026-09-01T08:15:00.000Z', end: '2026-09-02T09:30:00.000Z',
  }), 'https://panel.invalid');
  assert.equal(url.pathname, '/api/records/load.csv');
  assert.equal(url.searchParams.get('uuid'), 'node/a');
  assert.equal(url.searchParams.get('start'), '2026-09-01T08:15:00.000Z');
  assert.equal(url.searchParams.get('end'), '2026-09-02T09:30:00.000Z');
  assert.equal(url.searchParams.has('hours'), false);
});

test('relative windows are whole hours and bounded', () => {
  const params = (hours) => new URL(recordCsvUrl('ping', 'node', { hours }), 'https://panel.invalid').searchParams;
  assert.equal(params(0.1).get('hours'), '1');
  assert.equal(params(12).get('hours'), '12');
  assert.equal(params(9000).get('hours'), '8784');
});

test('history download reports server errors without navigating away', async () => {
  let clicked = false;
  const context = {
    module: { exports: {} }, URLSearchParams, AbortController, Blob, URL,
    window: { setTimeout, clearTimeout },
    document: { createElement: () => ({ click: () => { clicked = true; } }) },
    fetch: async () => new Response('{"status":"error"}', { status: 413 }),
  };
  context.exports = context.module.exports;
  vm.runInNewContext(code, context);
  await assert.rejects(context.module.exports.downloadRecordCsv('/csv', 'test.csv'), /csv_too_large/);
  assert.equal(clicked, false);
});

test('history download saves only a bounded successful CSV response', async () => {
  let filename = '';
  let clicked = false;
  const context = {
    module: { exports: {} }, URLSearchParams, AbortController, Blob,
    URL: { createObjectURL: () => 'blob:test', revokeObjectURL: () => {} },
    window: { setTimeout, clearTimeout },
    document: {
      body: { appendChild: () => {} },
      createElement: () => ({
        set download(value) { filename = value; },
        click: () => { clicked = true; }, remove: () => {},
      }),
    },
    fetch: async () => new Response('time,value\r\n', { headers: { 'content-type': 'text/csv' } }),
  };
  context.exports = context.module.exports;
  vm.runInNewContext(code, context);
  await context.module.exports.downloadRecordCsv('/csv', 'test.csv');
  assert.equal(filename, 'test.csv');
  assert.equal(clicked, true);
});

test('history download aborts and cancels an oversized streaming response', async () => {
  let cancelled = false;
  let calls = 0;
  const context = {
    module: { exports: {} }, URLSearchParams, AbortController, Blob, URL,
    window: { setTimeout, clearTimeout },
    fetch: async () => ({
      status: 200, ok: true, headers: { get: () => 'text/csv' },
      body: { getReader: () => ({
        read: async () => ({ done: false, value: { byteLength: ++calls === 1 ? 32 * 1024 * 1024 : 1 } }),
        cancel: async () => { cancelled = true; },
      }) },
    }),
  };
  context.exports = context.module.exports;
  vm.runInNewContext(code, context);
  await assert.rejects(context.module.exports.downloadRecordCsv('/csv', 'test.csv'), /csv_too_large/);
  assert.equal(calls, 2);
  assert.equal(cancelled, true);
});

test('history download rejects non-CSV success responses', async () => {
  const context = {
    module: { exports: {} }, URLSearchParams, AbortController, Blob, URL,
    window: { setTimeout, clearTimeout },
    fetch: async () => new Response('<html>login</html>', { headers: { 'content-type': 'text/html' } }),
  };
  context.exports = context.module.exports;
  vm.runInNewContext(code, context);
  await assert.rejects(context.module.exports.downloadRecordCsv('/csv', 'test.csv'), /csv_download_failed/);
});
