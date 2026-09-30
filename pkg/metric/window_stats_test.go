package metric

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestWholeWindowMergesMomentsBeforeStatistics(t *testing.T) {
	ctx := context.Background()
	s := newRollupStore(t, RollupPolicy{RawRetention: 10 * time.Minute, Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}, {Interval: 5 * time.Minute, Retention: 48 * time.Hour}, {Interval: time.Hour, Retention: 72 * time.Hour}}})
	if err := s.CreateMetric(ctx, Definition{Name: "latency", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := now.Add(-30 * time.Minute).Truncate(time.Minute)
	points := make([]Point, 0, 101)
	for i := 0; i < 100; i++ {
		points = append(points, Point{MetricName: "latency", EntityID: "node", Timestamp: base.Add(time.Duration(i) * time.Millisecond), Value: 10})
	}
	points = append(points, Point{MetricName: "latency", EntityID: "node", Timestamp: base.Add(time.Minute), Value: 1000})
	if err := s.WriteBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	for _, interval := range []time.Duration{time.Minute, 5 * time.Minute, time.Hour} {
		got, err := s.SeriesBatch(ctx, BatchSeriesQuery{Specs: []BatchSeriesSpec{{MetricName: "latency", Aggregations: []Aggregation{AggAvg, AggP50, AggStdDev}, Interval: interval, PreserveSeries: true, WholeWindow: true}}, EntityIDs: []string{"node"}, Start: base, End: base.Add(2 * time.Minute)}, now)
		if err != nil {
			t.Fatal(err)
		}
		for agg, want := range map[Aggregation]float64{AggAvg: 2000.0 / 101, AggP50: 10, AggStdDev: 98.01980198019803} {
			values := got.Values["latency"][agg]
			if len(values) != 1 || values[0].Count != 101 || math.Abs(values[0].Value-want) > 0.01 {
				t.Fatalf("interval=%s agg=%s: %v want %g, count=101", interval, agg, values, want)
			}
		}
	}
}

func TestMergedQuantilesRemainMonotonicAndBounded(t *testing.T) {
	d := NewTDigest(100)
	for bucket := 0; bucket < 100; bucket++ {
		child := NewTDigest(100)
		for i := 0; i < 100; i++ {
			child.Add(float64(bucket*100+i), 1)
		}
		d.Merge(child)
	}
	previous := math.Inf(-1)
	for i := 0; i <= 1000; i++ {
		q := float64(i) / 1000
		value := d.Quantile(q)
		if math.IsNaN(value) || value < previous || value < 0 || value > 9999 {
			t.Fatalf("nonmonotonic/out-of-range q=%v previous=%v value=%v", q, previous, value)
		}
		if math.Abs(value-q*9999) > 100 {
			t.Fatalf("uniform fixture error exceeds 1%% range at q=%v: %v", q, value)
		}
		previous = value
	}
	constant := NewTDigest(100)
	constant.Add(10, 100)
	constant.Add(1000, 1)
	for _, q := range []float64{.50, .95} {
		if got := constant.Quantile(q); got != 10 {
			t.Fatalf("constant mass smeared into outlier at q=%v: %v", q, got)
		}
	}
}
