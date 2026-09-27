/**
 * Build every standalone page and fold the results into the panel's dist.
 *
 * `npm run build` runs the main build, then this. For each page it:
 *
 *   1. builds that page with its own config into `dist-standalone/<page>/`
 *   2. copies the result to `dist/standalone/<page>/`
 *   3. asserts the copy landed, because `dist/` is what `tools/zstdpack` embeds into the Go
 *      binary — a page that silently failed to copy ships as a URL that 404s, which is the
 *      failure this whole arrangement exists to stop repeating
 *
 * One script rather than one per page, and one cell per page in the list below rather than a
 * config file that has to be found by convention: adding a page is meant to require exactly
 * two edits (the list here and the config next to it), both of which are obvious when they
 * are missing because the build fails by name.
 *
 * Run from `frontend/`: `node script/build-standalone.mjs`.
 */
import { cp, mkdir, readdir, rm, stat, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// The script lives in frontend/script/; the builds it folds together are relative to
// frontend/ itself — `..` from here.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const site = path.join(root, "dist");

/** The standalone pages: the directory name, the html entry, and the config that builds it. */
const PAGES = [
  { page: "sla", entry: "sla.html", config: "vite.standalone.sla.config.ts" },
  { page: "bulk", entry: "bulk.html", config: "vite.standalone.bulk.config.ts" },
  { page: "maintenance", entry: "maintenance.html", config: "vite.standalone.maintenance.config.ts" },
  { page: "config", entry: "config.html", config: "vite.standalone.config.config.ts" },
  { page: "forecast", entry: "forecast.html", config: "vite.standalone.forecast.config.ts" },
];

async function buildOne(build, { page, entry, config }) {
  const configFile = path.join(root, config);
  if (!existsSync(configFile)) {
    throw new Error(`${page}: no config at ${config}`);
  }

  await build({ configFile });

  const built = path.join(root, "dist-standalone", page);
  if (!existsSync(built)) {
    throw new Error(`${page}: the build produced nothing at ${built}`);
  }
  const produced = await readdir(built);
  if (!produced.includes(entry)) {
    throw new Error(`${page}: ${built} has no ${entry} (found: ${produced.join(", ")})`);
  }

  const target = path.join(site, "standalone", page);
  await rm(target, { recursive: true, force: true });
  await mkdir(target, { recursive: true });
  await cp(built, target, { recursive: true });

  const files = await readdir(target);
  let bytes = 0;
  for (const name of files) {
    bytes += (await stat(path.join(target, name))).size;
  }
  await writeFile(
    path.join(target, "BUILD-INFO.txt"),
    `standalone page: ${page}\nentry: ${entry}\nfiles: ${files.join(", ")}\nbytes: ${bytes}\n`,
  );
  return { page, files, bytes };
}

async function main() {
  if (!existsSync(path.join(site, "index.html"))) {
    throw new Error(
      "dist/index.html is missing, so the main build has not run; the standalone pages are folded into that output and cannot be copied into nothing",
    );
  }

  const { build } = await import("vite");
  for (const spec of PAGES) {
    const { page, files, bytes } = await buildOne(build, spec);
    console.log(`standalone: dist/standalone/${page}/ (${files.length} files, ${bytes} bytes)`);
  }
}

await main();
