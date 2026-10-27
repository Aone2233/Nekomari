# Hermes node metadata client (v1.7.0)

`deploy/hermes_nodes.py` is the reviewed metadata-only consumer for the owner's
Hermes on MAC-WAN. The Panel now serves v1.7.0/8eb711e, so its version gate is
satisfied; **no production key is configured yet**. During cutover the gateway
is briefly stopped, 3 old references are backed up/directed to this client, then
the gateway resumes. This is not proof that every existing runtime task has
been rewired or live writes accepted. The old global key is now 30-day read-only;
node mutations wait for owner key issuance/private configure/disposable check.

## Scope and trust boundary

- Requires Linux/Python3.10+ standard library; no new Python package, service,
  frontend, Agent or protocol dependency.
- Operations are `check`, `list`, `get`, `add`, `edit` and explicit-UUID `remove`.
  Default output selects UUID/name/group/region/hidden only, not raw private
  node objects, remarks, IPs or Agent tokens. These selected metadata values can
  still be private; do not post them publicly without review.
- The proposed dedicated key is `hermes-node-management`,90-day expiry, with
  `nodes:read` + `nodes:write`. Write is **fleet-wide and includes deletion**;
  it is not create-only, per-node isolation or an untrusted-agent sandbox.
- There is no token-read/install/exec/update/rotation/key-management command,
  arbitrary route option, admin cookie or credential fallback. Creating a node
  returns its UUID; the owner installs its Agent through the authenticated UI.
  Installer automation would separately require fleet-wide `agents:token:read`
  approval; do not silently grant it because a skill mentions installation.
- Private credentials are plaintext0600 JSON under an owned0700 directory.
  Same UID, root or Docker administrators can read them. This is explicit local
  storage, not an OS keychain or secret-manager integration. The backup identity
  is a separate file and is never used as an API key.

## Credential and request contract

`configure` requires an owner-operated TTY, not a key in argv, environment,
stdin, chat or logs. It checks the public version **before prompting**, then
uses `getpass` without an echoed-input fallback. Only canonical newly issued
`nk_` keys are accepted, never the old global credential. A read succeeds
before exclusive0600 publication; existing credentials are not overwritten.
Read success does not prove the write scope or complete live acceptance.

All operations require an exact HTTPS origin without userinfo/path/query or
fragment. TLS validation remains enabled. The client disables proxies,
redirects (including same-origin), cookie jars and automatic retries; the key
goes only in the Authorization header. Responses/patches are bounded to2MiB/
64KiB and socket timeout20s. Raw response/network exception text is withheld.
The version guard accepts stable v1.7+ within major1, not old/unknown/prerelease
or future-major contracts. Version reporting is a compatibility guard, not
cryptographic proof of server permissions.

REST creation uses the flat `{status, uuid}` response, reads/list use raw
Client/Client[], and edits/removal require the success envelope. Display uses
an allowlist, dropping any unexpected credential fields even from a malformed
server. `edit` sends only explicitly supplied metadata fields from the reviewed
server allowlist, never a replayed full node object. `remove` requires matching
`--confirm-uuid`; this guard is not a per-node scope on the actual API key.
Mutation errors explicitly require inspection before manual retry; a timeout
or bad success response may follow an already committed write. The client
does not automatically clean up/retry an uncertain operation.

## Staged MAC-WAN paths

| Purpose | Path |
|---|---|
| Client and instructions | `/home/macos/.local/share/nekomari-hermes/hermes_nodes.py`, `README.md` |
| Future owner-created credential | `/home/macos/.config/nekomari-hermes/credential.json` |
| Separate recovery identity | `/home/macos/.local/share/nekomari-recovery/identities/oc424-primary.agekey` |

The client remains checksum-matched. The cutover marker is
`/home/macos/.local/share/nekomari-hermes/v1.7-cutover.json`; it records only
non-secret status/reference hashes. 3 known devops references now begin with a
reviewed-client override that forbids legacy API/token/install fallback for
this Panel. Their original bytes are preserved privately on MAC-WAN; they are
not copied into Git/evidence because historical prose may contain credentials.
Hermes gatewayPID922780/restart0 is active again; AgentPID976/restart0 is
unchanged. No key or administrator session is extracted or handed to Hermes.

