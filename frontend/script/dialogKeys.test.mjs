import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real module: it has no imports, so the transpiled CommonJS runs in a
// bare vm context.
const source = readFileSync(new URL('../src/pages/terminal/dialogKeys.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

const context = { module: { exports: {} }, exports: {}, console };
context.exports = context.module.exports;
vm.runInNewContext(compiled, context);
const { shouldSubmitOnEnter } = context.module.exports;

const enterOn = (tagName) => ({ key: 'Enter', target: { tagName } });
const options = { submitting: false };

test('Enter on a button is not a confirmation', () => {
  // The bug this file exists for: TerminalDialog's handler sits on the full-screen
  // backdrop and the Cancel button is its child. Enter's activation of the focused
  // button is the *default action* of that keydown, so the handler's preventDefault()
  // suppressed the Cancel click and ran onSubmit() instead — on a dialog that deletes
  // files when `destructive` is set.
  assert.equal(shouldSubmitOnEnter(enterOn('BUTTON'), options), false);
  assert.equal(shouldSubmitOnEnter(enterOn('button'), options), false);
});

test('Enter on the dialog surface is still a confirmation', () => {
  assert.equal(shouldSubmitOnEnter(enterOn('DIV'), options), true);
  assert.equal(shouldSubmitOnEnter(enterOn('INPUT'), options), true);
  assert.equal(shouldSubmitOnEnter({ key: 'Enter', target: null }, options), true);
  assert.equal(shouldSubmitOnEnter({ key: 'Enter' }, options), true);
});

test('other keys and an in-flight submission never confirm', () => {
  assert.equal(shouldSubmitOnEnter(enterOn('DIV'), { submitting: true }), false);
  assert.equal(shouldSubmitOnEnter({ key: 'Escape', target: { tagName: 'DIV' } }, options), false);
  assert.equal(shouldSubmitOnEnter({ key: ' ', target: { tagName: 'DIV' } }, options), false);
});
