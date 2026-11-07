//go:build windows

package panellock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 {
		_ = f.Close()
		return nil, errors.New("panel lock cannot be a symlink")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("panel lock is not a regular file")
	}
	return f, nil
}

func tryLock(f *os.File) error {
	var overlap windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrInUse
	}
	if err != nil {
		return fmt.Errorf("lock panel data directory: %w", err)
	}
	return nil
}
