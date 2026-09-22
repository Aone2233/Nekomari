import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real upload client without a browser or a live server: the module is
// transpiled to CommonJS and run in a vm context whose globals we own, so the fake XHR
// and fetch decide exactly what the "server" answers, when it answers, and how long the
// client waits. Fake timers make the retry backoff observable instead of slow.
const compiled = ts.transpileModule(
  readFileSync(new URL('../src/lib/chunkUpload.ts', import.meta.url), 'utf8'),
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText;

const CHUNK_SIZE = 5 * 1024 * 1024;
const SESSION_ID = '11111111-1111-4111-8111-111111111111';

class FakeFile {
  constructor(name, size) {
    this.name = name;
    this.size = size;
  }
  slice(start, end) {
    return { size: end - start };
  }
}

class FakeFormData {
  constructor() {
    this.fields = new Map();
  }
  append(name, value, filename) {
    this.fields.set(name, { value, filename });
  }
}

class Listeners {
  constructor() {
    this.byType = new Map();
  }
  addEventListener(type, listener) {
    this.byType.set(type, [...(this.byType.get(type) ?? []), listener]);
  }
  removeEventListener(type, listener) {
    const list = this.byType.get(type) ?? [];
    this.byType.set(
      type,
      list.filter((entry) => entry !== listener),
    );
  }
  dispatch(event) {
    for (const listener of [...(this.byType.get(event.type) ?? [])]) listener(event);
  }
}

function createWorld() {
  const state = {
    initRequests: 0,
    mergeRequests: 0,
    chunkRequests: [],
    cancelRequests: [],
    timers: [],
    timerSeq: 0,
    warnings: [],
    chunkPlans: [],
    // Overridable responders. A function receives the request signal; returning a
    // promise keeps the request pending until that signal aborts.
    init: () => ({
      status: 200,
      body: {
        status: 'success',
        data: { upload_id: SESSION_ID, chunk_size: CHUNK_SIZE },
      },
    }),
    merge: () => ({
      status: 200,
      body: { status: 'success', data: { installed: true } },
    }),
    cancel: () => ({ status: 200, body: { status: 'success' } }),
  };
  const firedDelays = [];

  const respond = (descriptor, signal) => {
    const value = typeof descriptor === 'function' ? descriptor(signal) : descriptor;
    if (value && typeof value.then === 'function') return value;
    // Real Headers: header lookup is case-insensitive, exactly like the browser.
    const headers = new Headers(value.headers ?? {});
    return {
      ok: value.status >= 200 && value.status < 300,
      status: value.status,
      headers,
      async json() {
        if (value.body === undefined) throw new SyntaxError('not json');
        return value.body;
      },
    };
  };

  const fetch = (url, init = {}) => {
    const path = String(url);
    if (path.endsWith('/init')) {
      state.initRequests += 1;
      return respond(state.init, init.signal);
    }
    if (path.endsWith('/merge')) {
      state.mergeRequests += 1;
      return respond(state.merge, init.signal);
    }
    if (path.endsWith('/cancel')) {
      state.cancelRequests.push(JSON.parse(init.body));
      return respond(state.cancel, init.signal);
    }
    throw new Error(`unexpected fetch: ${path}`);
  };

  class FakeXHR {
    constructor() {
      this.upload = new Listeners();
      this.listeners = new Listeners();
      this.status = 0;
      this.responseText = '';
      this.headers = new Map();
      this.aborted = false;
      this.timeout = 0;
    }
    open(method, url) {
      this.method = method;
      this.url = url;
    }
    setRequestHeader() {}
    addEventListener(type, listener) {
      this.listeners.addEventListener(type, listener);
    }
    removeEventListener(type, listener) {
      this.listeners.removeEventListener(type, listener);
    }
    getResponseHeader(name) {
      return this.headers.get(String(name).toLowerCase()) ?? null;
    }
    send(body) {
      this.body = body;
      this.uploadID = body.fields.get('upload_id')?.value;
      this.chunkIndex = Number(body.fields.get('chunk_index')?.value);
      state.chunkRequests.push(this);
      const plan = state.chunkPlans.shift();
      if (plan) plan(this);
      else this.respond(200, { status: 'success', data: { received: true } });
    }
    abort() {
      this.aborted = true;
      this.listeners.dispatch({ type: 'abort' });
    }
    progress(loaded, total) {
      this.upload.dispatch({ type: 'progress', lengthComputable: true, loaded, total });
    }
    respond(status, body, headers = {}) {
      this.status = status;
      this.responseText = typeof body === 'string' ? body : JSON.stringify(body);
      this.headers = new Map(
        Object.entries(headers).map(([key, value]) => [key.toLowerCase(), String(value)]),
      );
      this.listeners.dispatch({ type: 'load' });
    }
    failNetwork() {
      this.listeners.dispatch({ type: 'error' });
    }
    timeOut() {
      this.listeners.dispatch({ type: 'timeout' });
    }
  }

  const exports = {};
  vm.runInNewContext(compiled, {
    exports,
    setTimeout: (fn, ms) => {
      const timer = { id: (state.timerSeq += 1), fn, ms };
      state.timers.push(timer);
      return timer.id;
    },
    clearTimeout: (id) => {
      state.timers = state.timers.filter((timer) => timer.id !== id);
    },
    console: { warn: (...args) => state.warnings.push(args.map(String).join(' ')) },
    DOMException,
    AbortController,
    XMLHttpRequest: FakeXHR,
    FormData: FakeFormData,
    fetch,
  });
  return { exports, state, firedDelays };
}

const settle = async (rounds = 6) => {
  for (let round = 0; round < rounds; round += 1) {
    await new Promise((resolve) => setImmediate(resolve));
  }
};

// Fires every queued fake timer (retry backoff, request deadlines) until the module
// stops scheduling new ones, recording the requested delays for assertions.
async function runTimers(world) {
  for (let guard = 0; world.state.timers.length > 0; guard += 1) {
    assert.ok(guard < 50, 'fake timers did not settle');
    const batch = world.state.timers;
    world.state.timers = [];
    world.firedDelays.push(...batch.map((timer) => timer.ms));
    for (const timer of batch) timer.fn();
    await settle();
  }
}

// A hung module promise must fail the test instead of stalling the runner.
const withDeadline = (promise, ms = 2_000) =>
  new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('test deadline exceeded')), ms);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  });

