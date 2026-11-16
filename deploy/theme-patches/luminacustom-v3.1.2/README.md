# LuminaCustom 3.1.2 mobile navigation diagnostic

Upstream: <https://github.com/bbingz/Komari-Theme-LuminaCustom>, tag `v3.1.2`,
commit `3bf07dbf2ce27760a54b5496f1e926a170e151d9`. The locked market ZIP
has SHA-256 `56784c204656a6310867ca6671f210dfcbaef935c1d41c537765fa882cbdd001` (verify it before applying anything).

With a synthetic node, the original theme's mobile connections link can miss
pointer/touch activation as its horizontal scroll-snap strip moves between
pointer-down and pointer-up. `mobile-tooltip.css` is a **local compatibility candidate**
for the existing archive. It hides
only the TOP 3 hover popup at widths up to 560px; scroll snapping and cards stay
in place. Desktop retains the popup. This intentionally removes the popup on
narrow screens, including narrow devices with a mouse. It is not an upstream
release, has not been applied to a production installation, and an online theme
reinstall may overwrite local modifications. It does not make the locked original
archive pass the acceptance gate.

Build a local ZIP from the hash-verified original, using a new output path:

```sh
python3 deploy/theme-patches/luminacustom-v3.1.2/build.py original.zip local-luminacustom.zip
```

The builder rejects other inputs and existing output paths; it preserves every
archive entry except appending this CSS to the pinned stylesheet. One build
produced SHA-256 `ebb1fe63f8d6f69539881dceed1784fc81777c37820d2fff3b8a6d03125d3cf5`.
A disposable Panel accepted that ZIP through its chunked upload and theme
switch APIs; simulated touch and CDP touch-drag worked at 320, 390 and 560 CSS
pixels with scroll snap enabled. A separate manual extraction of the same ZIP
passed keyboard activation at those sizes and desktop hover/link checks. These
are bounded controls, not real-device, assistive-technology, all-browser or
full theme acceptance. See `docs/V1.8.1-THEME-SMOKE.md`.

To repeat the focused check, install the repository's Playwright browser test
dependencies, then run against a disposable local Panel binary:

```sh
python3 deploy/theme-patches/luminacustom-v3.1.2/check.py \
  --panel-binary /path/to/nekomari --archive local-luminacustom.zip --output /tmp/lumina-check.json
```

The checker starts a temporary Panel, uploads the given ZIP, supplies a synthetic
node and checks touch and keyboard navigation at three widths. It blocks external
browser requests and exits nonzero on a failed check. It does not inspect the
complete 64-theme gate, swipe behavior, all page errors outside the observation
window, or real devices. Running it on the original ZIP is expected to expose
the observed failure, not to pass.
