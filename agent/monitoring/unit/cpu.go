package monitoring

import (
	"bufio"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	pkg_flags "github.com/Aone2233/nekomari/agent/cmd/flags"
	"github.com/shirou/gopsutil/v4/cpu"
)

var flags = pkg_flags.GlobalConfig

type CpuInfo struct {
	CPUName          string  `json:"cpu_name"`
	CPUArchitecture  string  `json:"cpu_architecture"`
	CPUCores         int     `json:"cpu_cores"`
	CPUPhysicalCores int     `json:"cpu_physical_cores"`
	CPUUsage         float64 `json:"cpu_usage"`
	IntervalSeconds  float64 `json:"interval_seconds,omitempty"`
	Valid            bool    `json:"-"`
}

var cpuIntervalState struct {
	sync.Mutex
	lastTotal uint64
	lastIdle  uint64
	lastAt    time.Time
	ready     bool
}

// cpuIntervalSample computes host CPU usage from /proc/stat deltas. The first
// call only establishes a baseline: a percentage without a known interval is
// unknown, not zero.
func cpuIntervalSample() (usage, interval float64, ok bool) {
	file, err := os.Open(procRoot() + "/stat")
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var total, idle uint64
	for i, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += value
		if i == 3 || i == 4 { // idle + iowait
			idle += value
		}
	}
	now := time.Now()
	cpuIntervalState.Lock()
	defer cpuIntervalState.Unlock()
	if !cpuIntervalState.ready {
		cpuIntervalState.lastTotal, cpuIntervalState.lastIdle = total, idle
		cpuIntervalState.lastAt, cpuIntervalState.ready = now, true
		return 0, 0, false
	}
	totalDelta := total - cpuIntervalState.lastTotal
	idleDelta := idle - cpuIntervalState.lastIdle
	elapsed := now.Sub(cpuIntervalState.lastAt).Seconds()
	cpuIntervalState.lastTotal, cpuIntervalState.lastIdle = total, idle
	cpuIntervalState.lastAt = now
	if totalDelta == 0 || elapsed <= 0 {
		return 0, 0, false
	}
	usage = (1 - float64(idleDelta)/float64(totalDelta)) * 100
	if math.IsNaN(usage) || math.IsInf(usage, 0) || usage < 0 || usage > 100 {
		return 0, 0, false
	}
	return usage, elapsed, true
}

func Cpu() CpuInfo {
	cpuinfo := CpuStaticInfo()

	if runtime.GOOS == "linux" {
		if usage, interval, ok := cpuIntervalSample(); ok {
			cpuinfo.CPUUsage = usage
			cpuinfo.IntervalSeconds = interval
			cpuinfo.Valid = true
			return cpuinfo
		}
		// First Linux sample only establishes the baseline; an unqualified
		// percentage is unknown, not zero.
		cpuinfo.Valid = false
		return cpuinfo
	}

	percentages, err := cpu.Percent(0, false)
	if err == nil && len(percentages) > 0 {
		cpuinfo.CPUUsage = percentages[0]
		cpuinfo.IntervalSeconds = flags.Interval
		cpuinfo.Valid = !math.IsNaN(percentages[0]) && !math.IsInf(percentages[0], 0) && percentages[0] >= 0 && percentages[0] <= 100
	}

	return cpuinfo
}

func CpuStaticInfo() CpuInfo {
	cpuinfo := CpuInfo{
		CPUName:          "Unknown",
		CPUArchitecture:  runtime.GOARCH,
		CPUCores:         1,
		CPUPhysicalCores: 0, // 为兼容旧版 agent，0 表示未上报或未知，避免与实际核心数混淆
		CPUUsage:         0.0,
	}

	// 优先使用 gopsutil 获取 CPU 信息，避免触发 lscpu 在部分内核上的 lockdown 日志刷屏。
	info, err := cpu.Info()
	if err == nil && len(info) > 0 {
		cpuinfo.CPUName = strings.TrimSpace(info[0].ModelName)
		if cpuinfo.CPUName == "" {
			if info[0].VendorID != "" || info[0].Family != "" {
				cpuinfo.CPUName = strings.TrimSpace(info[0].VendorID + " " + info[0].Family)
			}
		}
	}

	if cpuinfo.CPUName == "Unknown" {
		name, err := readCPUNameFromProc()
		if err == nil && name != "" {
			cpuinfo.CPUName = strings.TrimSpace(name)
		}
	}

	cores, err := cpu.Counts(true)
	if err == nil && cores > 0 {
		cpuinfo.CPUCores = cores
	}

	physicalCores, err := cpu.Counts(false)
	if err == nil && physicalCores > 0 {
		cpuinfo.CPUPhysicalCores = physicalCores
	}

	return cpuinfo
}

// readCPUNameFromProc 从 /proc/cpuinfo 读取 CPU 名称
func readCPUNameFromProc() (string, error) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Model\t") || strings.HasPrefix(line, "Hardware\t") || strings.HasPrefix(line, "Processor\t") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}

	return "", scanner.Err()
}