function startUpload(world, purpose, size, onProgress = () => {}) {
  const task = world.exports.createChunkUploadTask('/api/admin/upload');
  const promise = task.upload(purpose, new FakeFile('archive.zip', size), onProgress);
  // Mark the rejection as handled immediately: several tests let the module fail while
  // they inspect request counts, and an unobserved rejection would abort the runner.
  promise.catch(() => {});
  return { task, promise };
}

test('parses Retry-After seconds and HTTP-dates', () => {
  const world = createWorld();
  const { parseRetryAfter } = world.exports;
  assert.equal(parseRetryAfter('7'), 7000);
  assert.equal(parseRetryAfter(' 0 '), 0);
  assert.equal(parseRetryAfter(null), undefined);
  assert.equal(parseRetryAfter(''), undefined);
  assert.equal(parseRetryAfter('soon'), undefined);
  const now = Date.now();
  const delay = parseRetryAfter(new Date(now + 3000).toUTCString(), now);
  assert.ok(delay >= 2000 && delay <= 4000, `unexpected HTTP-date delay ${delay}`);
  assert.equal(parseRetryAfter(new Date(now - 5000).toUTCString(), now), 0);
});

test('retries a transient 500 and then succeeds', async () => {
  const world = createWorld();
  world.state.chunkPlans.push((xhr) =>
    xhr.respond(500, { status: 'error', message: 'store exploded' }),
  );
  const { promise } = startUpload(world, 'plugin', 1024);
  await settle();
  assert.equal(world.state.chunkRequests.length, 1);
  assert.deepEqual(
    world.state.timers.map((timer) => timer.ms),
    [1000],
  );

  await runTimers(world);
  assert.equal((await withDeadline(promise)).installed, true);
  assert.equal(world.state.chunkRequests.length, 2);
  assert.equal(world.state.mergeRequests, 1);
});

for (const status of [400, 403, 404, 413]) {
  test(`does not retry a permanent ${status}`, async () => {
    const world = createWorld();
    world.state.chunkPlans.push((xhr) =>
      xhr.respond(status, { status: 'error', message: `chunk refused ${status}` }),
    );
    const { promise } = startUpload(world, 'theme', 1024);
    await settle();
    await assert.rejects(withDeadline(promise), { message: `chunk refused ${status}` });
    assert.equal(world.state.chunkRequests.length, 1);
    assert.equal(world.state.timers.length, 0);
    // The failed upload still asks the server to release its reservation.
    assert.equal(world.state.cancelRequests.length, 1);
  });
}

