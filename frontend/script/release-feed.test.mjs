import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real module without a browser or a live GitHub.
const source = readFileSync(new URL('../src/lib/releaseFeed.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

/**
 * The module runs in a vm context, so the arrays it returns have that realm's
 * Array.prototype and `deepStrictEqual` would reject them as unequal even when
 * the contents match. Rebuild them in this realm before comparing.
 */
const tags = (releases) => Array.from(releases, (r) => r.tag_name);

const CACHE_KEY = 'nekomari.github.releases.v2';

/** A localStorage stand-in with the two methods the module uses. */
function fakeStorage(initial) {
  const map = new Map(initial ? Object.entries(initial) : []);
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    raw: map,
  };
}

/**
 * Load the module with stubbed globals. `responses` is consumed one per fetch;
 * an entry that is an Error is thrown, otherwise it is returned as
 * `{ ok, json() }`.
 */
function harness({ storage = fakeStorage(), responses = [] } = {}) {
  const exports = {};
  let fetches = 0;
  const fetchStub = async () => {
    const next = responses[fetches++];
    if (next instanceof Error) throw next;
    if (next === undefined) throw new Error('unexpected fetch');
    return { ok: next.ok ?? true, status: next.status ?? 200, json: async () => next.body };
  };
  vm.runInNewContext(compiled, {
    module: { exports },
    exports,
    window: { localStorage: storage },
    fetch: fetchStub,
    console,
  });
  return { ...exports, storage, fetchCount: () => fetches };
}

const RELEASE = (tag, extra = {}) => ({
  tag_name: tag,
  name: tag,
  html_url: `https://example.test/${tag}`,
  ...extra,
});

const SUCCESS_TTL = 6 * 60 * 60 * 1000;
const FAILURE_TTL = 15 * 60 * 1000;
const T0 = Date.UTC(2026, 8, 23, 12);

test('fetches once and reuses the cache within the TTL', async () => {
  const h = harness({ responses: [{ body: [RELEASE('v0.2.0')] }] });
  const first = await h.loadGithubReleases(T0);
  assert.deepEqual(tags(first), ['v0.2.0']);
  assert.equal(h.fetchCount(), 1);

  // A second mount — the admin bar re-mounts on every admin page navigation —
  // must not touch the network again.
  const second = await h.loadGithubReleases(T0 + 1000);
  assert.deepEqual(tags(second), ['v0.2.0']);
  assert.equal(h.fetchCount(), 1, 'a fresh cache must not re-fetch');

  await h.loadGithubReleases(T0 + SUCCESS_TTL - 1);
  assert.equal(h.fetchCount(), 1, 'still fresh one millisecond before the TTL');
});

test('refetches once the cache is stale', async () => {
  const h = harness({
    responses: [{ body: [RELEASE('v0.2.0')] }, { body: [RELEASE('v0.3.0')] }],
  });
  await h.loadGithubReleases(T0);
  const after = await h.loadGithubReleases(T0 + SUCCESS_TTL + 1);
  assert.deepEqual(tags(after), ['v0.3.0']);
  assert.equal(h.fetchCount(), 2);
});

test('drops drafts and prereleases before caching', async () => {
  const h = harness({
    responses: [
      {
        body: [
          RELEASE('v0.2.0'),
          RELEASE('v0.2.1-rc1', { prerelease: true }),
          RELEASE('v0.3.0', { draft: true }),
        ],
      },
    ],
  });
  const releases = await h.loadGithubReleases(T0);
  assert.deepEqual(tags(releases), ['v0.2.0']);

  const cached = JSON.parse(h.storage.getItem(CACHE_KEY));
  assert.deepEqual(tags(cached.releases), ['v0.2.0']);
});

test('a failed refresh falls back to the stale list rather than throwing', async () => {
  const h = harness({
    responses: [{ body: [RELEASE('v0.2.0')] }, new Error('network down')],
  });
  await h.loadGithubReleases(T0);
  const after = await h.loadGithubReleases(T0 + SUCCESS_TTL + 1);
  assert.deepEqual(tags(after), ['v0.2.0']);
  assert.equal(h.fetchCount(), 2);
});

test('an HTTP error is treated the same way as a transport failure', async () => {
  const h = harness({
    responses: [{ body: [RELEASE('v0.2.0')] }, { ok: false, status: 403, body: null }],
  });
  await h.loadGithubReleases(T0);
  const after = await h.loadGithubReleases(T0 + SUCCESS_TTL + 1);
  assert.deepEqual(tags(after), ['v0.2.0']);
});

test('a failure with no cache propagates, so the caller can clear its state', async () => {
  const h = harness({ responses: [{ ok: false, status: 403, body: null }] });
  await assert.rejects(() => h.loadGithubReleases(T0), /GitHub HTTP 403/);
});

test('a failure is remembered, so a blocked network is not retried per page load', async () => {
  // This is the case the first version of this cache got wrong: it cached only
  // successes, so where GitHub is unreachable every navigation fetched again.
  const h = harness({
    responses: [{ ok: false, status: 403, body: null }, { body: [RELEASE('v0.9.0')] }],
  });
  await assert.rejects(() => h.loadGithubReleases(T0), /GitHub HTTP 403/);
  assert.equal(h.fetchCount(), 1);

  // Every navigation inside the failure window is served from the cache, which
  // means it must not reach the network — and must still report failure so the
  // caller clears its state.
  for (const offset of [1_000, 60_000, FAILURE_TTL - 1]) {
    await assert.rejects(() => h.loadGithubReleases(T0 + offset), /could not be fetched recently/);
  }
  assert.equal(h.fetchCount(), 1, 'a recent failure must not be retried');

  // Once the failure window passes, the next load tries again.
  const recovered = await h.loadGithubReleases(T0 + FAILURE_TTL + 1);
  assert.deepEqual(tags(recovered), ['v0.9.0']);
  assert.equal(h.fetchCount(), 2);
});

test('concurrent callers share a single attempt', async () => {
  // The admin bar mounts twice on its first render; without de-duplication both
  // mounts miss the empty cache and fetch.
  const h = harness({ responses: [{ body: [RELEASE('v0.2.0')] }] });
  const [a, b, c] = await Promise.all([
    h.loadGithubReleases(T0),
    h.loadGithubReleases(T0),
    h.loadGithubReleases(T0),
  ]);
  for (const result of [a, b, c]) assert.deepEqual(tags(result), ['v0.2.0']);
  assert.equal(h.fetchCount(), 1, 'three concurrent callers must fetch once');
});

test('a later call fetches again after an attempt settles', async () => {
  const h = harness({
    responses: [{ ok: false, status: 403, body: null }, { body: [RELEASE('v0.2.0')] }],
  });
  await assert.rejects(() => h.loadGithubReleases(T0), /GitHub HTTP 403/);
  // Past the failure window, a new call is a new attempt rather than the old
  // settled promise.
  const after = await h.loadGithubReleases(T0 + FAILURE_TTL + 1);
  assert.deepEqual(tags(after), ['v0.2.0']);
  assert.equal(h.fetchCount(), 2);
});

test('a corrupt cache entry is ignored instead of breaking the indicator', async () => {
  const h = harness({
    storage: fakeStorage({ [CACHE_KEY]: 'not json' }),
    responses: [{ body: [RELEASE('v0.2.0')] }],
  });
  const releases = await h.loadGithubReleases(T0);
  assert.deepEqual(tags(releases), ['v0.2.0']);
  assert.equal(h.fetchCount(), 1);
});

test('a cache entry with the wrong shape is ignored', async () => {
  const h = harness({
    storage: fakeStorage({
      [CACHE_KEY]: JSON.stringify({ attemptedAt: 'yesterday', releases: 7 }),
    }),
    responses: [{ body: [RELEASE('v0.2.0')] }],
  });
  const releases = await h.loadGithubReleases(T0);
  assert.deepEqual(tags(releases), ['v0.2.0']);
  assert.equal(h.fetchCount(), 1);
});

test('a storage that throws does not break the fetch path', async () => {
  const hostile = {
    getItem: () => { throw new Error('denied'); },
    setItem: () => { throw new Error('denied'); },
  };
  const h = harness({ storage: hostile, responses: [{ body: [RELEASE('v0.2.0')] }] });
  const releases = await h.loadGithubReleases(T0);
  assert.deepEqual(tags(releases), ['v0.2.0']);
  assert.equal(h.fetchCount(), 1);
});
