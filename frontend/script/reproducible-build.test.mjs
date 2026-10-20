import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The 2026-10-20 regression: __BUILD_TIME__ came from new Date(), and because the
// define is inlined into the bundle every build produced different chunk hashes.
// These are source-level contracts; the byte-for-byte check is the two-build
// comparison in CI and in docs/DEPLOY-OC424.md.
const vite = readFileSync(new URL("../vite.config.ts", import.meta.url), "utf8");
const stamp = readFileSync(new URL("../build-time.txt", import.meta.url), "utf8").trim();
const attributes = readFileSync(
  new URL("../../.gitattributes", import.meta.url),
  "utf8",
);

test("__BUILD_TIME__ is resolved deterministically, not read from the wall clock", () => {
  assert.match(vite, /VITE_BUILD_TIME/, "an explicit override must be honoured");
  assert.match(vite, /SOURCE_DATE_EPOCH/, "reproducible-build tooling uses SOURCE_DATE_EPOCH");
  assert.match(vite, /build-time\.txt/, "a git-archive stamped time must be read");
  assert.doesNotMatch(
    vite,
    /const buildTime = new Date\(\)\.toISOString\(\)/,
    "the old wall-clock assignment must not come back",
  );
});

test("a source tarball carries its commit time through export-subst", () => {
  // In a checkout the file is still the literal placeholder; git archive substitutes
  // it with the commit time. Both are valid states of the same source, and a test
  // that only accepts one of them fails on the other (caught by the BERO tarball run).
  assert.match(
    stamp,
    /^(\$Format:%cI\$|\d{4}-\d{2}-\d{2}T\S+)$/,
    "build-time.txt is the export-subst placeholder or its substituted commit time",
  );
  assert.match(
    attributes,
    /^frontend\/build-time\.txt export-subst$/m,
    ".gitattributes must substitute the placeholder in git archive output",
  );
});
