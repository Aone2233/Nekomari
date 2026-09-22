package upload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	logger "github.com/Aone2233/nekomari/utils/log"
)

func (s *Store) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return 24 * time.Hour
}

// CleanupStats is a point-in-time view of the store for operators. Session
// counts and reservations are refreshed by every successful scan, so reading
// the stats performs no disk I/O and never waits for an in-flight upload.
type CleanupStats struct {
	// Sessions and ReservedBytes describe the declared reservations, not the
	// bytes currently allocated; the free-space budget is what bounds the
	// allocation those reservations imply.
	Sessions         int           `json:"sessions"`
	ReservedBytes    int64         `json:"reserved_bytes"`
	OldestSessionAge time.Duration `json:"oldest_session_age"`
	// LastSuccess is the last cleanup pass that completed without error.
	LastSuccess time.Time `json:"last_success"`
	// LastError is the most recent cleanup failure, empty after a success.
	LastError string `json:"last_error,omitempty"`
	// ReclaimedFiles and ReclaimedBytes are running totals for the abandoned
	// scratch files and empty reservation directories that reconciliation has
	// removed since the process started.
	ReclaimedFiles int   `json:"reclaimed_files"`
	ReclaimedBytes int64 `json:"reclaimed_bytes"`
}

// Stats returns the cached maintenance view. It takes a dedicated lock and
// does no I/O, so it is safe to call while cleanup runs.
func (s *Store) Stats() CleanupStats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	stats := s.stats
	if !s.oldestSession.IsZero() {
		// Kept as a timestamp internally so the age is exact at read time
		// instead of frozen at the last scan.
		stats.OldestSessionAge = time.Since(s.oldestSession)
	}
	return stats
}

// scanResult is the accounting one scan produced. A failed scan returns the
// zero value, which is why it is never recorded: a failure must leave the last
// known-good view intact rather than look like an empty store.
type scanResult struct {
	sessions int
	reserved int64
	oldest   time.Time
}

// recordScan publishes the accounting of a successful scan.
func (s *Store) recordScan(result scanResult) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.stats.Sessions = result.sessions
	s.stats.ReservedBytes = result.reserved
	s.oldestSession = result.oldest
}

// scan counts persisted reservations, including after restart. Only canonical
// upload directories are eligible for deletion; unknown entries fail admission
// and are reported rather than removed.
func (s *Store) scan() (scanResult, error) {
	var result scanResult
	entries, err := os.ReadDir(s.Root)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if !validUploadID(entry.Name()) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return scanResult{}, fmt.Errorf("unexpected entry in upload store")
		}
		dir := filepath.Join(s.Root, entry.Name())
		info, err := os.Stat(filepath.Join(dir, "upload.json"))
		if os.IsNotExist(err) {
			info, err = entry.Info()
		}
		if err != nil {
			return scanResult{}, err
		}
		if time.Since(info.ModTime()) >= s.ttl() {
			if err := s.cancel(entry.Name()); err != nil {
				return scanResult{}, err
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "upload.json"))
		if err != nil {
			return scanResult{}, err
		}
		var metadata Metadata
		if json.Unmarshal(data, &metadata) != nil || metadata.Size <= 0 || metadata.Size > s.MaxSize {
			return scanResult{}, fmt.Errorf("invalid upload reservation")
		}
		if result.reserved > (1<<63-1)-metadata.Size {
			return scanResult{}, ErrQuota
		}
		result.reserved += metadata.Size
		result.sessions++
		if result.oldest.IsZero() || info.ModTime().Before(result.oldest) {
			result.oldest = info.ModTime()
		}
	}
	s.recordScan(result)
	return result, nil
}

func (s *Store) admit(size int64) error {
	result, err := s.scan()
	if err != nil {
		return err
	}
	maxSessions, maxTotal := s.MaxSessions, s.MaxTotalSize
	if maxSessions <= 0 {
		maxSessions = 16
	}
	if maxTotal <= 0 {
		maxTotal = 8 << 30
	}
	if result.sessions >= maxSessions || size > maxTotal || result.reserved > maxTotal-size {
		return ErrQuota
	}
	// The reservation counts the declared payload, which is only a fraction of
	// what the filesystem has to hold before the session is released.
	return s.checkFreeSpace(size)
}

// ReconcileOrphans removes scratch files this package created and abandoned
// inside canonical upload directories, plus the empty reservation directories a
// process that died during Init left behind. It runs at store start, before the
// first admission: chunk.go holds the store lock for the whole of SaveChunk and
// merge, so a scratch file visible while the lock is held cannot belong to a
// live upload and must have been left by a process that died mid-write.
//
// Only names this package writes are eligible (see isTemporaryStoreFile).
// Unknown files, unknown directories, symlinks and anything outside a canonical
// session directory are left in place: scan() reports them and admission fails,
// which is the documented behaviour for unrecognised data.
func (s *Store) ReconcileOrphans() (int, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcileOrphans()
}

