# Nekomari Mint extension

Mint is an optional, separately installed Komari theme:
<https://github.com/Aone2233/komari-theme-nekomari-mint>.
It does not replace the embedded frontend or the administration panel. The theme
also works with official Komari 1.6.0, without the extensions described here.

## Compatibility and installation

Import `nekomari-mint.zip` through the administration panel's theme page, activate
it, and configure **Mint theme settings**. Do not unzip it into `frontend/dist`.
Keep the previous theme available so switching back requires no server rollback.

The additive server/agent changes released in v1.6.13 provide the optional evidence below.
An old agent does not provide TCP retransmission or route results; an old server
returns method-not-found and Mint shows the basic monitoring view. Update both
server and agent for the complete extension. Back up the database before any
server upgrade: v1.6.13 adds `routes`; v1.6.14 adds nullable `routes_probed_at`
to `unlock_reports` without removing existing rows.

No production deployment or stable backend release is part of the theme install.
In particular, publishing a new backend release may trigger agent auto-updates;
schedule that rollout separately after CI passes on the exact commit.

## Public read API

`POST /api/rpc2`, JSON-RPC method `public:getNodeInsights`:

```json
{"jsonrpc":"2.0","id":1,"method":"public:getNodeInsights","params":{"uuid":"node-uuid","include_ip":true}}
```

The result has `version: 1` and nullable `unlock`, `routes`, and `ip` objects.
`unlock`/`routes` carry `probed_at`, `stale`, and `results`. Snapshots older than
12 hours (media/AI) or 30 hours (routes), or more than five minutes in the future,
are stale. Media/AI probes run every six hours; routes have an independent
24-hour clock. HTTP requests from the theme never trigger probes.

Private-site login and hidden/deleted node visibility apply before lookup.
Hidden and nonexistent UUIDs receive the same error. The new endpoint omits the
raw egress IP and raw traceroute hops. Optional IP metadata resolves only the
server's stored node IPv4/IPv6 address, not an arbitrary caller-supplied address,
and reuses the existing provider cache with an eight-second request budget.
Provider requests necessarily disclose that queried address to the configured
provider. Disable extensions in Mint settings to stop theme-initiated lookups.

IP metadata describes the stored node address, not necessarily its NAT/proxy
egress. It reports provider-backed organization and network type, never an IP
reputation score. Failed lookups leave `ip` null, and unknown classification
remains unknown.

## Opt-in route observations

No carrier targets are built in. Select public numeric addresses you are
authorized to probe, in the actual carrier networks you want to observe. Configure
the existing agent configuration file, environment, or flag:

```text
AGENT_ROUTE_TARGETS=telecom=<public-telecom-IP>,unicom=<public-unicom-IP>,mobile=<public-mobile-IP>
--route-targets 'telecom=<public-telecom-IP>,unicom=<public-unicom-IP>'
```

Angle-bracket placeholders must be replaced, not pasted literally. Each carrier
may appear at most once. Private, loopback, link-local, documentation, multicast,
and other recognized reserved addresses are rejected. DNS names are not accepted.
The default empty value disables route probing.

Install the full Linux `traceroute` utility with `-I -A` support (Debian/Ubuntu:
`apt install traceroute`). The optional probe invokes it without a shell, limits
each target to 30 hops/45 seconds and output to 32 KiB, with a 140-second round
budget. ICMP needs root or appropriate raw-socket permission on traceroute itself.
The full traceroute binary is not bundled in the
agent or guaranteed in its container image; BusyBox alone is insufficient.
Unsupported platforms and failures remain unknown, not zero or healthy.

The schedule and results persist in `--route-state-file` (environment
`AGENT_ROUTE_STATE_FILE`, default `./route_probe_state.json` in the agent working
directory). The file is mode 0600 and atomically replaced. A daily slot is saved
before network work, so restarting, a timeout, or a crash does not repeatedly
probe the same targets. Do not delete it as part of an upgrade. Corrupt/unwritable
state prevents probing until repaired. Changing targets starts a new observation.
Re-uploading a cached result preserves its original route timestamp.

Labels are observed ASN evidence: AS4134=163, AS4809=CN2, AS4837=169,
AS9929=9929, AS58807=CMIN2, AS58453=CMI, AS9808=CMNET. Seeing an ASN is not a
guarantee of a premium route (in particular it does not prove CN2 GIA), return
path symmetry, reachability of every application, or SLA. `reached` is independent
of the label. Targets appear in the public extension response, so do not configure
private infrastructure addresses or sensitive destinations.

## Metric and evidence semantics

- `tcp_retrans_percent` in latest status is the Linux interval delta of
  `/proc/net/snmp` TCP RetransSegs divided by OutSegs, times 100. First/idle/reset
  samples are null. It is host-wide TCP retransmission, not carrier packet loss.
  `HOST_PROC` is honored; containers otherwise report their own network namespace.
- `traffic_up`/`traffic_down` are included only with `quality.traffic_cycle=ok`.
  Kernel lifetime counters are never substituted for billing-cycle usage.
- Public ping records retain `family`, `ping_type`, and `role` so IPv4/IPv6,
  protocols, and reference targets cannot silently become one synthetic line.
- Netflix/YouTube results describe recognized catalog/Premium page evidence,
  not a streaming-account or playback guarantee. Generic redirects, challenge
  and consent pages stay unknown. AI probes cover ChatGPT (OpenAI compliance),
  Claude login, and Gemini application pages; reachable pages are `partial`,
  never proof of working conversations. Explicit service region denials are
  `blocked`; country-only fallbacks remain `unknown`/`region`.

## Reference implementations and Passkey compatibility

NodeQuality's `part/trace.sh` delegates to oneclickvirt/nt3. Its ICMP/30-hop/ASN
approach informed this implementation; no remote installer or GPL/AGPL shell
code is executed or bundled. The native Go implementation uses the installed
traceroute utility and operator-selected targets, not nt3's complete city matrix.

Chen017/komari-plugin-ipqa-alert-report consumes IP-Quality-Archive reports; it is
not itself a network probe. Its distinction between unavailable, partial, unknown
and stale evidence informs result semantics here. This is not an IPQA archive
importer, IPv4/IPv6 matrix, historical diff engine or integration with that plugin.
References: <https://github.com/LloydAsp/NodeQuality>,
<https://github.com/oneclickvirt/nt3>,
<https://github.com/Chen017/komari-plugin-ipqa-alert-report>.

Private sites allow only GET `/api/komari-passkey/status` and POST authentication
options/verify before login. The installed Passkey plugin still validates origin,
challenge, signature and session issuance. Registration, credential management
and unrelated plugin APIs remain private. Mint supplies the plugin's login DOM
mount points; it does not implement WebAuthn or alter existing credentials.

## Verification

New tests cover visibility/redaction/private-site authorization, IP summary
redaction, ping family preservation, TCP counter deltas, route target validation,
ASN parsing, and the additive latest-status fields. See `docs/TESTING.md` for the
existing external-network test exclusions. Use the repository's patched release
Go toolchain and binary vulnerability scans before distributing executables.
