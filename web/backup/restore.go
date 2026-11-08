// Package backup contains the shared upload preparation for backup restores.
package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Aone2233/nekomari/internal/fsyncdir"
	"github.com/Aone2233/nekomari/internal/restorearchive"
)

var restoreMutex sync.Mutex

var pendingRestore struct {
	sync.Mutex
	lock *RestoreLock
	path string
}

const (
	MaxArchiveSize     = restorearchive.MaxArchiveSize
	stagedUploadPrefix = ".backup-upload-"
	stagedUploadSuffix = ".zip"
	stagedCheckPrefix  = restorearchive.CheckPrefix
	stagedCheckSuffix  = restorearchive.CheckSuffix
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

// StageUploadedBackup validates an archive without making it eligible for
// startup restore. The server publishes it only after its dependencies drain.
func StageUploadedBackup(file io.Reader, filename string) error {
	lock, err := AcquireRestoreLock()
	if err != nil {
		return err
	}
	if err := lock.stageUploadedBackup(file, filename); err != nil {
		lock.Release()
		return err
	}
	return nil
}

func (l *RestoreLock) stageUploadedBackup(file io.Reader, filename string) error {
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return fmt.Errorf("uploaded file must be a ZIP archive")
	}
	if err := os.MkdirAll("./data", 0755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	// A previous published archive belongs to an earlier restore attempt; do
	// not replace it. A staged archive is deliberately not named backup.zip.
	if _, err := os.Lstat(filepath.Join(".", "data", "backup.zip")); err == nil {
		return fmt.Errorf("a backup is already pending restore")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check pending backup: %w", err)
	}
	if _, err := os.Lstat(filepath.Join(".", "data", restorearchive.StateFileName)); err == nil {
		return fmt.Errorf("a restore journal exists; inspect it before staging a backup")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check restore journal: %w", err)
	}
	tempFile, err := os.CreateTemp("./data", restorearchive.PendingPrefix+"*.zip")
	if err != nil {
		return fmt.Errorf("create temporary backup: %w", err)
	}
	tempPath := tempFile.Name()
	registered := false
	defer func() {
		if !registered {
			_ = os.Remove(tempPath)
		}
	}()
	written, err := io.Copy(tempFile, io.LimitReader(file, MaxArchiveSize+1))
	if err != nil {
		tempFile.Close()
		return fmt.Errorf("save uploaded backup: %w", err)
	}
	if written > MaxArchiveSize {
		tempFile.Close()
		return fmt.Errorf("backup archive exceeds the %d byte limit", MaxArchiveSize)
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return fmt.Errorf("sync uploaded backup: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close uploaded backup: %w", err)
	}

	if err := ValidateArchive(tempPath); err != nil {
		return err
	}
	// Make the inert name durable before reporting a successfully staged upload.
	if err := syncRestoreDirectory(filepath.Join(".", "data")); err != nil {
		return fmt.Errorf("sync staged backup directory: %w", err)
	}

	pendingRestore.Lock()
	pendingRestore.lock, pendingRestore.path = l, tempPath
	pendingRestore.Unlock()
	registered = true
	return nil
}

// PublishPendingRestore must run after HTTP and resource cleanup, while the
// panel data-directory lease is still held. A crash before publication leaves
// the archive inert for manual recovery rather than restoring it on startup.
func PublishPendingRestore() error {
	return publishPendingRestore(syncRestoreDirectory)
}

func syncRestoreDirectory(path string) error { return fsyncdir.Sync(path) }

func publishPendingRestore(syncDir func(string) error) error {
	pendingRestore.Lock()
	defer pendingRestore.Unlock()
	if pendingRestore.lock == nil {
		return nil
	}
	finalPath := filepath.Join(".", "data", "backup.zip")
	if _, err := os.Lstat(finalPath); err == nil {
		return fmt.Errorf("a backup is already pending restore")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check pending backup: %w", err)
	}
	// Linking publishes a complete same-filesystem file without replacing an
	// existing archive, even if an uncooperative process races the check.
	if err := os.Link(pendingRestore.path, finalPath); err != nil {
		return fmt.Errorf("publish pending backup: %w", err)
	}
	// After the link succeeds its visibility on restart is uncertain if this
	// sync fails. Retain both names and the lock; do not claim a clean handoff.
	if err := syncDir(filepath.Join(".", "data")); err != nil {
		return fmt.Errorf("published backup may be eligible for restore, but directory sync failed: %w", err)
	}
	// A leftover link is inert and is intentionally not auto-pruned.
	_ = os.Remove(pendingRestore.path)
	pendingRestore.lock.Release()
	pendingRestore.lock, pendingRestore.path = nil, ""
	return nil
}

// CleanupStagedUploads reclaims legacy upload/check files a process that died
// mid-upload left in ./data. The newer .backup-pending-* files are deliberately
// retained for manual recovery when shutdown or publication did not finish.
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

// isStagedUploadName matches only the legacy upload-staging pattern. New
// pending restore files are deliberately not eligible for automatic cleanup.
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

// ValidateArchive applies the same validation used immediately before startup restore.
func ValidateArchive(archivePath string) error {
	return restorearchive.ValidateArchive(archivePath)
}
