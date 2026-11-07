//go:build unix

package panellock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("panel lock is not a regular file")
	}
	return f, nil
}

func tryLock(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return ErrInUse
		}
		return fmt.Errorf("lock panel data directory: %w", err)
	}
	return nil
}
