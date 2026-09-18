# IP information API (`/api/*/ip-info/v1`)

A key-less, cache-backed IP information API added for third-party themes
(LuminaPlus) that render an "IP info" panel. Implemented in
`web/api/ipinfo`, registered in `web/router/router.go`.

| Method | Path | Auth |
|---|---|---|
| `GET` | `/api/public/ip-info/v1/status` | none |
| `GET` | `/api/public/ip-info/v1/lookup?uuid=<uuid>&ip=<ip>` | none |
| `GET` | `/api/public/ip-info/v1/latency?uuid=<uuid>&ip=<ip>` | none |
| `POST` | `/api/admin/ip-info/v1/refresh` | `RequireRole(admin)` |

Response envelope: `{"ok":true,"data":{…},"meta":{…}}` on success,
`{"error":{"message":"…"}}` with a non-2xx status on failure. This differs
from the RPC2 envelope (`status`/`message`/`data`) used elsewhere in the
server, so these routes are plain gin handlers rather than `jsonRpc.Bind`
targets. `uuid` is echoed back verbatim; the server does not resolve it.

`POST /refresh` takes `{"uuid","ip","force","include_latency"}`. `force`
defaults to **true** (the endpoint exists to refresh); pass `false` to allow a
cache hit. `include_latency: true` also re-runs the Globalping measurement, but
a latency failure never affects the returned lookup payload.

## Upstreams

All four are key-less. Each is individually failable: partial failure degrades
the payload, it never fails the whole response.

| Source | Used for | Notes |
|---|---|---|
| `ipwho.is` | location, ASN/ISP/org/domain | base source (`provider.base_source`) |
| `stat.ripe.net` | `network.route` (network-info), registered country + `network.rir` (whois), `network.domain` fallback (reverse-dns-ip) | whois runs after the ASN is known; reverse-dns only when ipwho.is has no domain |
| `ip-api.com` | boolean flags `hosting`/`mobile`/`proxy` | **HTTP only** (the free endpoint has no HTTPS) |
| `globalping.io` | global ping measurement (`/latency`) | best-effort; never hard-fails |

A lookup succeeds as soon as **one** of ipwho.is / RIPEstat / ip-api.com
answers. If all three fail: HTTP 502 with `error.message`, unless a retained
(stale) entry exists, in which case it is served with `meta.stale: true` and a
`meta.warning` explaining the degradation. `available_sources` /
`failed_sources` in `reputation` list which sources answered.

### Notes on the live upstream APIs (verified 2026-09)

These were confirmed by calling the real endpoints; three of them differ from
what the API documentation suggests, and the code carries comments for each:

* `network-info` returns `"asns":["15169"]` — **strings**, not numbers. A `[]int`
  field would fail to decode and silently drop `network.route` and the ASN
  fallback along with it, so `asnList` accepts both forms.
* `as-overview` (version 1.3) returns only `holder` / `block` / `announced` — it
  has **no country field**, so the registered country is read from `whois`
  instead (ARIN writes `"Country"`, APNIC writes `"country"`). RIPE-region ASNs
  carry no country in either call, so their classification falls back to
  `unknown`.
* Creating a Globalping measurement answers **HTTP 202 Accepted** with
  `{"id","probesCount"}` and no `status`, so success is any 2xx and the client
  polls until `status == "finished"`.
* `ip-api.com` limits the free endpoint to ~45 requests/minute and signals it
  with HTTP 429 **or** `X-Rl: 0` plus `X-Ttl`.

## Cache / TTL / rate limiting

| Item | TTL | After the fresh window |
|---|---|---|
| lookup (success) | 1 h | kept 24 h more and served as `meta.stale: true` if upstreams fail |
| lookup (all sources failed) | 5 min negative cache | dropped; repeated calls short-circuit without hitting upstreams |
| latency | 10 min | 24 h stale window |
| latency (failure) | 2 min negative cache | dropped |
| ip-api.com rate limit | `X-Ttl` seconds (default 60 s, capped at 1 h) | requests short-circuit to "unavailable" until the deadline passes |

`meta.cache` is `hit` when the payload came from the cache (including a stale
serve) and `miss` when upstreams were queried. `meta.updated_at` /
`expires_at` / `stale_until` are RFC3339. The cache is an in-process
mutex-guarded map with no background goroutine (see `cache.go` for why
`go-cache` was not reused).

The ip-api.com cooldown is triggered by HTTP 429 **or** by `X-Rl: 0` on an
otherwise successful response. In both cases the deadline is remembered, so
later lookups do not spend quota and do not add latency.

## Input validation

`ip` is required. Non-IP values, and any address that is not a public unicast
address, are rejected with HTTP 400 and a clear message — upstreams are never
queried for them. Rejected: RFC1918 private, loopback, link-local, multicast,
unspecified, CGNAT (`100.64.0.0/10`), IETF reserved (`192.0.0.0/24`),
TEST-NET 1/2/3, benchmarking (`198.18.0.0/15`), `240.0.0.0/4`, IPv6 ULA,
`100::/64` and `2001:db8::/32`. Both IPv4 and IPv6 are accepted;
IPv4-mapped IPv6 (`::ffff:1.2.3.4`) is reported as `family: 4`.

## Classification and reputation

`classification` compares the geolocated country code (ipwho.is) with the ASN's
registered country (RIPEstat `whois`; see the live-API notes above):

