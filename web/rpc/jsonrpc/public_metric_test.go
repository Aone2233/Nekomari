package jsonrpc

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
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
	if got.Loss == nil || *got.Loss != 25 || got.LossApproximate {
		t.Fatalf("loss should come from ping.loss metric: %#v", got)
	}
	if got.Min != nil || got.Max == nil || *got.Max != 92 || got.Avg == nil || math.Abs(*got.Avg-121.0/3) > 1e-9 {
		t.Fatalf("latency stats mismatch: %#v", got)
	}
	if got.P50 != nil || got.P99 != nil || got.StdDev != nil || got.P99P50Ratio != nil || got.Quality != "legacy_quantiles_unknown" {
		t.Fatalf("mixed legacy digests must not invent successful quantiles or moments: %#v", got)
	}
}

func TestPublicPingMetricStatsIncludesZeroVolatility(t *testing.T) {
	payload, err := json.Marshal(publicPingMetricTaskStats{
		EntityID:    "node-a",
		TaskID:      "1",
		Total:       1,
		Valid:       1,
		P99P50Ratio: publicMetricValue(0),
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
		Start:      now.Add(-10 * time.Minute),
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
	if publicMetricUsesRawWindow(delayedEnd.Add(-10*time.Minute), delayedEnd, now) {
		t.Fatal("partially retained windows must use rollups rather than silently truncate old samples")
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
		{EntityID: "node-a", Bucket: base, Count: 4, Value: 0, Tags: map[string]string{"task_id": "7", "family": "ipv4"}},
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

	if v4.Total != 4 || v4.Loss == nil || *v4.Loss != 0 {
		t.Fatalf("ipv4 stat should be all-ok: %#v", v4)
	}
	if v6.Total != 2 || v6.Loss == nil || *v6.Loss != 100 {
		t.Fatalf("ipv6 stat should be all-loss: %#v", v6)
	}
	if v4.Avg == nil || *v4.Avg != 25 || v6.Avg != nil {
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

// F6/P1-1：v1.6.5 把 `sum` 改道到 traffic.interval.*，而 9/10 台遗留 agent 根本不产生这些
// 序列。空的区间序列并不等于「零流量」，但读取层此前把它当零，于是主题同款请求
// `metric_keys=["traffic.up"], aggregation="sum", hours=24` 在线上读到 buckets=0，
// 而同一台节点用 `last` 读得到 57.65 GB。
//
// 这条测试同时钉住反向的一半，而它才是这次改动的前提：有区间序列的节点（生产里那台
// MAC-WAN / v1.6.4）走的是与改动前逐字相同的路径 —— 它不在回退查询的实体集合里，因此它的
// 取值、Semantics 与 Quality 都不变。所以同一个请求里必须同时放两种节点，且分别断言。
func TestPublicQueryMetricsFallsBackToTheCycleCounterForLegacyAgents(t *testing.T) {
	const legacy = "traffic-legacy-node"
	const modern = "traffic-modern-node"

	db := dbcore.GetDBInstance()
	for _, client := range []models.Client{
		{UUID: legacy, Name: "legacy agent", Token: "test-only-token-traffic-legacy"},
		{UUID: modern, Name: "modern agent", Token: "test-only-token-traffic-modern"},
	} {
		if err := db.Create(&client).Error; err != nil {
			t.Fatal(err)
		}
	}
	dbcache.InvalidateAll()

	ctx := context.Background()
	// A single minute tier with a long retention, so both reads resolve to the tier the
	// data is actually in. The default policy answers a 24-hour window from its 5-minute
	// tier, which a store that has not run its compactor yet has no rows for — production
	// does, and this test is about the read fallback, not about tier planning (that is
	// pkg/metric's).
	store, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(metric.RollupPolicy{
			RawRetention: 10 * time.Minute,
			Tiers:        []metric.RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
			Compression:  30,
		}),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Production creates both definitions at startup. The legacy agent simply never
	// writes a point to the interval series, which is the whole bug: the definition's
	// existence is not evidence of data.
	for _, name := range []string{metricstore.MetricTrafficUp, metricstore.MetricTrafficIntervalUp} {
		if err := store.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	now := time.Now().UTC()
	base := now.Truncate(time.Hour).Add(-2 * time.Hour)
	// 57.65 GB: the production `last` reading on a legacy node.
	const cycleCounter = 57_650_000_000
	for i, value := range []float64{5_000_000_000, 31_400_000_000, cycleCounter} {
		if err := store.Write(ctx, metric.Point{
			MetricName: metricstore.MetricTrafficUp,
			EntityID:   legacy,
			Timestamp:  base.Add(time.Duration(i) * time.Minute),
			Value:      value,
		}); err != nil {
			t.Fatalf("write legacy cycle counter: %v", err)
		}
	}
	// A v1.6.4+ agent reports validated interval amounts: three minutes of 100 bytes.
	for i := 0; i < 3; i++ {
		if err := store.Write(ctx, metric.Point{
			MetricName: metricstore.MetricTrafficIntervalUp,
			EntityID:   modern,
			Timestamp:  base.Add(time.Duration(i) * time.Minute),
			Value:      100,
		}); err != nil {
			t.Fatalf("write modern interval amount: %v", err)
		}
	}

	params := publicMetricQueryParams{
		MetricKeys:  []string{metricstore.MetricTrafficUp},
		EntityIDs:   []string{legacy, modern},
		Hours:       24,
		Aggregation: "sum",
	}
	result, rpcErr := publicQueryMetricsWithStore(ctx, params, store)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want the query response map", result)
	}
	series, ok := payload["series"].([]publicMetricSeries)
	if !ok {
		t.Fatalf("series type = %T", payload["series"])
	}
	byEntity := make(map[string]publicMetricSeries, len(series))
	for _, item := range series {
		byEntity[item.EntityID] = item
	}

	legacySeries, ok := byEntity[legacy]
	if !ok {
		t.Fatalf("no series for the legacy node: %+v", series)
	}
	if len(legacySeries.Points) == 0 {
		t.Fatal("legacy node still answers buckets=0: an absent interval series was reported as zero traffic")
	}
	if legacySeries.Quality != publicTrafficFallbackQuality {
		t.Fatalf("legacy quality = %q, want %q so a caller can tell unknown from zero",
			legacySeries.Quality, publicTrafficFallbackQuality)
	}
	if legacySeries.Semantics != "billing_cycle_cumulative" {
		t.Fatalf("legacy semantics = %q, want the cycle counter, not an interval sum", legacySeries.Semantics)
	}
	if legacySeries.DownsampleAlgorithm != string(metric.AggLast) {
		t.Fatalf("legacy downsample algorithm = %q, want last", legacySeries.DownsampleAlgorithm)
	}
	last := legacySeries.Points[len(legacySeries.Points)-1].Value
	if last == nil || math.Abs(*last-cycleCounter) > 1e-6*cycleCounter {
		t.Fatalf("legacy last reading = %v, want the 57.65 GB cycle counter", last)
	}

	// The production acceptance criterion, encoded: the fallback must agree with what
	// `aggregation="last"` reads on the same node over the same window, because that
	// request is where the 57.65 GB was measured.
	directResult, rpcErr := publicQueryMetricsWithStore(ctx, publicMetricQueryParams{
		MetricKeys:  []string{metricstore.MetricTrafficUp},
		EntityIDs:   []string{legacy},
		Hours:       24,
		Aggregation: "last",
	}, store)
	if rpcErr != nil {
		t.Fatalf("unexpected error on the last-aggregation control query: %+v", rpcErr)
	}
	directSeries, ok := directResult.(map[string]any)["series"].([]publicMetricSeries)
	if !ok || len(directSeries) != 1 || len(directSeries[0].Points) == 0 {
		t.Fatalf("the last-aggregation control query returned nothing: %+v", directResult)
	}
	directLast := directSeries[0].Points[len(directSeries[0].Points)-1].Value
	if directLast == nil || *directLast != *last {
		t.Fatalf("the fallback reading %v disagrees with aggregation=last %v", last, directLast)
	}

	modernSeries, ok := byEntity[modern]
	if !ok {
		t.Fatalf("no series for the node that does report intervals: %+v", series)
	}
	if modernSeries.Quality != "validated_intervals_only;legacy_history_unknown" {
		t.Fatalf("the interval-reporting node changed quality: %q", modernSeries.Quality)
	}
	if modernSeries.Semantics != "interval_delta_v2" {
		t.Fatalf("the interval-reporting node changed semantics: %q", modernSeries.Semantics)
	}
	sum := 0.0
	for _, point := range modernSeries.Points {
		if point.Value != nil {
			sum += *point.Value
		}
	}
	if math.Abs(sum-300) > 1e-9 {
		t.Fatalf("the interval-reporting node's sum = %v, want 300", sum)
	}
}

// A2：回退读的是 1 分钟 interval 序列，24h 窗口 1 分钟桶最多 1440 点/实体，而回退分支
// 完全跳过了 max_points 下采样 —— 正常分支就在它下面一行。9 台遗留节点同一个请求就是
// 约 13k 点，响应却仍然宣称 max_points=500 / server_downsample_default=true；节点再多
// 就撞上 250000 点硬上限，把一次本来可回答的查询变成 InvalidParams 硬失败。
//
// 这条测试构造出远超 max_points 的回退点，钉住「回退同样遵守 max_points」。它同时钉住
// 下采样的算法：周期累计值只能保留读数，不能把同一个 bin 里的读数相加 —— 相加的结果会
// 超过最终累计值，正是这次 interval 改道要修的那类错误。
func TestPublicQueryMetricsFallbackHonoursMaxPoints(t *testing.T) {
	const legacy = "traffic-long-window-node"

	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Client{
		UUID:  legacy,
		Name:  "long window legacy agent",
		Token: "test-only-token-traffic-long-window",
	}).Error; err != nil {
		t.Fatal(err)
	}
	dbcache.InvalidateAll()

	ctx := context.Background()
	// 与既有回退测试同样的分层：单层 1 分钟 rollup + 24h 保留，这样 24h 窗口真的按 1 分钟
	// 出点，回退路径拿到的点数才会超过 max_points。默认策略会用 5 分钟层回答 24h 窗口，
	// 在没跑过 compaction 的库里没有行。
	store, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(metric.RollupPolicy{
			RawRetention: 10 * time.Minute,
			Tiers:        []metric.RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
			Compression:  30,
		}),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer func() { _ = store.Close() }()

	for _, name := range []string{metricstore.MetricTrafficUp, metricstore.MetricTrafficIntervalUp} {
		if err := store.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	base := start.Truncate(time.Minute)

	// 1380 个 1 分钟累计采样：单调增长的周期累计值，末点就是「本周期至今」的字节数。
	const sampleCount = 1380
	const bytesPerMinute = 70_000_000
	written := make([]metric.Point, 0, sampleCount)
	final := 0.0
	for i := 0; i < sampleCount; i++ {
		final = float64(i+1) * bytesPerMinute
		written = append(written, metric.Point{
			MetricName: metricstore.MetricTrafficUp,
			EntityID:   legacy,
			Timestamp:  base.Add(time.Duration(i) * time.Minute),
			Value:      final,
		})
	}
	if err := store.WriteBatch(ctx, written); err != nil {
		t.Fatalf("write legacy cycle counter: %v", err)
	}

	// 先证明这个场景不是空转：存储层对同一窗口返回的回退点数必须真的超过 max_points，
	// 否则「回退也要下采样」这条断言什么都不算。
	direct, err := store.SeriesBatch(ctx, metric.BatchSeriesQuery{
		Specs: []metric.BatchSeriesSpec{{
			MetricName:     metricstore.MetricTrafficUp,
			Aggregations:   []metric.Aggregation{metric.AggLast},
			Interval:       time.Minute,
			PreserveSeries: true,
		}},
		EntityIDs: []string{legacy},
		Start:     start,
		End:       end,
		Order:     metric.OrderAsc,
	}, end)
	if err != nil {
		t.Fatalf("direct fallback-series read: %v", err)
	}
	rawFallbackPoints := len(direct.Values[metricstore.MetricTrafficUp][metric.AggLast])
	if rawFallbackPoints <= defaultMetricQueryPoints {
		t.Fatalf("scenario is vacuous: the fallback series holds %d points, want more than max_points=%d",
			rawFallbackPoints, defaultMetricQueryPoints)
	}

	result, rpcErr := publicQueryMetricsWithStore(ctx, publicMetricQueryParams{
		MetricKeys:  []string{metricstore.MetricTrafficUp},
		EntityIDs:   []string{legacy},
		Start:       &start,
		End:         &end,
		Aggregation: "sum",
	}, store)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want the query response map", result)
	}
	if payload["server_downsample_default"] != true {
		t.Fatalf("server_downsample_default = %v, want true", payload["server_downsample_default"])
	}
	series, ok := payload["series"].([]publicMetricSeries)
	if !ok || len(series) != 1 {
		t.Fatalf("series = %#v, want exactly the legacy node's one series", payload["series"])
	}
	got := series[0]
	if got.Quality != publicTrafficFallbackQuality {
		t.Fatalf("quality = %q, want the fallback path (%q)", got.Quality, publicTrafficFallbackQuality)
	}
	if got.MaxPoints != defaultMetricQueryPoints {
		t.Fatalf("advertised max_points = %d, want %d", got.MaxPoints, defaultMetricQueryPoints)
	}
	if len(got.Points) == 0 {
		t.Fatal("fallback returned no points at all")
	}
	if len(got.Points) > got.MaxPoints {
		t.Fatalf("fallback returned %d points for an advertised max_points=%d (pre-fix it returned %d)",
			len(got.Points), got.MaxPoints, rawFallbackPoints)
	}
	if got.Count != len(got.Points) {
		t.Fatalf("count = %d, want %d", got.Count, len(got.Points))
	}
	if got.DownsampleAlgorithm != string(metric.AggLast) {
		t.Fatalf("downsample algorithm = %q, want last", got.DownsampleAlgorithm)
	}

	lastValue := got.Points[len(got.Points)-1].Value
	if lastValue == nil {
		t.Fatal("last retained reading is nil")
	}
	if *lastValue != final {
		t.Fatalf("last retained reading = %v, want the final cycle counter %v", *lastValue, final)
	}
	for i, point := range got.Points {
		if point.Value == nil {
			continue
		}
		if *point.Value > final+1e-9 {
			t.Fatalf("point %d = %v exceeds the final cycle counter %v: cumulative readings were added up",
				i, *point.Value, final)
		}
	}
}

// A5：回退表此前按 storageKey 存、也按 storageKey 查，而 `traffic.up`（sum 被改道）与显式
// 请求的 `traffic.interval.up` 解析到同一个 storageKey `traffic.interval.up`。于是一旦同一个
// 请求里两条都在，显式写的 interval 序列会命中另一条请求创建的回退条目，被打上
// billing_cycle_cumulative 与周期累计读数 —— 与生成循环里那句注释声称的「显式请求得到它
// 自己的答案」正好相反。
//
// 这里之所以是函数级测试而不是 RPC 级：`queryMetrics` 这个组合今天到不了回退代码。
// pkg/metric 的 BatchSeriesQuery.Validate 按 MetricName 拒绝重复的 series spec，而两条请求
// 解析出的 MetricName 都是 `traffic.interval.up`，所以
// `metric_keys=["traffic.up","traffic.interval.up"]` 在 `public.metric.go` 组装 batchSpecs
// 的地方就以 -32602 `duplicate series specification for metric "traffic.interval.up"` 失败，
// 污染只在这道硬错误被去掉（或在别处放开重复 spec）之后才会显形。
//
// 因此这条测试直接钉住响应循环依赖的那份契约：回退表按【调用方点名的 metricKey】归位，
// 显式请求 `traffic.interval.up` 的查表结果必须是 nil。另一半（响应循环确实按 metricKey
// 查表、因而被改道的那条仍然拿得到回退）由
// TestPublicQueryMetricsFallsBackToTheCycleCounterForLegacyAgents 端到端钉住。
func TestPublicLegacyTrafficFallbacksAreKeyedByRequestedMetricKey(t *testing.T) {
	const legacy = "traffic-explicit-interval-node"

	ctx := context.Background()
	store, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(metric.RollupPolicy{
			RawRetention: 10 * time.Minute,
			Tiers:        []metric.RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
			Compression:  30,
		}),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer func() { _ = store.Close() }()

	for _, name := range []string{metricstore.MetricTrafficUp, metricstore.MetricTrafficIntervalUp} {
		if err := store.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	base := start.Truncate(time.Minute)
	const cycleCounter = 57_650_000_000
	// 只有周期累计计数器有数据：interval 序列对这台遗留节点是空的，所以 `traffic.up` 的
	// sum 需要回退。
	for i, value := range []float64{5_000_000_000, 31_400_000_000, cycleCounter} {
		if err := store.Write(ctx, metric.Point{
			MetricName: metricstore.MetricTrafficUp,
			EntityID:   legacy,
			Timestamp:  base.Add(time.Duration(i) * time.Minute),
			Value:      value,
		}); err != nil {
			t.Fatalf("write legacy cycle counter: %v", err)
		}
	}

	// 与 `metric_keys=["traffic.up","traffic.interval.up"], aggregation="sum"` 组装出的
	// loadSpecs 逐字一致。
	specs := []metricLoadSpec{
		{
			metricKey:  metricstore.MetricTrafficUp,
			storageKey: metricstore.MetricTrafficIntervalUp,
			algorithm:  metric.AggSum,
			maxPoints:  defaultMetricQueryPoints,
			interval:   time.Minute,
		},
		{
			metricKey:  metricstore.MetricTrafficIntervalUp,
			storageKey: metricstore.MetricTrafficIntervalUp,
			algorithm:  metric.AggSum,
			maxPoints:  defaultMetricQueryPoints,
			interval:   time.Minute,
		},
	}
	fallbacks, err := loadPublicLegacyTrafficFallbacks(
		ctx, store, specs, []string{legacy}, start, end, nil, end, nil, nil)
	if err != nil {
		t.Fatalf("load legacy traffic fallbacks: %v", err)
	}
	if len(fallbacks) != 1 {
		t.Fatalf("fallback table has %d entries, want exactly the redirected traffic.up read: %#v", len(fallbacks), fallbacks)
	}
	// 响应循环的查表表达式，逐字：fallback := trafficFallbacks[spec.metricKey]。
	if got := fallbacks[specs[1].metricKey]; got != nil {
		t.Fatalf("an explicit traffic.interval.up lookup found %d cycle-counter points: the table is keyed by the storage key",
			len(got.points))
	}
	if fallbacks[metricstore.MetricTrafficUp] == nil {
		t.Fatalf("the redirected traffic.up read lost its fallback: %#v", fallbacks)
	}
}
