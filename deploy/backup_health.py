#!/usr/bin/env python3
"""Inspect encrypted backup freshness or receive bounded, non-secret health reports."""

import argparse
import datetime as dt
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import time


MAX_REPORT = 4096
MAX_AGE = dt.timedelta(hours=26)
CLOCK_SKEW = dt.timedelta(minutes=5)
UTC = dt.timezone.utc


class HealthError(Exception):
    pass


def timestamp(value):
    if not isinstance(value, str) or not re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,6})?\+00:00", value):
        raise HealthError("invalid UTC timestamp")
    return dt.datetime.fromisoformat(value)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise HealthError("duplicate report field")
        result[key] = value
    return result


def validate_report(raw, now):
    if len(raw) > MAX_REPORT:
        raise HealthError("report exceeds byte limit")
    report = json.loads(raw, object_pairs_hook=unique_object)
    if not isinstance(report, dict) or set(report) != {"version", "checked_at", "status", "backup_at"}:
        raise HealthError("invalid report fields")
    if type(report["version"]) is not int or report["version"] != 1:
        raise HealthError("unsupported report version")
    if report["status"] not in ("ok", "missing", "invalid"):
        raise HealthError("invalid backup status")
    checked = timestamp(report["checked_at"])
    if abs(now - checked) > CLOCK_SKEW:
        raise HealthError("report is expired or future-dated")
    if report["status"] == "ok":
        created = timestamp(report["backup_at"])
        if created > min(checked, now) + CLOCK_SKEW:
            raise HealthError("backup is future-dated")
    elif report["backup_at"] is not None:
        raise HealthError("failed checks must not claim a valid backup time")
    return report


def read_small(path):
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_size > MAX_REPORT:
        raise HealthError("unsafe report file")
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as file:
        if not os.path.samestat(before, os.fstat(file.fileno())):
            raise HealthError("report file changed")
        return file.read(MAX_REPORT + 1)


def private_directory(path):
    path = Path(os.path.abspath(path))
    for parent in (path, *path.parents):
        if parent.is_symlink():
            raise HealthError("directory traverses a symlink")
    info = path.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise HealthError("health directory must be private and owned by this user")
    return path


def ingest(raw, output, now):
    report = validate_report(raw, now)
    output = Path(output)
    directory = private_directory(output.parent)
    descriptor = os.open(directory / ".ingest.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "rb") as lock:
        info = os.fstat(lock.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise HealthError("unsafe ingest lock")
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if output.exists() or output.is_symlink():
            previous = json.loads(read_small(output), object_pairs_hook=unique_object)
            if timestamp(previous["checked_at"]) >= timestamp(report["checked_at"]):
                return "ignored-out-of-order"
        report["received_at"] = now.isoformat()
        temp = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", encoding="ascii", prefix=".health-", dir=directory, delete=False) as file:
                temp = Path(file.name)
                json.dump(report, file, separators=(",", ":"))
                file.write("\n")
                file.flush()
                os.fsync(file.fileno())
            os.replace(temp, output)
            temp = None
            fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
        finally:
            if temp is not None:
                temp.unlink(missing_ok=True)
    return "stored"


def inspect(directory, prefix, now, timeout=90):
    path = Path(__file__).with_name("encrypted_backup.py")
    spec = importlib.util.spec_from_file_location("encrypted_backup", path)
    backup = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(backup)
    try:
        directory = backup.private_dir(directory)
    except backup.BackupError:
        raise HealthError("unsafe backup directory") from None
    if not backup.SAFE_PREFIX.fullmatch(prefix):
        raise HealthError("invalid backup prefix")
    pattern = re.compile(re.escape(prefix) + r"-(\d{8}T\d{12}Z)-[a-f0-9]{12}\.tar\.gz\.age\Z")
    report = {"version": 1, "checked_at": now.isoformat(), "status": "missing", "backup_at": None}
    # A receive/retention in progress is not an outage. Do not refresh its heartbeat
    # until a stable directory can actually be inspected.
    lock_path = directory / ".backup.lock"
    try:
        backup.regular_file(lock_path, private=True)
    except backup.BackupError:
        raise HealthError("unsafe backup lock") from None
    fd = os.open(lock_path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as lock:
        info = os.fstat(lock.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise HealthError("unsafe backup lock")
        try:
            fcntl.flock(lock, fcntl.LOCK_SH | fcntl.LOCK_NB)
        except BlockingIOError:
            return None
        entries = []
        for index, entry in enumerate(directory.iterdir()):
            if index >= 2048:
                raise HealthError("backup directory entry limit exceeded")
            match = pattern.fullmatch(entry.name)
            if match:
                entries.append((match[1], entry))
        if entries:
            stamp, latest = max(entries)
            try:
                created = dt.datetime.strptime(stamp, "%Y%m%dT%H%M%S%fZ").replace(tzinfo=UTC)
                if created > now + CLOCK_SKEW:
                    raise HealthError("backup is future-dated")
                with backup.checked_ciphertext(latest, max_bytes=1 << 30, deadline=time.monotonic() + timeout) as file:
                    if file.read(len(backup.AGE_HEADER)) != backup.AGE_HEADER or os.fstat(file.fileno()).st_size < 128:
                        raise HealthError("backup is not native age ciphertext")
                report.update(status="ok", backup_at=created.isoformat())
            except (backup.BackupError, HealthError, OSError, ValueError):
                report["status"] = "invalid"
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="mode", required=True)
    receiver = commands.add_parser("ingest")
    receiver.add_argument("--output", required=True)
    observer = commands.add_parser("inspect")
    observer.add_argument("--output-dir", required=True)
    observer.add_argument("--prefix", default="oc424")
    observer.add_argument("push_command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        now = dt.datetime.now(UTC)
        if args.mode == "ingest":
            result = ingest(sys.stdin.buffer.read(MAX_REPORT + 1), args.output, now)
            print(json.dumps({"status": result}))
            return 0
        report = inspect(args.output_dir, args.prefix, now)
        if report is None:
            print(json.dumps({"status": "deferred-receive-in-progress"}))
            return 0
        raw = json.dumps(report).encode()
        command = args.push_command
        if command and command[0] == "--":
            command = command[1:]
        if command:
            subprocess.run(command, input=raw, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                           check=True, timeout=25)
        print(json.dumps(report))
        return 0 if report["status"] == "ok" and now - timestamp(report["backup_at"]) <= MAX_AGE else 1
    except (HealthError, OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        print("backup health check/transport failed; inspect fixed local configuration", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
