package metricstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// GetRecordsByClientAndTime 从 metric store 查询记录并重构为 models.Record
func GetRecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	return getRecordsByClientAndTimeFromSeries(ctx, s, clientUUID, start, end)
}

// GetRecordMetricMaxByClientAndTime 查询单项监控指标在各时间桶内的最大值。
func GetRecordMetricMaxByClientAndTime(ctx context.Context, clientUUID, recordMetric string, start, end time.Time) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	return getRecordMetricMaxByClientAndTimeFromSeries(ctx, s, clientUUID, recordMetric, start, end)
}

// GetRecordsByTime 从 metric store 查询所有客户端在时间范围内的记录
func GetRecordsByTime(ctx context.Context, start, end time.Time) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	interval := recordSeriesInterval(s, start, end, time.Now().UTC())
	entityIDs, err := listRecordEntityIDs(ctx, s, start, end, interval)
	if err != nil {
		return nil, err
	}
	return getRecordsForEntitiesFromSeries(ctx, s, entityIDs, start, end)
}

type recordSeriesKey struct {
	client string
	ts     int64
}

// recordBatchSpecs 构造重建记录所需的全部指标规格。
//
// 每个指标用一次 SeriesBatch 里的一条 spec 表达：SeriesBatch 会把它们按分辨率
// 合并成每个 tier 一次扫描，而不是每个指标扫一遍。
func recordBatchSpecs(interval time.Duration) []metric.BatchSeriesSpec {
	specs := make([]metric.BatchSeriesSpec, 0, len(loadRecordMetricNames))
	for _, metricName := range loadRecordMetricNames {
		specs = append(specs, metric.BatchSeriesSpec{
			MetricName:   metricName,
			Aggregations: []metric.Aggregation{recordMetricAggregation(metricName)},
			Interval:     interval,
			// 必须保留各实体自己的桶：一次查询可能覆盖整个机群，不保留就会把
			// 不同节点同一时间桶的数据合并成一条记录。
			PreserveSeries: true,
		})
	}
	return specs
}

// getRecordsForEntitiesFromSeries 用一次批量 rollup 查询重建若干节点的记录。
//
// 旧实现是「每节点 × 每指标」各一次 Series：全机群一次请求就是
// 节点数 × 17 次查询（9 节点 = 153 次），而且整个请求期间占着一个查询槽。
// SeriesBatch 把同样的指标合并成每个分辨率一次扫描，只占一个槽。
func getRecordsForEntitiesFromSeries(ctx context.Context, s *metric.Store, entityIDs []string, start, end time.Time) ([]models.Record, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	loaded, err := s.SeriesBatch(ctx, metric.BatchSeriesQuery{
		Specs:     recordBatchSpecs(recordSeriesInterval(s, start, end, now)),
		EntityIDs: entityIDs,
		Start:     start,
		End:       end,
		Order:     metric.OrderAsc,
	}, now)
	if err != nil {
		return nil, fmt.Errorf("failed to query record metrics: %w", err)
	}

	recordMap := make(map[recordSeriesKey]*models.Record)
	for _, metricName := range loadRecordMetricNames {
		points := loaded.Values[metricName][recordMetricAggregation(metricName)]
		for _, point := range points {
			entityID := point.EntityID
			if entityID == "" {
				// 单实体查询时兜底，多实体时缺 EntityID 无法归属，只能跳过。
				if len(entityIDs) != 1 {
					continue
				}
				entityID = entityIDs[0]
			}
			key := recordSeriesKey{client: entityID, ts: point.Bucket.Unix()}
			if recordMap[key] == nil {
				recordMap[key] = &models.Record{
					Client: entityID,
					Time:   point.Bucket.UTC(),
				}
			}
			applyRecordMetricValue(recordMap[key], metricName, point.Value)
		}
	}

	records := make([]models.Record, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, *rec)
	}
	sortRecords(records)
	return records, nil
}

func getRecordsByClientAndTimeFromSeries(ctx context.Context, s *metric.Store, clientUUID string, start, end time.Time) ([]models.Record, error) {
	return getRecordsForEntitiesFromSeries(ctx, s, []string{clientUUID}, start, end)
}

func getRecordMetricMaxByClientAndTimeFromSeries(ctx context.Context, s *metric.Store, clientUUID, recordMetric string, start, end time.Time) ([]models.Record, error) {
	metricName, ok := metricNameForRecordField(recordMetric)
	if !ok {
		return nil, fmt.Errorf("unsupported record metric %q", recordMetric)
	}

	now := time.Now().UTC()
	points, err := s.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: metricName,
			EntityID:   clientUUID,
			Start:      start,
			End:        end,
			Order:      metric.OrderAsc,
		},
		Aggregation: metric.AggMax,
		Interval:    recordSeriesInterval(s, start, end, now),
	}, now)
	if err != nil {
		return nil, fmt.Errorf("failed to query metric %s: %w", metricName, err)
	}

	records := make([]models.Record, 0, len(points))
	for _, point := range points {
		entityID := point.EntityID
		if entityID == "" {
			entityID = clientUUID
		}
		record := models.Record{Client: entityID, Time: point.Bucket.UTC()}
		applyRecordMetricValue(&record, metricName, point.Value)
		records = append(records, record)
	}
	return records, nil
}

