import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

/**
 * `panel-smoke.spec.py`'s route coverage, pinned where CI can actually see it.
 *
 * The spec itself cannot run here: it drives a real instance through Playwright. But the part
 * of it that let a defect through is static, and that is exactly what is asserted below.
 *
 * F5/P2-1 is why this file exists. `/install` and `/database-recovery` are the panel's own
 * routes, and on a themed installation the server answered them with the *theme's* document —
 * a 404 page the theme rendered for paths its own router did not know. The sweep never noticed
 * because it built its route list out of `a[href]` read from the served document, and a theme
 * only ever links to its own paths; the panel's own routes are reachable only by name. Adding
 * them to a test that is always run by hand was not the missing piece — *defaulting* them, and
 * asserting whose document came back, is.
 *
 * So three properties are pinned: the panel's routes are visited with no environment variable
 * set, the markers they are checked against are the panel document's own, and failing that
 * check can fail the run.
 */
const source = readFileSync(new URL('./panel-smoke.spec.py', import.meta.url), 'utf8');

test('the panel-owned routes are visited by default, with no environment variable set', () => {
  const declaration = source.match(/^PANEL_OWNED_ROUTES = \(([^)]*)\)/m);
  assert.ok(declaration, 'PANEL_OWNED_ROUTES must be a module-level default');
  const routes = [...declaration[1].matchAll(/"([^"]+)"/g)].map((match) => match[1]);
  assert.deepEqual(
    routes,
    ['/install', '/database-recovery'],
    'the two routes the theme used to answer for',
  );

  // Declared is not visited: the default has to reach the set the sweep iterates.
  assert.match(
    source,
    /routes = sorted\([^\n]*PANEL_OWNED_ROUTES[^\n]*\)/,
    'the defaults must be merged into the route list the sweep walks',
  );

  // And it has to be independent of the operator-supplied list, which is parsed from the
  // environment and is empty when nothing sets it.
  assert.match(
    source,
    /os\.environ\.get\("PANEL_EXTRA_ROUTES", ""\)\.split\(","\)/,
    'PANEL_EXTRA_ROUTES must stay an additive, optional list',
  );
});

test("the panel-owned routes are checked against the panel's own document", () => {
  assert.match(source, /^PANEL_DOC_TITLE = "<title>Nekomari<\/title>"$/m);
  assert.match(source, /^PANEL_BUNDLE_MARKER = "\/admin\/assets\/"$/m);

  // Both markers, together: the embedded default theme is titled "Nekomari Monitor", so a
  // looser title check would call the theme's document the panel's.
  assert.ok(
    source.includes('if PANEL_BUNDLE_MARKER not in document:') &&
      source.includes('if PANEL_DOC_TITLE not in document:'),
    'the ownership check must require the panel bundle and the panel title',
  );

  // Read from what the server sent rather than from the hydrated DOM: which document was
  // served is a property of the response, and hydration may rewrite either marker.
  assert.ok(
    source.includes('panel_problem = panel_document_problem(served.text())'),
    'the check must run against the response body',
  );
  assert.ok(
    /served = page\.request\.get\(BASE \+ route/.test(source),
    "the document must be read with the session's own request client",
  );
});

test('a panel-owned route is never excused as a theme page, and failing the check fails the run', () => {
  assert.match(
    source,
    /if [^\n]*panel_problem[^\n]*:/,
    'the ownership result must be part of the failure condition',
  );
  assert.ok(
    source.includes('route not in PANEL_OWNED_ROUTES'),
    "the theme's minimal-page excuse must not apply to a route the panel owns",
  );
});
