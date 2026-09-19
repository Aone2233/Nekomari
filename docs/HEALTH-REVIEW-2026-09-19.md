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

Widening the suite from 79 tests to the whole hermetic set immediately turned two
`pkg/jsruntime` tests red on Linux (`TestNodeCoreModulesAndECMAScriptBuiltins`,
`TestStorageDirIsConfinedAdditionalRoot`). They are hermetic, so this was not the
environment: `fs.writeFileSync(path, data, "utf8")` was creating a file with mode
**0000**. `fsMode` treated the encoding string `"utf8"` as an octal mode, failed to
parse it, and fell through to `ToInteger()` = 0. Windows ignores Unix mode bits, so the
Windows job stayed green and the old CI never ran the tests on Linux anyway. Fixed by
falling back to the default mode when a string is not a valid octal mode; a focused
regression test (`TestWriteFileWithEncodingStringUsesDefaultMode`) was added. This is the
bug the CI change existed to find, found on its first run.

## Deliberately not done (recorded in `docs/OPEN-WORK.md`)

- **Password hashing** is still `sha256(password + constant salt)`. Replacing it needs a
  migration (verify the old format on login, rewrite on success) and its own tests.
- **Login rate limiting** is still absent. A limiter needs a decision on key, storage and
  configurability.

Both are bigger than a razor change and are recorded as open items rather than guessed at.

## Pre-existing, left alone

- `gofmt -l` flags 288 Go files repo-wide (mostly import ordering, e.g. `gin` sorted
  before the `Aone2233` imports). This predates the review; fixing it would be a large
  unrelated diff.
- `install-komari.sh` (45 KB, no references) and `agent/.github/workflows/*` (inert,
  reference the old `komari-monitor/komari-agent` module path) are still present.

## Releasing

`CHANGELOG.md` already carries the `v0.1.10` entry. To release, tag `v0.1.10` and push;
`release.yml` builds the artifacts and `docker.yml` publishes the image. The `verify`
job then downloads the published assets and runs `deploy/deploy-verify.sh`.