func recordMetricAggregation(metricName string) metric.Aggregation {
	switch metricName {
	case MetricTrafficUp, MetricTrafficDown:
		return metric.AggSum
	case MetricNetTotalUp, MetricNetTotalDown:
		return metric.AggLast
	default:
		return metric.AggAvg
	}
}

func recordSeriesInterval(s *metric.Store, start, end, now time.Time) time.Duration {
	interval := recordDownsampleInterval(end.Sub(start), 500)
	return s.CompatibleSeriesInterval(start, now, interval)
}

func recordDownsampleInterval(rangeDuration time.Duration, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = 500
	}
	nanos := rangeDuration.Nanoseconds()
	if nanos <= 0 {
		return time.Second
	}
	interval := time.Duration((nanos + int64(maxPoints) - 1) / int64(maxPoints))
	if interval < time.Second {
		return time.Second
	}
	return metric.FloorStandardInterval(interval)
}

func listRecordEntityIDs(ctx context.Context, s *metric.Store, start, end time.Time, interval time.Duration) ([]string, error) {
	seen := make(map[string]struct{})
	for _, metricName := range loadRecordMetricNames {
		ids, err := s.EntityIDs(ctx, metric.Query{
			MetricName: metricName,
			Start:      start.Add(-interval),
			End:        end,
		})
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func applyRecordMetricValue(rec *models.Record, metricName string, value float64) {
	switch metricName {
	case MetricCPU:
		rec.Cpu = float32(value)
	case MetricGPU:
		rec.Gpu = float32(value)
	case MetricRAM:
		rec.Ram = int64(value)
	case MetricSwap:
		rec.Swap = int64(value)
	case MetricLoad:
		rec.Load = float32(value)
	case MetricDisk:
		rec.Disk = int64(value)
	case MetricNetIn:
		rec.NetIn = int64(value)
	case MetricNetOut:
		rec.NetOut = int64(value)
	case MetricNetTotalUp:
		rec.NetTotalUp = int64(value)
	case MetricNetTotalDown:
		rec.NetTotalDown = int64(value)
	case MetricTrafficUp:
		rec.TrafficUp = int64(value)
	case MetricTrafficDown:
		rec.TrafficDown = int64(value)
	case MetricProcess:
		rec.Process = int(value)
	case MetricConnections:
		rec.Connections = int(value)
	case MetricConnectionsUDP:
		rec.ConnectionsUdp = int(value)
	}
}

func sortRecords(records []models.Record) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Client != records[j].Client {
			return records[i].Client < records[j].Client
		}
		return records[i].Time.Before(records[j].Time)
	})
}

// GetGPURecordsByClientAndTime 从 metric store 查询 GPU 记录
func GetGPURecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time) ([]models.GPURecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	// 查询 GPU 相关指标（每设备利用率使用独立指标 gpu.device.usage）
	gpuMetrics := []string{MetricGPUDeviceUsage, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp}

	// 按设备索引和时间组织数据
	type gpuKey struct {
		deviceIndex int
		timestamp   int64
	}
	recordMap := make(map[gpuKey]*models.GPURecord)

	for _, metricName := range gpuMetrics {
		points, err := s.Query(ctx, metric.Query{
			MetricName: metricName,
			EntityID:   clientUUID,
			Start:      start,
			End:        end,
			Order:      metric.OrderAsc,
		})
		if err != nil {
			continue // GPU 数据可能不存在
		}

		for _, p := range points {
			deviceIndex := 0
			deviceName := ""
			if idx, ok := p.Tags["device_index"]; ok {
				fmt.Sscanf(idx, "%d", &deviceIndex)
			}
			if name, ok := p.Tags["device_name"]; ok {
				deviceName = name
			}

			key := gpuKey{deviceIndex: deviceIndex, timestamp: p.Timestamp.Unix()}
			if recordMap[key] == nil {
				recordMap[key] = &models.GPURecord{
					Client:      clientUUID,
					Time:        p.Timestamp.UTC(),
					DeviceIndex: deviceIndex,
					DeviceName:  deviceName,
				}
			}
			rec := recordMap[key]

			switch metricName {
			case MetricGPUDeviceUsage:
				rec.Utilization = float32(p.Value)
			case MetricGPUMem:
				rec.MemUsed = int64(p.Value)
			case MetricGPUMemTotal:
				rec.MemTotal = int64(p.Value)
			case MetricGPUTemp:
				rec.Temperature = int(p.Value)
			}
		}
	}

	// 转换为切片
	records := make([]models.GPURecord, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, *rec)
	}

	return records, nil
}

// GetPingRecords 从 metric store 查询兼容旧接口的 ping 记录。
//
// 旧接口过去直接读取 ping_records。这里使用与 queryMetrics 相同的 Series
