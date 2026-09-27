import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

/**
 * The bulk edit page's own contract, checked by running the real module.
 *
 * The rule this file exists for is the one that decides whether H2 is safe to use at all:
 * **a field whose switch is off must not be in the request.** The page's own comment says why
 * — sending every field with its form value overwrites each node's unmentioned fields, so the
 * first bulk edit anyone tries would set a whole fleet's group to the empty string. That is
 * exactly the behaviour a typo in one line would restore, and it is invisible in a rendered
 * page, so it is asserted on the module's output rather than on the DOM.
 *
 * The module is transpiled and imported rather than string-matched: the assertion is about
 * what `buildUpdate` returns, not about how it is written.
 */

const compiled = ts.transpileModule(
  readFileSync(new URL('../src/types/Bulk.ts', import.meta.url), 'utf8'),
  { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } },
).outputText;

// The module imports a type only, so the transpiled output is self-contained; loaded through
// a data URL so no file has to be written.
const module = await import(
  `data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`
);

const { buildUpdate, emptyForm, enabledFieldCount } = module;

test('an untouched form sends nothing', () => {
  const form = emptyForm();
  assert.deepEqual(buildUpdate(form), {}, 'a fresh form must produce an empty update');
  assert.equal(enabledFieldCount(form), 0);
});

test('only switched-on fields are sent', () => {
  const form = emptyForm();
  form.group.enabled = true;
  form.group.value = 'apac';
  form.weight.enabled = true;
  form.weight.value = 9;

  const update = buildUpdate(form);
  assert.deepEqual(
    Object.keys(update).sort(),
    ['group', 'weight'],
    'exactly the enabled fields, and nothing else',
  );
  assert.equal(update.group, 'apac');
  assert.equal(update.weight, 9);
});

test('a switched-off field keeps its default out of the request', () => {
  // This is the failure the rule prevents: the form's default for `group` is the empty
  // string, so sending it would blank the group on every selected node.
  const form = emptyForm();
  form.weight.enabled = true;
  form.weight.value = 3;

  const update = buildUpdate(form);
  assert.equal(update.group, undefined, 'the untouched group must not be sent');
  assert.equal(update.tags, undefined, 'and neither must any other untouched field');
  assert.equal(update.hidden, undefined);
  assert.equal(update.currency, undefined);
});

test('every field writes its own wire name', () => {
  const form = emptyForm();
  for (const field of Object.values(form)) field.enabled = true;
  form.billingCycle.value = 12;
  form.trafficLimit.value = 1024;
  form.trafficLimitType.value = 'sum';

  const update = buildUpdate(form);
  // snake_case on the wire, as the Go side expects; the form uses camelCase for readability.
  assert.ok('billing_cycle' in update, 'billingCycle must write billing_cycle');
  assert.ok('traffic_limit' in update, 'trafficLimit must write traffic_limit');
  assert.ok('traffic_limit_type' in update, 'trafficLimitType must write traffic_limit_type');
  assert.equal(update.billing_cycle, 12);
  assert.equal(update.traffic_limit, 1024);
  assert.equal(update.traffic_limit_type, 'sum');
  // And never the selector: the envelope carries `uuids` separately.
  assert.ok(!('uuid' in update), 'uuid is not a field to edit');
});

test('a falsy value is still sent when its field is on', () => {
  // `hidden = false` and `weight = 0` are meaningful values, so "is it truthy" must not be
  // how the page decides whether to include a field.
  const form = emptyForm();
  form.hidden.enabled = true;
  form.hidden.value = false;
  form.weight.enabled = true;
  form.weight.value = 0;

  const update = buildUpdate(form);
  assert.ok('hidden' in update, 'hidden=false is a decision, not an absence');
  assert.equal(update.hidden, false);
  assert.ok('weight' in update, 'weight=0 is a value');
  assert.equal(update.weight, 0);
});
