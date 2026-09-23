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
and `npm run build`. The separate frontend browser job starts Vite on loopback
and uses Chromium to check animated chart data, dark/light colors, keyboard
focus, accessible naming and mounted price transitions. To run it locally:

```bash
cd frontend
python -m pip install -r script/requirements-browser.txt
python -m playwright install chromium
python script/chart-a11y.spec.py
python script/compiler-static.browser.spec.py
```

`npm run audit:compiler` is an advisory migration inventory and exits nonzero
while the remaining React Compiler recommendations are unresolved. It is not a
release gate; the configured `npm run lint` has zero allowed warnings.

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
