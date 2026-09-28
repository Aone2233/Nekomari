package metricstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

// reportMetricPoints 把一次上报映射成指标点位。
//
// 流量有两个量，语义不同，各自按正确的方式映射：
//
//   - `net.total.*` 是内核自开机累计（单调）。原样存入，是唯一适合取差值的对象。
//   - `traffic.*` 是自计费周期重置日以来的累计（跨周期归零，**不是**计数器）。原样存入，
//     读取方取最后值；对它求和会得到"累计值 × 采样次数"，取差会把整个累计当成一次增量 ——
//     后者正是 41 GB 单点幻影的成因。
//
// 未配置 `--month-rotate` 的探针上报 0，此时 `traffic.*` 是零值序列，与"该节点没有周期统计"一致。
func reportMetricPoints(report v2.Report) []metric.Point {
	entityID := report.UUID
	ts := report.UpdatedAt
	points := []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: ts, Value: report.CPU.Usage},
		{MetricName: MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(report.Ram.Used)},
		{MetricName: MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(report.Swap.Used)},
		{MetricName: MetricLoad, EntityID: entityID, Timestamp: ts, Value: report.Load.Load1},
		{MetricName: MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(report.Disk.Used)},
		{MetricName: MetricNetIn, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.Down)},
		{MetricName: MetricNetOut, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.Up)},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.TotalUp)},
		{MetricName: MetricNetTotalDown, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.TotalDown)},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.CycleUp)},
		{MetricName: MetricTrafficDown, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.CycleDown)},
		{MetricName: MetricProcess, EntityID: entityID, Timestamp: ts, Value: float64(report.Process)},
		{MetricName: MetricConnections, EntityID: entityID, Timestamp: ts, Value: float64(report.Connections.TCP)},
		{MetricName: MetricConnectionsUDP, EntityID: entityID, Timestamp: ts, Value: float64(report.Connections.UDP)},
	}
	// 备份新鲜度：仅在 agent 配置了状态文件时上报，避免给未使用该功能的
	// 部署凭空写入零值序列（那样会让图表显示成「备份一直是 0 秒前」）。
	if report.Backup != nil {
		points = append(points,
			metric.Point{MetricName: MetricBackupAge, EntityID: entityID, Timestamp: ts, Value: float64(report.Backup.AgeSeconds)},
			metric.Point{MetricName: MetricBackupOK, EntityID: entityID, Timestamp: ts, Value: float64(report.Backup.Ok)},
		)
	}
	if report.GPU == nil {
		return points
	}
	points = append(points, metric.Point{MetricName: MetricGPU, EntityID: entityID, Timestamp: ts, Value: report.GPU.AverageUsage})
	for deviceIndex, gpu := range report.GPU.DetailedInfo {
		tags := map[string]string{
			"device_index": strconv.Itoa(deviceIndex),
			"device_name":  gpu.Name,
		}
		points = append(points,
			metric.Point{MetricName: MetricGPUMem, EntityID: entityID, Timestamp: ts, Value: float64(gpu.MemoryUsed), Tags: tags},
			metric.Point{MetricName: MetricGPUMemTotal, EntityID: entityID, Timestamp: ts, Value: float64(gpu.MemoryTotal), Tags: tags},
			metric.Point{MetricName: MetricGPUDeviceUsage, EntityID: entityID, Timestamp: ts, Value: gpu.Utilization, Tags: tags},
			metric.Point{MetricName: MetricGPUTemp, EntityID: entityID, Timestamp: ts, Value: float64(gpu.Temperature), Tags: tags},
		)
	}
	return points
}

func latestReportCounter(ctx context.Context, s *metric.Store, metricName, entityID string, before time.Time) (int64, bool, error) {
	point, ok, err := s.LatestBefore(ctx, metricName, entityID, before)
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, nil
	}
	return int64(point.Value), true, nil
}

// GetLatestTrafficBefore returns the latest retained upload/download counters
// before a boundary, transparently reading raw points or rollup summaries.
func GetLatestTrafficBefore(ctx context.Context, entityIDs []string, before time.Time) (map[string]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}
	result := make(map[string]models.Record, len(entityIDs))
	for _, entityID := range entityIDs {
		if entityID == "" {
			continue
		}
		up, hasUp, err := latestReportCounter(ctx, s, MetricNetTotalUp, entityID, before)
		if err != nil {
			return nil, err
		}
		down, hasDown, err := latestReportCounter(ctx, s, MetricNetTotalDown, entityID, before)
		if err != nil {
			return nil, err
		}
		if !hasUp && !hasDown {
			continue
		}
		result[entityID] = models.Record{
			Client:       entityID,
			Time:         before.UTC().Add(-time.Nanosecond),
			NetTotalUp:   up,
			NetTotalDown: down,
		}
	}
	return result, nil
}

// maxResetAwareDelta is the largest per-sample increase accepted when a
// counter appears to reset. A "reset" whose current value is larger than this
// is treated as jitter of a still-huge counter, not a wrap or reboot leftover.
const maxResetAwareDelta int64 = 64 << 30

// TrafficCounterDelta returns a reset-aware increase between two cumulative
// traffic counters. After a wrap or reboot, the current counter is the new
// increase. A tiny dip of a TB-scale counter is not a reset: counting the
// still-huge current value would inject a multi-terabyte spike.
func TrafficCounterDelta(current, previous int64) int64 {
	if current < 0 || previous < 0 {
		return 0
	}
	if current >= previous {
		return current - previous
	}
	if current > maxResetAwareDelta {
		return 0
	}
	return current
}

func deleteReportTrafficState(entityID string) {
	reportTrafficStates.Delete(entityID)
}

func clearReportTrafficStates() {
	reportTrafficStates.Range(func(key, _ any) bool {
		reportTrafficStates.Delete(key)
		return true
	})
}
