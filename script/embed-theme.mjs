/**
 * Check the theme embedded in the panel binary, and explain how to regenerate it.
 *
 * ## Why the archive is committed rather than built in CI
 *
 * `web/public/defaultTheme/dist.tar.zst` is the theme's build output, and the theme is a **separate
 * repository** (https://github.com/shanyang242/Komari-Theme-LuminaPlus). This repository does not
 * contain its sources, so CI cannot build it; it can only check what is committed. That is the honest
 * arrangement, and it is worth stating because the previous one was not: CI and the release workflow
 * each rebuilt the archive inline from `frontend/dist` and `frontend/komari-theme.json` — the panel's
 * own UI, described by `komari-web`'s manifest — so the committed archive never survived a build, and
 * a test asserting the embedded theme's identity passed locally and failed in CI.
 *
 * ## What goes in, and why from two places
 *
 *   - **the theme**, from `theme-luminaplus/dist` — a checkout of the theme's repository.
 *   - **the panel's standalone pages**, from `frontend/dist/standalone` — panel features that happen to
 *     be served from the same archive. Losing them is a 404 on a working page, and that happened once
 *     when the archive was replaced with a theme build alone.
 *
 * The archive holds the *contents* of a `dist` directory with that prefix stripped: the embedded theme
 * is served from the archive root, while an installed theme is served from `<short>/dist` (see
 * `DistDir` in `web/public/public.go`). Packing `dist/` itself serves a theme whose every asset is one
 * directory deeper than the HTML expects, which is a blank page and no error in the log.
 *
 * ## Regenerating
 *
 *     git clone https://github.com/shanyang242/Komari-Theme-LuminaPlus theme-luminaplus
 *     cd theme-luminaplus && npm install && npm run build && cd ..
 *     cd frontend && npm install && npm run build && cd ..
 *     node script/embed-theme.mjs --write
 *
 * Then commit `web/public/defaultTheme/dist.tar.zst`. `docs/THIRD-PARTY-LICENSES.md` records the
 * theme version and the licence obligation that comes with shipping it.
 *
 * Without `--write` this only checks: the archive is present, the manifest is the theme's and carries
 * its licence notice, and the panel's own pages are inside. CI runs it that way.
 */
