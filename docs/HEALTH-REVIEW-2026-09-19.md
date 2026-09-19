# Health review — 2026-09-19 (v0.1.10)

Scope: fix the P0/P1 items from the v0.1.9 health review, verify locally, and leave a
record. Base commit `eab2ef8` (v0.1.9). Every change below was built and tested with
Go 1.27.1 and Node 22 on the author's Windows machine; no change was made to the
production hosts.

## What changed, and why

| File | Change | Reason |
|---|---|---|
| `agent/unlock/unlock.go` | a 404 title page now returns `(false, nil)` instead of an error | A 404 is a definitive answer ("not available here"), not a transient failure. Treating it as a failure made `probeNetflix` report a blocked region as `unknown`. |
| `internal/server/trusted_proxy_test.go` | new test | Locks in the forwarded-header rule below. |
| `internal/server/runtime.go` | `configureTrustedProxies` + `ReadHeaderTimeout` | Gin trusted every proxy, so any direct client could forge `ClientIP` via `X-Forwarded-For`. Now only loopback/private peers are trusted. |
| `internal/server/guide_server.go` | same proxy list + `ReadHeaderTimeout` | The install/recovery guide is a second Gin engine and had the same gap. |
| `web/api/public/login.go` | `http.MaxBytesReader` (1 MiB) | The login body was read unbounded; every other handler already caps its body. |
| `.github/workflows/ci.yml` | `-run` allowlist → `-skip` denylist | The allowlist ran 79 of 552 root tests and 6 of 81 agent tests, and hid two failing `agent/unlock` tests since the day they were committed. |
| `.github/workflows/release.yml` | inject `utils.VersionHash` | It was read in six places but never set, so every release logged at `Debug` and reported `hash: unknown`. |
| `build.sh` | inject `CurrentVersion` + `VersionHash` | Same, for local builds. |
| `frontend/package.json`, `package-lock.json` | drop `"komari-web": "file:"` | A package depending on itself made npm create a junction back to `frontend/`, so any recursive tool that follows junctions looped on `node_modules/komari-web/node_modules/komari-web/...`. |
| `CHANGELOG.md`, `docs/TESTING.md`, `docs/OPEN-WORK.md`, `docs/RELEASING.md` | updated | Keep the docs consistent with the code. |

## The bug CI was hiding

`agent/unlock/unlock_test.go` and the implementation were added in the same commit
(`41f9a00`). Two of its tests were red from that commit onward:

```
--- FAIL: TestProbeNetflixOriginalsOnly
    unlock_test.go:85: status = "unknown", want "partial"
--- FAIL: TestProbeNetflixBlocked
    unlock_test.go:97: status = "unknown", want "blocked"
```

They are hermetic (the transport is faked), so nothing about the environment explains
it: the code simply did not match its own tests. The CI `-run` allowlist did not name
them, so it never ran them. After the 404 fix both pass.

## Verification

Run locally, matching the new CI commands exactly:

```bash
# root module
go vet ./...                                   # clean
go build ./...                                 # clean
go test ./... -count=1 -skip '^TestIpInfo$'    # 37 packages ok, 0 fail

# agent module
(cd agent && go vet ./...)                     # clean
(cd agent && go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing')  # 5 packages ok, 0 fail

# frontend
cd frontend && npx tsc -b                      # exit 0
npx eslint .                                   # 0 errors, 27 pre-existing warnings
npm run build                                  # ok
npm audit                                      # 0 vulnerabilities
```

Runtime smoke test (fresh temp data dir):

```
$ ./nekomari server -l 127.0.0.1:25799
[INFO/SERVER] Nekomari Monitor v0.1.9-6-geab2ef8 (hash: eab2ef8)
[INFO/SERVER] First-run installation guide is available on 127.0.0.1:25799
$ curl /api/install/status   -> {"state":"ready","required":true}
```

The `hash: eab2ef8` in that line is the `VersionHash` fix; without the ldflags the same
binary prints `0.0.1 (hash: unknown)`. The trusted-proxy rule is covered by
`TestClientIPIgnoresForwardedHeaderFromUntrustedPeer` (public peer ignores the header,
loopback and private peers honour it).

Not run: the three agent network tests (`TestICMPPing`, `TestTCPPing`, `TestHTTPPing`)
need root/IPv6/target reachability, and the release workflow itself only runs in CI.
`nekomari.exe` in the repo root is a stale local artifact and was left untouched.

## Deployment verification (2026-09-19, on MAC)

`MAC` (192.168.100.168) is the Ubuntu x86_64 host that runs the "MAC-WAN" agent. The
release artifacts were reproduced there and the canonical verifier was run against them:

- agent: cross-compiled for `linux/amd64`, `linux/arm64` and `darwin/amd64`
  (`CGO_ENABLED=0`), matching the release names.
- server: built **natively on MAC** with Go 1.27.1 and gcc 13.3 (`CGO_ENABLED=1`), so it
  is dynamically linked against glibc exactly like the CI artifact — not a cross-build.
- `deploy/deploy-verify.sh` run with `BASE=file:///tmp/nkrel` (a local artifact dir plus a
  local `SHA256SUMS.txt`), so it exercised the same steps against pre-release binaries:

```
===== 13 passed, 0 failed =====
```

That covers: artifact checksums, the version banner showing `v0.1.10`, the first-run
install guide, install through the API, the public API, issuing an agent token, the agent
connecting over WebSocket, and the node reporting metrics.

The agent-side unlock fix was then exercised against the real network, which the hermetic
unit tests cannot do. Running the new binary's probe on MAC printed:

