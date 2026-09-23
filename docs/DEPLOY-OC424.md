# Public deployment (OC424 / komari.orderly2233.org)

The live Nekomari panel. This file is the runbook: what runs where, how it was
restored, and the two hostname/TLS traps that cost the most time.

## Topology

| Piece | Where |
|---|---|
| Panel URL | **https://komari.orderly2233.org** |
| Host | OC424 (`ubuntu@213.35.99.48`, Oracle Cloud, **arm64**, Ubuntu 22.04) |
| Container | `nekomari`, image `ghcr.io/aone2233/nekomari:v0.1.23`, bound to `127.0.0.1:25774` |
| Compose dir | `/opt/nekomari` (bind mount `./data` → `/app/data`) |
| Reverse proxy | host **nginx** `/etc/nginx/sites-available/nekomari` |
| TLS at origin | `/etc/nginx/ssl/{fullchain,privkey}.pem` (Cloudflare Origin cert, shared with the other vhosts) |
| Edge | Cloudflare proxied A → `213.35.99.48` |
| Agent | `komari-agent-oc424-original-node.service` (source: `deploy/hosts/oc424.service`), arm64 agent reporting this host to the local panel |
| Old data | backed up by the server on first boot to `data/backup/upgrade-*.zip` |

The container is deliberately **not** published on a public interface: UFW allows
80/443 only from Cloudflare's ranges, so all traffic arrives via the edge, and
nginx is the only thing that talks to the panel port.

## Current rollout: 2026-09-23 (v0.1.23)

Panel upgraded from v0.1.22 at approximately 07:12 UTC. Origin and public
version APIs reported `v0.1.23`, hash `7c82708`. See
[the follow-up review](FOLLOWUP-v0.1.23.md) for the frontend changes and
remaining work.

- The tag and merged main pointed to
  `7c82708ee45a5b5a6ae08dc06220ec7e8aab5f2f`. Exact-commit CI
  `35829087706` passed all three jobs before release. Release `35829551439`
  passed all eight jobs, including downloaded-artifact deployment with an
  agent; Docker `35830057800` passed both image jobs and their startup smoke
  tests. Both package manifests independently returned HTTP 200 with anonymous
  tokens. The Release published eight binaries and `SHA256SUMS.txt`.
- The panel image index was
  `sha256:1b6116b7663b16724097541733fc7881a152ca4f12aa4ff102065e5113616182`.
  Its arm64 `/app/nekomari` SHA256 matched the published arm64 binary:
  `f5e2088a4062aa5118f3fe7f8f0882c9bb6bb21b35fbe81c0d59434fe80d77fa`.
  The image revision label matched the tagged commit. The agent, protocol,
  internal and shared-package source did not change, so the v0.1.19 agent
  fleet was left intact.
- Pulled and inspected the image before stopping writes. The stopped panel's
  Compose file and complete bound data were backed up under
  `/opt/nekomari-backups/pre-v0.1.23-20260923-071131` (mode 0700).
  `data.tar` SHA256 was verified before and after deployment:
  `8c24fbefc02979aec004beb710e4484797bd818746074d28e9d890bbc08c0501`.
  Archive listing included `data/komari.db` and `data/metrics.db`; the
  previous Compose file and image were retained for rollback.
- After the image swap, both SQLite databases passed `quick_check`, and the
  panel remained healthy with zero restarts. The 9 registered clients all
  had fresh metric buckets (approximately 8-12 seconds old at 07:14 UTC).
  The public homepage returned HTTP 200; the actual admin backup-download
  and client-list routes returned HTTP 401 without a session from both origin
  and public paths. nginx configuration passed; no ERROR/FATAL lines appeared
  in the initial post-upgrade logs.
- The loopback-only port and data bind mount remained intact. The active
  `panel-probe.timer` and a manually run probe completed successfully at
  07:14:51 UTC (`origin=0.002s`, `public_max=0.285s`, 5/5 samples). These are
  spot checks, not a long-term latency or load measurement.

Rollback: restore the retained
`/opt/nekomari/docker-compose.yml.bak-pre-v0.1.23` into `/opt/nekomari` and
start the retained v0.1.22 image. Recheck the version, both database
`quick_check` results and recent metrics from all registered nodes. If data
restoration is needed, stop the panel, preserve the current data separately,
verify `data.tar` against the SHA256 above, and restore it before restarting.
Never extract an archive over a running database.

## Previous rollout: 2026-09-23 (v0.1.22)

Panel upgraded from v0.1.21 at 05:51:40 UTC. At that rollout, origin and public
version APIs reported `v0.1.22`, hash `5f71703`. See [the follow-up review](FOLLOWUP-v0.1.22.md)
for the code changes and remaining work.

- PR #10 merged as `5f717037ccb2e7124747d0b097aa8b9ed2d954be`.
  Exact-commit CI `35822804305` passed the frontend browser, Ubuntu, and Windows
  jobs before the tag was pushed at that commit.
