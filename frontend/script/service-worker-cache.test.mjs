import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Read the real Vite config and call its exported factory, so the Workbox rule
// is asserted against the object the build actually uses rather than against a
// copy of the pattern. The config only touches the filesystem at import time
// (checking whether monaco's language definitions are installed), and nothing
// else it imports is reached before the factory returns.
const require = createRequire(import.meta.url);
const source = readFileSync(new URL('../vite.config.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2022,
    esModuleInterop: true,
  },
}).outputText;

const identity = (value) => value;
const stubs = {
  vite: { defineConfig: identity },
  '@vitejs/plugin-react': () => ({}),
  '@tailwindcss/vite': () => ({}),
  'vite-plugin-pages': () => ({}),
  'rollup-plugin-visualizer': { visualizer: () => ({}) },
  'vite-plugin-pwa': { VitePWA: (options) => options },
  dotenv: { parse: () => ({}), config: () => ({}) },
};

const exports = {};
vm.runInNewContext(compiled, {
  module: { exports },
  exports,
  require: (id) => {
    if (id in stubs) return stubs[id];
    return require(id);
  },
  process: { env: {} },
  console,
  __dirname: new URL('..', import.meta.url).pathname,
});

const config = exports.default({ mode: 'production' });

const workbox = config.plugins
  .map((plugin) => plugin?.workbox)
  .find((options) => options && Array.isArray(options.runtimeCaching));

const ruleFor = (url) => {
  const match = workbox.runtimeCaching.find((rule) => rule.urlPattern.test(url));
  return match ? { handler: match.handler, cacheName: match.options.cacheName } : null;
};

test('the GitHub release API is kept out of the service worker cache', () => {
  // The release feed cannot tell a replayed response from a fresh one, so a
  // cached answer would be stamped as a successful fetch and freeze the list.
  assert.equal(
    ruleFor('https://api.github.com/repos/Aone2233/Nekomari/releases?per_page=100'),
    null,
    'api.github.com must not match any runtime caching rule',
  );
  assert.equal(
    ruleFor('https://api.github.com/rate_limit'),
    null,
    'the whole host is excluded, not just the releases path',
  );
});

test('other third-party api hosts keep their existing cache rule', () => {
  // The exclusion is a lookahead on one host, not a removal of the rule: a
  // regex that stopped matching `api.` hosts entirely would silently drop the
  // offline behaviour this rule exists for.
  assert.deepEqual(ruleFor('https://api.telegram.org/bot123/sendMessage'), {
    handler: 'NetworkFirst',
    cacheName: 'api-cache',
  });
  assert.deepEqual(ruleFor('https://api.example.com/v1/status'), {
    handler: 'NetworkFirst',
    cacheName: 'api-cache',
  });
});

test('a host that merely starts with the excluded name is still cached', () => {
  // `github.com.` is a different host from `github.com/`, so the lookahead must
  // anchor on the path separator rather than the name alone.
  assert.deepEqual(ruleFor('https://api.github.com.evil.test/releases'), {
    handler: 'NetworkFirst',
    cacheName: 'api-cache',
  });
});

test('same-origin panel requests are never matched by the rule', () => {
  assert.equal(ruleFor('https://komari.example.org/api/public'), null);
  assert.equal(ruleFor('/api/public'), null);
});