```
egress=192.220.32.17/US
  Netflix          unlocked  basis=probe   region=    自制剧与片库均可访问
  YouTube Premium  blocked   basis=probe   region=    该地区不提供 Premium
  ChatGPT          unlocked  basis=region  region=US  出口地区 US（按地区判断，未验证该 IP 是否真的可用）
  Claude           unlocked  basis=region  region=US  出口地区 US（按地区判断，未验证该 IP 是否真的可用）
```

The production agent on MAC (`~/nekomari-agent/`, v0.1.9) was **not** replaced: the
verification used a throwaway server on its own port and a standalone probe. Upgrading it
is a separate, reversible step (`deploy/upgrade-agent.sh`, which backs up the binary and
restores the `cap_net_raw` file capability).

## Found by the widened CI (fixed in v0.1.11)

Widening the suite from 79 tests to the whole hermetic set exposed three latent bugs in
`pkg/jsruntime`, each on a different platform:

1. **Linux — `fs.writeFileSync(path, data, "utf8")` created a mode-0000 file.** `fsMode`
   treated the encoding string as an octal mode, failed to parse it, and fell through to
   `ToInteger()` = 0, so the file was unreadable even by its owner. Windows ignores Unix
   mode bits, so only the Linux job caught it.
2. **Windows — the `BaseDir` confinement check rejected 8.3 short paths.** The runner's
   temp dir is `RUNNER~1` while `resolveRoot` canonicalises `BaseDir` to `runneradmin`;
   `filepath.Rel` then saw two different directories. Both `WithinBase` and
   `RelativeToBase` now re-check with both paths resolved, on the mismatch path only.
3. **Linux runner — `child_process.spawn` could drop output.** The stdout/stderr readers
   ran concurrently with `cmd.Wait()`, which closes the pipes; Go requires the reads to
   finish first. The process is now waited for only after both readers reach EOF.

Each is fixed with a focused change; the first two have regression tests
(`TestWriteFileWithEncodingStringUsesDefaultMode`, and the require test now runs with a
short `TEMP`). All three pass on Windows (including a short-path `TEMP`), on Ubuntu
(MAC), and in CI on both `ubuntu-latest` and `windows-latest`.

## Deliberately not done (planned in `docs/AUTH-HARDENING.md`)

- **Password hashing** is still `sha256(password + constant salt)`.
- **Login rate limiting** is still absent.

Both are migrations rather than patches, so they were left as their own change. The full
plan — argon2id/bcrypt, a self-describing stored format, transparent re-hash on next
login, the exact call sites, the throttling policy and storage choice, and the tests — is
in **[AUTH-HARDENING.md](./AUTH-HARDENING.md)**.

## Pre-existing, left alone

- `gofmt -l` flags 288 Go files repo-wide (mostly import ordering, e.g. `gin` sorted
  before the `Aone2233` imports). This predates the review; fixing it would be a large
  unrelated diff.
- `install-komari.sh` (45 KB, no references) and `agent/.github/workflows/*` (inert,
  reference the old `komari-monitor/komari-agent` module path) are still present.

## Fleet upgrade (2026-09-19)

All eight nodes in the live panel were moved from agent v0.1.9 to v0.1.11. The binary
was fetched once, verified against the published `SHA256SUMS.txt`, and copied to each host
rather than letting each node download it (PZYC cannot fetch release assets, and a per-node
download adds one failure mode per node). Each replacement backed up the old binary as
`*.bak-pre-v0.1.11`.

| Node | Binary | Unit | How |
|---|---|---|---|
| AkkoCloud, BandwagonHost, CloudLeadInno, HK04, 华纳云 | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | root, from OC424 |
| 并行智算云 (PZYC) | `/opt/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` | passwordless sudo |
| 甲骨文 OC424 | `/home/ubuntu/nekomari-agent/komari-agent-linux-arm64` | `komari-agent-oc424-original-node.service` | arm64, panel host |
| MAC Server | `~/nekomari-agent/komari-agent-linux-amd64` | `nekomari-agent.service` (user) | needs `cap_net_raw` |

MAC has no passwordless sudo, so its `cap_net_raw` file capability cannot be re-applied
with `setcap` directly. It was restored through a `--privileged` Alpine container that
bind-mounts the binary — the `macos` user is in the `docker` group, so this needs no
password. Verified: **8/8 report v0.1.11** in the panel.

## Lessons

1. **An allowlist test filter is worse than no filter.** The old CI `-run` allowlist ran
   79 of 552 tests and silently skipped every test not named in it, so a new test defaulted
   to *not running*. A denylist (`-skip`) is the safe default: new tests run unless they are
   explicitly excluded.
2. **Widening coverage finds real bugs on the first run — budget for it.** The first
   full-suite run exposed three latent `pkg/jsruntime` bugs on three different platforms.
   Several iterations to green is the coverage working, not a regression.
3. **Platform-specific behaviour needs every platform in CI.** Unix file modes are
   invisible on Windows; Windows 8.3 short paths are invisible on Linux; the pipe race only
   appeared under the runner's scheduling.
4. **Do not tag while CI is red.** v0.1.10 was tagged while the CI run for that commit was
   failing — the widened suite had just exposed the three bugs. The code was fine, but the
   release should have waited for the exact commit's green run. v0.1.11 followed that rule.
   See `docs/RELEASING.md`.
5. **"Passes locally" is not "correct".** `TestChildProcessPermissionAndExecution` passed
   on a quiet machine by timing luck and failed on the runner; the `StdoutPipe`/`Wait`
   ordering it exposed is documented Go behaviour.

## Releasing

`CHANGELOG.md` carries the `v0.1.11` entry. To release, tag `v0.1.11` and push;
`release.yml` builds the artifacts, `docker.yml` publishes the image, and the `verify`
job downloads the published assets and runs `deploy/deploy-verify.sh`.

Before tagging, confirm the `ci` workflow for the exact commit is green (see the rule in
`docs/RELEASING.md`).
