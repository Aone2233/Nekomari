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

> The CI `-run` filter uses `TestIpInfo[A-Z]` instead of a bare `TestIpInfo`
> prefix: the upstream `utils/geoip` package already has a test literally named
> `TestIpInfo`, and it makes real requests to ipinfo.io.

```bash
# live protocol-resolution check (needs root for real ICMP)
NEKOMARI_LIVE_PROBE=1 sudo -E env "PATH=$PATH" go test -run TestProbeAutoProtocolLive -v ./agent/server/
```
