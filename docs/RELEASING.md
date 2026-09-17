# Releasing

## Cut a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which builds every
platform, creates the GitHub Release and attaches the artifacts plus
`SHA256SUMS.txt`.

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

> **One manual step after the first push:** GHCR creates packages as *private*.
> A token without the `read:packages` scope cannot change that through the API,
> so the first time you publish an image, flip it in the UI:
> **Package settings → Change visibility → Public**. The check is
> `curl -o /dev/null -w '%{http_code}' https://ghcr.io/v2/<owner>/nekomari/manifests/latest`
> — `401` means it is still private, `200` means it is pullable anonymously.

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

## Verifying a release locally

Both workflow build commands can be reproduced without CI:

```bash
# server (add GOOS/GOARCH for a non-native target)
go build -trimpath \
  -ldflags="-s -w -X github.com/Aone2233/nekomari/utils.CurrentVersion=v0.1.0" \
  -o nekomari-$(go env GOOS)-$(go env GOARCH) .

# agent
(cd agent && go build -trimpath \
  -ldflags="-s -w -X github.com/Aone2233/nekomari/agent/update.CurrentVersion=v0.1.0" \
  -o komari-agent-$(go env GOOS)-$(go env GOARCH) .)
```

Check that the version actually landed in the binary — a wrong package path in
`-X` fails silently:

```bash
./nekomari-<os>-<arch> --help | head -1     # expect "Komari Monitor v0.1.0"
```

## A note on which workflow files exist

The upstream repository shipped ten workflows tied to its multi-repo layout and
a module path this fork no longer uses. They were removed rather than left to
fail. What remains:

- `ci.yml` — build + hermetic tests on push and pull request
- `release.yml` — the release pipeline above

`docs/TESTING.md` explains which tests are hermetic and which need a privileged
or IPv6-capable host.
