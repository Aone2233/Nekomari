import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import test from 'node:test';

/**
 * The rules every standalone page shares, checked at the source level.
 *
 * These are the rules that were learned the hard way and that a new page is most likely to
 * break, because breaking them produces a page that works in development and is wrong in
 * production:
 *
 *   - **not a route in the built-in router** — an installed theme replaces that router, so a
 *     route added there exists in the image and in no browser. v0.1.31 shipped that way and
 *     the production panel answered its own 404 for a live, tested feature.
 *   - **its own build, its own output directory, one file** — a shared build puts the page's
 *     chunks in the `/assets/` namespace the theme owns, where a name collision resolves to
 *     the theme's file and the theme's `index.html` starts preloading chunks built for the UI
 *     it replaced.
 *   - **no service worker** — the main app's precache is the built-in shell, which a
 *     standalone page neither uses nor should join.
 *   - **nothing new in `/assets/`** — see the second point.
 *   - **no app router import** — a Router in a standalone entry compiles, runs, and is
 *     useless in the deployment that matters.
 *
 * The artefact-level half of this contract is in `web/public/standalone_test.go`, which
 * inspects the built, embedded files: the page is in the archive, references only its own
 * prefix, and its bundle contains React with no chunk imports. The two halves are split
 * because neither can see what the other can — this one has no build output, that one cannot
 * see the source.
 */

const PAGES = [
  { page: 'sla', entry: 'sla.html', tsx: 'src/entries/sla.tsx', component: 'src/pages/sla/index.tsx' },
  { page: 'bulk', entry: 'bulk.html', tsx: 'src/entries/bulk.tsx', component: 'src/pages/bulk/index.tsx' },
  { page: 'maintenance', entry: 'maintenance.html', tsx: 'src/entries/maintenance.tsx', component: 'src/pages/maintenance/index.tsx' },
  { page: 'config', entry: 'config.html', tsx: 'src/entries/config.tsx', component: 'src/pages/config/index.tsx' },
  { page: 'forecast', entry: 'forecast.html', tsx: 'src/entries/forecast.tsx', component: 'src/pages/forecast/index.tsx' },
  // The admin interface uses its own router on purpose: it is served from `/admin`, so its absolute routes
  // and API calls resolve without a basename, and `App` owns that router. `ownsItsOwnRouter` marks the one
  // page for which "does not use a router" is the wrong assertion — that rule exists because a *standalone
  // page* must not depend on the router a theme replaces, and the admin is not behind a theme at all.
  { page: 'admin', entry: 'admin.html', tsx: 'src/entries/admin.tsx', component: 'src/App.tsx', ownsItsOwnRouter: true },
];

const read = (relative) => readFileSync(new URL(`../${relative}`, import.meta.url), 'utf8');
const routes = read('src/routes.ts');

