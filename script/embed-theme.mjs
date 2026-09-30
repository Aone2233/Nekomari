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
 * To update only the panel pages while preserving the existing theme byte-for-byte:
 *
 *     cd frontend && npm run build && cd ..
 *     node script/embed-theme.mjs --refresh-panel
 *
 * This mode needs a tar executable with zstd support. It verifies the staged output
 * before replacing the archive, without requiring the theme's source checkout.
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
import { createHash } from "node:crypto";
import path from "node:path";

const root = process.cwd();
const target = path.join(root, "web", "public", "defaultTheme");
const manifestPath = path.join(target, "komari-theme.json");
const archivePath = path.join(target, "dist.tar.zst");
const themeDist = path.join(root, "theme-luminaplus", "dist");
const standalone = path.join(root, "frontend", "dist", "standalone");
const stage = path.join(root, ".runtest", "embedded-theme");

const write = process.argv.includes("--write");
const refreshPanel = process.argv.includes("--refresh-panel");

const problems = [];
function check(condition, message) {
  if (!condition) problems.push(message);
  return condition;
}

/**
 * listArchive lists the archive's entries.
 *
 * Two routes, because the reader has to work in three places with different Python versions:
 *
 *   1. `tarfile` reading the archive directly -- Python 3.14 and later, and a developer's machine may
 *      have no `zstd` CLI at all.
 *   2. `zstd -dc` piped into `tarfile` -- present on the CI runners (`zstd` is there, Python is 3.13
 *      which has no zstd support of its own) and in Git for Windows.
 *
 * Neither alone works everywhere, and choosing only the first is the mistake that was made here: it
 * works where it is written and fails on the runner with "unknown compression type 'zst'". A check that
 * passes where it is written and fails where it matters is worse than no check.
 */
