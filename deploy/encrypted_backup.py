#!/usr/bin/env python3
"""Recipient-only off-host backups and isolated, opt-in restore drills (Linux)."""

import argparse
import contextlib
import datetime as dt
import fcntl
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import secrets
import shutil
import signal
import sqlite3
import stat
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
from urllib.parse import parse_qs, unquote, urlsplit


FORMAT = "nekomari-offhost-v1"
AGE_HEADER = b"age-encryption.org/v1\n"
MAX_BYTES = 16 << 30
MAX_FILES = 10_000
MAX_MANIFEST = 2 << 20
CHUNK = 1 << 20
ASSETS = ("favicon.ico", "font.ttf", "theme", "plugin", "plugin-data")
SAFE_PREFIX = re.compile(r"[A-Za-z0-9][A-Za-z0-9_-]{0,47}\Z")
DIGEST = re.compile(r"[a-f0-9]{64}\Z")
RECIPIENT = re.compile(r"age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}\Z")
IMAGE = re.compile(r"(?:[A-Za-z0-9][A-Za-z0-9._:/-]*@)?sha256:[a-f0-9]{64}\Z")


class BackupError(Exception):
    pass


def check_deadline(deadline):
    if time.monotonic() >= deadline:
        raise BackupError("operation timed out")


def regular_file(path, private=False):
    if any(parent.is_symlink() for parent in path.parents):
        raise BackupError("file paths must not traverse symlinks")
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise BackupError("expected a regular, non-symlink, non-hardlinked file")
    if private and (info.st_uid != os.getuid() or info.st_mode & 0o077):
        raise BackupError("secret files must be owned by this user and mode 0600 (or stricter)")
    return info


def private_dir(path):
    # Do not resolve away a symlink before validating the operator's path.
    path = Path(os.path.abspath(path))
    if any(part.is_symlink() for part in (path, *path.parents)):
        raise BackupError("private directories must not traverse symlinks")
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise BackupError("create a dedicated directory owned by this user with mode 0700 first")
    return path


def native_key_file(path, identity=False):
    path = Path(path)
    info = regular_file(path, private=identity)
    if info.st_size > 64 << 10:
        raise BackupError("key file is too large")
    lines = [line.strip() for line in path.read_text(encoding="ascii").splitlines()
             if line.strip() and not line.lstrip().startswith("#")]
    if identity:
        valid = all(re.fullmatch(r"AGE-SECRET-KEY-1[0-9A-Z]+", line) for line in lines)
    else:
        valid = all(RECIPIENT.fullmatch(line) for line in lines)
    if not lines or len(lines) > 16 or not valid:
        raise BackupError("use 1-16 native age X25519 keys; plugins, SSH keys and passwords are unsupported")
    return str(path.absolute())


@contextlib.contextmanager
def child_process(command, timeout, **kwargs):
    try:
        process = subprocess.Popen(command, start_new_session=True, stderr=subprocess.DEVNULL, **kwargs)
    except OSError as error:
        raise BackupError("required executable could not be started") from error
    timed_out = threading.Event()

    def kill():
        timed_out.set()
        stop()

    def stop():
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    timer = threading.Timer(timeout, kill)
    timer.daemon = True
    timer.start()
    try:
        yield process
        if process.wait() != 0:
            raise BackupError("child command timed out" if timed_out.is_set() else "child command failed")
        if timed_out.is_set():
            raise BackupError("child command timed out")
    finally:
        timer.cancel()
        stop()
        process.wait()
        for stream in (process.stdin, process.stdout):
            if stream is not None:
                stream.close()


def file_digest(file, limit=MAX_BYTES, deadline=None):
    digest = hashlib.sha256()
    size = 0
    while chunk := file.read(CHUNK):
        if deadline is not None:
            check_deadline(deadline)
        size += len(chunk)
        if size > limit:
            raise BackupError("file exceeds the configured byte limit")
        digest.update(chunk)
    return digest.hexdigest(), size


