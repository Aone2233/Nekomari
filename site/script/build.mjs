#!/usr/bin/env node
// Stage the documentation sources that mkdocs will build.
//
// ## Why staging exists
//
// Two kinds of page live in this site:
//
//   * pages written **for** it, under `site/content/`, which are the product documentation
//   * pages that already exist in the repository and are maintained there, such as the IP information API
//     reference and the third-party licence list
//
// The second kind must not be copied into the repository a second time — then there are two versions of the
// same document and one of them is always stale. They are copied **at build time** instead, into a
// `staging/` directory that is generated and ignored.
//
// Symlinks would avoid the copy and were the first attempt, but creating them needs administrator rights on
// Windows, so the build would work on the server and fail on the machine it is edited on. A build step that
// only works in the deployment environment is a build step nobody runs.
//
// ## The links inside reused pages
//
// A reused page is written for its position in `docs/`, so its relative links (`./OTHER.md`, `../x`) point at
// neighbours that are not in this site. Those are rewritten to absolute GitHub URLs, because the alternative
// — leaving them — produces links that 404 and nobody notices until a reader does.
//
// ## Usage
//
//     node site/script/build.mjs stage     # populate site/staging
//     node site/script/build.mjs check     # verify staging matches the configured navigation
import { existsSync } from "node:fs";
import { cp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const siteRoot = resolve(here, "..");
const repoRoot = resolve(siteRoot, "..");
const contentDir = join(siteRoot, "content");
const stagingDir = join(siteRoot, "staging");

// Pages maintained elsewhere in the repository and reused here.
//
// `from` is relative to the repository root, `to` relative to the staging root. Listed explicitly rather than
// globbed: this is the decision about what the public site contains, and it should be readable in one place.
const REUSED = [
  { from: "docs/IP-INFO-API.md", to: "reference/ip-info-api.md" },
  { from: "docs/THIRD-PARTY-LICENSES.md", to: "reference/third-party.md" },
];

// The repository the rewritten links point at, and the branch whose copy the reader lands on.
const REPO = "https://github.com/Aone2233/Nekomari";
const BRANCH = "main";

/** Rewrite links that point outside the site into absolute repository links.
 *
 * Only links to local files are touched: `http(s)://`, site-absolute (`/x`) and anchor-only (`#x`) links are
 * left alone, because rewriting those would break working links to fix nothing.
 */
function rewriteExternalLinks(markdown, fromPath) {
  return markdown.replace(/\]\((\.{1,2}\/[^)\s#]+)(#[^)\s]*)?\)/g, (whole, target, anchor = "") => {
    // Where the target actually lives, resolved from the file's original position in the repository.
    const absolute = resolve(repoRoot, dirname(fromPath), target);
    const repoRelative = relative(repoRoot, absolute).split("\\").join("/");
    if (repoRelative.startsWith("..")) {
      // Leaves the repository entirely — leave it as written rather than inventing a destination.
      return whole;
    }
    return `](${REPO}/blob/${BRANCH}/${repoRelative}${anchor})`;
  });
}

/** Copy the hand-written pages, so staging is generated from scratch every time.
 *
 * A stale staging directory is how a site silently ships a page that was deleted three commits ago.
 */
async function copyOwnContent() {
  const entries = await readdir(contentDir, { withFileTypes: true });
  for (const entry of entries) {
    const source = join(contentDir, entry.name);
    const destination = join(stagingDir, entry.name);
    await cp(source, destination, { recursive: true });
  }
  return entries.length;
}

async function stageReusedPages() {
  const staged = [];
  for (const page of REUSED) {
    const source = join(repoRoot, page.from);
    if (!existsSync(source)) {
      // Failing loudly: a renamed source document would otherwise make this site quietly drop a page, and the
      // navigation entry pointing at it would 404.
      throw new Error(`reused page is missing: ${page.from} (expected at ${source})`);
    }
    const markdown = await readFile(source, "utf8");
    const destination = join(stagingDir, page.to);
    await mkdir(dirname(destination), { recursive: true });
    await writeFile(destination, rewriteExternalLinks(markdown, page.from), "utf8");
    staged.push(`${page.from} -> ${page.to}`);
  }
  return staged;
}

/** Every page the navigation names must exist, or the build succeeds and the site has dead links. */
async function checkNavigation() {
  const config = await readFile(join(siteRoot, "mkdocs.yml"), "utf8");
  const referenced = [...config.matchAll(/^\s*-\s+[^:\n]+:\s*(\S+\.md)\s*$/gm)].map((m) => m[1]);
  const missing = referenced.filter((page) => !existsSync(join(stagingDir, page)));
  if (missing.length > 0) {
    throw new Error(
      `navigation references pages that do not exist in staging:\n  ${missing.join("\n  ")}\n` +
        `Staged pages are listed in site/script/build.mjs and site/content/.`,
    );
  }
  return referenced;
}

async function main() {
  const command = process.argv[2] || "stage";

  if (command === "stage") {
    await rm(stagingDir, { recursive: true, force: true });
    await mkdir(stagingDir, { recursive: true });
    const count = await copyOwnContent();
    const reused = await stageReusedPages();
    console.log(`staged ${count} own entries`);
    for (const line of reused) console.log(`  reused ${line}`);
    return;
  }

  if (command === "check") {
    const pages = await checkNavigation();
    console.log(`navigation: ${pages.length} pages, all present`);
    return;
  }

  console.error(`unknown command: ${command}\nusage: build.mjs [stage|check]`);
  process.exit(2);
}

main().catch((error) => {
  console.error(`\n${error.message}\n`);
  process.exit(1);
});
