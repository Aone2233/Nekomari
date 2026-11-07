//go:build windows

// Package fsyncdir synchronizes directory entries where the OS supports it.
package fsyncdir

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// Windows requires a write-capable directory handle for FlushFileBuffers.
// If the filesystem or permissions do not support it, restore fails closed.
func Sync(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	return errors.Join(windows.FlushFileBuffers(handle), windows.CloseHandle(handle))
}
