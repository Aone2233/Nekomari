package jsonrpc

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
)

func TestPublicTrafficSumSurvivesMaxPointsCompactionAndRestart(t *testing.T) {
	ctx := context.Background()
	policy := metric.RollupPolicy{RawRetention: 10 * time.Minute, Tiers: []metric.RollupTier{{Interval: time.Minute, Retention: 10 * time.Hour}, {Interval: 5 * time.Minute, Retention: 50 * time.Hour}, {Interval: time.Hour, Retention: 600 * time.Hour}}, Compression: 100}
	dsn := filepath.Join(t.TempDir(), "traffic.db")
	open := func() *metric.Store {
		s, err := metric.Open(ctx, metric.SQLite(dsn, metric.WithMaxOpenConns(1), metric.WithRollupPolicy(policy)))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	name := metricstore.MetricTrafficIntervalUp
	if err := s.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := now.Truncate(time.Minute).Add(-5 * time.Minute)
	for i := 0; i < 4; i++ {
		if err := s.Write(ctx, metric.Point{MetricName: name, EntityID: "node", Timestamp: base.Add(time.Duration(i*20+5) * time.Second), Value: float64((i + 1) * 10)}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(now time.Time, wantDownsampled bool) {
		t.Helper()
		for _, maxPoints := range []int{1, 2, 500} {
			got, err := loadPublicMetricPoints(ctx, s, metric.Query{MetricName: metricstore.MetricTrafficUp, EntityID: "node", Start: base, End: base.Add(2 * time.Minute), Order: metric.OrderAsc}, metric.AggSum, maxPoints, false, now)
			if err != nil {
				t.Fatal(err)
			}
			sum := 0.0
			for _, p := range got.points {
				if p.Value != nil {
					sum += *p.Value
				}
			}
			if sum != 100 {
				t.Fatalf("age=%v maxPoints=%d sum=%v points=%+v", now.Sub(base), maxPoints, sum, got.points)
			}
			if maxPoints == 500 && got.downsampled != wantDownsampled {
				t.Fatalf("wrong source after restart: %+v", got)
			}
		}
	}
	check(now, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer func() { _ = s.Close() }()
	check(now, true)
	for _, age := range []time.Duration{20 * time.Minute, 12 * time.Hour, 60 * time.Hour} {
		at := now.Add(age)
		if _, err := s.Compact(ctx, at); err != nil {
			t.Fatal(err)
		}
		check(at, true)
	}
}

func TestPublicPingUsesSuccessfulWholeWindowDistribution(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{metricstore.MetricPingLatency, metricstore.MetricPingSuccessLatency, metricstore.MetricPingLoss} {
		if err := s.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	base := now.Truncate(time.Hour).Add(-2 * time.Hour)
	write := func(task, protocol string, values []float64, successMetric bool) {
		for i, value := range values {
			tags := map[string]string{"task_id": task, "family": "ipv4", "protocol": protocol, "role": "primary"}
			point := metric.Point{MetricName: metricstore.MetricPingLatency, EntityID: "node", Timestamp: base.Add(time.Duration(i) * time.Second), Tags: tags, Value: value}
			if err := s.Write(ctx, point); err != nil {
				t.Fatal(err)
			}
			point.MetricName = metricstore.MetricPingLoss
			point.Value = 0
			if value < 0 {
				point.Value = 1
			}
			if err := s.Write(ctx, point); err != nil {
				t.Fatal(err)
			}
			if successMetric && value >= 0 {
				point.MetricName = metricstore.MetricPingSuccessLatency
				point.Value = value
				if err := s.Write(ctx, point); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	write("1", "icmp", []float64{100, -1, 100}, true)
	write("1", "tcp", []float64{20, 20}, true)
	write("2", "icmp", []float64{-1, -1}, true)
	write("3", "icmp", []float64{100, -1, 100}, false)
	values := make([]float64, 101)
	for i := range values {
		values[i] = 10
	}
	values[100] = 1000
	write("4", "icmp", values, true)
	for _, interval := range []time.Duration{time.Minute, 5 * time.Minute, time.Hour} {
		groups, err := loadPublicPingMetricAggregateGroups(ctx, s, []string{"node"}, base, base.Add(2*time.Hour-time.Millisecond), interval, now)
		if err != nil {
			t.Fatal(err)
		}
		stats := publicPingStatsFromAggregateGroups("node", groups["node"], nil, nil)
		if len(stats) != 5 {
			t.Fatalf("protocol groups=%+v", stats)
		}
		for _, stat := range stats {
			switch stat.TaskID {
			case "1":
				if stat.Protocol == "icmp" && (stat.Total != 3 || stat.Valid != 2 || stat.Loss == nil || math.Abs(*stat.Loss-100.0/3) > 1e-8 || stat.Avg == nil || *stat.Avg != 100 || stat.P50 == nil || *stat.P50 != 100) {
					t.Fatalf("mixed success=%+v", stat)
				}
			case "2":
				if stat.Valid != 0 || stat.Loss == nil || *stat.Loss != 100 || stat.Avg != nil || stat.P50 != nil || stat.StdDev != nil || stat.P99P50Ratio != nil {
					t.Fatalf("all failures=%+v", stat)
				}
			case "3":
				if stat.Avg == nil || *stat.Avg != 100 || stat.P50 != nil || stat.P99 != nil || stat.Quality != "legacy_quantiles_unknown" {
					t.Fatalf("legacy=%+v", stat)
				}
			case "4":
				if stat.P50 == nil || math.Abs(*stat.P50-10) > 1e-8 || stat.P95 == nil || math.Abs(*stat.P95-10) > 1e-8 || stat.StdDev == nil || math.Abs(*stat.StdDev-98.01980198019803) > 1e-7 {
					payload, _ := json.Marshal(stat)
					t.Fatalf("interval=%v global distribution=%s", interval, payload)
				}
			}
		}
	}
}

func TestPublicPingMissingLossIsJSONNull(t *testing.T) {
	key := pingStatGroupKey("missing-loss", "")
	point := metric.AggregatePoint{Count: 2, Value: 100}
	groups := publicPingMetricAggregateGroups{Avg: map[string][]metric.AggregatePoint{key: {point}}}
	stats := publicPingStatsFromAggregateGroups("node", groups, nil, nil)
	if len(stats) != 1 || stats[0].Loss != nil || stats[0].ValidKnown || stats[0].Quality != "legacy_loss_unknown" {
		t.Fatalf("missing loss fabricated success: %+v", stats)
	}
	b, err := json.Marshal(stats[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if v, exists := decoded["loss"]; !exists || v != nil {
		t.Fatalf("loss must be explicit null: %s", b)
	}
}

func TestPublicTrafficBinsPreserveSeriesAndCoverage(t *testing.T) {
	base := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	start, end := base.Add(65*time.Second), base.Add(125*time.Second)
	points := []publicMetricPoint{
		{entityID: "a", Time: base.Add(time.Minute), Value: publicMetricValue(10), Count: 1, Tags: map[string]string{"scope": "one"}},
		{entityID: "a", Time: base.Add(2 * time.Minute), Value: publicMetricValue(20), Count: 2, Tags: map[string]string{"scope": "one"}},
		{entityID: "a", Time: base.Add(time.Minute), Value: publicMetricValue(7), Count: 1, Tags: map[string]string{"scope": "two"}},
		{entityID: "b", Time: base.Add(time.Minute), Value: publicMetricValue(3), Count: 1},
	}
	binned := sumPublicTrafficBins(points, start, end, 1)
	if len(binned) != 3 || binned[0].Value == nil || *binned[0].Value != 30 || binned[0].Count != 3 || *binned[1].Value != 7 || *binned[2].Value != 3 {
		t.Fatalf("series identities mixed: %+v", binned)
	}
	lo, hi := metricBucketCoverage(start, end, time.Minute)
	if lo == nil || hi == nil || !lo.Equal(base.Add(time.Minute)) || !hi.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("unreported boundary coverage: %v %v", lo, hi)
	}
}

func TestPublicTrafficHistoricalWindowDoesNotChangeWithMaxPoints(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	name := metricstore.MetricTrafficIntervalUp
	if err := s.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := now.Truncate(time.Hour).Add(-2 * time.Hour)
	for i, value := range []float64{1000, 10, 20, 1000} {
		if err := s.Write(ctx, metric.Point{MetricName: name, EntityID: "node", Timestamp: base.Add(time.Duration(i)*time.Minute + 10*time.Second), Value: value}); err != nil {
			t.Fatal(err)
		}
	}
	query := metric.Query{MetricName: metricstore.MetricTrafficUp, EntityID: "node", Start: base.Add(65 * time.Second), End: base.Add(125 * time.Second)}
	for _, maxPoints := range []int{1, 2, 500} {
		got, err := loadPublicMetricPoints(ctx, s, query, metric.AggSum, maxPoints, false, now)
		if err != nil {
			t.Fatal(err)
		}
		sum := 0.0
		for _, p := range got.points {
			sum += *p.Value
		}
		if sum != 30 {
			t.Fatalf("maxPoints=%d broadened coverage: %+v", maxPoints, got)
		}
	}
}
