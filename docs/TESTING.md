# Testing

## Running the suite

```bash
# server + shared packages
go test ./...

# agent (nested module)
(cd agent && go test ./...)
```

CI runs the whole suite and excludes only the tests that genuinely need the
network, root or IPv6 — a denylist, not an allowlist:

```bash
go test ./... -count=1 -skip '^TestIpInfo$'
(cd agent && go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing')
```

The anchors matter: `^TestIpInfo$` skips only the upstream `utils/geoip` test that
requests ipinfo.io, while Nekomari's `TestIpInfo*` cases still run. This used to be
a `-run` allowlist, which silently excluded every test not named in it — 552 root
tests down to 79 — and it hid two `agent/unlock` tests that had been failing since
the commit that added them.

Frontend checks run from `frontend` after `npm ci`: `npm test`, `npm run lint`
and `npm run build`. A separate CI job starts Vite on loopback and drives
Chromium against five mounted fixtures. To run them locally:

```bash
cd frontend
python -m pip install -r script/requirements-browser.txt
python -m playwright install chromium
python script/chart-a11y.spec.py
python script/compiler-static.browser.spec.py
python script/admin-clock.browser.spec.py
python script/file-manager.browser.spec.py
python script/selector-state.browser.spec.py
python script/number-picker.browser.spec.py
python script/remote-file-tree.browser.spec.py
```

| Spec | Fixture mounts | Covers |
|---|---|---|
| `chart-a11y.spec.py` | `components/ui/chart` | keyboard focus name/outline/tooltip, the live region updating on a keyboard tooltip, theme-resolved series and legend colors, a decorative legend icon, series labels naming the chart, and an animated series update reaching new data |
| `compiler-static.browser.spec.py` | `components/PriceTags` | a free-to-paid transition reads the clock on the paid mount |
| `admin-clock.browser.spec.py` | `pages/admin/sessions`, `pages/admin/dashboard` | time-dependent labels sharing one live clock without refetching, expiry boundaries and renewed exclusion, and a mounted dashboard moving its expiry window and urgency |
| `file-manager.browser.spec.py` | `pages/terminal/FileManagerPanel`, `pages/terminal/FileEditorDialog` | rename/delete refreshing the real directory, ctrl-deselect keeping only the remaining file selected, a stale directory response not replacing a newer node, a delayed read updating its own tab, and a post-reconnect refresh ignoring a prior connection's response |
| `selector-state.browser.spec.py` | `components/SelectorDialog`, `components/NodeSelectorDialog` | cancel/confirm and external value updates, an uncontrolled node dialog's open/cancel/confirm, and a parent-controlled open without a trigger |
| `number-picker.browser.spec.py` | `components/ui/number-picker`, and the real `pages/admin/log` | one `onChange` per valid keystroke, an out-of-range draft kept then clamped on blur, a `defaultValue` change re-syncing without reporting, an unrelated parent render reporting nothing, and the log page's pagination: page 2 stays page 2, and editing the limit returns to page 1 exactly once |
| `remote-file-tree.browser.spec.py` | `pages/terminal/RemoteFileTree` | the root listing exactly once on mount, the directory memo (expanding lists once and re-expanding does not re-list), refresh forcing a re-list, reveal expanding and listing its ancestor chain, a root-path change dropping the memo, and selection and context-menu targeting |

Two limits are worth knowing before trusting a green run. The fixtures mount
components directly rather than routing to a page, so a page-level wiring bug is
invisible to them — a page can loop on mount and every fixture stays green, which
is exactly what happened to `pages/admin/settings/sign-on`. And `file-manager`
never opens the file tree, so `remote-file-tree` is what covers the tree now;
every admin page other than `dashboard` and `log` still has **no** browser
coverage, and changes there rest on the type checker, lint, the unit tests and
the build.

`npm run audit:compiler` is an advisory migration inventory and exits nonzero
while the remaining React Compiler recommendations are unresolved. It is not a
release gate; the configured `npm run lint` has zero allowed warnings. Read its
counts from the JSON formatter rather than the default `stylish` output, which
interleaves each finding's primary and related locations and so overstates the
total by roughly 5%.

