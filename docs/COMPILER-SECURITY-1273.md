# Go 1.27.3 Security Build

Status: locally and OVH-verified candidate, 2026-10-24. This is a narrow
compiler-refresh branch from `464a3fd`, not the accumulated v1.7+ development
workspace. No release/deployment or whole-roadmap completion follows from this
document. Exact-commit remote CI and published-artifact checks remain required.

## Reason And Scope

[GO-2026-6809](https://vuln.go.dev/ID/GO-2026-6809.json),
[CVE-2026-111943](https://pkg.go.dev/vuln/GO-2026-6809), affects
`crypto/x509.Certificate.Verify` before Go 1.26.10/1.27.3. A denylist rejects
known compromised intermediate certificates even when a platform trust store
does not supply that protection. Official
[Go release metadata](https://go.dev/dl/?mode=json) lists 1.27.3 as stable.

All three `setup-go` pins in CI and both in release use 1.27.3. The existing
`govulncheck@v1.8.0` source/binary gates remain enabled. There is no change to
`go.mod` minimums, sums, third-party requirements, Go/Agent application source,
REST/RPC/protocol, credential handling, SQL schema or frontend source. The
scoped-key migration, later metrics/lifecycle work and marketplace features
remain outside this candidate. Agent sources are byte-identical to v1.6.25;
compilation alone does not require or authorize a fleet rollout.

## Required Verification

Test the exact candidate, with the CI network exclusions unchanged:

```bash
export GOTOOLCHAIN=go1.27.3+auto GOWORK=off GOFLAGS=-mod=readonly
umask 022
go version
go test ./... -count=1 -skip '^TestIpInfo$|^TestIpApi$|^TestGeojs$'
(cd agent && go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing')
go vet ./...
(cd agent && go vet ./...)
go mod verify
(cd agent && go mod verify)
govulncheck ./...
(cd agent && govulncheck ./...)
```

Also require frontend test/lint/compiler/build and theme repack, ordinary/race
regressions, actual candidate-binary vulnerability scans and version/hash checks.
The unused OpenPGP dependency may have a module-only notice; record it rather
than calling the entire dependency graph advisory-free. A source scan is not a
substitute for scanning each distributed executable.

The development host may use existing dependencies in an isolated directory.
OVH may run exact-hash amd64 candidates against private synthetic loopback DBs.
Do not use production credentials or backup contents for those checks. OVH is
not the arm64 production host or a Windows/macOS native runner. No 200-node
experiment, benchmark or soak is included: that work remains explicitly deferred.

## Candidate Verification (2026-10-24)

The isolated candidate passes the root and Agent Go suites with the existing
CI network exclusions, both `go vet` and module verification checks, repeated
account/public-auth race tests and OAuth/security/upload races. Frontend tests
pass 183/183, along with lint, compiler audit, non-incremental types and the full
production build. Installer, updater, probe, auth-smoke and upgrade-check offline
assertion counts are 37/35/88/29/69; capability/workflow/action-input contracts
and the 14-page documentation stage/check pass too.

The rebuilt embedded frontend is exercised through an actual candidate Panel:
41 route visits (40 distinct paths, including a repeated landing page), two
server-backed write/retry groups and four desktop/mobile/navigation groups
pass. The write tests deliberately inject one HTTP 500 before retrying a real
write; these are not wholly unmocked tests. Existing Zod/large-chunk build
warnings remain; this patch does not claim the later frontend-budget milestone.

Both actual amd64 executables report Go 1.27.3 and pass binary `govulncheck`;
source scans also report no reachable or imported-package vulnerability. The
Panel retains the module-only `GO-2026-5932` notice for unused OpenPGP, not a
reachable finding. Preliminary candidate SHA-256 values are:

| Input | SHA-256 |
|---|---|
| Panel | `523810797d13e493a0f764247d27112fa5f00c6dcfd0ad9a7151523c9c43ac07` |
| Agent | `60c2aaa37cf4844ebf5a1faa0ba8af10b56be218fa68a84c32199eff470fea7d` |

These are **not release assets**: they use the provisional `v1.6.26` version
and Panel hash `candidate`, built before the documentation commit. The tracked
embedded archive and all runtime/module/frontend sources remain unchanged;
an isolated overlay supplies the fresh embedded frontend to the local build.

Both local and OVH synthetic upgrade drills pass 80 observations each: start
checksum-verified published v1.6.25, install, receive old/new Agent reports,
capture SQLite backup-API snapshots, upgrade to the exact candidate, roll the
binary back without reverting newer writes, then separately restore the old
snapshot. Both databases pass `quick_check` at each stopped boundary. This is
a same-architecture synthetic compatibility drill, not a production restore or
arm64-container pass. OVH input/report/log digests are independently checked
after transfer. Its existing Agent, Docker objects and off-host-backup filename/
size/mtime metadata remain unchanged; backup contents are not opened.

Private evidence is retained outside Git: candidate `.runtest/candidate-*.json`
and logs, `expanded-browser/report.json`, `upgrade-pilot/report.json` and
`ovh-compiler-check/verification.json` (SHA-256
`2424590e521ba3ed85f8a6d1a32f685f73f4671413fd5aeb9cb8ba2dce61e880`).
OVH workspace `/home/ubuntu/nekomari-compiler-20261024-b090dd2e` retains reports
and scripts; successful synthetic databases/processes, duplicate executables
and transfer archives are removed. One initial local repack failed because a
staging symlink was copied over a directory; its failure log is retained and
the corrected regular-directory stage passes without a product-source edit.

## Delivery And Rollback

Push this bounded branch for review and require green CI on the **exact commit**
before choosing/tagging a release. The existing release workflow rebuilds,
scans, publishes checksums/provenance and verifies actual downloaded amd64/arm64
artifacts; the Docker workflow packages those exact released binaries.

For OC424, verify the published arm64 checksum/provenance and pinned image,
take fresh WAL-safe primary/metrics backups and retain the old compose/image
and upgrade archives before switching. Run `panel-upgrade-check.sh` plus served
frontend/authentication checks immediately afterward. This candidate has no new
migration, but its rollback still needs a verified pre-upgrade recovery point.
Do not restore old auth data unnecessarily or silently undo a later revocation.

No production trust roots, backup/TOTP key custody, service privileges or fleet
update policy are selected here. This patch does not deliver the later roadmap
features, native-platform acceptance or long-window SLO/soak criteria.
