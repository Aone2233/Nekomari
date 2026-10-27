"""Offline tests with real SQLite, gzip/tar and native age encryption."""

import contextlib
import datetime as dt
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from unittest.mock import patch

import encrypted_backup as backup


SCRIPT = str(Path(backup.__file__).resolve())


class EncryptedBackupTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        for command in ("age", "age-keygen"):
            if not shutil.which(command):
                raise RuntimeError("install age to run backup tests; missing dependencies must not skip")

    def setUp(self):
        self.previous_umask = os.umask(0o077)
        self.temp = tempfile.TemporaryDirectory(prefix="encrypted-backup-test-")
        self.root = Path(self.temp.name)
        self.data = self.root / "data"
        self.out = self.root / "backups"
        self.work = self.root / "work"
        for directory in (self.data, self.out, self.work):
            directory.mkdir(mode=0o700)
        self.secret = "test-only-panel-secret-not-in-ciphertext"
        self.connections = []
        for name in ("komari.db", "metrics.db"):
            db = sqlite3.connect(self.data / name)
            db.execute("PRAGMA journal_mode=WAL")
            db.execute("PRAGMA wal_autocheckpoint=0")
            db.execute("CREATE TABLE evidence (value TEXT)")
            db.execute("INSERT INTO evidence VALUES (?)", (self.secret,))
            db.execute("CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT)")
            db.execute("CREATE TABLE users (uuid TEXT PRIMARY KEY)")
            db.execute("INSERT INTO users VALUES ('test-only-account')")
            for key, value in (("metric_db_driver", "sqlite"), ("metric_db_dsn", "./data/metrics.db")):
                db.execute("INSERT INTO configs VALUES (?, ?)", (key, json.dumps(value)))
            db.commit()
            self.connections.append(db)
        self.identity, self.recipients = self.make_keys("first")
        (self.data / "theme").mkdir()
        (self.data / "theme" / "fixture.js").write_text("theme fixture")
        (self.data / "plugin-data").mkdir()
        (self.data / "plugin-data" / "state.json").write_text('{"value":"persisted"}')
        (self.data / "backup").mkdir()
        (self.data / "backup" / "upgrade-fixture.zip").write_bytes(b"old archive fixture")
        (self.data / "komari.db-wal-unrelated").write_text("excluded")
        self.compose = self.root / "docker-compose.yml"
        self.compose.write_text("services: {}\n# " + self.secret)
        self.archive = self.out / "fixture.tar.gz.age"

    def tearDown(self):
        for db in self.connections:
            db.close()
        self.temp.cleanup()
        os.umask(self.previous_umask)

    def make_keys(self, name):
        identity = self.root / (name + ".agekey")
        result = subprocess.run(["age-keygen", "-o", str(identity)], capture_output=True, check=True)
        self.assertNotIn(b"AGE-SECRET", result.stderr)
        recipient = subprocess.run(["age-keygen", "-y", str(identity)], capture_output=True, check=True).stdout
        recipients = self.root / (name + ".recipients")
        recipients.write_bytes(recipient)
        return identity, recipients

    def create_args(self, output=None):
        return ["create", "--data-dir", str(self.data), "--compose", str(self.compose),
                "--recipients-file", str(self.recipients), "--work-dir", str(self.work),
                "--output", str(output or self.archive)]

    def restore_args(self, command="verify", archive=None, identity=None):
        return [command, "--archive", str(archive or self.archive), "--identity-file", str(identity or self.identity),
                "--work-dir", str(self.work)]

    def call(self, args, success=True, timeout=20):
        result = subprocess.run([sys.executable, SCRIPT, *args], capture_output=True, timeout=timeout)
        self.assertEqual(result.returncode, 0 if success else 1, result.stderr.decode())
        self.assertNotIn(self.secret.encode(), result.stdout + result.stderr)
        self.assertNotIn(b"AGE-SECRET-KEY", result.stdout + result.stderr)
        self.assertEqual(list(self.work.iterdir()), [], "plaintext scratch was not reclaimed")
        self.assertEqual(list(self.out.glob(".receive-*")), [], "failed ciphertext staging was not reclaimed")
        return result

    def assert_no_output(self):
        self.assertEqual([path for path in self.out.iterdir() if path.name != ".backup.lock"], [])

    def source_hashes(self):
        return {str(path.relative_to(self.data)): hashlib.sha256(path.read_bytes()).hexdigest()
                for path in self.data.rglob("*") if path.is_file() and not path.name.endswith("-shm")}

    def write_ciphertext(self, plaintext, path=None, corrupt=False):
        path = path or self.archive
        result = subprocess.run(["age", "--encrypt", "--recipients-file", str(self.recipients)],
                                input=plaintext, capture_output=True, check=True)
        ciphertext = result.stdout
        if corrupt:
            ciphertext = ciphertext[:-1] + bytes([ciphertext[-1] ^ 1])
        path.write_bytes(ciphertext)
        Path(str(path) + ".sha256").write_text(hashlib.sha256(ciphertext).hexdigest() + "  " + path.name + "\n")

    def malicious_tar(self, name="../escape", kind=tarfile.REGTYPE, size=None, trailing=False):
        payload = b"broken SQLite database"
        files = [{"name": name, "size": len(payload) if size is None else size, "sha256": hashlib.sha256(payload).hexdigest()},
                 {"name": "metrics.db", "size": len(payload), "sha256": hashlib.sha256(payload).hexdigest()}]
        manifest = {"format": backup.FORMAT, "created_at": "2026-10-20T00:00:00+00:00", "files": files}
        result = io.BytesIO()
        with tarfile.open(fileobj=result, mode="w:gz", format=tarfile.USTAR_FORMAT) as archive:
            raw = json.dumps(manifest).encode()
            backup.add_tar_file(archive, "manifest.json", len(raw), io.BytesIO(raw))
            for entry in files:
                info = tarfile.TarInfo(entry["name"])
                info.size = len(payload)
                info.type = kind
                info.linkname = "../../escape" if kind != tarfile.REGTYPE else ""
                archive.addfile(info, io.BytesIO(payload))
            if trailing:
                backup.add_tar_file(archive, "extra", 1, io.BytesIO(b"x"))
        return result.getvalue()

    def test_roundtrip_includes_wal_assets_archives_but_does_not_change_source(self):
        original = self.source_hashes()
        created = json.loads(self.call(self.create_args()).stdout)
        self.assertGreater(created["bytes"], 128)
        self.assertNotIn(self.secret.encode(), self.archive.read_bytes())
        self.assertEqual(self.archive.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.archive.stat().st_nlink, 1)
        self.call(self.restore_args())
        destination = self.root / "recovered"
        self.call(self.restore_args("extract") + ["--destination", str(destination)])
        manifest = json.loads((destination / "manifest.json").read_text())
        self.assertEqual({item["name"] for item in manifest["files"]}, {
            "komari.db", "metrics.db", "docker-compose.yml", "theme/fixture.js",
            "plugin-data/state.json", "upgrade-archives/upgrade-fixture.zip"})
        for name in ("komari.db", "metrics.db"):
            with contextlib.closing(sqlite3.connect(destination / name)) as db:
                self.assertEqual(db.execute("SELECT value FROM evidence").fetchone()[0], self.secret)
                self.assertEqual(db.execute("PRAGMA integrity_check").fetchone()[0], "ok")
        self.assertEqual(self.source_hashes(), original)
        self.call(self.restore_args("extract") + ["--destination", str(destination)], success=False)
        self.assertEqual((destination / "theme" / "fixture.js").read_text(), "theme fixture")

    def test_snapshot_binding_rejects_changes_before_primary_capture(self):
        for key, value, message in (
                ("metric_db_dsn", "./data/replacement.db", "metrics file does not match"),
                ("metric_db_driver", "postgresql", "supports local SQLite metrics only")):
            with self.subTest(key=key):
                with self.connections[0]:
                    self.connections[0].execute("UPDATE configs SET value=? WHERE key='metric_db_dsn'",
                                                (json.dumps("./data/metrics.db"),))
                    self.connections[0].execute("UPDATE configs SET value=? WHERE key='metric_db_driver'",
                                                (json.dumps("sqlite"),))
                output = self.out / f"capture-{key}.tar.gz.age"
                args = backup.parser().parse_args(self.create_args(output))
                real_snapshot = backup.sqlite_snapshot
                captured, metrics_reads = [], []

                def snapshot(source, destination, deadline, max_bytes):
                    if source == self.data / "komari.db":
                        # A real independent WAL writer commits at the capture boundary.
                        with contextlib.closing(sqlite3.connect(source)) as writer:
                            with writer:
                                writer.execute("UPDATE configs SET value=? WHERE key=?", (json.dumps(value), key))
                    else:
                        metrics_reads.append(source)
                    real_snapshot(source, destination, deadline, max_bytes)
                    if source == self.data / "komari.db":
                        with contextlib.closing(sqlite3.connect(destination)) as db:
                            captured.append(json.loads(db.execute("SELECT value FROM configs WHERE key=?", (key,)).fetchone()[0]))

                error = None
                try:
                    with patch.object(backup, "sqlite_snapshot", side_effect=snapshot), contextlib.redirect_stdout(io.StringIO()):
                        backup.create_backup(args)
                except backup.BackupError as failure:
                    error = failure
                self.assertEqual(captured, [value], "fixture did not capture the committed configuration")
                self.assertIsNotNone(error, "captured primary/metrics mismatch was published")
                self.assertRegex(str(error), message)
                self.assertEqual(metrics_reads, [], "mismatched metrics were read before snapshot validation")
                self.assert_no_output()
                self.assertEqual(list(self.work.iterdir()), [])

    def test_snapshot_binding_uses_capture_when_live_config_changes_afterwards(self):
        real_snapshot = backup.sqlite_snapshot
        captures = []

        def snapshot(source, destination, deadline, max_bytes):
            real_snapshot(source, destination, deadline, max_bytes)
            if source == self.data / "komari.db":
                captures.append(destination)
                with contextlib.closing(sqlite3.connect(source)) as writer:
                    with writer:
                        writer.execute("UPDATE configs SET value=? WHERE key='metric_db_dsn'",
                                       (json.dumps("./data/later-live-metrics.db"),))
                        writer.execute("UPDATE configs SET value=? WHERE key='metric_db_driver'",
                                       (json.dumps("postgresql"),))

        before = self.source_hashes()
        try:
            with patch.object(backup, "sqlite_snapshot", side_effect=snapshot), contextlib.redirect_stdout(io.StringIO()):
                backup.create_backup(backup.parser().parse_args(self.create_args()))
        except backup.BackupError as error:
            self.fail("snapshot validation followed live configuration instead of capture: " + str(error))
        self.assertEqual(len(captures), 1, "fixture did not reach primary capture")
        self.assertNotEqual(self.source_hashes(), before, "fixture did not commit the later live change")
        after_writer = self.source_hashes()
        destination = self.root / "captured-not-live"
        self.call(self.restore_args("extract") + ["--destination", str(destination)])
        with contextlib.closing(sqlite3.connect(destination / "komari.db")) as db:
            values = {key: json.loads(value) for key, value in db.execute("SELECT key, value FROM configs")}
            self.assertEqual(values["metric_db_dsn"], "./data/metrics.db")
            self.assertEqual(values["metric_db_driver"], "sqlite")
        with contextlib.closing(sqlite3.connect(destination / "metrics.db")) as db:
            self.assertEqual(db.execute("SELECT value FROM evidence").fetchone()[0], self.secret)
            self.assertEqual(db.execute("PRAGMA integrity_check").fetchone()[0], "ok")
        self.assertEqual(self.source_hashes(), after_writer, "backup rewrote a source after capture")

    def test_snapshot_binding_preserves_relative_uri_and_custom_metric_paths(self):
        custom = self.data / "custom metrics #%.db"
        with contextlib.closing(sqlite3.connect(custom)) as db:
            self.connections[1].backup(db)
        custom_db = sqlite3.connect(custom)
        self.connections.append(custom_db)
        self.assertEqual(custom_db.execute("SELECT value FROM evidence").fetchone()[0], self.secret)
        for index, dsn in enumerate((str(custom), "file:./data/custom%20metrics%20%23%25.db?mode=ro")):
            with self.subTest(dsn_kind=index):
                with self.connections[0]:
                    self.connections[0].execute("UPDATE configs SET value=? WHERE key='metric_db_dsn'", (json.dumps(dsn),))
                archive = self.out / f"custom-{index}.tar.gz.age"
                before = self.source_hashes()
                self.call(self.create_args(archive) + ["--panel-dir", str(self.root), "--metrics-database", str(custom)])
                self.call(self.restore_args(archive=archive))
                self.assertEqual(self.source_hashes(), before)

    def test_snapshot_binding_failed_cli_producer_preserves_receiver_retention(self):
        self.call(self.create_args())
        old = self.out / "binding-20200101T000000000000Z-000000000000.tar.gz.age"
        shutil.copyfile(self.archive, old)
        Path(str(old) + ".sha256").write_text(hashlib.sha256(old.read_bytes()).hexdigest() + "  " + old.name + "\n")
        producer = self.root / "switch-before-capture.py"
        producer.write_text("""import contextlib
import importlib.machinery
import importlib.util
import json
from pathlib import Path
import sqlite3
import sys

loader = importlib.machinery.SourceFileLoader('binding_backup', sys.argv[1])
spec = importlib.util.spec_from_loader(loader.name, loader)
backup = importlib.util.module_from_spec(spec)
loader.exec_module(backup)
real_snapshot = backup.sqlite_snapshot
database = Path(sys.argv[2])
def snapshot(source, destination, deadline, max_bytes):
    if source == database:
        with contextlib.closing(sqlite3.connect(source)) as writer:
            with writer:
                writer.execute("UPDATE configs SET value=? WHERE key='metric_db_dsn'",
                               (json.dumps('./data/not-the-captured-store.db'),))
    return real_snapshot(source, destination, deadline, max_bytes)
backup.sqlite_snapshot = snapshot
sys.exit(backup.main(sys.argv[3:]))
""")
        original = {path.name: path.read_bytes() for path in self.out.iterdir() if path.is_file()}
        result = subprocess.run([sys.executable, SCRIPT, "receive", "--output-dir", str(self.out),
                                 "--prefix", "binding", "--keep", "1", "--", sys.executable,
                                 str(producer), SCRIPT, str(self.data / "komari.db"), *self.create_args("-")],
                                capture_output=True, timeout=20)
        self.assertEqual(result.returncode, 1, "receiver published a mismatched captured primary/metrics pair")
        self.assertNotIn(self.secret.encode(), result.stdout + result.stderr)
        self.assertNotIn(b"AGE-SECRET-KEY", result.stdout + result.stderr)
        self.assertNotIn(b"not-the-captured-store", result.stdout + result.stderr)
        self.assertEqual(original, {path.name: path.read_bytes() for path in self.out.iterdir() if path.is_file()})
        self.assertEqual(list(self.out.glob(".receive-*")), [])
        self.assertEqual(list(self.work.iterdir()), [])
        self.call(self.restore_args(archive=old))

    def test_wrong_identity_corruption_truncation_and_digest_mismatch_do_not_publish_plaintext(self):
        self.call(self.create_args())
        wrong, _ = self.make_keys("wrong")
        self.call(self.restore_args(identity=wrong), success=False)
        original = self.archive.read_bytes()
        for data, rewrite_checksum in ((original[:-5], True), (original[:-1] + bytes([original[-1] ^ 1]), True),
                                       (original[:-1] + bytes([original[-1] ^ 1]), False)):
            with self.subTest(length=len(data), rewrite_checksum=rewrite_checksum):
                self.archive.write_bytes(data)
                if rewrite_checksum:
                    Path(str(self.archive) + ".sha256").write_text(hashlib.sha256(data).hexdigest() + "  " + self.archive.name + "\n")
                else:
                    Path(str(self.archive) + ".sha256").write_text(hashlib.sha256(original).hexdigest() + "  " + self.archive.name + "\n")
                destination = self.root / "must-not-exist"
                self.call(self.restore_args("extract") + ["--destination", str(destination)], success=False)
                self.assertFalse(destination.exists())

    def test_source_failures_and_unsafe_keys_fail_closed(self):
        for mutation in ("symlink", "hardlink", "missing", "corrupt", "wrong-dsn", "wrong-driver", "memory-dsn",
                         "small-budget", "ciphertext-budget"):
            with self.subTest(mutation=mutation):
                target = self.data / "theme" / "fixture.js"
                original = target.read_bytes()
                extra = []
                if mutation == "symlink":
                    target.unlink()
                    target.symlink_to(self.compose)
                elif mutation == "hardlink":
                    target.unlink()
                    os.link(self.compose, target)
                elif mutation == "missing":
                    extra = ["--metrics-database", str(self.data / "absent.db")]
                elif mutation == "corrupt":
                    bad = self.root / "bad.db"
                    bad.write_bytes(b"not sqlite")
                    extra = ["--database", str(bad)]
                elif mutation in ("wrong-dsn", "wrong-driver", "memory-dsn"):
                    key, value = {"wrong-dsn": ("metric_db_dsn", "./wrong.db"),
                                  "wrong-driver": ("metric_db_driver", "postgresql"),
                                  "memory-dsn": ("metric_db_dsn", "file:./data/metrics.db?mode=memory")}[mutation]
                    self.connections[0].execute("UPDATE configs SET value=? WHERE key=?", (json.dumps(value), key))
                    self.connections[0].commit()
                elif mutation == "small-budget":
                    extra = ["--max-expanded-bytes", "16"]
                else:
                    extra = ["--max-archive-bytes", "20"]
                self.call(self.create_args() + extra, success=False)
                self.assert_no_output()
                if mutation in ("symlink", "hardlink"):
                    target.unlink()
                    target.write_bytes(original)
                if mutation in ("wrong-dsn", "wrong-driver", "memory-dsn"):
                    self.connections[0].execute("UPDATE configs SET value=? WHERE key=?", (json.dumps("./data/metrics.db" if key == "metric_db_dsn" else "sqlite"), key))
                    self.connections[0].commit()
        self.recipients.write_text("age-plugin-unsafe1example\n")
        self.call(self.create_args(), success=False)
        self.assert_no_output()
        self.identity.chmod(0o644)
        self.call(self.restore_args(), success=False)

    def test_tar_path_link_schema_and_resource_attacks_are_rejected(self):
        cases = [("../escape", tarfile.REGTYPE, None), ("/absolute", tarfile.REGTYPE, None),
                 ("theme/../../escape", tarfile.REGTYPE, None), ("theme\\escape", tarfile.REGTYPE, None),
                 ("komari.db", tarfile.SYMTYPE, None), ("komari.db", tarfile.LNKTYPE, None),
                 ("komari.db", tarfile.FIFOTYPE, None), ("komari.db", tarfile.REGTYPE, 1 << 50),
                 ("komari.db", tarfile.REGTYPE, None)]
        for name, kind, size in cases:
            with self.subTest(name=name, kind=kind, size=size):
                self.write_ciphertext(self.malicious_tar(name, kind, size))
                self.call(self.restore_args(), success=False)
                self.assertFalse((self.root / "escape").exists())
        for invalid in (None, [], {"format": "wrong"}, {"format": backup.FORMAT, "files": [1, 2]}):
            with self.subTest(manifest=invalid):
                with self.assertRaises((backup.BackupError, ValueError)):
                    backup.parse_manifest(json.dumps(invalid), 1000)
        huge_header = tarfile.TarInfo("manifest.json")
        huge_header.size = 1 << 30
        self.write_ciphertext(gzip.compress(huge_header.tobuf(format=tarfile.USTAR_FORMAT)))
        self.call(self.restore_args(), success=False)
        pax = tarfile.TarInfo("pax")
        pax.type = tarfile.XHDTYPE
        pax.size = 1 << 30
        self.write_ciphertext(gzip.compress(pax.tobuf(format=tarfile.USTAR_FORMAT)))
        self.call(self.restore_args(), success=False)

    def test_authenticated_archive_cannot_hide_bad_hashes_unlisted_files_or_trailing_streams(self):
        self.call(self.create_args())
        compressed = subprocess.run(["age", "--decrypt", "--identity", str(self.identity), str(self.archive)],
                                    capture_output=True, check=True).stdout
        for mutation in ("wrong-hash", "trailing-file", "trailing-stream", "duplicate", "overlap", "bad-time", "json-list"):
            with self.subTest(mutation=mutation):
                payloads = {}
                with tarfile.open(fileobj=io.BytesIO(compressed), mode="r:gz") as archive:
                    for member in archive:
                        payloads[member.name] = archive.extractfile(member).read()
                manifest = json.loads(payloads.pop("manifest.json"))
                if mutation == "wrong-hash":
                    manifest["files"][0]["sha256"] = "0" * 64
                elif mutation == "duplicate":
                    manifest["files"].append(manifest["files"][0])
                elif mutation == "overlap":
                    manifest["files"].append({"name": "theme/fixture.js/child", "size": 0, "sha256": "0" * 64})
                elif mutation == "bad-time":
                    manifest["created_at"] = "2026-10-20T00:00:00"
                elif mutation == "json-list":
                    manifest = []
                stream = io.BytesIO()
                with tarfile.open(fileobj=stream, mode="w:gz", format=tarfile.USTAR_FORMAT) as archive:
                    raw = json.dumps(manifest).encode()
                    backup.add_tar_file(archive, "manifest.json", len(raw), io.BytesIO(raw))
                    for name, value in payloads.items():
                        backup.add_tar_file(archive, name, len(value), io.BytesIO(value))
                    if mutation == "trailing-file":
                        backup.add_tar_file(archive, "theme/unlisted.js", 1, io.BytesIO(b"x"))
                plaintext = stream.getvalue()
                if mutation == "trailing-stream":
                    plaintext += gzip.compress(b"unlisted tar content")
                self.write_ciphertext(plaintext)
                self.call(self.restore_args(), success=False)

    def test_receive_publishes_only_successful_producers_and_retains_exact_prefix(self):
        self.call(self.create_args())
        plain = self.out / "oc424-legacy.tar.zst"
        plain.write_bytes(b"not touched")
        unrelated = self.out / "other-20200101T000000Z-000000000000.tar.gz.age"
        shutil.copyfile(self.archive, unrelated)
        Path(str(unrelated) + ".sha256").write_text("unrelated")
        receive = ["receive", "--output-dir", str(self.out), "--prefix", "oc424", "--keep", "2"]
        for _ in range(3):
            result = json.loads(self.call(receive + ["--", "cat", str(self.archive)]).stdout)
            self.assertTrue((self.out / result["archive"]).exists())
        kept = list(self.out.glob("oc424-*.tar.gz.age"))
        self.assertEqual(len(kept), 2)
        for path in kept:
            self.call(self.restore_args(archive=path))
            self.assertEqual(path.stat().st_nlink, 1)
        before = {path.name: path.read_bytes() for path in self.out.iterdir() if path.is_file()}
        self.call(receive + ["--", sys.executable, "-c",
                            "import sys; sys.stdout.buffer.write(open(sys.argv[1],'rb').read()); sys.exit(9)", str(self.archive)], success=False)
        self.call(receive + ["--", sys.executable, "-c", "print('plaintext is forbidden')"], success=False)
        self.call(receive + ["--max-archive-bytes", "20", "--", "cat", str(self.archive)], success=False)
        self.call(receive + ["--timeout", "1", "--", sys.executable, "-c", "import time; time.sleep(30)"], success=False, timeout=5)
        self.assertEqual(before, {path.name: path.read_bytes() for path in self.out.iterdir() if path.is_file()})
        self.assertEqual(plain.read_bytes(), b"not touched")
        self.assertTrue(unrelated.exists())

    def test_full_create_stream_to_receiver_and_multi_recipient_recovery(self):
        second_identity, second_recipient = self.make_keys("second")
        self.recipients.write_bytes(self.recipients.read_bytes() + second_recipient.read_bytes())
        result = self.call(["receive", "--output-dir", str(self.out), "--", sys.executable, SCRIPT, *self.create_args("-")])
        archive = self.out / json.loads(result.stdout)["archive"]
        self.call(self.restore_args(archive=archive))
        self.call(self.restore_args(archive=archive, identity=second_identity))
        self.assertFalse(self.archive.exists())

    def test_no_overwrite_or_private_directory_escape(self):
        self.call(self.create_args())
        original = self.archive.read_bytes()
        self.call(self.create_args(), success=False)
        self.assertEqual(self.archive.read_bytes(), original)
        self.work.chmod(0o755)
        self.call(self.create_args(self.out / "second.tar.gz.age"), success=False)
        self.work.chmod(0o700)
        link = self.root / "work-link"
        link.symlink_to(self.work, target_is_directory=True)
        self.call(self.create_args(self.out / "second.tar.gz.age") + ["--work-dir", str(link)], success=False)
        self.assertFalse((self.out / "second.tar.gz.age").exists())

    def test_directory_lock_refuses_concurrent_writers_and_symlink_lockfiles(self):
        with backup.directory_lock(self.out):
            self.call(self.create_args(), success=False)
        lock = self.out / ".backup.lock"
        lock.unlink()
        lock.symlink_to(self.compose)
        self.call(self.create_args(), success=False)
        self.assertEqual(self.compose.read_text(), "services: {}\n# " + self.secret)

    def test_posix_termination_reclaims_receiver_staging(self):
        ready = self.root / "ready"
        command = [sys.executable, SCRIPT, "receive", "--output-dir", str(self.out), "--", sys.executable, "-c",
                   "import pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text('ready'); time.sleep(30)", str(ready)]
        process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            for _ in range(100):
                if ready.exists():
                    break
                time.sleep(0.02)
            self.assertTrue(ready.exists())
            process.terminate()
            _, errors = process.communicate(timeout=5)
            self.assertEqual(process.returncode, 1, errors.decode())
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
            process.stdout.close()
            process.stderr.close()
        self.assert_no_output()
        self.assertEqual(list(self.out.glob(".receive-*")), [])

    def test_retention_fails_before_deleting_when_a_survivor_is_corrupt(self):
        self.call(self.create_args())
        prefix = "keep"
        for date in ("20200101", "20200102", "20200103"):
            path = self.out / f"{prefix}-{date}T000000000000Z-000000000000.tar.gz.age"
            shutil.copyfile(self.archive, path)
            Path(str(path) + ".sha256").write_text(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n")
        newest = self.out / "keep-20200103T000000000000Z-000000000000.tar.gz.age"
        newest.write_bytes(b"corrupted")
        with self.assertRaises(backup.BackupError):
            backup.retain_archives(self.out, prefix, 2, backup.MAX_BYTES)
        self.assertEqual(len(list(self.out.glob("keep-*.tar.gz.age"))), 3)

    def test_retention_preserves_new_receive_when_older_names_sort_later(self):
        self.call(self.create_args())
        for keep in (1, 2, 4):
            with self.subTest(keep=keep):
                directory = self.root / f"clock-retention-{keep}"
                directory.mkdir(mode=0o700)
                older = []
                for day in ("99981229", "99981230", "99981231"):
                    path = directory / f"clock-{day}T000000000000Z-ffffffffffff.tar.gz.age"
                    shutil.copyfile(self.archive, path)
                    Path(str(path) + ".sha256").write_text(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n")
                    older.append(path)
                untouched = {"clock-legacy.tar.zst": b"legacy",
                             "clock-99991231T000000000000Z-ffffffffffff.tar.gz.age": b"orphan",
                             "other-99991231T000000000000Z-ffffffffffff.tar.gz.age": b"other stream",
                             "other-99991231T000000000000Z-ffffffffffff.tar.gz.age.sha256": b"other checksum"}
                for name, value in untouched.items():
                    (directory / name).write_bytes(value)
                result = self.call(["receive", "--output-dir", str(directory), "--prefix", "clock", "--keep", str(keep),
                                    "--", "cat", str(self.archive)])
                receipt = json.loads(result.stdout)
                published = directory / receipt["archive"]
                self.assertTrue(published.is_file(), "successful receiver pruned its own new archive after clock rollback")
                self.assertEqual(hashlib.sha256(published.read_bytes()).hexdigest(), receipt["sha256"])
                expected = {published.name, *(path.name for path in sorted(older, reverse=True)[:keep - 1])}
                complete = {path.name for path in directory.glob("clock-*.tar.gz.age")
                            if Path(str(path) + ".sha256").exists()}
                self.assertEqual(complete, expected, "retention must keep this receive plus the newest remaining names")
                for name in complete:
                    self.call(self.restore_args(archive=directory / name))
                for name, value in untouched.items():
                    self.assertEqual((directory / name).read_bytes(), value)
                self.assertEqual(list(directory.glob(".receive-*")), [])

    def test_retention_preserves_receive_at_equal_timestamp_with_lower_nonce(self):
        self.call(self.create_args())
        stamp = "20200102T030405000006Z"
        older = []
        for nonce in ("aaaaaaaaaaaa", "ffffffffffff"):
            path = self.out / f"tie-{stamp}-{nonce}.tar.gz.age"
            shutil.copyfile(self.archive, path)
            Path(str(path) + ".sha256").write_text(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n")
            older.append(path)

        class FixedDatetime(dt.datetime):
            @classmethod
            def now(cls, tz=None):
                return cls(2020, 1, 2, 3, 4, 5, 6, tzinfo=tz)

        args = backup.parser().parse_args(["receive", "--output-dir", str(self.out), "--prefix", "tie", "--keep", "2",
                                           "--", "cat", str(self.archive)])
        response = io.StringIO()
        with patch.object(backup.dt, "datetime", FixedDatetime), patch.object(backup.secrets, "token_hex", return_value="000000000000"):
            with contextlib.redirect_stdout(response):
                backup.receive_backup(args)
        published = self.out / json.loads(response.getvalue())["archive"]
        self.assertEqual(published.name, f"tie-{stamp}-000000000000.tar.gz.age", "fixture did not force a lower nonce")
        self.assertTrue(published.is_file(), "successful receiver pruned its own new archive on a timestamp/nonce tie")
        self.assertEqual({path.name for path in self.out.glob("tie-*.tar.gz.age")}, {published.name, max(older).name})
        self.call(self.restore_args(archive=published))
        self.assertEqual(list(self.out.glob(".receive-*")), [])

    def test_retention_checks_all_selected_survivors_before_pruning(self):
        self.call(self.create_args())
        for corrupt in ("published", "older-survivor"):
            with self.subTest(corrupt=corrupt):
                directory = self.root / ("checked-" + corrupt)
                directory.mkdir(mode=0o700)
                paths = []
                for day in ("20200101", "99981230", "99981231"):
                    path = directory / f"checked-{day}T000000000000Z-000000000000.tar.gz.age"
                    shutil.copyfile(self.archive, path)
                    Path(str(path) + ".sha256").write_text(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n")
                    paths.append(path)
                paths[0 if corrupt == "published" else 2].write_bytes(b"test-only-corrupt-ciphertext")
                before = {path.name: path.read_bytes() for path in directory.iterdir()}
                with self.assertRaises(backup.BackupError, msg="retention did not verify every selected survivor"):
                    backup.retain_archives(directory, "checked", 2, backup.MAX_BYTES, published=paths[0])
                self.assertEqual({path.name: path.read_bytes() for path in directory.iterdir()}, before,
                                 "retention deleted an archive before checking all survivors")

    def test_retention_refuses_missing_or_unrelated_published_archive(self):
        self.call(self.create_args())
        older = []
        for day in ("99981230", "99981231"):
            path = self.out / f"required-{day}T000000000000Z-000000000000.tar.gz.age"
            shutil.copyfile(self.archive, path)
            Path(str(path) + ".sha256").write_text(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n")
            older.append(path)
        for published in (self.out / "required-20200101T000000000000Z-000000000000.tar.gz.age", self.archive,
                          self.root / older[0].name):
            with self.subTest(location=published.relative_to(self.root)):
                before = {path.name: path.read_bytes() for path in self.out.iterdir() if path.is_file()}
                with self.assertRaises(backup.BackupError, msg="missing/unrelated published archive was not refused"):
                    backup.retain_archives(self.out, "required", 1, backup.MAX_BYTES, published=published)
                self.assertEqual({path.name: path.read_bytes() for path in self.out.iterdir() if path.is_file()}, before)

    def test_retention_failed_receive_keeps_new_pair_and_old_recovery(self):
        self.call(self.create_args())
        older = []
        for day in ("99981229", "99981230", "99981231"):
            path = self.out / f"failed-{day}T000000000000Z-000000000000.tar.gz.age"
            shutil.copyfile(self.archive, path)
            Path(str(path) + ".sha256").write_text(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n")
            older.append(path)
        older[-1].write_bytes(b"test-only-corrupt-survivor")
        before = {path.name: path.read_bytes() for path in self.out.iterdir() if path.is_file()}
        result = self.call(["receive", "--output-dir", str(self.out), "--prefix", "failed", "--keep", "2",
                            "--", sys.executable, SCRIPT, *self.create_args("-")], success=False)
        self.assertIn(b"new encrypted backup saved, but retention failed", result.stderr)
        for name, value in before.items():
            self.assertEqual((self.out / name).read_bytes(), value, "failed retention changed an older recovery file")
        published = [path for path in self.out.glob("failed-*.tar.gz.age") if path.name not in before]
        self.assertEqual(len(published), 1, "retention refusal did not preserve the newly published archive")
        self.call(self.restore_args(archive=published[0]))
        self.call(self.restore_args(archive=older[0]))

    def test_drill_is_digest_pinned_offline_and_cleans_up_even_on_failure(self):
        self.call(self.create_args())
        args = backup.parser().parse_args(self.restore_args("drill") + [
            "--panel-image", "example.invalid/panel@sha256:" + "a" * 64, "--expected-version", "fixture"])
        calls = []

        def run(command, **kwargs):
            calls.append(command)
            if command[1:3] == ["context", "inspect"]:
                return subprocess.CompletedProcess(command, 0, b'"unix:///var/run/docker.sock"\n', b"")
            if command[1] == "run":
                mount = command[command.index("--mount") + 1]
                scratch = Path(mount.split("src=", 1)[1].split(",dst=", 1)[0])
                with contextlib.closing(sqlite3.connect(scratch / "komari.db")) as db:
                    values = {key: json.loads(value) for key, value in db.execute("SELECT key, value FROM configs")}
                    self.assertEqual(values["metric_db_dsn"], "./data/metrics.db")
                    self.assertFalse(values["notification_enabled"])
                self.assertEqual(command[command.index("--network=none")], "--network=none")
                self.assertIn("--pull=never", command)
                self.assertIn("--read-only", command)
                self.assertFalse(any(item == "-p" or item.startswith("--publish") for item in command))
            return subprocess.CompletedProcess(command, 0, b"", b"")

        with patch.object(backup.subprocess, "run", side_effect=run), patch.object(backup, "docker_json", side_effect=[
                {"sitename": "test"}, {"version": "fixture"}]):
            with contextlib.redirect_stdout(io.StringIO()):
                backup.drill_backup(args)
        self.assertEqual(calls[-1][1:3], ["rm", "--force"])
        self.assertEqual(list(self.work.iterdir()), [])
        calls.clear()
        with patch.object(backup.subprocess, "run", side_effect=run), patch.object(backup, "docker_json", side_effect=RuntimeError("test")):
            with self.assertRaises(RuntimeError):
                backup.drill_backup(args)
        self.assertEqual(calls[-1][1:3], ["rm", "--force"])
        self.assertEqual(list(self.work.iterdir()), [])
        bad = self.restore_args("drill") + ["--panel-image", "example.invalid/panel:latest", "--expected-version", "fixture"]
        self.call(bad, success=False)

    def test_drill_refuses_remote_or_unknown_docker_before_decryption(self):
        args = backup.parser().parse_args(self.restore_args("drill") + [
            "--panel-image", "sha256:" + "a" * 64, "--expected-version", "fixture"])
        cases = [
            ({"DOCKER_HOST": "ssh://restore@example.invalid"}, None),
            ({"DOCKER_HOST": "tcp://127.0.0.1:2375"}, None),
            ({"DOCKER_HOST": "unix://remote/var/run/docker.sock"}, None),
            ({"DOCKER_CONTEXT": "remote", "DOCKER_HOST": "unix:///var/run/docker.sock"},
             subprocess.CompletedProcess([], 0, b'"ssh://example.invalid"\n', b"")),
            ({}, subprocess.CompletedProcess([], 0, b'"tcp://example.invalid:2376"\n', b"")),
            ({}, subprocess.CompletedProcess([], 1, b"", b"private diagnostic")),
            ({}, subprocess.CompletedProcess([], 0, b"null\n", b"")),
        ]
        for environment, result in cases:
            with self.subTest(environment=environment), patch.dict(os.environ, environment, clear=True), \
                    patch.object(backup.subprocess, "run", return_value=result) as run, \
                    patch.object(backup, "verified_backup") as decrypt:
                with self.assertRaises(backup.BackupError):
                    backup.drill_backup(args)
                decrypt.assert_not_called()
                if result is None:
                    run.assert_not_called()
                else:
                    self.assertEqual(run.call_args.args[0][1:3], ["context", "inspect"])
                    if "DOCKER_CONTEXT" in environment:
                        self.assertEqual(run.call_args.args[0][-1], "remote")
        for environment, result in [
                ({"DOCKER_HOST": "unix:///var/run/docker.sock"}, None),
                ({"DOCKER_CONTEXT": "local", "DOCKER_HOST": "ssh://ignored.invalid"},
                 subprocess.CompletedProcess([], 0, b'"unix:///run/user/1000/docker.sock"\n', b""))]:
            with self.subTest(environment=environment), patch.dict(os.environ, environment, clear=True), \
                    patch.object(backup.subprocess, "run", return_value=result):
                backup.require_local_docker()


if __name__ == "__main__":
    unittest.main(verbosity=2)
