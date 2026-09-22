import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

const compiled = ts.transpileModule(
  readFileSync(new URL('../src/lib/frameThrottle.ts', import.meta.url), 'utf8'),
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText;
function harness() {
  const frames = new Map();
  let id = 0;
  const exports = {};
  vm.runInNewContext(compiled, {
    exports,
    requestAnimationFrame: callback => { frames.set(++id, callback); return id; },
    cancelAnimationFrame: key => frames.delete(key),
  });
  return { frameThrottle: exports.frameThrottle, frames, paint() {
    const callbacks = [...frames.values()];
    frames.clear();
    callbacks.forEach(callback => callback());
  } };
}
test('coalesces a pointer burst to the latest position per paint', () => {
  const h = harness(), calls = [];
  const drag = h.frameThrottle(x => calls.push(x));
  for (let x = 0; x < 100; x++) drag(x);
  assert.equal(h.frames.size, 1);
  assert.deepEqual(calls, []);
  h.paint();
  assert.deepEqual(calls, [99]);
  drag(120); h.paint();
  assert.deepEqual(calls, [99, 120]);
});
test('flush delivers the last movement once before drag end', () => {
  const h = harness(), calls = [];
  const drag = h.frameThrottle(x => calls.push(x));
  drag(42); drag.flush(); drag.flush(); h.paint();
  assert.deepEqual(calls, [42]);
  assert.equal(h.frames.size, 0);
});
test('unmount cancellation drops pending work and can be reused', () => {
  const h = harness(), calls = [];
  const drag = h.frameThrottle(x => calls.push(x));
  drag(42); drag.cancel(); h.paint(); drag.flush();
  assert.deepEqual(calls, []);
  drag(7); h.paint();
  assert.deepEqual(calls, [7]);
});
