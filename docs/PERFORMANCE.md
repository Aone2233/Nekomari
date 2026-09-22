# Panel load performance

What made the panel feel slow on 2026-09-21, how it was measured, and what to check
when it happens again. Two independent causes: one in the delivery path (not ours to
fix) and one in the page payload (ours, now fixed). The order below matters — check the
path before the payload, because a 0.5 MB file once took 109 s to fetch.

## Measure in this order

```bash
# 1. The panel itself. Milliseconds here means the panel is not the problem.
ssh OC424 'curl -s -o /dev/null -w "%{time_starttransfer}\n" http://127.0.0.1:25774/api/public'

# 2. Which Cloudflare POP served the request, and how long it took.
curl -s -D - -o /dev/null -w "%{time_starttransfer}\n" \
  https://komari.orderly2233.org/api/public | grep -iE 'cf-ray|^[0-9]'

# 3. Cache fill or cache hit on a static asset.
curl -s -D - -o /dev/null -w "%{size_download} %{time_total}\n" \
  https://komari.orderly2233.org/assets/<file> | grep -iE 'cf-cache-status|^[0-9]'
```

Reading them:

- **The POP suffix in `cf-ray`** is the whole story for an uncached request. Measured on
  one client within minutes: `HKG` 0.32 s, `FRA` 1.30 s, `SYD` 20.3 s. The origin is in
  Singapore, so a far POP is a long round trip before the request even starts.
- **`cf-cache-status`** separates "the CDN is slow" from "we are slow". `HIT` is served
  from the edge; `MISS`/`EXPIRED` goes to the origin and is where the multi-second
  stalls appear.
- A slow *container-local* number is the only one that implicates the panel.

## Finding 1 — the origin was healthy; the edge path was not

Measured while the panel felt slow:

| Where | Result |
|---|---|
| Container, `/api/public` and `/admin` | 1–2 ms |
| Host nginx (loopback, no Cloudflare) | 9 ms |
| Container CPU / memory | 0.13% / 94 MB of 1 GB |
| Accept queue, conntrack, retransmits | `recvq=0`, no listen overflows, 465/262144, 0 in a 10 s sample |
| Through Cloudflare, same URL, seconds apart | HKG 0.32 s, FRA 1.30 s, SYD 20.3 s |
| PZYC (China) / TG (Tokyo) | LHR & AMS 1.6–2.9 s / NRT 0.28 s |
| check-host nodes | Europe & North America 0.27–1.8 s; Vietnam 10.7 s; Iran 25 s |
| A new 0.5 MB background, first fetch | `MISS` — **108.9 s**; second fetch `HIT` — 0.5 s |

The last row is the clearest: the file size was irrelevant, the *cache fill* stalled.
Nothing on the panel can make a far POP close, so this is a Cloudflare-side or
topology-side item:

- a Cache Rule giving `/assets/*` a long Edge Cache TTL (fewer fills; Cloudflare's own
  4-hour browser TTL currently overrides the origin's `no-store`),
- Tiered Cache (free), or Argo Smart Routing (paid), to change how fills reach the origin,
- or not proxying the admin path at all: a DNS-only hostname straight to the origin needs
  a real certificate and exposes the origin IP, and it needs origin-side gzip, because
  through Cloudflare the compression is Cloudflare's (see Finding 2).

## Finding 2 — the dashboard shipped a 6.34 MB background (fixed)

The active theme is LuminaPlus, and its `backgroundImage` / `backgroundImageMobile`
settings pointed at four files installed from OpenList on 2026-09-18. Every visitor
downloads one of them on every cold load:

| File | Before | After | Gain |
|---|---|---|---|
| `bg-desktop-dark` (3840×2160) | 6.34 MB | 0.51 MB (2560×1440, JPEG q80) | 12.5× |
| `bg-mobile-light` (PNG photo) | 5.39 MB | 0.23 MB (1080×1578, JPEG q82) | 23.2× |
| `bg-mobile-dark` | 1.25 MB | 0.25 MB | 5.1× |
| `bg-desktop-light` | 1.13 MB | 0.17 MB | 6.8× |

Two details worth keeping:

- **New filenames, not overwrites.** The optimized files are `*.v2.jpg` and the settings
  were repointed. Cloudflare's own `max-age` (4 h) overrides the origin's `no-store`, so
  only a changed URL is guaranteed to be fetched fresh — the repository already learned
  this once, in the favicon comment in `web/public/public.go`.
- **The legacy names were shrunk in place too**, so a stale reference (a browser with the
  old settings cached, a cached HTML page) cannot pull 6 MB either. Full-quality
  originals are in `/opt/nekomari/data/theme-assets-backup-20260921-150521/` on the panel
  host; reverting is pointing the settings back at the legacy paths.

Cold-load payload afterwards, measured with a browser-like `Accept-Encoding`:

| Page | Payload |
|---|---|
| Public dashboard | 0.24 MB over 12 requests |
| Admin console (first screen) | 0.33 MB over 3 requests (entry 238 KB gz + CSS 93 KB gz) |

Text assets were already fine through Cloudflare: it compresses at the edge (CSS 172 KB →
27 KB, entry JS 186 KB → 57 KB). The origin's own `gzip_types` is commented out in
`/etc/nginx/nginx.conf`, which only matters if something stops going through Cloudflare.

## Finding 3 — theme settings need a restart

The panel serves `theme_configurations` from memory: an out-of-process `UPDATE` is
invisible to `/api/public` until `docker compose restart nekomari`. The elimination
(not dbcache, not managedconfig, not the wrong database file) and the `zzProbe` trick
that located it are in `deploy/set-theme-background-local.py`.

## Housekeeping

`data/backup` accumulates one 60–100 MB `upgrade-*.zip` per version change — 17 files and
1.4 GB in four days. `deploy/prune-upgrade-backups.sh` keeps the newest few and verifies
each survivor before deleting anything; `deploy/hosts/prune-upgrade-backups.{service,timer}`
run it weekly (installed on OC424 on 2026-09-21).
