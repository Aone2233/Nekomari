package metricstore

import (
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestReportMetricPointsAddHostProbeTruthMetrics(t *testing.T) {
	available, cached, buffers, zswap := int64(800), int64(500), int64(50), int64(20)
	cgUsage, cgQuota, cgThrottled := 12.5, 2.0, 3.5
	tcpOut, tcpRetrans := uint64(1000), uint64(10)
	pct := 1.0
	report := v2.Report{
		UUID:      "node-a",
		UpdatedAt: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
		CPU: v2.CPUReport{
			Usage: 25, IntervalSeconds: 5,
			Cgroup: &v2.CgroupCPUReport{UsagePercent: &cgUsage, QuotaCores: &cgQuota, ThrottledSecondsTotal: &cgThrottled},
		},
		Ram: v2.RamReport{Total: 1000, Used: 400, Available: &available, Cached: &cached, Buffers: &buffers, Zswap: &zswap},
		Disk: v2.DiskReport{
			Total: 2000, Used: 500,
			Mounts: []v2.DiskMountReport{{Device: "/dev/sda1", Mountpoint: "/", FSType: "ext4", Total: 2000, Used: 500}},
		},
		Network: v2.NetworkReport{TCPOutSegs: &tcpOut, TCPRetransSegs: &tcpRetrans, TCPRetransPercent: &pct},
		Quality: map[string]string{"cpu": "ok", "cgroup": "ok", "ram": "ok", "disk": "ok", "network": "ok", "net_rate": "ok"},
	}
	points := reportMetricPoints(report)
	byName := map[string]float64{}
	for _, p := range points {
		byName[p.MetricName] = p.Value
	}
	want := map[string]float64{
		MetricCPUInterval:        5,
		MetricCPUCgroupUsage:     12.5,
		MetricCPUCgroupQuota:     2,
		MetricCPUCgroupThrottled: 3.5,
		MetricRAMAvailable:       800,
		MetricRAMCached:          500,
		MetricRAMBuffers:         50,
		MetricRAMZswap:           20,
		MetricDiskMountUsed:      500,
		MetricDiskMountTotal:     2000,
		MetricTCPOutSegs:         1000,
		MetricTCPRetransSegs:     10,
		MetricTCPRetransPct:      1,
	}
	for name, value := range want {
		if got, ok := byName[name]; !ok || got != value {
			t.Errorf("%s = %v (present=%v), want %v", name, got, ok, value)
		}
	}
}

func TestReportMetricPointsDoesNotInventHostDetail(t *testing.T) {
	report := v2.Report{
		UUID:      "node-a",
		UpdatedAt: time.Now().UTC(),
		Quality:   map[string]string{"cpu": "unknown", "cgroup": "unknown", "ram": "unknown", "disk": "unknown", "network": "unknown", "net_rate": "unknown"},
	}
	points := reportMetricPoints(report)
	for _, p := range points {
		switch p.MetricName {
		case MetricCPUInterval, MetricCPUCgroupUsage, MetricRAMAvailable, MetricDiskMountUsed, MetricTCPOutSegs, MetricTCPRetransPct:
			t.Errorf("unknown host detail was written as %s=%v", p.MetricName, p.Value)
		}
	}
}
