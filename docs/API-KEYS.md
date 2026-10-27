# Scoped API keys (v1.7.0)

Named, expiring keys replace the global `configs.api_key` credential.
**Published/deployed:** OC424 serves v1.7.1/3b7e300 after exact-source/release/
native image verification. One old key is migrated to 30-day `nodes:read`, with
the plaintext settings row removed. Existing Agent tokens are unchanged.
Owner-configured Hermes read/write key, private handoff and actual disposable
CLI write acceptance pass; owner authenticated UI and other-caller inventory
remain separate. See [`DEPLOY-OC424.md`](./DEPLOY-OC424.md)
for actual migration evidence and paired old-binary/DB rollback; public checks
are not authenticated acceptance. This feature does not add user roles.

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
| Hermes on MAC-WAN (owner confirmed2026-10-27) | Gateway paused/resumed at cutover; 3 known references redirected, old key now read-only. Owner-configured dedicated key and disposable CLI writes pass; complete runtime inventory remains open |
| `deploy/hermes_nodes.py` (reviewed live-accepted consumer) | `nodes:read` + `nodes:write`; stable-v1.7+ HTTPS guard, metadata-only REST CLI, owner-TTY exclusive key handoff. No Agent token/install/exec/update access; see [`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md) |
| Other out-of-repository operator jobs/CI/hosts | No exhaustive inventory is claimed by the owner's identification of the main consumer |

Production preparation must not read keys from SQLite, backups or Agent files.
Securely inject replacements from a secret manager or use owner-run acceptance.
Do not persist one-time tokens in reports, URLs, screenshots or Git. Rotate/revoke
the transitional legacy key once callers are replaced, before its 30-day expiry.

### Hermes transition (owner confirmed 2026-10-27)

The owner identifies Hermes on **MAC-WAN** as the main node-management caller
and accepts token edit/import restrictions without rotating Agent tokens.
During cutover the gateway is explicitly stopped, three known node/install
references are backed up and prepended with reviewed-client/no-legacy-fallback
rules, then the gateway is resumed (PID922780/restart0). AgentPID976/restart0 is
unchanged. Reference edits are not proof that every runtime task is migrated.
The old key is server-enforced read-only. The owner privately configures the
new key, metadata matching verifies only the intended scopes/private modes, and
actual MAC-WAN create/read/edit/read/remove passes at13:49:10 Asia/Shanghai. The
disposable node is deleted and original13 metadata is unchanged. Reviewed CLI
writes may resume; three reference overrides/marker record that acceptance,
without service restart or a complete-caller-inventory claim. No environment, process args, raw headers,
history, sessions or stored credential values are extracted.

The metadata-only CLI and hidden owner-TTY provisioning path are installed on
MAC-WAN; the owner supplies the one-time key through its hidden prompt. Stable
v1.7 passes the version gate, but no
credential fallback exists; missing credentials stop the operation. See
[`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md). Host identification and
reference edits do not approve installer token access or command grants.

Actual dedicated key: `hermes-node-management`, only `nodes:read` + `nodes:write`,
owner-selected365days, expiring2027-10-27 13:38:02 Asia/Shanghai (not the suggested90).
No silent shortening/rotation or broader grant. The original transition procedure
below is retained as a template, not an instruction to issue another key.
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

Cutover and brief gateway pause/resume are complete, but new-key provisioning
and actual consumer/UI acceptance are **not**. The owner performs authenticated
acceptance and key issuance; no dedicated test session is requested. Node writes
remain paused at the old-key/server boundary until the replacement passes.
MAC-WAN also holds the online backup identity; same-UID Hermes/root/Docker can
reach it. The owner reports downloading an offline copy; independent-media
recovery is not claimed. See [`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md);
never expose that identity as an API credential or put a new key in skill prose.

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
