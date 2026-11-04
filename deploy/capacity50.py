#!/usr/bin/env python3
"""Run the 50-node capacity/memory gate against a scratch panel.

The gate is the v1.8.0 exit criterion named in docs/LONG-TERM-PLAN.md: a
50-node synthetic fleet reports through the real v2 JSON-RPC path while a
throwaway panel instance is measured for RSS, API latency and error output.
It never touches a deployed panel: the server runs from --binary in its own
work directory on its own loopback port, exactly like deploy-verify.sh.

Measured signals, matching the SLO table in LONG-TERM-PLAN.md:
  - panel RSS peak over the run       (target < 256 MiB)
  - report/pull RPC p95 and p99       (target p95 < 300 ms, p99 < 1 s)
  - /api/public p95                   (target < 300 ms)
  - report success rate               (target >= 99.9%)
  - ERROR lines in the server log     (target: none)
  - post-warmup RSS growth            (first vs last 5 samples; < 16 MiB)

Exit status is 0 only when every gate passes, so the script can be reused in
CI once a binary is available.
The plateau gate needs enough post-warmup samples: use --duration >= 300.
"""

import argparse
import http.cookiejar
import json
import os
import random
import shutil
import statistics
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from collections import Counter
from pathlib import Path


class GateFailure(Exception):
    pass


def percentile(values: list[float], pct: float) -> float:
    if not values:
        return float("nan")
    ordered = sorted(values)
    idx = (len(ordered) - 1) * pct / 100.0
    lo = int(idx)
    hi = min(lo + 1, len(ordered) - 1)
    frac = idx - lo
    return ordered[lo] * (1 - frac) + ordered[hi] * frac


def rss_stats(samples: list[tuple[float, int]], run_start: float, warmup: float) -> dict:
    """Reduce timestamped VmRSS samples (seconds since run start, KiB) to gate numbers."""
    mib = [kb / 1024.0 for _, kb in samples]
    post_warmup = [(ts - run_start, kb / 1024.0) for ts, kb in samples if ts - run_start >= warmup]
    drift_mib_day = float("nan")
    growth_mib = float("nan")
    plateau_growth_mib = float("nan")
    if len(post_warmup) >= 10:
        xs = [p[0] / 3600.0 for p in post_warmup]  # hours
        ys = [p[1] for p in post_warmup]
        mean_x = sum(xs) / len(xs)
        mean_y = sum(ys) / len(ys)
        num = sum((x - mean_x) * (y - mean_y) for x, y in zip(xs, ys))
        den = sum((x - mean_x) ** 2 for x in xs)
        if den > 0:
            drift_mib_day = (num / den) * 24.0
        growth_mib = sum(ys[-5:]) / 5 - sum(ys[:5]) / 5
        mid = len(ys) // 2
        plateau_growth_mib = sum(ys[-5:]) / 5 - sum(ys[mid - 2:mid + 3]) / 5
    return {
        "peak_mib": max(mib) if mib else None,
        "mean_post_warmup_mib": sum(p[1] for p in post_warmup) / len(post_warmup) if post_warmup else None,
        "drift_mib_per_day": drift_mib_day,
        "growth_mib": growth_mib,
        "plateau_growth_mib": plateau_growth_mib,
    }


