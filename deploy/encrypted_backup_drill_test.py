#!/usr/bin/env python3
"""Opt-in real panel/age/Docker restore drill; uses temporary accounts and data only."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener


SCRIPT = Path(__file__).with_name("encrypted_backup.py").resolve()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--panel-binary", required=True)
    parser.add_argument("--panel-image", required=True, help="already-installed digest or local sha256:image-id")
    args = parser.parse_args()
    binary = str(Path(args.panel_binary).resolve(strict=True))
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix="nekomari-encrypted-drill-test-") as temp:
        scratch = Path(temp)
        data, work, out = (scratch / name for name in ("data", "work", "backups"))
        for directory in (data, work, out):
            directory.mkdir(mode=0o700)
        identity, recipients = scratch / "test-only.agekey", scratch / "test-only.recipients"
        subprocess.run(["age-keygen", "-o", str(identity)], capture_output=True, check=True)
        recipients.write_bytes(subprocess.run(["age-keygen", "-y", str(identity)], capture_output=True, check=True).stdout)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        opener = build_opener(ProxyHandler({}))
        base = f"http://127.0.0.1:{port}"

        def call(path, payload=None):
            request = Request(base + path, data=json.dumps(payload).encode() if payload else None,
                              headers={"Content-Type": "application/json"})
            with opener.open(request, timeout=5) as response:
                return json.load(response)

        def wait(path):
            for _ in range(200):
                if server.poll() is not None:
                    raise RuntimeError("temporary source panel exited")
                try:
                    return call(path)
                except (URLError, TimeoutError):
                    time.sleep(0.1)
            raise RuntimeError("temporary source panel did not start")

        def run(*command, success=True):
            result = subprocess.run([sys.executable, str(SCRIPT), *map(str, command)],
                                    capture_output=True, timeout=180)
            if (result.returncode == 0) != success:
                # The tool suppresses raw parser/child stderr and credential values.
                raise RuntimeError("unexpected backup exit: " + result.stderr.decode()[-1000:])
            return json.loads(result.stdout) if success else None

        with (scratch / "source.log").open("wb") as log:
            server = subprocess.Popen([binary, "server", "--listen", f"127.0.0.1:{port}"], cwd=scratch,
                                      stdout=log, stderr=subprocess.STDOUT)
            try:
                assert wait("/api/install/status")["data"]["required"]
                installed = call("/api/install/complete", {"username": "encrypted-drill-test",
                    "password": "Test-Aa1-" + secrets.token_urlsafe(24), "sitename": "Isolated encrypted restore",
                    "metric_dsn": "./data/metrics.db"})
                assert installed["status"] == "success"
                version = wait("/api/version")["data"]["version"]
                archive = out / "test.tar.gz.age"
                run("create", "--data-dir", data, "--recipients-file", recipients, "--work-dir", work, "--output", archive)
                previous = out / "drill-99981231T235959999999Z-ffffffffffff.tar.gz.age"
                shutil.copyfile(archive, previous)
                Path(str(previous) + ".sha256").write_text(hashlib.sha256(previous.read_bytes()).hexdigest() + "  " + previous.name + "\n")
                received = run("receive", "--output-dir", out, "--prefix", "drill", "--keep", "1", "--",
                               sys.executable, SCRIPT, "create", "--data-dir", data, "--recipients-file", recipients,
                               "--work-dir", work, "--output", "-")
                archive = out / received["archive"]
                assert archive.is_file(), "receiver pruned its new archive behind a future-dated predecessor"
                assert not previous.exists() and not Path(str(previous) + ".sha256").exists()
                restore = ("--archive", archive, "--identity-file", identity, "--work-dir", work)
                assert run("verify", *restore)["status"] == "verified"
                drill = ("--panel-image", args.panel_image, "--boot-timeout", "30")
                assert run("drill", *restore, *drill, "--expected-version", version)["status"] == "drill-passed"
                assert list(work.iterdir()) == []
                assert call("/api/public")["status"] == "success"
                print("PASS: real installed panel -> encrypted stream -> receiver retention -> DB integrity checks -> offline Docker restore")
                run("drill", *restore, *drill, "--expected-version", "intentionally-wrong", "--boot-timeout", "1", success=False)
                assert list(work.iterdir()) == []
                assert call("/api/public")["status"] == "success"
                print("PASS: wrong-version drill fails; scratch data/container removed; source panel untouched")
            finally:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=5)
    return 0


if __name__ == "__main__":
    sys.exit(main())
