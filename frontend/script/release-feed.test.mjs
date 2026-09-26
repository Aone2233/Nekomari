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

const CACHE_KEY = 'nekomari.github.releases.v3';
const LEGACY_CACHE_KEY = 'nekomari.github.releases.v2';

/** A localStorage stand-in with the three methods the module uses. */
function fakeStorage(initial) {
  const map = new Map(initial ? Object.entries(initial) : []);
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    raw: map,
  };
}

/**
 * What the module is expected to leave behind after a successful fetch at
 * `fetchedAt`, with no failed attempt since.
 */
const successEntry = (fetchedAt, tags) =>
  JSON.stringify({ fetchedAt, attemptedAt: fetchedAt, releases: tags.map((t) => RELEASE(t)) });

/**
 * Load the module with stubbed globals. `responses` is consumed one per fetch;
 * an entry that is an Error is thrown, otherwise it is returned as
 * `{ ok, json() }`.
 */
function harness({ storage = fakeStorage(), responses = [] } = {}) {
  const exports = {};
  let fetches = 0;
  const calls = [];
  const fetchStub = async (url, init) => {
    calls.push({ url, init });
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
  return { ...exports, storage, fetchCount: () => fetches, calls };
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
    removeItem: () => { throw new Error('denied'); },
  };
  const h = harness({ storage: hostile, responses: [{ body: [RELEASE('v0.2.0')] }] });
  const releases = await h.loadGithubReleases(T0);
  assert.deepEqual(tags(releases), ['v0.2.0']);
  assert.equal(h.fetchCount(), 1);
});

test('a failed refresh does not make the stale list fresh again for six hours', async () => {
  // The bug this pins down: the successful list and the failed attempt shared
  // one timestamp, so a failed refresh past the six-hour TTL rewrote it as
  // "now". The next load then compared that timestamp against the *success*
  // TTL, saw a non-empty list that looked fresh, and served it without
  // fetching — the stale list was treated as current for another six hours,
  // and the fifteen-minute retry after a failure never happened at all.
  const h = harness({
    responses: [{ body: [RELEASE('v0.2.0')] }, new Error('network down')],
  });
  await h.loadGithubReleases(T0);

  // Stale, refresh fails, the stale list is still the best answer available.
  const atExpiry = T0 + SUCCESS_TTL + 1;
  const afterFailure = await h.loadGithubReleases(atExpiry);
  assert.deepEqual(tags(afterFailure), ['v0.2.0']);
  assert.equal(h.fetchCount(), 2, 'a stale cache must attempt a refresh');

  // The failed attempt starts a fifteen-minute window.
  const servedStale = await h.loadGithubReleases(atExpiry + 60_000);
  assert.deepEqual(tags(servedStale), ['v0.2.0']);
  assert.equal(h.fetchCount(), 2, 'a recent failure must not be retried immediately');

  // And the attempt is dated when it happened, not six hours forward.
  const persisted = JSON.parse(h.storage.getItem(CACHE_KEY));
  assert.equal(persisted.fetchedAt, T0, 'a failure must not move the success time');
  assert.equal(persisted.attemptedAt, atExpiry, 'a failure records when it was attempted');
});

test('a failing network is retried once the window passes, and recovers', async () => {
  const h = harness({
    responses: [
      { body: [RELEASE('v0.2.0')] },
      new Error('network down'),
      { body: [RELEASE('v0.3.0')] },
    ],
  });
  await h.loadGithubReleases(T0);

  const atExpiry = T0 + SUCCESS_TTL + 1;
  await h.loadGithubReleases(atExpiry);
  assert.equal(h.fetchCount(), 2);

  // Inside the failure window: still no network.
  const inside = await h.loadGithubReleases(atExpiry + FAILURE_TTL - 1);
  assert.deepEqual(tags(inside), ['v0.2.0']);
  assert.equal(h.fetchCount(), 2);

  // Past it: one more attempt, and this one succeeds, so the new list wins.
  const recovered = await h.loadGithubReleases(atExpiry + FAILURE_TTL + 1);
  assert.deepEqual(tags(recovered), ['v0.3.0']);
  assert.equal(h.fetchCount(), 3);

  // A success refills the six-hour window from its own time.
  const stillFresh = await h.loadGithubReleases(atExpiry + FAILURE_TTL + 1 + 1000);
  assert.deepEqual(tags(stillFresh), ['v0.3.0']);
  assert.equal(h.fetchCount(), 3);
});

test('consecutive failures keep the stale list and do not stack network attempts', async () => {
  const h = harness({
    responses: [
      { body: [RELEASE('v0.2.0')] },
      new Error('down'),
      new Error('down'),
      new Error('down'),
    ],
  });
  await h.loadGithubReleases(T0);

  // Three failed refreshes, each spaced past the fifteen-minute window, so each
  // one is allowed exactly one attempt. Every attempt dates the *failure*; the
  // success time never moves, so the list stays stale rather than being
  // promoted back to fresh.
  for (const offset of [SUCCESS_TTL + 1, SUCCESS_TTL + FAILURE_TTL + 2, SUCCESS_TTL + 2 * FAILURE_TTL + 3]) {
    const cached = await h.loadGithubReleases(T0 + offset);
    assert.deepEqual(tags(cached), ['v0.2.0']);
  }
  assert.equal(h.fetchCount(), 4, 'one success plus one attempt per window');

  const persisted = JSON.parse(h.storage.getItem(CACHE_KEY));
  assert.equal(persisted.fetchedAt, T0);
  assert.equal(persisted.attemptedAt, T0 + SUCCESS_TTL + 2 * FAILURE_TTL + 3);
});

