/**
 * Build the default-theme archive that is embedded in the panel binary.
 *
 * ## What goes in, and from where
 *
 *   - **the panel's own front end**, from `frontend/dist`. Its document, hashed assets and PWA
 *     files (`sw.js`, `registerSW.js`, `workbox-*.js`, `manifest.json`, `manifest.webmanifest`)
 *     are served from the archive root, so `/install`, `/database-recovery` and the public pages
 *     are the panel's own screens rather than whichever theme is installed.
 *   - **`frontend/dist/standalone/admin` also at `admin/`**. The panel's interface has absolute
 *     routes and API calls (`/admin/servers`, `/api/admin/...`) and the server serves this subtree
 *     for requests under `/admin`, so `admin/` is the location matching the URL the browser is on.
 *     It is copied rather than moved: the archive wants it at the root, and the standalone copy is
 *     what `/standalone/admin/...` resolves to.
 *   - **`frontend/dist/standalone/<page>`**, kept beside the rest because those pages are panel
 *     features served from this same archive.
 *   - **`frontend/komari-theme.json`** as the default theme's manifest.
 *
 * The path rule that is easiest to get backwards: an **installed** theme is served from
 * `data/theme/<short>/dist/`, while this archive holds the *contents* of a dist directory with
 * that prefix stripped (see `DistDir` in `web/public/public.go`). Packing `dist/` itself serves a
 * theme whose every asset is one directory deeper than the HTML expects — a blank page, and no
 * error anywhere in the log.
 *
 * ## Why the archive is committed *and* rebuilt in CI
 *
 * `//go:embed` (see `web/public/public.go`) needs both `defaultTheme/dist.tar.zst` and
 * `defaultTheme/komari-theme.json` to exist at build time, and producing `frontend/dist` needs a
 * Node toolchain. Without the committed copy, a Go-only checkout could not run `go build`,
 * `go test` or `go vet` at all. CI and the release workflow run this script before `go build`, so
 * the shipped binary is always built from the current sources; the committed copy is what makes a
 * plain `go build` work.
 *
 * That split is only safe because the inputs are in this repository. Until 2026-10-01 the archive
 * was the build output of a **separate theme repository**, so a build here could not reproduce it
 * and the committed copy could silently be a different theme than the one a build produced.
 *
 * ## Usage
 *
 *     cd frontend && npm install && npm run build && cd ..
 *     node script/embed-theme.mjs
 *
 * `build.sh` does both steps in order. After changing anything under `frontend/`, run them and
 * commit the regenerated `web/public/defaultTheme/dist.tar.zst` and `komari-theme.json`.
 */
import { readdir, readFile, rm, mkdir, cp, writeFile, stat } from "node:fs/promises";
import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";
import path from "node:path";

const root = process.cwd();
const target = path.join(root, "web", "public", "defaultTheme");
const archivePath = path.join(target, "dist.tar.zst");
const manifestOut = path.join(target, "komari-theme.json");
const panelDist = path.join(root, "frontend", "dist");
const adminSource = path.join(panelDist, "standalone", "admin");
const manifestSource = path.join(root, "frontend", "komari-theme.json");
const previewSources = ["preview.webp", "preview.png", "perview.png"];
const stage = path.join(root, ".runtest", "embedded-theme");

const problems = [];
function check(condition, message) {
  if (!condition) problems.push(message);
  return condition;
}

/** Every file under dir, as archive-relative paths with forward slashes. */
async function listFiles(dir, prefix = "") {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const name = prefix + entry.name;
    if (entry.isDirectory()) out.push(...(await listFiles(path.join(dir, entry.name), name + "/")));
    else if (entry.isFile()) out.push(name);
    else throw new Error(`unexpected non-regular entry: ${path.join(dir, entry.name)}`);
  }
  return out;
}

/**
 * The checks that used to run against the packed archive, run against the staged tree instead:
 * reading a tar.zst back needs a `tar` with zstd support, which a developer machine may not have,
 * and the Go tests already assert the structure of what ends up embedded.
 */
