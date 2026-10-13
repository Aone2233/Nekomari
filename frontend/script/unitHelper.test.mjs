import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real module. It has no imports, so the transpiled CommonJS runs in a
// bare vm context.
const source = readFileSync(new URL('../src/utils/unitHelper.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

const context = { module: { exports: {} }, exports: {}, console };
context.exports = context.module.exports;
context.globalThis = context;
vm.runInNewContext(compiled, context);
const { stringToBytes, formatBytes } = context.module.exports;

test('stringToBytes keeps every documented form', () => {
  assert.equal(stringToBytes('1MB'), 1048576);
  assert.equal(stringToBytes('1 MB'), 1048576);
  assert.equal(stringToBytes('5.4MB'), 5662310);
  // The JSDoc used to claim 6525139624935 for this one. Nothing tested the function,
  // so the wrong number sat there unobserved; 6222765 * 2**20 is the value below.
  assert.equal(stringToBytes('6,222,765 MB'), 6525042032640);
  assert.equal(stringToBytes('128*1024gb'), 140737488355328);
  assert.equal(stringToBytes('1e3kb'), 1024000);
  assert.equal(stringToBytes('0.2gb'), 214748365);
  assert.equal(stringToBytes('1024'), 1024);
  assert.equal(stringToBytes('1tb'), 1099511627776);
  assert.equal(stringToBytes('kb'), 1024);
  assert.equal(stringToBytes('.5gb'), 536870912);
});

test('stringToBytes returns 0 for input it cannot parse', () => {
  assert.equal(stringToBytes('abc-gb'), 0);
  assert.equal(stringToBytes('1.'), 0);
  assert.equal(stringToBytes('*1024gb'), 0);
  assert.equal(stringToBytes('1*gb'), 0);
  assert.equal(stringToBytes(''), 0);
});

// The regression this file exists for: the numeric part used to be evaluated with
// `new Function("return " + numericPart)`, so anything pasted into the traffic-limit
// field ran as JavaScript in the panel's origin.
test('stringToBytes never evaluates the numeric part as JavaScript', () => {
  const payloads = [
    '1;(globalThis.__pwned = 1)',
    '1;globalThis.__pwned = 1',
    '(globalThis.__pwned = 1)',
    'globalThis.__pwned = 1',
    '1+1',
    '1/0',
  ];
  for (const payload of payloads) {
    assert.equal(stringToBytes(payload), 0, `payload ${JSON.stringify(payload)} must not parse`);
    assert.equal(context.__pwned, undefined, `payload ${JSON.stringify(payload)} must not execute`);
  }
});

test('formatBytes still reports unknown values as "-"', () => {
  assert.equal(formatBytes(null), '-');
  assert.equal(formatBytes(undefined), '-');
  assert.equal(formatBytes(Number.NaN), '-');
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(1024), '1.00 KB');
});
