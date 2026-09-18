# Testing

## Running the suite

```bash
# server + shared packages
go test ./...

# agent (nested module)
(cd agent && go test ./...)
```

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
package is not otherwise touched by this fork, and neither test is in the CI filter
above. If you see exactly these two and nothing else, it is the environment rather
than the change under test — confirm by running the same command as root, where both
pass.

> The CI `-run` filter uses `TestIpInfo[A-Z]` instead of a bare `TestIpInfo`
> prefix: the upstream `utils/geoip` package already has a test literally named
> `TestIpInfo`, and it makes real requests to ipinfo.io.

```bash
# live protocol-resolution check (needs root for real ICMP)
NEKOMARI_LIVE_PROBE=1 sudo -E env "PATH=$PATH" go test -run TestProbeAutoProtocolLive -v ./agent/server/
```
