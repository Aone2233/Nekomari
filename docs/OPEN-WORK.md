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

Production runs `ghcr.io/aone2233/nekomari:v0.1.26`; the dated current-state
record and rollback procedure are in `docs/DEPLOY-OC424.md`. This file tracks
the older 2026-09-18 batch and its remaining decisions, rather than the entire
release backlog. **`docs/ROADMAP.md` is the consolidated list of what is left** —
correctness, performance, visualization and the decisions waiting on the owner;
`docs/FOLLOWUP-v0.1.25.md` is the per-release record that fed into it.

## Released

Everything in this batch is now in a release; nothing is waiting on `main`.

- v0.1.8 — the ip-info contract fix (`classification.source`)
- v0.1.7 — the scheduler guard, and the per-family task split
- v0.1.6 — the one-line installers, macOS agent builds, and `logged_in`

## Decisions waiting

### 1. A ping task can still measure two different network paths — **resolved**

**Mitigated, then fixed.** Tasks 11 and 12 targeted dual-stack hostnames. HK04 has both
families and dials IPv6; the two v4-only probes dial IPv4. One task, two paths, plotted
as comparable series — HK04 read 12.6% loss and the others 0.0%, because those numbers
describe different routes to the same hostname.

**Done:** option A below — those tasks were split per family and verified single-family
(13 tasks, `混族任务数: 0`). The two v4-only tasks now have v4-only probes, and the two
new IPv6 tasks use the dual-stack probes.

**Resolved: option C is implemented.** The agent now reports the address family it
actually used, and the panel splits both the series and the statistics per family, so a
mixed task can no longer be read as one comparable line. Full account, including what is
and is not guaranteed, in entry 4 of `SILENT-FAILURES.md`.

The options are kept for the record:

- **A** — split each dual-stack task into one per family, with probes that can only
  reach that family. *Done for the existing tasks; no code, so nothing prevented a new
  mixed one.*
- **B** — keep one task and restrict it to probes of a single family.
- **C** — have the agent report the address it actually used, and let the panel split
  the series per family. *Implemented — the durable answer, and it also closes entry 4
  of `SILENT-FAILURES.md`.*

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

### 4. Nomao's agent is still running after its node was deleted — **resolved**

Found while reading the post-deploy logs: the panel logs a `401` on
`/api/clients/v2/rpc` from `2604:abc0:50::11:601e` roughly every 25 seconds, forever.
That address is **Nomao** (`deploy/verify-cf-probe-retired.sh` carries it, and
`DEPLOY-OC424.md` lists it as the IPv6-only host). The node was deleted from the panel,
so its token no longer resolves and the agent retries with a credential that can never
work.

Confirmed on the host at the time (reachable from OC424 over IPv6):
`nekomari-agent.service` was `active (running)`, started Sep 17. There was also an
older, **not loaded** `komari-agent.service` unit file pointing at `/opt/komari-agent`
with an upstream `--auto-discovery` flag; it was not the source of the traffic.

Deleting a node in the panel does not tell the host to stop reporting. Anything that
removes a node should be paired with stopping its agent, or the panel logs a permanent
401 stream that looks like an authentication problem rather than a leftover.

**Resolved — the agent was already gone, and the leftovers are now removed.** Checked
on 2026-09-24 before deleting anything:

- The host is the one the fleet scripts manage — it carries
  `/root/cf-probe-retired-20260917-174111`, the backup directory
  `deploy/verify-cf-probe-retired.sh` creates.
- No agent is installed or running: no `nekomari-agent` or `komari-agent` unit is
  loaded or enabled, no process matches, and nothing holds a connection to the panel.
  Only two **inert** unit backups remained, `komari-agent.service.bak-20260915-213543`
  and `nekomari-agent.service.bak-month-rotate`; both were removed and
  `daemon-reload` was run. Their contents were deliberately not read — a unit file of
  this kind carries an agent token, and `SECRETS.md` forbids putting one in a document
  or a transcript.
- The 401 stream had already stopped: **zero** `401` responses in the panel's last
  three hours and **zero** log lines mentioning that address in the last 24, across a
  container that had been up for about 22 hours. So it ended before the current
  container started, not as a result of this cleanup.

Nothing else on that host referenced komari: no agent directory, no binary in
`/usr/local/bin`.

### 5. Password hashing — done in v0.1.13

Shipped: Argon2id (`m=19456 KiB, t=2, p=1`) with a random per-password salt, legacy
SHA-256 hashes verified and migrated on successful login, and bounded KDF
admission. `docs/AUTH-HARDENING.md` records the shipped format, the migration, the
rollback, and what is still only a proposal. The former plan in this entry — the
constant-salt SHA-256 description and the migration steps — is history; the code
moved on and the entry had not.

### 6. Login rate limit — done in v0.1.12

Implemented in `web/api/public/login_limiter.go`: a per-IP token bucket (burst 10,
refill 1/90s) and a per-account bucket (burst 5, refill 1/5min), checked before the
password and before the 2FA step, `429 + Retry-After` when exhausted, account bucket
cleared on success. Design and tests are in **[AUTH-HARDENING.md](./AUTH-HARDENING.md)**
and `web/api/public/login_limiter_test.go`.

### 7. The panel's Docker install option — done in the same pass

Found on 2026-09-20 while adding the NOSLA node. Two of the panel's three
install-command generators were still handing out upstream's installer
(`komari-monitor/komari-agent`), which installs a differently-shaped node —
`/opt/komari`, a `komari-agent` unit, `--auto-discovery` — from an archived project
and without the flags this fork added. Those two were migrated to this repository's
installers; `components/admin/NodeTable/NodeFunction.tsx` had been migrated earlier.
`deploy/install-node-agent.sh` also had to start accepting `--auto-discovery`: the
"add node" dialog has no token yet, and requiring `-t` made its command die.

The **docker** branch had the same problem in a different shape: it ran
`ghcr.io/komari-monitor/komari-agent:latest`, because this fork published no agent
image. It now runs `ghcr.io/aone2233/nekomari-agent`, built by `docker.yml` from the
release's `komari-agent-linux-*` assets alongside the panel image — so the binary in
the image is the binary on the release page, and a release publishes both.

Updating a container is replacing its image, so the generated command is
re-runnable: it removes the container, pulls with `--pull always`, and mounts
`/data` as a named volume for `net_static.json` (the `--month-rotate` traffic ledger)
and `auto-discovery.json` (the node's identity after registration). The command also
forces `--disable-auto-update`, since the agent cannot replace its own binary inside
a container.

## Planned — agreed, not started

### The service worker precaches every route chunk, not just the shell

Measured on the built panel: a first visit transfers **1.9 MB gzipped across 540
files**, because the precache glob covers every route chunk rather than the shell.
The editor is already excluded and loaded on demand, and the 763 KB stylesheet is not
the problem it looks like — it gzips to 96 KB, and 3 886 of its ~7 000 rules are Radix
Themes'. See `STRUCTURAL-REVIEW-2026-09-23.md` for the full table.

**Agreed direction: measure before changing.** Narrowing the precache trades offline
coverage and repeat-visit speed for first-load bytes, so the first step is to measure
what a first visit actually fetches and what the cache holds, not to guess which
chunks are "rarely used". Only then decide which routes stay precached and which fall
back to runtime caching.

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
