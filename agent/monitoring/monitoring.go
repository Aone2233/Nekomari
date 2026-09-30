package monitoring

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	pkg_flags "github.com/Aone2233/nekomari/agent/cmd/flags"
	unit "github.com/Aone2233/nekomari/agent/monitoring/unit"
)

var flags = pkg_flags.GlobalConfig

type report struct {
	CPU         cpuReport         `json:"cpu"`
	Ram         usageReport       `json:"ram"`
	Swap        usageReport       `json:"swap"`
	Load        loadReport        `json:"load"`
	Disk        usageReport       `json:"disk"`
	Network     networkReport     `json:"network"`
	Connections connectionsReport `json:"connections"`
	GPU         interface{}       `json:"gpu,omitempty"`
	Uptime      uint64            `json:"uptime"`
	Process     int               `json:"process"`
	// Backup 仅在配置了 --backup-status-file 时填充，否则为 nil（omitempty）。
	Backup                *backupReport     `json:"backup,omitempty"`
	Message               string            `json:"message"`
	SampledAt             time.Time         `json:"sampled_at"`
	SampleIntervalSeconds float64           `json:"sample_interval_seconds"`
	CounterEpoch          string            `json:"counter_epoch,omitempty"`
	Quality               map[string]string `json:"quality"`
}

type cpuReport struct {
	Usage float64 `json:"usage"`
}

type usageReport struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

type loadReport struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type networkReport struct {
	Up        uint64 `json:"up"`
	Down      uint64 `json:"down"`
	TotalUp   uint64 `json:"totalUp"`
	TotalDown uint64 `json:"totalDown"`
	// CycleUp / CycleDown 是自**计费周期重置日**以来的累计流量（`--month-rotate`），
	// 与 TotalUp/TotalDown 的语义不同 —— 后者是内核自开机累计，单调，可以取差值。
	//
	// 分成两组字段是本轮修复的核心：两者原先共用一个字段，下游按内核计数器做增量时就会拿
	// 一种口径的基线去减另一种口径的当前值，进而把整个周期累计当成一次增量。
	// 现场证据：CLISP（`--month-rotate 9`）记录过两次 41 GB 的单点读数，而该网卡自开机
	// 累计仅 0.29 GB。详见 unit.NetworkSpeed 的说明。
	//
	// 未配置 `--month-rotate` 时为 0。
	CycleUp   uint64 `json:"cycleUp,omitempty"`
	CycleDown uint64 `json:"cycleDown,omitempty"`
}

type connectionsReport struct {
	TCP int `json:"tcp"`
	UDP int `json:"udp"`
}

type gpuModelsReport struct {
	Models []string `json:"models"`
}

type gpuReport struct {
	Count        int               `json:"count"`
	AverageUsage float64           `json:"average_usage"`
	DetailedInfo []gpuDeviceReport `json:"detailed_info"`
}

type gpuDeviceReport struct {
	Name        string  `json:"name"`
	MemoryTotal uint64  `json:"memory_total"`
	MemoryUsed  uint64  `json:"memory_used"`
	Utilization float64 `json:"utilization"`
	Temperature uint64  `json:"temperature"`
}