## Environment-dependent tests

Some upstream tests exercise the real network and therefore depend on the host
they run on. They are **not** hermetic and will fail in restricted environments:

| Test | Requirement | Typical failure |
|---|---|---|
| `TestICMPPing` (agent) | privilege to open raw ICMP sockets (root, or `setcap cap_net_raw+ep`) | `error setting traffic class: ... access permissions` |
| `TestTCPPing` / `TestHTTPPing` (agent) | IPv6 connectivity, reachability of the fixed upstream hosts | `unreachable network`, `http status not ok` |

These fail identically on the **unmodified upstream** code (verified against
`komari-agent` at tag `1.5.0`), so a red run here is an environment signal, not
a regression. Run them as root on a host with IPv6:

```bash
sudo env "PATH=$PATH" go test -run 'TestICMPPing|TestTCPPing|TestHTTPPing' ./server/
```

## Tests added by Nekomari

These are hermetic (no network) unless noted:

| Test | Covers |
|---|---|
| `pkg/netcheck` — `TestDecide` | all 7 verdict branches of the reachability probe |
| `pkg/netcheck` — `TestDecideAdviceHasNoFormatVerbs` | regression for an unescaped `%` that produced `100%!超(MISSING)时` |
| `pkg/netcheck` — `TestRunLoopback` / `TestRunPortInTarget` / `TestRunBadDNS` | end-to-end probe paths (loopback only) |
| `agent/server` — `TestDecideAuto` | all 8 branches of the `auto` protocol decision |
| `agent/server` — `TestIsPermissionErr` | separating "no local ICMP permission" from "target unreachable" |
| `agent/server` — `TestResolveAutoCaches` | resolution cache behaviour |
| `agent/server` — `TestMeasureWithRetriesReportsSuccessAfterRetransmit` / `TestTcpRetransmitSuspected` | a completed TCP handshake is reported with the retry's RTT, not as packet loss |
| `agent/unlock` — `TestProbe*` | Netflix (full / originals-only / blocked / transient), YouTube Premium marker, and region-only services. All transports are faked, so the package is hermetic |
| `internal/server` — `TestClientIPIgnoresForwardedHeaderFromUntrustedPeer` | `X-Forwarded-For` is honoured only from loopback/private peers, not from a public client |
| `agent/server` — `TestProbeAutoProtocolLive` | **opt-in**, real network: `NEKOMARI_LIVE_PROBE=1` (run as root) |
| `internal/metricstore` — `TestWritePingRecordsKeepsProtocolsDistinct` | dual-probe results stay two distinguishable series |
| `internal/metricstore` — `TestWritePingRecordsOmitsEmptyProtocol` | old probes without a protocol keep the historical tag shape |
| `web/api/ipinfo` — `TestIpInfo*` | IP information API: contract shapes for all four endpoints, per-source graceful degradation, stale/negative caching, the ip-api.com rate-limit cooldown, private-IP rejection, IPv6, and classification derivation. All four upstreams are faked with `httptest`, so the package is hermetic |
| `web/router` — `TestIpInfoRoutesRegistered` / `TestIpInfoAdminRefreshRequiresAdmin` | the four `/api/*/ip-info/v1` routes exist and the refresh route stays behind `RequireRole(admin)` |
| `utils` — `TestPingTargetHost` / `TestPingTargetFamily` | target parsing: `1.1.1.1:443`, `[2001:db8::1]:443`, a bare IPv6 literal (whose colons are not a port separator), hostnames, and IPv4-mapped IPv6 classified as IPv4 |
| `utils` — `TestClientCanReachFamily` | address-family classification, including the two cases that must **not** filter: a hostname target, and a node with no addresses recorded yet |
| `utils` — `TestFilterClientsByTargetFamily` | the filter: drops mismatches and deleted nodes, keeps order, passes everything through for a hostname target |

## Known failures in unprivileged environments

A full `go test ./...` reports two failures on a host where the test process cannot
write its own temporary directory — observed on a systemd service with a confined
`/tmp`. Both are in `pkg/jsruntime`:

```
TestNodeCoreModulesAndECMAScriptBuiltins
TestStorageDirIsConfinedAdditionalRoot
```

They exercise a Node child process that needs to create its own temp directory. The
package is not otherwise touched by this fork. If you see exactly these two and
nothing else, it is the environment rather than the change under test — confirm by
running the same command as root, where both pass. GitHub-hosted runners have a
normal `/tmp`, so CI runs them.

## Running the browser and shell suites on Windows

Two environment problems cost an afternoon on 2026-09-26; both are worth knowing
before concluding a change is broken.

**`npm run build` fails at the very end with `Access is denied`.** The error is
`[vite:esbuild-transpile] remove …\Temp\esbuild-<hex>: Access is denied`, after
`built in`, i.e. every chunk was produced and only esbuild's temp-file cleanup
failed. It reproduces on a pristine `d37bc01`, so it is the host, not the change.
`TMPDIR` does **not** help — Node ignores it on Windows and `os.tmpdir()` keeps
returning `%LOCALAPPDATA%\Temp`. What does help is pointing `TEMP` and `TMP` into a
directory the build can delete from:

```powershell
$env:TEMP = "C:\path\to\workspace\.runtest\tmp"; $env:TMP = $env:TEMP
cd frontend; npm run build
```

**The browsery specs need Playwright's own Chromium.** `python -m playwright
install chromium` fetches it; with only a system Chrome available they cannot
launch. Nothing else about them is environment-specific. When Chromium is present,
the suites run as CI runs them:

```powershell
cd frontend
# every mounted fixture
Get-ChildItem script/*.browser.spec.py | ForEach-Object { python $_.Name }

# the real panel, end to end — needs a server, which CI builds and installs
python script/panel-smoke.spec.py http://127.0.0.1:25774
python script/admin-write-paths.spec.py http://127.0.0.1:25774
```

`panel-smoke.spec.py` reports external failures (its update check reaches
`api.github.com`) as warnings and never fails on them, so a blocked third party
does not read as a regression here either.

The deploy scripts have offline shell tests that are worth running on the
workstation too — they need `bash` (Git Bash is enough) and no host access:

```bash
bash deploy/install-node-agent.test.sh   # the token must not reach the unit
bash deploy/panel-probe.test.sh
```

### MAC-WAN as the standing browser-test host

The browser suites also run on **MAC-WAN**, and it is the better place for a long
run: 4 cores, 8 GB, a real Linux userspace, and no Windows temp-file quirks. It was
set up on 2026-09-26 and the full suite was run there to prove it — 43/43.

What is installed, and what was already there:

| Piece | Where | Note |
|---|---|---|
| Chromium | `~/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome` | already downloaded; `chrome --version` → `Google Chrome for Testing 153.0.8010.12` |
| Python Playwright | `~/.venvs/browsertest` (1.63.0) | **was missing** — the browser was cached but no package could drive it |
| Repo | `~/nekomari-test/frontend` | source only; `npm ci` there installs the 761 packages the specs need |

The revision matched by luck worth knowing about: Playwright 1.63.0 wants chromium
revision **1243**, which is exactly what was already in the cache, so no second
download was needed. If a future Playwright bump asks for a different revision,
`~/.venvs/browsertest/bin/playwright install chromium` fetches it.

To refresh the checkout and run the suite:

```bash
# from the workstation, shipped as a tar of the committed tree
git archive --format=tar -o /tmp/frontend.tar HEAD frontend
scp /tmp/frontend.tar MAC-WAN:/home/macos/frontend.tar
ssh MAC-WAN 'tar -xf ~/frontend.tar -C ~/nekomari-test'

# then on MAC-WAN
cd ~/nekomari-test/frontend
for f in script/*.browser.spec.py; do ~/.venvs/browsertest/bin/python "$f"; done
```

`npm ci` only needs re-running when `package-lock.json` changes.

```bash
# live protocol-resolution check (needs root for real ICMP)
NEKOMARI_LIVE_PROBE=1 sudo -E env "PATH=$PATH" go test -run TestProbeAutoProtocolLive -v ./agent/server/
```