// reconcileOrphans is the caller-locked body of ReconcileOrphans. It keeps
// going after an individual failure and joins the errors so one undeletable
// scratch file cannot hide the rest of the store.
func (s *Store) reconcileOrphans() (int, int64, error) {
	entries, err := os.ReadDir(s.Root)
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
		if !validUploadID(entry.Name()) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		directory := filepath.Join(s.Root, entry.Name())
		contents, err := os.ReadDir(directory)
		if err != nil {
			failures = append(failures, fmt.Errorf("read upload directory %s: %w", directory, err))
			continue
		}
		hasMetadata := false
		for _, content := range contents {
			if content.Name() == "upload.json" {
				hasMetadata = true
				continue
			}
			if !isTemporaryStoreFile(content.Name()) {
				continue
			}
			info, err := content.Info()
			if err != nil {
				failures = append(failures, fmt.Errorf("stat orphan %s: %w", content.Name(), err))
				continue
			}
			// A directory or link that merely carries a scratch name is not
			// something this package created, so it is never removed.
			if !info.Mode().IsRegular() {
				continue
			}
			path := filepath.Join(directory, content.Name())
			if err := os.Remove(path); err != nil {
				failures = append(failures, fmt.Errorf("remove orphan %s: %w", path, err))
				continue
			}
			files++
			bytes += info.Size()
		}
		// Init creates the directory first and writes upload.json second, so a
		// canonical directory without metadata is either a reservation being
		// created right now or one whose creating process died between those two
		// steps. The store lock rules out the first. Leaving it in place is not
		// harmless: scan() fails on the missing upload.json, which refuses every
		// later admission until the directory's mtime passes the 24-hour TTL --
		// a crash during Init would take uploads down for a day.
		//
		// It is removed only once it is empty, so a directory that still holds
		// something this package cannot name is reported by scan() instead of
		// being deleted along with it.
		if !hasMetadata {
			remaining, err := os.ReadDir(directory)
			if err != nil {
				failures = append(failures, fmt.Errorf("re-read upload directory %s: %w", directory, err))
				continue
			}
			if len(remaining) > 0 {
				continue
			}
			if err := os.Remove(directory); err != nil {
				failures = append(failures, fmt.Errorf("remove abandoned upload directory %s: %w", directory, err))
				continue
			}
			files++
		}
	}
	if files > 0 {
		s.statsMu.Lock()
		s.stats.ReclaimedFiles += files
		s.stats.ReclaimedBytes += bytes
		s.statsMu.Unlock()
	}
	return files, bytes, errors.Join(failures...)
}

// isTemporaryStoreFile reports whether name is a scratch file this package
// creates inside a session directory and may therefore reclaim:
//
//	.merged-<rand>.zip    merge()'s in-flight archive
//	.<index>-<rand>.part  SaveChunk()'s in-flight chunk
//
// Both come from os.CreateTemp, whose random suffix is decimal digits. Matching
// that suffix strictly keeps the allowlist from claiming names this package
// never wrote, such as the published "<index>.part" chunk or archive.zip.
func isTemporaryStoreFile(name string) bool {
	if strings.HasPrefix(name, ".merged-") && strings.HasSuffix(name, ".zip") {
		return allDigits(name[len(".merged-") : len(name)-len(".zip")])
	}
	if strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".part") {
		index, random, ok := strings.Cut(name[1:len(name)-len(".part")], "-")
		return ok && allDigits(index) && allDigits(random)
	}
	return false
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (s *Store) CleanupExpired() error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	_, err := s.scan()
	return err
}

// RunCleanup is owned by the server lifecycle, including first-run setup. The
// first pass reconciles scratch files left by a crashed process and expires
// sessions, then expiry repeats hourly. Both passes report through
// reportCleanup, so a failure is logged once per burst instead of being
// discarded.
func (s *Store) RunCleanup(ctx context.Context) {
	s.startupPass()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expirePass()
		}
	}
}

// startupPass reconciles abandoned scratch files and expires sessions in one
// locked pass, then reports the outcome. Reclaiming here matters because a
// session survives up to its TTL: without this, a crashed merge would keep its
// scratch bytes on disk for a day.
func (s *Store) startupPass() {
	files, bytes, err := s.reconcileAndExpire()
	if files > 0 {
		logger.Info("upload", "reclaimed abandoned upload scratch data",
			"entries", files, "bytes", bytes)
	}
	s.reportCleanup(err)
}

// expirePass runs one expiry pass and reports its outcome. The hourly ticker
// uses it so the error is not dropped.
func (s *Store) expirePass() {
	s.reportCleanup(s.CleanupExpired())
}

// reconcileAndExpire holds the store lock across both steps so an admission
// cannot interleave between the two scans.
func (s *Store) reconcileAndExpire() (int, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, bytes, err := s.reconcileOrphans()
	if _, scanErr := s.scan(); scanErr != nil {
		err = errors.Join(err, scanErr)
	}
	return files, bytes, err
}

// reportCleanup records the outcome of one maintenance pass and logs it at most
// once per failure burst: the first failure is logged, identical repeats are
// counted, and recovery logs once with the number of folded repeats. Without
// the gate, a store that stays broken (an unreadable directory, an unrecognised
// entry) would emit a line every tick forever.
func (s *Store) reportCleanup(err error) {
	if errors.Is(err, ErrBusy) {
		// Busy is the normal outcome while an upload finalizes; it is not a
		// cleanup failure and logging it hourly would be noise.
		return
	}
	var message string
	var args []any
	s.statsMu.Lock()
	switch {
	case err == nil && s.stats.LastError == "":
		s.stats.LastSuccess = time.Now()
	case err == nil:
		message = "upload store cleanup recovered"
		args = []any{"suppressed_failures", s.suppressed}
		s.suppressed = 0
		s.stats.LastError = ""
		s.stats.LastSuccess = time.Now()
	case err.Error() == s.stats.LastError:
		// Same failure as the one already logged: count it, stay quiet.
		s.suppressed++
	default:
		message = "upload store cleanup failed"
		args = []any{"error", err.Error(), "suppressed_failures", s.suppressed}
		s.suppressed = 0
		s.stats.LastError = err.Error()
	}
	s.statsMu.Unlock()
	if message == "" {
		return
	}
	// Logged outside statsMu so a slow writer cannot stall Stats.
	if err == nil {
		logger.Info("upload", message, args...)
		return
	}
	logger.Error("upload", message, args...)
}
