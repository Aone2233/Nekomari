//go:build !windows

package upload

import (
	"os"
	"testing"
)

// blockRemoval makes the session directory unwritable so unlinking the scratch
// file fails with EACCES. A process running as root ignores the mode, so the
// block is verified before the test relies on it instead of asserting on the
// host's privileges.
func blockRemoval(t *testing.T, dir, path string) (release func()) {
	t.Helper()
	restore := func() { _ = os.Chmod(dir, 0o700) }
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("make %s unwritable: %v", dir, err)
	}
	if err := os.Remove(path); err == nil {
		restore()
		t.Skip("filesystem permits deletion regardless of directory permissions")
	}
	return restore
}