class Admin:
    """Session-authenticated admin calls: install, login, addClient."""

    def __init__(self, base: str):
        self.base = base
        jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def post_json(self, path: str, payload: dict) -> tuple[int, object]:
        req = urllib.request.Request(
            f"{self.base}{path}",
            data=json.dumps(payload, separators=(",", ":")).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with self.opener.open(req, timeout=30) as resp:
                body = resp.read()
                return resp.status, json.loads(body)
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read().decode(errors="replace")

    def get(self, path: str) -> tuple[int, object]:
        try:
            with self.opener.open(f"{self.base}{path}", timeout=10) as resp:
                return resp.status, json.loads(resp.read())
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read().decode(errors="replace")

    def login(self, username: str, password: str) -> None:
        status, body = self.post_json("/api/login", {"username": username, "password": password})
        if status != 200:
            raise RuntimeError(f"admin login failed: HTTP {status}: {body}")

    def add_client(self, name: str, req_id: int) -> dict[str, str]:
        payload = {"jsonrpc": "2.0", "method": "admin:addClient", "params": {"name": name}, "id": req_id}
        status, body = self.post_json("/api/rpc2", payload)
        if status != 200:
            raise RuntimeError(f"admin:addClient HTTP {status}: {body}")
        if isinstance(body, dict) and body.get("error"):
            raise RuntimeError(f"admin:addClient RPC error: {body['error']}")
        result = body.get("result", {}) if isinstance(body, dict) else {}
        if not result.get("uuid") or not result.get("token"):
            raise RuntimeError(f"admin:addClient returned no uuid/token: {body}")
        return {"uuid": str(result["uuid"]), "token": str(result["token"])}

    def list_clients(self) -> list[dict]:
        payload = {"jsonrpc": "2.0", "method": "admin:listClients", "params": {}, "id": 1}
        status, body = self.post_json("/api/rpc2", payload)
        if status != 200 or not isinstance(body, dict) or body.get("error"):
            raise RuntimeError(f"admin:listClients failed: HTTP {status}: {body}")
        result = body.get("result", {})
        if isinstance(result, list):
            clients = result
        elif isinstance(result, dict):
            clients = result.get("clients") or result.get("list") or []
        else:
            clients = []
        if not isinstance(clients, list):
            raise RuntimeError(f"admin:listClients result shape unexpected: {result}")
        return clients


class FakeNode:
    """One synthetic v2 node: reports system metrics and pulls events."""

    def __init__(self, base: str, token: str, uuid: str, name: str, metrics):
        self.base = base
        self.token = token
        self.uuid = uuid
        self.name = name
        self.metrics = metrics
        self.start = time.monotonic()

    def report_payload(self) -> dict:
        memory_total = 8 * 1024**3
        disk_total = 100 * 1024**3
        return {
            "cpu": {"name": "Fake CPU", "cores": 4, "arch": "amd64", "usage": round(random.uniform(8, 75), 2)},
            "ram": {"total": memory_total, "used": random.randint(memory_total // 4, memory_total * 3 // 4)},
            "swap": {"total": 0, "used": 0},
            "load": {
                "load1": round(random.uniform(0.05, 2.0), 2),
                "load5": round(random.uniform(0.05, 1.5), 2),
                "load15": round(random.uniform(0.05, 1.0), 2),
            },
            "disk": {"total": disk_total, "used": random.randint(disk_total // 5, disk_total * 4 // 5)},
            "network": {
                "up": random.randint(1_000, 2_000_000),
                "down": random.randint(1_000, 5_000_000),
                "totalUp": random.randint(1_000_000, 10_000_000_000),
                "totalDown": random.randint(1_000_000, 10_000_000_000),
            },
            "connections": {"tcp": random.randint(1, 100), "udp": random.randint(0, 20)},
            # time.monotonic() never decreases, so the restart detector cannot
            # misread synthetic uptime regressions as node restarts.
            "uptime": int(time.monotonic() - self.start),
            "process": random.randint(30, 150),
            "message": self.name,
        }

    def rpc(self, method: str, params: dict, rid: str | None = None) -> tuple[float, object]:
        payload: dict = {"jsonrpc": "2.0", "method": method, "params": params}
        if rid is not None:
            payload["id"] = rid
        req = urllib.request.Request(
            f"{self.base}/api/clients/v2/rpc",
            data=json.dumps(payload, separators=(",", ":")).encode(),
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {self.token}"},
            method="POST",
        )
        t0 = time.perf_counter()
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                body = resp.read()
                lat = time.perf_counter() - t0
                return lat, json.loads(body)
        except urllib.error.HTTPError as exc:
            lat = time.perf_counter() - t0
            return lat, {"error": {"code": exc.code, "message": exc.read().decode(errors="replace")[:200]}}
        except (urllib.error.URLError, TimeoutError) as exc:
            lat = time.perf_counter() - t0
            return lat, {"error": {"code": -1, "message": f"network: {exc.reason}"}}

    def run(self, report_interval: float, pull_interval: float, stop: threading.Event) -> None:
        lat, body = self.rpc(
            "agent.basicInfo",
            {
                "info": {
                    "cpu_name": "Fake CPU",
                    "cpu_cores": 4,
                    "arch": "amd64",
                    "os": "FakeAgent",
                    "kernel_version": "test",
                    "ipv4": "127.0.0.1",
                    "mem_total": 8 * 1024**3,
                    "disk_total": 100 * 1024**3,
                    "version": "fake-agent/1.0",
                }
            },
            "basic-info",
        )
        self.metrics.record("basicInfo", lat, body)

        # agent.pull over HTTP long-polls server-side for ~25 s, exactly like a
        # waiting WebSocket. Keep it on its own thread (as fake_agent.py does)
        # so report cadence is not stretched by the long-poll.
        def pull_loop() -> None:
            while not stop.is_set():
                lat, body = self.rpc("agent.pull", {"capabilities": ["ping"], "ack_event_ids": []}, f"pull-{time.time_ns()}")
                self.metrics.record("pull", lat, body)

        threading.Thread(target=pull_loop, daemon=True).start()
        next_report = time.monotonic()
        while not stop.is_set():
            now = time.monotonic()
            if now >= next_report:
                lat, body = self.rpc(
                    "agent.report",
                    {"report": self.report_payload(), "ack_event_ids": []},
                    f"report-{time.time_ns()}",
                )
                self.metrics.record("report", lat, body)
                next_report = now + report_interval
            stop.wait(min(0.05, max(0.0, next_report - time.monotonic())))


class Metrics:
    """Thread-safe latency/success bookkeeping for the run."""

    def __init__(self):
        self.lock = threading.Lock()
        self.latency: dict[str, list[float]] = {}
        self.ok: Counter = Counter()
        self.fail: Counter = Counter()
        self.fail_detail: Counter = Counter()

    def record(self, method: str, lat: float, body: object) -> None:
        ok = isinstance(body, dict) and not body.get("error")
        with self.lock:
            self.latency.setdefault(method, []).append(lat)
            if ok:
                self.ok[method] += 1
            else:
                self.fail[method] += 1
                msg = "unknown error"
                if isinstance(body, dict) and isinstance(body.get("error"), dict):
                    msg = str(body["error"].get("message", body["error"]))[:140]
                self.fail_detail[msg] += 1


def rss_kib(pid: int) -> int | None:
    try:
        with open(f"/proc/{pid}/status") as handle:
            for line in handle:
                if line.startswith("VmRSS:"):
                    return int(line.split()[1])
    except (OSError, ValueError):
        return None
    return None


def public_latency(base: str) -> tuple[float, bool]:
    t0 = time.perf_counter()
    try:
        with urllib.request.urlopen(f"{base}/api/public", timeout=10) as resp:
            resp.read()
            return time.perf_counter() - t0, resp.status == 200
    except urllib.error.HTTPError as exc:
        return time.perf_counter() - t0, False


def main() -> int:
    parser = argparse.ArgumentParser(description="50-node capacity/memory gate against a scratch panel")
    parser.add_argument("--binary", required=True, help="path to the panel server binary")
    parser.add_argument("--workdir", default=None, help="run directory (created if missing; default: /tmp/nek-capacity/run-<ts>)")
    parser.add_argument("--port", type=int, default=25799)
    parser.add_argument("--nodes", type=int, default=50)
    parser.add_argument("--duration", type=int, default=300, help="soak seconds after provisioning")
    parser.add_argument("--report-interval", type=float, default=3.0)
    parser.add_argument("--pull-interval", type=float, default=6.0)
    parser.add_argument("--max-rss-mib", type=float, default=256.0)
    parser.add_argument("--p95-ms", type=float, default=300.0)
    parser.add_argument("--p99-ms", type=float, default=1000.0)
    parser.add_argument("--success-pct", type=float, default=99.9)
    parser.add_argument("--rm", action="store_true", help="remove the run directory when finished (default: keep)")
    args = parser.parse_args()

    workdir = Path(args.workdir or f"/tmp/nek-capacity/run-{time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())}")
    data_dir = workdir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    log_path = workdir / "server.log"
    base = f"http://127.0.0.1:{args.port}"

    log_file = open(log_path, "wb")
    server = subprocess.Popen(
        [args.binary, "server", "-l", f"127.0.0.1:{args.port}"],
        cwd=workdir,
        env={**os.environ, "KOMARI_LISTEN": f"0.0.0.0:{args.port}"},
        stdout=log_file,
        stderr=subprocess.STDOUT,
    )
    metrics = Metrics()
    stop = threading.Event()
    try:
        admin = Admin(base)
        ready = False
        for _ in range(60):
            try:
                status, body = admin.get("/api/install/status")
                data = body.get("data", {}) if isinstance(body, dict) else {}
                required = body.get("required") if isinstance(body, dict) else None
                if status == 200 and (required is True or data.get("required") is True):
                    ready = True
                    break
            except (urllib.error.URLError, ConnectionError):
                pass
            time.sleep(1)
        if not ready:
            raise RuntimeError(f"install guide did not come up: {body}")

        status, body = admin.post_json(
            "/api/install/complete",
            {
                "username": "admin",
                "password": "Cap50-2026",
                "sitename": "Capacity 50",
                "description": "throwaway capacity run",
                "metric_dsn": "./data/metrics.db",
            },
        )
        if status != 200 or not (isinstance(body, str) or isinstance(body, dict)):
            raise RuntimeError(f"install complete failed: HTTP {status}: {body}")
        time.sleep(2)

        admin.login("admin", "Cap50-2026")
        provision_start = time.perf_counter()
        nodes = []
        for idx in range(args.nodes):
            cred = admin.add_client(f"cap50-{idx:02d}", idx + 1)
            nodes.append(FakeNode(base, cred["token"], cred["uuid"], f"cap50-{idx:02d}", metrics))
        provision_seconds = time.perf_counter() - provision_start

        threads = []
        for offset, node in enumerate(nodes):
            thread = threading.Thread(target=node.run, args=(args.report_interval, args.pull_interval, stop), daemon=True)
            threads.append(thread)
            thread.start()
            time.sleep(0.02)

        rss_samples: list[tuple[float, int]] = []
        public_samples: list[tuple[float, float]] = []

        def sample_rss():
            while not stop.is_set():
                kb = rss_kib(server.pid)
                if kb is not None:
                    with metrics.lock:
                        rss_samples.append((time.monotonic(), kb))
                stop.wait(5.0)

        def sample_public():
            while not stop.is_set():
                lat, ok = public_latency(base)
                if ok:
                    with metrics.lock:
                        public_samples.append((time.monotonic(), lat))
                stop.wait(5.0)

        rss_thread = threading.Thread(target=sample_rss, daemon=True)
        public_thread = threading.Thread(target=sample_public, daemon=True)
        run_start = time.monotonic()
        rss_thread.start()
        public_thread.start()
        stop.wait(args.duration)
        stop.set()
        for thread in threads:
            thread.join(timeout=30)
        rss_thread.join(timeout=10)
        public_thread.join(timeout=10)
        run_seconds = time.monotonic() - run_start

        clients = admin.list_clients()
        listed_uuids = {str(client.get("uuid", "")) for client in clients}
        missing = [node.uuid for node in nodes if node.uuid not in listed_uuids]

        with metrics.lock:
            latency = {method: list(values) for method, values in metrics.latency.items()}
            ok = dict(metrics.ok)
            fail = dict(metrics.fail)
            fail_detail = dict(metrics.fail_detail)
            rss_values = list(rss_samples)
            public_values = list(public_samples)

        log_text = log_path.read_text(errors="replace")
        error_lines = [line for line in log_text.splitlines() if "[ERROR" in line or " ERROR " in line]

        summary = {
            "workdir": str(workdir),
            "duration_s": round(run_seconds, 1),
            "nodes": args.nodes,
            "provision_s": round(provision_seconds, 2),
            "nodes_listed": len(listed_uuids),
            "nodes_missing": len(missing),
            "rpc": {},
        }
        for method in ("basicInfo", "report", "pull"):
            values = latency.get(method, [])
            total = ok.get(method, 0) + fail.get(method, 0)
            summary["rpc"][method] = {
                "n": total,
                "ok": ok.get(method, 0),
                "fail": fail.get(method, 0),
                "success_pct": round(100.0 * ok.get(method, 0) / total, 3) if total else None,
                "p50_ms": round(percentile(values, 50) * 1000, 1),
                "p95_ms": round(percentile(values, 95) * 1000, 1),
                "p99_ms": round(percentile(values, 99) * 1000, 1),
                "max_ms": round(max(values) * 1000, 1) if values else None,
            }

        public_lat = [lat for _, lat in public_values]
        rss_measure = rss_stats(rss_values, run_start, warmup=60.0)

        summary["public_api"] = {
            "n": len(public_lat),
            "p95_ms": round(percentile(public_lat, 95) * 1000, 1),
            "max_ms": round(max(public_lat) * 1000, 1) if public_lat else None,
        }
        summary["rss"] = {
            "samples": len(rss_values),
            "peak_mib": round(rss_measure["peak_mib"], 1) if rss_measure["peak_mib"] is not None else None,
            "mean_post_warmup_mib": (
                round(rss_measure["mean_post_warmup_mib"], 1)
                if rss_measure["mean_post_warmup_mib"] is not None
                else None
            ),
            "drift_mib_per_day": round(rss_measure["drift_mib_per_day"], 3),
            "growth_mib": round(rss_measure["growth_mib"], 1),
            "plateau_growth_mib": round(rss_measure["plateau_growth_mib"], 1),
        }
        summary["log"] = {"error_lines": len(error_lines), "error_classes": dict(Counter(errors[:120] for errors in error_lines).most_common(12))}
        summary["fail_detail"] = dict(Counter(fail_detail).most_common(12))
        summary["rss_series"] = [[round(ts - run_start, 1), round(kb / 1024.0, 1)] for ts, kb in rss_values]
        summary["public_series"] = [[round(ts - run_start, 1), round(lat * 1000, 1)] for ts, lat in public_values]

        gates = {
            "all_nodes_provisioned": len(missing) == 0 and len(listed_uuids) >= args.nodes,
            "report_success_rate": summary["rpc"]["report"]["success_pct"] is not None
            and summary["rpc"]["report"]["success_pct"] >= args.success_pct,
            "rss_under_limit": summary["rss"]["peak_mib"] is not None and summary["rss"]["peak_mib"] < args.max_rss_mib,
            "report_p95": summary["rpc"]["report"]["p95_ms"] < args.p95_ms,
            "report_p99": summary["rpc"]["report"]["p99_ms"] < args.p99_ms,
            "public_p95": summary["public_api"]["p95_ms"] < args.p95_ms,
            "no_error_logs": len(error_lines) == 0,
            # The second half is the plateau region; a runaway shows up there.
            "rss_plateau": abs(rss_measure["plateau_growth_mib"]) < 8.0,
        }
        summary["gates"] = gates
        summary["gate_thresholds"] = {
            "max_rss_mib": args.max_rss_mib,
            "p95_ms": args.p95_ms,
            "p99_ms": args.p99_ms,
            "success_pct": args.success_pct,
        }

        (workdir / "summary.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(summary, indent=2))
        print()
        failed = [name for name, passed in gates.items() if not passed]
        for name, passed in gates.items():
            print(f"  {'PASS' if passed else 'FAIL'}  {name}")
        print(f"\nrun directory: {workdir}")
        return 0 if not failed else 1
    except Exception as exc:
        print(f"harness error: {exc}", file=sys.stderr)
        return 2
    finally:
        stop.set()
        if server.poll() is None:
            server.terminate()
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server.kill()
        log_file.close()
        if args.rm:
            shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