These reference edits do **not** prove a complete active-task inventory or
runtime migration. Existing-key writes are denied server-side after migration.
Do not resume node writes until the new credential and explicit live workflow
check pass. Unrelated Hermes operations can continue. No autonomous retry,
credential recovery or Agent-token grant is authorized.

## Owner actions, at the appropriate time

### Offline recovery checkpoint

The owner reports the independent offline identity copy downloaded and
explicitly authorizes cutover. No private identity is exported by this task.
Matching the copy's derived public recipient and actual recovery from that
medium remain separate owner-run checks; online MAC-WAN restore is not their
substitute. See [`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md).

### Now: owner key and authenticated acceptance

1. Log in to Settings -> Sign-on -> API. Create `hermes-node-management`,90days,
   **only** `nodes:read` and `nodes:write`, with existing TOTP when required.
2. In an owner-operated terminal, run:

   ```sh
   ssh -t MAC-WAN 'python3 -I /home/macos/.local/share/nekomari-hermes/hermes_nodes.py configure'
   ```

   Paste the one-time key only at the hidden prompt. Do not supply it in the
   SSH command or transcript; keep terminal recording/clipboard exposure in
   mind. New keys cannot be recovered later from the Panel database. Existing
   local credentials are not overwritten; a later key replacement needs an
   explicit private-file handoff, not blind configure retries.
3. Check read access, then explicitly create/edit/read/remove one disposable
   test node, recording only non-secret evidence. Reconcile a failed/uncertain
   write before retrying. Do not touch an existing reporting node, install an
   Agent, rotate tokens or probe powerful forbidden operations in production.
4. Verify history/audit/API-key UI behavior during owner acceptance. Resume
   Hermes mutations only after the new consumer passes. Reconcile any other
   callers before retiring the migrated 30-day read-only key.

Metadata examples, **not executed against production**:

```sh
python3 -I /home/macos/.local/share/nekomari-hermes/hermes_nodes.py check
python3 -I /home/macos/.local/share/nekomari-hermes/hermes_nodes.py add --name hermes-disposable-check
# Use only the UUID returned by that explicitly disposable creation.
printf '%s\n' '{"name":"hermes-disposable-updated","hidden":true}' | \
  python3 -I /home/macos/.local/share/nekomari-hermes/hermes_nodes.py edit <UUID> --patch-file -
python3 -I /home/macos/.local/share/nekomari-hermes/hermes_nodes.py get <UUID>
python3 -I /home/macos/.local/share/nekomari-hermes/hermes_nodes.py remove <UUID> --confirm-uuid <UUID>
```

## Bounded evidence

`deploy/hermes_nodes_test.py` passes17 offline groups: exclusive/private storage,
symlink/hardlink/FIFO/size/mode refusal, no legacy/env/echo fallback, version
guard before credentials/mutations, real redirect refusal, bounded failures,
REST shapes/redaction, metadata-only edits and explicit no-retry deletion.
CI runs that focused harness in `deploy-scripts`.

A one-off real compiled synthetic Panel additionally passes6 groups through an
ephemeral certificate-verified localhost HTTPS proxy: actual session-created
scoped keys, read/create/edit/list/remove, read-only mutation refusal, no
token/exec/cookie traffic, and no credentials in CLI/Panel logs. No API handler
is mocked. The single Go1.27.3 build injects `v1.7.0/hermes-fixture` solely for
the guard; it is **not a release artifact or production migration evidence**.
The temporary TLS key, synthetic accounts/keys/DBs and server/proxy are cleaned.
The first fixture ran login before the post-install router was ready; readiness
was corrected without weakening the client guard or changing application code.

The staged scripts also run their synthetic17-group harness on MAC-WAN without
credentials or production calls. No redundant broad local frontend/Go suite,
fleet work or 200-node run is needed for this operational-only batch. The release
and encrypted backup cutover are delivered; actual consumer/owner acceptance
remains in
[`V1.7-CUTOVER-READINESS.md`](./V1.7-CUTOVER-READINESS.md).
