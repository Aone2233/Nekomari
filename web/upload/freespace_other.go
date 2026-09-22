//go:build !windows && !android && !darwin && !dragonfly && !freebsd && !ios && !linux

package upload

import (
	"fmt"
	"runtime"
)

// platformFreeSpace refuses instead of guessing on platforms this package has
// no query for. OpenBSD and NetBSD expose the value through different syscalls
// and field names than unix.Statfs, and the release matrix only ships linux,
// windows and darwin; shipping an untested probe there would be worse than an
// explicit refusal. Failing closed keeps the guarantee that admission never
// runs on a filesystem assumed to be infinite, and the operator sees the
// reason in the 507 response instead of a build failure.
func platformFreeSpace(string) (int64, error) {
	return 0, fmt.Errorf("free space query is not implemented on %s", runtime.GOOS)
}
