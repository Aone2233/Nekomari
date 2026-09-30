package monitoring

import (
	"bufio"
	"bytes"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	pkg_flags "github.com/Aone2233/nekomari/agent/cmd/flags"
	"github.com/shirou/gopsutil/v4/mem"
)

type RamInfo struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Mode  string
	Valid bool `json:"-"`
}

type ProcMemInfo struct {
	MemTotal     uint64
	MemFree      uint64
	MemAvailable uint64
	Buffers      uint64
	Cached       uint64
	SwapTotal    uint64
	SwapFree     uint64
	SwapCached   uint64
	Shmem        uint64
	SReclaimable uint64
	Zswap        uint64
	Zswapped     uint64
	Seen         map[string]bool
}

func (info *ProcMemInfo) hasFields(keys ...string) bool {
	for _, key := range keys {
		if !info.Seen[key] {
			return false
		}
	}
	return true
}

// readProcMeminfo reads /proc/meminfo and returns a filled ProcMemInfo struct
func ReadProcMeminfo() (*ProcMemInfo, error) {
	file, err := os.Open(procRoot() + "/meminfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info := &ProcMemInfo{Seen: make(map[string]bool)}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSuffix(parts[0], ":")
		valStr := parts[1]
		val, err := strconv.ParseUint(valStr, 10, 64)
		if err != nil {
			continue
		}
		if val > math.MaxUint64/1024 {
			continue
		}
		val *= 1024 // Convert kB to bytes
		info.Seen[key] = true

		switch key {
		case "MemTotal":
			info.MemTotal = val
		case "MemFree":
			info.MemFree = val
		case "MemAvailable":
			info.MemAvailable = val
		case "Buffers":
			info.Buffers = val
		case "Cached":
			info.Cached = val
		case "SwapTotal":
			info.SwapTotal = val
		case "SwapFree":
			info.SwapFree = val
		case "SwapCached":
			info.SwapCached = val
		case "Shmem":
			info.Shmem = val
		case "SReclaimable":
			info.SReclaimable = val
		case "Zswap":
			info.Zswap = val
		case "Zswapped":
			info.Zswapped = val
		}
	}
	return info, scanner.Err()
}

func GetMemHtopLike() RamInfo {
	raminfo := RamInfo{Mode: "htoplike"}
	if runtime.GOOS == "linux" {
		info, err := ReadProcMeminfo()
		if err == nil {
			return memHtopLikeFrom(info)
		}
	}
	return raminfo
}

func memHtopLikeFrom(info *ProcMemInfo) RamInfo {
	r := RamInfo{Mode: "htoplike"}
	if info == nil || info.MemTotal == 0 || info.MemFree > info.MemTotal || !info.hasFields("MemTotal", "MemFree", "Buffers", "Cached", "SReclaimable", "Shmem") {
		return r
	}
	deductions := info.MemFree
	for _, value := range []uint64{info.Cached, info.SReclaimable, info.Buffers} {
		if value > math.MaxUint64-deductions {
			return r
		}
		deductions += value
	}
	used := info.MemTotal - info.MemFree
	if deductions <= info.MemTotal {
		used = info.MemTotal - deductions
	}
	if info.Shmem > info.MemTotal-used {
		return r
	}
	r.Total, r.Used, r.Valid = info.MemTotal, used+info.Shmem, true
	return r
}

func GetMemGopsutil() RamInfo {
	raminfo := RamInfo{Mode: "gopsutil"}
	v, err := mem.VirtualMemory()
	if err == nil && v.Total > 0 && v.Available <= v.Total {
		raminfo.Total = v.Total
		raminfo.Used = v.Total - v.Available
		raminfo.Valid = true
	}
	return raminfo
}

// 这我还能干嘛，大伙天天说和free显示不一样，我也没办法
func CallFree() RamInfo {
	raminfo := RamInfo{Mode: "callFree"}

	// Only works on Linux/Unix systems
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		return raminfo
	}

	// Execute 'free -b' command to get memory in bytes
	cmd := exec.Command("free", "-b")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return raminfo
	}

	// Parse the output
	scanner := bufio.NewScanner(&out)
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		// Skip the header line
		if lineNum == 1 {
			continue
		}

		// Parse the "Mem:" line
		if strings.HasPrefix(line, "Mem:") {
			fields := strings.Fields(line)
			// Format: Mem: total used free shared buff/cache available
			if len(fields) >= 3 {
				total, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					raminfo.Total = total
				}

				used, err := strconv.ParseUint(fields[2], 10, 64)
				if err == nil {
					raminfo.Used = used
					raminfo.Valid = raminfo.Total > 0 && used <= raminfo.Total
				}
			}
			break
		}
	}

	return raminfo
}

func Ram() RamInfo {
	// Use global config
	if pkg_flags.GlobalConfig.MemoryIncludeCache {
		v, err := mem.VirtualMemory()
		if err != nil || v.Total == 0 || v.Free > v.Total {
			return RamInfo{}
		}
		return RamInfo{
			Total: v.Total,
			Used:  v.Total - v.Free,
			Mode:  "includeCache",
			Valid: true,
		}
	}

	if pkg_flags.GlobalConfig.MemoryReportRawUsed {
		return GetMemHtopLike()
	}

	if runtime.GOOS == "linux" {
		h := GetMemHtopLike()
		if h.Valid {
			return h
		}
	}

	// Default fallback
	return GetMemGopsutil()
}

func Swap() RamInfo {
	swapinfo := RamInfo{}

	if runtime.GOOS == "linux" {
		info, err := ReadProcMeminfo()
		if err == nil && info.hasFields("SwapTotal", "SwapFree", "SwapCached") && info.SwapFree <= info.SwapTotal {
			swapinfo.Total = info.SwapTotal
			// used = total - free - cached
			// Check for underflow
			swapinfo.Used = info.SwapTotal - info.SwapFree
			if info.SwapCached <= swapinfo.Used {
				swapinfo.Used -= info.SwapCached
			}
			swapinfo.Valid = swapinfo.Used <= swapinfo.Total
			return swapinfo
		}
	}

	s, err := mem.SwapMemory()
	if err != nil {
		return swapinfo
	}
	swapinfo.Total = s.Total
	swapinfo.Used = s.Used
	swapinfo.Valid = s.Used <= s.Total
	return swapinfo
}
