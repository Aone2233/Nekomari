# LuminaCustom 3.1.2 mobile navigation diagnostic

Upstream: <https://github.com/bbingz/Komari-Theme-LuminaCustom>, tag `v3.1.2`,
commit `3bf07dbf2ce27760a54b5496f1e926a170e151d9`. The locked market ZIP
has SHA-256 `56784c204656a6310867ca6671f210dfcbaef935c1d41c537765fa882cbdd001` (verify it before applying anything).

With a synthetic node, the original theme's mobile connections link can miss
pointer/touch activation as its horizontal scroll-snap strip moves between
pointer-down and pointer-up. `mobile-tooltip.css` is a **diagnostic candidate**
for the existing archive: load it after the theme's own stylesheet. It hides
only the TOP 3 hover popup at widths up to 560px; scroll snapping and cards stay
in place. Desktop retains the popup. This intentionally removes the popup on
narrow screens, including narrow devices with a mouse. It is not an upstream
release, has not been applied to a production installation, and an online theme
reinstall may overwrite local modifications. It does not make the locked original
archive pass the acceptance gate.

In a disposable Panel, injecting the candidate into the original theme let
simulated touch and keyboard activation reach `/connections` at 320, 390 and
560 CSS pixels. Chromium CDP touch-drag moved the strip at those widths with
scroll snap still enabled; a desktop hover retained the popup and its link
worked. These are bounded controls, not real-device, assistive-technology,
all-browser or full theme acceptance. See `docs/V1.8.1-THEME-SMOKE.md`.