- Release `35823140745` passed all eight jobs, including scans and installation
  from downloaded release artifacts with a reporting agent. Docker
  `35823618102` passed both image jobs, with anonymous manifest fetches (HTTP 200).
- Panel image index: `sha256:53e500db6a1811c2b488647ae3d162c56637a68b38352dd2ab2b4d9c5ed94a5e`.
  Its arm64 executable SHA256 is
  `d0365e582567322059f7da5e5992ab43f5a6a0cdcd43c44e3ca889d0fc8e438a`,
  matching the published Release checksum. The agent image executable also
  matched its published checksum; its source did not change in this release.
- Pulled and inspected the image before stopping writes. Compose and the complete
  data directory were backed up at
  `/opt/nekomari-backups/pre-v0.1.22-20260923-054850` (mode 0700);
  `data.tar` SHA256:
  `c5ffbc69aa65815ffaaa6379eb8150479a4d6c420bb236f25b03eaf050f5f0c4`.
  The first archive-listing pipeline exited 141 under `pipefail`, so the trap
  restarted the unchanged v0.1.21 panel. A separate full listing and Compose
  comparison then verified the backup before the v0.1.22 image swap.
- Both SQLite databases passed `quick_check`; the 9 clients, 1 user, and 9 ping
  tasks remained. All 9 current client IDs had new metric buckets between
  05:51:55 and 05:51:59 UTC, after the new container started. Homepage returned
  HTTP 200, while the actual admin backup and private recent-history endpoints
  returned HTTP 401 without a session.
- The data bind mount and loopback-only port remained intact; container restart
  count was zero, nginx configuration passed, the probe timer was active, and
  no ERRO/FATAL lines appeared in the initial post-upgrade window. The agent
  fleet retains v0.1.19 because agent, protocol, and shared-package source is
  unchanged from v0.1.21.
- At 06:03-06:04 UTC, all 9 client `updated_at` values had advanced past the
  container start (earliest 05:55:26, latest 06:00:44), and all 9 current
  client metric buckets reached 06:03:54-06:03:58 UTC. The 05:56:18 UTC probe
  completed successfully (`origin=0.002s`, `public_max=0.092s`, 5/5 samples),
  and the container remained healthy with zero restarts or matching database
  error, ERRO, FATAL, or PANIC log lines. These timings are spot checks, not
  long-term performance measurements.

Rollback: restore this backup's `docker-compose.yml` into `/opt/nekomari` and
start the retained v0.1.21 image. If data restoration is needed, stop the panel,
preserve the current data separately, and restore the archive before restarting.
Never extract an archive over a running database.

## Previous rollout: 2026-09-23 (v0.1.21)

Panel upgraded from v0.1.20 at 01:40 UTC. Origin and public version APIs report
`v0.1.21`, hash `8d0a8d1`. See [the follow-up review](FOLLOWUP-v0.1.21.md)
for scope, validation limits and the remaining compiler/chart work.

- PR #8 merged as `8d0a8d124b48538702355686916b26eb8d6b877e`. Exact-commit
  CI `35751946083` passed before the immutable release tag was pushed.
- Release `35806607898` passed all eight binary vulnerability scans and the
  downloaded-artifact installation/agent-report test. Docker `35807060048`
  passed both startup smoke tests and anonymous manifest fetches (HTTP 200).
- Image: `sha256:63bce6b943230d3d52d0d2dc29149e73aeb1f8da6cc72e33751b9031fab8a6e8`.
  The arm64 binary downloaded from the Release and the executable inside the
  image both match published SHA256
  `22f1151a7badd2d577e46305da128699f26acfd32c14b1db47d846ac38e56f2e`.
  An independent binary scan found no affected symbols.
- Pulled and checked the image before stopping writes. Complete data and Compose
  backup: `/opt/nekomari-backups/pre-v0.1.21-20260923-014038` (mode 0700).
  Validated the archive listing; `data.tar` SHA256:
  `da296fea3aba7033b23e8f230226a716b8950c9050c29893b27392155c77c4ea`.
- Both databases pass `quick_check`; 9 clients, 1 user and 9 ping tasks remain.
  Public homepage returns HTTP 200. Data still binds to `/opt/nekomari/data`,
  the port stays loopback-only, restart count is zero, and no ERRO/FATAL lines
  were observed in the initial post-upgrade window. The probe timer is active.
  By 01:43 UTC, all 9 current client IDs had metric buckets newer than the
  container's 01:40:42 UTC start time; the latest rollup reached 01:42 UTC.
- Agent/protocol/shared-package source is unchanged from v0.1.20. The nine
  agents retain v0.1.19; this rollout does not require fleet restarts.

Rollback: restore this backup's `docker-compose.yml` into `/opt/nekomari` and
start the retained v0.1.20 image. If data restoration is needed, stop the panel,
preserve the current data separately, and restore the archive before restarting.
Never extract an archive over a running database.

