package jsonrpc

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestMetricQueryParamsRequireRFC3339Time(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{name: "RFC3339", value: "2026-07-17T09:30:00.123456789+08:00"},
		{name: "offset free", value: "2026-07-17 09:30:00.123456789", wantErr: true},
		{name: "Unix number", value: float64(1_752_720_600), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := &rpc.JsonRpcRequest{Params: map[string]any{"start": test.value}}
			var params publicMetricQueryParams
			err := req.BindParams(&params)
			if (err != nil) != test.wantErr {
				t.Fatalf("BindParams() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil {
				want := time.Date(2026, 7, 17, 1, 30, 0, 123456789, time.UTC)
				if params.Start == nil || !params.Start.Equal(want) {
					t.Fatalf("start = %v, want %s", params.Start, want)
				}
			}
		})
	}
}

func TestMetricQueryParamsIgnoreRemovedDownsampleFlags(t *testing.T) {
	req := &rpc.JsonRpcRequest{Params: map[string]any{
		"metric_key":                  "cpu.usage",
		"downsample":                  false,
		"server_downsample":           false,
		"downsample_by_metric":        map[string]bool{"cpu.usage": false},
		"server_downsample_by_metric": map[string]bool{"cpu.usage": false},
		"max_points":                  123,
	}}
	var params publicMetricQueryParams
	if err := req.BindParams(&params); err != nil {
		t.Fatalf("removed downsample flags should be ignored: %v", err)
	}
	if params.MetricKey != "cpu.usage" || params.MaxPoints != 123 {
		t.Fatalf("remaining query params were not bound: %#v", params)
	}
}

func TestSplitPublicMetricSeriesKeepsTagSeries(t *testing.T) {
	baseTime := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	base := publicMetricSeries{
		MetricKey: "gpu.device.usage",
		EntityID:  "node-a",
		Points: []publicMetricPoint{
			{Time: baseTime, Value: publicMetricValue(10), Count: 2, Tags: map[string]string{"device_index": "0"}},
			{Time: baseTime, Value: publicMetricValue(80), Count: 2, Tags: map[string]string{"device_index": "1"}},
			{Time: baseTime.Add(time.Minute), Value: publicMetricValue(20), Count: 2, Tags: map[string]string{"device_index": "0"}},
		},
	}

	got := splitPublicMetricSeries(base)
	if len(got) != 2 {
		t.Fatalf("expected 2 tag series, got %d: %#v", len(got), got)
	}
	if got[0].Tags["device_index"] != "0" || got[0].Count != 2 {
		t.Fatalf("unexpected first series: %#v", got[0])
	}
	if got[1].Tags["device_index"] != "1" || got[1].Count != 1 {
		t.Fatalf("unexpected second series: %#v", got[1])
	}
	if got[0].Points[0].Tags["device_index"] != "0" || got[1].Points[0].Tags["device_index"] != "1" {
		t.Fatalf("point tags were not preserved: %#v", got)
	}
}

