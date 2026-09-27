// The panel's own interface, served at `/admin/`. See src/entries/admin.tsx.
//
// Not a `standaloneConfig` page: those live at `/standalone/<page>/` behind a theme, whereas this is the
// panel's admin entry and has to be served from `/admin/` so that its absolute routes and API calls
// resolve without a basename. The rest of the rules — one bundle with no lazily-split chunks, its own
// output directory, `copyPublicDir` off — are the same ones, for the same reasons.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import * as path from "path";

export default defineConfig(() => ({
  base: "/admin/",
  plugins: [react(), tailwindcss()],
  resolve: {
    // Two aliases beyond `@`, copied from the main config because they work around build-tool bugs rather
    // than describing this project's layout. Missing them fails the build outright, which is how this list
    // was found: `monaco-editor-codicon.css` does not resolve as a bare specifier.
    alias: [
      { find: "@", replacement: path.resolve(__dirname, "./src") },
      {
        find: /^monaco-editor-codicon\.css$/,
        replacement: path.resolve(
          __dirname,
          "node_modules/monaco-editor/esm/vs/base/browser/ui/codicons/codicon/codicon.css",
        ),
      },
      // Force xterm to use the CJS build to avoid a rollup bug where `||=` in xterm.mjs is incorrectly
      // lowered to `void 0||(i={})` with an undeclared `i`, causing `ReferenceError: i is not defined` at
      // requestMode when vi sends DECRQM sequences. Regex so it matches only the bare specifier, not
      // subpaths like @xterm/xterm/css/xterm.css.
      { find: /^@xterm\/xterm$/, replacement: path.resolve(__dirname, "node_modules/@xterm/xterm/lib/xterm.js") },
    ],
  },
  build: {
    outDir: "dist-admin",
    emptyOutDir: true,
    copyPublicDir: false,
    assetsInlineLimit: 0,
    // Emit `.vite/manifest.json`, which is how the server knows which asset names carry a content hash and
    // can therefore be cached indefinitely. Without it every admin asset is served without a long-lived
    // Cache-Control — the same defect found in the theme, and worth not repeating here.
    manifest: true,
    // The admin is a real application with dozens of lazily imported pages, and it is served from its own
    // prefix rather than from `/assets/` where a theme could shadow a chunk. So unlike the five standalone
    // pages it is allowed to split: inlining every page into one file would be several megabytes fetched
    // before the login form appears.
    chunkSizeWarningLimit: 2048,
    rollupOptions: {
      input: path.resolve(__dirname, "admin.html"),
    },
  },
}));