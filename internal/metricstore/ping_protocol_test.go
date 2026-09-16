package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// 「双协议并列探测」的数据基础：同一任务、同一时刻的 icmp 与 tcp 结果
// 必须落成两条可分辨的序列，而不是互相覆盖。
func TestWritePingRecordsKeepsProtocolsDistinct(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer s.Close()

	for _, name := range []string{MetricPingLatency, MetricPingLoss} {
		if err := s.UpsertMetric(ctx, metric.Definition{
			Name: name, Type: metric.TypeGauge, RetentionDays: 30,
		}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	storeMu.Lock()
	oldStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		storeMu.Unlock()
	}()

	// 同一任务的同一时刻：ICMP 丢包(-1) 与 TCP 正常(2ms)
	records := []models.PingRecord{
		{Client: "node-a", TaskId: 9, PingType: "icmp", Value: -1, Time: now},
		{Client: "node-a", TaskId: 9, PingType: "tcp", Value: 2, Time: now},
	}
	if err := writePingRecords(ctx, records); err != nil {
		t.Fatalf("write ping records: %v", err)
	}

	// 查询侧必须给出 protocol 标签不同的两条序列
	points, err := s.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: MetricPingLatency,
			EntityID:   "node-a",
			Start:      now.Add(-time.Minute),
			End:        now.Add(time.Minute),
			Tags:       map[string]string{"task_id": "9"},
		},
		Aggregation:    metric.AggLast,
		Interval:       time.Second,
		PreserveSeries: true,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("query series: %v", err)
	}

	seen := map[string]int{}
	for _, p := range points {
		if p.Tags["task_id"] != "9" {
			continue
		}
		seen[p.Tags["protocol"]] = int(p.Value)
	}
	if len(seen) != 2 {
		t.Fatalf("期望 icmp/tcp 两条可分辨序列，实际 %d 条: %#v", len(seen), seen)
	}
	if v, ok := seen["icmp"]; !ok || v != -1 {
		t.Errorf("icmp 序列值应为 -1(丢包)，实际 %#v", seen)
	}
	if v, ok := seen["tcp"]; !ok || v != 2 {
		t.Errorf("tcp 序列值应为 2，实际 %#v", seen)
	}

	// 读取侧也应带回协议，否则消费方无法区分
	got, err := GetPingRecords(ctx, "node-a", 9, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("get ping records: %v", err)
	}
	types := map[string]bool{}
	for _, r := range got {
		types[r.PingType] = true
	}
	if !types["icmp"] || !types["tcp"] {
		t.Fatalf("GetPingRecords 未带回两种协议: %#v", types)
	}
}

// 不写协议时不应产生 protocol 标签，保证与历史数据/旧探针兼容。
func TestWritePingRecordsOmitsEmptyProtocol(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer s.Close()
	// writePingRecords 每条记录会写 latency + loss 两个指标，两个都要先定义
	for _, name := range []string{MetricPingLatency, MetricPingLoss} {
		if err := s.UpsertMetric(ctx, metric.Definition{
			Name: name, Type: metric.TypeGauge, RetentionDays: 30,
		}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	storeMu.Lock()
	oldStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		storeMu.Unlock()
	}()

	if err := writePingRecords(ctx, []models.PingRecord{
		{Client: "node-b", TaskId: 3, Value: 7, Time: now}, // 旧探针不带 PingType
	}); err != nil {
		t.Fatalf("write ping records: %v", err)
	}

	points, err := s.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: MetricPingLatency,
			EntityID:   "node-b",
			Start:      now.Add(-time.Minute),
			End:        now.Add(time.Minute),
		},
		Aggregation:    metric.AggLast,
		Interval:       time.Second,
		PreserveSeries: true,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("query series: %v", err)
	}
	if len(points) == 0 {
		t.Fatal("应至少写入一个点")
	}
	for _, p := range points {
		if _, ok := p.Tags["protocol"]; ok {
			t.Fatalf("空协议不应写入 protocol 标签: %#v", p.Tags)
		}
	}
}
