// Package upload provides the shared chunked archive upload flow.
package upload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/web/backup"
	"github.com/google/uuid"
)

const ChunkSize int64 = 5 * 1024 * 1024

type Purpose string

const (
	PurposeBackup Purpose = "backup"
	PurposePlugin Purpose = "plugin"
	PurposeTheme  Purpose = "theme"
)

var ErrNotFound = errors.New("upload not found")
var ErrBusy = errors.New("upload store busy; retry later")
var ErrQuota = errors.New("upload quota exceeded")

type Metadata struct {
	Purpose  Purpose `json:"purpose"`
	Size     int64   `json:"size"`
	Filename string  `json:"filename"`
}

type Session struct {
	ID          string
	Metadata    Metadata
	Directory   string
	ArchivePath string
}

type Store struct {
	Root    string
	MaxSize int64
	// Zero values select 16 sessions, 8 GiB of reserved payload and 24 hours.
	MaxSessions  int
	MaxTotalSize int64
	TTL          time.Duration
	// FreeSpace reports the bytes available to this process on the filesystem
	// holding the store root. Nil selects the build-tagged platform query in
	// freespace_windows.go / freespace_unix.go / freespace_other.go; tests
	// inject a deterministic implementation so admission never depends on the
	// host's real disk.
	FreeSpace func(path string) (int64, error)
	mu        sync.Mutex
	// statsMu guards the maintenance view read by Stats. It is a leaf lock, so
	// Stats stays cheap and cannot block behind an in-flight upload.
	statsMu       sync.Mutex
	stats         CleanupStats
	oldestSession time.Time
	// suppressed counts repeats of the cleanup failure already logged for the
	// current burst, so a failure that repeats every tick logs once.
	suppressed int
}

var DefaultStore = &Store{
	Root:    filepath.Join(".", "data", ".uploading"),
	MaxSize: backup.MaxArchiveSize,
}

func (s *Store) Init(purpose Purpose, filename string, size int64) (Session, error) {
	if !s.mu.TryLock() {
		return Session{}, ErrBusy
	}
	defer s.mu.Unlock()
	if !isKnownPurpose(purpose) {
		return Session{}, fmt.Errorf("invalid upload purpose")
	}
	if size <= 0 || size > s.MaxSize {
		return Session{}, fmt.Errorf("size must be between 1 and %d bytes", s.MaxSize)
	}
	if len(filename) > 1024 {
		return Session{}, fmt.Errorf("filename too long")
	}
	if err := s.admit(size); err != nil {
		return Session{}, err
	}

	id := uuid.NewString()
	directory := filepath.Join(s.Root, id)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return Session{}, fmt.Errorf("create upload directory: %w", err)
	}
	metadata := Metadata{Purpose: purpose, Size: size, Filename: filename}
	data, err := json.Marshal(metadata)
	if err != nil {
		_ = os.RemoveAll(directory)
		return Session{}, fmt.Errorf("encode upload metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "upload.json"), data, 0600); err != nil {
		_ = os.RemoveAll(directory)
		return Session{}, fmt.Errorf("write upload metadata: %w", err)
	}
	return Session{ID: id, Metadata: metadata, Directory: directory}, nil
}

func (s *Store) SaveChunk(uploadID string, index int64, source io.Reader) error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	session, err := s.load(uploadID)
	if err != nil {
		return err
	}
	expectedSize, err := expectedChunkSize(session.Metadata.Size, index)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(session.Directory, fmt.Sprintf(".%d-*.part", index))
	if err != nil {
		return fmt.Errorf("create chunk: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	written, copyErr := io.Copy(temporary, io.LimitReader(source, ChunkSize+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("write chunk: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close chunk: %w", closeErr)
	}
	if written != expectedSize {
		return fmt.Errorf("chunk %d has size %d, want %d", index, written, expectedSize)
	}

	chunkPath := filepath.Join(session.Directory, chunkFilename(index))
	if err := os.Remove(chunkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace chunk: %w", err)
	}
	if err := os.Rename(temporaryPath, chunkPath); err != nil {
		return fmt.Errorf("publish chunk: %w", err)
	}
	return nil
}

func (s *Store) Merge(uploadID string) (Session, error) {
	if !s.mu.TryLock() {
		return Session{}, ErrBusy
	}
	defer s.mu.Unlock()
	return s.merge(uploadID)
}