function listArchive() {
  const failures = [];

  // Two routes, because the reader has to work in three places with different Python versions:
  //
  //   1. `zstd` piped into `tarfile` -- works on the CI runners (`zstd` present, Python 3.13 which has
  //      no zstd support of its own) and in Git for Windows.
  //   2. `tarfile` reading the archive directly -- only Python 3.14 and later, but that is a developer's
  //      machine, where `zstd` may not be on PATH at all.
  //
  // Choosing only the second works where it is written and fails on the runner with "unknown compression
  // type 'zst'"; choosing only the first fails on a machine without the CLI. The list is what makes this
  // independent of both.
  const directPython = [
    "import sys, tarfile",
    "t = tarfile.open(sys.argv[1], 'r:zst')",
    "print('\\n'.join(t.getnames()))",
  ].join("\n");

  for (const python of ["python3", "python", "py"]) {
    const result = spawnSync(python, ["-c", directPython, archivePath], { encoding: "utf8" });
    if (result.status !== 0 || !result.stdout) {
      continue;
    }
    const names = result.stdout.split("\n").map((name) => name.trim()).filter(Boolean);
    if (names.length > 0) {
      return { names };
    }
  }
  failures.push("direct tarfile (needs Python 3.14+)");

  const listPython = [
    "import sys, tarfile",
    "t = tarfile.open(fileobj=sys.stdin.buffer, mode='r|')",
    "print('\\n'.join(m.name for m in t))",
  ].join("\n");
  const quotedPython = listPython.replace(/"/g, '\\"');

  for (const decompressor of ["zstd -dc", "zstdcat"]) {
    for (const python of ["python3", "python", "py"]) {
      const pipeline = `${decompressor} "${archivePath}" | ${python} -c "${quotedPython}"`;
      const result = spawnSync(pipeline, { shell: true, encoding: "utf8", maxBuffer: 64 << 20 });
      if (result.status !== 0 || !result.stdout) {
        const detail = (result.stderr || "").trim().split("\n").slice(-1)[0] || "no output";
        failures.push(`${decompressor} | ${python}: ${detail}`);
        continue;
      }
      const names = result.stdout.split("\n").map((name) => name.trim()).filter(Boolean);
      if (names.length === 0) {
        failures.push(`${decompressor} | ${python}: read no entries`);
        continue;
      }
      return { names };
    }
  }

  return {
    error:
      "could not list the archive. It needs either a Python with tarfile zstd support (3.14+) or " +
      `\`zstd\` on PATH. Tried: ${failures.join("; ")}`,
  };
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
    // `admin` appears at both locations by design: the archive root is what `/admin` serves, and the copy
    // beside the other pages keeps this check honest about what was built.
    const candidates =
      page === "admin"
        ? ["admin/index.html", "standalone/admin/index.html"]
        : [`standalone/${page}/${page}.html`];
    check(
      candidates.some((name) => names.has(name)),
      `the panel's ${page} page is missing from the archive (looked for ${candidates.join(" or ")}): it must be packed alongside the theme, not replaced by it`,
    );
  }

  // The admin's own assets must be reachable too, because a document whose script 404s is a blank page rather
  // than an error anyone can see.
  //
  // And its absence is a failure rather than something to note when the build output is sitting right there.
  // Regenerating the archive without a fresh `frontend/dist` produced a 1.89 MiB archive with no admin at all
  // — the size bound below accepted it, because 1.89 MiB is a plausible theme — and the only thing that
  // objected was a Go test about a different page. Comparing the archive against what was built is what
  // catches that, and the check above already knows whether the source exists.
  const adminSource = path.join(standalone, "admin");
  if (existsSync(adminSource)) {
    check(
      names.has("admin/index.html"),
      "the admin was built but is not in the archive at admin/index.html: the staging step did not run, or ran without it",
    );
    check(
      [...names].some((name) => name.startsWith("admin/assets/")),
      "the admin document is in the archive but its assets are not, so it would render nothing",
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

  // The panel's own interface also goes at the archive root as `admin/`, not under `standalone/`.
  //
  // Roadmap H7. Its routes and API calls are absolute (`/admin/servers`, `/api/admin/...`) and the server
  // serves this subtree for requests under `/admin`, so `admin/` is the location that matches the URL the
  // browser is on. Left at `standalone/admin/` the same bytes would be served from a different path, where
  // the app's own links would point outside the subtree it was loaded from.
  //
  // Copied rather than moved: the archive wants it at the root, and the verification above still looks for
  // it beside the other pages, so removing the source would make the two disagree.
  const adminDist = path.join(standalone, "admin");
  if (existsSync(adminDist)) {
    await cp(adminDist, path.join(stage, "admin"), { recursive: true });
  }
  // The theme's preview image, copied in beside the manifest.
  //
  // The embedded theme must carry one: the panel's theme list renders `preview`, and the first attempt
  // pointed that field at a file inside the theme's build (`assets/...`) which is not reachable from the
  // theme path — `/themes/<short>/assets/...` resolves inside the theme, not inside the admin. The result was
  // a 404 in the console on the theme page and a broken image. The source image sits above the theme's `dist`
  // and so is not in the archive otherwise.
  const previewSource = path.join(root, "theme-luminaplus", "preview.png");
  if (existsSync(previewSource)) {
    await cp(previewSource, path.join(stage, "preview.png"));
  }
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

async function fileHashes(dir, prefix = "", omitPanel = false) {
  const hashes = new Map();
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (!prefix && omitPanel && ["admin", "standalone"].includes(entry.name)) continue;
    const name = prefix + entry.name;
    const source = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      for (const [key, value] of await fileHashes(source, name + "/")) hashes.set(key, value);
    } else if (entry.isFile()) {
      hashes.set(name, createHash("sha256").update(await readFile(source)).digest("hex"));
    } else {
      throw new Error(`unexpected non-regular entry: ${source}`);
    }
  }
  return hashes;
}

function assertSameFiles(expected, actual, label) {
  if (expected.size !== actual.size || [...expected].some(([name, hash]) => actual.get(name) !== hash)) {
    throw new Error(`${label}: file contents differ`);
  }
}

function extractArchive(source, destination) {
  const result = spawnSync("tar", ["-xf", source, "-C", destination], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(`tar extraction failed: ${result.stderr || result.error}`);
}

async function refreshPanelPages() {
  if (!check(existsSync(path.join(standalone, "admin", "index.html")), "build the panel frontend before --refresh-panel")) return;
  const temporary = path.join(root, ".runtest", "refresh-panel");
  const unpacked = path.join(temporary, "old");
  const checked = path.join(temporary, "checked");
  const output = path.join(temporary, "dist.tar.zst");
  if (existsSync(temporary)) throw new Error(`remove or preserve the existing staging directory first: ${temporary}`);
  await mkdir(unpacked, { recursive: true });
  try {
    extractArchive(archivePath, unpacked);
    const themeBefore = await fileHashes(unpacked, "", true);
    await rm(path.join(unpacked, "admin"), { recursive: true, force: true });
    await rm(path.join(unpacked, "standalone"), { recursive: true, force: true });
    await cp(standalone, path.join(unpacked, "standalone"), { recursive: true });
    await cp(path.join(standalone, "admin"), path.join(unpacked, "admin"), { recursive: true });
    const packed = spawnSync("go", ["run", ".", "-src", unpacked, "-out", output], { cwd: path.join(root, "tools", "zstdpack"), stdio: "inherit" });
    if (packed.status !== 0) throw new Error(`zstdpack failed with status ${packed.status}`);
    await mkdir(checked);
    extractArchive(output, checked);
    assertSameFiles(themeBefore, await fileHashes(checked, "", true), "preserved theme");
    assertSameFiles(await fileHashes(standalone), await fileHashes(path.join(checked, "standalone")), "standalone pages");
    assertSameFiles(await fileHashes(path.join(standalone, "admin")), await fileHashes(path.join(checked, "admin")), "admin pages");
    await cp(output, archivePath);
    console.log(`embed-theme: refreshed panel pages; preserved ${themeBefore.size} theme files byte-for-byte`);
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}

if (write && refreshPanel) throw new Error("choose --write or --refresh-panel, not both");
if (write) {
  await regenerate();
}
if (refreshPanel) {
  await refreshPanelPages();
}
await verify();

if (problems.length > 0) {
  console.error("embed-theme: the embedded theme has problems:");
  for (const problem of problems) console.error(`  - ${problem}`);
  process.exit(1);
}
console.log("embed-theme: the embedded archive holds the theme, its manifest and the panel's own pages");
