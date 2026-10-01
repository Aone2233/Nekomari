// Package backup contains the shared upload preparation for backup restores.
package backup

import (
	"archive/zip"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	// Registers the "sqlite3" driver used to run PRAGMA quick_check on a staged
	// database copy. Already a direct dependency of this module: the database
	// layer links the same driver.
	_ "github.com/mattn/go-sqlite3"
)

var restoreMutex sync.Mutex

const (
	MaxArchiveSize    int64 = 4 << 30 // 4 GiB
	maxArchiveEntries       = 100_000
)

const (
	// markupEntryName marks an archive as a Komari backup.
	markupEntryName = "komari-backup-markup"
	// databaseEntryName is the database at the archive root. The startup restore
	// extracts entries relative to ./data, so anything else (data/komari.db,
	// ./komari.db, a directory) would extract to a path the panel never reads.
	databaseEntryName = "komari.db"

	// Names os.CreateTemp produces from this package's staging patterns: the
	// literal prefix, a decimal random suffix, the extension.
	stagedUploadPrefix = ".backup-upload-"
	stagedUploadSuffix = ".zip"
	stagedCheckPrefix  = ".backup-check-"
	stagedCheckSuffix  = ".db"
)

// sqliteMagic starts every SQLite 3 database file; sqliteHeaderSize is the fixed
// 100-byte header that follows the magic and describes the rest of the file.
const (
	sqliteMagic      = "SQLite format 3\x00"
	sqliteHeaderSize = 100
)

// RestoreLock serializes staging a backup until the caller releases it.
// A successful restore must hold this lock until the process restarts so a
// second request cannot replace backup.zip in the meantime.
type RestoreLock struct {
	once sync.Once
}

func AcquireRestoreLock() (*RestoreLock, error) {
	if !restoreMutex.TryLock() {
		return nil, fmt.Errorf("another restore operation is already in progress")
	}
	return &RestoreLock{}, nil
}

func (l *RestoreLock) Release() {
	l.once.Do(restoreMutex.Unlock)
}

// SaveUploadedBackup validates a Komari backup and stages it for restoration
// during the next process startup.
func SaveUploadedBackup(file io.Reader, filename string) error {
	lock, err := AcquireRestoreLock()
	if err != nil {
		return err
	}
	defer lock.Release()
	return lock.SaveUploadedBackup(file, filename)
}

// SaveUploadedBackup stages a backup while the caller holds the restore lock.
func (l *RestoreLock) SaveUploadedBackup(file io.Reader, filename string) error {
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return fmt.Errorf("uploaded file must be a ZIP archive")
	}
	if err := os.MkdirAll("./data", 0755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	// Stage alongside backup.zip so the final rename is atomic on every
	// supported platform and never crosses filesystem boundaries.
	tempFile, err := os.CreateTemp("./data", ".backup-upload-*.zip")
	if err != nil {
		return fmt.Errorf("create temporary backup: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	written, err := io.Copy(tempFile, io.LimitReader(file, MaxArchiveSize+1))
	if err != nil {
		tempFile.Close()
		return fmt.Errorf("save uploaded backup: %w", err)
	}
	if written > MaxArchiveSize {
		tempFile.Close()
		return fmt.Errorf("backup archive exceeds the %d byte limit", MaxArchiveSize)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close uploaded backup: %w", err)
	}

	if err := ValidateArchive(tempPath); err != nil {
		return err
	}

	finalPath := filepath.Join(".", "data", "backup.zip")
	if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove previous backup: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err == nil {
		return nil
	}
	in, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("prepare backup file: %w", err)
	}
	defer in.Close()
	out, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("write backup file: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close backup file: %w", err)
	}
	return nil
}

// CleanupStagedUploads reclaims staging files a process that died mid-upload
// left in ./data. SaveUploadedBackup removes its temporary file on every error
// path, and ValidateArchive removes its temporary database copy the same way, so
// a file that survives belongs to a process that was killed in between -- up to
// MaxArchiveSize each for an upload, or one database for a content check.
// Nothing else reclaims them: the startup restore only clears ./data when a
// backup.zip is actually waiting, so an abandoned staging file would otherwise
// sit on the disk indefinitely.
//
// Only the exact names os.CreateTemp produces from the patterns in this package
// are eligible, and only regular files, so an operator's own file in ./data is
// never touched. The restore lock is taken for the duration: if a restore is
// already staging a file, that operation owns its cleanup.
func CleanupStagedUploads() (int, int64, error) {
	lock, err := AcquireRestoreLock()
	if err != nil {
		return 0, 0, nil
	}
	defer lock.Release()

	entries, err := os.ReadDir("./data")
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var files int
	var bytes int64
	var failures []error
	for _, entry := range entries {
		if !isStagedUploadName(entry.Name()) && !isStagedCheckName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, fmt.Errorf("stat staged upload %s: %w", entry.Name(), err))
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(".", "data", entry.Name())
		if err := os.Remove(path); err != nil {
			failures = append(failures, fmt.Errorf("remove staged upload %s: %w", path, err))
			continue
		}
		files++
		bytes += info.Size()
	}
	return files, bytes, errors.Join(failures...)
}

