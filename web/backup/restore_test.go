package backup

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestArchive(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}
	return path
}

func TestPublishedBackupDirectorySyncFailureIsUncertain(t *testing.T) {
	t.Chdir(t.TempDir())
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari.db":            writeTestDatabase(t, t.TempDir(), 20),
		"komari-backup-markup": []byte("backup marker"),
	})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageUploadedBackup(bytes.NewReader(data), "backup.zip"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pendingRestore.Lock()
		if pendingRestore.lock != nil {
			pendingRestore.lock.Release()
			pendingRestore.lock, pendingRestore.path = nil, ""
		}
		pendingRestore.Unlock()
	})
	cause := errors.New("injected directory sync failure")
	err = publishPendingRestore(func(string) error { return cause })
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "may be eligible for restore") {
		t.Fatalf("sync failure = %v", err)
	}
	if err := ValidateArchive(filepath.Join("data", "backup.zip")); err != nil {
		t.Fatalf("visible final link is invalid: %v", err)
	}
	if staged, err := filepath.Glob(filepath.Join("data", ".backup-pending-*.zip")); err != nil || len(staged) != 1 {
		t.Fatalf("retained staging link = %v, error = %v", staged, err)
	}
	if _, err := AcquireRestoreLock(); err == nil {
		t.Fatal("uncertain handoff released restore lock")
	}
	if err := PublishPendingRestore(); err == nil {
		t.Fatal("uncertain handoff overwrote existing final link on retry")
	}
}

func TestStagingRefusesOutstandingRestoreJournal(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", ".restore-in-progress"), []byte("unfinished"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := StageUploadedBackup(bytes.NewReader(nil), "backup.zip"); err == nil || !strings.Contains(err.Error(), "restore journal") {
		t.Fatalf("staging accepted outstanding restore journal: %v", err)
	}
	lock, err := AcquireRestoreLock()
	if err != nil {
		t.Fatalf("rejected staging left the restore lock held: %v", err)
	}
	lock.Release()
}

func TestStagedBackupIsInertUntilCleanHandoff(t *testing.T) {
	t.Chdir(t.TempDir())
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari.db":            writeTestDatabase(t, t.TempDir(), 20),
		"komari-backup-markup": []byte("backup marker"),
	})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageUploadedBackup(bytes.NewReader(data), "backup.zip"); err != nil {
		t.Fatalf("stage backup: %v", err)
	}
	// Prevent a failing assertion from leaking the process-wide lock into the
	// following tests; the artifact itself stays in this temporary directory.
	t.Cleanup(func() {
		pendingRestore.Lock()
		if pendingRestore.lock != nil {
			pendingRestore.lock.Release()
			pendingRestore.lock, pendingRestore.path = nil, ""
		}
		pendingRestore.Unlock()
	})
	final := filepath.Join("data", "backup.zip")
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("staged archive is already eligible for restore: %v", err)
	}
	staged, err := filepath.Glob(filepath.Join("data", ".backup-pending-*.zip"))
	if err != nil || len(staged) != 1 {
		t.Fatalf("staged archive = %v, error = %v", staged, err)
	}
	if err := StageUploadedBackup(bytes.NewReader(data), "other.zip"); err == nil {
		t.Fatal("second upload replaced the pending archive")
	}
	if err := os.WriteFile(final, []byte("other restore"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishPendingRestore(); err == nil {
		t.Fatal("handoff replaced an existing archive")
	}
	if got, err := os.ReadFile(final); err != nil || string(got) != "other restore" {
		t.Fatalf("existing archive = %q, error = %v", got, err)
	}
	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	if err := PublishPendingRestore(); err != nil {
		t.Fatalf("retry handoff: %v", err)
	}
	if got, err := os.ReadFile(final); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("published archive differs: %v", err)
	}
	if err := ValidateArchive(final); err != nil {
		t.Fatalf("published archive is invalid: %v", err)
	}
	if _, err := os.Stat(staged[0]); !os.IsNotExist(err) {
		t.Fatalf("staging link remains: %v", err)
	}
	lock, err := AcquireRestoreLock()
	if err != nil {
		t.Fatalf("restore lock not released after handoff: %v", err)
	}
	lock.Release()
}

