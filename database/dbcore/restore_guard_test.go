package dbcore

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
)

// The tests below cover the two guardrails in the startup recovery branch
// (doInitialize) and the atomic write in zipDirectoryExcluding (F3a/F10).
// Before this change the branch had no coverage at all: a failed pre-restore
// snapshot was logged and ./data was wiped anyway, and a failed extraction
// still deleted the archive it had just failed to apply.

type zipSpec struct {
	name    string
	content string
}

// writeTestZip writes a zip with the entries in the given order. Order matters:
// a zip is read entry by entry, so the first entry can succeed before a later
// one fails.
func writeTestZip(t *testing.T, path string, entries ...zipSpec) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatalf("create archive %s: %v", path, err)
	}
	zw := zip.NewWriter(out)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatalf("create archive entry %q: %v", entry.name, err)
		}
		if _, err := io.WriteString(w, entry.content); err != nil {
			t.Fatalf("write archive entry %q: %v", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive %s: %v", path, err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requireContent(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q, want %q", path, raw, want)
	}
}

func requirePresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s should still exist: %v", path, err)
	}
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists, want it absent", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}

func requireReadableEntry(t *testing.T, zipPath, entryName, want string) {
	t.Helper()
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open archive %s: %v", zipPath, err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if entry.Name != entryName {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			t.Fatalf("open entry %q in %s: %v", entryName, zipPath, err)
		}
		defer source.Close()
		raw, err := io.ReadAll(source)
		if err != nil {
			t.Fatalf("read entry %q in %s: %v", entryName, zipPath, err)
		}
		if string(raw) != want {
			t.Fatalf("entry %q in %s = %q, want %q", entryName, zipPath, raw, want)
		}
		return
	}
	t.Fatalf("archive %s has no entry %q (entries: %v)", zipPath, entryName, archiveEntryNames(t, zipPath))
}

// partialArtifacts lists leftover ".partial" files, which must never survive a
// failed or successful run.
func partialArtifacts(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, partialArchiveSuffix) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(found)
	return found
}

func requireNoPartialArtifacts(t *testing.T, root string) {
	t.Helper()
	if leftover := partialArtifacts(t, root); len(leftover) != 0 {
		t.Fatalf("leftover partial archives under %s: %v", root, leftover)
	}
}

func preRestoreArchives(t *testing.T) []string {
	t.Helper()
	archives, err := filepath.Glob(filepath.Join("data", "backup", "pre-restore-*.zip"))
	if err != nil {
		t.Fatalf("glob pre-restore archives: %v", err)
	}
	sort.Strings(archives)
	return archives
}

// makeSourceUnreadable plants one entry inside dir that makes packing fail part
// way through the walk (after earlier entries have already been written to the
// archive). It reports false when the platform cannot express such an entry:
// creating a symlink needs a privilege that is usually absent, and os.Chmod
// does not hide a directory on Windows.
func makeSourceUnreadable(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.Symlink(filepath.Join(dir, "missing-target"), filepath.Join(dir, "zzz-dangling")); err == nil {
		return true
	}
	if runtime.GOOS == "windows" {
		return false
	}
	blocked := filepath.Join(dir, "zzz-blocked")
	mustWriteFile(t, filepath.Join(blocked, "inner.txt"), "inner")
	if err := os.Chmod(blocked, 0); err != nil {
		return false
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := os.ReadDir(blocked); err == nil {
		// Running as root, or a filesystem that ignores the mode bits.
		return false
	}
	return true
}

func requireContains(t *testing.T, haystack []string, needle string) {
	t.Helper()
	for _, item := range haystack {
		if item == needle {
			return
		}
	}
	t.Fatalf("%v does not contain %q", haystack, needle)
}

// ---------------------------------------------------------------------------
// F3a: atomic archive writing
// ---------------------------------------------------------------------------

func TestZipDirectoryExcludingHonoursExclusions(t *testing.T) {
	src := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(src, "keep.txt"), "keep")
	mustWriteFile(t, filepath.Join(src, "drop.txt"), "drop")
	mustWriteFile(t, filepath.Join(src, "sub", "nested.txt"), "nested")
	mustWriteFile(t, filepath.Join(src, "dropdir", "inner.txt"), "inner")

	outDir := t.TempDir()
	dst := filepath.Join(outDir, "archive.zip")
	exclude := map[string]struct{}{
		filepath.Join(src, "drop.txt"): {},
		filepath.Join(src, "dropdir"):  {},
	}
	if err := zipDirectoryExcluding(src, dst, exclude); err != nil {
		t.Fatalf("zipDirectoryExcluding: %v", err)
	}

	names := archiveEntryNames(t, dst)
	requireContains(t, names, "keep.txt")
	requireContains(t, names, "sub/nested.txt")
	for _, name := range names {
		if strings.Contains(name, "drop") {
			t.Fatalf("excluded path leaked into the archive: %v", names)
		}
	}
	// The archive is readable end to end: the central directory is present and
	// entry contents can be streamed back.
	requireReadableEntry(t, dst, "keep.txt", "keep")
	requireReadableEntry(t, dst, "sub/nested.txt", "nested")
	requireNoPartialArtifacts(t, outDir)
}