func GenerateReport() []byte {
	message := ""
	data := report{SampleIntervalSeconds: flags.Interval, Quality: make(map[string]string)}
	mark := func(key string, valid bool) {
		data.Quality[key] = "unknown"
		if valid {
			data.Quality[key] = "ok"
		}
	}

	cpu := unit.Cpu()
	cpuUsage := cpu.CPUUsage
	if !cpu.Valid {
		cpuUsage = 0
	}
	data.CPU = cpuReport{Usage: cpuUsage}
	mark("cpu", cpu.Valid)

	ram := unit.Ram()
	data.Ram = usageReport{Total: ram.Total, Used: ram.Used}
	mark("ram", ram.Valid)
	data.Quality["ram_mode"] = ram.Mode

	swap := unit.Swap()
	data.Swap = usageReport{Total: swap.Total, Used: swap.Used}
	mark("swap", swap.Valid)
	load := unit.Load()
	data.Load = loadReport{Load1: load.Load1, Load5: load.Load5, Load15: load.Load15}
	mark("load", load.Valid)

	disk := unit.Disk()
	data.Disk = usageReport{Total: disk.Total, Used: disk.Used}
	mark("disk", disk.Valid)

	totalUp, totalDown, cycleUp, cycleDown, networkUp, networkDown, err := unit.NetworkSpeed()
	if err != nil {
		message += fmt.Sprintf("failed to get network speed: %v\n", err)
	}
	data.Network = networkReport{
		Up: networkUp, Down: networkDown,
		TotalUp: totalUp, TotalDown: totalDown,
		CycleUp: cycleUp, CycleDown: cycleDown,
	}
	sampledAt, epoch, rateValid := unit.NetworkSampleMetadata()
	data.SampledAt = sampledAt
	data.CounterEpoch = epoch
	mark("network", !sampledAt.IsZero())
	mark("net_rate", rateValid)
	mark("traffic_cycle", err == nil && flags.MonthRotate != 0 && !sampledAt.IsZero())
	if data.SampledAt.IsZero() {
		data.SampledAt = time.Now().UTC()
	}

	tcpCount, udpCount, err := unit.ConnectionsCount()
	if err != nil {
		message += fmt.Sprintf("failed to get connections: %v\n", err)
	}
	data.Connections = connectionsReport{TCP: tcpCount, UDP: udpCount}
	mark("connections", err == nil)

	uptime, err := unit.Uptime()
	if err != nil {
		message += fmt.Sprintf("failed to get uptime: %v\n", err)
	}
	data.Uptime = uptime
	mark("uptime", err == nil)

	data.Process = unit.ProcessCount()
	mark("process", data.Process > 0)

	// 备份新鲜度（可选）：未配置状态文件时不上报，避免写入无意义的零值序列。
	if path := strings.TrimSpace(flags.BackupStatusFile); path != "" {
		age, ok, msg := CollectBackupStatus(path, time.Now())
		data.Backup = &backupReport{AgeSeconds: age, Ok: ok, Message: msg}
		if ok == 0 && msg != "" {
			message += fmt.Sprintf("backup status: %s\n", msg)
		}
	}

	// GPU监控 - 根据标志决定详细程度
	if flags.EnableGPU {
		mark("gpu", false)
		// 详细GPU监控模式
		gpuInfo, err := unit.GetDetailedGPUInfo()
		if err != nil {
			message += fmt.Sprintf("failed to get detailed GPU info: %v\n", err)
			// 降级到基础GPU信息
			gpuNames, nameErr := unit.GetDetailedGPUHost()
			if nameErr == nil && len(gpuNames) > 0 {
				data.GPU = gpuModelsReport{Models: gpuNames}
			}
		} else if len(gpuInfo) > 0 {
			// 成功获取详细信息
			gpuData := make([]gpuDeviceReport, len(gpuInfo))
			totalGPUUsage := 0.0

			for i, info := range gpuInfo {
				gpuData[i] = gpuDeviceReport{
					Name:        info.Name,
					MemoryTotal: info.MemoryTotal,
					MemoryUsed:  info.MemoryUsed,
					Utilization: info.Utilization,
					Temperature: info.Temperature,
				}
				totalGPUUsage += info.Utilization
			}

			avgGPUUsage := totalGPUUsage / float64(len(gpuInfo))
			data.GPU = gpuReport{Count: len(gpuInfo), AverageUsage: avgGPUUsage, DetailedInfo: gpuData}
			mark("gpu", true)
		}
	}
	// 基础模式下，GPU信息已在basicInfo中处理

	data.Message = message

	s, err := json.Marshal(data)
	if err != nil {
		log.Println("Failed to marshal data:", err)
	}
	return s
}
