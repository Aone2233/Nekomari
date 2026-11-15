// Package restorearchive validates archives before they can replace panel data.
package restorearchive

import (
	"archive/zip"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Aone2233/nekomari/internal/panellock"
	_ "github.com/mattn/go-sqlite3"
)

const (
	MaxArchiveSize    int64 = 4 << 30
	maxArchiveEntries       = 100_000
	markupEntryName         = "komari-backup-markup"
	databaseEntryName       = "komari.db"
	CheckPrefix             = ".backup-check-"
	CheckSuffix             = ".db"
	PendingPrefix           = ".backup-pending-"
	StateFileName           = ".restore-in-progress"
	StateTempPrefix         = ".restore-state-"
	sqliteMagic             = "SQLite format 3\x00"
	sqliteHeaderSize        = 100
)

// ValidateArchive checks paths, complete entry contents and both SQLite
// snapshots before the startup restore path extracts them into data/.
//
// The content check is what stands between an unusable upload and a destructive
// restore: the startup path clears ./data and only then unzips this archive, so
// an archive that passes here but holds a broken database would leave the panel
// with an empty data directory and the install wizard.
func ValidateArchive(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open backup archive: %w", err)
	}
	defer reader.Close()

	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("backup archive has too many files: %d", len(reader.File))
	}

	var expandedSize uint64
	hasMarkup := false
	var database, metrics *zip.File
	entries := make(map[string]bool, len(reader.File))
	for _, entry := range reader.File {
		name, isDir, err := restoreEntryName(entry)
		if err != nil {
			return err
		}
		if _, exists := entries[name]; exists {
			return fmt.Errorf("invalid backup file: duplicate entry %q", name)
		}
		entries[name] = isDir
		if name == markupEntryName && !isDir {
			hasMarkup = true
		}
		if name == databaseEntryName && !isDir {
			database = entry
		}
		if name == "metrics.db" && !isDir {
			metrics = entry
		}
		if entry.UncompressedSize64 > uint64(MaxArchiveSize) || expandedSize > uint64(MaxArchiveSize)-entry.UncompressedSize64 {
			return fmt.Errorf("backup archive expands beyond the %d byte limit", MaxArchiveSize)
		}
		expandedSize += entry.UncompressedSize64
	}
	for name := range entries {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if isDir, exists := entries[parent]; exists && !isDir {
				return fmt.Errorf("invalid backup file: %q is inside file %q", name, parent)
			}
		}
	}
	if !hasMarkup {
		return fmt.Errorf("invalid backup file: missing %s file", markupEntryName)
	}
	if database == nil {
		return fmt.Errorf("invalid backup file: missing %s at the archive root", databaseEntryName)
	}

	if err := validateDatabaseHeader(database, databaseEntryName); err != nil {
		return err
	}
	if metrics != nil {
		if err := validateDatabaseHeader(metrics, "metrics.db"); err != nil {
			return err
		}
	}
	// Verify every entry and CRC before startup removes any existing data.
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open backup entry %q: %w", entry.Name, err)
		}
		written, copyErr := io.Copy(io.Discard, io.LimitReader(source, MaxArchiveSize+1))
		closeErr := source.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return fmt.Errorf("invalid backup entry %q: %w", entry.Name, err)
		}
		if written != int64(entry.UncompressedSize64) {
			return fmt.Errorf("invalid backup entry %q: size mismatch", entry.Name)
		}
	}
	if err := quickCheckDatabase(archivePath, database, databaseEntryName); err != nil {
		return err
	}
	if metrics != nil {
		return quickCheckDatabase(archivePath, metrics, "metrics.db")
	}
	return nil
}

func restoreEntryName(entry *zip.File) (string, bool, error) {
	name := strings.TrimPrefix(entry.Name, "./")
	isDir := entry.FileInfo().IsDir()
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	// ZIP names use forward slashes. Reject alternate Windows paths, streams,
	// duplicate spellings, links and special files on every platform.
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:\x00") ||
		(!isDir && !entry.Mode().IsRegular()) {
		return "", false, fmt.Errorf("invalid backup entry %q", entry.Name)
	}
	root := strings.SplitN(name, "/", 2)[0]
	if root == "backup" || root == "backup.zip" || root == panellock.FileName || root == StateFileName ||
		strings.HasPrefix(root, PendingPrefix) || strings.HasPrefix(root, StateTempPrefix) {
		return "", false, fmt.Errorf("invalid backup entry %q: reserved restore path", entry.Name)
	}
	for _, database := range []string{databaseEntryName, "metrics.db"} {
		if root == database+"-wal" || root == database+"-shm" || root == database+"-journal" {
			return "", false, fmt.Errorf("invalid backup entry %q: database journals cannot accompany a snapshot", entry.Name)
		}
	}
	return name, isDir, nil
}