func TestPublicMetricJSONIncludesOnlyTags(t *testing.T) {
	pointTime := time.Date(2026, 6, 18, 0, 0, 0, 123456789, time.UTC)
	payload, err := json.Marshal(publicMetricSeries{
		MetricKey: "ping.loss",
		EntityID:  "node-a",
		Tags:      map[string]string{"task_id": "7"},
		Points: []publicMetricPoint{{
			Time:  pointTime,
			Value: publicMetricValue(0),
			Tags:  map[string]string{"task_id": "7"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal series: %v", err)
	}
	text := string(payload)
	if !strings.Contains(text, `"tags":{"task_id":"7"}`) {
		t.Fatalf("series tags missing: %s", text)
	}
	if strings.Contains(text, `"tag":`) {
		t.Fatalf("legacy tag field should not be serialized: %s", text)
	}
	if !strings.Contains(text, `"time":"2026-06-18T00:00:00.123456789Z"`) {
		t.Fatalf("metric time is not UTC RFC3339Nano: %s", text)
	}
}

func TestAdaptiveFillPublicMetricSeriesUsesObservedInterval(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	series := publicMetricSeries{
		MetricKey:       "cpu.usage",
		EntityID:        "node-a",
		FillEmpty:       true,
		IntervalSeconds: 1,
		Tags:            map[string]string{"core": "0"},
	}
	for i := 0; i < 10; i++ {
		series.Points = append(series.Points, publicMetricPoint{
			Time:  base.Add(time.Duration(i) * time.Minute),
			Value: publicMetricValue(float64(i)),
		})
	}

	got := adaptiveFillPublicMetricSeries(series, base, base.Add(9*time.Minute))
	if got.IntervalSeconds != 60 {
		t.Fatalf("expected observed 60s interval, got %v", got.IntervalSeconds)
	}
	if len(got.Points) != 10 {
		t.Fatalf("regular sparse series should not gain null buckets, got %#v", got.Points)
	}
	for _, point := range got.Points {
		if point.Value == nil {
			t.Fatalf("regular sparse series gained a null point: %#v", got.Points)
		}
	}
}

func TestAdaptiveFillPublicMetricSeriesAddsCompactGapsAndBounds(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	tags := map[string]string{"device_index": "0"}
	series := publicMetricSeries{
		MetricKey:       "gpu.device.usage",
		EntityID:        "node-a",
		IntervalSeconds: 1,
		Tags:            tags,
		Points: []publicMetricPoint{
			{Time: base, Value: publicMetricValue(10)},
			{Time: base.Add(time.Minute), Value: publicMetricValue(20)},
			{Time: base.Add(2 * time.Minute), Value: publicMetricValue(30)},
			{Time: base.Add(4 * time.Minute), Value: publicMetricValue(40)},
		},
	}
	start := base.Add(-30 * time.Second)
	end := base.Add(4*time.Minute + 30*time.Second)
	got := adaptiveFillPublicMetricSeries(series, start, end)
	if got.IntervalSeconds != 60 {
		t.Fatalf("expected observed 60s interval, got %v", got.IntervalSeconds)
	}
	if len(got.Points) != 6 {
		t.Fatalf("expected four values, one gap and one leading bound, got %#v", got.Points)
	}
	wantNullTimes := map[time.Time]bool{
		start:                     true,
		base.Add(3 * time.Minute): true,
	}
	for _, point := range got.Points {
		if point.Value != nil {
			continue
		}
		if !wantNullTimes[point.Time] {
			t.Fatalf("unexpected null point at %s: %#v", point.Time, got.Points)
		}
		if point.Tags["device_index"] != "0" {
			t.Fatalf("adaptive null point lost tags: %#v", point)
		}
		delete(wantNullTimes, point.Time)
	}
	if len(wantNullTimes) != 0 {
		t.Fatalf("missing expected null points: %#v", wantNullTimes)
	}
}

func TestAdaptiveFillPublicMetricSeriesNeverEndsWithNullAfterData(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	series := publicMetricSeries{
		MetricKey:       "cpu.usage",
		EntityID:        "node-a",
		IntervalSeconds: 60,
		Points: []publicMetricPoint{
			{Time: base, Value: publicMetricValue(10)},
			{Time: base.Add(time.Minute), Value: publicMetricValue(20)},
		},
	}
	got := adaptiveFillPublicMetricSeries(series, base, base.Add(time.Hour))
	if len(got.Points) == 0 || got.Points[len(got.Points)-1].Value == nil {
		t.Fatalf("series ended with an empty chart bucket: %#v", got.Points)
	}
}

func TestPublicPingMetricFillEmptyMapsMinusOneToNull(t *testing.T) {
	for _, metricName := range []string{metricstore.MetricPingLatency, metricstore.MetricPingLoss} {
		if value := publicRawMetricValue(metricName, -1, true); value != nil {
			t.Fatalf("raw %s -1 should become null when fill_empty is enabled, got %v", metricName, *value)
		}
	}
	if value := publicRawMetricValue(metricstore.MetricPingLatency, -1, true); value != nil {
		t.Fatalf("downsampled ping -1 should become null when fill_empty is enabled, got %v", *value)
	}
}

func TestPublicPingMetricMinusOneIsPreservedWithoutFillEmpty(t *testing.T) {
	value := publicRawMetricValue(metricstore.MetricPingLatency, -1, false)
	if value == nil || *value != -1 {
		t.Fatalf("raw ping -1 should be preserved when fill_empty is disabled, got %v", value)
	}

	nonPing := publicRawMetricValue("temperature", -1, true)
	if nonPing == nil || *nonPing != -1 {
		t.Fatalf("negative values from non-ping metrics must be preserved, got %v", nonPing)
	}
}

func TestPublicPingStatsFromAggregateGroupsUsesTaskNamesAndLossMetric(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	taskMap := map[string]models.PingTask{
		"1": {Id: 1, Name: "Tokyo ICMP", Type: "icmp", Interval: 60},
	}
	groups := publicPingMetricAggregateGroups{
		Avg: map[string][]metric.AggregatePoint{
			"1": {
				{Bucket: base, Count: 2, Value: 20},
				{Bucket: base.Add(time.Minute), Count: 2, Value: 40},
			},
		},
		Min: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 12}},
		},
		Max: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 92}},
		},
		Last: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base.Add(time.Minute), Count: 1, Value: 44}},
		},
		P50: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 30}},
		},
		P99: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 80}},
		},
		StdDev: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 8}},
		},
		Loss: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 0.25}},
		},
		LossAvailable: true,
	}

	stats := publicPingStatsFromAggregateGroups("node-a", groups, taskMap, nil)
	if len(stats) != 1 {
		t.Fatalf("expected one stat, got %#v", stats)
	}
	got := stats[0]
	if got.Name != "Tokyo ICMP" || got.Type != "icmp" || got.Interval != 60 {
		t.Fatalf("task metadata not applied: %#v", got)
	}
	if got.Total != 4 || got.Valid != 3 {
		t.Fatalf("unexpected totals: %#v", got)
	}
	if got.Loss != 25 || got.LossApproximate {
		t.Fatalf("loss should come from ping.loss metric: %#v", got)
	}
	if got.Min == nil || *got.Min != 12 || got.Max == nil || *got.Max != 92 || got.Avg == nil || *got.Avg != 30 {
		t.Fatalf("latency stats mismatch: %#v", got)
	}
	if math.Abs(got.P99P50Ratio-1.6666666666666667) > 0.000001 {
		t.Fatalf("unexpected volatility ratio: %#v", got)
	}
}

