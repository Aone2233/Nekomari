# Komari Next Pro v2.0.0 hydration patch

This is a local compatibility patch to the third-party online theme, **not** a
new theme. Upstream baseline:

- Repository: <https://github.com/fanchengliu/komari-next-pro>
- Tag: `v2.0.0`, commit `d2e2310` ("feat: add 2.0 built theme with mood/level system")
- Market archive SHA-256:
  `e557a5fe89eab5ec0b553f631511e499a01b611481e13771e8c5d3f8268b50ba`
- Manifest version stays `2.0.0`; this is a local bundle, not an official release.

## Observed triggers

Two independent defects were reproduced with the disposable panel:

1. **React #418 during hydration.** The market ZIP's static build is not a
   clean reproduction of the tagged source: serving its `dist` from a plain
   static server still produced #418 with no API at all. Rebuilding v2.0.0
   from source produced a clean static page. The clean rebuild still produced
   #418 when the panel answered APIs quickly; blocking any one API endpoint or
   delaying `/api/**` by 1.5 s removed it. Updates from data providers during
   hydration are a plausible explanation, rather than a proven cause. A
   separate diagnostic identified a concrete footer mismatch in the original
   market archive; see `docs/V1.8.1-THEME-SMOKE.md` for the controls and limits.
   Evidence:
   - Market ZIP: `pageerror: Minified React error #418` on public desktop and
     mobile; `/admin/` was unaffected.
   - Fresh source rebuild in the panel: same #418; API block/delay removed it.
   - Fresh source rebuild on a plain static server: no #418.
2. **Home-page 404.** A later browser control identified the observed
   request as `HEAD /`, which the Panel rejected; it did not establish an RSC
   prefetch as the cause. The v1.8.1 candidate answers that HEAD request,
   independently of the local theme patch. See `docs/V1.8.1-THEME-SMOKE.md`.

## Patch

- `theme/src/components/providers.tsx`: do not mount the provider tree until
  after the first client effect. The server and the first client render both
  return `null`, so no data update can race hydration.
- `theme/src/components/NavBar.tsx`: `<Link href="/" prefetch={false}>` was
  part of the original local patch; its effect on the observed HEAD request
  was not independently established.

## Build and release-artifact caveat

The upstream source does not contain the release ZIP's extra browser scripts
(`komari-mood-v21.js`, `komari-asset-fix.js`, the expiry-timeline inline script
and the `data-k` integrity scripts). Rebuilding from source alone drops those
features. The verified fixed artifact re-injects them from the original market
ZIP after `npm ci && npm run build`; that build passed the same gate with them
present.

A repeatable builder now checks the market archive SHA-256 and upstream tag
commit, applies `nekomari.patch`, runs `npm ci` and `npm run build` in a
temporary checkout, and copies the original archive's extra scripts back into
the rebuilt page. It requires Git, Node/npm and network access:

```sh
python3 deploy/theme-patches/komari-next-pro-v2.0.0/build.py original.zip local-next-pro.zip
```

The output is a local variant; build metadata can change the output hash. One
independent builder run produced
`e0de4b035c724009ef390c049871feb2203b67d12d64d2d8faec715d8f0c8424`.
A disposable Panel accepted that ZIP through its chunked upload and theme
switch APIs. With one synthetic node, desktop and mobile runs under 100 ms
emulated latency and 250,000 bytes/second throughput showed CPU, RAM and disk,
retained the three integrity scripts and attribution, switched table/grid and
opened the node detail without observed page exceptions. These are limited
Chromium checks, not all theme interactions or real-device acceptance. Neither
local build is the original market ZIP.

The fixed archive used for this verification is:

```text
/tmp/theme-fixed/komari-next-pro.zip
sha256 52286ce05f90dc6b8c07aca8bc94c6a9b70804811a1b73015210e573b81323f1
```

## Verification

Disposable panel v1.8.0/a92a242, no production data:

- Desktop `1280x900`, mobile `390x844`
- Routes `/` and authenticated `/admin/`
- Public desktop, public mobile, admin desktop, admin mobile: **all PASS**
- No pageerror and no local console error; hydration warning count zero
- External theme-owned calls are recorded separately as warnings

The `Providers` gate makes the server-rendered public document empty until the
client mounts. This is acceptable for this SPA theme but is a deliberate
trade-off: no-JS/SEO shell is lost for the public page. Do not call the patched
bundle an unmodified upstream release.
