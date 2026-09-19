# Changelog

Notable changes per release. Dates are UTC.

This fork is based on Komari `1.5.0-fix1` (commit `0ca87aa`, the last release before
upstream was archived); see [FORK.md](./FORK.md) for provenance. Releases below are
Nekomari's own.

## [v0.1.12] — 2026-09-19

### Added

- **Login throttling.** `/api/login` had no attempt limit at all, so passwords and
  6-digit 2FA codes could be tried as fast as the network allowed. Two token buckets
  now gate each attempt — per source IP (burst 10, one token per 90s) and per account
  (burst 5, one token per 5 min) — and a failed password or 2FA step consumes from
  both. Exceeding a bucket answers `429` with `Retry-After`. It is a bucket, not a
  lock, so a legitimate user recovers without an operator, and a successful login
  clears the account's failures. The IP bucket is deliberately not cleared on
  success, and an unknown username is throttled exactly like a real one, so the
  response cannot be used to probe which accounts exist.

### Documentation

- `docs/AUTH-HARDENING.md` — login throttling is implemented; the password-hash
  migration remains open, with its plan unchanged.

## [v0.1.11] — 2026-09-19

### Fixed

- **`fs.writeFileSync(path, data, "utf8")` created an unreadable file on Unix.** The
  third argument is an encoding, but `fsMode` treated any string as an octal mode: it
  parsed `"utf8"`, failed, and fell through to `ToInteger()`, which is `0`. The file
  was created with mode **0000** — even its owner could not read it back. Windows
  ignores Unix mode bits, so only the Linux run caught it, and the two `pkg/jsruntime`
  tests that cover it had been excluded from CI by the old `-run` allowlist. A string
  that is not a valid octal mode now falls back to the caller's default mode.

  Found by the v0.1.10 CI change itself: widening the suite from 79 tests to the whole
  hermetic set turned these two red on Linux, and they had been failing there all along.

- **The `BaseDir` confinement check rejected Windows 8.3 short paths.** A temp directory
  can be named `RUNNER~1` while its long form is `runneradmin`. `resolveRoot`
  canonicalises `BaseDir` with `EvalSymlinks`, which expands the short name, but an
  absolute path passed to `require`/`fs` was compared lexically — so `filepath.Rel` saw
  two different directories and rejected a path that was inside `BaseDir`. Both
  `WithinBase` and `RelativeToBase` now re-check with both paths resolved, on the mismatch
  path only, and the `require` loader shares the same check. Windows-only; the lexical
  fast path is unchanged. Also found by the widened CI, on the `windows-latest` runner.

- **`child_process.spawn` could drop a child's output.** The stdout/stderr readers ran
  in goroutines while `cmd.Wait()` closed the pipes concurrently. Go documents that
  reads from a `StdoutPipe` must finish before `Wait`; doing them concurrently can lose
  the last chunk, so a `close` handler could observe an empty buffer. The process is now
  waited for only after both readers reach EOF. Found by the widened CI on the
  `ubuntu-latest` runner, where it failed and on a quiet machine it passes by timing.

## [v0.1.10] — 2026-09-19

### Fixed

- **The unlock probe reported a region block as "unknown".** `agent/unlock` treated a
  non-200 title page as a transient failure: it retried and, when the retries failed,
  returned an error, so `probeNetflix` mapped a definitive 404 to `unknown` instead of
  `blocked` / `partial`. A 404 is a definitive answer, not a network failure. The two
  hermetic tests that covered this (`TestProbeNetflixOriginalsOnly`, `TestProbeNetflixBlocked`)
  had been failing since the commit that added the feature — because CI's `-run` allowlist
  never ran them.

### Security

- **Forwarded headers are honoured only from trusted peers.** Gin's default is to trust
  every proxy, so anyone able to reach the panel directly could forge `ClientIP` with an
  `X-Forwarded-For` header — and `ClientIP` feeds session records, audit logs and the
  visitor-audit rate limiter. The engine now trusts only loopback and private ranges
  (`127.0.0.0/8`, `::1/128`, `10/8`, `172.16/12`, `192.168/16`, `fc00::/7`), which covers
  the reference nginx-on-loopback deployment and Docker/LAN proxies while ignoring a
  public client's forged header.
- The main and guide HTTP servers now set `ReadHeaderTimeout` (10s), so a half-open
  connection cannot pin a worker. WebSocket upgrades are unaffected.
- `/api/login` request bodies are capped at 1 MiB instead of being read unbounded.

### Changed

