# Admin Tools Navigation

## v1.6.27 Scope

The five operations tools now open inside the panel-owned admin shell:

- Status and SLA: `/admin/sla`
- Bulk edit: `/admin/bulk`
- Maintenance windows: `/admin/maintenance`
- Configuration export/import: `/admin/config`
- Traffic forecast: `/admin/forecast`

These routes share the sidebar, topbar, active appearance and accent with the
rest of the admin interface. They use client-side navigation, not new tabs,
iframes or a second theme provider. The node-list bulk action carries selected
UUIDs to the same route. SLA window selection preserves router history, other
query parameters and hashes.

The original `/standalone/<tool>/<tool>.html` documents remain available for
bookmarks and public sharing subject to the existing access policy. Public
themes still cannot replace `/admin`. Documentation, home and terminal links
retain their existing behavior.

This is a bounded frontend patch on v1.6.26. It does not include the accumulated
v1.7 development tree, backend/schema changes, API-key migrations, Agent
changes or the deferred 200-node work. Existing API methods and authorization
are unchanged.

## Verification And Rollout

The focused Node tests cover menu reachability, actual admin child routes,
standalone compatibility, selected-node links and the SLA route adapter.
`frontend/script/admin-tools-navigation.browser.spec.py` boots an isolated
real Panel with synthetic credentials and exercises all five tools at desktop
and mobile sizes across five locales. It checks same-document navigation,
shared appearance, live theme changes, back/forward, reloads, direct links,
selected nodes, old standalone links and anonymous login boundaries.

Release only after exact-commit CI, then verify the published arm64 image on
OC424. Retain its previous immutable image/compose and a fresh OVH recovery
point. Change only the Panel service, not Agents or access policy. Anonymous
checks cannot certify private-fleet login/reconnect acceptance; the owner
reviews the live pages before further version development or cutover.
