import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('../src/utils/RecordHelper.tsx', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const exports = {};
vm.runInNewContext(compiled, { module: { exports }, exports, console });

test('recent and normalized records preserve uptime quality without inventing legacy zeros', () => {
  const base = { updated_at: '2026-10-28T00:00:00Z', cpu: {}, ram: {}, swap: {}, load: {}, disk: {}, network: {}, connections: {} };
  const cases = [
    [{ uptime: 0 }, null],
    [{}, null],
    [{ uptime: 0, quality: { uptime: 'ok' } }, null],
    [{ quality: { uptime: 'ok' } }, null],
    [{ uptime: 123 }, 123],
    [{ uptime: 123, quality: { uptime: 'ok' } }, 123],
    [{ uptime: 123, quality: { uptime: 'unknown' } }, null],
    [{ uptime: 123, quality: { uptime: '' } }, null],
    [{ uptime: -1, quality: { uptime: 'ok' } }, null],
  ];
  for (const [fields, expected] of cases) {
    const [record] = exports.liveDataToRecords('node', [{ ...base, ...fields }]);
    assert.equal(record.uptime, expected, JSON.stringify(fields));
  }
});
