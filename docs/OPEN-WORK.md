# Open work

State of the batch that started 2026-09-18. Written so a later session can pick up
without re-deriving anything.

## Status

| Item | State |
|---|---|
| One-line agent installer | **Done and released** in v0.1.6 |
| `logged_in` missing from `/api/public` | **Done and released** in v0.1.6 |
| macOS agent builds | **Done and released** in v0.1.6 |
| Scheduler accepting a job that never runs | **Fixed, not released** |
| Silent-failure catalogue | **Done** — 4 entries, 2 real, 2 latent |
| LuminaPlus IP panel | **Open** — one real bug fixed, remaining cause unexplained |
| Family mixing within one ping task | **Diagnosed, not fixed** — needs a decision |

Production runs `ghcr.io/aone2233/nekomari:v0.1.6`. Nothing below is urgent.

## Unreleased

Two commits sit on `main`, both hardening, neither in a release:

- `f534f19` — `AddContextFunc` rejects a cron spec whose next run is the zero time.
  Such a job used to be accepted, then silently never executed. Tests included.
- `581bb2d` — runs those tests in the CI filter.

Roll them into the next release. No migration, no config change.

## Decisions waiting

### 1. A ping task can measure two different network paths

**The only open item that affects live monitoring.** Tasks 11 and 12 target
dual-stack hostnames. HK04 has both families and dials IPv6; the two v4-only probes
dial IPv4. One task, two paths, plotted as comparable series — HK04 reads 12.6% loss
and the others read 0.0%, because those numbers describe different routes to the
same hostname.

Options:

- **A** — split each dual-stack task into one per family, with probes that can only
  reach that family. No code, immediate.
- **B** — keep one task and restrict it to probes of a single family.
- **C** — have the agent report the address it actually used, and let the panel split
  the series per family. This is the real fix and also closes entry 4 of
  `SILENT-FAILURES.md`; it needs protocol and frontend work.

A is recommended as the immediate step, C as the durable one.

### 2. Whether to keep chasing the IP panel

Four rounds of investigation are recorded in `IP-INFO-API.md`, including three of my
own wrong conclusions. What is established:

- every server-side endpoint the panel depends on returns 200 with correct data
- the mainland-China region gate is not the cause (tested a US and a HK node)
- the WebSocket authentication noise is not the cause (it is pre-login only)
- `logged_in` **was** genuinely missing from `/api/public` and is now fixed — this
  was necessary, and not sufficient
- instrumenting the theme bundle did not work: the page's own fetch returns the
  patched text, with Cloudflare, browser cache and the service worker all ruled out,
  and the injected global is still never set

Recommended next step is a **theme swap**: if another theme shows its IP panel, the
remaining cause is inside LuminaPlus and the server should be left alone. Continuing
to instrument the bundle has poor return — that is where the four rounds went.

### 3. MAC-WAN stalls

`deploy/ping-spike-shape.py` shows the 教育网 "jitter" is MAC Server alone: 10 of 10
spike minutes, and it spikes on every task it runs (max 635/576/349 ms against
p50 3/9/13 ms). So it is local to that host, not the target. MAC-WAN is both the
probe host and the backup host, which may be relevant. Not investigated.

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

## Things that will bite again

**2FA breaks password-only automation.** Every script that logs in must go through
`nekomari_auth.py` with `NEKOMARI_2FA_SECRET` set. The failure looks like a broken
script rather than a rejected login.

**`/api/me` returns 200 for guests.** Check `logged_in`, not the status code.

**Test theme changes against the origin on the host.** Through Cloudflare a cache hit
and dead code are indistinguishable — that mistake cost a round.

**Do not reason about minified bundles.** Two readings both predicted the gate should
pass while the tab stayed hidden. Instrument, or leave it.

**The theme bundle on the panel host has been patched and reverted twice.** Both
times it was confirmed byte-identical to its backup afterwards; `/tmp/Instance.orig.js`
holds the original if it is needed again.
