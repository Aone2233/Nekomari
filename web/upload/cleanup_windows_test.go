//go:build windows

package upload

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// blockRemoval holds the file open without sharing so the OS refuses every
// other access to it, including the delete cleanup performs. The read-only
// attribute is not enough: os.Remove clears FILE_ATTRIBUTE_READONLY and retries
// (see os/file_windows.go), so the failure would never materialise.
func blockRemoval(t *testing.T, dir, path string) (release func()) {
	t.Helper()
	_ = dir // the exclusive handle is what blocks the delete on Windows
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open %s without sharing: %v", path, err)
	}
	if err := os.Remove(path); err == nil {
		// The block did not take on this filesystem; asserting on it would
		// make the test depend on host behaviour.
		_ = windows.CloseHandle(handle)
		t.Skip("filesystem permits deletion despite the exclusive handle")
	}
	return func() { _ = windows.CloseHandle(handle) }
}
