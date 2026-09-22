package backup

import (
	"archive/zip"
	"os"
	"path/filepath"
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

func TestValidateArchiveAcceptsLegacyRootLayout(t *testing.T) {
	archive := writeTestArchive(t, map[string]string{
		"komari.db":            "database",
		"theme/config.json":    "{}",
		"komari-backup-markup": "backup marker",
	})
	if err := ValidateArchive(archive); err != nil {
		t.Fatalf("ValidateArchive rejected legacy root layout: %v", err)
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
		".backup-upload-.zip",    // no random suffix
		".backup-upload-abc.zip", // non-numeric suffix
		".backup-upload-12.tar",  // wrong extension
		"notes.txt",              // an operator's own file
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
