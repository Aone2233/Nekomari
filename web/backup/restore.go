// Package backup contains the shared upload preparation for backup restores.
package backup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var restoreMutex sync.Mutex

const (
	MaxArchiveSize    int64 = 4 << 30 // 4 GiB
	maxArchiveEntries       = 100_000
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
// path, so a file that survives belongs to a process that was killed between
// CreateTemp and the rename -- up to MaxArchiveSize each. Nothing else reclaims
// them: the startup restore only clears ./data when a backup.zip is actually
// waiting, so an abandoned staging file would otherwise sit on the disk
// indefinitely.
//
// Only the exact name os.CreateTemp produces from the pattern in
// SaveUploadedBackup is eligible, and only regular files, so an operator's own
// file in ./data is never touched. The restore lock is taken for the duration:
// if a restore is already staging a file, that operation owns its cleanup.
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
		if !isStagedUploadName(entry.Name()) {
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
	const prefix = ".backup-upload-"
	const suffix = ".zip"
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
// the startup restore path extracts it into data/.
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
	for _, entry := range reader.File {
		if entry.Name == "komari-backup-markup" {
			hasMarkup = true
		}
		if entry.UncompressedSize64 > uint64(MaxArchiveSize) || expandedSize > uint64(MaxArchiveSize)-entry.UncompressedSize64 {
			return fmt.Errorf("backup archive expands beyond the %d byte limit", MaxArchiveSize)
		}
		expandedSize += entry.UncompressedSize64
	}
	if !hasMarkup {
		return fmt.Errorf("invalid backup file: missing komari-backup-markup file")
	}
	return nil
}
