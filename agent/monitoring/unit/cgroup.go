package monitoring

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CgroupCPUStats is the additive cgroup view. Nil means the value was not
// available; zero is a real measurement (for example, no throttling).
type CgroupCPUStats struct {
	UsagePercent          *float64
	QuotaCores            *float64
	ThrottledSecondsTotal *float64
}

var cgroupUsageState struct {
	sync.Mutex
	lastUsage uint64
	lastAt    time.Time
	ready     bool
}

func cgroupRoot() string {
	if _, err := os.Stat("/sys/fs/cgroup/cpu.max"); err == nil {
		return "/sys/fs/cgroup"
	}
	for _, candidate := range []string{"/sys/fs/cgroup/cpu,cpuacct", "/sys/fs/cgroup/cpu"} {
		if _, err := os.Stat(filepath.Join(candidate, "cpu.stat")); err == nil {
			return candidate
		}
	}
	return ""
}

func CgroupCPU() *CgroupCPUStats {
	root := cgroupRoot()
	if root == "" {
		return nil
	}
	out := &CgroupCPUStats{}
	now := time.Now()

	// cgroup v2 quota: "max 100000" or "50000 100000".
	if data, err := os.ReadFile(filepath.Join(root, "cpu.max")); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 && fields[0] != "max" {
			quota, qErr := strconv.ParseFloat(fields[0], 64)
			period, pErr := strconv.ParseFloat(fields[1], 64)
			if qErr == nil && pErr == nil && period > 0 {
				cores := quota / period
				out.QuotaCores = &cores
			}
		}
	}
	// cgroup v1 quota.
	if out.QuotaCores == nil {
		quotaData, qErr := os.ReadFile(filepath.Join(root, "cpu.cfs_quota_us"))
		periodData, pErr := os.ReadFile(filepath.Join(root, "cpu.cfs_period_us"))
		if qErr == nil && pErr == nil {
			quota, qConv := strconv.ParseFloat(strings.TrimSpace(string(quotaData)), 64)
			period, pConv := strconv.ParseFloat(strings.TrimSpace(string(periodData)), 64)
			if qConv == nil && pConv == nil && quota > 0 && period > 0 {
				cores := quota / period
				out.QuotaCores = &cores
			}
		}
	}

	if data, err := os.ReadFile(filepath.Join(root, "cpu.stat")); err == nil {
		var usageUS, throttledUS uint64
		var haveUsage, haveThrottled bool
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			value, convErr := strconv.ParseUint(fields[1], 10, 64)
			if convErr != nil {
				continue
			}
			switch fields[0] {
			case "usage_usec":
				usageUS, haveUsage = value, true
			case "throttled_usec":
				throttledUS, haveThrottled = value, true
			case "throttled_time": // cgroup v1 reports nanoseconds
				throttledUS, haveThrottled = value/1000, true
			}
		}
		if haveThrottled {
			seconds := float64(throttledUS) / 1e6
			out.ThrottledSecondsTotal = &seconds
		}
		if haveUsage {
			cgroupUsageState.Lock()
			if cgroupUsageState.ready {
				delta := usageUS - cgroupUsageState.lastUsage
				elapsed := now.Sub(cgroupUsageState.lastAt).Seconds()
				if delta > 0 && elapsed > 0 {
					percent := float64(delta) / 1e6 / elapsed * 100
					if !math.IsNaN(percent) && !math.IsInf(percent, 0) && percent >= 0 {
						out.UsagePercent = &percent
					}
				}
			}
			cgroupUsageState.lastUsage, cgroupUsageState.lastAt = usageUS, now
			cgroupUsageState.ready = true
			cgroupUsageState.Unlock()
		}
	}
	if out.UsagePercent == nil && out.QuotaCores == nil && out.ThrottledSecondsTotal == nil {
		return nil
	}
	return out
}
