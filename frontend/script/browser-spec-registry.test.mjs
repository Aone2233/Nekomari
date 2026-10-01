import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

/**
 * Every mounted browser spec must actually be run by CI.
 *
 * The fixture job in `.github/workflows/ci.yml` enumerates its specs one line at a time instead of
 * globbing `script/*.browser.spec.py`. That is deliberate — an explicit list is reviewable, and a
 * glob would quietly pick up a half-written file — but it has one failure mode that nothing else
 * catches: **a new spec file is simply never run.** It passes locally, it is committed, and every
 * CI run stays green. `account-sso-2fa.browser.spec.py` was added exactly that way, and the only
 * reason it surfaced was somebody reading the workflow by hand.
 *
 * Only `*.browser.spec.py` is covered here. The specs named `*.spec.py` without `browser`
 * (`panel-smoke.spec.py`, `admin-navigation.spec.py`, `admin-write-paths.spec.py`) belong to the
 * later `panel-smoke` job, which boots the real server instead of mounting a fixture.
 */
const scriptDir = fileURLToPath(new URL('./', import.meta.url));
const workflow = readFileSync(new URL('../../.github/workflows/ci.yml', import.meta.url), 'utf8');

const onDisk = readdirSync(scriptDir)
  .filter((name) => name.endsWith('.browser.spec.py'))
  .sort();

// Tolerates trailing arguments on the line; what matters is that the file is invoked at all.
const listed = [...workflow.matchAll(/^ *python script\/([\w.-]+\.spec\.py)\b/gm)]
  .map((match) => match[1])
  .sort();

test('the CI fixture job runs every mounted browser spec that exists', () => {
  // A guard on the guard: if the list stops being parsed, the assertions below would pass
  // vacuously against an empty array.
  assert.ok(onDisk.length >= 20, `expected the browser-spec suite, found only ${onDisk.length}`);
  assert.ok(listed.length >= 20, `parsed only ${listed.length} specs out of .github/workflows/ci.yml`);

  const neverRun = onDisk.filter((name) => !listed.includes(name));
  assert.deepEqual(
    neverRun,
    [],
    `these specs are in the repository but no CI job runs them: ${neverRun.join(', ')}`,
  );
});

test('every spec CI runs still exists', () => {
  const allSpecs = new Set(readdirSync(scriptDir).filter((name) => name.endsWith('.spec.py')));
  const ghosts = listed.filter((name) => !allSpecs.has(name));
  assert.deepEqual(ghosts, [], `CI runs specs that are not in the repository: ${ghosts.join(', ')}`);
});
