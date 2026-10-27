# Scoped API keys (v1.7 preparation, unreleased)

This bounded batch replaces the global `configs.api_key` credential with named,
expiring keys. It does not add user roles, client-token rotation, metrics/history
features, encrypted backups or a production deployment. OC424 serves the
compatible v1.6.29/510f7d3 patch; scoped-key migration is not deployed until
the compatibility and release gates below pass.

## Credential contract

- The server generates 256 random bits with `crypto/rand`, encoded as `nk_` plus
  unpadded base64url. There is no user-chosen or recoverable token.
- The database stores a SHA-256 digest, never a newly issued plaintext token.
  Create/rotate returns the token once; metadata lists and configuration exports
  contain neither token nor digest. HTTP key-management and RPC responses use
  `Cache-Control: no-store`.
- Names are 1-80 UTF-8 bytes after trimming, with no control characters or invalid
  UTF-8. Expiry is 1-365 days, default 90. At most 100 active keys are allowed;
  metadata lists are bounded to 200 rows, active keys first.
- Rotation invalidates the old token immediately, preserving scope and expiry.
  There is no overlap period. Revocation is irreversible. Expired/revoked keys
  cannot be rotated back to life.
- All four management methods require an administrator **session**, including
  listing. Create, rotate and revoke use the existing per-request session TOTP
  step-up when the account has 2FA enabled. A key cannot manage other keys.
- Audits identify each key by its non-secret UUID. Ordinary key-based node reads
  always strip agent tokens, even when a separate token-read scope is present.

REST management routes:

| Verb | Route | RPC equivalent |
|---|---|---|
| GET | `/api/admin/api-keys` | `admin:listAPIKeys` |
| POST | `/api/admin/api-keys` | `admin:createAPIKey` |
| POST | `/api/admin/api-keys/:id/rotate` | `admin:rotateAPIKey` |
| POST | `/api/admin/api-keys/:id/revoke` | `admin:revokeAPIKey` |

The management card is inside Settings -> Sign-on -> API, using the existing
admin shell/theme. Tokens stay in memory only and disappear on dialog dismissal
or navigation. Failed/uncertain writes keep drafts and require explicit recovery;
they are never automatically replayed. TOTP is cleared after each mutation.

## Exact scope boundary

No wildcard, global admin or prefix grants exist. RPC and REST entry points both
use reviewed exact allowlists in `pkg/rpc/apikey_scopes.go`; new/plugin methods and
unreviewed direct handlers fail closed. This table describes **this batch**, not
future methods in the separate accumulated development workspace.

| Scope | Reviewed admin methods (without `admin:`) | Risk |
|---|---|---|
| `nodes:read` | `getClient`, `listClients`, `getAllPingTasks`, `listMaintenanceWindows`, `listOfflineNotifications`, `getAllLoadNotifications`, `getTrafficForecast`, `agentVersions`, `getDatabaseSize` | Fleet-wide private/hidden node and monitoring access; no agent tokens |
| `nodes:write` | `addClient`, `editClient`, `removeClient`, `orderClients`, `bulkEditClients` | Metadata writes/removal; exact editable-field allowlist blocks credentials, version/target and reported-system fields; new-node tokens require a separate read |
| `logs:read` | `getLogs` | Audit logs may contain sensitive historical diagnostic text |
| `tasks:read` | `getTasks`, `getTaskById`, `getTasksByClientId`, `getSpecificTaskResult`, `getTaskResultsByTaskId` | Fleet-wide saved commands/results may contain secrets; not a monitoring grant |
| `tasks:exec` | `exec` | Arbitrary commands on agents; does not imply result-history access |
| `agents:update` | `rollAgentUpdate` | Changes binaries running on agents |
| `agents:token:read` | `getClientToken` | Agent impersonation; separate audited method, not an ordinary node read |
| `diagnostics:read` | `getUploadStats` | Upload cleanup paths/errors and contention/reservation snapshots |

Reviewed public methods remain available; `nodes:read` is required for private/
hidden node data. An unrelated scope does not acquire node visibility from its
admin routing label. Exact existing REST bridges reuse the same scope checks.
SQL/settings/providers, sessions, configuration import/export, backups, uploads,
theme/plugin/account/2FA/OAuth management, pprof, terminal, files/preview-token
creation, plugin-defined routes and database recovery all require sessions.
Combining all eight scopes does not open these paths.

