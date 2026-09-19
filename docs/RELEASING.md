# Releasing

## Cut a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which builds every
platform, creates the GitHub Release and attaches the artifacts plus
`SHA256SUMS.txt`.

> **Wait for CI green on the exact commit before tagging.** The tag starts the
> release pipeline immediately. v0.1.10 was tagged while the `ci` run for that commit
> was failing — the widened suite had just exposed three latent `pkg/jsruntime` bugs —
> so the release shipped ahead of its own green run. Check first:
>
> ```bash
> gh run list --workflow ci.yml --limit 1   # must be `completed success` for your commit
> ```
>
> Only then tag. If CI is red, fix the cause and tag the fix.

You can also run it manually from the Actions tab (`workflow_dispatch`) by
supplying a tag name.

## Container images

`.github/workflows/docker.yml` builds multi-arch images (`linux/amd64`,
`linux/arm64`) and pushes them to GHCR:

```
ghcr.io/aone2233/nekomari:<tag>
ghcr.io/aone2233/nekomari:latest
```

```bash
docker run -d --name nekomari   -p 25774:25774   -v nekomari-data:/app/data   ghcr.io/aone2233/nekomari:latest
```

It runs on `release: published`, or manually via `workflow_dispatch` with a tag.
It consumes the binaries from the GitHub Release rather than rebuilding them, so
the binary inside the image is the same one you download from the release page —
and the CGO cross-compile problem does not come back.

### Checking that the image is publicly pullable

GHCR answers the *first* unauthenticated manifest request with `401` **whether or
not the package is public** -- you have to fetch an anonymous token first. A bare
`curl .../manifests/latest` therefore tells you nothing, which is an easy way to
misread a public image as private.

Do it in two steps instead:

```bash
TOKEN=$(curl -s "https://ghcr.io/token?scope=repository:<owner>/nekomari:pull&service=ghcr.io" | jq -r .token)
curl -s -o /dev/null -w '%{http_code}
'   -H "Authorization: Bearer $TOKEN"   -H "Accept: application/vnd.oci.image.index.v1+json"   "https://ghcr.io/v2/<owner>/nekomari/manifests/latest"
```

`200` means anyone can `docker pull` it. The docker workflow runs this check at
the end of every build, so a package that ends up private is reported rather
than discovered by a user.

## What gets built

| Artifact | Contents |
|---|---|
| `nekomari-<os>-<arch>` | Server + web panel. The default theme is embedded, so it is a single self-contained binary. |
| `komari-agent-<os>-<arch>[.exe]` | The monitoring agent. |
| `SHA256SUMS.txt` | Checksums for the above. |

Platforms: `linux/amd64`, `linux/arm64`, `windows/amd64`.

## Two things to be careful about

### 1. The agent asset name is a compatibility contract

The agent's self-updater looks for a specific filename:

```go
// agent/update/update.go
func expectedAssetName(goos, goarch string) string {
	name := fmt.Sprintf("komari-agent-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}
```

So the agent asset **keeps the `komari-agent-` prefix** even though the project
is called Nekomari. Renaming it without also changing `expectedAssetName` would
silently break auto-update: the agent would report "no suitable asset found" and
stay on its current version forever.

### 2. Why the builds are native rather than cross-compiled

The server links `mattn/go-sqlite3` through CGO, so:

```bash
GOOS=linux CGO_ENABLED=0 go build   # fails
```

```
internal/sqlitetune/connector.go:109:21: conn.Exec undefined
    (type *sqlite3.SQLiteConn has no field or method Exec)
```

Upstream solved this with a bundled `zig cc` cross-compiler. This fork instead
builds on a runner of each target OS (`ubuntu-latest`, `windows-latest`), so
every command is the same one a contributor would run locally. The one
exception is `linux/arm64`, which cross-compiles on an amd64 runner using
`gcc-aarch64-linux-gnu`.

## Verifying a release

There are two levels, and the second is the one that matters most.

### 1. Reproduce the build locally

Both workflow build commands can be reproduced without CI:

```bash
# server (add GOOS/GOARCH for a non-native target)
go build -trimpath \
  -ldflags="-s -w -X github.com/Aone2233/nekomari/utils.CurrentVersion=v0.1.2 -X github.com/Aone2233/nekomari/utils.VersionHash=$(git rev-parse --short HEAD)" \
  -o nekomari-$(go env GOOS)-$(go env GOARCH) .

# agent
(cd agent && go build -trimpath \
  -ldflags="-s -w -X github.com/Aone2233/nekomari/agent/update.CurrentVersion=v0.1.2" \
  -o komari-agent-$(go env GOOS)-$(go env GOARCH) .)
```

Check that the version actually landed in the binary — a wrong package path in
`-X` fails silently:

```bash
./nekomari-<os>-<arch> --help | head -1     # expect "Nekomari Monitor v0.1.2"
```

### 2. Deploy the published artifacts

`deploy/deploy-verify.sh` treats the Release as a user would: it downloads the
assets, verifies them against `SHA256SUMS.txt`, starts the server on its own port
with its own data directory, completes the first-run install through the API,
connects an agent, and confirms the node reports. It cleans up after itself.

```bash
bash deploy/deploy-verify.sh                 # latest release, throwaway temp dir
bash deploy/deploy-verify.sh /tmp/dv 25799 v0.1.2   # explicit dir/port/tag
```

This runs automatically in CI as the `verify` job, after `release`. It exists
because `build` only proves the code compiles and `release` only proves the assets
upload — neither would have caught the v0.1.2 container bug, where the image's
binary did not match its base image's libc so every `docker run` failed instantly
while all three pipelines were green.

Because it uses its own port and data directory, it is safe to run on a host that
is already serving a panel.

## A note on which workflow files exist

The upstream repository shipped ten workflows tied to its multi-repo layout and
a module path this fork no longer uses. They were removed rather than left to
fail. What remains:

- `ci.yml` — build + hermetic tests on push and pull request
- `release.yml` — the release pipeline, then the deploy verification above
- `docker.yml` — multi-arch container images to GHCR, chained off `release`

`docs/TESTING.md` explains which tests are hermetic and which need a privileged
or IPv6-capable host. `docs/DEPLOY-OC424.md` documents the reference deployment,
and `docs/RETIRE-CF-PROBE.md` the monitoring stack this fork replaced.
