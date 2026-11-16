# Probe Truth calibration harness

This directory starts the v1.8 calibration gate. It is intentionally strict:
an item that cannot be measured is `NOT RUN` and makes the report fail; it is
never reported as a pass.

## Current slice: host

```bash
# Capture an Agent report JSON during the same window, then:
python3 deploy/calibrate/probe_calibrate.py --report-json /tmp/agent-report.json --interval 10
```

The report must carry `quality: ok` for a measured CPU, RAM or disk value;
unknown or absent quality is `NOT RUN`. The report still has to be captured
over the same window as the reference: this script does not establish that
alignment on its own. RAM uses the reported `gopsutil`, `includeCache` or
`htoplike` mode and matching `/proc/meminfo` fields; an unrecognised mode or
missing reference field is `NOT RUN`. Disk compares the Agent's aggregate to
`/` only when the report proves it contains exactly that mount and its
aggregate matches it; other mount scopes require a separate reference.

| Item | Tolerance |
|---|---|
| CPU | ±2 percentage points absolute |
| RAM used | `max(1% of total, 4 KiB)`; definition differences are reported |
| Disk used | ±0.5% relative to `df /` |
| Network counter delta | NOT RUN: an aligned pair of Agent counters and interface scope is required |
| Traffic interval | NOT RUN: a validated gap-free window is required |

The report is written to `.runtest/probe-calibration-<UTC>/report.json`.

## Locked btop-terminal live-status check

`btop_terminal_live_check.py` starts a disposable Panel and real Agent, checks
the locked archive hash, and compares the theme's online count against a
separate `/api/clients` request at 320 and 390 pixels. It writes only binary
hashes, boolean results and counts. Exit 1 means failed or incomplete; it is
not a full theme compatibility test. Install Python Playwright and its
Chromium browser first. Run in an isolated network namespace because the
Agent can make its own external requests; the browser's requests are already
restricted to the disposable Panel. For example, on a Linux host where
network namespace creation is available:

```bash
unshare -n sh -c 'ip link set lo up && python3 deploy/calibrate/btop_terminal_live_check.py \
  --panel-binary /path/to/panel --agent-binary /path/to/agent \
  --archive /path/to/btop-terminal.zip --output /tmp/btop-check.json'
```

The original 1.0.3 archive currently fails: the independent control reports
the Agent online while the theme reports zero online nodes. See
[`V1.8.1-THEME-SMOKE.md`](../../docs/V1.8.1-THEME-SMOKE.md) for controls,
baseline comparison and limitations.

## Not yet wired (must not be claimed as pass)

- ICMP loopback ±50 µs / LAN ±1 ms
- TCP ±1 ms
- HTTP phases `max(5 ms, 2%)`
- Loss under `tc netem`: compare Agent results with independent sent/received
  counts, then assess the random injection rate against a predeclared confidence interval
- Clock skew rejection
- Boot-id restart count
- Retention expiry unknown

These require a disposable Agent/Panel fixture and, for `tc netem`, root. The
harness emits `NOT RUN` for them today so the release gate stays red until the
fixture exists.