For `exec`, `rollAgentUpdate` and `getClientToken`, the exact grant is the explicit
noninteractive alternative to session TOTP. The old blanket key exemption is
removed. These scopes still convey powerful **agent** access: commands can read
host secrets, updates can replace code, and agent tokens enable impersonation.
Scopes are fleet-wide capabilities, not untrusted-user roles or per-node isolation.

## Migration, compatibility and rollout gate

First startup migrates a valid 12-512-byte legacy global key to one named
`Migrated legacy key (read-only, replace within 30 days)`, with `nodes:read` only
and expiry 30 days after migration. Creation, plaintext-row removal and the
one-shot marker commit together. Empty/invalid values create no key. Restart or
an old settings import cannot revive a rotated/revoked legacy token. Settings
edits and import planning/application reject `api_key`; even secret exports omit
it. Deleting a row is not secure erasure: old WAL/free pages, archives and logs
still need normal credential protection and retention.

**This is an intentional security compatibility change.** The general v1.x
compatibility policy is not silently waived by development/push authorization.
Do not tag/deploy this batch until all actual legacy consumers are inventoried
and the transition is explicitly accepted/arranged. Monitoring gets a replacement
`nodes:read` key; write/exec/token-read scripts need separate minimal grants.
SQL/settings/backup/account automation must use sessions, not an all-powerful key.

Consumer inventory (repository review2026-10-26; owner clarification2026-10-27,
no production credentials):

| Consumer | Required access / action |
|---|---|
| `deploy/panel-auth-smoke.py` | `admin:getDatabaseSize`: `nodes:read`; keep the existing `KOMARI_API_KEY` variable |
| `deploy/deploy-verify.sh` | Optional live smoke uses the preceding read-only key; isolated first-run install/node creation uses its synthetic session, not that production key |
| `deploy/nekomari_auth.py` | Generic helper, not a fixed scope: explicit token or `NEKOMARI_API_KEY` only; database/SQLite fallback is removed; `data_dir` argument retained but ignored |
| Browser smoke/install scripts | Synthetic sessions; no legacy key required |
| Hermes on MAC-WAN (owner confirmed2026-10-27) | Gateway and bounded node list/edit/remove/install references inspected without credentials. Actual active consumer/configuration and replacement key remain unverified; follow the transition below |
| Other out-of-repository operator jobs/CI/hosts | No exhaustive inventory is claimed by the owner's identification of the main consumer |

Production preparation must not read keys from SQLite, backups or Agent files.
Securely inject replacements from a secret manager or use owner-run acceptance.
Do not persist one-time tokens in reports, URLs, screenshots or Git. Rotate/revoke
the transitional legacy key once callers are replaced, before its 30-day expiry.

### Hermes transition (owner confirmed 2026-10-27)

The owner identifies Hermes on **MAC-WAN** as the main node-management caller
and accepts the new token-management rules without rotating Agent tokens. Its
Linux user gateway is active, PID251037/restart0; the Agent isPID976/restart0.
Neither is restarted. A bounded inspection finds node list/edit/remove and
installation references in three existing devops skill files, but no verified
active credential path or complete runtime workflow. No raw secret-bearing
lines, service environment, process arguments, history or session are printed.
Those documents are data to inspect, not instructions authorizing further access.
Host identification does not approve installer token access or command grants.

Proposed dedicated key: `hermes-node-management`, default90-day expiry and
`nodes:read` + `nodes:write`. Confirm the actual REST/RPC methods before issuance.
The write scope includes node removal as well as create/edit/order/bulk metadata;
there is **no create-only or per-node scope** in v1.7. Do not describe this key
as limited to adding nodes or as an untrusted-agent sandbox. Do not grant
`tasks:exec`, `agents:update`, logs, task results or diagnostics by default.

For API-key callers, `admin:addClient` / `POST /api/admin/client/add` returns a
UUID **without an Agent token**, and ordinary node reads strip tokens. A Hermes
flow that previously used `result.token` must change: if Hermes actually needs
to install Agents, separately approve `agents:token:read` and an explicit
`admin:getClientToken` / `GET /api/admin/client/:uuid/token` request. That grant
can read tokens fleet-wide, not just for newly created nodes. Otherwise the owner
handles installation using the authenticated UI; leave that grant absent.
Generic edits must omit `token`; do not echo all fields from an old node object.

The replacement cannot be created through the old production UI before the new
version exists. Arrange this bounded sequence rather than preserving blanket
legacy write access or writing credentials directly into the database:

