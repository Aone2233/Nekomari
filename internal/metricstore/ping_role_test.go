package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// 路径归因的数据基础：同一任务、同一时刻的「主目标」与「参考点」必须落成
// 两条可分辨的序列，否则参考点会把主目标的曲线覆盖掉。
func TestWritePingRecordsKeepsRolesDistinct(t *testing.T) {
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

	// 主目标延迟尖峰 320ms，同时参考点（网关）正常 2ms
	records := []models.PingRecord{
		{Client: "node-a", TaskId: 11, PingType: "icmp", Value: 320, Time: now},
		{Client: "node-a", TaskId: 11, PingType: "icmp", Role: "reference", Value: 2, Time: now},
	}
	if err := writePingRecords(ctx, records); err != nil {
		t.Fatalf("write ping records: %v", err)
	}

	points, err := s.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: MetricPingLatency,
			EntityID:   "node-a",
			Start:      now.Add(-time.Minute),
			End:        now.Add(time.Minute),
			Tags:       map[string]string{"task_id": "11"},
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
		if p.Tags["task_id"] != "11" {
			continue
		}
		seen[p.Tags["role"]] = int(p.Value)
	}
	if len(seen) != 2 {
		t.Fatalf("期望主目标/参考点两条序列，实际 %d 条: %#v", len(seen), seen)
	}
	if v := seen[""]; v != 320 {
		t.Errorf("主目标应为 320，实际 %#v", seen)
	}
	if v := seen["reference"]; v != 2 {
		t.Errorf("参考点应为 2，实际 %#v", seen)
	}

	// 读取侧也应带回 role，供调用方区分
	got, err := GetPingRecords(ctx, "node-a", 11, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("get ping records: %v", err)
	}
	roles := map[string]bool{}
	for _, r := range got {
		roles[r.Role] = true
	}
	if !roles[""] || !roles["reference"] {
		t.Fatalf("GetPingRecords 未带回两种角色: %#v", roles)
	}
}

// 未设置 reference 时不应产生 role 标签，保持与历史数据/旧探针兼容。
func TestWritePingRecordsOmitsEmptyRole(t *testing.T) {
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

	if err := writePingRecords(ctx, []models.PingRecord{
		{Client: "node-c", TaskId: 4, Value: 9, Time: now},
	}); err != nil {
		t.Fatalf("write ping records: %v", err)
	}
	points, err := s.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: MetricPingLatency,
			EntityID:   "node-c",
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
		if _, ok := p.Tags["role"]; ok {
			t.Fatalf("空 role 不应写入 role 标签: %#v", p.Tags)
		}
	}
}