func TestValidateArchiveAcceptsLegacyRootLayout(t *testing.T) {
	// The legacy layout keeps the database, the theme data and the markup at the
	// archive root. komari.db has to be a real database now that ValidateArchive
	// checks its content.
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari.db":            writeTestDatabase(t, t.TempDir(), 200),
		"theme/config.json":    []byte("{}"),
		"komari-backup-markup": []byte("backup marker"),
	})
	if err := ValidateArchive(archive); err != nil {
		t.Fatalf("ValidateArchive rejected legacy root layout: %v", err)
	}
}

func TestValidateArchiveRejectsUnsafeOrConflictingEntries(t *testing.T) {
	database := writeTestDatabase(t, t.TempDir(), 20)
	for name, extra := range map[string]map[string][]byte{
		"traversal":         {"../escape": []byte("bad")},
		"windows traversal": {"..\\escape": []byte("bad")},
		"absolute":          {"/absolute": []byte("bad")},
		"stream":            {"name:stream": []byte("bad")},
		"duplicate db":      {"./komari.db": database},
		"duplicate markup":  {"./komari-backup-markup": []byte("other")},
		"parent file":       {"theme": []byte("file"), "theme/a": []byte("child")},
		"backup archive":    {"backup.zip": []byte("overwrite")},
		"snapshot dir":      {"backup/prior.zip": []byte("overwrite")},
		"lock":              {".nekomari-panel.lock": []byte("overwrite")},
		"pending":           {".backup-pending-1.zip": []byte("overwrite")},
		"restore journal":   {".restore-in-progress": []byte("overwrite")},
		"journal staging":   {".restore-state-1": []byte("overwrite")},
		"main journal":      {"komari.db-wal": []byte("stale")},
		"metric journal":    {"metrics.db-journal": []byte("stale")},
	} {
		t.Run(name, func(t *testing.T) {
			entries := map[string][]byte{"komari.db": database, "komari-backup-markup": []byte("marker")}
			for key, value := range extra {
				entries[key] = value
			}
			if err := ValidateArchive(writeTestArchiveBytes(t, entries)); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestValidateArchiveChecksMetricDatabase(t *testing.T) {
	database := writeTestDatabase(t, t.TempDir(), 20)
	corrupt := append([]byte(nil), database...)
	pageSize := fixturePageSize(t, corrupt)
	copy(corrupt[pageSize:2*pageSize], make([]byte, pageSize))
	for _, metrics := range [][]byte{[]byte("not SQLite"), corrupt} {
		archive := writeTestArchiveBytes(t, map[string][]byte{
			"komari.db": database, "metrics.db": metrics, "komari-backup-markup": []byte("marker"),
		})
		err := ValidateArchive(archive)
		if err == nil || !strings.Contains(err.Error(), "metrics.db") {
			t.Fatalf("corrupt metric store accepted: %v", err)
		}
	}
}

func TestValidateArchiveRejectsCorruptAssetBeforeStaging(t *testing.T) {
	t.Chdir(t.TempDir())
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari.db":            writeTestDatabase(t, t.TempDir(), 20),
		"komari-backup-markup": []byte("backup marker"),
		"theme/index.html":     []byte("valid but checksummed asset"),
	})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	corruptCentralDirectorySize(t, data, "theme/index.html")
	if err := StageUploadedBackup(bytes.NewReader(data), "backup.zip"); err == nil {
		t.Fatal("corrupt asset was accepted")
	}
	if _, err := os.Stat(filepath.Join("data", "backup.zip")); !os.IsNotExist(err) {
		t.Fatalf("invalid archive was published: %v", err)
	}
	if staged, err := filepath.Glob(filepath.Join("data", ".backup-pending-*.zip")); err != nil || len(staged) != 0 {
		t.Fatalf("invalid archive retained: %v, %v", staged, err)
	}
}

func TestValidateArchiveRejectsSymlinkEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "symlink.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range map[string][]byte{
		"komari.db": writeTestDatabase(t, t.TempDir(), 20), "komari-backup-markup": []byte("marker"),
	} {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	header := &zip.FileHeader{Name: "theme/link", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchive(path); err == nil {
		t.Fatal("symlink entry was accepted")
	}
}

func TestValidateArchiveRejectsCentralDirectorySizeMismatch(t *testing.T) {
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari.db": writeTestDatabase(t, t.TempDir(), 20), "komari-backup-markup": []byte("marker"),
		"asset": []byte("asset content"),
	})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	corruptCentralDirectorySize(t, data, "asset")
	if err := os.WriteFile(archive, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchive(archive); err == nil {
		t.Fatal("asset with falsified size was accepted")
	}
}

func corruptCentralDirectorySize(t *testing.T, data []byte, name string) {
	t.Helper()
	// Find the entry's central directory record and falsify its expanded size.
	central := []byte{'P', 'K', 1, 2}
	for pos := 0; ; {
		i := bytes.Index(data[pos:], central)
		if i < 0 {
			t.Fatal("asset directory record not found")
		}
		i += pos
		nameLen := int(binary.LittleEndian.Uint16(data[i+28 : i+30]))
		if string(data[i+46:i+46+nameLen]) == name {
			binary.LittleEndian.PutUint32(data[i+24:i+28], 1)
			return
		}
		pos = i + 46 + nameLen
	}
}

func TestValidateArchiveRequiresMarkup(t *testing.T) {
	archive := writeTestArchive(t, map[string]string{"komari.db": "database"})
	if err := ValidateArchive(archive); err == nil {
		t.Fatal("ValidateArchive accepted archive without markup")
	}
}

// TestCleanupStagedUploadsReclaimsOnlyItsOwnTemporaryFiles covers the startup
// reclaim of a staging file left by a process killed mid-upload: the exact name
// os.CreateTemp produces is removed, and every neighbouring name -- including
// the published backup.zip and look-alikes this package never writes -- is left
// alone.
func TestCleanupStagedUploadsReclaimsOnlyItsOwnTemporaryFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("./data", 0o755); err != nil {
		t.Fatal(err)
	}

	orphan := filepath.Join(".", "data", ".backup-upload-1234567890.zip")
	if err := os.WriteFile(orphan, make([]byte, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	survivors := []string{
		"backup.zip",             // the real staging target
		".backup-pending-42.zip", // an inert restore needs manual review
		".backup-upload-.zip",    // no random suffix
		".backup-upload-abc.zip", // non-numeric suffix
		".backup-upload-12.tar",  // wrong extension
		"notes.txt",              // an operator's own file
		// The content check stages its database copy with its own pattern, and
		// the same strict shape applies to it.
		".backup-check-12.zip", // wrong extension for the check pattern
		".backup-check-.db",    // no random suffix
	}
	for _, name := range survivors {
		if err := os.WriteFile(filepath.Join(".", "data", name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A directory that merely carries the staging name is not something this
	// package created, so it must survive too.
	stagedDir := filepath.Join(".", "data", ".backup-upload-777.zip")
	if err := os.MkdirAll(stagedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	files, bytes, err := CleanupStagedUploads()
	if err != nil {
		t.Fatalf("CleanupStagedUploads: %v", err)
	}
	if files != 1 || bytes != 8 {
		t.Fatalf("want one 8-byte file reclaimed, got files=%d bytes=%d", files, bytes)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("abandoned staging file survived")
	}
	for _, name := range append(survivors, ".backup-upload-777.zip") {
		if _, err := os.Stat(filepath.Join(".", "data", name)); err != nil {
			t.Fatalf("%s must survive: %v", name, err)
		}
	}
}

// TestCleanupStagedUploadsSkipsWhileARestoreIsStaging pins the lock contract: a
// restore that is holding the lock owns its own temporary file, so the startup
// sweep must not race it.
func TestCleanupStagedUploadsSkipsWhileARestoreIsStaging(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("./data", 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(".", "data", ".backup-upload-42.zip")
	if err := os.WriteFile(orphan, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}

	lock, err := AcquireRestoreLock()
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := CleanupStagedUploads()
	if err != nil {
		t.Fatalf("CleanupStagedUploads: %v", err)
	}
	if files != 0 {
		t.Fatalf("cleanup ran while a restore held the lock: files=%d", files)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("staging file was removed under the lock: %v", err)
	}
	lock.Release()

	files, _, err = CleanupStagedUploads()
	if err != nil {
		t.Fatalf("CleanupStagedUploads after release: %v", err)
	}
	if files != 1 {
		t.Fatalf("want the staging file reclaimed once the lock is free, got %d", files)
	}
}
