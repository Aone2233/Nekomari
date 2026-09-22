//go:build windows

package upload

import (
	"math"

	"golang.org/x/sys/windows"
)

// platformFreeSpace reports the bytes the calling user may still write on the
// volume holding path. GetDiskFreeSpaceEx returns that quota directly, which is
// the honest number for a service account on a volume with per-user quotas;
// totalNumberOfFreeBytes would overstate what this process can use.
func platformFreeSpace(path string) (int64, error) {
	directory, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(directory, &available, nil, nil); err != nil {
		return 0, err
	}
	if available > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(available), nil
}
