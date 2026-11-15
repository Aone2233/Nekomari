# Komari theme and plugin compatibility baseline

This is the development baseline for the next Nekomari release, not a claim
that all extensions work. Catalogs and upstream change independently; each
extension must be checked at a specific version, with real data and lifecycle
behavior. Never install third-party code into production merely to test it.

Baseline captured 2026-11-08:

- Komari upstream `9a4d730507d7679977ff1b810f131369e278c694`.
- Official [theme catalog](https://github.com/komari-monitor/theme-market):
  66 entries, updated 2026-11-03; catalog SHA-256
  `9acc4e7b94cbadfd681a8e58d34c7ebb6ccc45178eeb8a8da4f81e20809d3a1c`.
- Official [plugin catalog](https://github.com/komari-monitor/plugin-market):
  26 entries, updated 2026-11-05; catalog SHA-256
  `f0cfdcdb3cd2dfc542dd8ffd1f056483533f7418ae032a3451aae64bd74f8b91`.

All 26 plugin ZIPs and 64 of 66 theme ZIPs downloaded and matched catalog
SHA-256; their catalog short/version matched their manifest. `nezha` returned
HTTP 404 and `Emerald-Cazi` did not match its catalog hash, so neither is
included in the verified set. The `testdata/komari-market-20261108` fixtures
are the exact root manifests extracted from those verified ZIPs. No downloaded
extension code or credentials are included in the fixtures.

`TestKomariMarketPluginManifests` and `TestKomariMarketThemeManifests` validate
those declarations offline. For deeper but still limited package verification,
provide hash-verified ZIP directories via `NEKOMARI_COMPAT_PLUGIN_ZIPS` and
`NEKOMARI_COMPAT_THEME_ZIPS`; the optional tests exercise plugin installation
into a temporary directory and theme ZIP preflight. These checks do **not** load
plugins, execute their network actions, render themes, verify authentication,
or establish compatibility of all their API calls and statistics.

Behavior fixes in this branch include awaiting Promise results from plugin RPC
handlers, excluding staging directories from installed themes, extracting ZIPs
without Unix permission metadata, staging replacements before touching an
installed theme, preserving Next.js hydration data when rewriting the
description, URL globals in both runtime modes, a limited TLS client and
plugin notification channels. Notification configuration fields known to
contain credentials are masked in the browser.

The 26 verified plugin packages passed an isolated load/reload/unload smoke
test in individual, network-disabled, resource-limited containers. The GFW
plugin used an empty task-list RPC fixture. All six notification packages sent
to local mock servers: email with PLAIN and LOGIN authentication, plaintext,
STARTTLS and implicit TLS, and the five HTTP channels over loopback. Each
delivery test checks the archive hash before loading it; no external service
was contacted. Browser testing confirmed the email password field is masked.
This is not a functional test of all plugins.

Additional network-isolated tests with real, hash-checked plugin packages and
synthetic fixtures covered the `api` plugin's public node list, live CPU data,
hidden-node and credential filtering, the `availability-history` plugin's
connection tracking and anonymous event denial, and `override-location` HTTP
and WebSocket response rewriting. With an empty temporary credential store and
a synthetic account, `komari-passkey` 0.1.14 reports an available verifier and
denies anonymous credential listing. In a separate isolated candidate panel,
Chromium's virtual authenticator registered a credential and signed in after
clearing the test session. These are focused scenarios, not complete coverage
of those plugins or a test of the user's physical authenticator.
The real one-time-code plugin was tested with a mock account, mock notification
and mock session writer: a wrong code failed, the delivered code succeeded once,
and reuse failed. No notification or session was sent to production.
The traffic-report plugin also produced a daily report from mock upload and
download series through a synthetic schedule and notification receiver.

NetForecast `26.1021.6` exposes administrator client records in its debug
routes, allows unauthenticated quota edits, and accepts untrusted input into a
shared forecast snapshot. Nekomari therefore requires an administrator session
for all of its `/api/plugin/net-forecast/` routes and rejects cross-origin
state-changing requests. This makes its guest floating widget unavailable; an
upstream fix must filter private clients, authenticate mutations and avoid
sharing untrusted snapshots before restoring public access. The upstream
plugin also reads the Promise returned by `server.getConfig()` synchronously,
so its configured settings are not applied; this is not fixed by pretending
the upstream asynchronous API is synchronous.

`api`, `ipqa-alert-report`, `availability-history` and `komari-gfw` treat any
API key as a full administrator, whereas Nekomari keys have scoped grants. Their HTTP routes
therefore reject API-key principals until those extensions enforce scope-aware
authorization; IPQA's state-changing routes also reject cross-origin requests.
Anonymous IPQA reads retain the plugin's own node-visibility filtering and
session administrators retain access. These protections are in the candidate,
not in the currently deployed server.

On an isolated test panel, all 64 available theme archives have installed
and opened in a mobile browser with an offline fixture node, including
`komari-deer` after the file-mode fix. Their shells returned 200 and rendered
some text; this smoke test does not check data correctness or navigation.
All 64 were also opened at desktop size; the shells rendered. Some themes
made external requests which were blocked. `komari-next-pro` and `MiStatus`
produce React hydration errors even when the browser receives the original
unmodified HTML from their packages. `Mochi` raised an unhandled fetch error
when the isolated browser blocked its external world-atlas CDN request; its
offline node list nevertheless rendered. The `ran` theme's
unfiltered ping-history request initially returned 400; the new budgeted,
visibility-filtered aggregate endpoint returned 200 in a subsequent browser
check. Populated histories, missing data, permissions and detailed interactions
remain to be checked.

After changing installation to stage and replace, all 64 verified archives
passed install and reinstall in temporary directories. A rejected archive test
confirms the installed theme remains intact.

Known work still required before a compatibility release:

1. Expand notification delivery coverage for TLS variants, failure handling,
   reloads and the upstream built-in channel RPC contract. Plugin choices use
   namespaced IDs to preserve built-in sender IDs and existing settings.
2. Evaluate upstream plugin route WebSockets, including cancellation, origin
   checks, unload and resource limits; existing WebSocket hooks are distinct.
3. Run original market bundles in an isolated panel with representative node,
   visibility, plugin, historical and missing-data fixtures; exercise install,
   permission approval, reload, unload, RPC, routes and both desktop/mobile
   themes. Record per-version evidence and failures.
4. Compare response semantics, including nullable values, grouped ping series,
   traffic cycles and historical data. Keep unknown data unknown; a theme that
   assumes missing data is zero or collapses distinct series may need an
   upstream fix or an explicitly documented compatibility path.
5. Verify each extension's documented permissions and side effects without
   sending notifications or contacting third parties during acceptance.

The Marketplace and upstream can publish newer versions or unlisted extensions;
re-run this audit when choosing the release baseline and report the precise
coverage rather than promising compatibility with unknown future packages.

`unlock-test` also calls `admin:getAgentStartupConfig` to discover whether an
Agent permits remote commands. Nekomari does not implement that newer Komari
Agent protocol. The plugin catches the error and reports this capability as
unknown. Returning a fabricated enabled/disabled value, or exposing an Agent's
credentials to approximate the upstream API, would be unsafe. A compatible
implementation requires a versioned Agent request/result, authenticated
response correlation, timeout, credential handling and compatibility tests
for older Agents; it has not been shipped.