func TestZipDirectoryExcludingLeavesNothingBehindOnFailure(t *testing.T) {
	src := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(src, "keep.txt"), "keep")

	t.Run("missing source directory", func(t *testing.T) {
		outDir := t.TempDir()
		dst := filepath.Join(outDir, "archive.zip")
		if err := zipDirectoryExcluding(filepath.Join(t.TempDir(), "nope"), dst, nil); err == nil {
			t.Fatal("want an error for a missing source directory")
		}
		requireAbsent(t, dst)
		requireAbsent(t, dst+partialArchiveSuffix)
		requireNoPartialArtifacts(t, outDir)
	})

	t.Run("missing destination directory", func(t *testing.T) {
		outDir := t.TempDir()
		dst := filepath.Join(outDir, "nodir", "archive.zip")
		if err := zipDirectoryExcluding(src, dst, nil); err == nil {
			t.Fatal("want an error when the destination directory does not exist")
		}
		requireAbsent(t, dst)
		requireNoPartialArtifacts(t, outDir)
	})

	t.Run("destination path is a directory", func(t *testing.T) {
		outDir := t.TempDir()
		dst := filepath.Join(outDir, "archive.zip")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatalf("create %s: %v", dst, err)
		}
		// The write itself succeeds; only the final rename can fail here.
		if err := zipDirectoryExcluding(src, dst, nil); err == nil {
			t.Fatal("want an error when the destination path is a directory")
		}
		info, err := os.Stat(dst)
		if err != nil || !info.IsDir() {
			t.Fatalf("destination directory was replaced: %v (info %v)", err, info)
		}
		requireNoPartialArtifacts(t, outDir)
	})

	t.Run("entry cannot be opened", func(t *testing.T) {
		brokenSrc := t.TempDir()
		mustWriteFile(t, filepath.Join(brokenSrc, "keep.txt"), "keep")
		if !makeSourceUnreadable(t, brokenSrc) {
			t.Skip("this platform cannot make a source entry unreadable")
		}
		outDir := t.TempDir()
		dst := filepath.Join(outDir, "archive.zip")
		if err := zipDirectoryExcluding(brokenSrc, dst, nil); err == nil {
			t.Fatal("want an error when a source entry cannot be opened")
		}
		// This is the F3a failure mode: a truncated archive must not appear
		// under the real name, because it is indistinguishable from a good one
		// until the day it is needed for a rollback.
		requireAbsent(t, dst)
		requireNoPartialArtifacts(t, outDir)
	})
}

// ---------------------------------------------------------------------------
// F10 guardrail 1: a failed snapshot must abort the restore
// ---------------------------------------------------------------------------

