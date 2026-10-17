import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import test from 'node:test';

import { diffAndMerge } from './i18n-merge.mjs';

const LOCALES_DIR = new URL('../src/i18n/locales/', import.meta.url);
const REFERENCE = 'en';

function flatten(obj, prefix = '') {
  const out = {};
  for (const [key, value] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      Object.assign(out, flatten(value, path));
    } else {
      out[path] = value;
    }
  }
  return out;
}

function localeFiles() {
  return readdirSync(LOCALES_DIR).filter((name) => name.endsWith('.json'));
}

function load(name) {
  return flatten(JSON.parse(readFileSync(new URL(name, LOCALES_DIR), 'utf8')));
}

function placeholders(value) {
  return [...String(value).matchAll(/\{\{\s*([\w.]+)\s*\}\}/g)].map((m) => m[1]).sort();
}

// The gap this test exists for: ja_JP, zh_TW and id_ID were each missing 134 keys —
// whole feature areas (bulk, status, maintenance, config, forecast) fell back to
// English at runtime, and nothing noticed because `fallbackLng` makes the fallback
// invisible. The numbers were measured on 2026-10-13; the point is not the number, it
// is that a missing key fails a test that runs in `npm test` and in CI.
test('every locale carries every key the reference locale defines', () => {
  const reference = load(`${REFERENCE}.json`);
  const referenceKeys = Object.keys(reference);

  for (const file of localeFiles()) {
    const locale = file.replace(/\.json$/, '');
    const flat = load(file);
    const missing = referenceKeys.filter((key) => !(key in flat));
    const extra = Object.keys(flat).filter((key) => !(key in reference));

    assert.deepEqual(
      missing,
      [],
      `${locale} is missing ${missing.length} key(s); the UI falls back to ${REFERENCE} silently: ${missing.slice(0, 10).join(', ')}`,
    );
    assert.deepEqual(
      extra,
      [],
      `${locale} has ${extra.length} key(s) the reference locale does not; either add them to ${REFERENCE} or drop them: ${extra.slice(0, 10).join(', ')}`,
    );
  }
});

test('a translation keeps the placeholders its reference string uses', () => {
  const reference = load(`${REFERENCE}.json`);

  for (const file of localeFiles()) {
    const locale = file.replace(/\.json$/, '');
    if (locale === REFERENCE) continue;
    const flat = load(file);

    for (const [key, sourceText] of Object.entries(reference)) {
      if (!(key in flat)) continue;
      const want = placeholders(sourceText);
      if (want.length === 0) continue;
      assert.deepEqual(
        placeholders(flat[key]),
        want,
        `${locale}:${key} must interpolate exactly ${want.join(', ')}`,
      );
    }
  }
});

// `npm run i18n:sync` used to rewrite every target file keeping only the keys the
// source had, so running it without OPENAI_API_KEY (where the AI fill returns empty
// strings) deleted every target-only key. A tool that loses data when it cannot reach
// its API is worse than no tool.
test('the merge keeps keys that only the target locale has', () => {
  const source = { 'a.one': 'one', 'a.two': 'two' };
  const target = { 'a.one': 'eins', 'legacy.only': 'nur hier' };

  const { result, added, keptOnlyInTarget } = diffAndMerge(source, target);

  assert.equal(result['legacy.only'], 'nur hier', 'a target-only key must survive the merge');
  assert.deepEqual(keptOnlyInTarget, ['legacy.only']);
  assert.equal(result['a.one'], 'eins', 'an existing translation is kept');
  assert.deepEqual(added, ['a.two'], 'a key the target lacks is reported as added');
});

test('the merge treats an empty translation as missing rather than as a value', () => {
  const source = { 'a.one': 'one' };
  const target = { 'a.one': '   ' };

  const { result, added } = diffAndMerge(source, target);

  assert.equal('a.one' in result, false, 'a blank translation is not written back as blank');
  assert.deepEqual(added, ['a.one']);
});