def sqlite_check(path, deadline):
    with path.open("rb") as file:
        if file.read(16) != b"SQLite format 3\x00":
            raise BackupError("backup contains an empty or non-SQLite database")
    with contextlib.closing(sqlite3.connect(path.as_uri() + "?mode=ro", uri=True)) as db:
        db.set_progress_handler(lambda: int(time.monotonic() >= deadline), 1000)
        rows = db.execute("PRAGMA integrity_check")
        if rows.fetchone() != ("ok",) or rows.fetchone() is not None:
            raise BackupError("SQLite integrity_check failed")
    check_deadline(deadline)


def sqlite_snapshot(source, destination, deadline, max_bytes):
    regular_file(source)
    page_size = 0

    def progress(status, remaining, total):
        check_deadline(deadline)
        if total * page_size > max_bytes:
            raise BackupError("database snapshot exceeds expanded-byte limit")

    with contextlib.closing(sqlite3.connect(source.as_uri() + "?mode=ro", uri=True, timeout=5)) as src:
        page_size = src.execute("PRAGMA page_size").fetchone()[0]
        if src.execute("PRAGMA page_count").fetchone()[0] * page_size > max_bytes:
            raise BackupError("database exceeds expanded-byte limit")
        with contextlib.closing(sqlite3.connect(destination)) as dst:
            src.backup(dst, pages=1024, progress=progress, sleep=0.05)
    destination.chmod(0o600)
    sqlite_check(destination, deadline)


def allowed_name(name):
    if not isinstance(name, str) or not name or "\\" in name or ":" in name:
        return False
    if any(ord(char) < 32 or ord(char) == 127 for char in name):
        return False
    path = PurePosixPath(name)
    if path.is_absolute() or str(path) != name or any(part in (".", "..") for part in path.parts):
        return False
    if name in ("komari.db", "metrics.db", "docker-compose.yml", "favicon.ico", "font.ttf"):
        return True
    if len(path.parts) > 1 and path.parts[0] in ("theme", "plugin", "plugin-data"):
        return True
    return len(path.parts) == 2 and path.parts[0] == "upgrade-archives" and bool(
        re.fullmatch(r"upgrade-[A-Za-z0-9_.-]+\.zip", path.parts[1]))


def validate_metric_source(database, metrics, panel_dir):
    with contextlib.closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)) as db:
        values = {key: json.loads(value) for key, value in db.execute(
            "SELECT key, value FROM configs WHERE key IN ('metric_db_driver', 'metric_db_dsn')")}
    driver = values.get("metric_db_driver", "sqlite")
    dsn = values.get("metric_db_dsn", "./data/metrics.db")
    if driver not in ("sqlite", "sqlite3", "") or not isinstance(dsn, str) or not dsn or ":memory:" in dsn:
        raise BackupError("this tool supports local SQLite metrics only")
    if dsn.startswith("file:"):
        uri = urlsplit(dsn)
        if uri.netloc or "memory" in parse_qs(uri.query).get("mode", []):
            raise BackupError("remote or in-memory SQLite URIs are unsupported")
        dsn = unquote(uri.path)
    elif urlsplit(dsn).scheme:
        raise BackupError("non-file metrics DSNs are unsupported")
    if (panel_dir / dsn).resolve() != metrics.resolve():
        raise BackupError("metrics file does not match metric_db_dsn; pass the actual panel directory and metrics path")