- **CI runs the whole test suite again.** The `-run` allowlist matched 79 of 552 root
  tests (and 6 of 81 agent tests, in two packages only), so every test not named in it
  silently did not run. It is replaced by `-skip`, which excludes only the tests that
  genuinely need network, root or IPv6. `docs/TESTING.md` documents the exact commands.
- Server builds carry `VersionHash` again. It was read in six places but never injected,
  so every build — including official releases — logged at `Debug` level and reported
  `hash: unknown` through `/api/version`, `/api/public`, RPC and the DB version marker.
  `release.yml` and `build.sh` now inject it.
- The frontend no longer declares itself as a dependency (`"komari-web": "file:"`).
  npm installed it as a junction pointing back at `frontend/`, which made any recursive
  tool that follows junctions (git, find, backups, editor indexers) loop on
  `node_modules/komari-web/node_modules/komari-web/...`.

## [v0.1.9] — 2026-09-18

### Added

- **流媒体 / AI 解锁, shown in the IP information panel.** Unlock depends on the IP that
  makes the request, so it is measured by the agent on the node (every 6 h), stored in
  `unlock_reports`, and returned with `/api/public/ip-info/v1/lookup`. The panel block is
  a companion script — see `deploy/theme-unlock-panel/README.md` for why the theme cannot
  render it and why patching the bundle was rejected.

  What is reported, and what is not, was decided by probing the endpoints rather than
  assuming: `chatgpt.com` and `claude.ai` answer 403 to a datacenter IP regardless of
  country, and the Disney+/Prime Video pages contain "unavailable" in their own bundle, so
  status codes and substring tests both lie. Each result therefore carries its basis
  (`probe` or `region`), and anything undetermined is reported as `unknown` rather than
  guessed. Netflix and YouTube Premium are probe-based, calibrated against a real blocked
  and a real unblocked sample; ChatGPT and Claude are region-based and say so.

  The measured egress address travels with the results, because it is not always the
  node's own: one host in this fleet reaches the internet through another node.

### Fixed

- **The agent reported a completed TCP handshake as packet loss.** When the first
  handshake took longer than 1 s, the agent retried and, if the retry came back more
  than 800 ms faster, concluded the first SYN had been retransmitted and reported the
  whole measurement as lost. But by then the handshake had already completed and the
  retry had measured the real RTT. The effect on the panel was large: every retransmit
  became a full minute of 100% loss, because each 60 s bucket holds exactly one ping.
  On OC424 the 天津电信 task read 12% loss while a direct 30-connection test to the same
  target lost nothing — and 14 of the agent's 15 failures in four hours were this
  branch, the fifteenth being a genuine timeout.

  Measured on OC424, replaying the agent's own constants over 40 connections: 0 real
  timeouts, 5 flagged as loss, 12.5% reported — matching the 12% on the panel. The
  retransmit is still logged, but the retry's latency is now what gets reported, so
  loss means what it says.

  Only high-latency probes were affected. Probes with a single-digit RTT to the target
  never reached the 1 s threshold, which is why the same task read 0% from China and
  11–18% from Hong Kong, the US and Singapore.

## [v0.1.8] — 2026-09-18

### Fixed

- **The IP 信息 panel in LuminaPlus never appeared, and every server-side check said it
  should.** The theme parses each ip-info response with a strict zod schema in which
  `classification.source` is a required string; the server did not send it, so `/lookup`
  and `/latency` were rejected inside the browser. The endpoints answered `200` with
  correct data, the server's own contract tests passed, and React Query does not log
  query errors by default — so the failure was invisible from every direction except the
  one place nobody could see. `classification.source` is now set to `country_comparison`
  or `unavailable`, matching the reference implementation's values.

### Changed

- `deploy/validate_contract.py` is **removed**. It was a hand-written Python mirror of
  the theme's schemas and it required `classification` to contain only `type` and
  `label` — exactly what the server sent — so it validated the implementation against
  itself and reported success while the theme rejected the same payload. Replaced by
  `deploy/theme-contract-check.mjs`, which extracts the schema block from the installed
  theme bundle and runs it with the theme's own bundled zod.

### Documentation

- `docs/IP-INFO-API.md` — the IP panel investigation is closed with the proven root
  cause, and the four hypotheses that were tested and eliminated are recorded so they
  are not retried.
- `docs/SILENT-FAILURES.md` — new entry for this case, which is the purest example of
  the class: the server had no way to know it was wrong.

## [v0.1.7] — 2026-09-18

### Fixed