* both known and equal → `native` / `原生 IP`
* both known and different → `broadcast` / `广播 IP`
* either missing → `unknown` / `未知` (this is what RIPE-region ASNs get, since
  their whois records carry no country)

Reputation is derived only from ip-api.com's boolean flags. Risk weights sum to
100, so the maximum risk is exactly 100:

| signal | weight | source |
|---|---|---|
| `tor` | +30 | not reported by ip-api.com → always `false` |
| `abuser` | +30 | not reported → always `false` |
| `proxy` | +20 | `proxy` |
| `vpn` | +15 | not reported → always `false` |
| `datacenter` | +5 | `hosting` |

`risk_score = Σ weights`, `purity_score = 100 - risk_score`. `risk_level` is
empty at 0 and otherwise `low` (<30) / `medium` (<60) / `high` (<80) /
`critical`. `valid_signal_count` is 6 when ip-api.com answered (all six flags
have a defined value), `positive_signal_count` counts the clean ones.
`pollution_score` / `pollution_level` are always `0` / `""`: judging "pollution"
needs mail-blacklist or pollution-database data that none of these sources
provide.

## Field-level guesses (not specified by the theme's contract)

* `network.datacenter` is filled with the operator/organisation name when
  `hosting` is true (there is no dedicated datacenter-name field upstream).
* `network.network_type` is `"mobile"` when the `mobile` flag is set;
  `network.company_type` is `"hosting"` when the `hosting` flag is set.
* `network.domain` normally comes from ipwho.is. The reverse-DNS fallback takes
  the **last two labels** of the PTR (`host.example.com` → `example.com`); it
  cannot be exact without a public-suffix list, and it is only used when
  ipwho.is gave nothing.
* `location.registered_country` (the *name*) is empty: RIPEstat only returns the
  registered country *code*.
* `latency.nodes[].latency_ms` is an integer (rounded) so that both `number` and
  `integer` schemas validate; a non-zero latency never rounds down to 0.
* `provider.quality_sources` is the static list of supplementary sources;
  whether they actually answered is in `available_sources`.
* `capabilities.global_latency` is `true` because this backend implements the
  latency endpoint. Set `NEKOMARI_IP_INFO_DISABLE_LATENCY=1` to disable it (then
  both `/status` and `/latency` report it as unavailable).
* `globalping` requests use `NEKOMARI_GLOBALPING_TOKEN` when set; the public API
  works without a token.

## Operational notes

* Every upstream call goes through one `http.Client` with a 5 s timeout. The
  lookup handler caps the whole request at 15 s, the latency handler at 25 s
  (Globalping gives up after 20 s and returns what it has).
* No goroutine or timer outlives a request; the service needs no `Close`.
* Nothing is persisted: caches live in the process and are per-IP.

## Verification against the real theme (open item)

Verified on the live instance with a real browser (Playwright, logged in as a
visitor), not just with curl:

* The theme issues `GET /status` and then a `GET /lookup` for **each** of the
  node's addresses — IPv4 and IPv6 — and all three answer **200**.
* The bodies are accepted by the theme's strict schemas: no zod error and no
  console error of any kind, with real values (`SG` / Singapore, `AS31898`,
  route `213.35.96.0/19` for IPv4 and `2603:c024:4000::/35` for IPv6).
* `/latency` is not called on page load; the theme treats it as on-demand.

**Still open:** the "IP 信息" tab does not appear. The tab is rendered as
`x.available && <button>IP 信息</button>` where

```js
// Instance-B37568w_.js
function gn(e, t, n, r, i = true) {
  const a = mn(t), o = mn(n), c = !!(a || o), l = T(r) === 'CN';
  const u = useQuery({ queryKey: ['ip-info','status'], enabled: !!(i && e && c && !l) });
  const d = i && !l && u.data?.available === true;   // <- gates the lookups
  const f = hn(e, a, d), p = hn(e, o, d);
  const m = (!i || l) ? [] : [f.data, p.data].filter((x) => !!(x && !x.data.excluded));
  return { available: m.length > 0, lookups: m };
}
```

Everything that function tests is satisfied in the captured traffic
(`status.available === true`, both lookups `200` with `excluded: false`), and the
lookups only fire when `d` is true — so the gate passed at least once.

**Ruled out: the mainland-China gate.** The theme hides this panel when
`T(region) === 'CN'`, and two restored nodes carry a Chinese region, so that was
the obvious explanation. It is not the cause. Probed with
`deploy/ip_panel_probe.js` against a US node (`🇺🇸`) and a Hong Kong node (`🇭🇰`):
both render no IP tab, and both see `status.available: true` and a `200` lookup
with `excluded: false`. The gate that hides the panel is therefore not the region
check, or not only it.

**Observed while probing, not yet explained:** the browser logs repeated
`WebSocket connection to 'wss://…/api/rpc2' failed: HTTP Authentication failed` —
19 of them on one page load. The REST calls in the same session succeed. This may
be an artefact of logging in through `fetch` rather than the real form (the probe
does that to skip the login UI), in which case it is a harness problem and not a
product one — but it has not been checked against a real login, so it is recorded
rather than dismissed. If the frontend really does open an unauthenticated
WebSocket on the instance page, that would be worth fixing on its own.

So the server side is confirmed correct and the remaining cause is inside the
theme's own gating. Anyone picking this up should instrument `gn` (or the
`['ip-info','status']` query cache) rather than re-check the API — and should
check the WebSocket errors above before assuming the gate is the only thing
involved.
