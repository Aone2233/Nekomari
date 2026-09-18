# Nekomari

[English](./README.md) | [简体中文](./README_zh-cn.md)

> ### 🐱 Nekomari is a fork of [Komari](https://github.com/komari-monitor/komari)
>
> **Base version: `1.5.0-fix1` (commit `0ca87aa`, 2026-09-14)** — the last release published before upstream was archived.
> Upstream is **archived** and no longer maintained; Nekomari continues it with new features.
> See **[FORK.md](./FORK.md)** for full provenance and the change list.
>
> Licensed MIT, same as upstream. The original copyright (c) 2025 Komari Moniter is preserved verbatim in `LICENSE` and `NOTICE`.

Nekomari is a lightweight, self-hosted server monitoring solution — a maintained continuation of Komari. It provides a simple and efficient way to track server performance through a web interface, with metrics collected by a lightweight agent.

> [!WARNING]
> Nekomari is a self-hosted monitoring and control application. Deploy it only on systems you own or are authorized to manage. You are solely responsible for how you deploy and use it. The developers accept no liability for unauthorized access, persistence, command execution, other misuse, or any resulting consequences.

## What Nekomari adds over upstream

| Change | Status |
|---|---|
| **`netcheck`** — multi-protocol reachability probe (DNS + ICMP + TCP) that tells you which probe type a target actually supports | ✅ available |
| Ping task type **`auto`** — probes once and uses a protocol the target actually answers | ✅ available |
| Ping task type **`dual`** — measures ICMP and TCP in the same cycle, so "target only answers TCP" reads as `ICMP 100% / TCP 2ms` instead of a flat 100% loss | ✅ available |
| **Reference target** on a ping task — probe your gateway alongside the target, to tell a local problem from an upstream one | ✅ available |
| **Baseline alerting** — alert on deviation from a rule's own P95 history instead of a hand-tuned absolute threshold | ✅ available |
| **Backup freshness metric** (`backup.age_seconds` / `backup.ok`) — alert when backups stop, not only when a host goes down | ✅ available |
| **IP information API** (`/api/*/ip-info/v1`) — geo, ASN, network type and Globalping latency, for themes that render an IP panel | ✅ available |
| **Address-family aware scheduling** — a node with no IPv4 address is skipped on IPv4-literal targets instead of reporting a permanent 100% loss, which is a structural mismatch rather than an outage | ✅ available |
| Monorepo layout — server + `frontend/` + `agent/` in one repository | ✅ |
| Bundled theme packer (`tools/zstdpack`) — builds without the `zstd` CLI (useful on Windows) | ✅ |
| One-shot build script (`build.sh`) | ✅ |

### `netcheck` in 20 seconds

Monitoring tasks probe a target with **one** protocol (ICMP, TCP or HTTP). Pick the wrong one and you get a flat 100% packet loss — which looks like an outage but is really a **protocol mismatch**. `netcheck` tells you up front which one to use:

```
$ ./nekomari netcheck 1.51.3.134

ICMP probe (3x, timeout 3s)
  x recv 0/3  loss 100%   -- target does not answer ICMP
TCP probe (timeout 3s)
  x 22     unreachable
  v 80     open   (handshake 2 ms)
  v 443    open   (handshake 2 ms)
----------------------------------------------------------
Verdict: TCP-only reachable (does not answer ICMP)
Advice:  this target does NOT answer ICMP, but TCP:80,443 is open. Use a `tcp`
         probe task and write the target as host:port (e.g. 1.51.3.134:80),
         otherwise you will get 100% packet loss.
```

It also distinguishes **"permission denied"** from **"target unreachable"** — otherwise a non-privileged ICMP attempt leads to the *opposite* conclusion.

## Features

These describe the base software Nekomari inherits from Komari:

- **Real-time monitoring** — data at one-second intervals.
- **Lightweight** — minimal system resources; runs on small servers.
- **Self-hosted** — your data stays on your machines.
- **Web interface** — a dashboard for both the public view and administration.
- **Extensible** — custom themes and plugins.

## Quick start

### Prebuilt binaries

