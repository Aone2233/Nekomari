import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

/**
 * `panel-smoke.spec.py`'s route contracts, pinned where CI can actually see them.
 *
 * The spec itself cannot run here: it drives a real instance through Playwright. But the part of
 * it that was wrong is static, and that is what is asserted below.
 *
 * Two corrections are protected, both from the same mistake — asserting one document for two
 * routes the server treats differently:
 *
 *   * `/install` is a route of the panel's **front-end app** (`frontend/src/routes.ts`). It is
 *     answered from the *built-in default front end*, so the admin package's markers
 *     (`<title>Nekomari</title>`, `/admin/assets/`) are the wrong target; those belong to
 *     `/admin` and `/terminal`. What must hold is the exclusion: not the admin package, and not
 *     the installed theme's document.
 *   * `/database-recovery` is a **registered** route of the normal server and answers 307 to `/`
 *     (`internal/server/runtime.go`) — the recovery UI belongs to the temporary restricted
 *     listener. "It must serve a panel document" can never hold there.
 */
const source = readFileSync(new URL('./panel-smoke.spec.py', import.meta.url), 'utf8');

test('the routes a theme never links to are visited by default, with no environment variable set', () => {
  const declaration = source.match(/^PANEL_OWNED_ROUTES = \(([^)]*)\)/m);
  assert.ok(declaration, 'PANEL_OWNED_ROUTES must be a module-level default');
  const routes = [...declaration[1].matchAll(/"([^"]+)"/g)].map((match) => match[1]);
  assert.deepEqual(
    routes,
    ['/install', '/install/'],
    'the front-end route the theme used to answer for, in both spellings the prefix rule covers',
  );

  // Declared is not visited: both defaults have to reach the set the sweep iterates.
  assert.match(
    source,
    /routes = sorted\([^\n]*PANEL_OWNED_ROUTES[^\n]*REDIRECTED_ROUTES[^\n]*\)/,
    'the visited set must merge both default lists',
  );

  // And it has to be independent of the operator-supplied list, which is parsed from the
  // environment and is empty when nothing sets it.
  assert.match(
    source,
    /os\.environ\.get\("PANEL_EXTRA_ROUTES", ""\)\.split\(","\)/,
    'PANEL_EXTRA_ROUTES must stay an additive, optional list',
  );
});

test("/install is judged as a front-end route, never by the admin package's markers", () => {
  // The admin package is a different build and serves `/admin` and `/terminal`. Its title may be
  // *mentioned* (the comment above the constants explains which build is which) but must never be
  // a marker the check requires: that was the error. So no code line may carry it, and no
  // containment test may look for it.
  const codeLines = source.split('\n').filter((line) => !/^\s*#/.test(line));
  assert.ok(
    !codeLines.some((line) => line.includes('<title>Nekomari</title>')),
    "the admin package's title must never be asserted in code for a front-end route",
  );
  assert.ok(!source.includes('PANEL_DOC_TITLE'), 'the admin title constant must be gone');
  assert.match(source, /^ADMIN_BUNDLE_MARKER = "\/admin\/assets\/"$/m);
  assert.match(
    source,
    /if ADMIN_BUNDLE_MARKER in document:/,
    'the admin bundle must be a *negative* check on a panel-owned front-end path',
  );

  // "Not the installed theme's document" needs to know whether a theme is installed, and the
  // answer comes from the panel itself rather than from a guess at the markup: `/api/public`'s
  // `data.theme`, the field the front end reads. Equality with the document `/` serves is the
  // theme answering for the path; with no theme installed there is nothing to judge.
  assert.match(source, /^PUBLIC_SETTINGS_ENDPOINT = "\/api\/public"$/m);
  assert.match(source, /^DEFAULT_THEME = "default"$/m);
  assert.ok(
    source.includes('installed_theme == DEFAULT_THEME'),
    'an unthemed instance must skip the comparison instead of comparing two built-in documents',
  );
  assert.match(
    source,
    /if document == landing_document:/,
    "the theme's document is the one `/` serves; equality with it is the failure",
  );

  // Read from what the server sent rather than from the hydrated DOM: which document was served
  // is a property of the response, and the href harvest from the DOM is what missed this defect.
  assert.ok(
    source.includes('return response.text(), ""'),
    'the check must run against the response body',
  );
});

test('/database-recovery asserts the redirect the normal server actually performs', () => {
  assert.match(
    source,
    /^REDIRECTED_ROUTES = \{"\/database-recovery": "\/"\}$/m,
    'the registered route and its destination, exactly as runtime.go registers them',
  );
  assert.ok(
    !/^PANEL_OWNED_ROUTES = \([^)]*database-recovery/m.test(source),
    'it must not be in the list that is required to serve a panel document',
  );
  assert.match(
    source,
    /page\.request\.get\(BASE \+ route, max_redirects=0/,
    'the 307 has to be observed without following it',
  );
  assert.match(source, /hop\.status != 307/, 'the status is the assertion');
  assert.match(source, /hop\.headers\.get\("location"/, 'and so is the destination');
});

test('a failing contract fails the run, and a panel-owned route is never excused', () => {
  assert.match(
    source,
    /if [^\n]*contract_problem[^\n]*:/,
    'the contract result must be part of the failure condition',
  );
  assert.ok(
    source.includes('route not in PANEL_OWNED_ROUTES'),
    "the theme's minimal-page excuse must not apply to a route the panel owns",
  );
});
