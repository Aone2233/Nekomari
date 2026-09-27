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
import { cp, mkdir, readdir, rename, rm, stat, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// The script lives in frontend/script/; the builds it folds together are relative to
// frontend/ itself — `..` from here.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const site = path.join(root, "dist");

/**
 * The standalone pages: the directory name, the html entry, and the config that builds it.
 *
 * `admin` is the odd one and is listed last on purpose. It is not a standalone *page* — it is the panel's
 * own interface, built separately so that it does not depend on a theme providing it (roadmap H7). It
 * differs in three ways the loop below handles: its config is `vite.admin.config.ts` rather than a
 * `vite.standalone.*` one, its output is `dist-admin` rather than `dist-standalone/<page>`, and it is not
 * inlined into a single file because as a real application it needs code splitting.
 */
const PAGES = [
  { page: "sla", entry: "sla.html", config: "vite.standalone.sla.config.ts" },
  { page: "bulk", entry: "bulk.html", config: "vite.standalone.bulk.config.ts" },
  { page: "maintenance", entry: "maintenance.html", config: "vite.standalone.maintenance.config.ts" },
  { page: "config", entry: "config.html", config: "vite.standalone.config.config.ts" },
  { page: "forecast", entry: "forecast.html", config: "vite.standalone.forecast.config.ts" },
  { page: "admin", entry: "index.html", config: "vite.admin.config.ts", outDir: "dist-admin", renameFrom: "admin.html" },
];

async function buildOne(build, { page, entry, config, outDir, renameFrom }) {
  const configFile = path.join(root, config);
  if (!existsSync(configFile)) {
    throw new Error(`${page}: no config at ${config}`);
  }

  await build({ configFile });

  const built = path.join(root, outDir ?? path.join("dist-standalone", page));
  if (!existsSync(built)) {
    throw new Error(`${page}: the build produced nothing at ${built}`);
  }
  const produced = await readdir(built);
  // The entry is checked under the name the build produces, which is the name after `renameFrom` is applied
  // only for the admin. Checking `entry` directly would fail for exactly the page that needs the rename.
  const producedAs = renameFrom ?? entry;
  if (!produced.includes(producedAs)) {
    throw new Error(`${page}: ${built} has no ${producedAs} (found: ${produced.join(", ")})`);
  }

  const target = path.join(site, "standalone", page);
  await rm(target, { recursive: true, force: true });
  await mkdir(target, { recursive: true });
  await cp(built, target, { recursive: true });

  // The admin's document is called `index.html` in the output, not `admin.html`.
  //
  // Vite names an HTML entry after its input file and gives no way to override that, so the rename happens
  // here rather than in the config. It is worth doing because the archive's `admin/` subtree then has the
  // same shape as any other dist directory, which is what lets the server find it with the constant it
  // already has (`IndexFile`) instead of a second, admin-specific filename constant.
  if (renameFrom) {
    await rename(path.join(target, renameFrom), path.join(target, entry));
  }

  // Counted recursively, and the entry is checked to exist rather than only listed.
  //
  // The admin made this necessary: it is a real application with an `assets/` subtree of 240-odd files,
  // and the first version of this loop read only the top level — so it reported "3 files, 649 bytes" for
  // an 8 MB build and wrote a BUILD-INFO that was quietly wrong. A size that is off by four orders of
  // magnitude is worth noticing, but only if something reports it.
  const walk = async (dir) => {
    const out = [];
    for (const item of await readdir(dir, { withFileTypes: true })) {
      const full = path.join(dir, item.name);
      if (item.isDirectory()) {
        out.push(...(await walk(full)));
      } else {
        out.push(full);
      }
    }
    return out;
  };
  const files = await walk(target);
  let bytes = 0;
  for (const file of files) {
    bytes += (await stat(file)).size;
  }
  const relative = files.map((file) => path.relative(target, file).split(path.sep).join("/"));
  if (!relative.includes(entry)) {
    throw new Error(`${page}: ${entry} did not survive the copy into ${target}`);
  }
  await writeFile(
    path.join(target, "BUILD-INFO.txt"),
    `page: ${page}\nentry: ${entry}\nfiles: ${relative.length}\nbytes: ${bytes}\n`,
  );
  return { page, files: relative, bytes };
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