async function verifyStage(files) {
  const present = new Set(files);
  check(present.has("index.html"), "the panel's document (index.html) is not at the archive root");
  check(present.has("admin/index.html"), "the admin is not in the archive at admin/index.html: /admin would render nothing");
  check(files.some((name) => name.startsWith("admin/assets/")), "the admin document is in the archive but its assets are not");

  // Every standalone page the panel ships must be reachable beside the rest.
  const standaloneDir = path.join(panelDist, "standalone");
  for (const page of await readdir(standaloneDir).catch(() => [])) {
    if (page === "admin") continue;
    check(
      present.has(`standalone/${page}/${page}.html`),
      `the panel's ${page} page is missing from the archive (looked for standalone/${page}/${page}.html)`,
    );
  }

  // A document that references an asset the archive does not contain is a blank page with no error.
  const document = await readFile(path.join(stage, "index.html"), "utf8");
  const referenced = new Set();
  for (const match of document.matchAll(/(?:src|href)="([^"]+)"/g)) {
    const value = match[1];
    if (/^(https?:)?\/\//.test(value) || value.startsWith("data:")) continue;
    const relative = value.replace(/^\//, "").split(/[?#]/)[0];
    if (relative && !relative.endsWith("/")) referenced.add(relative);
  }
  for (const relative of referenced) {
    check(present.has(relative), `index.html references ${relative}, which is not in the archive`);
  }
}

async function build() {
  if (!existsSync(path.join(panelDist, "index.html"))) {
    console.error(
      `embed-theme: no panel build at ${path.relative(root, panelDist)}.\n` +
        `  Build it first:  cd frontend && npm install && npm run build\n` +
        `  (build.sh does this in order; the committed archive is what lets a Go-only checkout build.)`,
    );
    process.exit(1);
  }
  if (!existsSync(adminSource)) {
    console.error(
      `embed-theme: the admin build is missing at ${path.relative(root, adminSource)}.\n` +
        `  It is produced by frontend/script/build-standalone.mjs as part of \`npm run build\`.`,
    );
    process.exit(1);
  }

  await rm(stage, { recursive: true, force: true });
  await mkdir(stage, { recursive: true });
  await cp(panelDist, stage, { recursive: true });
  await cp(adminSource, path.join(stage, "admin"), { recursive: true });

  // The manifest describes a *theme*, whose asset paths are relative to its own `dist/`. The
  // embedded archive has no `dist/`, so `preview` has to be rewritten to the path it will actually
  // be served from — otherwise the theme card shows a broken image and nothing logs an error.
  const manifest = JSON.parse(await readFile(manifestSource, "utf8"));
  const preview = (manifest.preview ?? "").replace(/^dist\//, "");
  if (preview && existsSync(path.join(stage, preview))) {
    manifest.preview = preview;
  } else {
    let fallback = "";
    for (const candidate of previewSources) {
      if (existsSync(path.join(root, "frontend", candidate))) {
        await cp(path.join(root, "frontend", candidate), path.join(stage, candidate));
        fallback = candidate;
        break;
      }
    }
    if (preview) {
      console.warn(`embed-theme: manifest preview ${manifest.preview} is not in the archive; using ${fallback || "(none)"}`);
    }
    manifest.preview = fallback;
  }
  await writeFile(path.join(stage, "komari-theme.json"), JSON.stringify(manifest, null, 2) + "\n");

  await verifyStage(await listFiles(stage));
  if (problems.length > 0) {
    for (const problem of problems) console.error(`embed-theme: ${problem}`);
    await rm(stage, { recursive: true, force: true });
    process.exit(1);
  }

  // The manifest is embedded separately from the archive (`//go:embed` in public.go reads both),
  // so it is written next to it as well as inside it.
  await mkdir(target, { recursive: true });
  await cp(path.join(stage, "komari-theme.json"), manifestOut);

  const zstdpack = path.join(root, "tools", "zstdpack", process.platform === "win32" ? "zstdpack.exe" : "zstdpack");
  const packed = spawnSync(zstdpack, ["-src", stage, "-out", archivePath], { stdio: "inherit" });
  await rm(stage, { recursive: true, force: true });
  if (packed.status !== 0) {
    console.error(`embed-theme: zstdpack failed with status ${packed.status}`);
    process.exit(1);
  }

  const size = (await stat(archivePath)).size;
  console.log(
    `embed-theme: wrote ${path.relative(root, archivePath)} (${(size / (1 << 20)).toFixed(2)} MiB) ` +
      `and ${path.relative(root, manifestOut)}`,
  );
}

await build();
