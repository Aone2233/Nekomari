# Third-party licences

This file exists to satisfy a specific obligation, not as documentation for its own sake. It records
what a distributed Nekomari build contains and whose notice that requires. Part of it is now
**history** — until 2026-10-01 the embedded default theme was a third-party theme — and each such
section says so and says why it is still here.

## The embedded default theme: the panel's own front end

`web/public/defaultTheme/dist.tar.zst` is the theme the binary embeds (`//go:embed`, see
`web/public/public.go`). It is now **the panel's own front-end build**, assembled by
`script/embed-theme.mjs` from sources in this repository:

| In the archive | Built from |
|---|---|
| the panel document, its hashed assets and the PWA files (`sw.js`, `registerSW.js`, `workbox-*.js`, `manifest.json`, `manifest.webmanifest`) | `frontend/dist` |
| the admin interface subtree at `admin/` | `frontend/dist/standalone/admin` |
| the remaining standalone pages | `frontend/dist/standalone/<page>` |
| the theme manifest `komari-theme.json` | `frontend/komari-theme.json` (its `preview` is rewritten to a path inside the archive) |

Everything in it is this project's own code. `frontend/` is `komari-web`, the front end that has been
part of this project since it forked `komari-monitor/komari`, and it carries the same MIT notice as
the rest of the tree: [`../LICENSE`](../LICENSE), MIT, Copyright (c) 2025 Komari Moniter. So the
archive adds **no third-party notice obligation of its own**, and there is no longer a theme
repository behind it whose notice would have to travel with the copy.

One thing in the archive is not written in this file: the front end's **bundled npm dependencies**
(the UI framework, the charting library, the compiled CSS, the codicon font). Their licences are
recorded where they are resolved, `frontend/package-lock.json`, not here — and they are not new to a
distribution either, because the panel's own `admin/` bundle has been embedded in the binary all
along and shares them.

Two properties of this archive are easy to mistake for regressions, and both matter to how the panel
behaves (not to licensing, but they are recorded here because this file is where the archive is
described):

- `manifest: true` in `frontend/vite.config.ts` and `frontend/vite.admin.config.ts` makes the archive
  carry `.vite/manifest.json` for the front end and for the `admin/` subtree. That is how the server
  recognises content-hashed asset names and gives them
  `Cache-Control: public, max-age=31536000, immutable` (`isHashedAsset` in `web/public/public.go`).
  Without it the assets are still served correctly but lose the header, so behind Cloudflare every
  POP re-fetches files that never change.
- The archive holds the **contents** of `frontend/dist`, not `dist/` itself (`DistDir` handling in
  `web/public/public.go`), and the admin subtree is *copied* to `admin/` rather than moved, because
  the panel's interface has absolute routes and API paths. `script/embed-theme.mjs` refuses to pack
  when either is wrong: both produce a blank page and no log line.

## Komari-Theme-LuminaPlus: no longer embedded, still owed to the releases that carry it