Download from [Releases](https://github.com/Aone2233/Nekomari/releases). Each
release ships the server and the agent for `linux/amd64`, `linux/arm64` and
`windows/amd64`, plus `SHA256SUMS.txt`.

```bash
./nekomari-linux-amd64 server          # open http://localhost:25774 to install
./komari-agent-linux-amd64 -e https://your-panel -t <token>
```

The agent binary keeps the `komari-agent-` prefix because that is the filename
its self-updater looks for — see [docs/RELEASING.md](./docs/RELEASING.md).

### Docker

```bash
docker run -d --name nekomari \
  -p 25774:25774 \
  -v nekomari-data:/app/data \
  ghcr.io/aone2233/nekomari:latest
```

Multi-arch (`linux/amd64`, `linux/arm64`), anonymously pullable. The binary inside
is the same one attached to the release.

> [!IMPORTANT]
> **Always pass `-v`.** The image declares `VOLUME /app/data`, so running it
> without one makes Docker create an *anonymous* volume. Updating the image means
> recreating the container, and the new container gets a **different** anonymous
> volume — so the panel starts with an empty data directory and shows the install
> wizard again. Your data is not deleted, just orphaned, but from the outside it
> looks exactly like "the update reset my settings".
>
> Either a named volume (`-v nekomari-data:/app/data`, above) or a bind mount
> (`-v /opt/nekomari/data:/app/data`) is fine. The server logs a warning at
> startup if it detects the anonymous case.

With Compose:

```yaml
services:
  nekomari:
    image: ghcr.io/aone2233/nekomari:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:25774:25774"   # keep it on loopback; put a reverse proxy in front
    volumes:
      - ./data:/app/data
    environment:
      TZ: Asia/Shanghai
```

Agents hold long-lived WebSocket connections, so a reverse proxy in front must
pass the upgrade headers — see [deploy/nginx-nekomari.conf](./deploy/nginx-nekomari.conf).

### Build from source

Requires Go (see `go.mod`), Node 22+, and a C toolchain — the server links
SQLite through CGO, so `CGO_ENABLED=0` will not build it.

```bash
./build.sh          # frontend -> embedded theme -> server -> agent
```

`build.sh` runs all five steps including the theme packer, so it needs no
external `zstd` binary.

### Installation notes

The installer asks for a **metric store DSN** (SQLite by default). The upstream
[installation guide](https://www.komari.wiki/en/install/quick-start) describes the
base software's Docker and deployment options; it documents Komari, so where the
two differ, this repository's `docs/` wins.

To connect a node, download the agent from
[Releases](https://github.com/Aone2233/Nekomari/releases) and point it at the
panel — `deploy/install-node-agent.sh` does it for a Linux host, including the
checksum check and the systemd unit.

## Documentation

| Document | Contents |
|---|---|
| [FORK.md](./FORK.md) | Provenance, base commit, and the full change list |
| [CHANGELOG.md](./CHANGELOG.md) | What changed in each release, and why |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | How to build, test and submit a change |
| [docs/RELEASING.md](./docs/RELEASING.md) | How to cut a release, and the two traps |
| [docs/TESTING.md](./docs/TESTING.md) | Which tests are hermetic; which need root or IPv6 |
| [docs/DEPLOY-OC424.md](./docs/DEPLOY-OC424.md) | The public deployment: topology, data restore, node reconnection |
| [docs/RETIRE-CF-PROBE.md](./docs/RETIRE-CF-PROBE.md) | Retiring the previous monitoring stack, and how to roll it back |
| [docs/SECRETS.md](./docs/SECRETS.md) | The no-credentials rule, and the token-leak incident record |
| [docs/DEPLOY-VERIFICATION.md](./docs/DEPLOY-VERIFICATION.md) | Deploy verification on a test host, and what it found |
| [docs/IP-INFO-API.md](./docs/IP-INFO-API.md) | The `/api/*/ip-info/v1` contract, its upstreams, and caching |
| [deploy/README.md](./deploy/README.md) | What each operational script does, and why some hosts need their own |

## Credits and provenance

Nekomari is a fork of **[Komari](https://github.com/komari-monitor/komari)**,
based on `1.5.0-fix1` (commit `0ca87aa`). Upstream is archived, and this project
continues it.

Thanks are due to everyone who contributed to Komari — see the
[upstream contributors](https://github.com/komari-monitor/komari/graphs/contributors).
None of them are responsible for anything added here.

**Sponsors and donations:** this fork has none. The sponsorship and donation
sections you may remember from upstream's README belonged to the upstream
maintainer and were deliberately removed here, so that nothing in this
repository implies an endorsement or a payment channel that does not exist.
If you want to support the original author, do it through
[the upstream repository](https://github.com/komari-monitor/komari).

## License

MIT, unchanged. The original copyright notice is preserved verbatim in
[LICENSE](./LICENSE) and [NOTICE](./NOTICE).

