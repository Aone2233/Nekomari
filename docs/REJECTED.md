# Rejected approaches

**A register of paths already closed.** Not a list of what is open — that is
[`OPEN-WORK.md`](./OPEN-WORK.md) and nothing else.

The engineering record in `docs/` is organised by *moment*: this review, that follow-up,
the deployment on a given day. A closed approach has no moment of its own, so the reasons
scatter — one in a review, one in a comment in `ci.yml`, one in a CHANGELOG entry that
nobody greps. This file is where they are all in one place, so the next person can check
instead of re-deriving, and so a plausible-sounding path that already failed does not get
proposed for the third time.

Seeded 2026-11-12 from the existing record; every row below was already written down
somewhere in this repository, and the evidence column says where.

## How to add an entry

- **Only approaches that were actually considered or tried.** If nobody weighed it, there
  is nothing to close, and a row invented to look thorough is worse than an empty section.
- **Write its best case first.** "Nobody would ever want that" is not a reason, and it
  hides the constraint that killed the path — that constraint is the row's whole value.
- **Link the evidence.** If the only evidence is a terminal session from months ago, state
  the observation and its date instead; an unevidenced claim ages into folklore.
- **Append, do not renumber.** New rows go at the end of the section they belong to, or in
  a new section. Tables are read, not sorted.
- **If a closed path is reopened, mark it `(reopened <date>)` and say what changed.**
  Deleting the row is how the same ground gets covered twice.
- **Not for:** defects and their fixes (→ [`CHANGELOG.md`](../CHANGELOG.md)), product
  limits and their rationale (→ the design document that owns them), open questions
  (→ [`OPEN-WORK.md`](./OPEN-WORK.md)).

## Build and release

| Closed path | Best case for it | Why it is closed | Evidence |
|---|---|---|---|
| `CGO_ENABLED=0`, fully static server build | One static binary, trivial cross-compilation, no C toolchain on the build machine | The server links SQLite through CGO; with `CGO_ENABLED=0` it fails at `internal/sqlitetune/connector.go` with `conn.Exec undefined`. A Go-only toolchain is not available to this project, and the build script and docs say so | [`RELEASING.md`](./RELEASING.md) |
| Upstream's bundled `zig cc` cross-compiler | Upstream's proven answer: every target from one runner, no per-OS jobs | This fork builds on a runner of each target OS instead, so every command is one a contributor would run locally. The single exception is `linux/arm64`, which cross-compiles on an amd64 runner with `gcc-aarch64-linux-gnu` | [`RELEASING.md`](./RELEASING.md) |
| The docker workflow rebuilding the binaries | One pipeline, no dependency on published release assets | The image must contain the same binary a user downloads from the release page. Consuming the release assets also keeps the CGO cross-compile problem out of the image path | [`RELEASING.md`](./RELEASING.md) |
| `git describe --tags --always` for the build stamp | Works on any commit, including one with no tags behind it | On any commit after a tag it produces `v1.6.10-3-g4e7d389`, which neither the panel's plugin gate nor `CheckKomariVersion` can parse. `build.sh` used it until 2026-10-13; the shipped version must now be plain `X.Y.Z`, enforced in three places | [`FORK.md`](../FORK.md) |
| Failing open when the server version cannot be parsed | The original comment: "a malformed server version must not block plugin loading" — availability over strictness | It silently disabled the constraint gate: every constrained plugin installed cleanly and failed at load time, with nothing left to catch it. Both ends now refuse, in `isKomariCompatible` and `CheckKomariVersion`, and `internal/plugin/version_test.go` pins both halves | [`FORK.md`](../FORK.md) |
| Renaming `komari-agent-<os>-<arch>` | Consistent Nekomari naming in user-visible artifacts | The agent's self-updater looks for that exact filename; renaming breaks auto-update silently, with no error anywhere | [`CONTRIBUTING.md`](../CONTRIBUTING.md) |
| Deciding whether a GHCR image is public from a bare `curl .../manifests/latest` | One request, and the response looks authoritative | GHCR answers the *first* unauthenticated manifest request with `401` whether or not the package is public. Fetch an anonymous token first; `docker.yml` runs the two-step check at the end of every build so a private package is reported, not discovered by a user | [`RELEASING.md`](./RELEASING.md) |

## Testing