for (const { page, entry, tsx, component, ownsItsOwnRouter } of PAGES) {
  test(`${page}: the entry and its html agree`, () => {
    assert.ok(existsSync(new URL(`../${entry}`, import.meta.url)), `${entry} is missing`);
    const html = read(entry);
    const source = read(tsx);

    assert.match(html, /<div id="root">/, `${entry} must provide the mount point`);
    assert.match(html, new RegExp(`src="/src/entries/${page}\\.tsx"`), `${entry} must load its entry`);
    assert.match(source, /getElementById\(["']root["']\)/, `${tsx} must find #root`);
    assert.ok(existsSync(new URL(`../${component}`, import.meta.url)), `${component} is missing`);
  });

  test(`${page}: the entry renders without the app router`, () => {
    if (ownsItsOwnRouter) {
      // Asserted the other way round: this page *must* mount a router, because its own routes are absolute
      // and the server answers every path under its prefix with its document.
      assert.match(read(tsx), /BrowserRouter/, 'the admin serves its own routes and needs its own router');
      return;
    }
    const source = read(tsx);
    assert.doesNotMatch(source, /from ["']react-router/, 'must not use the app router');
    assert.doesNotMatch(source, /from ["'][^"']*\/routes["']/, 'must not import the route table');
    assert.doesNotMatch(source, /useRoutes/, 'must not resolve routes');
  });

  test(`${page}: it is not a route in the built-in router`, () => {
    if (ownsItsOwnRouter) {
      // The admin *is* the `/admin` route: it holds the whole router, which is why it is served from that
      // prefix and why the server answers every path under it with its document. The rule being checked
      // here is for standalone pages, which must not hide inside a router a theme replaces.
      assert.match(routes, /path: "\/admin"/, 'the admin holds the /admin route itself');
      return;
    }
    // If someone adds it back, this says why not, next to the reason.
    assert.doesNotMatch(
      routes,
      new RegExp(`pages/${page}`),
      `the ${page} page must not be a router entry: an installed theme replaces that router`,
    );
    assert.match(routes, /standalone entry/i, 'the reason must stay recorded in routes.ts');
  });

  test(`${page}: its build is isolated from the main one`, () => {
    if (ownsItsOwnRouter) {
      // `vite.admin.config.ts`, not a `vite.standalone.*` one, and it does not call the shared factory:
      // the admin is served from `/admin` rather than `/standalone/<page>/`, so `base` differs, and it is
      // allowed to code-split because it is a real application rather than one page.
      const config = read('vite.admin.config.ts');
      assert.match(config, /base: "\/admin\/"/, 'the admin is served from /admin');
      assert.match(config, /outDir: "dist-admin"/, 'its own output directory');
      assert.match(config, /copyPublicDir: false/, 'it does not need the flags and OS logos');
      assert.match(config, /manifest: true/, 'so its hashed assets get a long-lived Cache-Control');
      return;
    }
    assert.ok(
      existsSync(new URL(`../vite.standalone.${page}.config.ts`, import.meta.url)),
      `vite.standalone.${page}.config.ts is missing`,
    );
    assert.match(
      read(`vite.standalone.${page}.config.ts`),
      new RegExp(`standaloneConfig\\("${page}"\\)`),
      'the per-page config must call the shared factory',
    );

    const shared = read('vite.standalone.shared.ts');
    assert.match(shared, /dist-standalone/, 'its own output directory');
    assert.match(shared, /inlineDynamicImports:\s*true/, 'one file, no chunks to resolve');
    assert.match(shared, new RegExp(`base: \`/standalone/\\$\\{page\\}/\``), 'its own asset prefix');
    assert.match(shared, /copyPublicDir:\s*false/, 'it does not need the flags and OS logos');
    assert.doesNotMatch(shared, /VitePWA/, 'it must not register the panel service worker');
    assert.match(shared, /alias:/, 'the @ alias has to be repeated for a separate config');
  });
}

// The registry and the build script have to agree: a page in one and not the other builds
// nothing, and a page in the build script with no config fails by name at build time rather
// than silently shipping.
test('the build script lists exactly the pages that exist', () => {
  const script = read('script/build-standalone.mjs');
  for (const { page, entry, config } of [
    { page: 'sla', entry: 'sla.html', config: 'vite.standalone.sla.config.ts' },
    { page: 'bulk', entry: 'bulk.html', config: 'vite.standalone.bulk.config.ts' },
    { page: 'maintenance', entry: 'maintenance.html', config: 'vite.standalone.maintenance.config.ts' },
    { page: 'config', entry: 'config.html', config: 'vite.standalone.config.config.ts' },
    { page: 'forecast', entry: 'forecast.html', config: 'vite.standalone.forecast.config.ts' },
    // `index.html` is what ends up in the archive for the admin; the build emits `admin.html` and the script
    // renames it, which the `renameFrom` assertion below pins.
    { page: 'admin', entry: 'index.html', config: 'vite.admin.config.ts' },
  ]) {
    assert.match(script, new RegExp(`page: "${page}"`), `the build script must build ${page}`);
    assert.match(script, new RegExp(`entry: "${entry.replace('.', '\\.')}"`), `entry for ${page}`);
    assert.match(script, new RegExp(`config: "${config.replace(/\./g, '\\.')}"`), `config for ${page}`);
    // The admin additionally names its output directory, because it does not use the
    // `dist-standalone/<page>` default.
    if (page === 'admin') {
      assert.match(script, /outDir: "dist-admin"/, 'the admin names its own output directory');
      assert.match(script, /renameFrom: "admin.html"/, 'and its document is renamed to index.html');
    }
  }
});

test('nothing new lives in /assets/, which an installed theme owns', () => {
  for (const { entry, tsx } of PAGES) {
    for (const source of [read(entry), read(tsx)]) {
      assert.doesNotMatch(source, /["']\/assets\//, 'a standalone page must not reference /assets/');
    }
  }
});
