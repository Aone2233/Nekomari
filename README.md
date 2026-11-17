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
./manual-komari-agent-linux-amd64 -e https://your-panel -t <token>
```

The v1.7.30, v1.8.0 and planned v1.8.1 releases name Agent assets
`manual-komari-agent-*`: verify the release checksums and explicitly install the
binary after reviewing the node. The Agent self-updater searches for
`komari-agent-*` and will not automatically install these manual assets. GitHub
Latest remains on the previous installer release. For v1.8.1, Docker moving
tags remain unchanged; use the versioned panel image after publication. Deploy
the new panel before a new Agent so unknown probe results are not read as loss
by an old panel. See [docs/RELEASING.md](./docs/RELEASING.md).

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
| [docs/SILENT-FAILURES.md](./docs/SILENT-FAILURES.md) | Where the system knows something is wrong and does not say so |
| [docs/OPEN-WORK.md](./docs/OPEN-WORK.md) | What is unfinished, what is blocked, and what will bite again |
| [docs/HANDOFF-DSH-2026-10-27.md](./docs/HANDOFF-DSH-2026-10-27.md) | Dated handoff for the v1.7.4 delivery: evidence, next bounded work, operational boundaries |
| [docs/DSH-REVIEW-2026-10-27.md](./docs/DSH-REVIEW-2026-10-27.md) | Review of dsh's v1.7.5 delivery, cross-backend recovery findings and bounded corrections |
| [docs/SLA-COVERAGE.md](./docs/SLA-COVERAGE.md) | What `Presence.Coverage` measures: the observed span, not the requested window |
| [docs/METRIC-RECOVERY-STARTUP.md](./docs/METRIC-RECOVERY-STARTUP.md) | Why a restored metrics store could miss its startup deadline, and the plan fix that bounds it |
| [docs/RELEASING.md](./docs/RELEASING.md) | How to cut a release, and the two traps |
| [docs/TESTING.md](./docs/TESTING.md) | Which tests are hermetic; which need root or IPv6 |
| [docs/DEPLOY-OC424.md](./docs/DEPLOY-OC424.md) | The public deployment: topology, data restore, node reconnection |
| [docs/RETIRE-CF-PROBE.md](./docs/RETIRE-CF-PROBE.md) | Retiring the previous monitoring stack, and how to roll it back |
| [docs/SECRETS.md](./docs/SECRETS.md) | The no-credentials rule, and the token-leak incident record |
| [docs/API-KEYS.md](./docs/API-KEYS.md) | Scoped-key lifecycle, exact permissions, legacy consumer migration and rollback gates |
| [docs/HERMES-NODE-CLIENT.md](./docs/HERMES-NODE-CLIENT.md) | Metadata-only Hermes CLI, hidden owner-TTY key handoff and remaining live transition steps |
| [docs/CLIENT-TOKEN-ROTATION.md](./docs/CLIENT-TOKEN-ROTATION.md) | Session-only Agent-token rotation, retirement boundaries, independent recovery and compatibility gates |
| [docs/V1.7-CUTOVER-READINESS.md](./docs/V1.7-CUTOVER-READINESS.md) | Delivered v1.7 cutover evidence, historical conditions and remaining owner acceptance/handoff |
| [docs/ENCRYPTED-BACKUPS.md](./docs/ENCRYPTED-BACKUPS.md) | Recipient-only tooling, encrypted nightly schedule and isolated restore contract |
| [docs/MAC-WAN-RECOVERY.md](./docs/MAC-WAN-RECOVERY.md) | Online key custody, actual pre/post encrypted recovery and owner-reported offline copy |
| [docs/DEPLOY-VERIFICATION.md](./docs/DEPLOY-VERIFICATION.md) | Deploy verification on a test host, and what it found |
| [docs/IP-INFO-API.md](./docs/IP-INFO-API.md) | The `/api/*/ip-info/v1` contract, its upstreams, and caching |
| [deploy/README.md](./deploy/README.md) | What each operational script does, and why some hosts need their own |

The table above is the short list worth reading first. Every other document in
`docs/` is listed below under its own title; `docs/OPEN-WORK.md` stays the only
file that claims to say what is still open. `script/docs-index.mjs` regenerates
this list and fails CI when a document is missing from it or a relative link in
the repository's markdown points at nothing.

<!-- docs-index:start -->
- [Admin Tools Navigation](./docs/ADMIN-TOOLS-NAVIGATION.md)
- [Agent footprint, and the smallest server it will run on](./docs/AGENT-FOOTPRINT.md)
- [Saved load-alert rule preview](./docs/ALERT-RULE-PREVIEW.md)
- [Authentication hardening](./docs/AUTH-HARDENING.md)
- [Off-host backup health (v1.7.3)](./docs/BACKUP-HEALTH.md)
- [50-node capacity and memory gate](./docs/CAPACITY-50NODE.md)
- [v1.8 compatibility policy](./docs/COMPATIBILITY-V1.8.md)
- [React Compiler migration ledger](./docs/COMPILER-MIGRATION-2026-09-23.md)
- [Go 1.27.3 Security Build](./docs/COMPILER-SECURITY-1273.md)
- [Container data mount warning](./docs/DATA-MOUNT-WARNING.md)
- [Remote execution history: design and safety contract](./docs/EXEC-HISTORY-DESIGN.md)
- [v0.1.21 follow-up to DSH's v0.1.20](./docs/FOLLOWUP-v0.1.21.md)
- [v0.1.22: frontend state and browser review](./docs/FOLLOWUP-v0.1.22.md)
- [v0.1.23: review and next changes](./docs/FOLLOWUP-v0.1.23.md)
- [v0.1.25: post-release review and next work](./docs/FOLLOWUP-v0.1.25.md)
- [Traffic forecast direction integrity](./docs/FORECAST-DIRECTIONS.md)
- [GeoIP empty-country cache](./docs/GEOIP-EMPTY-COUNTRY.md)
- [GeoIP configuration reload](./docs/GEOIP-RELOAD.md)
- [Health review — 2026-09-19 (v0.1.10)](./docs/HEALTH-REVIEW-2026-09-19.md)
- [改进审计 — 2026-10-13](./docs/IMPROVEMENT-AUDIT-2026-10-13.md)
- [Nekomari long-term plan (2026-10-20 onward)](./docs/LONG-TERM-PLAN.md)
- [MAC-WAN backup receiver (updated 2026-11-05)](./docs/MAC-WAN-BACKUP.md)
- [Metric CSV exports](./docs/METRIC-CSV-EXPORT.md)
- [Nekomari Mint extension](./docs/MINT-THEME.md)
- [监控准确性修复与验收记录](./docs/MONITORING-ACCURACY-FIXES-2026-09-30.md)
- [Nekomari 监控数据准确性审查](./docs/MONITORING-ACCURACY-REVIEW-2026-09-30.md)
- [Next improvements after v0.1.19](./docs/NEXT-REVIEW-v0.1.19.md)
- [OAuth integration, admission, and numeric identity migration](./docs/OAUTH-INTEGRATION.md)
- [Offline notifications: maintenance and grace](./docs/OFFLINE-MAINTENANCE-GRACE.md)
- [Nekomari 优化推荐报告](./docs/OPTIMIZATION-RECOMMENDATIONS-2026-09-26.md)
- [Optimization review — 2026-09-22](./docs/OPTIMIZATION-REVIEW-2026-09-22.md)
- [OVH retirement security and backup follow-up (2026-10-28)](./docs/OVH-RETIREMENT-2026-10-28.md)
- [Panel process history](./docs/PANEL-HISTORY.md)
- [Panel database connection-pool wait history](./docs/PANEL-POOL-HISTORY.md)
- [Panel load performance](./docs/PERFORMANCE.md)
- [Family-pinned hostname probes: design and safety contract](./docs/PING-DOMAIN-FAMILY-DESIGN.md)
- [Family-pinned rollback research (experimental, not a runbook)](./docs/PING-FAMILY-ROLLBACK-DRAFT.md)
- [Family-pinned probes: transport fencing design](./docs/PING-FAMILY-TRANSPORT-DESIGN.md)
- [Probe data contract (v1.8 probe truth)](./docs/PROBE-DATA-CONTRACT.md)
- [v1.8.0 "Probe Truth" checklist](./docs/PROBE-TRUTH-V1.8-CHECKLIST.md)
- [Rejected approaches](./docs/REJECTED.md)
- [Resource and security boundaries (v0.1.13)](./docs/RESOURCE-HARDENING.md)
- [Project review and dependency refresh - 2026-09-22](./docs/REVIEW-2026-09-22.md)
- [Roadmap](./docs/ROADMAP.md)
- [SAO cycle-traffic rollout, 2026-11-02](./docs/SAO-CYCLE-ROLLOUT.md)
- [SLA task-generation normalization (v1.7.4)](./docs/SLA-GENERATIONS.md)
- [Structural and optimization review — 2026-09-23](./docs/STRUCTURAL-REVIEW-2026-09-23.md)
- [Default-theme cache diagnostics](./docs/THEME-CACHE-DIAGNOSTICS.md)
- [Default-theme cache history](./docs/THEME-CACHE-HISTORY.md)
- [Third-party licences](./docs/THIRD-PARTY-LICENSES.md)
- [Agent/panel traffic accounting — what was wrong, and what changed](./docs/TRAFFIC-ACCOUNTING.md)
- [Node uptime history](./docs/UPTIME-HISTORY.md)
- [v1.8 probe-truth OC424 staging acceptance (2026-11-08)](./docs/V1.8-OC424-STAGING.md)
- [OC424 CPU interval comparison, 2026-11-15](./docs/V1.8.1-CPU-CALIBRATION.md)
- [v1.8.1 current implementation and acceptance status](./docs/V1.8.1-CURRENT-STATUS.md)
- [HTTP phase injected-delay comparison, 2026-11-15](./docs/V1.8.1-HTTP-CALIBRATION.md)
- [Default memory accounting comparison, 2026-11-15](./docs/V1.8.1-MEMORY-CALIBRATION.md)
- [Mixed Agent address preference check, 2026-11-15](./docs/V1.8.1-MIXED-AGENT.md)
- [v1.8.1 preliminary isolated netem evidence](./docs/V1.8.1-NETEM-PRELIMINARY.md)
- [v1.8.1 original-theme public-root scan matrix](./docs/V1.8.1-THEME-MATRIX.md)
- [v1.8.1 preliminary theme browser smoke](./docs/V1.8.1-THEME-SMOKE.md)
<!-- docs-index:end -->

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