def snapshot_files(args, scratch, deadline):
    data = Path(args.data_dir).absolute()
    if any(part.is_symlink() for part in (data, *data.parents)) or not data.is_dir():
        raise BackupError("data directory must be a real directory")
    panel_dir = Path(args.panel_dir).absolute() if args.panel_dir else data.parent
    database = Path(args.database).absolute() if args.database else data / "komari.db"
    metrics = Path(args.metrics_database).absolute() if args.metrics_database else data / "metrics.db"
    regular_file(database)
    regular_file(metrics)
    files = {}
    total = 0
    for name, source in (("komari.db", database), ("metrics.db", metrics)):
        destination = scratch / name
        sqlite_snapshot(source, destination, deadline, args.max_expanded_bytes - total)
        if name == "komari.db":
            # Bind the metric file to the captured configuration, not a live
            # read that can change before the primary snapshot is taken.
            validate_metric_source(destination, metrics, panel_dir)
        files[name] = destination
        total += destination.stat().st_size
    pending = []
    for asset in ASSETS:
        source = data / asset
        if source.exists() or source.is_symlink():
            pending.append((source, asset))
    backup = data / "backup"
    if backup.is_symlink():
        raise BackupError("upgrade backup directory must not be a symlink")
    if backup.exists():
        for source in sorted(backup.glob("upgrade-*.zip")):
            pending.append((source, "upgrade-archives/" + source.name))
    if args.compose:
        pending.append((Path(args.compose).absolute(), "docker-compose.yml"))
    visited = len(files)
    while pending:
        check_deadline(deadline)
        visited += 1
        if visited > MAX_FILES:
            raise BackupError("too many backup filesystem entries")
        source, name = pending.pop()
        if any(parent.is_symlink() for parent in source.parents):
            raise BackupError("source paths must not traverse symlinks")
        info = source.lstat()
        if stat.S_ISDIR(info.st_mode):
            for child in source.iterdir():
                pending.append((child, name + "/" + child.name))
                if visited + len(pending) > MAX_FILES:
                    raise BackupError("too many backup filesystem entries")
            continue
        regular_file(source)
        if not allowed_name(name):
            raise BackupError("unsupported backup path")
        total += info.st_size
        if total > args.max_expanded_bytes or len(files) >= MAX_FILES:
            raise BackupError("backup exceeds file-count or expanded-byte limit")
        destination = scratch / "assets" / name
        destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with source.open("rb") as src, destination.open("xb") as dst:
            copied = 0
            while chunk := src.read(CHUNK):
                check_deadline(deadline)
                copied += len(chunk)
                if copied > info.st_size:
                    raise BackupError("source file changed while snapshotting")
                dst.write(chunk)
        if copied != info.st_size:
            raise BackupError("source file changed while snapshotting")
        destination.chmod(0o600)
        files[name] = destination
    if total > args.max_expanded_bytes:
        raise BackupError("databases exceed expanded-byte limit")
    return files


def make_manifest(files, max_bytes, deadline):
    entries = []
    total = 0
    for name, path in sorted(files.items()):
        with path.open("rb") as file:
            digest, size = file_digest(file, max_bytes, deadline)
        total += size
        entries.append({"name": name, "size": size, "sha256": digest})
    if total > max_bytes:
        raise BackupError("backup exceeds expanded-byte limit")
    manifest = {"format": FORMAT, "created_at": dt.datetime.now(dt.timezone.utc).isoformat(), "files": entries}
    raw = json.dumps(manifest, separators=(",", ":"), ensure_ascii=True).encode("ascii")
    if len(raw) > MAX_MANIFEST:
        raise BackupError("manifest is too large")
    return manifest, raw


def add_tar_file(archive, name, size, file):
    entry = tarfile.TarInfo(name)
    entry.size = size
    entry.mode = 0o600
    archive.addfile(entry, file)


def stream_backup(args, output):
    recipients = native_key_file(args.recipients_file)
    parent = private_dir(args.work_dir)
    deadline = time.monotonic() + args.timeout
    with tempfile.TemporaryDirectory(prefix=".nekomari-snapshot-", dir=parent) as temp:
        files = snapshot_files(args, Path(temp), deadline)
        manifest, raw = make_manifest(files, args.max_expanded_bytes, deadline)
        with child_process(["age", "--encrypt", "--recipients-file", recipients],
                           max(0.01, deadline - time.monotonic()), stdin=subprocess.PIPE, stdout=subprocess.PIPE) as process:
            errors = []

            def write_tar():
                try:
                    with tarfile.open(fileobj=process.stdin, mode="w|gz", format=tarfile.USTAR_FORMAT) as archive:
                        add_tar_file(archive, "manifest.json", len(raw), io.BytesIO(raw))
                        for entry in manifest["files"]:
                            check_deadline(deadline)
                            with files[entry["name"]].open("rb") as file:
                                add_tar_file(archive, entry["name"], entry["size"], file)
                    process.stdin.close()
                except BaseException as error:
                    errors.append(error)
                    process.kill()

            # Drain age while feeding tar so pipe backpressure bounds memory and
            # the ciphertext byte ceiling is enforced before a file can grow.
            writer = threading.Thread(target=write_tar, daemon=True)
            writer.start()
            try:
                size = 0
                while chunk := process.stdout.read(CHUNK):
                    check_deadline(deadline)
                    size += len(chunk)
                    if size > args.max_archive_bytes:
                        raise BackupError("encrypted archive exceeds byte limit")
                    output.write(chunk)
                writer.join()
                if errors:
                    raise errors[0]
                process.wait()
            finally:
                if process.poll() is None:
                    process.kill()
                writer.join()