| Closed path | Best case for it | Why it is closed | Evidence |
|---|---|---|---|
| Treat an empty screen-reader log as proof that a dialog is silent | Would let a headless accessibility run detect missing announcements without listening | A stalled Speech Dispatcher on a soundless desktop also suppressed the known-good Cancel announcement. Require a working positive control and compare the action's timestamps in the completed Orca log | [Native terminal dialog check](./TESTING.md#native-terminal-dialog-check) |
| Ship the Linux receive-only `SO_TIMESTAMPNS` ICMP prototype as the 50 µs fix | Removes userspace receive queue scheduling from RTT | The predeclared loopback comparison still exceeded the bound in both raw and unprivileged socket modes, and the prototype did not cover send timestamps, clock steps, IPv6 or fallback semantics | [`V1.8.1-NETEM-PRELIMINARY.md`](./V1.8.1-NETEM-PRELIMINARY.md) |
| A `-run` allowlist for the hermetic suite | Explicit about what runs, and provably free of network tests | Every test not named in the list silently stops running, which includes every test written later. Measured cost when it was in use: 552 root tests down to 79, and it hid two `agent/unlock` tests that had been failing since the commit that added them. CI uses `-skip` with anchored patterns instead, deliberately (2026-10-13) | [`TESTING.md`](./TESTING.md), `.github/workflows/ci.yml` |
| Putting the three live GeoIP tests into the CI gate | They are real tests of real providers; skipping them is lost coverage | They bind the suite to three third-party quotas, two of them rate-limited. On 2026-09-26 successive runs of `go test ./...` failed `TestGeojs`, then `TestIpApi`, while each passed alone and both passed on retry. A red run unrelated to the change under review is worse than an honest skip. The anchors in the skip list matter: the thirty-plus hermetic `TestIpInfo*` cases must keep running | [`TESTING.md`](./TESTING.md) |
| Skipping `TestMmdb` as well, since it downloads 8.5 MB | It is the last network-dependent test in the gate, and 8.5 MB per run for one test looks like a bad trade | It passes, and it is the only coverage of the real MaxMind read path. If the 8.5 MB ever matters, cache it or check in a tiny fixture — skipping it would not be the fix | [`TESTING.md`](./TESTING.md) |
| Treating a green build as a runnable artifact | The pipeline is green; what else is there to know | A container in this repository has built green and then failed to start. `deploy/deploy-verify.sh` downloads the *published* artifacts and proves they deploy; CI's `panel-smoke` job reuses the candidate binary rather than trusting the build | [`CONTRIBUTING.md`](../CONTRIBUTING.md), [`TESTING.md`](./TESTING.md) |
| Claiming cross-backend correctness from SQLite alone | SQLite is the default backend and most of the fleet, so one dialect covers most of the risk | v1.7.5 passed five arguments for three placeholders: SQLite tolerated it, and both actual drivers rejected recovery (`expected 3 arguments, got 5`). The new v1.7.5 test even expected five. Backend claims now need disposable MySQL 8.4 / PostgreSQL 17 recovery runs, which is a CI job of its own | [`DSH-REVIEW-2026-10-27.md`](./DSH-REVIEW-2026-10-27.md) |

## The theme contract

| Closed path | Best case for it | Why it is closed | Evidence |
|---|---|---|---|
| Reasoning about the theme's minified client instead of executing its schema | The bundle is right there and the code path is readable; extracting a schema looked like more work than reading it | Four rounds, three confident wrong answers — a mainland-China region gate, WebSocket authentication, and a misread bundle — every one reached by reasoning about the client. The `Classification` field the theme requires was missing, the server had no way to know, and the answer only appeared when the zod schema was extracted and run against a real response. `deploy/theme-contract-check.mjs` now does exactly that and exits non-zero | [`IP-INFO-API.md`](./IP-INFO-API.md), [`SILENT-FAILURES.md`](./SILENT-FAILURES.md) |
| A hand-written validator that mirrors the theme's zod schemas (`validate_contract.py`) | It runs in CI, needs no browser, and gives the server side a gate on a contract it otherwise cannot see | It required `classification` to have `type` and `label` and nothing else — because that is what the server sent. It validated the implementation against itself, passed, and the real theme rejected the same payload for a missing `classification.source`: the IP 信息 tab stayed invisible while the script said everything was fine. A transcription of a contract you cannot see is not a check. Deleted; `theme-contract-check.mjs` extracts the schema from the installed bundle and runs it with the theme's own zod | [`deploy/README.md`](../deploy/README.md) |
| Verifying a theme change through Cloudflare | It is the URL users hit, and it needs no access to the host | Behind the edge a cache hit and dead code are indistinguishable, and the difference cost a round. Test against the origin on the host (`http://127.0.0.1:25774` from OC424) | [`IP-INFO-API.md`](./IP-INFO-API.md) |
| Counting a `200` and a well-formed payload as evidence of correctness | HTTP status is fine, every field sent was correct, and all four of the server's own contract tests passed | The rejection happened entirely inside the browser, on one required field. A strict client schema is part of the server's contract and lives in code the server cannot see; reasoning about it produces plausible wrong answers | [`SILENT-FAILURES.md`](./SILENT-FAILURES.md) |
| Return the SPA shell for Next Pro's node-detail HEAD probe | A successful HEAD could allow its detail route prefetch to work | On the locked archive both the existing and experimental Panel navigated to the node, and both still raised hydration errors. The experimental response exposed a further missing Next.js route-data request on desktop, without a demonstrated user-facing improvement | [`V1.8.1-THEME-SMOKE.md`](./V1.8.1-THEME-SMOKE.md) |
| Disable all pointer events on Aero's node preview | A temporary injected rule made a simulated mobile tap reach node details | It also prevents moving the pointer onto the desktop preview, which the theme explicitly supports. The local focus-only candidate retains the hover handler and passed a bounded desktop hover control; it remains a third-party variant rather than a market fix | [`V1.8.1-THEME-SMOKE.md`](./V1.8.1-THEME-SMOKE.md) |
| Suppress btop-terminal's periodic node-list updates to retain socket metrics | A temporary modified archive displayed the synthetic CPU, memory and network rates | After node-list requests were blocked for 12 seconds it still showed the previous online state and metrics; it also lacks verified repeated polling, offline transitions and reconnection. Plausible stale data is not a safe compatibility fix | [`V1.8.1-THEME-SMOKE.md`](./V1.8.1-THEME-SMOKE.md) |
| Clear selected btop-terminal values to zero on socket close | A temporary variant changed the host list to offline and restored fixture values on reconnect | Its monitor displayed zero used and 100% available memory and retained disk values during the simulated disconnect. Those numbers look measured even though the data is unknown | [`V1.8.1-THEME-SMOKE.md`](./V1.8.1-THEME-SMOKE.md) |