- **A scheduled job could be accepted and then never run.** A cron spec can parse
  cleanly and still have no future occurrence — `Next` scans a year of seconds and
  gives up, so `0 0 0 30 2 *` (30 February) is valid to the parser and never comes
  due. That was caught only inside the runner, which logged a warning and returned:
  `AddContextFunc` reported success to its caller, and the job then silently never
  executed. Whatever it drove — metric rollups, notification dispatch, retention —
  simply stopped, with nothing surfaced anywhere an operator would look. It is now
  rejected at registration. The runtime guard remains as a safety net and logs at
  error level.
- **A ping task could measure two different network paths and plot them as one.**
  A dual-stack hostname in a mixed fleet is resolved per probe: the dual-stack probes
  dial IPv6 and the v4-only probes dial IPv4. One task therefore reported HK04 at
  12.6% loss against 0.0% from its peers — the difference between the IPv6 and IPv4
  routes to the same hostname, not instability in either.

### Documentation

- `docs/SILENT-FAILURES.md` — a catalogue of places where the system knows something
  is wrong and does not say so, with each entry checked against the running instance
  so observed problems are distinguishable from latent ones.
- `docs/OPEN-WORK.md` — unfinished work, decisions waiting, and the traps that cost
  time, so a later session does not re-derive them.

## [v0.1.6] — 2026-09-18

### Fixed

- **The admin panel handed out upstream's agent installer.** All three platform
  commands pointed at `komari-monitor/komari-agent`, so anyone copying the command
  out of the panel installed the upstream agent — from an archived project, without
  the flags this fork added. The panel's own `--disable-web-ssh` checkbox did not
  even exist upstream.
- **`logged_in` was missing from `/api/public`.** Third-party themes read account
  state from the public settings rather than from `/api/me`, so a theme that gates
  a panel on `logged_in === true` never showed it — while every API call it depends
  on returned 200 with correct data. Necessary but not sufficient for the LuminaPlus
  IP panel; see [docs/IP-INFO-API.md](./docs/IP-INFO-API.md).

### Added

- **One-line agent installer** (`deploy/install-node-agent.sh`) that accepts the
  agent flags the panel generates, so the panel's command works verbatim. Consumes
  the installer-only options, passes the rest through to the agent and into the
  unit, resolves the latest release instead of a hardcoded version, verifies
  `SHA256SUMS.txt`, and handles macOS as well as Linux.
- **Windows installer** (`deploy/install-node-agent.ps1`). PowerShell cannot name a
  parameter `--disable-web-ssh`, so unrecognised arguments are collected and
  translated — that is what lets the same panel command work there too.
- **macOS agent builds.** The panel has always offered a macOS option and there was
  never an asset to install. The agent is pure Go, so it cross-compiles from the
  existing Linux runner at no extra cost.

### Documentation

- `docs/IP-INFO-API.md` now records the IP-panel investigation: what was ruled out
  (the mainland-China region gate, WebSocket authentication noise, three separate
  caching layers) and the two hypotheses that remain, so none of it is redone.

## [v0.1.5] — 2026-09-17

### Added

- **The task list now shows which nodes are being skipped and why.** The
  address-family filter stopped dispatching to a node that cannot reach a target,
  which was correct but invisible — the only symptom was a node with no curve, which
  reads as a broken target. The skip set is computed server-side and returned as
  `skipped_clients`; the public nodes API deliberately does not expose node
  addresses, and recomputing the rule in the browser would let the display drift
  from what the scheduler actually does.

### Fixed

- **The 2FA issuer said `Komari Monitor`.** That string is stored permanently in the
  user's authenticator app, so every user who enabled 2FA saw the upstream project's
  name rather than this fork's.
- **The PWA manifest said `Komari Monitor`**, in both `public/manifest.json` and the
  `VitePWA` block — the name shown when the panel is installed as an app.

### Documentation

- `CHANGELOG.md` and `CONTRIBUTING.md` added; the repository had seven docs and no
  changelog and no contributing guide, so the release history was only readable on
  GitHub.
- `docs/TESTING.md` names the two `pkg/jsruntime` tests that fail when the test
  process cannot write its own temp directory, and how to confirm it is the
  environment rather than the change under test.
- `deploy/README.md` documents that enabling 2FA breaks any script that logs in with
  only a password, and points at `nekomari_auth.py` as the single login path.

## [v0.1.4] — 2026-09-17

### Added

- **Address-family aware scheduling.** A node with no IPv4 address is no longer sent
  an IPv4-literal target, where it could only ever report a permanent 100% loss.
  Hostname targets are never filtered (the agent resolves them per family), and a
  node with no recorded addresses yet is not filtered either, so a newly added node
  is not silently dropped from monitoring. Skips are logged once per schedule
  reload. Fixes a target that looked unstable when the real problem was structural.

