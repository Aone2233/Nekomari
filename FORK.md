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

## Version numbering, and why the fork's version is a compatibility statement

The plugin market gates every plugin on the server's version. Each entry carries a `komari` constraint
(`>=1.4.0`, `>=1.6.0`, …), and **both** sides enforce it: the panel hides the install button
(`isKomariCompatible` in the market UI) and the server refuses to load the plugin (`CheckKomariVersion` in
`internal/plugin/version.go`).

Both parsers accept **only numeric dotted versions** — the string is split on `.` and each part parsed as an
integer. That has two consequences, and the second is silent:

- a version like `1.5.0-fix1` does not parse. The panel reads that as an unsatisfiable constraint and marks
  every plugin as needing a newer server.
- on the server the same failure takes the **opposite** branch: an unparseable `utils.CurrentVersion` is
  deliberately ignored ("a malformed server version must not block plugin loading"), so a suffixed version
  *passes* the server check while the UI says it cannot. The two ends disagree, which is the worst of both.

So the version the release workflow writes into the binary has to be plain `X.Y.Z`.

### What the number means here

Nekomari's releases were numbered `0.1.x` — a fresh sequence unrelated to the upstream lineage recorded at the
top of this file. Upstream published up to `1.5.1`, and the ecosystem's constraints are written against that
line, so a `0.1.x` server is judged older than *every* plugin requirement: 15 of the 19 plugins in the
official market could not be installed, including all the notification plugins.

From **`1.6.0`** the fork numbers releases **as a continuation of the upstream line**:

| | |
|---|---|
| Upstream base | `1.5.0-fix1` |
| Last upstream release | `1.5.1` |
| Upstream's next entry | `1.6.0-pre1` (prerelease, never finalised) |
| **Nekomari from here** | **`1.6.0`, `1.6.1`, …** |

This is a choice about what the number is *for*. It is not a claim that an upstream `1.6.0` exists to compare
against — it does not. It is the mechanism by which the inherited plugin ecosystem recognises this server as
one it can run on, and it is honest in the direction that matters: this fork's feature set exceeds upstream
`1.5.1` (SLA reporting, bulk operations, maintenance windows, configuration import/export, traffic
forecasting, a bundleable admin interface, panel-triggered agent updates).

**The residual risk, stated plainly:** a plugin whose manifest requires `>=1.6.0` may use an API that upstream
added in `1.6.0-pre1` after this fork's base. Such a plugin satisfies the gate and fails when it loads. The
gate is a version comparison, not a capability check, and it cannot become one without reading each plugin's
source. If one is found, the fix is to raise that plugin's requirement in the market index — or to port the
missing API — not to lower this version.

### Why not simply keep `0.1.x`

Because the number is what the ecosystem reads. Renumbering is the only way for a fork to inherit a plugin
market that predates it, and the alternative — staying on a sequence the ecosystem will always read as
ancient — means shipping a fork whose entire plugin market is unusable by construction.


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
| `pkg/netcheck/` | Multi-protocol reachability probe (DNS + ICMP + TCP) with a machine-readable verdict. Distinguishes "no permission to send ICMP" from "target unreachable". |
| `cmd/netcheck.go` | **`netcheck`** CLI subcommand over the above. |
| `admin:netcheck` + `POST /api/admin/ping/netcheck` | Runs the same probe from the panel server, for the **target preflight** button in the ping task editor. |
| `agent` — task type `auto` | The agent probes once and uses a protocol the target actually answers, so nobody has to guess. |
| `agent` — task type `dual` | Probes **both** ICMP and TCP in one cycle and reports two results, so "target only answers TCP" shows up as `ICMP 100% / TCP 2ms` instead of a misleading flat 100% loss. |
| `internal/metricstore` — `protocol` tag | Keeps the two protocols of a `dual` task as two distinguishable series. |
| `agent/fake_agent.py` | Extended to simulate `dual`, so the ingest path is integration-testable. |
| `docs/TESTING.md` | Which tests are hermetic and which need root/IPv6. |
| `tools/zstdpack/` | Theme packer. Replaces the `zstd` CLI (not available on Windows) using the same `klauspost/compress/zstd` library the server decodes with. |
| `build.sh` | One-shot build for the whole monorepo. |
| `scripts/linux-dev.sh` | Sync + build + test on a Linux host (the server needs CGO, so cross-compiling from Windows fails). |
| `.github/workflows/ci.yml` | Build + hermetic tests on push and pull request (native builds on ubuntu/windows runners). |
| `.github/workflows/release.yml` | Multi-platform release pipeline; produces `nekomari-<os>-<arch>` and `komari-agent-<os>-<arch>` plus `SHA256SUMS.txt`. |
| `docs/RELEASING.md` | How to cut a release, and the two traps (the agent asset-name contract, and why builds are native). |
| `FORK.md`, `docs/` | This file and documentation. |

