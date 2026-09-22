//go:build android || darwin || dragonfly || freebsd || ios || linux

package upload

import (
	"math"

	"golang.org/x/sys/unix"
)

// platformFreeSpace reports the bytes the calling user may still write on the
// filesystem holding path. Bavail is used instead of Bfree because it excludes
// the blocks reserved for root: on an ext4 volume that reserves 5% for root,
// Bfree would promise the service account space it cannot actually write.
func platformFreeSpace(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if available > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(available), nil
}