// isStagedUploadName matches exactly what os.CreateTemp writes for the pattern
// used by SaveUploadedBackup: the literal prefix, a decimal random suffix and
// the .zip extension. Matching the suffix strictly keeps the allowlist from
// claiming a name this package never created.
func isStagedUploadName(name string) bool {
	return isStagedName(name, stagedUploadPrefix, stagedUploadSuffix)
}

// isStagedCheckName matches the temporary database copy ValidateArchive extracts
// for its content check, with the same strict shape.
func isStagedCheckName(name string) bool {
	return isStagedName(name, stagedCheckPrefix, stagedCheckSuffix)
}

func isStagedName(name, prefix, suffix string) bool {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	random := name[len(prefix) : len(name)-len(suffix)]
	if random == "" {
		return false
	}
	for _, char := range random {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// ValidateArchive checks the backup marker and bounds archive expansion before
// the startup restore path extracts it into data/, and rejects an archive whose
// komari.db is missing, empty, truncated or corrupt.
//
// The content check is what stands between an unusable upload and a destructive
// restore: the startup path clears ./data and only then unzips this archive, so
// an archive that passes here but holds a broken database would leave the panel
// with an empty data directory and the install wizard.
func ValidateArchive(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open backup archive: %w", err)
	}
	defer reader.Close()

	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("backup archive has too many files: %d", len(reader.File))
	}

	var expandedSize uint64
	hasMarkup := false
	var database *zip.File
	for _, entry := range reader.File {
		if entry.Name == markupEntryName {
			hasMarkup = true
		}
		if database == nil && !entry.FileInfo().IsDir() && isRootEntry(entry.Name, databaseEntryName) {
			database = entry
		}
		if entry.UncompressedSize64 > uint64(MaxArchiveSize) || expandedSize > uint64(MaxArchiveSize)-entry.UncompressedSize64 {
			return fmt.Errorf("backup archive expands beyond the %d byte limit", MaxArchiveSize)
		}
		expandedSize += entry.UncompressedSize64
	}
	if !hasMarkup {
		return fmt.Errorf("invalid backup file: missing %s file", markupEntryName)
	}
	if database == nil {
		return fmt.Errorf("invalid backup file: missing %s at the archive root", databaseEntryName)
	}

	if err := validateDatabaseHeader(database); err != nil {
		return err
	}
	return quickCheckDatabase(path, database)
}

// isRootEntry reports whether a zip entry name refers to name at the archive
// root. Zip names always use forward slashes, but a leading "./" is accepted.
func isRootEntry(entryName, name string) bool {
	normalized := strings.ReplaceAll(entryName, "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	return normalized == name
}

// validateDatabaseHeader streams the fixed 100-byte SQLite header out of the
// archive entry and rejects a database that cannot be one. Only the header is
// read here, never the whole entry.
func validateDatabaseHeader(entry *zip.File) error {
	size := entry.UncompressedSize64
	if size == 0 {
		return fmt.Errorf("invalid backup file: %s is empty", databaseEntryName)
	}
	if size < sqliteHeaderSize {
		return fmt.Errorf("invalid backup file: %s is %d bytes, too short to be a SQLite database", databaseEntryName, size)
	}

	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("read %s from the archive: %w", databaseEntryName, err)
	}
	defer source.Close()
	header := make([]byte, sqliteHeaderSize)
	if _, err := io.ReadFull(source, header); err != nil {
		return fmt.Errorf("read %s header: %w", databaseEntryName, err)
	}

	if problem := sqliteHeaderProblem(header, size); problem != "" {
		return fmt.Errorf("invalid backup file: %s %s", databaseEntryName, problem)
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
func quickCheckDatabase(archivePath string, entry *zip.File) error {
	temp, err := os.CreateTemp(filepath.Dir(archivePath), stagedCheckPrefix+"*"+stagedCheckSuffix)
	if err != nil {
		return fmt.Errorf("stage %s for validation: %w", databaseEntryName, err)
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
		return fmt.Errorf("read %s from the archive: %w", databaseEntryName, err)
	}
	written, copyErr := io.Copy(temp, io.LimitReader(source, MaxArchiveSize+1))
	source.Close()
	if copyErr != nil {
		return fmt.Errorf("extract %s for validation: %w", databaseEntryName, copyErr)
	}
	if written > MaxArchiveSize {
		return fmt.Errorf("invalid backup file: %s exceeds the %d byte limit", databaseEntryName, MaxArchiveSize)
	}
	if written != int64(entry.UncompressedSize64) {
		return fmt.Errorf("invalid backup file: %s is truncated: the archive declares %d bytes but holds %d", databaseEntryName, entry.UncompressedSize64, written)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close staged %s: %w", databaseEntryName, err)
	}

	db, err := sql.Open("sqlite3", tempPath)
	if err != nil {
		return fmt.Errorf("open staged %s: %w", databaseEntryName, err)
	}
	defer db.Close()

	// quick_check(1) stops at the first problem, which is all a rejection needs.
	var result string
	if err := db.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil {
		return fmt.Errorf("invalid backup file: %s is not a readable SQLite database: %w", databaseEntryName, err)
	}
	if result != "ok" {
		return fmt.Errorf("invalid backup file: %s is corrupt: %s", databaseEntryName, result)
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
