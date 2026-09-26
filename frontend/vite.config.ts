import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import Pages from "vite-plugin-pages";
import { visualizer } from "rollup-plugin-visualizer";
import { VitePWA } from "vite-plugin-pwa";

// https://vite.dev/config/
import type { Plugin, UserConfig } from "vite";
import * as fs from "fs";
import * as path from "path";
import dotenv from "dotenv";

function localKomariThemePlugin(): Plugin {
  const themeRequestPath = "/themes/default/komari-theme.json";
  const localThemeFile = path.resolve(__dirname, "komari-theme.json");

  return {
    name: "local-komari-theme",
    apply: "serve",
    enforce: "pre",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (!req.url) return next();

        const url = new URL(req.url, "http://localhost");
        if (!url.pathname.endsWith(themeRequestPath)) return next();

        fs.readFile(localThemeFile, (err, data) => {
          if (err) {
            res.statusCode = 404;
            res.setHeader("Content-Type", "application/json; charset=utf-8");
            res.end(
              JSON.stringify({
                error: "Local theme file not found",
                file: localThemeFile,
              })
            );
            return;
          }

          res.statusCode = 200;
          res.setHeader("Content-Type", "application/json; charset=utf-8");
          res.setHeader("Cache-Control", "no-store");
          res.end(data);
        });
      });
    },
  };
}

// The code editor is opened on demand (React.lazy in pages/terminal), and Monaco
// ships one lazily-loaded chunk per language definition. None of it belongs in the
// service-worker precache: it is ~3.4 MB a first visit should not have to download.
// See docs/OPTIMIZATION-REVIEW-2026-09-22.md (B2).
const monacoDefinitionsDir = path.resolve(
  __dirname,
  "node_modules/monaco-editor/esm/vs/languages/definitions",
);
// One chunk per definition directory, named after the directory (e.g. chunk-abap-<hash>.js).
const monacoLanguageChunkGlobs = fs.existsSync(monacoDefinitionsDir)
  ? fs
      .readdirSync(monacoDefinitionsDir, { withFileTypes: true })
      .filter((entry) => entry.isDirectory())
      .map((entry) => `**/chunk-${entry.name}-*.js`)
  : [];

