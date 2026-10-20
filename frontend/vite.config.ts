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
import { execFileSync } from "child_process";
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

// __BUILD_TIME__ is embedded into the bundle, so it is part of every chunk's content hash.
// Reading the wall clock here made two builds of the same commit produce different asset
// names (and therefore different archives), which is what blocked comparing a rebuild with
// the release. Resolve it deterministically: an explicit override, then SOURCE_DATE_EPOCH,
// then the checkout's commit time, and only fall back to "now" when there is no git metadata.
function resolveBuildTime(): string {
  const override = process.env.VITE_BUILD_TIME;
  if (override) return override;
  const epoch = process.env.SOURCE_DATE_EPOCH;
  if (epoch && /^\d+$/.test(epoch)) {
    return new Date(Number(epoch) * 1000).toISOString();
  }
  // A source tarball has no .git; git archive substitutes this file via export-subst
  // (see .gitattributes). In a checkout the literal "$Format:%cI$" is not a date and is skipped.
  try {
    const stamped = fs.readFileSync(path.resolve(__dirname, "build-time.txt"), "utf8").trim();
    if (/^\d{4}-\d{2}-\d{2}T/.test(stamped)) return new Date(stamped).toISOString();
  } catch {
    // No stamped file (older tree or a checkout): fall through to git.
  }
  try {
    const committed = execFileSync("git", ["log", "-1", "--format=%cI"], {
      cwd: __dirname,
      encoding: "utf8",
    }).trim();
    if (committed) return new Date(committed).toISOString();
  } catch {
    // Tarball without .git: fall through to the wall clock rather than failing the build.
  }
  return new Date().toISOString();
}

export default defineConfig(({ mode }) => {
  const buildTime = resolveBuildTime();

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
        // "prompt", not "autoUpdate": an update that activates itself clears the old precache, and
        // an already-open tab then lazily imports a chunk that no longer exists (blank page). The
        // waiting worker is released by PWAUpdatePrompt's button, which reloads at the same time.
        registerType: "prompt",
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
            // 语言包是典型的「按需数据」：entry 只内联 en，其余 4 个由 i18n/config.ts 动态
            // import。不排除的话，SW 安装会把这 4 个 chunk 在后台整份下载一遍（约 100 KB
            // gzip），首访省下的传输量又被加回去。排除后：在线切换语言照常按需拉取（文件名
            // 带内容哈希，浏览器 HTTP 缓存长期有效）；离线且从未加载过该语言时切换会失败并
            // 保持当前语言（ensureLanguage 的 catch 分支），不会出现空白首帧。
            "**/chunk-zh_CN-*.js",
            "**/chunk-zh_TW-*.js",
            "**/chunk-ja_JP-*.js",
            "**/chunk-id_ID-*.js",
            // 国旗（258 个 svg）与 OS 图标（33 个：26 svg + 4 webp + 3 ico）按需加载。
            //
            // 实测（2026-10-13，对构建产物取 gzip）：国旗 653 KB 原始 / 约 273 KB gzip，
            // 图标 169 KB / 约 57 KB —— 合计约 330 KB gzip，而 service worker 每次安装
            // 与每次更新都会在后台把整个预缓存重新下载一遍。B1 那次测量量的是「首访传输
            // 480 KiB」，预缓存的内容是另一回事，所以这 330 KB 从未被定价。
            //
            // 它们仍然离线可用：下面的 runtimeCaching 用 CacheFirst 缓存真正请求过的那些，
            // 也就是用户实际看过的地区与系统。
            "**/assets/flags/*.svg",
            "**/assets/logo/*",
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
            {
              // 国旗与 OS 图标：不进预缓存（见上面的 globIgnores），但真正用到之后
              // 走缓存，所以离线时看过的节点仍然有图标。
              //
              // 排在 api.* 之后：workbox 取第一个匹配的规则，而这条只匹配同源资源，
              // 两条不会互相遮挡。
              urlPattern: /\/assets\/(flags|logo)\//i,
              handler: "CacheFirst",
              options: {
                cacheName: "asset-icons",
                expiration: {
                  maxEntries: 320,
                  maxAgeSeconds: 60 * 60 * 24 * 365,
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
        // The standalone SLA report is deliberately NOT an entry here: it is built by
        // vite.standalone.config.ts into its own directory, because a shared build splits
        // its dependencies into chunks inside the /assets/ namespace the installed theme
        // owns, and a name collision would resolve to the theme's file. See roadmap H0.
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