// 507 is the one 5xx the store means permanently: web/upload answers it when the
// filesystem cannot absorb this upload's worst-case footprint, so repeating the
// chunk cannot help. It must fail on the first response like a 4xx rather than
// burning the retry budget and delaying the disk-full message.
test('does not retry a 507 insufficient-storage refusal', async () => {
  const world = createWorld();
  world.state.chunkPlans.push((xhr) =>
    xhr.respond(507, {
      status: 'error',
      message: 'insufficient free disk space for upload: 1 bytes available on /data, 2 required',
    }),
  );
  const { promise } = startUpload(world, 'backup', 1024);
  await settle();
  await assert.rejects(withDeadline(promise), { message: /insufficient free disk space/ });
  assert.equal(world.state.chunkRequests.length, 1);
  assert.equal(world.state.timers.length, 0);
});

test('stops after the bounded chunk attempt budget', async () => {
  const world = createWorld();
  for (let index = 0; index < 4; index += 1) {
    world.state.chunkPlans.push((xhr) =>
      xhr.respond(503, { status: 'error', message: 'unavailable' }),
    );
  }
  const { promise } = startUpload(world, 'plugin', 1024);
  await settle();
  await runTimers(world);
  await assert.rejects(withDeadline(promise), { message: 'unavailable' });
  assert.equal(world.state.chunkRequests.length, 4);
  assert.deepEqual(world.firedDelays, [1000, 2000, 4000]);
});

test('honours Retry-After on a 429', async () => {
  const world = createWorld();
  world.state.chunkPlans.push((xhr) =>
    xhr.respond(
      429,
      { status: 'error', message: 'upload store busy; retry later' },
      { 'Retry-After': '7' },
    ),
  );
  const { promise } = startUpload(world, 'backup', 1024);
  await settle();
  assert.deepEqual(
    world.state.timers.map((timer) => timer.ms),
    [7000],
  );

  await runTimers(world);
  assert.equal((await withDeadline(promise)).installed, true);
  assert.equal(world.state.chunkRequests.length, 2);
});

test('clamps an oversized Retry-After', async () => {
  const world = createWorld();
  world.state.chunkPlans.push((xhr) =>
    xhr.respond(429, { status: 'error', message: 'busy' }, { 'Retry-After': '3600' }),
  );
  const { promise } = startUpload(world, 'backup', 1024);
  await settle();
  assert.deepEqual(
    world.state.timers.map((timer) => timer.ms),
    [30000],
  );

  await runTimers(world);
  assert.equal((await withDeadline(promise)).installed, true);
});

test('treats a chunk timeout as transient', async () => {
  const world = createWorld();
  world.state.chunkPlans.push((xhr) => xhr.timeOut());
  const { promise } = startUpload(world, 'plugin', 1024);
  await settle();
  assert.equal(world.state.chunkRequests[0].timeout, 120000);
  assert.deepEqual(
    world.state.timers.map((timer) => timer.ms),
    [1000],
  );

  await runTimers(world);
  assert.equal((await withDeadline(promise)).installed, true);
  assert.equal(world.state.chunkRequests.length, 2);
});

test('aborts an in-flight chunk and retries a 429 on /cancel', async () => {
  const world = createWorld();
  world.state.chunkPlans.push(() => {});
  let cancelCalls = 0;
  world.state.cancel = () => {
    cancelCalls += 1;
    if (cancelCalls === 1) {
      return {
        status: 429,
        body: { status: 'error', message: 'upload store busy; retry later' },
        headers: { 'Retry-After': '2' },
      };
    }
    return { status: 200, body: { status: 'success' } };
  };
  const { task, promise } = startUpload(world, 'theme', 1024);
  await settle();
  const [chunk] = world.state.chunkRequests;
  assert.equal(chunk.aborted, false);

  const cancelPromise = task.cancel();
  await assert.rejects(withDeadline(promise), { name: 'AbortError' });
  assert.equal(chunk.aborted, true);
  await settle();
  assert.equal(world.state.cancelRequests.length, 1);
  assert.deepEqual(
    world.state.timers.map((timer) => timer.ms),
    [2000],
  );

  await runTimers(world);
  const outcome = await withDeadline(cancelPromise);
  assert.equal(outcome.cancelled, true);
  assert.equal(world.state.cancelRequests.length, 2);
  assert.equal(world.state.cancelRequests[0].upload_id, SESSION_ID);
});

