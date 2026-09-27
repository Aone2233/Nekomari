# Third-party licences

This file exists to satisfy a specific obligation, not as documentation for its own sake.

## The embedded default theme: Komari-Theme-LuminaPlus

Nekomari embeds a default theme in its binary (`web/public/defaultTheme/dist.tar.zst`, packed from
the theme's build output). That theme is **MIT licensed**, and the MIT licence's one condition is:

> The above copyright notice and this permission notice shall be included in all copies or
> substantial portions of the Software.

A compiled binary that serves the theme *is* such a copy, so the notice has to travel with it. The
theme's own `LICENSE` is at [`third-party/luminaplus-LICENSE`](third-party/luminaplus-LICENSE), and
the notice is also carried in the theme's manifest — `web/public/defaultTheme/komari-theme.json`,
inside the embedded archive — so it is present even in a distribution that carries only the binary.

| | |
|---|---|
| Project | [Komari-Theme-LuminaPlus](https://github.com/shanyang242/Komari-Theme-LuminaPlus) |
| Copyright | (c) 2026 Shanyang242 |
| Licence | MIT |
| Embedded version | 1.3.5-nk1 (upstream 1.3.5 plus the fork's detail-page fix) |

The embedded build also enables `build.manifest` in the theme's Vite config, which upstream does
not: Komari uses `.vite/manifest.json` to identify content-hashed asset names and give them a
long-lived `Cache-Control` (see `isHashedAsset`). Without it the assets are still served correctly
but lose that header, so behind Cloudflare every POP re-fetches files that never change. A Go test
reports its absence rather than failing on it — it is a performance regression, not a fault.

### What else that theme carries

Its README credits two further projects, and the credit is reproduced here because the same
distribution argument applies:

- **[stqfdyr/komari-theme-Lumina](https://github.com/stqfdyr/komari-theme-Lumina)** — LuminaPlus is
  an enhanced branch of it.
- **[Montia37/komari-theme-purcarte](https://github.com/Montia37/komari-theme-purcarte)** — the
  design idea for the video background, **and the bundled test video**
  (`assets/LanternRivers_1080p15fps2Mbps3s.mp4`, 0.74 MB). That asset is redistributed inside the
  archive; if it is ever a problem, deleting it and the theme's video-background default is the fix.

No other bundled asset declares a licence of its own. The Inter font files under
`assets/inter-*-wght-normal-*.woff2` are served from the theme's build; Inter is SIL Open Font
Licensed, which permits embedding and redistribution.

## Nekomari itself

MIT, Copyright (c) 2025 Komari Moniter — see [`../LICENSE`](../LICENSE). Nekomari is a fork of
[komari-monitor/komari](https://github.com/komari-monitor/komari); the previous embedded default
theme (`Akizon77`, `komari-monitor/komari-web`) was replaced by the theme above, so its own notice
is no longer required in the binary — it remains in the git history for anyone checking what was
distributed before.

## How the embedded archive is rebuilt

Because the archive is a build output, it has to be regenerated whenever the theme changes. On
Windows that is manual (the official CI uses `tar` + `zstd`):

```powershell
# 1. build the theme
cd theme-luminaplus; npm run build; cd ..

# 2. stage its dist at the archive root, plus the manifest for the embedded theme
$stage = ".runtest/embedded-theme"
Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $stage | Out-Null
Copy-Item -Recurse theme-luminaplus/dist/* $stage/
# komari-theme.json goes at the archive root alongside index.html and assets/

# 3. pack
tools/zstdpack/zstdpack.exe -src $stage -out web/public/defaultTheme/dist.tar.zst
```

Two details that are easy to get wrong and produce a blank panel rather than an error:

- the archive holds the **contents of `dist/`**, not `dist/` itself — the embedded theme has its
  `dist/` prefix stripped (`DistDir` handling in `web/public/public.go`), unlike an installed theme
  which is served from `data/theme/<short>/dist/`.
- `preview` in the embedded manifest must name a file that exists **inside the archive**. The
  upstream theme's `preview.png` sits above `dist/`, so it is not in the archive; the embedded
  manifest points at an asset that is.
