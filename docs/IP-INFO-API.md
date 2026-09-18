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

## Verification against the real theme

The theme (LuminaPlus) parses every response with a **strict zod schema** and hides the
"IP 信息" tab unless at least one address survives it. That schema is the real contract,
and it is not in this repository — it is minified inside the installed theme bundle.

### The defect, and why it was invisible

`classification.source` was missing from `/lookup` and `/latency`. The theme declares it
as a required string:

```js
// Instance-*.js — the schema block, extracted verbatim by deploy/theme-contract-check.mjs
Xt = h({ type: g(['native','broadcast','anycast','unknown']),
         label: J, geolocated_country_code: J, registered_country_code: J,
         confidence: Y,          // optional, defaults to null
         source: d() })          // z.string() — required, no default
```

Everything else in the payload was correct, so:

* every endpoint answered **200** with real data
* the server's own contract tests passed
* no zod error reached the console — React Query does not log query errors by default
* `available` was computed from the parsed lookups, so the tab simply never rendered

The fix is `Classification.Source`, set in `deriveClassification` to `country_comparison`
(both country codes known) or `unavailable` (either missing). Those two strings are the
reference implementation's own values (`shanyang242/Komari-IP-Info`,
`normalizeNativeClassification`), whose `source` also defaults to `"unavailable"`.

### How it was found

By extracting the theme's schema block out of the installed bundle and running it, with
the theme's own bundled zod, against the live responses:

```
$ node deploy/theme-contract-check.mjs --assets <theme>/dist/assets --base http://127.0.0.1:25774
  ✓ status
  ✗ lookup  HK04 82.152.161.202: rejected by the theme's schema
      data.classification.source — Required
  ✗ latency HK04 82.152.161.202: rejected by the theme's schema
      data.classification.source — Required
```

That script re-derives the contract from whatever theme build is installed, so it stays
correct across theme upgrades. Run it before shipping any change to `web/api/ipinfo`.

### Hypotheses that were tested and eliminated

Recorded so they are not re-tried. All four were reached by reasoning about the minified
client rather than running it; all four were wrong.

* **Mainland-China region gate.** The theme does hide the panel when
  `T(node.region) === 'CN'`. `T` is a normalizer that matches a flag emoji and returns the
  country code, `region` really is stored as an emoji flag, and two nodes really do carry
  `🇨🇳` — so on those two nodes the gate genuinely closes. Probing a US node and a Hong
  Kong node changed nothing, which is what ruled this out as the cause.
* **WebSocket authentication failures.** The pre-login `401 /api/rpc2` noise does not
  starve anything; `deploy/ws_timeline.js` shows every failure happens before a session
  exists, and an authenticated upgrade returns a real JSON-RPC reply.
* **`logged_in` missing from `/api/public`.** This was a genuine bug and a genuine fix
  (the theme's gate is `!isPending && data?.logged_in === true`), released in v0.1.6. It
  was necessary and not sufficient — the tab still did not render, because the failure was
  downstream of it.
* **The IP tab is dead code in this theme version.** It is not: the tab strip in `vn()`
  has exactly one builder, and it contains `x.available && <button>IP 信息</button>`.
  There is no second component rendering `[负载, Ping]`.

Also worth knowing, because it cost a round: **test theme changes against the origin on
the host** (`http://127.0.0.1:25774` from OC424), never through Cloudflare. Behind the
edge, a cache hit and dead code are indistinguishable.

### What the theme actually calls

All four endpoints exist and are exercised by the panel; `network-profile` is only a React
Query key name for `/latency`, not a separate route.

| Theme call | Endpoint |
|---|---|
| status query | `GET /api/public/ip-info/v1/status` |
| per-address lookup | `GET /api/public/ip-info/v1/lookup?uuid=&ip=` |
| "全球延迟" panel | `GET /api/public/ip-info/v1/latency?uuid=&ip=` |
| 刷新 button | `POST /api/admin/ip-info/v1/refresh` |

Two details of the theme's own logic are worth keeping in mind when changing this API:

* it reads node addresses from `common:getNodes`, and **falls back to the REST
  `/api/nodes`** if the RPC response fails its schema. `/api/nodes` blanks `ipv4`/`ipv6`
  unconditionally (`publicGetNodesInformation`), so the fallback silently produces a node
  with no addresses and therefore no IP panel. The RPC path returns real addresses to an
  admin only.
* it strips surrounding brackets from an address before using it, so a bracketed IPv6
  literal in the node record is tolerated.