func TestPublicPingMetricStatsIncludesZeroVolatility(t *testing.T) {
	payload, err := json.Marshal(publicPingMetricTaskStats{
		EntityID:    "node-a",
		TaskID:      "1",
		Total:       1,
		Valid:       1,
		P99P50Ratio: 0,
	})
	if err != nil {
		t.Fatalf("marshal ping stats: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal ping stats: %v", err)
	}
	if value, ok := decoded["p99_p50_ratio"]; !ok || value != float64(0) {
		t.Fatalf("zero volatility must be present, got %s", payload)
	}
}

func TestMetricDownsampleIntervalCeilsToStandardInterval(t *testing.T) {
	got := metricDownsampleInterval(30*24*time.Hour, 500)
	if got != 2*time.Hour {
		t.Fatalf("30d/500 should ceil to 2h, got %s", got)
	}

	got = metricDownsampleInterval(time.Hour, 500)
	if got != 10*time.Second {
		t.Fatalf("1h/500 should ceil to 10s, got %s", got)
	}

	got = metricDownsampleInterval(1000*24*time.Hour, 10)
	if got != 100*24*time.Hour {
		t.Fatalf("ranges beyond the standard table should ceil to whole days, got %s", got)
	}
}

func TestLoadPublicMetricPointsReturnsAllRecentRawSamples(t *testing.T) {
	ctx := context.Background()
	store, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(metric.RollupPolicy{
			RawRetention: metricstore.DefaultRollupRawRetention,
			Tiers: []metric.RollupTier{
				{Interval: time.Minute, Retention: 10 * time.Hour},
			},
			Compression: 30,
		}),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer store.Close()

	const metricName = "query.raw"
	if err := store.CreateMetric(ctx, metric.Definition{
		Name:          metricName,
		Type:          metric.TypeGauge,
		RetentionDays: 1,
	}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	input := []metric.Point{
		{MetricName: metricName, EntityID: "node-a", Timestamp: now.Add(-9 * time.Minute), Value: 1, Tags: map[string]string{"core": "0"}, Labels: map[string]string{"source": "oldest"}},
		{MetricName: metricName, EntityID: "node-a", Timestamp: now.Add(-5 * time.Minute), Value: 2, Tags: map[string]string{"core": "0"}},
		{MetricName: metricName, EntityID: "node-a", Timestamp: now.Add(-90 * time.Second), Value: 3, Tags: map[string]string{"core": "0"}},
		{MetricName: metricName, EntityID: "node-a", Timestamp: now.Add(-10 * time.Second), Value: 4, Tags: map[string]string{"core": "0"}},
	}
	if err := store.WriteBatch(ctx, input); err != nil {
		t.Fatalf("write raw points: %v", err)
	}

	queryEnd := now.Add(-3 * time.Second)
	got, err := loadPublicMetricPoints(ctx, store, metric.Query{
		MetricName: metricName,
		EntityID:   "node-a",
		Start:      queryEnd.Add(-10 * time.Minute),
		End:        queryEnd,
		Order:      metric.OrderAsc,
	}, metric.AggAvg, 1, false, now)
	if err != nil {
		t.Fatalf("load public metric points: %v", err)
	}
	if got.downsampled || got.interval != 0 {
		t.Fatalf("recent raw query was marked downsampled: %#v", got)
	}
	if len(got.points) != len(input) {
		t.Fatalf("recent query returned %d points, want all %d: %#v", len(got.points), len(input), got.points)
	}
	for i, point := range got.points {
		if !point.Time.Equal(input[i].Timestamp) || point.Value == nil || *point.Value != input[i].Value || point.Count != 1 {
			t.Fatalf("point %d = %#v, want %#v", i, point, input[i])
		}
	}
	if got.points[0].Labels["source"] != "oldest" {
		t.Fatalf("compressed raw point lost labels: %#v", got.points[0])
	}
}

func TestPublicMetricUsesRawWindowOnlyForCurrentlyRetainedRange(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	if !publicMetricUsesRawWindow(now.Add(-10*time.Minute), now, now) {
		t.Fatal("exact ten-minute current range should use raw samples")
	}
	delayedEnd := now.Add(-3 * time.Second)
	if !publicMetricUsesRawWindow(delayedEnd.Add(-10*time.Minute), delayedEnd, now) {
		t.Fatal("client-side ten-minute range should tolerate transit delay while it overlaps raw retention")
	}
	cutoff := now.Add(-10 * time.Minute)
	if publicMetricUsesRawWindow(cutoff.Add(-10*time.Minute), cutoff, now) {
		t.Fatal("range ending exactly at the raw cutoff should use rollups")
	}
	historicalEnd := cutoff.Add(-time.Millisecond)
	if publicMetricUsesRawWindow(historicalEnd.Add(-10*time.Minute), historicalEnd, now) {
		t.Fatal("fully historical range should use rollups")
	}
	if publicMetricUsesRawWindow(now.Add(-10*time.Minute-time.Millisecond), now, now) {
		t.Fatal("range longer than ten minutes should use rollups")
	}
}

func TestLoadPublicMetricPointsReturnsOnlyRawAfterRestart(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "metrics.db")
	policy := metric.RollupPolicy{
		RawRetention: metricstore.DefaultRollupRawRetention,
		Tiers: []metric.RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
		},
		Compression: 30,
	}
	open := func() *metric.Store {
		store, err := metric.Open(ctx, metric.SQLite(dsn,
			metric.WithMaxOpenConns(1),
			metric.WithRollupPolicy(policy),
		))
		if err != nil {
			t.Fatalf("open metric store: %v", err)
		}
		return store
	}

	const metricName = "query.restart"
	now := time.Now().UTC().Truncate(time.Millisecond)
	store := open()
	if err := store.CreateMetric(ctx, metric.Definition{Name: metricName, Type: metric.TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	beforeRestart := []metric.Point{
		{MetricName: metricName, EntityID: "node-a", Timestamp: now.Add(-8 * time.Minute), Value: 1, Tags: map[string]string{"core": "0"}},
		{MetricName: metricName, EntityID: "node-a", Timestamp: now.Add(-90 * time.Second), Value: 2, Tags: map[string]string{"core": "0"}},
	}
	if err := store.WriteBatch(ctx, beforeRestart); err != nil {
		t.Fatalf("write pre-restart points: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close pre-restart store: %v", err)
	}

	store = open()
	defer store.Close()
	afterRestart := metric.Point{
		MetricName: metricName,
		EntityID:   "node-a",
		Timestamp:  now.Add(-5 * time.Second),
		Value:      3,
		Tags:       map[string]string{"core": "0"},
		Labels:     map[string]string{"source": "raw"},
	}
	if err := store.Write(ctx, afterRestart); err != nil {
		t.Fatalf("write post-restart point: %v", err)
	}

	got, err := loadPublicMetricPoints(ctx, store, metric.Query{
		MetricName: metricName,
		EntityID:   "node-a",
		Start:      now.Add(-10 * time.Minute),
		End:        now,
		Order:      metric.OrderAsc,
	}, metric.AggAvg, 500, false, now)
	if err != nil {
		t.Fatalf("load mixed restart window: %v", err)
	}
	if got.downsampled || got.interval != 0 {
		t.Fatalf("raw query metadata = %#v", got)
	}
	if len(got.points) != 1 {
		t.Fatalf("restart window returned %d points, want one raw point: %#v", len(got.points), got.points)
	}
	if got.points[0].Value == nil || *got.points[0].Value != 3 {
		t.Fatalf("raw point = %#v, want value 3", got.points[0])
	}
	if got.points[0].Count != 1 || got.points[0].Labels["source"] != "raw" {
		t.Fatalf("post-restart exact point changed: %#v", got.points[0])
	}
}

// TestPingStatGroupKeyKeepsLegacyShape 钉住向后兼容：族为空时分组键必须【原样】
// 等于 task_id。旧 agent 不上报族，历史数据也没有 family 标签 —— 这条不变量一旦
// 破掉，所有既有部署的统计会立刻按一个新维度重算。
func TestPingStatGroupKeyKeepsLegacyShape(t *testing.T) {
	if got := pingStatGroupKey("7", ""); got != "7" {
		t.Fatalf("empty family changed the group key: %q", got)
	}
	if taskID, family := splitPingStatGroupKey("7"); taskID != "7" || family != "" {
		t.Fatalf("legacy key split into (%q, %q)", taskID, family)
	}
	if taskID, family := splitPingStatGroupKey(pingStatGroupKey("7", "ipv6")); taskID != "7" || family != "ipv6" {
		t.Fatalf("round trip lost data: (%q, %q)", taskID, family)
	}
}

// TestPublicPingStatsSplitByAddressFamily 是这次改动的核心断言：同一个任务下，
// 实际走 IPv4 与走 IPv6 的点必须产出【两条】统计，而不是被算成一个数字。
//
// 改动前它们会被合成一条：实测中 HK04（双栈、解析到 IPv6）读 12.6% 丢包，另外两个
// v4-only 节点读 0.0%，而这三个数字描述的根本不是同一条路。合并之后图上只有一条
// 曲线，看起来像目标自己在抖。
func TestPublicPingStatsSplitByAddressFamily(t *testing.T) {
	base := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	taskMap := map[string]models.PingTask{
		"7": {Id: 7, Name: "dual-stack hostname", Type: "icmp", Interval: 60},
	}

	points := []metric.AggregatePoint{
		{EntityID: "node-a", Bucket: base, Count: 2, Value: 20, Tags: map[string]string{"task_id": "7", "family": "ipv4"}},
		{EntityID: "node-a", Bucket: base, Count: 2, Value: 30, Tags: map[string]string{"task_id": "7", "family": "ipv4"}},
		{EntityID: "node-a", Bucket: base, Count: 2, Value: 0, Tags: map[string]string{"task_id": "7", "family": "ipv6"}},
	}
	lossPoints := []metric.AggregatePoint{
		{EntityID: "node-a", Bucket: base, Count: 2, Value: 0, Tags: map[string]string{"task_id": "7", "family": "ipv4"}},
		{EntityID: "node-a", Bucket: base, Count: 2, Value: 1, Tags: map[string]string{"task_id": "7", "family": "ipv6"}},
	}

	groups := publicPingMetricAggregateGroups{
		Avg:           groupPingMetricAggregatePointsByEntity(points)["node-a"],
		Loss:          groupPingMetricAggregatePointsByEntity(lossPoints)["node-a"],
		Min:           map[string][]metric.AggregatePoint{},
		Max:           map[string][]metric.AggregatePoint{},
		Last:          map[string][]metric.AggregatePoint{},
		P50:           map[string][]metric.AggregatePoint{},
		P99:           map[string][]metric.AggregatePoint{},
		StdDev:        map[string][]metric.AggregatePoint{},
		LossAvailable: true,
	}

	stats := publicPingStatsFromAggregateGroups("node-a", groups, taskMap, nil)
	if len(stats) != 2 {
		t.Fatalf("two address families must produce two stats, got %d: %#v", len(stats), stats)
	}

	byFamily := make(map[string]publicPingMetricTaskStats, len(stats))
	for _, stat := range stats {
		byFamily[stat.Family] = stat
		if stat.TaskID != "7" {
			t.Fatalf("task id lost while splitting: %#v", stat)
		}
		if stat.Tags["task_id"] != "7" || stat.Tags["family"] != stat.Family {
			t.Fatalf("tags do not identify the series: %#v", stat.Tags)
		}
	}

	v4, ok := byFamily["ipv4"]
	if !ok {
		t.Fatalf("no ipv4 stat: %#v", stats)
	}
	v6, ok := byFamily["ipv6"]
	if !ok {
		t.Fatalf("no ipv6 stat: %#v", stats)
	}

	if v4.Total != 4 || v4.Loss != 0 {
		t.Fatalf("ipv4 stat should be all-ok: %#v", v4)
	}
	if v6.Total != 2 || v6.Loss != 100 {
		t.Fatalf("ipv6 stat should be all-loss: %#v", v6)
	}
	if v4.Avg == nil || v6.Avg == nil || *v4.Avg == *v6.Avg {
		t.Fatalf("the two families must not share an average: v4=%#v v6=%#v", v4.Avg, v6.Avg)
	}
}

// TestPublicPingStatsStayMergedWithoutFamily 钉住另一半：没有族信息时，同一任务的
// 点仍然合成一条统计，与改动前逐字一致。
func TestPublicPingStatsStayMergedWithoutFamily(t *testing.T) {
	base := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	points := []metric.AggregatePoint{
		{EntityID: "node-a", Bucket: base, Count: 2, Value: 20, Tags: map[string]string{"task_id": "7"}},
		{EntityID: "node-a", Bucket: base, Count: 2, Value: 30, Tags: map[string]string{"task_id": "7"}},
	}
	groups := publicPingMetricAggregateGroups{
		Avg:    groupPingMetricAggregatePointsByEntity(points)["node-a"],
		Loss:   map[string][]metric.AggregatePoint{},
		Min:    map[string][]metric.AggregatePoint{},
		Max:    map[string][]metric.AggregatePoint{},
		Last:   map[string][]metric.AggregatePoint{},
		P50:    map[string][]metric.AggregatePoint{},
		P99:    map[string][]metric.AggregatePoint{},
		StdDev: map[string][]metric.AggregatePoint{},
	}

	stats := publicPingStatsFromAggregateGroups("node-a", groups, nil, nil)
	if len(stats) != 1 {
		t.Fatalf("without family information the points must stay merged, got %d: %#v", len(stats), stats)
	}
	if stats[0].Family != "" {
		t.Fatalf("family should stay empty: %#v", stats[0])
	}
	if stats[0].Total != 4 {
		t.Fatalf("merged total changed: %#v", stats[0])
	}
}
