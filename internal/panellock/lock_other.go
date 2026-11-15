//go:build !unix && !windows

package panellock

import (
	"errors"
	"os"
)

func openLockFile(string) (*os.File, error) {
	return nil, errors.New("panel data-directory locking is unsupported on this platform")
}

func tryLock(*os.File) error {
	return errors.New("panel data-directory locking is unsupported on this platform")
}
