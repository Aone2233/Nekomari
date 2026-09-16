# Fork Provenance

Nekomari is a **fork of Komari**. This file records exactly where it came from and what has changed, so the lineage is auditable.

## Upstream

| | |
|---|---|
| Upstream project | **[komari-monitor/komari](https://github.com/komari-monitor/komari)** |
| Upstream status | **Archived** (`archived: true`, last push 2026-09-14) |
| **Base tag / version** | **`1.5.0-fix1`** |
| **Base commit** | **`0ca87aa`** ("移除流量通知", 2026-09-14) |
| Upstream license | MIT — Copyright (c) 2025 Komari Moniter |
| Fork created | 2026-09-16 |
| Fork maintainer | [@Aone2233](https://github.com/Aone2233) |

`1.5.0-fix1` was the **last release published before the upstream repository was archived**, which is why it was chosen as the base. (The fork's `main` branch pointed at the same commit when it was created: `main == 1.5.0-fix1 == 0ca87aa`.)

## Components and their upstream sources

Upstream splits the project across several repositories. Nekomari combines them into a **single monorepo** (`frontend/` and `agent/` are vendored in rather than referenced).

| Nekomari path | Upstream repository | Version used |
|---|---|---|
| `/` (server + panel, Go) | [komari-monitor/komari](https://github.com/komari-monitor/komari) | **1.5.0-fix1** (`0ca87aa`) |
| `/frontend/` (web SPA, TypeScript) | [komari-monitor/komari-web](https://github.com/komari-monitor/komari-web) | **1.5.0-fix1** |
| `/agent/` (probe agent, Go) | [komari-monitor/komari-agent](https://github.com/komari-monitor/komari-agent) | **1.5.0** |
| `/tools/zstdpack/` | — | new in Nekomari |

> The internal Go protocol package (`/protocol/`) was part of the upstream server repository and is therefore covered by the `1.5.0-fix1` entry above.

### Why agent `1.5.0` and not `1.5.10`

Upstream published `komari-agent` `1.5.10` *after* the last server release. The server's last release is `1.5.0-fix1`, so Nekomari pairs it with the contemporaneous agent `1.5.0` to keep the wire protocol generation consistent. If you want to use a newer agent, please verify protocol compatibility first.

## What changed in this fork

### Structural

- Converted from multi-repo to a **monorepo**: the frontend and agent now live in `frontend/` and `agent/`.
- Renamed the Go module path: `github.com/komari-monitor/komari` → `github.com/Aone2233/nekomari`
  (agent: `github.com/komari-monitor/komari-agent` → `github.com/Aone2233/nekomari/agent`).
- Confined the module rename to import paths and `module` directives. The single remaining reference to the
  upstream path is an attribution comment in `agent/server/task.go` pointing at the upstream commit it derives from — intentionally kept.

### Added

| Path | What |
|---|---|
| `cmd/netcheck.go` | **`netcheck`** subcommand — multi-protocol reachability probe (DNS + ICMP + TCP) that reports which probe type a target actually supports. See README. |
| `tools/zstdpack/` | Theme packer. Replaces the `zstd` CLI (not available on Windows) using the same `klauspost/compress/zstd` library the server decodes with. |
| `build.sh` | One-shot build for the whole monorepo. |
| `FORK.md` | This file. |

*(Further features will be appended here as they land.)*

## Attribution obligations

The upstream license is **MIT**, which requires preserving the copyright notice and permission notice. Accordingly:

- `LICENSE` is **kept verbatim** (MIT, Copyright (c) 2025 Komari Moniter).
- `NOTICE` is **kept verbatim** (third-party attributions inherited from upstream).
- The upstream project is credited prominently in `README.md` and `README_zh-cn.md`.
- Files that were modified keep their original authorship history in git.

Nekomari's own additions are released under the same MIT license.

## Syncing with upstream

Upstream is archived, so no further upstream commits are expected. If upstream were ever unarchived, the layout difference (monorepo) means a plain `git merge` would need care around `frontend/` and `agent/`, which do not exist upstream.

The fork remains a real GitHub fork (`parent: komari-monitor/komari`), so GitHub's fork UI and "forked from" attribution stay correct.
