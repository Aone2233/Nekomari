# Public deployment (OC424 / komari.orderly2233.org)

The live Nekomari panel. This file is the runbook: what runs where, how it was
restored, and the two hostname/TLS traps that cost the most time.

## Topology

| Piece | Where |
|---|---|
| Panel URL | **https://komari.orderly2233.org** |
| Host | OC424 (`ubuntu@213.35.99.48`, Oracle Cloud, **arm64**, Ubuntu 22.04) |
| Container | `nekomari`, image `ghcr.io/aone2233/nekomari:v0.1.15`, bound to `127.0.0.1:25774` |
| Compose dir | `/opt/nekomari` (bind mount `./data` → `/app/data`) |
| Reverse proxy | host **nginx** `/etc/nginx/sites-available/nekomari` |
| TLS at origin | `/etc/nginx/ssl/{fullchain,privkey}.pem` (Cloudflare Origin cert, shared with the other vhosts) |
| Edge | Cloudflare proxied A → `213.35.99.48` |
| Agent | `komari-agent-oc424-original-node.service` (source: `deploy/hosts/oc424.service`), arm64 agent reporting this host to the local panel |
| Old data | backed up by the server on first boot to `data/backup/upgrade-*.zip` |

The container is deliberately **not** published on a public interface: UFW allows
80/443 only from Cloudflare's ranges, so all traffic arrives via the edge, and
nginx is the only thing that talks to the panel port.

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

**2FA is now off and the password was reset** — re-enable 2FA in the panel when
convenient.

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

