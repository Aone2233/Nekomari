package metricstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
	logger "github.com/Aone2233/nekomari/utils/log"
)

// WritePingRecord 将 ping 记录写入 metric store
func WritePingRecord(ctx context.Context, rec models.PingRecord) error {
	if err := validatePingRecord(rec); err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("ping write requires a context")
	}
	if !reportAdmissions.TryAcquireShared() {
		return metric.ErrWriteUnavailable
	}
	defer reportAdmissions.ReleaseShared()
	if err := ctx.Err(); err != nil {
		return err
	}
	if reportClosing.Load() {
		return ErrReportBatchStopped
	}
	if GetStore() == nil {
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

// WritePingRecordSync admits a current result into the raw/hot store before
// returning. A short synchronous retry tolerates transient store contention;
// a queued writer could instead outlive the task or session admission fence.
func WritePingRecordSync(ctx context.Context, rec models.PingRecord) error {
	if err := validatePingRecord(rec); err != nil {
		return err
	}
	points := pingRecordPoints([]models.PingRecord{rec})
	if ctx == nil {
		return fmt.Errorf("ping write requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !reportAdmissions.TryAcquireShared() {
		return metric.ErrWriteUnavailable
	}
	defer reportAdmissions.ReleaseShared()
	if reportClosing.Load() {
		return ErrReportBatchStopped
	}
	if !storeOperations.TryAcquireShared() {
		return metric.ErrWriteUnavailable
	}
	defer storeOperations.ReleaseShared()
	if !storeMu.TryRLock() {
		return metric.ErrWriteUnavailable
	}
	s := store
	storeMu.RUnlock()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	return retryPinnedAppend(ctx, func() error { return s.TryAppendCurrentBatch(ctx, points) })
}

const (
	pinnedAppendRetryLimit = 100 * time.Millisecond
	pinnedAppendRetryDelay = 5 * time.Millisecond
)

// All attempts finish synchronously while the caller still owns its identity
// fences. No background writer can commit after the caller returns.
func retryPinnedAppend(ctx context.Context, appendCurrent func() error) error {
	deadline := time.Now().Add(pinnedAppendRetryLimit)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := appendCurrent()
		if !errors.Is(err, metric.ErrWriteUnavailable) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return err
		}
		timer := time.NewTimer(min(remaining, pinnedAppendRetryDelay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// PreparePinnedPingHistory primes the store's persisted-minute identity
// checks outside task/session fences. A changed store or minute simply makes
// the later nonblocking admission fail closed; it cannot admit a stale result.
func PreparePinnedPingHistory(ctx context.Context, uuid string, task models.PingTask) error {
	if ctx == nil || uuid == "" || task.Id == 0 || (task.TargetFamily != "ipv4" && task.TargetFamily != "ipv6") {
		return fmt.Errorf("ping history preparation requires a node and task")
	}
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return err
	}
	defer storeOperations.ReleaseShared()
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	type leg struct{ role, protocol, family string }
	var legs []leg
	primary := []string{task.Type}
	if task.Type == "auto" || task.Type == "dual" {
		primary = []string{"icmp", "tcp"}
	}
	for _, protocol := range primary {
		if protocol != "icmp" && protocol != "tcp" && protocol != "http" {
			return fmt.Errorf("invalid pinned probe type")
		}
		legs = append(legs, leg{protocol: protocol, family: task.TargetFamily})
	}
	if task.Reference != "" {
		protocol := task.Type
		if protocol == "auto" || protocol == "dual" {
			protocol = "icmp"
		}
		legs = append(legs, leg{role: "reference", protocol: protocol, family: "ipv4"}, leg{role: "reference", protocol: protocol, family: "ipv6"})
	}
	at := time.Now().UTC()
	// Some probes cross a minute boundary while they are in flight. Prepare
	// the current minute and two subsequent minutes; an unusually late result is
	// refused rather than doing SQL under its identity fence.
	for _, delta := range []time.Duration{0, time.Minute, 2 * time.Minute} {
		for _, leg := range legs {
			rec := models.PingRecord{Client: uuid, TaskId: task.Id, PingType: leg.protocol, Role: leg.role, Family: leg.family, Time: at.Add(delta)}
			if err := s.PrepareCurrentBatch(ctx, pingRecordPoints([]models.PingRecord{rec})); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePingRecord(rec models.PingRecord) error {
	if len(rec.Client) > 128 || len(rec.PingType) > 12 || len(rec.Role) > 12 || len(rec.Family) > 8 {
		return fmt.Errorf("ping record exceeds field limits")
	}
	return nil
}

func writePingRecords(ctx context.Context, records []models.PingRecord) error {
	if len(records) == 0 {
		return nil
	}
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return fmt.Errorf("wait for metric store operation before writing ping records: %w", err)
	}
	defer storeOperations.ReleaseShared()
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	if err := s.WriteBatch(ctx, pingRecordPoints(records)); err != nil {
		var accepted *metric.WriteAcceptedError
		if !errors.As(err, &accepted) {
			return err
		}
		logger.Errorf("metricstore", "ping batch accepted but maintenance failed: %v", err)
	}
	return nil
}

func pingRecordPoints(records []models.PingRecord) []metric.Point {
	points := make([]metric.Point, 0, len(records)*3)
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
		// family 标签让「实际走 IPv4」与「实际走 IPv6」成为两条可分辨的序列。
		// 目标是域名时由各节点自行解析，双栈域名可能落到不同族 —— 没有这个标签，
		// 两条路径的延迟与丢包会被合成一条曲线，读起来像是目标在抖。
		// 空值不写标签，与 protocol/role 一样保持与历史数据的兼容。
		if rec.Family != "" {
			tags["family"] = rec.Family
		}
		// 延迟值：新 agent 的 value_us 是同一个测量的更高精度表示，有效时以它为准
		// （420 µs → 0.42 ms，而不是被整数毫秒截成 0）。失败仍然按旧判定 —— 负值表示
		// 丢包，负的 value_us 不是有效延迟，不参与换算；loss 与延迟是两条不同的序列。
		//
		// 写入的指标/标签/序列完全不变（ping.latency_ms、ping.success_latency_ms、
		// ping.loss），变化的只是延迟点的数值从整数毫秒变成小数毫秒。
		latency := float64(rec.Value)
		loss := 0.0
		if rec.Value < 0 {
			loss = 1
		} else if rec.ValueUS > 0 {
			latency = float64(rec.ValueUS) / 1000
		}
		points = append(points,
			metric.Point{
				MetricName: MetricPingLatency,
				EntityID:   rec.Client,
				Timestamp:  rec.Time,
				Value:      latency,
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
		if rec.Value >= 0 {
			points = append(points, metric.Point{MetricName: MetricPingSuccessLatency, EntityID: rec.Client, Timestamp: rec.Time, Value: latency, Tags: tags})
		}
		if rec.Attempts > 0 {
			add := func(name string, value float64) {
				points = append(points, metric.Point{MetricName: name, EntityID: rec.Client, Timestamp: rec.Time, Value: value, Tags: tags})
			}
			add(MetricPingAttempts, float64(rec.Attempts))
			add(MetricPingSuccess, float64(rec.Success))
			add(MetricPingLossPct, rec.LossPct)
			if rec.Success > 0 {
				add(MetricPingMin, float64(rec.MinUS)/1000)
				add(MetricPingAvg, float64(rec.AvgUS)/1000)
				add(MetricPingMax, float64(rec.MaxUS)/1000)
			}
			if rec.JitterUS >= 0 && rec.Success >= 2 {
				add(MetricPingJitter, float64(rec.JitterUS)/1000)
			}
		}
		if rec.HTTP != nil {
			add := func(name string, value float64) {
				points = append(points, metric.Point{MetricName: name, EntityID: rec.Client, Timestamp: rec.Time, Value: value, Tags: tags})
			}
			h := rec.HTTP
			if h.DNSUS != nil {
				add(MetricHTTPDNS, float64(*h.DNSUS)/1000)
			}
			if h.TCPUS != nil {
				add(MetricHTTPTCP, float64(*h.TCPUS)/1000)
			}
			if h.TLSUS != nil {
				add(MetricHTTPTLS, float64(*h.TLSUS)/1000)
			}
			if h.TTFBUS != nil {
				add(MetricHTTPTTFB, float64(*h.TTFBUS)/1000)
			}
			if h.TotalUS != nil {
				add(MetricHTTPTotal, float64(*h.TotalUS)/1000)
			}
			if h.StatusCode != nil {
				add(MetricHTTPStatus, float64(*h.StatusCode))
			}
			if h.Redirects != nil {
				add(MetricHTTPRedirects, float64(*h.Redirects))
			}
			if h.TLSExpiryDays != nil {
				add(MetricHTTPTLSExpiryDays, *h.TLSExpiryDays)
			}
			if h.Reachable != nil {
				add(MetricHTTPReachable, boolValue(*h.Reachable))
			}
			if h.Available != nil {
				add(MetricHTTPAvailable, boolValue(*h.Available))
			}
		}
	}
	return points
}

func boolValue(v bool) float64 {
	if v {
		return 1
	}
	return 0
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
	// rollup 里存的是小数毫秒（0.42），整数 Value 字段会把它截成 0。把同一数值还原成
	// 微秒放进 ValueUS，前端就不会因为一次亚毫秒探测而看到「0 ms」。丢包（负值）没有
	// 微秒表示，保持 0 —— 响应里该字段随之省略。
	valueUS := int64(0)
	if p.Value >= 0 {
		valueUS = int64(math.Round(p.Value * 1000))
	}
	return models.PingRecord{
		Client:   p.EntityID,
		TaskId:   taskIDVal,
		PingType: p.Tags["protocol"], // dual 任务靠它区分 icmp/tcp 两条序列
		Role:     p.Tags["role"],     // 路径归因靠它区分主目标/参考点
		Family:   p.Tags["family"],   // 双栈域名靠它区分实际走的是 v4 还是 v6
		Time:     p.Bucket.UTC(),
		Value:    int(p.Value),
		ValueUS:  valueUS,
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