## Documentation and process

| Closed path | Best case for it | Why it is closed | Evidence |
|---|---|---|---|
| Publishing the `docs/` engineering record on the documentation site | Everything in one place and searchable, with no triage about what belongs where | The readers are different, writing for an audience changes what gets written, and a dated review read as current documentation is worse than absent. Only [`IP-INFO-API.md`](./IP-INFO-API.md) and [`THIRD-PARTY-LICENSES.md`](./THIRD-PARTY-LICENSES.md) are reused, because they are already written for an operator | [`site/README.md`](../site/README.md), [`site/mkdocs.yml`](../site/mkdocs.yml) |
| A Node-based site generator, or hand-written HTML | Same toolchain as `frontend/`; or total control and no generator at all | Another toolchain in a Go and Vite repository; hand-written HTML means no navigation, no search, and every page edited twice | [`site/mkdocs.yml`](../site/mkdocs.yml) |
| Keeping the live open list inside the dated audit that produced it | The audit holds the evidence, so the list stays next to the finding it came from | That is what was being done, and it put the list in a frozen snapshot that was wrong on four findings within days, while `ROADMAP.md`, `DEPLOY-OC424.md` and `OPEN-WORK.md` each presented their own superseded state as current. Three tables, none of them live. Rewritten 2026-10-13: `OPEN-WORK.md` is the only file that claims to say what is still open | [`OPEN-WORK.md`](./OPEN-WORK.md) |
| `npm run lint` as the React Compiler gate | Lint already runs on every push and reports zero problems | eslint has eight `react-hooks` rules enabled, so the shapes the compiler rejects are green under it. `audit:compiler` was in no workflow at all while it was failing with one error, and "the audit is in CI" was true only in the docs | `.github/workflows/ci.yml` |
| `mkdocs build --strict` as proof the navigation is complete | Strict mode already fails the build on a broken internal link | It does not catch a page named in `nav:` that no longer exists. `site/script/build.mjs check` compares the navigation against what exists, and both run in `ci.yml` — before 2026-10-13 nothing ran the check on push | [`site/README.md`](../site/README.md) |
| Mountinfo as proof that monitoring data persists across a container restart | If the path is on a real device, it is on disk — that is what mountinfo shows | Mountinfo cannot prove the container's own configuration keeps it. `dbcore.warnIfDataNotPersistent` warns at startup and the operator has to verify the configuration; the warning is deliberately not an assertion | [`SILENT-FAILURES.md`](./SILENT-FAILURES.md) |

## Credentials

| Closed path | Best case for it | Why it is closed | Evidence |
|---|---|---|---|
| Redacting most of a token in a tracked file | What is left is not usable, and the file stays readable as documentation | A redacted token is still a leak, and the file has to be cleaned and the credential rotated. Write `token-1`, `token-A`, or the first two characters instead | [`SECRETS.md`](./SECRETS.md) |
| Passing the agent token with `-t` on the command line | It matches the documented install command, and the agent already supports the flag | `command_args` puts the token in the process command line, readable by anything local. `--token-file` at mode 0600 is what systemd was given in D1 and what the OpenRC path was changed to; a 0600 `conf.d` file protects the token at rest and not at run time | [`SECRETS.md`](./SECRETS.md) |
| Hand-rolling the 2FA step in the token-rotation script | No dependency on panel internals, and the code reads as self-contained | The import did not exist, and the field the server actually reads is `2fa_code`. Delegate to `nekomari_auth.login` | [`SECRETS.md`](./SECRETS.md) |
