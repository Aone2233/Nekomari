//go:build !windows

// Package fsyncdir synchronizes directory entries where the OS supports it.
package fsyncdir

import (
	"errors"
	"os"
)

// Sync returns an error rather than ignoring an unsupported filesystem.
func Sync(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