test('reports a cancel the store keeps rejecting, with the TTL fallback', async () => {
  const world = createWorld();
  world.state.chunkPlans.push(() => {});
  world.state.cancel = () => ({
    status: 429,
    body: { status: 'error', message: 'upload store busy; retry later' },
    headers: { 'Retry-After': '2' },
  });
  const { task, promise } = startUpload(world, 'plugin', 1024);
  await settle();

  const cancelPromise = task.cancel();
  await assert.rejects(withDeadline(promise), { name: 'AbortError' });
  await runTimers(world);
  const outcome = await withDeadline(cancelPromise);
  assert.equal(outcome.cancelled, false);
  assert.equal(outcome.reason, 'busy');
  assert.match(outcome.message, /upload store busy/);
  assert.match(outcome.message, /within 24 hours/);
  assert.equal(outcome.ttlMs, 24 * 60 * 60 * 1000);
  assert.equal(world.state.cancelRequests.length, 3);
  assert.deepEqual(world.firedDelays, [2000, 2000]);

  // Idempotent: repeating the call reports the same outcome without another request.
  assert.equal(await withDeadline(task.cancel()), outcome);
  assert.equal(world.state.cancelRequests.length, 3);
});

test('does not replay a merge whose response is uncertain', async () => {
  const world = createWorld();
  world.state.merge = (signal) =>
    new Promise((_, reject) => {
      signal.addEventListener('abort', () =>
        reject(new DOMException('The operation was aborted.', 'AbortError')),
      );
    });
  const { promise } = startUpload(world, 'plugin', 1024);
  await settle();
  assert.equal(world.state.mergeRequests, 1);
  assert.deepEqual(
    world.state.timers.map((timer) => timer.ms),
    [300000],
  );

  await runTimers(world);
  await assert.rejects(withDeadline(promise), (error) => {
    assert.equal(error.name, 'MergeOutcomeUnknownError');
    assert.equal(error.uncertain, true);
    assert.match(error.message, /may already be installed/);
    return true;
  });
  assert.equal(world.state.mergeRequests, 1);
});

test('bounds the init request with a deadline', async () => {
  const world = createWorld();
  world.state.init = (signal) =>
    new Promise((_, reject) => {
      signal.addEventListener('abort', () =>
        reject(new DOMException('The operation was aborted.', 'AbortError')),
      );
    });
  const { promise } = startUpload(world, 'plugin', 1024);
  await settle();
  assert.equal(world.state.initRequests, 1);
  assert.deepEqual(
    world.state.timers.map((timer) => timer.ms),
    [30000],
  );

  await runTimers(world);
  await assert.rejects(withDeadline(promise), {
    message: 'upload init timed out after 30000ms',
  });
  assert.equal(world.state.initRequests, 1);
  assert.equal(world.state.chunkRequests.length, 0);
  // No session was created, so there is nothing to cancel.
  assert.equal(world.state.cancelRequests.length, 0);
});

test('logs an unreleased reservation when automatic cleanup fails', async () => {
  const world = createWorld();
  world.state.chunkPlans.push((xhr) =>
    xhr.respond(400, { status: 'error', message: 'bad chunk' }),
  );
  world.state.cancel = () => ({
    status: 429,
    body: { status: 'error', message: 'upload store busy; retry later' },
    headers: { 'Retry-After': '1' },
  });
  const { promise } = startUpload(world, 'plugin', 1024);
  await settle();
  await assert.rejects(withDeadline(promise), { message: 'bad chunk' });
  await runTimers(world);
  await settle();
  assert.equal(world.state.warnings.length, 1);
  assert.match(world.state.warnings[0], /within 24 hours/);
});

test('reports monotonic progress and finishes at 100', async () => {
  const world = createWorld();
  for (let index = 0; index < 4; index += 1) {
    world.state.chunkPlans.push((xhr) => {
      xhr.progress(CHUNK_SIZE / 2, CHUNK_SIZE);
      xhr.respond(200, { status: 'success' });
    });
  }
  const progresses = [];
  const { promise } = startUpload(
    world,
    'theme',
    CHUNK_SIZE * 3 + CHUNK_SIZE / 2,
    (value) => progresses.push(value),
  );
  await settle();
  assert.equal((await withDeadline(promise)).installed, true);
  assert.equal(world.state.chunkRequests.length, 4);
  assert.equal(progresses[0], 0);
  assert.equal(progresses[progresses.length - 1], 100);
  assert.ok(progresses.includes(14), `expected an intermediate sample in ${progresses}`);
  for (let index = 1; index < progresses.length; index += 1) {
    assert.ok(
      progresses[index] >= progresses[index - 1],
      `progress went backwards: ${progresses}`,
    );
  }
});