// Complete holds admission through finalization, so cancel/merge cannot remove
// an archive while an installer or backup restorer is reading it.
func (s *Store) Complete(uploadID string, finalize Finalizer) (Result, error) {
	if !s.mu.TryLock() {
		return Result{}, ErrBusy
	}
	defer s.mu.Unlock()
	session, err := s.merge(uploadID)
	if err != nil {
		return Result{}, err
	}
	defer s.cancel(uploadID)
	return finalize(session)
}

func (s *Store) merge(uploadID string) (Session, error) {
	session, err := s.load(uploadID)
	if err != nil {
		return Session{}, err
	}

	temporary, err := os.CreateTemp(session.Directory, ".merged-*.zip")
	if err != nil {
		return Session{}, fmt.Errorf("create merged archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	for index := int64(0); index < chunkCount(session.Metadata.Size); index++ {
		chunkPath := filepath.Join(session.Directory, chunkFilename(index))
		info, err := os.Stat(chunkPath)
		if err != nil {
			if os.IsNotExist(err) {
				return Session{}, fmt.Errorf("chunk %d is missing", index)
			}
			return Session{}, fmt.Errorf("read chunk %d: %w", index, err)
		}
		expectedSize, _ := expectedChunkSize(session.Metadata.Size, index)
		if info.Size() != expectedSize {
			return Session{}, fmt.Errorf("chunk %d has size %d, want %d", index, info.Size(), expectedSize)
		}
		chunk, err := os.Open(chunkPath)
		if err != nil {
			return Session{}, fmt.Errorf("open chunk %d: %w", index, err)
		}
		_, copyErr := io.Copy(temporary, chunk)
		closeErr := chunk.Close()
		if copyErr != nil {
			return Session{}, fmt.Errorf("merge chunk %d: %w", index, copyErr)
		}
		if closeErr != nil {
			return Session{}, fmt.Errorf("close chunk %d: %w", index, closeErr)
		}
	}
	if err := temporary.Close(); err != nil {
		return Session{}, fmt.Errorf("close merged archive: %w", err)
	}

	archivePath := filepath.Join(session.Directory, "archive.zip")
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return Session{}, err
	}
	if err := os.Rename(temporaryPath, archivePath); err != nil {
		return Session{}, fmt.Errorf("publish merged archive: %w", err)
	}
	session.ArchivePath = archivePath
	return session, nil
}

func (s *Store) Cancel(uploadID string) error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	return s.cancel(uploadID)
}

func (s *Store) cancel(uploadID string) error {
	if !validUploadID(uploadID) {
		return fmt.Errorf("invalid upload id")
	}
	if err := os.RemoveAll(filepath.Join(s.Root, uploadID)); err != nil {
		return fmt.Errorf("remove upload: %w", err)
	}
	return nil
}

func (s *Store) load(uploadID string) (Session, error) {
	if !validUploadID(uploadID) {
		return Session{}, fmt.Errorf("invalid upload id")
	}
	directory := filepath.Join(s.Root, uploadID)
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Session{}, fmt.Errorf("invalid upload directory")
	}
	metaInfo, err := os.Stat(filepath.Join(directory, "upload.json"))
	if err != nil {
		return Session{}, ErrNotFound
	}
	if time.Since(metaInfo.ModTime()) >= s.ttl() {
		return Session{}, ErrNotFound
	}
	data, err := os.ReadFile(filepath.Join(directory, "upload.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("read upload metadata: %w", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Session{}, fmt.Errorf("read upload metadata: %w", err)
	}
	if !isKnownPurpose(metadata.Purpose) || metadata.Size <= 0 || metadata.Size > s.MaxSize {
		return Session{}, fmt.Errorf("invalid upload metadata")
	}
	return Session{ID: uploadID, Metadata: metadata, Directory: directory}, nil
}

func isKnownPurpose(purpose Purpose) bool {
	return purpose == PurposeBackup || purpose == PurposePlugin || purpose == PurposeTheme
}

func validUploadID(uploadID string) bool {
	id, err := uuid.Parse(uploadID)
	return err == nil && id.String() == uploadID
}

func chunkCount(size int64) int64 {
	return (size + ChunkSize - 1) / ChunkSize
}

func expectedChunkSize(size, index int64) (int64, error) {
	if index < 0 || index >= chunkCount(size) {
		return 0, fmt.Errorf("invalid chunk index")
	}
	if index == chunkCount(size)-1 {
		return size - index*ChunkSize, nil
	}
	return ChunkSize, nil
}

func chunkFilename(index int64) string {
	return strconv.FormatInt(index, 10) + ".part"
}
