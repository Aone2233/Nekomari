# Ran · 岚 v2.1.2 ping-history patch

Local compatibility patch to the third-party online theme, **not** a new
theme. Upstream baseline:

- Repository: <https://github.com/saladinxp/komari-ran-theme>
- Tag: `v2.1.2`, commit `9a2b6e1` ("perf: stop fetching data the page cannot display (v2.1.2)")
- Market archive SHA-256:
  `4ea93085644626a5208fcd66204aab7c9da6f910214881b903825f91cd760791`
- Manifest version stays `2.1.2`.

## Root cause

`useKomari` called the legacy global endpoint:

```text
GET /api/records/ping?hours=1
```

The panel's `public:getPingRecords` requires `uuid` or `task_id` and returns
HTTP 400 `UUID or task_id is required` without one. The same theme already had
a valid per-node call (`/api/records/ping?uuid=...`), so the global request was
both redundant and impossible to satisfy.

## Patch

- Add `fetchPingHistoryForNodes(nodes, hours)` in `src/api/client.ts`.
  It fans out to the existing per-node endpoint with concurrency 6, merges task
  metadata by task id, sets `record.client = node.uuid` when needed, and returns
  the legacy `PingHistory` shape.
- In `src/hooks/useKomari.ts`, replace the global `fetchPingHistory(1)` with the
  per-node fan-out, keep a `nodesRef` for the 60 s refresh, and start the first
  fetch after the node roster is known.

No panel API change is required and no new endpoint is introduced.

## Verification

Disposable panel v1.8.0/a92a242, no production data:

- Desktop `1280x900`, mobile `390x844`
- Routes `/` and authenticated `/admin/`
- Public desktop, public mobile, admin desktop, admin mobile: **all PASS**
- `/api/records/ping?hours=1` no longer appears; no HTTP 400 remains
- The remaining `proxycheck.io` CORS console message is an external theme
  dependency and is recorded as a warning, not counted as a theme failure for
  this targeted change. `ipwho.is`/`frankfurter.app` remain in the separate
  external-dependency item.

The fixed archive used for this verification is:

```text
/tmp/theme-fixed/ran.zip
sha256 a35161ac921b584791ad33e9f6df5ee238d29b20917f60a1fae08ce48474b9f0
```
