package upload

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return 24 * time.Hour
}

// scan counts persisted reservations, including after restart. Only canonical
// upload directories are eligible for deletion; unknown entries fail admission.
func (s *Store) scan() (int, int64, error) {
	entries, err := os.ReadDir(s.Root)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var reserved int64
	for _, entry := range entries {
		if !validUploadID(entry.Name()) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return 0, 0, fmt.Errorf("unexpected entry in upload store")
		}
		dir := filepath.Join(s.Root, entry.Name())
		info, err := os.Stat(filepath.Join(dir, "upload.json"))
		if os.IsNotExist(err) {
			info, err = entry.Info()
		}
		if err != nil {
			return 0, 0, err
		}
		if time.Since(info.ModTime()) >= s.ttl() {
			if err := s.cancel(entry.Name()); err != nil {
				return 0, 0, err
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "upload.json"))
		if err != nil {
			return 0, 0, err
		}
		var metadata Metadata
		if json.Unmarshal(data, &metadata) != nil || metadata.Size <= 0 || metadata.Size > s.MaxSize {
			return 0, 0, fmt.Errorf("invalid upload reservation")
		}
		if reserved > (1<<63-1)-metadata.Size {
			return 0, 0, ErrQuota
		}
		reserved += metadata.Size
		count++
	}
	return count, reserved, nil
}

func (s *Store) admit(size int64) error {
	count, reserved, err := s.scan()
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
	if count >= maxSessions || size > maxTotal || reserved > maxTotal-size {
		return ErrQuota
	}
	return nil
}

func (s *Store) CleanupExpired() error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	_, _, err := s.scan()
	return err
}

// RunCleanup is owned by the server lifecycle, including first-run setup.
func (s *Store) RunCleanup(ctx context.Context) {
	_ = s.CleanupExpired()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.CleanupExpired()
		}
	}
}
