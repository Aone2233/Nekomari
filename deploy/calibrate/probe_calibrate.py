#!/usr/bin/env python3
"""Probe Truth calibration harness (v1.8 host slice).

This is deliberately not a "green by default" tool: an item that cannot be
measured is reported as NOT RUN, never as PASS. Host calibration compares an
Agent report JSON with /proc/df over the same window. Ping/HTTP live checks
are gated on a disposable Agent/Panel and are marked NOT RUN here until that
fixture is wired in.

Usage:
  python3 deploy/calibrate/probe_calibrate.py --report-json /tmp/report.json
  python3 deploy/calibrate/probe_calibrate.py --interval 10
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


TOLERANCES = {
    "cpu_percentage_points": 2.0,
    "ram_max": "max(1%, 4 KiB)",
    "disk_relative": 0.005,
    "network_rate_relative": 0.005,
    "traffic_interval_bytes": 0,
    "icmp_loopback_us": 50,
    "icmp_lan_ms": 1.0,
    "tcp_ms": 1.0,
    "http_phase": "max(5 ms, 2%)",
    "loss_percentage_points": 1.0,
}


def read_proc_stat() -> tuple[int, int]:
    with open("/proc/stat", "r", encoding="utf-8") as handle:
        line = handle.readline()
    fields = line.split()
    if not fields or fields[0] != "cpu":
        raise RuntimeError("/proc/stat has no aggregate cpu line")
    values = [int(value) for value in fields[1:]]
    if len(values) < 4:
        raise RuntimeError("/proc/stat has an incomplete aggregate cpu line")
    # guest and guest_nice are already included in user and nice.
    total = sum(values[:8])
    idle = values[3] + (values[4] if len(values) > 4 else 0)
    return total, idle


def read_meminfo() -> dict[str, int]:
    result: dict[str, int] = {}
    with open("/proc/meminfo", "r", encoding="utf-8") as handle:
        for line in handle:
            parts = line.split()
            if len(parts) >= 2:
                try:
                    result[parts[0].rstrip(":")] = int(parts[1]) * 1024
                except ValueError:
                    pass
    return result


def read_net_total() -> tuple[int, int]:
    up = down = 0
    with open("/proc/net/dev", "r", encoding="utf-8") as handle:
        for line in handle.readlines()[2:]:
            if ":" not in line:
                continue
            name, data = line.split(":", 1)
            if name.strip() == "lo":
                continue
            fields = data.split()
            if len(fields) >= 9:
                down += int(fields[0])
                up += int(fields[8])
    return up, down


def read_uptime() -> float:
    with open("/proc/uptime", "r", encoding="utf-8") as handle:
        return float(handle.read().split()[0])


def host_sample() -> dict[str, Any]:
    total, idle = read_proc_stat()
    mem = read_meminfo()
    disk = shutil.disk_usage("/")
    up, down = read_net_total()
    return {
        "cpu_total": total,
        "cpu_idle": idle,
        "mem_total": mem.get("MemTotal", 0),
        "mem_available": mem.get("MemAvailable", 0),
        "mem_cached": mem.get("Cached", 0),
        "mem_buffers": mem.get("Buffers", 0),
        "disk_total": disk.total,
        "disk_used": disk.used,
        "net_up": up,
        "net_down": down,
        "uptime": read_uptime(),
    }


def relative_error(actual: float, expected: float) -> float:
    if expected == 0:
        return 0.0 if actual == 0 else float("inf")
    return abs(actual - expected) / abs(expected)


def max_abs_or_relative(actual: float, expected: float, absolute: float, relative: float) -> bool:
    return abs(actual - expected) <= max(absolute, abs(expected) * relative)


def compare_report(report: dict[str, Any], before: dict[str, Any], after: dict[str, Any], elapsed: float) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []

    def add(name: str, actual: Any, expected: Any, passed: bool, detail: str) -> None:
        results.append({"item": name, "actual": actual, "expected": expected, "pass": passed, "detail": detail})

    total_delta = after["cpu_total"] - before["cpu_total"]
    idle_delta = after["cpu_idle"] - before["cpu_idle"]
    expected_cpu = (1 - idle_delta / total_delta) * 100 if total_delta > 0 and 0 <= idle_delta <= total_delta else None
    actual_cpu = report.get("cpu", {}).get("usage")
    if expected_cpu is None or not isinstance(actual_cpu, (int, float)):
        add("cpu", actual_cpu, expected_cpu, False, "NOT RUN: missing CPU interval or report value")
    else:
        ok = abs(actual_cpu - expected_cpu) <= TOLERANCES["cpu_percentage_points"]
        add("cpu", actual_cpu, round(expected_cpu, 4), ok, "±2 percentage points absolute")

    actual_ram = report.get("ram", {}).get("used")
    expected_ram = after["mem_total"] - after["mem_available"]
    if isinstance(actual_ram, (int, float)):
        tolerance = max(after["mem_total"] * 0.01, 4096)
        ok = abs(actual_ram - expected_ram) <= tolerance
        add("ram_used", actual_ram, expected_ram, ok, "max(1% of total, 4 KiB); definition differences are reported, not hidden")
    else:
        add("ram_used", actual_ram, expected_ram, False, "NOT RUN: report has no RAM used")

    actual_disk = report.get("disk", {}).get("used")
    if isinstance(actual_disk, (int, float)):
        ok = relative_error(actual_disk, after["disk_used"]) <= TOLERANCES["disk_relative"]
        add("disk_used", actual_disk, after["disk_used"], ok, "±0.5% relative to df /")
    else:
        add("disk_used", actual_disk, after["disk_used"], False, "NOT RUN: report has no disk used")

    network = report.get("network", {})
    if isinstance(network.get("totalUp"), (int, float)) and isinstance(network.get("totalDown"), (int, float)):
        up_delta = after["net_up"] - before["net_up"]
        down_delta = after["net_down"] - before["net_down"]
        up_ok = abs(network["totalUp"] - up_delta) <= max(4096, abs(up_delta) * 0.005) if up_delta >= 0 else False
        down_ok = abs(network["totalDown"] - down_delta) <= max(4096, abs(down_delta) * 0.005) if down_delta >= 0 else False
        add("network_up_delta", network["totalUp"], up_delta, up_ok, "kernel counter delta, ±0.5% or 4 KiB")
        add("network_down_delta", network["totalDown"], down_delta, down_ok, "kernel counter delta, ±0.5% or 4 KiB")
    else:
        add("network_delta", None, None, False, "NOT RUN: report has no kernel counters")

    return results


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report-json", help="Agent report JSON captured during the window")
    parser.add_argument("--interval", type=float, default=10.0, help="Host sampling interval in seconds")
    parser.add_argument("--output", help="Report directory (default .runtest/probe-calibration-<UTC>)")
    args = parser.parse_args()

    if args.interval < 1:
        parser.error("--interval must be >= 1 second")
    before = host_sample()
    time.sleep(args.interval)
    after = host_sample()

    report: dict[str, Any] = {}
    if args.report_json:
        with open(args.report_json, "r", encoding="utf-8") as handle:
            report = json.load(handle)

    results = compare_report(report, before, after, args.interval)
    for name in ("icmp", "tcp", "http", "loss", "clock_skew", "restart", "retention"):
        results.append({"item": name, "actual": None, "expected": None, "pass": False, "detail": "NOT RUN: requires the disposable Agent/Panel fixture"})

    now = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    out_dir = Path(args.output or f".runtest/probe-calibration-{now}")
    out_dir.mkdir(parents=True, exist_ok=True)
    payload = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "interval_seconds": args.interval,
        "tolerances": TOLERANCES,
        "before": before,
        "after": after,
        "report_present": bool(report),
        "results": results,
        "pass": all(item["pass"] for item in results),
    }
    with open(out_dir / "report.json", "w", encoding="utf-8") as handle:
        json.dump(payload, handle, indent=2, sort_keys=True)
    print(json.dumps(payload, indent=2, sort_keys=True))
    return 0 if payload["pass"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
