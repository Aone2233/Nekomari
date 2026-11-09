# Probe Truth calibration harness

This directory starts the v1.8 calibration gate. It is intentionally strict:
an item that cannot be measured is `NOT RUN` and makes the report fail; it is
never reported as a pass.

## Current slice: host

```bash
# Capture an Agent report JSON during the same window, then:
python3 deploy/calibrate/probe_calibrate.py --report-json /tmp/agent-report.json --interval 10
```

The host checks compare:

| Item | Tolerance |
|---|---|
| CPU | ±2 percentage points absolute |
| RAM used | `max(1% of total, 4 KiB)`; definition differences are reported |
| Disk used | ±0.5% relative to `df /` |
| Network counter delta | ±0.5% or 4 KiB |
| Traffic interval | exactly 0 bytes; gaps must be unknown |

The report is written to `.runtest/probe-calibration-<UTC>/report.json`.

## Not yet wired (must not be claimed as pass)

- ICMP loopback ±50 µs / LAN ±1 ms
- TCP ±1 ms
- HTTP phases `max(5 ms, 2%)`
- Loss ≤1 percentage point with `tc netem`
- Clock skew rejection
- Boot-id restart count
- Retention expiry unknown

These require a disposable Agent/Panel fixture and, for `tc netem`, root. The
harness emits `NOT RUN` for them today so the release gate stays red until the
fixture exists.
