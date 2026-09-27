/**
 * Shared Vite configuration for the panel's standalone pages (roadmap H0, H1, H2).
 *
 * **Why these pages are built separately from the main app.** The panel's UI can be replaced
 * wholesale by an installed theme: LuminaPlus ships its own compiled `index.html` and
 * `assets`, and `web/public/public.go` prefers the theme directory over the embedded bundle.
 * A route added to the built-in router therefore does not exist in a themed deployment —
 * v0.1.31 shipped exactly that way and the production panel answered its own 404 for a
 * feature whose server half was live and tested.
 *
 * **Why each page gets its own build rather than an entry in the main one.** A shared build
 * splits the page's dependencies into `chunk-*.js` files inside the `/assets/` namespace the
 * theme owns. A name collision there resolves to the theme's file, and the theme's
 * `index.html` would begin preloading chunks built for the UI it replaced — a failure that
 * looks like a broken theme, not a broken page. `inlineDynamicImports` puts everything in
 * one file per page, so there is nothing to collide with.
 *
 * **How this is wired.** Vite loads a *config file*, and a config file must default-export
 * the config object — so it cannot export a factory and be loaded directly (tried; Vite
 * answers "config must export or return an object"). Each page therefore has a three-line
 * config next to this one that calls the factory:
 *
 *     // vite.standalone.sla.config.ts
 *     export default standaloneConfig("sla");
 *
 * The factory is still worth having: `copyPublicDir: false`, `inlineDynamicImports`, the `@`
 * alias and the output naming are four things to forget per page, and the way you find out
 * is a page that renders unstyled or splits into chunks in production.
 *
 * Output goes to `dist-standalone/<page>/`, which `script/build-standalone.mjs` copies into
 * the main `dist/standalone/<page>/`. The prefixed directory is deliberate: a theme may
 * legally contain a file at any other path.
 */
import { defineConfig } from "vite";
import type { UserConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import * as path from "path";

/** The standalone pages, and the html entry each is built from. */
export const STANDALONE_PAGES = {
  sla: "sla.html",
  bulk: "bulk.html",
  maintenance: "maintenance.html",
} as const;

export type StandalonePage = keyof typeof STANDALONE_PAGES;

/** standaloneConfig returns the Vite config for one standalone page. */
export function standaloneConfig(page: StandalonePage): UserConfig {
  const entry = STANDALONE_PAGES[page];
  if (!entry) {
    throw new Error(
      `unknown standalone page ${JSON.stringify(page)}; known: ${Object.keys(STANDALONE_PAGES).join(", ")}`,
    );
  }
  return defineConfig(() => ({
    base: `/standalone/${page}/`,
    plugins: [react(), tailwindcss()],
    resolve: {
      // The `@` alias has to be repeated here: this is a separate config, and without it the
      // build fails on the first `@/...` import (i18n/config pulls one in immediately).
      alias: [{ find: "@", replacement: path.resolve(__dirname, "./src") }],
    },
    build: {
      outDir: `dist-standalone/${page}`,
      emptyOutDir: true,
      // The panel's `public/` is copied verbatim by default — several hundred country flags
      // and OS logos, ~1.5 MB, for pages that use none of them. Off, so each page's output
      // is only what that page needs. The main build still carries them, which is where the
      // rest of the UI reads them from.
      copyPublicDir: false,
      assetsInlineLimit: 0,
      rollupOptions: {
        input: path.resolve(__dirname, entry),
        output: {
          // One file, so there is nothing for a theme to shadow.
          inlineDynamicImports: true,
          entryFileNames: `${page}.js`,
          assetFileNames: `${page}.[ext]`,
        },
      },
    },
  }));
}