## Previous rollout: 2026-09-22 (v0.1.20)

Panel upgraded from v0.1.19 to v0.1.20 at 15:02 UTC. Runtime API reports hash
`5f82c78`. **The agents did not move with it and do not need to**: `git diff
v0.1.19..v0.1.20 -- agent/ protocol/ pkg/` is empty, so the agent source is
byte-identical and the fleet stays on v0.1.19 — the same reasoning as the v0.1.15
rollout. This release is the follow-up hardening batch from
[next improvements](NEXT-REVIEW-v0.1.19.md): the probe fix, the upload disk budget
and cleanup observability, the lock-contention measurement, the OAuth admission
work, and the Recharts 3 / ESLint 10 upgrades.

- Exact release commit: `5f82c788b6b8b783c10a4e1a49e432b21c1546cb`.
- Exact-commit CI on `main`: `35742632268`, Linux and Windows both passed.
- Release: `35743235246` — all eight binaries built and passed
  `govulncheck -mode=binary`, the Release was created, and the real
  downloaded-artifact deployment test (`deploy/deploy-verify.sh`, which completes
  the first-run install, connects an agent and asserts the version it reports)
  passed.
- Container release: `35744018445`, both images pushed.
- Panel image: `sha256:213751eb00e1b375f903299a4304f52fc7b23dbf8f2ce62a7d8dfb3cc3844aba`
  (v0.1.19 was `sha256:52e36dad…`, retained for rollback).
- Arm64 executable SHA256 verified against the published `SHA256SUMS.txt`;
  the artifact is `not stripped`, so the symbol tables v0.1.19 restored are intact
  and the version string reads `v0.1.20`.
- Stopped writes, then backed up Compose and the complete data directory to
  `/opt/nekomari-backups/pre-v0.1.20-20260922-150235` (mode 0700).
  `data.tar` SHA256: `528578aa10a67f4956afb36d51e0fc9d96e8cfde13ecd7ef4b50459654cf97e6`
  over 366 entries; the listing was checked before the swap. The image was pulled
  *before* stopping the container, so the downtime was the recreate only.
- The panel wrote its own pre-migration archive on first boot:
  `upgrade-20260922-150239.zip`, logged as `from "v0.1.19-31b3ee5" to
  "v0.1.20-5f82c78"` with the local metric store excluded and left in place.

Verified after the rollout:

- `/api/version` → `{"hash":"5f82c78","version":"v0.1.20"}`; the origin and the
  public URL both answered 200.
- Nine of nine agents reconnected and kept reporting: every node's `updated_at`
  advanced within the following ten minutes (they are staggered by a few minutes
  each, so a single snapshot can look stale — comparing two snapshots is what
  settles it), and the metric store took writes continuously, newest rollup
  15:05:00 across 336 series.
- `pragma quick_check` on `komari.db` → `ok`; 9 clients, 1 user and 9 ping tasks
  retained; container restart count 0; zero ERRO/FATAL lines since start.
- The health probe kept logging healthy samples (`origin=0.002s`) across the
  restart.

Rollback: restore `/opt/nekomari/docker-compose.yml.bak-pre-v0.1.20` and start the
retained v0.1.19 image. For data, stop the container, preserve the current `data`
separately, and restore `pre-v0.1.20-20260922-150235/data.tar` before restarting.
Never extract a rollback archive over a running database.

## Panel upgrade (2026-09-22) — v0.1.19

