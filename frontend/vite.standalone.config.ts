/**
 * The standalone SLA report's own Vite config (roadmap H1, decision in H0).
 *
 * **Why this is a second config and not an entry in the main one.** Serving the report
 * from `/sla.html` is only half the problem: with a shared build, Rollup splits the
 * page's dependencies into `chunk-*.js` files, and those land in the *same* `/assets/`
 * namespace the installed theme owns. `web/public/public.go` prefers the theme directory
 * over the embedded bundle, so a chunk whose name ever collided with a theme chunk would
 * silently resolve to the theme's file — and the theme's `index.html` would start
 * preloading chunks built for the built-in UI it replaced. Two builds, isolated outputs,
 * no shared names.
 *
 * **Why `inlineDynamicImports`.** The page has to be self-contained to survive a themed
 * deployment at all, so everything it needs is bundled into one file rather than split
 * across chunks resolved from a directory the theme controls.
 *
 * **Why no PWA plugin.** The main app registers a service worker whose precache is the
 * built-in shell. A report page must not join that: it would cache the shell it does not
 * use, and its own updates would be governed by a manifest it has nothing to do with.
 *
 * Output goes to `dist-standalone/`, which `script/build-standalone-sla.mjs` copies into
 * the main `dist/` under `standalone/`. The prefixed directory is deliberate: a theme may
 * legally contain a file at any other path, and `standalone/` is not a directory a
 * Komari theme uses.
 */
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import * as path from "path";

export default defineConfig(() => ({
  base: "/standalone/",
  plugins: [react(), tailwindcss()],
  resolve: {
    // The `@` alias has to be repeated here: this is a second config, and without it the
    // build fails on the first `@/...` import (i18n/config pulls one in immediately). The
    // main config carries the same alias plus a monaco-editor mapping the report does not
    // use, so only what the page needs is mirrored.
    alias: [{ find: "@", replacement: path.resolve(__dirname, "./src") }],
  },
  build: {
    outDir: "dist-standalone",
    emptyOutDir: true,
    // The panel's `public/` is copied verbatim by default — several hundred country flags
    // and OS logos, ~1.5 MB, for a report page that uses none of them. Off, so the output
    // is only what the page actually needs. The main build still carries them, which is
    // where the rest of the UI reads them from.
    copyPublicDir: false,
    // The whole point: one file, no chunks to resolve against the theme's assets.
    assetsInlineLimit: 0,
    rollupOptions: {
      input: path.resolve(__dirname, "sla.html"),
      output: {
        inlineDynamicImports: true,
        entryFileNames: "sla.js",
        assetFileNames: "sla.[ext]",
      },
    },
  },
}));
