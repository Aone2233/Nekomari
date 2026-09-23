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

```bash
# live protocol-resolution check (needs root for real ICMP)
NEKOMARI_LIVE_PROBE=1 sudo -E env "PATH=$PATH" go test -run TestProbeAutoProtocolLive -v ./agent/server/
```
