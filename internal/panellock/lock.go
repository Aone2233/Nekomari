// Package panellock prevents cooperating panel processes from opening the same
// data directory at the same time. It is not a fence for older binaries or
// arbitrary database writers.
package panellock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const FileName = ".nekomari-panel.lock"

var ErrInUse = errors.New("another panel process holds the data-directory lock")

type Lease struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire must run before restore or main database initialization. The file
// must not be removed: a second lock on a newly created inode would not fence
// a process that still holds the old one.
func Acquire(dataDir string) (*Lease, error) {
	path := filepath.Join(dataDir, FileName)
	f, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("open panel data-directory lock: %w", err)
	}
	if err := tryLock(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lease{file: f}, nil
}

func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		// Closing releases the operating-system lock, including after a crash.
		l.err = l.file.Close()
	})
	return l.err
}
