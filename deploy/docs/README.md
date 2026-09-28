# Documentation site

**https://nekomari-docs.66040321.xyz** — the product documentation, built from this repository.

```
site/content/  ──┐
                 ├─▶ stage ─▶ mkdocs build --strict ─▶ nginx ─▶ 127.0.0.1:25775 ─▶ nginx vhost ─▶ Cloudflare
docs/*.md  ──────┘
(reused pages)
```

## What runs where

| Piece | Where |
|---|---|
| Sources | `site/` in this repository — see `site/README.md` |
| Image | built locally on the panel host from `deploy/docs/Dockerfile` |
| Container | `nekomari-docs`, plain HTTP on `127.0.0.1:25775` |
| TLS + hostname | the panel host's nginx, in front of it |
| Edge | Cloudflare, proxy on |

The container is deliberately **separate from the panel**. The documentation changes on its own schedule, and
sharing an image would mean a typo fix required redeploying the panel — or that a documentation build failure
blocked a panel fix.

## Deploying

```bash
# on the panel host, from the repository root
sh deploy/docs/deploy.sh
```

The script builds, replaces the container (rather than restarting it, which would keep the old image), and
waits until the port actually answers. On a build failure it leaves the running container alone, so a bad
build does not take the site down.

## One-time setup

Three things are outside this repository and have to be done in Cloudflare and in nginx. **None of them are
done yet** — the section is written as the steps to follow, with the reasons, because each has a failure mode
that is confusing if you meet it without knowing why.

### 1. Cloudflare DNS

Add a record in the **66040321.xyz** zone:

| Type | Name | Content | Proxy |
|---|---|---|---|
| A | `nekomari-docs` | the panel host's public IP | **Proxied** (orange cloud) |

Proxied matters for a reason beyond hiding the origin: an **Origin certificate is only trusted when the
request arrives through Cloudflare**. With the cloud grey, browsers reject it.

### 2. Cloudflare Origin certificate

The existing certificate on the panel host covers `*.orderly2233.org` only, so it does not cover a `.xyz`
name. Issue a new one — this is a **different Cloudflare account** from the one the repository's DNS tooling
has a token for, so it cannot be automated from here:

Cloudflare dashboard → the `66040321.xyz` zone → **SSL/TLS → Origin Server → Create Certificate** →
hostnames `nekomari-docs.66040321.xyz` (or `*.66040321.xyz`) → install to the panel host:

```bash
sudo install -d -m 0755 /etc/nginx/ssl
sudo tee /etc/nginx/ssl/66040321.pem >/dev/null   # paste the certificate
sudo tee /etc/nginx/ssl/66040321.key >/dev/null   # paste the private key
sudo chmod 600 /etc/nginx/ssl/66040321.key
```

!!! note "Why not Let's Encrypt"
    It would work — with Cloudflare proxying, `HTTP-01` reaches the origin through the tunnel — but it adds a
    renewal timer and a failure mode that only appears in 90 days' time. The Origin certificate is valid for
    years and is what every other vhost on this host already uses.

### 3. nginx vhost

Copy `deploy/docs/nginx-docs.conf` into `/etc/nginx/sites-available/nekomari-docs`, change the port and
server name to match, symlink into `sites-enabled`, then `nginx -t && systemctl reload nginx`.

The vhost is a plain reverse proxy to `127.0.0.1:25775` — no WebSocket, no upgrade headers, because this
container serves files.

## Verifying

```bash
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:25775/          # 200, direct to the container
curl -sS -o /dev/null -w '%{http_code}\n' https://nekomari-docs.66040321.xyz/   # 200, through nginx + CF
```

Compare **both**: the first isolates the container, the second the whole path. When only the second fails, the
problem is the vhost or the Cloudflare record, not the build.

## Updating the site

Content lives in `site/content/` and is edited like any other file. After a change:

```bash
sh deploy/docs/deploy.sh      # rebuild and replace the container
```

There is no automatic rebuild on push — the container is built from the working tree on the panel host, and
making it follow `main` automatically would need a runner there. If that becomes worth it, the natural shape
is a systemd timer that fetches, rebuilds and only replaces the container when the build succeeds.
