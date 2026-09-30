package jsonrpc

import (
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

type latestStatusRecord struct {
	Client                string              `json:"client"`
	Time                  time.Time           `json:"time"`
	SampledAt             time.Time           `json:"sampled_at"`
	ReceivedAt            time.Time           `json:"received_at"`
	SampleIntervalSeconds float64             `json:"sample_interval_seconds"`
	CounterEpoch          string              `json:"counter_epoch"`
	Quality               map[string]string   `json:"quality"`
	Cpu                   *float64            `json:"cpu"`
	Gpu                   *float64            `json:"gpu"`
	GpuCount              int                 `json:"gpu_count,omitempty"`
	GpuAverageUsage       *float64            `json:"gpu_average_usage,omitempty"`
	GpuDetailedInfo       []v2.GPUDeviceInfo  `json:"gpu_detailed_info,omitempty"`
	Ram                   *int64              `json:"ram"`
	RamTotal              *int64              `json:"ram_total"`
	Swap                  *int64              `json:"swap"`
	SwapTotal             *int64              `json:"swap_total"`
	Load                  *float64            `json:"load"`
	Load5                 *float64            `json:"load5"`
	Load15                *float64            `json:"load15"`
	Temp                  *float64            `json:"temp"`
	Disk                  *int64              `json:"disk"`
	DiskTotal             *int64              `json:"disk_total"`
	NetIn                 *int64              `json:"net_in"`
	NetOut                *int64              `json:"net_out"`
	NetTotalUp            *int64              `json:"net_total_up"`
	NetTotalDown          *int64              `json:"net_total_down"`
	Process               *int                `json:"process"`
	Connections           *int                `json:"connections"`
	ConnectionsTCP        *int                `json:"connections_tcp"`
	ConnectionsUdp        *int                `json:"connections_udp"`
	Online                bool                `json:"online"`
	Uptime                *int64              `json:"uptime"`
	Ping                  map[string]pingStat `json:"ping"`
}

// An absent quality key denotes a legacy sample; an explicit failure is not zero.
func knownReportValue[T int | int64 | float64](rep *v2.Report, key string, value T) *T {
	if quality := rep.Quality[key]; quality != "" && quality != "ok" {
		return nil
	}
	return &value
}

func latestStatusFromReport(uuid string, rep *v2.Report, online bool, ping map[string]pingStat) latestStatusRecord {
	r := latestStatusRecord{
		Client: uuid, Time: rep.UpdatedAt, SampledAt: rep.SampledAt, ReceivedAt: rep.ReceivedAt,
		SampleIntervalSeconds: rep.SampleIntervalSeconds, CounterEpoch: rep.CounterEpoch, Quality: rep.Quality,
		Cpu: knownReportValue(rep, "cpu", rep.CPU.Usage),
		Ram: knownReportValue(rep, "ram", rep.Ram.Used), RamTotal: knownReportValue(rep, "ram", rep.Ram.Total),
		Swap: knownReportValue(rep, "swap", rep.Swap.Used), SwapTotal: knownReportValue(rep, "swap", rep.Swap.Total),
		Load: knownReportValue(rep, "load", rep.Load.Load1), Load5: knownReportValue(rep, "load", rep.Load.Load5), Load15: knownReportValue(rep, "load", rep.Load.Load15),
		Disk: knownReportValue(rep, "disk", rep.Disk.Used), DiskTotal: knownReportValue(rep, "disk", rep.Disk.Total),
		NetIn: knownReportValue(rep, "net_rate", rep.Network.Down), NetOut: knownReportValue(rep, "net_rate", rep.Network.Up),
		NetTotalUp: knownReportValue(rep, "network", rep.Network.TotalUp), NetTotalDown: knownReportValue(rep, "network", rep.Network.TotalDown),
		Process:        knownReportValue(rep, "process", rep.Process),
		Connections:    knownReportValue(rep, "connections", rep.Connections.TCP+rep.Connections.UDP),
		ConnectionsTCP: knownReportValue(rep, "connections", rep.Connections.TCP), ConnectionsUdp: knownReportValue(rep, "connections", rep.Connections.UDP),
		Online: online, Uptime: knownReportValue(rep, "uptime", rep.Uptime), Ping: ping,
	}
	if rep.GPU != nil {
		r.Gpu = knownReportValue(rep, "gpu", rep.GPU.AverageUsage)
		if r.Gpu != nil {
			r.GpuCount, r.GpuAverageUsage, r.GpuDetailedInfo = rep.GPU.Count, r.Gpu, rep.GPU.DetailedInfo
		}
	}
	return r
}