export default defineConfig(({ mode }) => {
  const buildTime = new Date().toISOString();

  // Supports configuring BASE_URL via environment variables, defaulting to the root path.
  const base: string = process.env.VITE_BASE_URL ? process.env.VITE_BASE_URL : '/';
  const baseConfig: UserConfig = {
    base: base,
    plugins: [
      localKomariThemePlugin(),
      react(),
      tailwindcss(),
      Pages({
        dirs: "src/pages",
        extensions: ["tsx", "jsx"],
      }),
      VitePWA({
        registerType: "autoUpdate",
        includeAssets: ["favicon.ico", "assets/pwa-icon.webp"],
        manifest: {
          name: "Nekomari",
          short_name: "Nekomari",
          description: "Self-hosted server monitoring",
          theme_color: "#2563eb",
          background_color: "#ffffff",
          display: "standalone",
          scope: base,
          start_url: base,
          icons: [
            {
              src: `${base}assets/pwa-icon.webp`,
              sizes: "192x192",
              type: "image/webp",
              purpose: "maskable any",
            },
            {
              src: `${base}assets/pwa-icon.webp`,
              sizes: "512x512",
              type: "image/webp",
              purpose: "maskable any",
            },
          ],
        },
        workbox: {
          // HTML is rendered dynamically with theme, plugin, and site settings.
          // Cache only immutable assets so every navigation reaches the server.
          globPatterns: ["**/*.{js,css,ico,png,svg}"],
          // Keep the on-demand code editor out of the precache. The app shell, the
          // CSS and the icons are still precached; the editor chunks are fetched
          // normally when a session actually opens a file.
          globIgnores: [
            "**/chunk-FileEditorDialog-*.js",
            "**/FileEditorDialog-*.css",
            "**/editor.worker-*.js",
            ...monacoLanguageChunkGlobs,
          ],
          maximumFileSizeToCacheInBytes: 2 * 1024 * 1024,
          navigateFallback: null,
          runtimeCaching: [
            {
              // Third-party `api.*` hosts are cached with NetworkFirst, which is
              // what makes an offline reload answer from the cache instead of
              // failing.
              //
              // `api.github.com` is singled out because the admin bar's release
              // check cannot tell a cached answer from a fresh one: it records
              // "fetched at now" for whatever comes back, so a response replayed
              // from `api-cache` would look like a successful refresh and the
              // real six-hour TTL would never start. It is also the only
              // absolute `api.*` URL the panel itself fetches (everything else
              // is same-origin `/api/…`, and the notification endpoints are
              // called server-side), so excluding it costs nothing else.
              urlPattern: /^https:\/\/api\.(?!github\.com\/)/i,
              handler: "NetworkFirst",
              options: {
                cacheName: "api-cache",
                expiration: {
                  maxEntries: 10,
                  maxAgeSeconds: 60 * 60 * 24 * 365, // <== 365 days
                },
                cacheableResponse: {
                  statuses: [0, 200],
                },
              },
            },
          ],
        },
      }),
      visualizer({
        open: false,
        filename: "bundle-analysis.html",
        gzipSize: true,
        brotliSize: true,
      }),
    ],
    define: {
      __BUILD_TIME__: JSON.stringify(buildTime),
    },
      resolve: {
        alias: [
          { find: "@", replacement: path.resolve(__dirname, "./src") },
          {
            find: /^monaco-editor-codicon\.css$/,
            replacement: path.resolve(
              __dirname,
              "node_modules/monaco-editor/esm/vs/base/browser/ui/codicons/codicon/codicon.css",
            ),
          },
        // Force xterm to use the CJS build to avoid a rollup bug where `||=` in
        // xterm.mjs is incorrectly lowered to `void 0||(i={})` with an undeclared `i`,
        // causing `ReferenceError: i is not defined` at requestMode when vi sends DECRQM sequences.
        // Regex to match only the bare specifier, not subpaths like @xterm/xterm/css/xterm.css.
        { find: /^@xterm\/xterm$/, replacement: path.resolve(__dirname, "node_modules/@xterm/xterm/lib/xterm.js") },
      ],
    },
    build: {
      assetsDir: "assets",
      outDir: "dist",
      chunkSizeWarningLimit: 800,
      // 产出 .vite/manifest.json：面板据此【精确】知道哪些文件是内容哈希的，
      // 从而只对它们发长期缓存头（没有它时 Cloudflare 会套用自己 4 小时的默认值，
      // 每个 POP 每 4 小时都要回源重取一次这些永远不变的文件）。
      //
      // 为什么不能靠文件名猜：assets/ 下并非全都带哈希（实际产物里 pwa-icon.webp
      // 与 edit_117847723_p0.webp 就没有），而 Vite 的 base64url 哈希本身可以含 '-'
      // （index-Kbf1m-l1.js）。靠模式匹配会把 logo-v2Final1.png 这类普通文件名误判成
      // 哈希文件，代价是用户看到过期的资源。清单是构建自己写的，没有猜测。
      manifest: true,
      rollupOptions: {
        output: {
          // go embed ignore files start with '_'
          chunkFileNames: "assets/chunk-[name]-[hash].js",
          entryFileNames: "assets/entry-[name]-[hash].js",
          // Do not use manualChunks, use React.lazy() and <Suspense> instead
        }
      },
    },
  };

  if (mode === "development") {
    const envPath = path.resolve(process.cwd(), ".env.development");
    if (fs.existsSync(envPath)) {
      const envConfig = dotenv.parse(fs.readFileSync(envPath));
      for (const k in envConfig) {
        process.env[k] = envConfig[k];
      }
    }
    if (!process.env.VITE_API_TARGET) {
      process.env.VITE_API_TARGET = "http://127.0.0.1:25774";
    }
    baseConfig.server = {
      proxy: {
        "/api": {
          target: process.env.VITE_API_TARGET,
          changeOrigin: true,
          rewriteWsOrigin: true,
          ws: true,
        },
        "/themes": {
          target: process.env.VITE_API_TARGET,
          changeOrigin: true,
        },
      },
    };
  }

  return baseConfig;
});
