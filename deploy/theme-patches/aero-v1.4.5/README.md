# Aero 1.4.5 node-card focus candidate

The locked third-party market ZIP has SHA-256
`fd27cf3e0dae2bad8d58a31a3ed303a101816cc43626a7b9a0a1c6fb19758233`.
Its [upstream project](https://github.com/biliblihuorong/komari-theme-aero)
retains its own license and attribution. This is a local compatibility candidate,
not an official release or a replacement for the locked archive.

The theme opens a node preview on any focus event. In isolated Chromium mobile
emulation, tapping the card focuses it and the preview can intercept activation.
The builder changes the focus handler to show the preview only for
`:focus-visible`; the existing pointer-hover handler is unchanged. A pointer
focus that the browser does not consider keyboard-visible no longer opens that
preview. Behavior depends on the browser's `:focus-visible` heuristics.

```sh
python3 deploy/theme-patches/aero-v1.4.5/build.py original-aero.zip local-aero.zip
python3 deploy/theme-patches/aero-v1.4.5/check.py \
  --panel-binary /path/to/nekomari --archive local-aero.zip --output /tmp/aero-check.json
```

The builder accepts only the pinned original, checks the exact handler, refuses
an existing output and changes only the script in the ZIP. One build produced
SHA-256 `ef0eec1dd4659a302fa1ca9e56c6f42337beffac4ba24ae0da60ced484a5645e`;
modified input was rejected. The checker starts an isolated Panel, uploads the
ZIP using the real upload API and creates one synthetic node. In one run the
original failed touch at 320, 390 and 560 CSS pixels but passed keyboard; the
candidate passed all six and showed the keyboard tooltip. A separate 1280-pixel
candidate check retained the desktop hover tooltip and allowed moving the
pointer onto it. See `docs/V1.8.1-THEME-SMOKE.md` for conditions and limits.

This is a bounded Chromium check. It does not establish real-device behavior,
all input methods or theme features. It has not been installed in production;
online reinstall can overwrite it. The original archive still fails the
observed touch check and the 64-theme release gate remains open.