### Fixed

- **The 2FA issuer said `Komari Monitor`.** That string is stored permanently in
  the user's authenticator app, so every user who enabled 2FA saw the upstream
  project's name.
- **The PWA manifest said `Komari Monitor`**, in both `public/manifest.json` and the
  `VitePWA` block — the name shown when the panel is installed as an app.

### Changed

- CI actions moved to the majors that ship a Node 24 runtime (checkout v7,
  setup-go v7, setup-node v7, upload-artifact v7, download-artifact v8, docker
  actions v4/v6/v7, action-gh-release v3). The Node 20 deprecation warning is gone.
- `setup-go` now reads `agent/go.mod` rather than the root `go.mod`. The agent is a
  separate module requiring a newer Go (1.26.0 vs 1.25.0) and both are built from
  the same job; the older action tolerated the mismatch and v7 correctly did not.
- README documents the container deployment, which it previously did not mention at
  all — including that `-v` is required and why.

## [v0.1.3] — 2026-09-17

### Added

- **IP information API** (`/api/*/ip-info/v1`): geo, ASN, network type and Globalping
  latency, for themes that render an IP panel. Multiple upstreams with caching; see
  [docs/IP-INFO-API.md](./docs/IP-INFO-API.md).

### Fixed

- **A replaced favicon did not take effect**, for three independent reasons: the
  response type came from the file extension so a PNG was served as
  `image/vnd.microsoft.icon` and discarded by browsers; no cache directives were
  sent; and the admin preview used a fixed URL. Now the type is sniffed from
  content, `no-store` is sent, and the served HTML versions the favicon URL by file
  mtime so a changed icon bypasses every cache layer — including Cloudflare, which
  overrides the origin's `Cache-Control` with its own.
- **Traffic was reported as since-boot rather than per billing cycle**, because the
  installer never set `--month-rotate`. A node up 151 days reported 599 GB against a
  26 GB plan.
- The admin version banner compared against upstream's releases, so it always
  offered an "update" to a version this fork is ahead of.
- Web SSH was disabled on every node by the installer's default flag, which also
  disables remote command execution.

### Added (infrastructure)

- `deploy/deploy-verify.sh`, run in CI as the `verify` job after every release: it
  downloads the published assets, checks them against `SHA256SUMS.txt`, starts the
  server, completes the first-run install, connects an agent and confirms the node
  reports. `build` only proves it compiles and `release` only proves it uploads;
  neither would have caught the v0.1.2 container bug below.

## [v0.1.2] — 2026-09-17

### Fixed

- **The container image could not start at all.** The binary was linked against
  glibc but the image was based on Alpine (musl), so every `docker run` died with
  `exec /app/nekomari: no such file or directory` while all three pipelines were
  green. The base is now `debian:bookworm-slim`, and the Docker workflow smoke-tests
  that the image actually starts before pushing.

## [v0.1.1] — 2026-09-17

### Fixed

- `COPY --chmod` required BuildKit, which not every build host has.

## [v0.1.0] — 2026-09-16

First Nekomari release, forked from Komari `1.5.0-fix1`.

### Added

- `netcheck` — probes a target with DNS, ICMP and TCP and reports which protocol it
  actually answers, so a monitoring task can be configured around reality instead of
  guesswork. It also distinguishes "no permission to send ICMP" from "target
  unreachable", because conflating those leads to the opposite conclusion.
- Ping task type `auto` — the agent picks a protocol the target answers.
- Ping task type `dual` — measures ICMP and TCP in the same cycle, so a target that
  only answers TCP reads as `ICMP 100% / TCP 2ms` rather than a flat outage.
- Reference target on a ping task — probe a gateway alongside the target to tell a
  local problem from an upstream one.
- Baseline alerting — alert on deviation from a rule's own P95 history instead of a
  hand-tuned absolute threshold.
- Backup freshness metric (`backup.age_seconds` / `backup.ok`).
- Monorepo layout (server + `frontend/` + `agent/`), a bundled theme packer that
  needs no `zstd` binary, and `build.sh`.

[v0.1.7]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.7
[v0.1.6]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.6
[v0.1.5]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.5
[v0.1.4]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.4
[v0.1.3]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.3
[v0.1.2]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.2
[v0.1.1]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.1
[v0.1.0]: https://github.com/Aone2233/Nekomari/releases/tag/v0.1.0
