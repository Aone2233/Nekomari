package backup

import (
	"archive/zip"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The tests below cover the content half of the restore guardrail: a valid
// markup and a plausible size are not enough, because the startup restore wipes
// ./data before it extracts the archive. An archive carrying a missing, empty,
// truncated or corrupt komari.db used to pass ValidateArchive and leave the panel
// on the install wizard with its data directory emptied.

// writeTestArchiveBytes is the binary-content companion to writeTestArchive:
// a real database cannot be written through the string helper.
func writeTestArchiveBytes(t *testing.T, entries map[string][]byte) string {
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
		if _, err := entry.Write(content); err != nil {
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

// writeTestDatabase writes a real SQLite database and returns its bytes. The
// rows are sized so the file spans several pages, which is what makes the
// corruption and truncation cases meaningful. Each call gets its own
// subdirectory, so a test can build more than one database.
func writeTestDatabase(t *testing.T, dir string, rows int) []byte {
	t.Helper()
	fixtureDir, err := os.MkdirTemp(dir, "dbfixture-")
	if err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	path := filepath.Join(fixtureDir, "komari.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE clients (uuid TEXT PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		_ = db.Close()
		t.Fatalf("create fixture table: %v", err)
	}
	for i := 0; i < rows; i++ {
		if _, err := db.Exec(`INSERT INTO clients (uuid, name) VALUES (?, ?)`,
			"uuid-"+strconv.Itoa(i), strings.Repeat("x", 900)); err != nil {
			_ = db.Close()
			t.Fatalf("insert fixture row %d: %v", i, err)
		}
	}
	// VACUUM produces the same compact single-file shape the panel exports with
	// VACUUM INTO, so the checks see the layout a real backup has.
	if _, err := db.Exec("VACUUM"); err != nil {
		_ = db.Close()
		t.Fatalf("vacuum fixture database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture database: %v", err)
	}
	if len(raw) < 3*4096 {
		t.Fatalf("fixture database is only %d bytes; the page-level cases need more", len(raw))
	}
	return raw
}

func fixturePageSize(t *testing.T, database []byte) int {
	t.Helper()
	pageSize := int(binary.BigEndian.Uint16(database[16:18]))
	if pageSize == 1 {
		return 65536
	}
	return pageSize
}

func requireRejected(t *testing.T, archive, wantSubstring string) {
	t.Helper()
	err := ValidateArchive(archive)
	if err == nil {
		t.Fatalf("ValidateArchive accepted %s", wantSubstring)
	}
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("ValidateArchive error = %q, want it to mention %q", err, wantSubstring)
	}
}

func TestValidateArchiveRejectsMissingDatabase(t *testing.T) {
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari-backup-markup": []byte("backup marker"),
		"theme/config.json":    []byte("{}"),
	})
	requireRejected(t, archive, "komari.db")
}

func TestValidateArchiveRejectsEmptyDatabase(t *testing.T) {
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari-backup-markup": []byte("backup marker"),
		"komari.db":            {},
	})
	requireRejected(t, archive, "empty")
}

func TestValidateArchiveRejectsNonSQLiteDatabase(t *testing.T) {
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari-backup-markup": []byte("backup marker"),
		// Long enough to get past the length check, so the magic is what fails.
		"komari.db": []byte(strings.Repeat("this is not a database. ", 20)),
	})
	requireRejected(t, archive, "not a SQLite database")
}

func TestValidateArchiveRejectsTruncatedDatabase(t *testing.T) {
	database := writeTestDatabase(t, t.TempDir(), 200)
	truncated := database[:len(database)/2]
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari-backup-markup": []byte("backup marker"),
		"komari.db":            truncated,
	})
	requireRejected(t, archive, "truncated")
}

// TestValidateArchiveRejectsCorruptDatabase covers the damage the header cannot
// see: a file of the right size whose interior no longer parses. The startup
// path would clear ./data and extract exactly this database.
func TestValidateArchiveRejectsCorruptDatabase(t *testing.T) {
	database := writeTestDatabase(t, t.TempDir(), 200)
	pageSize := fixturePageSize(t, database)
	corrupt := append([]byte(nil), database...)
	// Zero the first table page: the header stays valid and the file size stays
	// correct, so only a page-level check can notice.
	copy(corrupt[pageSize:2*pageSize], make([]byte, pageSize))

	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari-backup-markup": []byte("backup marker"),
		"komari.db":            corrupt,
	})
	requireRejected(t, archive, "corrupt")
}

func TestValidateArchiveRejectsDatabaseOutsideRoot(t *testing.T) {
	database := writeTestDatabase(t, t.TempDir(), 200)
	// The restore extracts entries relative to ./data, so data/komari.db lands at
	// ./data/data/komari.db and the panel never reads it.
	archive := writeTestArchiveBytes(t, map[string][]byte{
		"komari-backup-markup": []byte("backup marker"),
		"data/komari.db":       database,
	})
	requireRejected(t, archive, "komari.db")
}

// TestValidateArchiveAcceptsRealBackupArchive is the positive side: an archive
// shaped like the panel's own export, with real databases, must still be
// accepted -- and the temporary copy the content check makes must not survive it.
func TestValidateArchiveAcceptsRealBackupArchive(t *testing.T) {
	fixtureDir := t.TempDir()
	database := writeTestDatabase(t, fixtureDir, 200)
	metrics := writeTestDatabase(t, fixtureDir, 20)

	archiveDir := t.TempDir()
	archive := filepath.Join(archiveDir, "backup.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	writer := zip.NewWriter(file)
	for name, content := range map[string][]byte{
		"komari.db":            database,
		"metrics.db":           metrics,
		"komari-backup-markup": []byte("backup marker"),
		"theme/config.json":    []byte("{}"),
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}

	if err := ValidateArchive(archive); err != nil {
		t.Fatalf("ValidateArchive rejected a valid backup archive: %v", err)
	}

	// The staged copy, and any journal or WAL sibling, is gone.
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive directory: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "backup.zip" {
			t.Fatalf("content check left %s behind", entry.Name())
		}
	}
}

// TestValidateArchiveStagedCopyIsReclaimed documents that a check copy a killed
// process left behind is reclaimed by the startup sweep, exactly like an
// abandoned upload.
func TestValidateArchiveStagedCopyIsReclaimed(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("./data", 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(".", "data", stagedCheckPrefix+"99"+stagedCheckSuffix)
	if err := os.WriteFile(orphan, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A sibling that only resembles the pattern is not ours to remove.
	lookalike := filepath.Join(".", "data", stagedCheckPrefix+"abc"+stagedCheckSuffix)
	if err := os.WriteFile(lookalike, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, bytes, err := CleanupStagedUploads()
	if err != nil {
		t.Fatalf("CleanupStagedUploads: %v", err)
	}
	if files != 1 || bytes != 6 {
		t.Fatalf("want one 6-byte check file reclaimed, got files=%d bytes=%d", files, bytes)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("abandoned check file survived")
	}
	if _, err := os.Stat(lookalike); err != nil {
		t.Fatalf("look-alike file must survive: %v", err)
	}
}
