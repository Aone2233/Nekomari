# Client-token rotation (v1.7 preparation, unreleased)

This is the fourth bounded v1.7 source batch, based on report/queue merge
`fc84915`. It adds a supported replacement operation instead of editing live
SQLite. It does not rotate any production credential, update an Agent binary,
change the protocol/schema or deploy the panel. OC424 remains v1.6.27/12ad73a.

## Authorization and credential contract

| Entry point | Contract |
|---|---|
| Admin node table | In-page "Rotate agent token" dialog; existing light/dark theme and five locales |
| REST | `POST /api/admin/client/:uuid/token/rotate`; body `{}` or `{"2fa_code":"<current code>"}` |
| RPC over HTTP/WebSocket | `admin:rotateClientToken`; params `uuid` and optional `2fa_code` |
| Identity | Administrator session only; existing sensitive-operation TOTP policy when the account has 2FA enabled |
| API keys | Denied, even with every scope; `agents:token:read` is not permission to rotate |
| Success | REST returns `{ "status": "success", "token": "na_..." }`; RPC result contains `token`; HTTP responses use `Cache-Control: no-store` |
| Audit | Actor UUID, source IP and node UUID, never the old/new token |

The server generates 256 random bits using `crypto/rand`, encoded as `na_` plus
unpadded base64url. The replacement is returned only after the DB update commits
and local credential-bound runtime state is retired. A failed DB write returns
no replacement and does not reset the node. Invalid/missing node UUIDs fail.
Credential lookup/update SQL tracing is suppressed to avoid logging secrets.

There is **no overlap period and no automatic distribution through the old
connection**. An explicit recovery-channel checkbox is a UI safety interlock,
not an additional server authorization mechanism. Accounts without 2FA retain
the existing session-only sensitive-operation policy; this batch does not
mandate enrollment or introduce a new account policy.

## Retirement boundary

Short Agent-side effects and rotation/removal share a context-cancelable,
per-node admission gate. Credential identity is rechecked inside the gate,
including requests authenticated before rotation but delayed before a write.
Idle gate entries are removed after their last contender leaves.

- Old HTTP requests and new attempts with the old token cannot admit mutations
  after retirement. Every v2 mutation uses the same gate: reports, basic info,
  unlock, ping/task/file results and pull/acknowledgement.
- Existing report WebSockets are aborted; delayed handshakes cannot restore
  retired runtime state. Each socket operation checks both credential and
  current socket identity. A failed old dispatch cannot recreate an event queue
  or enqueue into a replacement runtime generation.
- POST presence timers, online state and report-warning episode state are reset.
  Blocked long polls wake and recheck authorization before replying. Long polls,
  socket writes, external GeoIP calls and file streaming do not hold admission.
- Matching terminal sessions (including resumable sessions/timers), pending file
  replies, relay pipes, preview grants and download chunk hints are retired.
  Delayed terminal attachments must still match the exact session object.
- Latest/recent and persisted measurements remain. Rotation is not node removal;
  successful node removal additionally clears its latest-report state.

This boundary does **not** undo already-admitted work. Accepted report samples
may finish asynchronous persistence after rotation. Bytes already forwarded,
commands already running on an Agent and partial remote files are not recalled
or rolled back. Trusted administrator operations remain independently
authorized; rotation is not a transaction spanning all remote jobs.

## Safe operator procedure

1. Establish and verify independent SSH/provider-console access first. Do not
   depend on the panel terminal that rotation is about to close. Identify the
   actual Agent service, credential-file path, owner and permissions without
   printing the credential. OC424 uses
   `komari-agent-oc424-original-node.service`, not the unused generic unit.
2. Choose a bounded maintenance window. Open the node's rotation dialog, confirm
   independent recovery access and supply the current TOTP if enabled. Submit
   once. The node may appear offline until its Agent receives the replacement.
3. Store the returned token privately. Through SSH/console, atomically replace
   the configured token file, preserve the service user's ownership, restrict
   access (normally mode `0600`) and restart the **correct** Agent service.
   Never paste credentials into URLs, shell history, Git, reports or screenshots.