Until 2026-10-01 the embedded default theme was the third-party theme
[Komari-Theme-LuminaPlus](https://github.com/shanyang242/Komari-Theme-LuminaPlus), MIT licensed. It
is **not embedded any more**: none of its source, assets or build output goes into the archive above
— `script/embed-theme.mjs` takes every input from `frontend/` — and the checkout it was built from
(`theme-luminaplus/`) is ignored by git (`.gitignore`: `/theme-luminaplus/`), so it is not part of
this repository, of the release assets, or of the container images either.

The MIT notice obligation this table was written for therefore does **not** attach to a current build.
It is kept here, and the licence is kept in the tree, because **releases up to and including v1.6.7
still embed that theme and are still downloadable**. Each of those binaries and images is a copy of
it, and MIT's one condition runs with the copies that exist, not with the newest one:

> The above copyright notice and this permission notice shall be included in all copies or
> substantial portions of the Software.

Deleting this section, or `docs/third-party/luminaplus-LICENSE`, would leave third-party code that
anyone can still download in an undocumented state.

| | |
|---|---|
| Project | [Komari-Theme-LuminaPlus](https://github.com/shanyang242/Komari-Theme-LuminaPlus) |
| Copyright | (c) 2026 Shanyang242 |
| Licence | MIT — [`third-party/luminaplus-LICENSE`](third-party/luminaplus-LICENSE) |
| Embedded in | v1.6.7 and every earlier release |
| Not embedded in | the current default archive (the panel's own front end, above) |

### What else that theme carried

Same reasoning, same releases. Its README credits two further projects, and the credit is reproduced
here for the same reason it was before:

- **[stqfdyr/komari-theme-Lumina](https://github.com/stqfdyr/komari-theme-Lumina)** — LuminaPlus is
  an enhanced branch of it.
- **[Montia37/komari-theme-purcarte](https://github.com/Montia37/komari-theme-purcarte)** — the
  design idea for the video background, **and the bundled test video**
  (`assets/LanternRivers_1080p15fps2Mbps3s.mp4`, 0.74 MB). That asset is redistributed inside the
  archives of v1.6.7 and earlier; nothing under `frontend/` contains it, so it is not in the current
  archive.

No other asset of that theme declared a licence of its own. Its Inter font files
(`assets/inter-*-wght-normal-*.woff2`) are SIL Open Font Licensed, which permits embedding and
redistribution — again only inside releases up to v1.6.7. The only font file in the current archive is
`assets/codicon-<hash>.ttf` (the same file is also at `admin/assets/`), from the panel's own build.

### Why the archive carries a manifest (the finding that section was written for)

Kept because it explains the `manifest: true` in the two Vite configs above, and because the
measurement is not recorded anywhere else. Measured 2026-09-30 on the embedded archive and on this
deployment's installed theme:

| | `Cache-Control` on a hashed asset |
|---|---|
| embedded archive, built with `manifest` enabled | `public, max-age=31536000, immutable` |
| the installed theme at the time (built without it) | *absent* |

So the theme served every hashed asset with no `Cache-Control` at all; behind Cloudflare that means
every POP re-fetches files that never change, on the provider's default (measured at four hours) —
on every panel that installed it, not only this one. The fix was to enable the manifest in the build
that produces the archive, which the panel's own configs now do for the front end and for the admin.
Two details that made this hard to see: the response headers had to be read with `GET`, not `HEAD`
(the asset routes answer `GET` only, so `curl -I` returns 404 and looks like missing assets), and a
missing manifest is a performance regression rather than a fault, so a Go test reports it instead of
failing on it.

## How the embedded archive is rebuilt

It is a build output, so it has to be regenerated whenever the front end changes:

```bash
cd frontend && npm install && npm run build && cd ..
node script/embed-theme.mjs
```

`build.sh` does both steps in order, and `.github/workflows/ci.yml` and `release.yml` run the script
before `go build`, so the shipped binary is always assembled from the current sources. Two facts
about that arrangement are worth keeping:

- **The archive stays committed** (`web/public/defaultTheme/dist.tar.zst` and, beside it, the
  manifest) even though CI rebuilds it. `//go:embed` needs both files to exist at build time, and
  producing `frontend/dist` needs a Node toolchain — without the committed copy, a plain Go checkout
  could not run `go build`, `go test` or `go vet` at all. `web/public/.gitignore` ignores
  `defaultTheme/*` with those two files excepted, and says why.
- **Rebuilding in CI is only safe because the inputs are in this repository.** Until 2026-10-01 they
  were not: the archive was a separate theme repository's build output, so a build here could not
  reproduce it and the committed copy could silently be a different theme than the one a build
  produced. Now CI and a local build assemble the same thing from the same files.

See `docs/RELEASING.md` for how this fits into the release pipeline.

## Nekomari itself

MIT, Copyright (c) 2025 Komari Moniter — see [`../LICENSE`](../LICENSE). Nekomari is a fork of
[komari-monitor/komari](https://github.com/komari-monitor/komari); that file is the notice covering
this tree, the panel's front end included.

The archive's manifest records where that front end came from — `frontend/komari-theme.json` credits
`Akizon77` and `komari-monitor/komari-web`. That code is the front end this repository has built and
shipped since the fork: before LuminaPlus was embedded, CI assembled the archive from `frontend/dist`
exactly as it does now (`web/public/.gitignore` records it). If that provenance is ever judged to
need a notice of its own, this file is where it belongs; nothing beyond `../LICENSE` is recorded for
it today, and no LuminaPlus code is distributed by a current build.
