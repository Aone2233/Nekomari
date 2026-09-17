# Public deployment (OC424 / komari.orderly2233.org)

The live Nekomari panel. This file is the runbook: what runs where, how it was
restored, and the two hostname/TLS traps that cost the most time.

## Topology

| Piece | Where |
|---|---|
| Panel URL | **https://komari.orderly2233.org** |
| Host | OC424 (`ubuntu@213.35.99.48`, Oracle Cloud, **arm64**, Ubuntu 22.04) |
| Container | `nekomari`, image `ghcr.io/aone2233/nekomari:v0.1.2`, bound to `127.0.0.1:25774` |
| Compose dir | `/opt/nekomari` (bind mount `./data` → `/app/data`) |
| Reverse proxy | host **nginx** `/etc/nginx/sites-available/nekomari` |
| TLS at origin | `/etc/nginx/ssl/{fullchain,privkey}.pem` (Cloudflare Origin cert, shared with the other vhosts) |
| Edge | Cloudflare proxied A → `213.35.99.48` |
| Agent | `komari-agent-oc424.service`, arm64 agent reporting this host to the local panel |
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

# 2. nginx (see deploy/oc424/nginx-nekomari.conf)
sudo cp nginx-nekomari.conf /etc/nginx/sites-available/nekomari
sudo ln -sf /etc/nginx/sites-available/nekomari /etc/nginx/sites-enabled/nekomari
sudo nginx -t && sudo systemctl reload nginx

# 3. agent
sudo cp deploy/oc424/komari-agent-oc424.service /etc/systemd/system/
sudo systemctl enable --now komari-agent-oc424.service
```

## Notes

- The panel inherits `private_site: true` from the old production config, so the
  API answers 401 without a session and the SPA shows the login gate. That is the
  restored setting, not a deployment fault.
- nginx needs the WebSocket upgrade headers and a long `proxy_read_timeout`;
  with the default 60 s the agents visibly flap on and off the panel.
- `X-Real-IP` prefers `CF-Connecting-IP` and falls back to `$remote_addr`, so the
  panel records the visitor's address rather than Cloudflare's edge.
