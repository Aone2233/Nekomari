package metricstore

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// 打开一个内存 metric store 并安装为包级 store（与既有 ping 用例同一套做法）。
func openPingTestStore(t *testing.T) *metric.Store {
	t.Helper()
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, name := range pingMetricNames {
		if err := s.UpsertMetric(ctx, metric.Definition{
			Name: name, Type: metric.TypeGauge, RetentionDays: 30,
		}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}
	storeMu.Lock()
	previous := store
	store = s
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		store = previous
		storeMu.Unlock()
	})
	return s
}

// lastValuesByProtocol 把某条指标在窗口内的最后一个点按 protocol 标签取值。
func lastValuesByProtocol(t *testing.T, s *metric.Store, metricName string, now time.Time) map[string]float64 {
	t.Helper()
	points, err := s.Series(context.Background(), metric.AggregateQuery{
		Query: metric.Query{
			MetricName: metricName,
			EntityID:   "node-us",
			Start:      now.Add(-time.Minute),
			End:        now.Add(time.Minute),
			Tags:       map[string]string{"task_id": "11"},
		},
		Aggregation:    metric.AggLast,
		Interval:       time.Second,
		PreserveSeries: true,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("query %s: %v", metricName, err)
	}
	values := make(map[string]float64, len(points))
	for _, point := range points {
		values[point.Tags["protocol"]] = point.Value
	}
	return values
}

// value_us 是同一测量的更高精度表示：写进**同一条** ping.latency_ms 序列，值变成小数毫秒。
//
// 钉住四件事：新 agent 的微秒值真的被用上（0.42 而不是被整数截断的 0）；旧 agent 的整数
// 毫秒行为不变；丢包仍按负值判定并只写 loss；指标名与标签不变（没有新增指标/标签/序列）。
func TestWritePingRecordsKeepsSubMillisecondLatency(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	s := openPingTestStore(t)

	records := []models.PingRecord{
		// 新 agent：value_us=420，旧 value 字段可能仍是 0（整数毫秒），必须用微秒值。
		{Client: "node-us", TaskId: 11, PingType: "icmp", Value: 0, ValueUS: 420, Time: now},
		// 旧 agent：没有 value_us，行为必须与改动前逐字节一致。
		{Client: "node-us", TaskId: 11, PingType: "tcp", Value: 3, Time: now},
		// 丢包：value=-1，仍然是 loss=1，延迟序列保持负值（既有契约）。
		{Client: "node-us", TaskId: 11, PingType: "http", Value: -1, Time: now},
	}
	if err := writePingRecords(ctx, records); err != nil {
		t.Fatalf("write ping records: %v", err)
	}

	latency := lastValuesByProtocol(t, s, MetricPingLatency, now)
	if got := latency["icmp"]; math.Abs(got-0.42) > 1e-9 {
		t.Errorf("icmp latency = %v, want 0.42 ms from value_us=420", got)
	}
	if got := latency["tcp"]; got != 3 {
		t.Errorf("tcp latency = %v, want the legacy 3 ms unchanged", got)
	}
	if got, ok := latency["http"]; !ok || got != -1 {
		t.Errorf("http latency = %v (present %v), want the legacy -1 for a failed probe", got, ok)
	}

	loss := lastValuesByProtocol(t, s, MetricPingLoss, now)
	if got := loss["http"]; got != 1 {
		t.Errorf("http loss = %v, want 1 for value=-1", got)
	}
	for _, protocol := range []string{"icmp", "tcp"} {
		if got := loss[protocol]; got != 0 {
			t.Errorf("%s loss = %v, want 0", protocol, got)
		}
	}

	success := lastValuesByProtocol(t, s, MetricPingSuccessLatency, now)
	if got := success["icmp"]; math.Abs(got-0.42) > 1e-9 {
		t.Errorf("icmp success latency = %v, want 0.42", got)
	}
	if got := success["tcp"]; got != 3 {
		t.Errorf("tcp success latency = %v, want 3", got)
	}
	if _, ok := success["http"]; ok {
		t.Error("a failed probe wrote a success-latency point")
	}

	// 序列/指标名一个都没变：这次改动只是把值换成小数毫秒。
	if MetricPingLatency != "ping.latency_ms" ||
		MetricPingSuccessLatency != "ping.success_latency_ms" ||
		MetricPingLoss != "ping.loss" {
		t.Fatalf("metric names changed: %q / %q / %q",
			MetricPingLatency, MetricPingSuccessLatency, MetricPingLoss)
	}
	for _, values := range []map[string]float64{latency, loss, success} {
		for protocol := range values {
			if protocol != "icmp" && protocol != "tcp" && protocol != "http" {
				t.Errorf("unexpected series protocol label %q", protocol)
			}
		}
	}

	// 读回：小数毫秒要还原成微秒，否则 0.42 ms 会被 int 截成 0 毫秒。
	got, err := GetPingRecords(ctx, "node-us", 11, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("get ping records: %v", err)
	}
	byProtocol := make(map[string]models.PingRecord, len(got))
	for _, record := range got {
		byProtocol[record.PingType] = record
	}
	icmp, ok := byProtocol["icmp"]
	if !ok {
		t.Fatalf("the icmp record is missing from %+v", got)
	}
	if icmp.ValueUS != 420 {
		t.Errorf("read-back value_us = %d, want 420", icmp.ValueUS)
	}
	if icmp.Value != 0 {
		t.Errorf("read-back value = %d, want the legacy integer truncation of 0.42", icmp.Value)
	}
	if tcp, ok := byProtocol["tcp"]; !ok || tcp.ValueUS != 3000 {
		t.Errorf("read-back tcp = %+v, want value_us 3000", tcp)
	}
	if httpRecord, ok := byProtocol["http"]; ok && httpRecord.ValueUS != 0 {
		t.Errorf("a loss record carries value_us %d, want 0 (no microsecond reading for a failure)", httpRecord.ValueUS)
	}
}