// validateDatabaseHeader streams the fixed 100-byte SQLite header out of the
// archive entry and rejects a database that cannot be one. Only the header is
// read here, never the whole entry.
func validateDatabaseHeader(entry *zip.File, name string) error {
	size := entry.UncompressedSize64
	if size == 0 {
		return fmt.Errorf("invalid backup file: %s is empty", name)
	}
	if size < sqliteHeaderSize {
		return fmt.Errorf("invalid backup file: %s is %d bytes, too short to be a SQLite database", name, size)
	}

	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("read %s from the archive: %w", name, err)
	}
	defer source.Close()
	header := make([]byte, sqliteHeaderSize)
	if _, err := io.ReadFull(source, header); err != nil {
		return fmt.Errorf("read %s header: %w", name, err)
	}

	if problem := sqliteHeaderProblem(header, size); problem != "" {
		return fmt.Errorf("invalid backup file: %s %s", name, problem)
	}
	return nil
}

// sqliteHeaderProblem returns why the header of a SQLite database is unusable,
// or "" when it is structurally plausible. The layout is documented at
// https://sqlite.org/fileformat2.html#the_database_header.
func sqliteHeaderProblem(header []byte, fileSize uint64) string {
	if len(header) < sqliteHeaderSize {
		return fmt.Sprintf("has a %d-byte header; a SQLite database needs %d bytes", len(header), sqliteHeaderSize)
	}
	if string(header[:len(sqliteMagic)]) != sqliteMagic {
		return "is not a SQLite database (bad magic)"
	}
	pageSize := uint64(binary.BigEndian.Uint16(header[16:18]))
	if pageSize == 1 {
		pageSize = 65536 // the value 1 encodes a 64 KiB page
	}
	switch pageSize {
	case 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536:
	default:
		return fmt.Sprintf("is not a SQLite database (invalid page size %d)", pageSize)
	}
	// Every SQLite database carries these three fixed payload fractions; a
	// mismatch means the file only looks like one at the magic.
	if header[21] != 64 || header[22] != 32 || header[23] != 32 {
		return "is not a SQLite database (invalid payload fractions)"
	}
	if format := binary.BigEndian.Uint32(header[44:48]); format < 1 || format > 4 {
		return fmt.Sprintf("is not a SQLite database (invalid schema format %d)", format)
	}
	// The header records the database size in pages. A file smaller than that is
	// truncated -- the exact shape of an upload that was cut short. Zero means
	// "derive from the file size" for old databases, so it proves nothing.
	if pages := uint64(binary.BigEndian.Uint32(header[28:32])); pages != 0 {
		if declared := pages * pageSize; declared > fileSize {
			return fmt.Sprintf("is truncated: the header declares %d pages (%d bytes) but the archive holds %d bytes", pages, declared, fileSize)
		}
	}
	return ""
}

// quickCheckDatabase extracts the database entry to a temporary file beside the
// archive and runs SQLite's PRAGMA quick_check on it. The header check above is
// cheap but blind to damage inside the file (a b-tree page that no longer parses,
// a page referenced twice); quick_check reads every page and reports the first
// problem. The copy is bounded by the expanded-size limit already enforced above.
func quickCheckDatabase(archivePath string, entry *zip.File, name string) error {
	temp, err := os.CreateTemp(filepath.Dir(archivePath), CheckPrefix+"*"+CheckSuffix)
	if err != nil {
		return fmt.Errorf("stage %s for validation: %w", name, err)
	}
	tempPath := temp.Name()
	// A failed check can leave -wal/-shm/-journal siblings behind, so remove
	// them together; a process killed here leaves a file the startup sweep
	// reclaims by exact name.
	defer func() {
		_ = temp.Close()
		removeSQLiteFiles(tempPath)
	}()

	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("read %s from the archive: %w", name, err)
	}
	written, copyErr := io.Copy(temp, io.LimitReader(source, MaxArchiveSize+1))
	source.Close()
	if copyErr != nil {
		return fmt.Errorf("extract %s for validation: %w", name, copyErr)
	}
	if written > MaxArchiveSize {
		return fmt.Errorf("invalid backup file: %s exceeds the %d byte limit", name, MaxArchiveSize)
	}
	if written != int64(entry.UncompressedSize64) {
		return fmt.Errorf("invalid backup file: %s is truncated: the archive declares %d bytes but holds %d", name, entry.UncompressedSize64, written)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close staged %s: %w", name, err)
	}

	db, err := sql.Open("sqlite3", tempPath)
	if err != nil {
		return fmt.Errorf("open staged %s: %w", name, err)
	}
	defer db.Close()

	// quick_check(1) stops at the first problem, which is all a rejection needs.
	var result string
	if err := db.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil {
		return fmt.Errorf("invalid backup file: %s is not a readable SQLite database: %w", name, err)
	}
	if result != "ok" {
		return fmt.Errorf("invalid backup file: %s is corrupt: %s", name, result)
	}
	return nil
}

// removeSQLiteFiles deletes a database file and any journal or WAL siblings
// SQLite may have created next to it.
func removeSQLiteFiles(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
}