func TestRestoreAbortsWhenSnapshotCannotBeTaken(t *testing.T) {
	t.Run("snapshot destination is a file", func(t *testing.T) {
		dir := prepareUpgradeTestWorkspace(t)
		dataDir := filepath.Join(dir, "data")

		writeTestZip(t, filepath.Join(dataDir, "backup.zip"), zipSpec{"from-archive.txt", "restored"})
		mustWriteFile(t, filepath.Join(dataDir, "precious.txt"), "keep-me")
		mustWriteFile(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")
		// ./data/backup exists as a regular file, so the pre-restore snapshot
		// cannot be created at all: restoring here would leave no copy behind.
		mustWriteFile(t, filepath.Join(dataDir, "backup"), "not a directory")

		if err := Initialize(); err != nil {
			t.Fatalf("initialize: %v", err)
		}

		// Nothing was deleted and nothing was extracted.
		requireContent(t, filepath.Join(dataDir, "precious.txt"), "keep-me")
		requireContent(t, filepath.Join(dataDir, "backup"), "not a directory")
		requireContent(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")
		requirePresent(t, filepath.Join(dataDir, "backup.zip"))
		requireAbsent(t, filepath.Join(dataDir, "from-archive.txt"))
		if archives := preRestoreArchives(t); len(archives) != 0 {
			t.Fatalf("snapshot should not exist when it could not be created: %v", archives)
		}
		requireNoPartialArtifacts(t, dataDir)
	})

	t.Run("source entry makes the snapshot fail", func(t *testing.T) {
		dir := prepareUpgradeTestWorkspace(t)
		dataDir := filepath.Join(dir, "data")

		writeTestZip(t, filepath.Join(dataDir, "backup.zip"), zipSpec{"from-archive.txt", "restored"})
		mustWriteFile(t, filepath.Join(dataDir, "precious.txt"), "keep-me")
		mustWriteFile(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")
		// A dangling symlink or an unreadable directory makes filepath.Walk/
		// os.Open fail part way through packing ./data, so the snapshot fails
		// after it has started writing.
		if !makeSourceUnreadable(t, dataDir) {
			t.Skip("this platform cannot make a source entry unreadable")
		}

		if err := Initialize(); err != nil {
			t.Fatalf("initialize: %v", err)
		}

		requireContent(t, filepath.Join(dataDir, "precious.txt"), "keep-me")
		requireContent(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")
		requirePresent(t, filepath.Join(dataDir, "backup.zip"))
		requireAbsent(t, filepath.Join(dataDir, "from-archive.txt"))
		if archives := preRestoreArchives(t); len(archives) != 0 {
			t.Fatalf("a failed snapshot must not leave an archive behind: %v", archives)
		}
		requireNoPartialArtifacts(t, dataDir)
	})
}

// ---------------------------------------------------------------------------
// F10 guardrail 2: a failed extraction keeps the archive for a retry
// ---------------------------------------------------------------------------

func TestRestoreKeepsArchiveAndMarkupWhenExtractionFails(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	dataDir := filepath.Join(dir, "data")

	// A valid zip whose second entry escapes the destination directory:
	// extraction writes the first entry and then fails, which is the worst case
	// for the archive — and the case that used to delete it anyway.
	writeTestZip(t, filepath.Join(dataDir, "backup.zip"),
		zipSpec{"ok.txt", "from-archive"},
		zipSpec{"../escaped.txt", "must never be written"},
	)
	mustWriteFile(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")
	mustWriteFile(t, filepath.Join(dataDir, "precious.txt"), "pre-restore state")

	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// The archive and the marker survive, so the next start can retry.
	requirePresent(t, filepath.Join(dataDir, "backup.zip"))
	requireContent(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")
	// Extraction really did fail midway: the first entry landed.
	requireContent(t, filepath.Join(dataDir, "ok.txt"), "from-archive")
	// The traversal attempt was refused.
	requireAbsent(t, filepath.Join(dir, "escaped.txt"))

	// The pre-restore snapshot is complete: the central directory has to be
	// written (and the file closed) before the rename, or this archive would be
	// unreadable and the only fallback would be a corrupt file.
	snapshots := preRestoreArchives(t)
	if len(snapshots) != 1 {
		t.Fatalf("pre-restore archives = %v, want exactly one", snapshots)
	}
	snapshotEntries := archiveEntryNames(t, snapshots[0])
	requireContains(t, snapshotEntries, "precious.txt")
	requireReadableEntry(t, snapshots[0], "precious.txt", "pre-restore state")
	for _, name := range snapshotEntries {
		if filepath.Base(name) == "backup.zip" {
			t.Fatalf("snapshot carries the archive it is meant to be independent of: %v", snapshotEntries)
		}
	}
	requireNoPartialArtifacts(t, dataDir)
}

func TestRestoreSucceedsOnRetryAfterExtractionFailure(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	dataDir := filepath.Join(dir, "data")

	writeTestZip(t, filepath.Join(dataDir, "backup.zip"),
		zipSpec{"../escaped.txt", "must never be written"},
	)
	mustWriteFile(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")

	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	// First attempt failed and is still pending.
	requirePresent(t, filepath.Join(dataDir, "backup.zip"))
	requirePresent(t, filepath.Join(dataDir, "komari-backup-markup"))

	// The operator replaces the broken archive and restarts the panel.
	resetDatabaseState()
	writeTestZip(t, filepath.Join(dataDir, "backup.zip"), zipSpec{"restored.txt", "recovered"})
	if err := Initialize(); err != nil {
		t.Fatalf("initialize after retry: %v", err)
	}

	requireContent(t, filepath.Join(dataDir, "restored.txt"), "recovered")
	requireAbsent(t, filepath.Join(dataDir, "backup.zip"))
	requireAbsent(t, filepath.Join(dataDir, "komari-backup-markup"))
	requireNoPartialArtifacts(t, dataDir)
}

// ---------------------------------------------------------------------------
// F3a regression on the real upgrade path
// ---------------------------------------------------------------------------

// TestUpgradeBackupFailureLeavesNoPartialArchive pins the caller side of F3a: a
// failed snapshot must leave no file at the official name (a truncated zip
// there is indistinguishable from a good backup until a rollback needs it) and
// no version marker, so the next start retries the backup.
func TestUpgradeBackupFailureLeavesNoPartialArchive(t *testing.T) {
	prepareFailedUpgradeBackup := func(t *testing.T) string {
		t.Helper()
		dir := prepareUpgradeTestWorkspace(t)
		dataDir := filepath.Join(dir, "data")
		createUpgradeFixtureDatabase(t, filepath.Join(dataDir, "komari.db"),
			`CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT)`,
			`INSERT INTO configs (key, value) VALUES ('system_version', '"v-old"')`,
		)
		SetVersionID("v-new")
		return dataDir
	}

	requireNoUpgradeBackup := func(t *testing.T, dataDir string) {
		t.Helper()
		if archives := upgradeArchives(t); len(archives) != 0 {
			t.Fatalf("failed backup left an archive behind: %v", archives)
		}
		requireNoPartialArtifacts(t, dataDir)
		// The marker is still the old one, so the next start retries the backup.
		recorded, err := config.GetAs[string](SystemVersionKey)
		if err == nil && recorded == versionID {
			t.Fatalf("version marker = %q after a failed backup; the next start must retry", recorded)
		}
	}

	t.Run("backup directory cannot be created", func(t *testing.T) {
		dataDir := prepareFailedUpgradeBackup(t)
		// ./data/backup exists as a regular file, so there is nowhere to stage
		// the archive.
		mustWriteFile(t, filepath.Join(dataDir, "backup"), "not a directory")

		if err := Initialize(); err != nil {
			t.Fatalf("initialize: %v", err)
		}
		requireNoUpgradeBackup(t, dataDir)
		requireContent(t, filepath.Join(dataDir, "backup"), "not a directory")
	})

	t.Run("snapshot fails part way", func(t *testing.T) {
		dataDir := prepareFailedUpgradeBackup(t)
		if !makeSourceUnreadable(t, dataDir) {
			t.Skip("this platform cannot make a source entry unreadable")
		}

		if err := Initialize(); err != nil {
			t.Fatalf("initialize: %v", err)
		}
		requireNoUpgradeBackup(t, dataDir)
	})
}

// ---------------------------------------------------------------------------
// F3a regression on the real recovery path
// ---------------------------------------------------------------------------

// TestRestoreSnapshotArchiveIsComplete pins the ordering trap in F3a on the
// path that actually uses it: if the archive were renamed into place before
// zw.Close() wrote the central directory, the pre-restore snapshot would be
// unreadable exactly when a rollback needs it.
func TestRestoreSnapshotArchiveIsComplete(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	dataDir := filepath.Join(dir, "data")

	writeTestZip(t, filepath.Join(dataDir, "backup.zip"), zipSpec{"restored.txt", "recovered"})
	mustWriteFile(t, filepath.Join(dataDir, "komari-backup-markup"), "markup")
	mustWriteFile(t, filepath.Join(dataDir, "config.yaml"), "pre-restore config")

	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	snapshots := preRestoreArchives(t)
	if len(snapshots) != 1 {
		t.Fatalf("pre-restore archives = %v, want exactly one", snapshots)
	}
	if err := verifyZipArchive(snapshots[0]); err != nil {
		t.Fatalf("pre-restore snapshot is not a usable archive: %v", err)
	}
	requireReadableEntry(t, snapshots[0], "config.yaml", "pre-restore config")
	// A successful restore consumes the archive and the marker.
	requireAbsent(t, filepath.Join(dataDir, "backup.zip"))
	requireAbsent(t, filepath.Join(dataDir, "komari-backup-markup"))
	requireContent(t, filepath.Join(dataDir, "restored.txt"), "recovered")
	requireNoPartialArtifacts(t, dataDir)
}