def fsync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def publish_archive(temp, output, max_bytes, deadline=None):
    with temp.open("rb") as file:
        if file.read(len(AGE_HEADER)) != AGE_HEADER:
            raise BackupError("source did not produce a native age archive")
        file.seek(0)
        digest, size = file_digest(file, max_bytes, deadline)
    if size < 128:
        raise BackupError("encrypted archive is truncated")
    sidecar = Path(str(output) + ".sha256")
    if output.exists() or output.is_symlink() or sidecar.exists() or sidecar.is_symlink():
        raise BackupError("refusing to overwrite an archive or checksum")
    checksum = temp.with_suffix(".sha256")
    try:
        with checksum.open("x", encoding="ascii") as file:
            file.write(digest + "  " + output.name + "\n")
            file.flush()
            os.fsync(file.fileno())
        checksum.chmod(0o600)
        # Hard-link publication is exclusive, including when another writer races us.
        os.link(temp, output)
        try:
            os.link(checksum, sidecar)
        except BaseException:
            output.unlink()
            raise
        temp.unlink()
        checksum.unlink()
        fsync_dir(output.parent)
    finally:
        checksum.unlink(missing_ok=True)
    return {"archive": output.name, "sha256": digest, "bytes": size}


@contextlib.contextmanager
def directory_lock(directory):
    descriptor = os.open(directory / ".backup.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "rb") as file:
        info = os.fstat(file.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise BackupError("unsafe backup lock file")
        try:
            fcntl.flock(file, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise BackupError("another backup operation holds the directory lock") from error
        yield


@contextlib.contextmanager
def checked_ciphertext(archive, max_bytes=MAX_BYTES, deadline=None):
    regular_file(archive)
    checksum = Path(str(archive) + ".sha256")
    info = regular_file(checksum)
    if info.st_size > 512:
        raise BackupError("invalid checksum sidecar")
    content = checksum.read_text(encoding="ascii")
    expected = content[:64]
    if not DIGEST.fullmatch(expected) or content != expected + "  " + archive.name + "\n":
        raise BackupError("invalid checksum sidecar")
    descriptor = os.open(archive, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as file:
        actual, _ = file_digest(file, max_bytes, deadline)
        if actual != expected:
            raise BackupError("encrypted archive checksum mismatch")
        file.seek(0)
        yield file


def retain_archives(directory, prefix, keep, max_bytes, deadline=None, *, published=None):
    pattern = re.compile(re.escape(prefix) + r"-\d{8}T\d{12}Z-[a-f0-9]{12}\.tar\.gz\.age\Z")
    archives = sorted((path for path in directory.iterdir() if pattern.fullmatch(path.name)), reverse=True)
    # Orphans and legacy/plaintext backups are not retention candidates. A bad
    # survivor stops pruning, rather than deleting a last known-good copy.
    complete = [path for path in archives if Path(str(path) + ".sha256").exists()]
    if published is not None:
        if keep < 1 or published not in complete:
            raise BackupError("newly published archive is missing from retention candidates")
        # Wall-clock rollback and random suffix order cannot demote this receive.
        complete.remove(published)
        complete.insert(0, published)
    for path in complete[:keep]:
        with checked_ciphertext(path, max_bytes, deadline):
            pass
    for path in complete[keep:]:
        if deadline is not None:
            check_deadline(deadline)
        regular_file(path)
        regular_file(Path(str(path) + ".sha256"))
        path.unlink()
        Path(str(path) + ".sha256").unlink()
    fsync_dir(directory)


def create_backup(args):
    if args.output == "-":
        stream_backup(args, sys.stdout.buffer)
        return
    output = Path(os.path.abspath(args.output))
    directory = private_dir(output.parent)
    deadline = time.monotonic() + args.timeout
    if not output.name.endswith(".tar.gz.age") or not re.fullmatch(r"[A-Za-z0-9_.-]+", output.name):
        raise BackupError("output must have a safe filename ending in .tar.gz.age")
    with directory_lock(directory), tempfile.TemporaryDirectory(prefix=".receive-", dir=directory) as staging:
        temp = Path(staging) / "ciphertext"
        with temp.open("xb") as file:
            stream_backup(args, file)
            file.flush()
            os.fsync(file.fileno())
        result = publish_archive(temp, output, args.max_archive_bytes, deadline)
    print(json.dumps(result))


def receive_backup(args):
    directory = private_dir(args.output_dir)
    deadline = time.monotonic() + args.timeout
    if not SAFE_PREFIX.fullmatch(args.prefix):
        raise BackupError("invalid backup prefix")
    command = args.source_command
    if command and command[0] == "--":
        command = command[1:]
    if not command:
        raise BackupError("pass the producer command after --; unchecked stdin is deliberately unsupported")
    with directory_lock(directory), tempfile.TemporaryDirectory(prefix=".receive-", dir=directory) as staging:
        stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
        output = directory / f"{args.prefix}-{stamp}-{secrets.token_hex(6)}.tar.gz.age"
        temp = Path(staging) / "ciphertext"
        with temp.open("xb") as file:
            with child_process(command, args.timeout, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE) as process:
                size = 0
                while chunk := process.stdout.read(CHUNK):
                    check_deadline(deadline)
                    size += len(chunk)
                    if size > args.max_archive_bytes:
                        raise BackupError("encrypted archive exceeds byte limit")
                    file.write(chunk)
            file.flush()
            os.fsync(file.fileno())
        result = publish_archive(temp, output, args.max_archive_bytes, deadline)
        try:
            retain_archives(directory, args.prefix, args.keep, args.max_archive_bytes, deadline, published=output)
        except (BackupError, OSError):
            raise BackupError("new encrypted backup saved, but retention failed; inspect the directory") from None
    print(json.dumps(result))


def parse_manifest(raw, max_bytes):
    manifest = json.loads(raw)
    if not isinstance(manifest, dict) or manifest.get("format") != FORMAT:
        raise BackupError("unsupported backup format")
    entries = manifest.get("files")
    created = manifest.get("created_at")
    if not isinstance(created, str) or len(created) > 40:
        raise BackupError("invalid manifest creation time")
    try:
        timestamp = dt.datetime.fromisoformat(created)
    except ValueError as error:
        raise BackupError("invalid manifest creation time") from error
    if timestamp.tzinfo is None or timestamp.utcoffset() != dt.timedelta(0):
        raise BackupError("manifest creation time must include UTC")
    if not isinstance(entries, list) or not 2 <= len(entries) <= MAX_FILES:
        raise BackupError("invalid manifest file count")
    seen = set()
    total = 0
    for entry in entries:
        if not isinstance(entry, dict):
            raise BackupError("invalid manifest entry")
        name, size, digest = entry.get("name"), entry.get("size"), entry.get("sha256")
        if not allowed_name(name) or name in seen or type(size) is not int or size < 0:
            raise BackupError("invalid or duplicate manifest path/size")
        if not isinstance(digest, str) or not DIGEST.fullmatch(digest):
            raise BackupError("invalid manifest checksum")
        seen.add(name)
        total += size
        if total > max_bytes:
            raise BackupError("manifest exceeds expanded-byte limit")
    if not {"komari.db", "metrics.db"} <= seen:
        raise BackupError("both SQLite databases are required")
    if any(parent.as_posix() in seen for name in seen for parent in PurePosixPath(name).parents):
        raise BackupError("manifest file/directory paths overlap")
    return manifest


class TarReader:
    """A bounded USTAR subset: no links, PAX allocations, devices or path rewriting."""

    def __init__(self, stream, limit, deadline):
        self.stream, self.limit, self.deadline = stream, limit, deadline
        self.bytes = 0

    def read(self, size):
        check_deadline(self.deadline)
        data = self.stream.read(size)
        self.bytes += len(data)
        if self.bytes > self.limit:
            raise BackupError("decompressed tar exceeds byte limit")
        return data

    def exact(self, size):
        data = self.read(size)
        if len(data) != size:
            raise BackupError("truncated tar archive")
        return data

    def header(self):
        header = self.exact(512)
        if header == bytes(512):
            if self.exact(512) != bytes(512):
                raise BackupError("invalid tar terminator")
            # The writer pads to a 10 KiB record. Do not accept concatenated tars.
            trailing = 0
            while chunk := self.read(512):
                trailing += len(chunk)
                if any(chunk) or trailing > tarfile.RECORDSIZE:
                    raise BackupError("unexpected trailing tar content")
            return None
        try:
            checksum = int(header[148:156].strip(b" \0"), 8)
            size = int(header[124:136].strip(b" \0"), 8)
        except ValueError as error:
            raise BackupError("invalid tar numeric field") from error
        if checksum != sum(header[:148]) + 8 * 32 + sum(header[156:]):
            raise BackupError("tar header checksum mismatch")
        if header[257:265] != b"ustar\x0000" or header[156:157] not in (b"\0", b"0") or size < 0:
            raise BackupError("only regular USTAR files are supported")
        name = header[:100].split(b"\0", 1)[0].decode("utf-8")
        prefix = header[345:500].split(b"\0", 1)[0].decode("utf-8")
        return (prefix + "/" + name if prefix else name), size

    def payload(self, size, destination):
        digest = hashlib.sha256()
        remaining = size
        while remaining:
            chunk = self.exact(min(remaining, CHUNK))
            digest.update(chunk)
            destination.write(chunk)
            remaining -= len(chunk)
        if any(self.exact((-size) % 512)):
            raise BackupError("invalid tar padding")
        return digest.hexdigest()


def unpack_archive(stream, destination, args, deadline):
    with gzip.GzipFile(fileobj=stream, mode="rb") as decompressed:
        reader = TarReader(decompressed, args.max_expanded_bytes + MAX_MANIFEST + MAX_FILES * 1024 + 20_480, deadline)
        first = reader.header()
        if first is None or first[0] != "manifest.json" or first[1] > MAX_MANIFEST:
            raise BackupError("archive must start with a bounded manifest")
        raw = io.BytesIO()
        reader.payload(first[1], raw)
        manifest = parse_manifest(raw.getvalue(), args.max_expanded_bytes)
        for entry in manifest["files"]:
            if reader.header() != (entry["name"], entry["size"]):
                raise BackupError("archive entry does not match manifest")
            target = destination / entry["name"]
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with target.open("xb") as file:
                if reader.payload(entry["size"], file) != entry["sha256"]:
                    raise BackupError("archive file checksum mismatch")
            target.chmod(0o600)
        if reader.header() is not None:
            raise BackupError("archive contains unlisted files")
    (destination / "manifest.json").write_bytes(raw.getvalue())
    (destination / "manifest.json").chmod(0o600)
    return manifest


@contextlib.contextmanager
def verified_backup(args):
    identity = native_key_file(args.identity_file, identity=True)
    parent = private_dir(args.work_dir)
    deadline = time.monotonic() + args.timeout
    with tempfile.TemporaryDirectory(prefix=".nekomari-restore-", dir=parent) as temp:
        data = Path(temp) / "data"
        data.mkdir(mode=0o700)
        with checked_ciphertext(Path(args.archive).absolute(), args.max_archive_bytes, deadline) as ciphertext:
            with child_process(["age", "--decrypt", "--identity", identity],
                               max(0.01, deadline - time.monotonic()), stdin=ciphertext, stdout=subprocess.PIPE) as process:
                manifest = unpack_archive(process.stdout, data, args, deadline)
        for name in ("komari.db", "metrics.db"):
            sqlite_check(data / name, deadline)
        yield data, manifest


def verify_backup(args):
    with verified_backup(args) as (_, manifest):
        print(json.dumps({"status": "verified", "format": FORMAT, "files": len(manifest["files"]),
                          "created_at": manifest.get("created_at")}))


def extract_backup(args):
    destination = Path(os.path.abspath(args.destination))
    private_dir(destination.parent)
    if destination.exists() or destination.is_symlink():
        raise BackupError("restore destination must not exist; live data is never replaced")
    with verified_backup(args) as (data, _):
        # Reserve the new name exclusively. Plaintext is published only after
        # authentication, every hash and both database checks have succeeded.
        destination.mkdir(mode=0o700)
        try:
            for source in data.iterdir():
                shutil.move(str(source), destination / source.name)
        except BaseException:
            shutil.rmtree(destination)
            raise
    print(json.dumps({"status": "extracted", "destination": str(destination)}))


def prepare_drill_database(database):
    # Repoint only the verified scratch copy. No restored DSN may reach a live
    # metrics service, and startup must not send production notifications.
    settings = {"metric_db_driver": "sqlite", "metric_db_dsn": "./data/metrics.db",
                "metric_migration_target": "sqlite|./data/metrics.db", "notification_enabled": False,
                "notification_method": "none", "geo_ip_enabled": False, "o_auth_enabled": False}
    with contextlib.closing(sqlite3.connect(database)) as db:
        with db:
            for key, value in settings.items():
                db.execute("INSERT INTO configs (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
                           (key, json.dumps(value)))


def docker_json(container, path):
    result = subprocess.run(["docker", "exec", container, "curl", "--fail", "--silent", "--max-time", "2",
                             "--noproxy", "*", "--max-filesize", str(MAX_MANIFEST),
                             "http://127.0.0.1:25774" + path], capture_output=True, timeout=5, check=False)
    if result.returncode != 0 or len(result.stdout) > MAX_MANIFEST:
        raise BackupError("scratch panel HTTP check failed")
    body = json.loads(result.stdout)
    if not isinstance(body, dict) or body.get("status") != "success" or not isinstance(body.get("data"), dict):
        raise BackupError("scratch panel returned an invalid envelope")
    return body["data"]


def require_local_docker():
    context = os.environ.get("DOCKER_CONTEXT")
    endpoint = os.environ.get("DOCKER_HOST") if not context else None
    if not endpoint:
        command = ["docker", "context", "inspect", "--format", "{{json .Endpoints.docker.Host}}"]
        if context:
            command.append(context)
        result = subprocess.run(command, capture_output=True, timeout=5, check=False)
        if result.returncode or len(result.stdout) > 4096:
            raise BackupError("could not determine the Docker endpoint")
        endpoint = json.loads(result.stdout)
    if not isinstance(endpoint, str):
        raise BackupError("invalid Docker endpoint")
    parsed = urlsplit(endpoint)
    if (parsed.scheme != "unix" or parsed.netloc or not parsed.path.startswith("/")
            or parsed.query or parsed.fragment):
        raise BackupError("restore drills require a local Unix-socket Docker endpoint")


def drill_backup(args):
    if not IMAGE.fullmatch(args.panel_image):
        raise BackupError("select an already-installed, digest-pinned panel image (name@sha256:... or sha256:image-id)")
    require_local_docker()
    with verified_backup(args) as (data, manifest):
        if "," in str(data):
            raise BackupError("Docker bind paths must not contain commas")
        with contextlib.closing(sqlite3.connect(data / "komari.db")) as db:
            if db.execute("SELECT COUNT(*) FROM users").fetchone()[0] == 0:
                raise BackupError("restore drill requires an installed panel with at least one account")
        prepare_drill_database(data / "komari.db")
        container = "nekomari-restore-" + secrets.token_hex(8)
        command = ["docker", "run", "--detach", "--pull=never", "--name", container, "--network=none",
                   "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128",
                   "--memory=512m", "--cpus=1", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
                   "--user", f"{os.getuid()}:{os.getgid()}", "--workdir=/app", "--entrypoint=/app/nekomari",
                   "--mount", f"type=bind,src={data},dst=/app/data", "--env", "KOMARI_LISTEN=127.0.0.1:25774",
                   args.panel_image, "server", "--listen", "127.0.0.1:25774"]
        try:
            result = subprocess.run(command, capture_output=True, timeout=30, check=False)
            if result.returncode:
                raise BackupError("could not start the isolated panel container")
            deadline = time.monotonic() + args.boot_timeout
            while True:
                try:
                    public = docker_json(container, "/api/public")
                    version = docker_json(container, "/api/version")
                    if not public or version.get("version") != args.expected_version:
                        raise BackupError("scratch panel is uninstalled or has the wrong version")
                    break
                except (BackupError, ValueError, subprocess.TimeoutExpired):
                    check_deadline(deadline)
                    time.sleep(0.2)
        finally:
            result = subprocess.run(["docker", "rm", "--force", container], capture_output=True, timeout=30, check=False)
            if result.returncode:
                raise BackupError("scratch container cleanup failed; inspect Docker before retrying")
    print(json.dumps({"status": "drill-passed", "version": args.expected_version,
                      "created_at": manifest.get("created_at"), "network": "none"}))


def positive(value):
    number = int(value)
    if number <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def parser():
    root = argparse.ArgumentParser(description=__doc__)
    commands = root.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create", help="WAL-safe SQLite snapshots -> gzip/USTAR -> native age")
    create.add_argument("--data-dir", required=True)
    create.add_argument("--panel-dir", help="working directory used to resolve the stored metrics DSN")
    create.add_argument("--database", help="actual primary SQLite database, if outside data-dir")
    create.add_argument("--metrics-database", help="actual metrics SQLite database, if outside data-dir")
    create.add_argument("--compose", help="optional compose file (contains secrets, encrypted with everything else)")
    create.add_argument("--recipients-file", required=True)
    create.add_argument("--output", required=True, help="new *.tar.gz.age file, or - for a forced-command stream")
    create.add_argument("--work-dir", required=True, help="private source-side scratch directory")
    create.set_defaults(func=create_backup)
    receive = commands.add_parser("receive", help="run a trusted producer and exclusively publish only successful ciphertext")
    receive.add_argument("--output-dir", required=True)
    receive.add_argument("--prefix", default="nekomari")
    receive.add_argument("--keep", type=positive, default=14)
    receive.add_argument("source_command", nargs=argparse.REMAINDER, help="-- ssh <restricted-backup-host>")
    receive.set_defaults(func=receive_backup)
    for name, function in (("verify", verify_backup), ("extract", extract_backup), ("drill", drill_backup)):
        command = commands.add_parser(name)
        command.add_argument("--archive", required=True)
        command.add_argument("--identity-file", required=True)
        command.add_argument("--work-dir", required=True, help="private temporary restore workspace")
        if name == "extract":
            command.add_argument("--destination", required=True, help="new directory; never a live data directory")
        if name == "drill":
            command.add_argument("--panel-image", required=True, help="pre-pulled name@sha256:... or local sha256:image-id; Docker access required")
            command.add_argument("--expected-version", required=True)
            command.add_argument("--boot-timeout", type=positive, default=90)
        command.set_defaults(func=function)
    for command in commands.choices.values():
        command.add_argument("--max-archive-bytes", type=positive, default=MAX_BYTES)
        command.add_argument("--max-expanded-bytes", type=positive, default=MAX_BYTES)
        command.add_argument("--timeout", type=positive, default=300)
    return root


def main(argv=None):
    os.umask(0o077)

    def interrupted(*_):
        raise BackupError("operation interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    args = parser().parse_args(argv)
    try:
        args.func(args)
    except KeyboardInterrupt:
        print("FAIL: operation interrupted", file=sys.stderr)
        return 1
    except (BackupError, OSError, ValueError, RecursionError, sqlite3.Error, tarfile.TarError, EOFError,
            subprocess.SubprocessError) as error:
        # Third-party parsers can echo credentials or DSNs in their exception.
        reason = str(error) if isinstance(error, BackupError) else type(error).__name__
        print("FAIL: " + reason, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