4. Verify the Agent reconnects, fresh samples arrive and the old credential is
   rejected. Do not treat an old retained chart as evidence of a fresh report.

The dialog keeps its response in component memory and clears it on dismissal;
it does not persist it in browser storage. It prevents duplicate submissions and
does not automatically replay a failed write. On any refused/lost response,
TOTP and recovery consent are cleared, the list is refreshed and the operator is
warned that the old token may already be invalid. **Do not blindly retry.**
Use the authenticated `admin:getClientToken` path (with its existing step-up)
and independent SSH/console to recover, then check fresh reporting.

Unlike scoped API keys, Agent tokens still use the legacy recoverable plaintext
DB model: session-authorized node reads/install commands and secret exports
are not redesigned here. A dialog displaying a response once is **not** proof
of digest-only storage, universal step-up on legacy reads or memory erasure.
Protect the database, backups, browser session and clipboard accordingly.

## Compatibility and rollback

Unchanged Agents keep working until an operator explicitly rotates their token;
the new value is opaque to existing Agents. There is no automatic fleet rotation
on startup. No new Agent/protocol/module/dependency/schema migration is included.

Two intentional security restrictions close alternate credential-write paths:

- Generic node edits/bulk adapters reject `token`, including capitalization and
  common quoting aliases. Agent basic-info maps can write only host facts, not
  credentials, visibility, names or rollout policy.
- Imports for an **existing** node may contain its unchanged token from a secret
  export, but a stale/different token is refused before application and checked
  again under admission before each node update. Unchanged tokens are omitted
  from the update map. The pre-existing new-node creation path is unchanged;
  this batch does not newly certify it as a full credential-restore mechanism.

Accept/document those restrictions for actual edit/import consumers before
release. Trusted raw SQL/database recovery still has broad administrator power;
the supported rotation boundary does not sandbox a deliberate DB modification.

A rollback must consider credential resurrection: restoring an old application
DB restores its old Agent tokens. Keep the old binary/image and verified paired
application/metrics snapshots, freeze writes for restore, account for newer
measurements and repeat incident rotations/revocations after restoring. Do not
blindly restore an old token file to make a disconnected Agent appear healthy.
Scoped-key consumer migration and encrypted-backup custody remain separate
release gates; development/push authorization does not waive either.

## Bounded verification (2026-10-26)

- Twelve affected Go packages pass 247 top-level tests (580 including subtests)
  ordinarily and with the race detector. These totals select the latest complete
  run per package, not one claimed full-suite run. Embedded-public regressions
  additionally pass 71 top-level tests (111 including subtests) ordinarily.
- Affected vet, module verification, Go1.27.3 Panel build and binary vulnerability
  scan pass. The scan finds no reachable/imported-package vulnerabilities and one
  required-module-only advisory; it is not an advisory-free module-graph claim.
- Frontend unit tests pass 194/194; lint, compiler audit, production build and
  embedded-theme repack pass. Existing build size/Zod warnings remain.
- Four mounted rotation browser groups cover recovery consent, TOTP, duplicate
  submission, no mutation replay, disposal and mobile-dark in-page behavior.
  Eight existing node-table groups pass with the real account context.
- A compiled embedded Panel, installed against private synthetic databases,
  performs real REST rotations from `/admin/servers` at desktop-dark and
  mobile-light sizes. Both check the returned secret against the DB-backed API,
  disposal, no page error, new tab or document overflow. The smoke binary has
  provisional build metadata and is not a release artifact.
- OVH runs checksum-matched, locally prebuilt race executables: six focused
  top-level groups (13 with visible subtests) pass in private fixtures; the
  existing Agent remains PID495903/restart0. Original failed fixture logs are
  retained; fixes initialize isolated state and use an absolute subprocess
  executable path, without weakening security assertions.

Local and OVH results are not remote-native compilation, arm64/Windows release
acceptance, production credential migration, capacity/SLO measurements or an
encrypted-backup restore drill. Exact-candidate CI, compatibility, backup/custody
and bounded OC424 acceptance still apply before tag/deploy. No 200-node,
benchmark or soak work is resumed.
