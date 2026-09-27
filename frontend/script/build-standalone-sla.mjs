/**
 * Build the standalone SLA report and fold it into the panel's dist.
 *
 * Two steps, run in this order by `npm run build`:
 *
 *   1. the main build writes `dist/` — the panel's own UI, which is what an unthemed
 *      deployment serves and what a theme replaces
 *   2. this script builds the SLA page on its own config and copies it to
 *      `dist/standalone/`
 *
 * A separate build rather than a second entry in the main one, for the reason
 * `vite.standalone.config.ts` states: shared chunks would land in the `/assets/`
 * namespace the theme owns, and a name collision would resolve to the theme's file.
 *
 * The copy is explicit and fails loudly. `dist/` is embedded into the Go binary by
 * `tools/zstdpack`, so a page that silently failed to land would ship as a URL that
 * 404s — which is the failure this whole detour exists to stop repeating.
 */
import { cp, mkdir, readdir, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// The script lives in frontend/script/, and the builds it folds together are relative to
// frontend/ itself — `..` from here. Taking the script's own directory as the root looks
// for dist-standalone inside script/, which is where it is not.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const built = path.join(root, "dist-standalone");
const target = path.join(root, "dist", "standalone");

/** main builds the standalone bundle, after the main build has written dist/. */
async function main() {
  if (!existsSync(built)) {
    throw new Error(
      `the standalone build produced nothing at ${built}; run \`vite build --config vite.standalone.config.ts\` first`,
    );
  }
  if (!existsSync(path.join(root, "dist", "index.html"))) {
    throw new Error(
      "dist/index.html is missing, so the main build has not run; the standalone page is folded into that output and cannot be copied into nothing",
    );
  }

  const produced = await readdir(built);
  const entry = produced.find((name) => name.endsWith(".html"));
  if (!entry) {
    throw new Error(`no html entry in ${built}: ${produced.join(", ")}`);
  }

  await rm(target, { recursive: true, force: true });
  await mkdir(target, { recursive: true });
  await cp(built, target, { recursive: true });

  // The Vite output entry is already named `sla.html` (the config's output names are
  // fixed), so the stable name is normally the file that is already there and a copy
  // would be source == destination. Keep the rename for the case where it is not.
  const stableName = "sla.html";
  const entryPath = path.join(target, entry);
  const stablePath = path.join(target, stableName);
  if (entryPath !== stablePath) {
    await cp(entryPath, stablePath);
  }

  const files = await readdir(target);
  const total = await Promise.all(
    files.map(async (name) => (await import("node:fs/promises")).stat(path.join(target, name))),
  );
  const bytes = total.reduce((sum, info) => sum + info.size, 0);
  await writeFile(
    path.join(target, "BUILD-INFO.txt"),
    `standalone SLA report\nentry: ${stableName}\nfiles: ${files.join(", ")}\nbytes: ${bytes}\n`,
  );

  console.log(`standalone: dist/standalone/${stableName} (${files.length} files, ${bytes} bytes)`);
}

await main();
