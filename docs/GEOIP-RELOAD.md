# GeoIP configuration reload

## v1.7.2 candidate scope (2026-10-27)

Panel-only optional-provider correctness patch. No Agent, protocol, dependency,
frontend, schema, authentication, API-key permission or token change.

- Configuration read failure or wrongly typed enabled/provider values fall back
  to `EmptyProvider` with a warning, rather than panic in a background goroutine.
- Both `geo_ip_enabled` and `geo_ip_provider` trigger reload. Disabling GeoIP
  replaces the provider and clears cached locations without a Panel restart.
  Re-enabling constructs the selected provider again.
- Empty/unknown provider selections and constructor errors use `EmptyProvider`;
  newly added diagnostics do not print arbitrary configuration values.
- Lookups and database updates finish before their provider is closed. Every
  replacement flushes the cache, including results written by retiring lookups.
  Reloads serialize and read current settings, not event payloads; queued reloads
  converge to the latest state. Shutdown prevents subsequent reload publication.

This is asynchronous reload, not an instantaneous settings transaction. An
in-flight lookup or MMDB download may delay replacement/shutdown within the
existing provider timeouts (HTTP lookup 5 seconds; MMDB download 60 seconds).
It does not revise the ipinfo empty-country caching policy, make external GeoIP
services reliable, or fix malformed settings globally.

## Focused verification

```sh
GOTOOLCHAIN=go1.27.3 go test -race -count=1 ./utils/geoip -run 'TestGeoIP|TestUpdateDatabase'
GOTOOLCHAIN=go1.27.3 go test -race -count=1 ./internal/server ./web/api/client
GOTOOLCHAIN=go1.27.3 go vet ./utils/geoip ./internal/server
```

Regressions cover malformed JSON types, closed settings DB, enable/disable/
provider replacement, cache invalidation, in-flight lookups, concurrent reloads
and shutdown, plus both reload event keys. Existing offline MMDB update-failure
contracts remain. Its success-path test requires an existing local MMDB fixture
and explicitly skips when absent. Required CI additionally runs the full existing
server suite and the hermetic GeoIP lifecycle tests under the race detector.

`deploy/geoip-reload-smoke.py --panel-binary /path/to/nekomari` installs an
isolated synthetic Panel and checks eight real settings-event groups, including
invalid MMDB fallback and recovery without a restart. Required panel-smoke CI
runs it; it can also run on OVH with the attested release binary. It makes no
third-party GeoIP lookups. Never toggle production settings or
reuse production admin credentials for that check. OC424 acceptance preserves
the existing `true`/`ipinfo` settings and the owner-approved 14-client/13-fresh-
reporter baseline (the owner authorized Pi's new node). Fresh backups must
include that node; earlier 13-client release backups are historical recovery
copies, not the current deployment baseline.

Publication/deployment and authenticated owner UI acceptance are separate gates.
This no-schema patch can roll back by pinned v1.7.1 image while preserving current
DBs. Do not restore an older 13-node DB or confuse this with a v1.6 auth downgrade.
See [the live work list](./OPEN-WORK.md) and [OC424 runbook](./DEPLOY-OC424.md).