import { readdir, readFile, rm, stat, mkdir, cp, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";
import path from "node:path";

const root = process.cwd();
const target = path.join(root, "web", "public", "defaultTheme");
const manifestPath = path.join(target, "komari-theme.json");
const archivePath = path.join(target, "dist.tar.zst");
const themeDist = path.join(root, "theme-luminaplus", "dist");
const standalone = path.join(root, "frontend", "dist", "standalone");
const stage = path.join(root, ".runtest", "embedded-theme");

const write = process.argv.includes("--write");

const problems = [];
function check(condition, message) {
  if (!condition) problems.push(message);
  return condition;
}

/** readArchive lists the archive's entries, using the Go packer's counterpart. */
function listArchive() {
  // Through the same tar+zstd stack the server decodes with, so this check cannot pass on an archive
  // the server would refuse.
  const script = [
    "import tarfile,sys",
    "t=tarfile.open(sys.argv[1],'r:zst')",
    "print('\\n'.join(t.getnames()))",
  ].join("\n");

  // Each candidate is tried, and a run that produced no output at all is treated as a failure rather
  // than as an empty archive. Getting that wrong made this check report eight missing entries when the
  // real problem was that `python` does not exist on Windows -- a checker that cannot tell "the tool did
  // not run" from "the thing is absent" reports the wrong fault, which is the failure mode the rest of
  // this file exists to avoid.
  const failures = [];
  for (const candidate of ["python3", "python", "py"]) {
    const result = spawnSync(candidate, ["-c", script, archivePath], { encoding: "utf8" });
    if (result.error) {
      failures.push(`${candidate}: ${result.error.code || result.error.message}`);
      continue;
    }
    if (result.status !== 0) {
      failures.push(`${candidate}: ${(result.stderr || "").trim().split("\n").slice(-1)[0] || `exit ${result.status}`}`);
      continue;
    }
    const names = result.stdout.split("\n").map((n) => n.trim()).filter(Boolean);
    if (names.length === 0) {
      failures.push(`${candidate}: read no entries`);
      continue;
    }
    return { names };
  }
  return { error: `could not read the archive with any of python3/python/py (${failures.join("; ")})` };
}

async function verify() {
  if (!check(existsSync(archivePath), `no archive at ${path.relative(root, archivePath)}; see the header for how to regenerate it`)) {
    return;
  }
  if (!check(existsSync(manifestPath), `no manifest at ${path.relative(root, manifestPath)}`)) {
    return;
  }

  // The manifest is inside the binary and carries the theme's licence notice, which the MIT licence
  // requires to travel with the software. Both halves are checked because either alone can rot.
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  check(manifest.short === "default", `the manifest's short is ${JSON.stringify(manifest.short)}, and public.go resolves the embedded theme by it`);
  check(manifest.license, "the manifest states no licence, so the MIT notice would not travel with the binary");
  check(
    typeof manifest.copyright === "string" && /shanyang/i.test(manifest.copyright),
    `the manifest carries no copyright notice for the theme's author (got ${JSON.stringify(manifest.copyright)})`,
  );
  check(
    manifest.name !== "Komari" && manifest.author !== "Akizon77",
    "the manifest still describes the previous default theme (komari-web)",
  );

  const read = listArchive();
  if (read.error) {
    problems.push(`the archive could not be read: ${read.error}`);
    return;
  }
  const names = new Set(read.names);
  check(names.has("index.html"), "the archive has no index.html at its root, so it was packed one directory too deep");
  check(!names.has("dist/index.html"), "the archive contains dist/index.html, so it was packed one directory too deep");
  check(names.has("komari-theme.json"), "the archive carries no manifest of its own");
  check(
    names.has(manifest.preview),
    `the manifest's preview ${JSON.stringify(manifest.preview)} is not in the archive`,
  );

  // The panel's own pages live here too. A theme build replacing the archive drops them, and the only
  // symptom is a 404 on a page that was working.
  for (const page of await readdir(standalone).catch(() => [])) {
    check(
      names.has(`standalone/${page}/${page}.html`),
      `the panel's ${page} page is missing from the archive: it must be packed alongside the theme, not replaced by it`,
    );
  }

  const size = (await stat(archivePath)).size;
  check(size > 1 << 20, `the archive is ${size} bytes, too small to hold a theme`);
  check(size < 40 << 20, `the archive is ${size} bytes, too large to embed in a binary`);
}

async function regenerate() {
  if (!check(existsSync(themeDist), `no theme build at ${path.relative(root, themeDist)}; clone and build the theme first (see the header)`)) {
    return;
  }
  if (!check(existsSync(standalone), `no standalone pages at ${path.relative(root, standalone)}; build the panel's frontend first`)) {
    return;
  }

  const manifestText = await readFile(manifestPath, "utf8");
  await rm(stage, { recursive: true, force: true });
  await mkdir(stage, { recursive: true });
  await cp(themeDist, stage, { recursive: true });
  await cp(standalone, path.join(stage, "standalone"), { recursive: true });
  await writeFile(path.join(stage, "komari-theme.json"), manifestText);

  const packed = spawnSync(
    path.join(root, "tools", "zstdpack", process.platform === "win32" ? "zstdpack.exe" : "zstdpack"),
    ["-src", stage, "-out", archivePath],
    { stdio: "inherit" },
  );
  await rm(stage, { recursive: true, force: true });
  if (!check(packed.status === 0, `zstdpack failed with status ${packed.status}`)) return;

  console.log(`embed-theme: wrote ${path.relative(root, archivePath)} (${((await stat(archivePath)).size / (1 << 20)).toFixed(2)} MiB)`);
}

if (write) {
  await regenerate();
}
await verify();

if (problems.length > 0) {
  console.error("embed-theme: the embedded theme has problems:");
  for (const problem of problems) console.error(`  - ${problem}`);
  process.exit(1);
}
console.log("embed-theme: the embedded archive holds the theme, its manifest and the panel's own pages");
