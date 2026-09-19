# Open work

State of the batch that started 2026-09-18. Written so a later session can pick up
without re-deriving anything.

## Status

| Item | State |
|---|---|
| One-line agent installer | **Done and released** in v0.1.6 |
| `logged_in` missing from `/api/public` | **Done and released** in v0.1.6 |
| macOS agent builds | **Done and released** in v0.1.6 |
| Scheduler accepting a job that never runs | **Fixed and released** in v0.1.7 |
| Silent-failure catalogue | **Done** — 5 entries, 3 real, 2 latent |
| LuminaPlus IP panel | **Fixed and released** in v0.1.8 — `classification.source` was missing |
| Family mixing within one ping task | **Fixed** — tasks split per family, verified single-family |

Production runs `ghcr.io/aone2233/nekomari:v0.1.9`; v0.1.10 carries the health-review
fixes below.

## Released

Everything in this batch is now in a release; nothing is waiting on `main`.

- v0.1.8 — the ip-info contract fix (`classification.source`)
- v0.1.7 — the scheduler guard, and the per-family task split
- v0.1.6 — the one-line installers, macOS agent builds, and `logged_in`

## Decisions waiting

### 1. A ping task can still measure two different network paths

**Mitigated, not fixed.** Tasks 11 and 12 targeted dual-stack hostnames. HK04 has both
families and dials IPv6; the two v4-only probes dial IPv4. One task, two paths, plotted
as comparable series — HK04 read 12.6% loss and the others 0.0%, because those numbers
describe different routes to the same hostname.

**Done:** option A below — those tasks were split per family and verified single-family
(13 tasks, `混族任务数: 0`). The two v4-only tasks now have v4-only probes, and the two
new IPv6 tasks use the dual-stack probes.

**Still open:** the underlying gap. The scheduler decides per node, but a task is still
reported as if every node measured the same thing, so a mixed task can be created again
by anyone who does not know to avoid it.

Options for the durable fix:

- **A** — split each dual-stack task into one per family, with probes that can only
  reach that family. *Done for the existing tasks; no code, so nothing prevents a new
  mixed one.*
- **B** — keep one task and restrict it to probes of a single family.
- **C** — have the agent report the address it actually used, and let the panel split
  the series per family. This is the real fix and also closes entry 4 of
  `SILENT-FAILURES.md`; it needs protocol and frontend work.

C is the durable answer.

### 2. The IP panel — resolved

Closed. The theme parses every ip-info response with a strict zod schema, and
`classification.source` was missing from ours, so the payload was rejected inside the
browser while every server-side check passed. Full account in `IP-INFO-API.md`; the
durable check is `deploy/theme-contract-check.mjs`.

Two things from this worth carrying forward, because both will recur:

- **A client's strict schema is part of the server's contract even though the server
  cannot see it.** Four rounds were spent reasoning about minified code and three of
  them produced confident wrong answers. Extract the schema and run it instead.
- **`common:getNodes` is the only path that returns real node addresses.** The theme
  falls back to REST `/api/nodes` when the RPC response fails its schema, and that
  endpoint blanks `ipv4`/`ipv6` unconditionally — a silent, panel-disappearing fallback
  that is worth remembering the next time an admin-only field looks empty.

### 3. MAC-WAN stalls

`deploy/ping-spike-shape.py` shows the 教育网 "jitter" is MAC Server alone: 10 of 10
spike minutes, and it spikes on every task it runs (max 635/576/349 ms against
p50 3/9/13 ms). So it is local to that host, not the target. MAC-WAN is both the
probe host and the backup host, which may be relevant. Not investigated.

### 4. Nomao's agent is still running after its node was deleted

Found while reading the post-deploy logs: the panel logs a `401` on
`/api/clients/v2/rpc` from `2604:abc0:50::11:601e` roughly every 25 seconds, forever.
That address is **Nomao** (`deploy/verify-cf-probe-retired.sh` carries it, and
`DEPLOY-OC424.md` lists it as the IPv6-only host). The node was deleted from the panel,
so its token no longer resolves and the agent retries with a credential that can never
work.

Confirmed on the host (reachable from OC424 over IPv6): `nekomari-agent.service` is
`active (running)` with `-t JOrbiMyxnxU5mRituvBmzl`, started Sep 17. There is also an
older, **not loaded** `komari-agent.service` unit file pointing at `/opt/komari-agent`
with an upstream `--auto-discovery` flag; it is not the source of the traffic.

Deleting a node in the panel does not tell the host to stop reporting. Anything that
removes a node should be paired with stopping its agent, or the panel logs a permanent
401 stream that looks like an authentication problem rather than a leftover.

**Waiting on a decision:** stop and remove the agent on Nomao, or leave it in case the
node comes back.

### 5. Password hashing is a single SHA-256 with a constant salt

`database/accounts/accounts.go` hashes passwords as
`base64(sha256(password + "06Wm4Jv1Hkxx"))` — no per-user salt and no key stretching
(bcrypt / scrypt / argon2). Inherited from upstream, and the weakest link in the auth
path: a database leak is offline-crackable at GPU speed. Changing it needs a migration,
because existing rows are in the old format — verify the old format on login, then
rewrite the row in the new format on success. Not done in v0.1.10 because it is a
behaviour change that needs its own tests.

### 6. Login has no rate limit

`web/api/public/login.go` calls `accounts.CheckPassword` with no attempt counter,
backoff or lockout, so passwords and 2FA codes can be brute-forced online. v0.1.10 only
bounded the request body (1 MiB). A limiter needs a decision on the key (account, IP,
or both), the storage (in-memory vs the panel DB) and whether it is configurable, so it
was left as its own change.

## Tooling added

All in `deploy/`, all indexed in `deploy/README.md`:

| Script | Use |
|---|---|
| `ping-spike-shape.py` | Are latency spikes shared across probes (target) or independent (per probe)? |
| `ping-task-stats.py` | Per-node loss and latency for one task, picking the newest series tag shape |
| `ping-tasks-audit-all.py` | Fleet-wide address-family mismatches and stale node references |
| `nekomari_auth.py` | Panel login including TOTP — every operational script goes through it |
| `pw_login.js` | The same for the Playwright probes |
| `install-node-agent.sh` / `.ps1` | The one-line installers |
| `verify-image.sh` | Pulls a published image and proves it starts |
| `theme-contract-check.mjs` | Runs the installed theme's own zod schemas against the live ip-info API; non-zero exit on rejection |

## Things that will bite again

**2FA breaks password-only automation.** Every script that logs in must go through
`nekomari_auth.py` with `NEKOMARI_2FA_SECRET` set. The failure looks like a broken
script rather than a rejected login.

**`/api/me` returns 200 for guests.** Check `logged_in`, not the status code.

**Test theme changes against the origin on the host.** Through Cloudflare a cache hit
and dead code are indistinguishable — that mistake cost a round.

**Do not reason about minified bundles.** Two readings both predicted the gate should
pass while the tab stayed hidden. Run `deploy/theme-contract-check.mjs` instead: it
pulls the schema out of the installed theme and reports the exact rejected field path.

**The theme bundle on the panel host has been patched and reverted twice.** Both
times it was confirmed byte-identical to its backup afterwards. Nothing is left in
place now; if a bundle ever needs patching again, back it up outside the container —
`/tmp` does not survive a container recreate.
