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
