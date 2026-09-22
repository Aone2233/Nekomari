package metricstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// WritePingRecord 将 ping 记录写入 metric store
func WritePingRecord(ctx context.Context, rec models.PingRecord) error {
	if len(rec.Client) > 128 || len(rec.PingType) > 12 || len(rec.Role) > 12 {
		return fmt.Errorf("ping record exceeds field limits")
	}
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker != nil {
		return worker.enqueuePing(ctx, rec)
	}
	return writePingRecords(ctx, []models.PingRecord{rec})
}

func writePingRecords(ctx context.Context, records []models.PingRecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	if len(records) == 0 {
		return nil
	}

	points := make([]metric.Point, 0, len(records)*2)
	for _, rec := range records {
		// protocol 标签让同一任务的 icmp / tcp 两条序列在查询侧可分辨，
		// 这是「双协议并列探测」的数据基础。空值不写标签，保持与历史数据兼容。
		tags := map[string]string{"task_id": fmt.Sprintf("%d", rec.TaskId)}
		if rec.PingType != "" {
			tags["protocol"] = rec.PingType
		}
		// role 标签让「主目标」与「参考点」成为两条可分辨的序列，
		// 这就是路径归因（主机 vs 网关）能在同一张图里并列的前提。
		if rec.Role != "" {
			tags["role"] = rec.Role
		}
		loss := 0.0
		if rec.Value < 0 {
			loss = 1
		}
		points = append(points,
			metric.Point{
				MetricName: MetricPingLatency,
				EntityID:   rec.Client,
				Timestamp:  rec.Time,
				Value:      float64(rec.Value),
				Tags:       tags,
			},
			metric.Point{
				MetricName: MetricPingLoss,
				EntityID:   rec.Client,
				Timestamp:  rec.Time,
				Value:      loss,
				Tags:       tags,
			},
		)
	}
	return s.WriteBatch(ctx, points)
}

func GetPingRecords(ctx context.Context, clientUUID string, taskID int, start, end time.Time) ([]models.PingRecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	query := metric.Query{
		MetricName: MetricPingLatency,
		Start:      start,
		End:        end,
		Order:      metric.OrderAsc,
	}

	if clientUUID != "" {
		query.EntityID = clientUUID
	}

	if taskID >= 0 {
		query.Tags = map[string]string{"task_id": fmt.Sprintf("%d", taskID)}
	}

	interval := pingQueryInterval(end.Sub(start), 4000)
	interval = s.CompatibleSeriesInterval(start, time.Now().UTC(), interval)
	points, err := s.Series(ctx, metric.AggregateQuery{
		Query:          query,
		Aggregation:    metric.AggLast,
		Interval:       interval,
		PreserveSeries: true,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	return pingRecordsFromPoints(points), nil
}

// GetPingRecordsBatch 用一次 rollup 查询取回多个节点的 ping 记录。
//
// 为什么需要它：节点实时状态里的 ping 统计是按节点调 GetPingRecords 的，每次都是一遍
// Series 扫描（覆盖该节点的全部任务）。节点多起来之后，1 分钟缓存同时过期时这些扫描会
// 一起爆发。这里把所有节点放进同一个 SeriesBatch —— 指标、聚合、间隔与标签过滤都与单
// 节点版本逐字一致（这些参数与实体无关），所以每个节点的输出不变，只是每个 tier 只扫
// 一遍。返回的 map 里没有键表示该节点没有数据。
func GetPingRecordsBatch(ctx context.Context, clientUUIDs []string, taskID int, start, end time.Time) (map[string][]models.PingRecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}
	if len(clientUUIDs) == 0 {
		return map[string][]models.PingRecord{}, nil
	}

	now := time.Now().UTC()
	interval := pingQueryInterval(end.Sub(start), 4000)
	interval = s.CompatibleSeriesInterval(start, now, interval)

	query := metric.BatchSeriesQuery{
		Specs: []metric.BatchSeriesSpec{{
			MetricName:     MetricPingLatency,
			Aggregations:   []metric.Aggregation{metric.AggLast},
			Interval:       interval,
			PreserveSeries: true,
		}},
		EntityIDs: clientUUIDs,
		Start:     start,
		End:       end,
		Order:     metric.OrderAsc,
	}
	if taskID >= 0 {
		query.Tags = map[string]string{"task_id": fmt.Sprintf("%d", taskID)}
	}

	loaded, err := s.SeriesBatch(ctx, query, now)
	if err != nil {
		return nil, err
	}

	byClient := make(map[string][]models.PingRecord, len(clientUUIDs))
	for _, point := range loaded.Values[MetricPingLatency][metric.AggLast] {
		if point.EntityID == "" {
			continue
		}
		byClient[point.EntityID] = append(byClient[point.EntityID], pingRecordFromPoint(point))
	}
	for uuid, records := range byClient {
		sortPingRecordsNewestFirst(records)
		byClient[uuid] = records
	}
	return byClient, nil
}

func pingRecordsFromPoints(points []metric.AggregatePoint) []models.PingRecord {
	records := make([]models.PingRecord, 0, len(points))
	for _, p := range points {
		records = append(records, pingRecordFromPoint(p))
	}
	sortPingRecordsNewestFirst(records)
	return records
}

// pingRecordFromPoint 把一条 rollup 桶映射成 PingRecord。
// 单节点与批量两条路径共用它，避免两边的标签映射分叉。
func pingRecordFromPoint(p metric.AggregatePoint) models.PingRecord {
	taskIDVal := uint(0)
	if tid, ok := p.Tags["task_id"]; ok {
		var t uint64
		fmt.Sscanf(tid, "%d", &t)
		taskIDVal = uint(t)
	}
	return models.PingRecord{
		Client:   p.EntityID,
		TaskId:   taskIDVal,
		PingType: p.Tags["protocol"], // dual 任务靠它区分 icmp/tcp 两条序列
		Role:     p.Tags["role"],     // 路径归因靠它区分主目标/参考点
		Time:     p.Bucket.UTC(),
		Value:    int(p.Value),
	}
}

func sortPingRecordsNewestFirst(records []models.PingRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Time.After(records[j].Time)
	})
}

func pingQueryInterval(rangeDuration time.Duration, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = 4000
	}
	if rangeDuration <= 0 {
		return time.Second
	}
	interval := time.Duration((rangeDuration.Nanoseconds() + int64(maxPoints) - 1) / int64(maxPoints))
	if interval < time.Second {
		return time.Second
	}
	return metric.FloorStandardInterval(interval)
}