Panel upgraded from v0.1.16 to v0.1.19 at approximately 09:53 UTC. Runtime API
reports hash `31b3ee5`. This rollout changed only the panel; the nine agents were
refreshed to the same tag separately — see
[the fleet agent refresh](#fleet-agent-refresh-2026-09-22-v0119-toolchain) below.

- Exact release commit: `31b3ee512a469bade95de667486f133a029c38a7`.
- Exact-commit CI: `35711635197`, both Linux and Windows passed.
- Release: `35712077446`, all eight binary vulnerability scans and the real
  downloaded-artifact deployment test passed.
- Container release: `35712642893`, both images passed smoke/public-pull checks.
- Panel image: `sha256:52e36dadee990abfb66dd440c54edc1e60f6f037b446184c1be64c73b4fb256e`.
- Arm64 executable SHA256: `e7a845a698986e8f9e4ceee2de6d00e6aa61a8d1307afeeccfabd7909f1c7976`.
  Independently matched the published checksum and the binary inside the image;
  downloaded binary govulncheck found no affected symbols.
- Stopped writes before backing up Compose and the complete data directory to
  `/opt/nekomari-backups/pre-v0.1.19-20260922-095308` (directory mode 0700).
  `data.tar` SHA256: `b867c42aa434a24f30bab522160d1ac532f01cc41ef9af1197a648e8c2dfb4d4`.
  Archive listing was validated before replacing the container. Old image retained.
- Origin/public version API and homepage returned 200; unauthenticated admin
  session API returned 401; disabled OAuth start/callback returned 403.
- Both SQLite `quick_check` results were `ok`; 9 clients, 1 user and 9 ping tasks
  were retained. Port remains loopback-only, data remains `/opt/nekomari/data`,
  nginx configuration passes, and container restart count is zero.
- Repeated database samples confirmed all nine latest metric buckets advanced
  past the restart; the follow-up sample had a maximum age of 73 seconds.
  No ERRO/FATAL lines were observed. Anonymous recent-history requests returned
  401, so verification did not relax access controls.
- Initial public homepage TTFB was 0.087 seconds from OC424; this is a single
  observation, not a performance benchmark.

v0.1.17 was not deployed: its workflow selected Go 1.26.0. v0.1.18 publication
was blocked by conservative scanner fallback on stripped binaries. v0.1.19 uses
Go 1.27.1 and retains symbol tables, without suppressing advisories. See
[next improvements](NEXT-REVIEW-v0.1.19.md) for evidence and acceptance criteria.
Authenticated admin uploads/restores and real OAuth provider login were not
exercised against production. Earlier sections below are historical records.

For rollback, restore the backed-up Compose file and start the retained v0.1.16
image. If data rollback is necessary, stop the container, preserve the current
data separately, and restore the consistent archive before restarting. Never
extract a rollback archive over a running database.

## Fleet agent refresh: 2026-09-22 (v0.1.19 toolchain)

The nine agents were still on v0.1.16 after the panel moved to v0.1.19. The agent
**source** did not change between those tags — the reason to move is the compiler:
v0.1.16 was built with Go 1.26.0, whose standard library carried 34 govulncheck
findings, while v0.1.19 pins Go 1.27.1 and ships symbol tables so the distributed
executable itself stays scannable. This is a compiler refresh, not a feature
change; no agent behaviour differs.

Method: the release assets were downloaded and verified against the published
`SHA256SUMS.txt` locally (`a6930033…` amd64, `d79f378a…` arm64), then copied to
each node and installed with `deploy/install-staged-agent.sh` — the staged path
exists because PZYC cannot reach the release assets, and one download per node is
one failure mode per node. Per node the old binary is kept as
`<bin>.bak-pre-v0.1.19`, owner, group and mode are preserved, `cap_net_raw` is
restored where it existed, and the unit is restarted and checked `active`.

| Node | Binary | Unit | Before | After |
|---|---|---|---|---|
| 甲骨文 OC424 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` | `a30b5445…` | `d79f378a…` |
| 华纳云 HN-JP1 | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | `394d9ad0…` | `a6930033…` |
| HK04 | same | same | `394d9ad0…` | `a6930033…` |
| AkkoCloud SJ | same | same | `394d9ad0…` | `a6930033…` |
| BandwagonHost MegaBox | same | same | `394d9ad0…` | `a6930033…` |
| 并行智算云 PYZC | same | same | `394d9ad0…` | `a6930033…` |
| MAC Server | `/home/macos/nekomari-agent/komari-agent-linux-amd64` | user `nekomari-agent.service` | `394d9ad0…` | `a6930033…` |
| NOSLA 东京-26秋-M | `/opt/komari/agent` | `komari-agent.service` | `394d9ad0…` | `a6930033…` |
| CloudLeadInno | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | `394d9ad0…` | `a6930033…` |

Two nodes needed the paths the earlier rollouts established:

- **MAC Server** runs a *user* unit with no passwordless sudo, so
  `install-staged-agent.sh` (which resolves a system unit and calls `setcap`
  directly) does not apply. The binary was installed as `macos:macos` and the
  unit restarted with `XDG_RUNTIME_DIR=/run/user/1000 systemctl --user`; the
  install dropped `cap_net_raw=ep` as it always does, and it was restored with the
  host's own `setcap` through a privileged container
  (`docker run --rm --privileged -v /:/host alpine chroot /host setcap
  cap_net_raw+ep <bin>`), verified with `getcap` before and after.
- **CloudLeadInno** still refuses both keys held on this workstation. The working
  path is the `CLISP` entry in MAC-WAN's `~/.ssh/config`: MAC-WAN holds the key
  that authenticates as `root@192.220.32.17`, so the staged binary and installer
  were copied to MAC-WAN and from there to the node. This is simpler than the
  panel remote-exec route in `deploy/panel-agent-upgrade.py`, which needs an
  authenticated admin session plus 2FA and only works because the node runs the
  agent with web-ssh enabled.

Verified after the rollout: `select name, version from clients` on the panel's
`data/komari.db` reports `v0.1.19` for all nine, each with a fresh `updated_at`,
and the container logged zero ERRO/FATAL lines across the window. Rollback per
node is `install -m 755 <bin>.bak-pre-v0.1.19 <bin>` plus a unit restart, with the
capability dance again on MAC.

## Health probe: the fix is deployed (2026-09-22)

`deploy/panel-probe.sh` on OC424 was replaced with the corrected version
(`/usr/local/bin/panel-probe.sh`, previous file kept as
`panel-probe.sh.bak-pre-v0.1.20`). The old script recorded `time_starttransfer`
without checking curl's exit status or the HTTP status, so a fast HTTP 500 — or a
connection refused, which returns `0.000000` — was logged as a healthy sample and
exited 0.

The replacement requires curl success, HTTP 200 and a valid success envelope
before a sample can count as healthy, and separates the failure reasons
(`refused` / `timeout` / `status-NNN` / `no-timing` / `envelope`) in both the log
line and the alert. The healthy log line is byte-identical to the old format, so
existing log parsing is unaffected; failures append `origin_fail=` /
`public_fail=`.

Verified on the host, not just locally:

- `deploy/panel-probe.test.sh` — 88 assertions, 0 failures on OC424's Ubuntu
  22.04 and Python 3.10, including the real closed-port refusal path that cannot
  be reproduced on Windows (a closed loopback port there is dropped rather than
  refused).
- A healthy run logs
  `origin=0.002s public_max=1.307s pop=MXP cache=DYNAMIC samples=5/5` and exits 0.
- A run with `PANEL_ORIGIN` pointed at a closed port exits **1** and logs
  `origin=0.000s … origin_fail=refused`, with the alert attributing it to the
  panel rather than the edge. The old script logged the same run as healthy.
- `systemctl start panel-probe.service` (exactly what the 10-minute timer runs)
  reports `ExecMainStatus=0` on a healthy panel, and the timer remains scheduled.

What the corrected probe immediately showed, and what it means: the origin has held
at `origin=0.002s` on every run, while the public path ranges from 0.17 s to
**26.9 s** depending on which POP answers. The slow runs cluster on US-East POPs —
IAD and EWR at 3.0-5.6 s, MIA at 3.6 s — where the Asian and European POPs that
usually serve this origin (SIN, HKG, KIX, CDG, MRS, MXP) sit at 0.2-1.5 s. Two
outliers of 26.9 s and 15.97 s (both HKG) predate the probe fix and would have
alerted under the old script too, so this is not a regression from it: it is the
edge path, and the probe is now recording the distinction between "the panel is
slow" and "this POP is far away" that it was written to make. `PUBLIC_MAX`
(default 3.0 s) is the knob if the alert rate becomes noise.

## Data restoration

The panel was restored from the retired Komari production instance rather than
started empty:

| Source | What |
|---|---|
| `komari.db` (260 KB) | 9 nodes with names/groups/tags/pricing, ping tasks, theme configs, message-sender providers, the admin user |
| `metrics.db` (141 MB) | 629 series / 318,570 rollups / 90 days |

Both were copied byte-for-byte (checksums compared on both ends) into
`/opt/nekomari/data` **before** first start, so the server adopted them instead
of running the install wizard. The metric-store schema in the archive is
identical to what v0.1.2 creates, so no structure migration was needed.

### What the retention policy removed (by design)

On first boot the server logged `Metric retention cleanup deleted 211312 rows`.
That is Nekomari's rollup tiering, not corruption:

| Tier | Retention | Effect on the restored data |
|---|---|---|
| 1-minute | 600 min | expired (the archive ended 2 days earlier) |
| 5-minute | 3000 min | expired |
| 1-hour | 600 h (25 d) | kept → 71,880 rows, back to ~2026-08-23 |
| 1-day | 100 years | kept → 7,538 rows, the full 90 days |

So the **90-day daily curve survived in full**, and the fine-grained minutes are
gone. That matches what a long-running instance looks like anyway. To keep more
history on a future restore, raise
`metric_rollup_{minute,five_minute,hour}_retention_*` **before** importing.

### Admin access

The restored user is `AONE2233` (not `admin`). Its password was unknown and it had
2FA enabled, so access was recovered with the built-in tools:

```bash
docker compose stop
docker compose run --rm --entrypoint /app/nekomari nekomari chpasswd -p '<new>'
docker compose run --rm --entrypoint /app/nekomari nekomari disable-2fa
docker compose up -d
```

The password and 2FA state above describe the historical restoration. A
read-only database check on 2026-09-23 confirmed that the current account has
2FA enabled; do not infer current authentication state from these old commands.

## The two hostname traps

### 1. A 2nd-level subdomain gets no Cloudflare edge certificate

`nekomari.orderly2233.org` had to be abandoned in favour of the 1st-level
`komari.orderly2233.org`. Measured, not assumed:

| Hostname | Depth | HTTPS |
|---|---|---|
| `orderly2233.org` | apex | 200 |
| `git` / `vault` / `blog` / `dav` / `subnc`. | 1st-level | 200 |
| `nekomari.` | 2nd-level | **000** (edge aborts the handshake) |
| `ssh.git.` | 2nd-level | **000** |

Cloudflare's Universal SSL is `*.orderly2233.org`, which covers one label only.
A 2nd-level name needs Total TLS, an advanced certificate, or a 1st-level name.
Changing hostname was the cheapest correct fix.

### 2. "No DNS record" can look like "record exists"

Before the record existed, this machine's resolver happily answered
`nekomari.orderly2233.org → 198.18.6.15` — an intercepting proxy on the local
network that answers *every* name. Authoritative DNS (Cloudflare DoH) showed the
truth: `Status: 0` with an empty `Answer`. **Verify DNS against an authoritative
resolver, never the local one.**

The recorder was repointed from an unused tunnel CNAME to the origin, matching the
pattern every working hostname in the zone already uses:

```
A  komari.orderly2233.org  213.35.99.48  proxied
```

The zone's `cloudflared` tunnel is **token-managed** (its ingress lives in the
Cloudflare dashboard), so tunnel hostnames cannot be added from the host.

## Reproducing

```bash
# 1. origin
sudo mkdir -p /opt/nekomari/data && sudo chown ubuntu: /opt/nekomari
#    put komari.db + metrics.db into /opt/nekomari/data (optional), then:
cd /opt/nekomari && docker compose up -d

# 2. nginx (see deploy/nginx-nekomari.conf)
sudo cp deploy/nginx-nekomari.conf /etc/nginx/sites-available/nekomari
sudo ln -sf /etc/nginx/sites-available/nekomari /etc/nginx/sites-enabled/nekomari
sudo nginx -t && sudo systemctl reload nginx

# 3. agent (source of truth: deploy/hosts/oc424.service)
#    On the host the unit is called komari-agent-oc424-original-node.service,
#    because it reports as the restored node "甲骨文 OC424" (see the unit's
#    WorkingDirectory and token comment).
sudo cp deploy/hosts/oc424.service /etc/systemd/system/komari-agent-oc424-original-node.service
sudo systemctl enable --now komari-agent-oc424-original-node.service
```

## Notes

- The panel inherits `private_site: true` from the old production config, so the
  API answers 401 without a session and the SPA shows the login gate. That is the
  restored setting, not a deployment fault.
- nginx needs the WebSocket upgrade headers and a long `proxy_read_timeout`;
  with the default 60 s the agents visibly flap on and off the panel.
- `X-Real-IP` prefers `CF-Connecting-IP` and falls back to `$remote_addr`, so the
  panel records the visitor's address rather than Cloudflare's edge.

## Reconnecting the nodes (9/9 online)

Restoring `komari.db` brought back all nine nodes' metadata, but their agents had
been retired with the old panel, so nothing was reporting. Each node is now back
with its **own** token from the recovered database — deliberately not
`--auto-discovery`, which would have registered a *new* node and orphaned the
restored one's group, tags, pricing and historical series.

`deploy/install-node-agent.sh` does this for a node: rejects an install if an
agent is already active for that host, downloads the agent from the release,
verifies it against `SHA256SUMS.txt`, disables any older agent unit so only one
process reports per host, and starts a `nekomari-agent.service`.

| Node | How it was reached |
|---|---|
| 甲骨文 OC424 | local, reusing the node's token |
| 华纳云 HN-JP1, HK04, AkkoCloud | direct root SSH |
| BandwagonHost (megabox) | SSH config, port 9950 |
| 并行智算云服务器 | SSH config `PZYC`, non-root + passwordless sudo |
| MAC Server (MAC-WAN) | **user** unit — that host has no passwordless sudo |
| Nomao v6_1 | IPv6 only; reachable from OC424 |
| CloudLeadInno | root SSH from OC424 (the key is not present on every machine) |
| NOSLA 东京-26秋-M (`TG`) | added 2026-09-20; root SSH from the workstation. First enrolled with `--auto-discovery`, then switched to its own node token |

### Two things worth knowing

**PZYC cannot reliably fetch release assets.** `github.com` answers, but
`objects.githubusercontent.com` resets the TLS connection, so `curl` on the
release URL fails intermittently and a timed-out download leaves a truncated
file. The binary was fetched elsewhere, checksum-verified, and installed by
`deploy/hosts/pzyc.sh`, which skips the download.

**MAC-WAN runs the agent unprivileged**, because that host has no passwordless
sudo and user units are the only option. The agent therefore cannot open raw
ICMP sockets, so ping tasks 17/18/19 (`原生电信/移动/联通IP`, type `icmp`) fail
there with `operation not permitted` while succeeding from the root agents. The
fork's `auto` task type does not help for those specific targets: they are bare
IPs, and `auto` falls back to TCP 443/80, which these hosts do not answer. Either
grant the binary `cap_net_raw` or leave those three tasks to the root nodes.

## Fleet upgrade (2026-09-20) — v0.1.14

The panel moved from v0.1.12 to v0.1.14 (compose pin in `/opt/nekomari`, previous
file kept as `docker-compose.yml.bak-pre-v0.1.14`; the server wrote
`data/backup/upgrade-20260920-130005.zip` on first boot and logged
`from "v0.1.12-9d11cfa" to "v0.1.14-6ed979b"`), and every node's agent moved with
it. All nine nodes report `v0.1.14` in the panel afterwards.

The agent binaries were fetched **once**, verified against the published
`SHA256SUMS.txt`, and copied to each host rather than downloaded per node: PZYC
cannot reach `objects.githubusercontent.com` at all, and a per-node download adds one
failure mode per node. On each host the copy was verified again, the old binary kept
as `<bin>.bak-pre-v0.1.14`, the new one installed with the previous owner and mode,
the file capability restored where there was one, and the unit restarted.

| Node | Reached as | Arch | Binary | Unit |
|---|---|---|---|---|
| 甲骨文 OC424 | local | arm64 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` |
| 华纳云 HN-JP1 | `HNJP01` from OC424 | amd64 | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` |
| HK04 | `HK04` from OC424 | amd64 | same | `nekomari-agent.service` |
| AkkoCloud SJ | `AKKO06` from OC424 | amd64 | same | `nekomari-agent.service` |
| BandwagonHost MegaBox | `megabox`, direct or from OC424 | amd64 | same | `nekomari-agent.service` |
| CloudLeadInno | `CLISP` from OC424 | amd64 | same | `nekomari-agent.service` |
| 并行智算云 PZYC | direct | amd64 | same | `nekomari-agent.service` |
| MAC Server | direct | amd64 | `/home/macos/nekomari-agent/komari-agent-linux-amd64` | user `nekomari-agent.service` |
| NOSLA 东京-26秋-M | `TG` | amd64 | `/opt/komari/agent` | `komari-agent.service` |

Three things the fleet is not uniform about, each of which the upgrade had to handle:

- **The binary path differs.** `/opt/nekomari-agent/...` on six nodes, the user's home
  on two, and `/opt/komari/agent` on NOSLA. The path is taken from the running
  process, never guessed — the same reason `upgrade-agent.sh` does it that way.
- **MAC Server cannot run `setcap`.** It has no passwordless sudo, and replacing the
  binary drops the file capability it needs for raw ICMP. It was restored with a
  privileged container that chroots into the host rootfs and runs the host's own
  `setcap`: `docker run --rm --privileged -v /:/host alpine chroot /host setcap
  cap_net_raw+ep <bin>`. `getcap` was checked before and after.
- **NOSLA used `--auto-discovery`, and no longer does.** Its first install followed
  the panel's install dialog, which was still handing out upstream's installer — see
  the open item in `docs/OPEN-WORK.md`. Auto-discovery registers a *new* client on
  every start that does not find a saved token
  (`web/api/client/autoDiscovery.go` calls `CreateClientWithName` unconditionally),
  so the node's identity depended on `/opt/komari/auto-discovery.json` surviving; a
  lost file would have produced a duplicate node. It now carries its own token like
  the rest of the fleet, with the file left in place but unused. Its unit is kept as
  `deploy/hosts/tender-guard.service`.

  Its flags were aligned with the fleet in the same pass, with one node-specific
  value: `--month-rotate 20`. That is the node's billing day, not a fleet constant —
  the live fleet runs HNJP01=18, HK04=11, AKKO06=12, megabox=7, CLISP=11, each
  matching its own `expired_at` day (see `deploy/set-month-rotate.sh`). Measured
  effect, from the metric store: before the flag the node reported 40.6 GiB up /
  18.8 GiB down, the raw kernel counter since boot; after the restart it reports the
  cycle-to-date deltas netstatic records — 0.0 at first, growing — which is the
  figure the provider's dashboard is comparable to. netstatic only runs when the flag
  is set, so this node had no `net_static.json` until then.

`deploy/upgrade-agent.sh` was fixed while doing this. It decided "already at the
target version" by running `"$BIN" --version`, and the agent has no such flag (it
answers `unknown flag: --version`), so that branch never ran and every invocation
re-downloaded and reinstalled the binary. It now compares the installed binary's
SHA-256 against the published `SHA256SUMS.txt`.

The fleet-wide check needs no panel login: each agent reports its version in its
basic-info upload, so `select name, version from clients` on `data/komari.db` is the
answer. It read `v0.1.11` for eight nodes and `v0.1.13` for NOSLA before, and
`v0.1.14` for all nine after.

## Panel upgrade (2026-09-21) — v0.1.15

The panel moved from v0.1.14 to v0.1.15 and **the agents did not move with it**: this
release touches only `frontend/`, `deploy/`, `docs/`, `Dockerfile.agent` and
`.github/workflows/docker.yml`. `git diff --stat v0.1.14..v0.1.15 -- agent protocol pkg
internal` is empty, and the two `komari-agent-linux-amd64` binaries differ only in the
version string, the Go build id and the embedded `vcs.*` metadata — so all nine nodes
keep reporting `v0.1.14` on purpose.

Recorded from the host: compose pin updated in `/opt/nekomari/docker-compose.yml` with
the previous file kept as `docker-compose.yml.bak-pre-v0.1.15`; the server wrote
`data/backup/upgrade-20260920-145936.zip` on first boot and logged
`from "v0.1.14-6ed979b" to "v0.1.15-9bacdf7"`; the banner read
`Nekomari Monitor v0.1.15 (hash: 9bacdf7)`; eight agents re-established their WebSocket
in the same second the container started, and `/api/version` answered
`{"hash":"9bacdf7","version":"v0.1.15"}`.

Two follow-ups from the same day, both in `docs/PERFORMANCE.md`: the LuminaPlus theme's
background images were re-encoded (6.34 MB → 0.51 MB for the desktop one) and the theme
settings repointed at new filenames, and `data/backup` was pruned from 1.4 GB to 221 MB
with `deploy/prune-upgrade-backups.sh` now running weekly from a systemd timer.

## Fleet upgrade (2026-09-22) — v0.1.16

The optimization batch in `docs/OPTIMIZATION-REVIEW-2026-09-22.md`. Unlike v0.1.15 this
one **does** change the agent (socket counting reads `/proc/net/sockstat`; the traffic
sampler moved from 2 s to 30 s and rewrites its ledger less often), so the panel and all
nine nodes moved together.

Panel: compose pin `v0.1.16`, previous file kept as `docker-compose.yml.bak-pre-v0.1.16`;
banner `Nekomari Monitor v0.1.16 (hash: 4f26aad)`; `/api/version`
`{"hash":"4f26aad","version":"v0.1.16"}`; 0 ERRO/FATAL lines since start; nine agents
reconnected. The upgrade also exercised two of the fixes:

- the archive is now taken **before** the migrations and excludes the metric store —
  `upgrade-20260922-042859.zip` is **26 MB** where the previous three were 62-100 MB, and
  the log says so: `backed up … before upgrade (from "v0.1.15-9bacdf7" to
  "v0.1.16-4f26aad"); the local metric store is excluded and left in place`.
- the agent's persisted traffic config migrated on load: `/opt/komari/net_static.json`
  now reads `detect_interval=30`, `config_version=2` with its 4,232 buckets intact.

Agents: `deploy/install-staged-agent.sh` (new, in-tree) with the release asset copied to
each node, which also covers PZYC, which cannot reach the release assets. Per node the
old binary is kept as `<bin>.bak-pre-v0.1.16`, owner/mode preserved, `cap_net_raw`
restored where it existed, the unit restarted, and the installed hash checked:
`394d9ad0…` on amd64, `a30b5445…` on arm64 (OC424).

| Node | Binary | Unit | Result |
|---|---|---|---|
| 甲骨文 OC424 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` | v0.1.16 |
| 华纳云 HN-JP1 | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | v0.1.16 |
| HK04 | same | same | v0.1.16 |
| AkkoCloud SJ | same | same | v0.1.16 |
| BandwagonHost MegaBox | same | same | v0.1.16 |
| CloudLeadInno | same | same | v0.1.16 |
| 并行智算云 PYZC | same | same | v0.1.16 |
| MAC Server | `/home/macos/nekomari-agent/komari-agent-linux-amd64` | user `nekomari-agent.service` | v0.1.16 |
| NOSLA 东京-26秋-M | `/opt/komari/agent` | `komari-agent.service` | v0.1.16 |

MAC needs the capability dance again: replacing the binary drops `cap_net_raw`, there is
no passwordless sudo, so it is restored with the host's own `setcap` through a privileged
container (`docker run --rm --privileged -v /:/host alpine chroot /host setcap
cap_net_raw+ep <bin>`), checked with `getcap` afterwards.

Verified after the rollout: nine of nine nodes report `v0.1.16`, nine established agent
connections, every node's newest metric bucket is ~1 minute old, and the traffic ledger
path is live again on all nine (`net.total.up` fresh and non-zero, e.g. 19.8 GiB on
OC424's restored node). The probe timer recorded `origin=0.002s public_max=0.152s
pop=SIN`, and the public URL answered 200 in 0.167 s.

Two leftovers were removed while doing this: `happy_heisenberg` on OC424 and
`funny_goldberg` on MAC — both `docker run` smoke tests of the agent image from the
v0.1.15 work, still running with a `bogus` token. They looked like host agents in the
process table (their parent is containerd), which is exactly why
`install-staged-agent.sh` discovers the systemd unit instead of matching the process name.