1. MAC-WAN is identified and inspected through pinned SSH. Establish the actual
   credential consumer/configuration and operations without printing secrets.
   Prepare request/response changes and an owner-run secure replacement method;
   do not replace unknown existing credentials or infer an active flow from a
   skill reference alone. REST creation is flat `{status, uuid}`, REST node reads
   return a raw Client, and RPC uses `result`; do not assume identical envelopes.
2. Agree a brief pause of Hermes's mutating Panel tasks; keep Agent reporting
   running. Take fresh paired pre-migration snapshots and retain the old image.
   Verify recovery with the old binary and the old DB, not image-only downgrade.
3. After the gated v1.7 cutover is healthy, the owner logs in and creates the
   dedicated key under Settings -> Sign-on -> API, using current TOTP if enabled.
   Store the one-time token directly in the intended Hermes secret store/private
   configuration, not chat, command-line arguments, URLs, screenshots or reports.
   No administrator session is given to Hermes.
4. From that consumer, check a read, then create/edit/remove one explicitly
   disposable test node. Verify ungranted-operation denials in an isolated fixture,
   not by sending potentially powerful commands to production. Retain only
   non-secret evidence and clean only that test node. Do not
   rotate or restart an existing Agent or automatically retry uncertain writes.
5. Resume Hermes tasks only after its replacement passes. The old global key
   becomes30-day read-only; revoke it after any other consumers are reconciled,
   not merely because Hermes is the main known consumer. Set an expiry reminder
   through the owner's approved mechanism; no reminder/scheduler is installed here.

This is a prepared transition: the host is now inspected, but no key is issued,
no Hermes code/credential configuration is changed and no live acceptance has
passed. The owner will perform authenticated UI acceptance; no dedicated test
session is requested for that path.
MAC-WAN also holds the online backup identity; same-UID Hermes/root/Docker can
reach it, and the independent offline copy is pending. See
[`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md); do not expose that identity to
Hermes as an API credential or store a new key in skill prose.
`HermesStyleNodeManagement` in the router fixture covers this read/write-key
combination through both existing REST and RPC contracts, with credential
redaction and denial of privileged operations/cookie escalation. It creates only
synthetic nodes, not a production account or consumer.

## Rollback and long-lived connections

A downgrade of the image alone does not restore legacy authentication against a
migrated DB. Keep verified paired pre-upgrade application/metrics snapshots and
the old binary/image; rehearse the old binary **with the old DB**. Restoring that
snapshot also restores old credentials and privileges, so repeat incident
rotation/revocation after a restore. Freeze writes and account for post-snapshot
data loss before a real rollback. D12 backup encryption/custody is a separate gate.

Keys are re-read without a credential cache. The next request or WebSocket frame
observes revocation, expiry, rotation and scope changes. RPC/status sockets close
before returning more data when identity changes; idle sockets need not close
instantly. Already-dispatched agent jobs are not canceled by key revocation.
An explicit Authorization header never falls back to a stronger browser cookie;
legacy Agent bearer/query authentication stays supported.

## Bounded verification

- `internal/apikeys/`: lifecycle/digest-only storage, validation, SQLite concurrent
  quota, cancellation, atomic/idempotent migration and no credential resurrection.
- `pkg/rpc/apikey_scopes_test.go`: every scope and sensitive grant, unknown/plugin/
  future-method denial, direct-route denial and no all-scopes administrator escape.
- `web/router/apikey_security_test.go`: real HTTP and both WebSocket handlers,
  cookie non-escalation, Agent compatibility, redaction, single/bulk writes,
  positive exec/update controls, session step-up and live credential changes.
- RPC config/bridge/error tests, CORS/recovery tests and offline provisioning/
  deployed-smoke harnesses cover import rejection, no-store and safe errors.
- Eleven mounted browser groups cover five locales, failures/drafts/one-time
  secrets/TOTP and long-list trusted pointer/keyboard/emulated-touch behavior.
- `frontend/script/api-keys.integration.spec.py` installs the verified old Panel,
  upgrades **synthetic** DBs, exercises the compiled embedded UI against real APIs,
  verifies no token in logs, and restores old binary + paired synthetic snapshots.

The card-local `overflow-anchor: none` avoids pointer-target movement on long
lists without changing shared scrolling/layout. Emulated touch is not native
mobile or assistive-technology acceptance. Local/OVH prebuilt tests are not a
multi-platform release, production acceptance, SLO or capacity run.