### Rebranding: what was renamed, and what deliberately was not

User-visible branding now says Nekomari throughout — the server banner, the
panel title and navbar, the footer, the installer's default site name, the CLI
help text, the EULA shown to users, and the prose in all five locale files
(~110 strings).

Four things were deliberately **left as Komari**, because they are contracts or
legal requirements rather than branding:

| Kept | Why |
|---|---|
| `LICENSE`, `utils/field.ts`, and the About page's copyright line | MIT requires the original copyright notice to be preserved. The About page now reads "Copyright (C) 2025 Komari Monitor (upstream, preserved under MIT)" so the attribution is unambiguous rather than looking like an oversight. |
| `X-Komari-Transfer-*` / `X-Komari-Upload-*` HTTP headers | Exchanged between the agent and the server for file transfer. Renaming them means changing both sides in lockstep and would break any older agent or server. |
| The `komari` field and `CheckKomariVersion` in the plugin market API | Part of the plugin-market wire format; upstream plugins depend on it. |
| `komari-plugin.json` / `komari-theme.json` manifest filenames | Every existing plugin and theme uses these names. Renaming would break the ecosystem this fork inherits. |
| `komari-agent-<os>-<arch>` release asset name | What the agent's self-updater looks for (see docs/RELEASING.md). Already published as v0.1.0. |

The Go module path was renamed earlier and is `github.com/Aone2233/nekomari`
throughout, so no import path still points at upstream.

### Upstream content removed from the README

Forking copies upstream's README *and* its repository metadata. Rewriting the
top of the file was not enough — the tail still carried content that belonged
to upstream and read as if it belonged here:

| Removed | Why |
|---|---|
| Sponsors (AxisNow, DreamCloud, Sharon Networks) | They sponsor **upstream**. Two of the links carry tracking/affiliate parameters (`utm=komari`, `aff=110`). |
| Donation QR codes (WeChat Pay, TRON) | These were **the upstream author's payment channels**. Leaving them in a fork means readers donate to someone who has nothing to do with this repository. |
| "Deploy on Rainyun / 1Panel" slots | Affiliate storefront links, again upstream's. |
| Screenshots | Upstream's demo images, hotlinked from upstream's own object storage (`b2.akz.moe`). |
| "Support the Project" | Pointed at upstream's funding. |

The repository's `homepage` field also arrived as `https://ss.akz.moe` — the
upstream maintainer's site — because GitHub copies that metadata when forking.
It has been cleared, along with the inherited description.

This fork now states plainly that it has no sponsors and no donation channel,
and points anyone who wants to support the original author at the upstream
repository. Attribution is kept where it is due: the fork banner, the credits
section, and the upstream contributors link.

### Removed from upstream

The upstream repository shipped **ten** workflows wired to its multi-repo layout
and to the old `github.com/komari-monitor/komari` module path — both of which no
longer exist here, so every one of them would have failed on first run. They
were deleted rather than left in place to rot:
`auto-merge-dev-to-main`, `build`, `cleanup-packages`, `development`,
`docker-publish`, `generate-release-notes`, `rebuild-release`, `release`,
`release-docker`, `snapshot`.

Two replacements cover the useful parts: `ci.yml` and `release.yml`.

## Attribution obligations

The upstream license is **MIT**, which requires preserving the copyright notice and permission notice. Accordingly:

- `LICENSE` is **kept verbatim** (MIT, Copyright (c) 2025 Komari Moniter).
- `NOTICE` is **kept verbatim** (third-party attributions inherited from upstream).
- The upstream project is credited prominently in `README.md` and `README_zh-cn.md`.
- Files that were modified keep their original authorship history in git.

Nekomari's own additions are released under the same MIT license.

## Syncing with upstream

Upstream is archived, so no further upstream commits are expected. If upstream were ever unarchived, the layout difference (monorepo) means a plain `git merge` would need care around `frontend/` and `agent/`, which do not exist upstream.

## Fork network status

Nekomari is a **genuine GitHub fork** of `komari-monitor/komari` and is **public**,
so the native *"forked from komari-monitor/komari"* banner and the *"N commits
ahead"* indicator are both shown. Current state, as reported by the API:

```
isFork = true   visibility = PUBLIC   parent = komari-monitor/komari
```

`main` is **23 commits ahead** of the upstream base `0ca87aa` (`1.5.0-fix1`).

> Earlier development happened in this repository while it was private. Making a
> fork private removes GitHub's `parent` link, and switching back to public does
> **not** restore it — so at that point the plan was to re-fork and force-push to
> recover the attribution. That turned out to be unnecessary: the repository was
> made public again and the fork relationship is intact (`isFork: true`), which is
> what the API reports above. The original caution is kept here only as a note,
> because the detach behaviour itself is real and worth remembering before
> toggling a fork's visibility.

Every upstream-derived file retains its original git history, and the lineage is
documented in this file and in both READMEs.

