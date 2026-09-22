package upload

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQuotaSurvivesRestartAndCancelReleasesIt(t *testing.T) {
	s := &Store{Root: t.TempDir(), MaxSize: 20, MaxSessions: 2, MaxTotalSize: 20, FreeSpace: unlimitedFreeSpace}
	first, err := s.Init(PurposeTheme, "a.zip", 12)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Store{Root: s.Root, MaxSize: 20, MaxSessions: 2, MaxTotalSize: 20, FreeSpace: unlimitedFreeSpace}
	if _, err := restarted.Init(PurposeTheme, "b.zip", 9); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota: %v", err)
	}
	if _, err := restarted.Init(PurposeTheme, "b.zip", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Init(PurposeTheme, "c.zip", 1); !errors.Is(err, ErrQuota) {
		t.Fatalf("session quota: %v", err)
	}
	if err := restarted.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Init(PurposeTheme, "c.zip", 12); err != nil {
		t.Fatalf("reservation not released: %v", err)
	}
}

func TestExpiredUploadRemovedAndQuotaReclaimed(t *testing.T) {
	s := &Store{Root: t.TempDir(), MaxSize: 10, MaxSessions: 1, TTL: time.Hour, FreeSpace: unlimitedFreeSpace}
	session, err := s.Init(PurposeTheme, "a.zip", 10)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(session.Directory, "upload.json"), past, past); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChunk(session.ID, 0, bytes.NewReader(make([]byte, 10))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired upload accepted: %v", err)
	}
	if err := s.CleanupExpired(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.Directory); !os.IsNotExist(err) {
		t.Fatal("expired files retained")
	}
	if _, err := s.Init(PurposeTheme, "b.zip", 10); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizationExcludesCancelAndDuplicateMerge(t *testing.T) {
	s := &Store{Root: t.TempDir(), MaxSize: 10, FreeSpace: unlimitedFreeSpace}
	session, err := s.Init(PurposeTheme, "a.zip", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChunk(session.ID, 0, bytes.NewReader([]byte{42})); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := s.Complete(session.ID, func(session Session) (Result, error) {
			close(entered)
			<-release
			data, err := os.ReadFile(session.ArchivePath)
			if err == nil && !bytes.Equal(data, []byte{42}) {
				err = errors.New("archive changed")
			}
			return Result{}, err
		})
		done <- err
	}()
	<-entered
	if err := s.Cancel(session.ID); !errors.Is(err, ErrBusy) {
		t.Errorf("cancel: %v", err)
	}
	if _, err := s.Merge(session.ID); !errors.Is(err, ErrBusy) {
		t.Errorf("merge: %v", err)
	}
	if err := s.CleanupExpired(); !errors.Is(err, ErrBusy) {
		t.Errorf("cleanup: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.Directory); !os.IsNotExist(err) {
		t.Fatal("session not cleaned")
	}
}

func TestCleanupPreservesUnknownDirectories(t *testing.T) {
	s := &Store{Root: t.TempDir(), MaxSize: 10, FreeSpace: unlimitedFreeSpace}
	unknown := filepath.Join(s.Root, "keep-me")
	if err := os.Mkdir(unknown, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupExpired(); err == nil {
		t.Fatal("unexpected directory not reported")
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unknown data removed")
	}
}
