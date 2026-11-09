# Komari Next Pro v2.0.0 hydration patch

This is a local compatibility patch to the third-party online theme, **not** a
new theme. Upstream baseline:

- Repository: <https://github.com/fanchengliu/komari-next-pro>
- Tag: `v2.0.0`, commit `d2e2310` ("feat: add 2.0 built theme with mood/level system")
- Market archive SHA-256:
  `e557a5fe89eab5ec0b553f631511e499a01b611481e13771e8c5d3f8268b50ba`
- Manifest version stays `2.0.0`; this is a local bundle, not an official release.

## Root cause

Two independent defects were reproduced with the disposable panel:

1. **React #418 during hydration.** The data providers (`/api/public`,
   `/api/rpc2`, `/api/me`, ...) start fetching in effects immediately after the
   first commit. On this page the API answers fast enough that state updates
   can commit while React is still hydrating the large client tree, producing
   `Hydration failed because the server rendered HTML didn't match the client`.
   Evidence:
   - Original theme: `pageerror: Minified React error #418` on public desktop
     and mobile; `/admin/` was unaffected.
   - Blocking any one API endpoint or delaying `/api/**` by 1.5 s removed the
     #418.
   - The same built files served by a plain static server showed no #418 until
     an API/data update was introduced.
2. **`404 http://127.0.0.1:<port>/` console error.** Next.js `<Link href="/">`
   prefetches an RSC payload; the panel does not serve the Next RSC response for
   that request, so the prefetch is logged as a 404. It is local to the panel,
   not an external dependency.

## Patch

- `theme/src/components/providers.tsx`: do not mount the provider tree until
  after the first client effect. The server and the first client render both
  return `null`, so no data update can race hydration.
- `theme/src/components/NavBar.tsx`: `<Link href="/" prefetch={false}>` to
  avoid the unsupported RSC prefetch.

## Build and release-artifact caveat

The upstream source does not contain the release ZIP's extra browser scripts
(`komari-mood-v21.js`, `komari-asset-fix.js`, the expiry-timeline inline script
and the `data-k` integrity scripts). Rebuilding from source alone drops those
features. The verified fixed artifact re-injects them from the original market
ZIP after `npm ci && npm run build`; that build passed the same gate with them
present.

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