test('an HTTP error and a transport failure share one failure window', async () => {
  const h = harness({
    responses: [
      { body: [RELEASE('v0.2.0')] },
      { ok: false, status: 403, body: null },
      { body: [RELEASE('v0.3.0')] },
    ],
  });
  await h.loadGithubReleases(T0);

  const atExpiry = T0 + SUCCESS_TTL + 1;
  const afterError = await h.loadGithubReleases(atExpiry);
  assert.deepEqual(tags(afterError), ['v0.2.0']);
  assert.equal(h.fetchCount(), 2);

  const inside = await h.loadGithubReleases(atExpiry + FAILURE_TTL - 1);
  assert.deepEqual(tags(inside), ['v0.2.0']);
  assert.equal(h.fetchCount(), 2, 'an HTTP error must start the same window');

  const recovered = await h.loadGithubReleases(atExpiry + FAILURE_TTL + 1);
  assert.deepEqual(tags(recovered), ['v0.3.0']);
});

test('concurrent callers past the TTL still share one attempt', async () => {
  const h = harness({
    storage: fakeStorage({
      [CACHE_KEY]: successEntry(T0, ['v0.2.0']),
    }),
    responses: [{ body: [RELEASE('v0.3.0')] }],
  });
  const results = await Promise.all([
    h.loadGithubReleases(T0 + SUCCESS_TTL + 1),
    h.loadGithubReleases(T0 + SUCCESS_TTL + 1),
    h.loadGithubReleases(T0 + SUCCESS_TTL + 1),
  ]);
  for (const result of results) assert.deepEqual(tags(result), ['v0.3.0']);
  assert.equal(h.fetchCount(), 1, 'a stale cache must still de-duplicate concurrent callers');
});

test('a failed attempt with no list to keep does not invent a success time', async () => {
  // The entry that has to be replaced here parses but cannot be aged, so it is
  // discarded. What replaces it must not claim a successful fetch, or the next
  // load would serve an empty cache as a fresh list.
  const storage = fakeStorage({
    [CACHE_KEY]: JSON.stringify({ fetchedAt: 'never', attemptedAt: 0, releases: 7 }),
  });
  const h = harness({ storage, responses: [new Error('down')] });
  await assert.rejects(() => h.loadGithubReleases(T0), /down/);

  const persisted = JSON.parse(storage.getItem(CACHE_KEY));
  assert.equal(persisted.releases, null);
  assert.equal(persisted.attemptedAt, T0);
  assert.equal(persisted.fetchedAt, 0, 'a failure is not a successful fetch');

  // The recorded failure is the window: the next load inside it does not fetch.
  const h2 = harness({ storage, responses: [{ body: [RELEASE('v0.9.0')] }] });
  await assert.rejects(() => h2.loadGithubReleases(T0 + FAILURE_TTL - 1), /recently/);
  assert.equal(h2.fetchCount(), 0);
});

test('a cache entry from the old format is not trusted', async () => {
  // The v2 entry kept only one timestamp, which a failed attempt could have
  // written. Reading it as a success time would restore exactly the bug this
  // version fixes, so it is refetched instead.
  const storage = fakeStorage({
    [LEGACY_CACHE_KEY]: JSON.stringify({ attemptedAt: T0, releases: [RELEASE('v0.2.0')] }),
  });
  const h = harness({ storage, responses: [{ body: [RELEASE('v0.3.0')] }] });

  const releases = await h.loadGithubReleases(T0 + 1000);
  assert.deepEqual(tags(releases), ['v0.3.0'], 'the old entry must not be served');
  assert.equal(h.fetchCount(), 1);

  // And the legacy entry is cleared, so it cannot come back through a later
  // version of this module that reads the old key again.
  assert.equal(storage.getItem(LEGACY_CACHE_KEY), null, 'the old entry is cleared');
});

test('a malformed cached timestamp is retried instead of trusted', async () => {
  const h = harness({
    storage: fakeStorage({
      [CACHE_KEY]: JSON.stringify({ fetchedAt: null, attemptedAt: T0, releases: [RELEASE('v0.2.0')] }),
    }),
    responses: [{ body: [RELEASE('v0.3.0')] }],
  });
  const releases = await h.loadGithubReleases(T0 + 1000);
  assert.deepEqual(tags(releases), ['v0.3.0']);
  assert.equal(h.fetchCount(), 1);
});

test('a cached list still fresh at its own success time is served without a fetch', async () => {
  const h = harness({
    storage: fakeStorage({
      [CACHE_KEY]: successEntry(T0, ['v0.2.0']),
    }),
  });
  const releases = await h.loadGithubReleases(T0 + SUCCESS_TTL - 1);
  assert.deepEqual(tags(releases), ['v0.2.0']);
  assert.equal(h.fetchCount(), 0, 'a fresh success cache must not reach the network');
});

test('the request opts out of any cache, so a replay cannot look like a fetch', async () => {
  // A response replayed by the service worker or the HTTP cache resolves the
  // fetch like any other, and the module would stamp it with the current time.
  const h = harness({ responses: [{ body: [RELEASE('v0.2.0')] }] });
  await h.loadGithubReleases(T0);
  assert.equal(h.calls.length, 1);
  assert.equal(h.calls[0].url, 'https://api.github.com/repos/Aone2233/Nekomari/releases?per_page=100');
  assert.equal(h.calls[0].init?.cache, 'no-store');
});
