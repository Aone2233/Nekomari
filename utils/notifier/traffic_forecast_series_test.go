package notifier

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/cycle"
	"github.com/Aone2233/nekomari/internal/forecast"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// The projection used to read `net.total.up` / `net.total.down` — the kernel counter
// since boot — while the plan limit is written against the cycle-to-date total. The
// old comment even asserted the two were the same quantity. Nothing failed when that
// was wrong, because the tests fed synthetic samples straight into forecast.Compute
// and never looked at which series produced them.
//
// These two tests close that hole from both ends: the constant is pinned, and the
// reader is exercised against a result that carries both series at once.

func TestTheForecastReadsTheCycleSeriesNotTheBootCounter(t *testing.T) {
	if len(cycleTrafficMetricNames) != 2 {
		t.Fatalf("cycleTrafficMetricNames = %v, want exactly the up/down pair", cycleTrafficMetricNames)
	}
	if cycleTrafficMetricNames[0] != metricstore.MetricTrafficUp {
		t.Errorf("cycleTrafficMetricNames[0] = %q, want %q", cycleTrafficMetricNames[0], metricstore.MetricTrafficUp)
	}
	if cycleTrafficMetricNames[1] != metricstore.MetricTrafficDown {
		t.Errorf("cycleTrafficMetricNames[1] = %q, want %q", cycleTrafficMetricNames[1], metricstore.MetricTrafficDown)
	}
	for _, name := range cycleTrafficMetricNames {
		if name == metricstore.MetricNetTotalUp || name == metricstore.MetricNetTotalDown {
			t.Fatalf("%q is the kernel counter since boot; the limit is a per-cycle quota", name)
		}
	}
}

func TestCycleSamplesComeFromTheCycleSeries(t *testing.T) {
	base := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	bucket := func(name string, value float64) metric.AggregatePoint {
		return metric.AggregatePoint{MetricName: name, Bucket: base, Value: value}
	}

	// Both series are present and deliberately disagree: the boot counter is far
	// larger than the cycle total, which is what a node up for longer than one cycle
	// looks like. The samples must be the smaller pair.
	result := metric.BatchSeriesResult{
		Values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
			metricstore.MetricTrafficUp: {
				metric.AggMax: {bucket(metricstore.MetricTrafficUp, 100)},
			},
			metricstore.MetricTrafficDown: {
				metric.AggMax: {bucket(metricstore.MetricTrafficDown, 50)},
			},
			metricstore.MetricNetTotalUp: {
				metric.AggMax: {bucket(metricstore.MetricNetTotalUp, 999999)},
			},
			metricstore.MetricNetTotalDown: {
				metric.AggMax: {bucket(metricstore.MetricNetTotalDown, 888888)},
			},
		},
	}

	samples, err := cycleSamplesFromSeries("sum", result)
	if err != nil {
		t.Fatalf("cycle samples: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("samples = %#v, want one bucket", samples)
	}
	if samples[0].Total != 150 {
		t.Fatalf("sample total = %d, want 150 (cycle up 100 + cycle down 50); "+
			"a total near 1888887 means the boot counter was read instead", samples[0].Total)
	}

	// 反向：只有开机累计时，投影拿不到任何样本，而不是把开机累计当周期用量。
	bootOnly := metric.BatchSeriesResult{
		Values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
			metricstore.MetricNetTotalUp: {
				metric.AggMax: {bucket(metricstore.MetricNetTotalUp, 999999)},
			},
		},
	}
	if got, err := cycleSamplesFromSeries("sum", bootOnly); err != nil || len(got) != 0 {
		t.Fatalf("samples = %#v, err = %v; want no samples: the boot counter is not the cycle total", got, err)
	}
}

func TestCycleSamplesRequireTheDirectionsUsedByTheLimit(t *testing.T) {
	base := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	point := func(hour int, value float64) metric.AggregatePoint {
		return metric.AggregatePoint{Bucket: base.Add(time.Duration(hour) * time.Hour), Value: value}
	}
	tests := []struct {
		name, limitType string
		up, down        []metric.AggregatePoint
		want            []int64
		wantError       string
	}{
		{"sum with zero is valid", "sum", []metric.AggregatePoint{point(1, 0)}, []metric.AggregatePoint{point(1, 10)}, []int64{10}, ""},
		{"sum missing down", "sum", []metric.AggregatePoint{point(1, 10)}, nil, nil, "missing"},
		{"sum missing up", "sum", nil, []metric.AggregatePoint{point(1, 10)}, nil, "missing"},
		{"sum later missing down", "sum", []metric.AggregatePoint{point(1, 10), point(2, 20)}, []metric.AggregatePoint{point(1, 5)}, nil, "missing"},
		{"min missing down", "min", []metric.AggregatePoint{point(1, 10)}, nil, nil, "missing"},
		{"max missing down", "max", []metric.AggregatePoint{point(1, 10)}, nil, nil, "missing"},
		{"default missing down", "other", []metric.AggregatePoint{point(1, 10)}, nil, nil, "missing"},
		{"sum masks a reset", "sum", []metric.AggregatePoint{point(1, 10), point(2, 5)}, []metric.AggregatePoint{point(1, 10), point(2, 20)}, nil, "decreased"},
		{"max masks a reset", "max", []metric.AggregatePoint{point(1, 10), point(2, 5)}, []metric.AggregatePoint{point(1, 20), point(2, 30)}, nil, "decreased"},
		{"min masks a reset", "min", []metric.AggregatePoint{point(1, 30), point(2, 25)}, []metric.AggregatePoint{point(1, 10), point(2, 20)}, nil, "decreased"},
		{"duplicate bucket is ambiguous", "up", []metric.AggregatePoint{point(1, 10), point(1, 20)}, nil, nil, "duplicate"},
		{"up ignores down-only buckets", "up", []metric.AggregatePoint{point(2, 20), point(1, 10)}, []metric.AggregatePoint{point(3, 300)}, []int64{10, 20}, ""},
		{"down ignores up-only buckets", "down", []metric.AggregatePoint{point(3, 300)}, []metric.AggregatePoint{point(2, 20), point(1, 10)}, []int64{10, 20}, ""},
		{"up absent is no observations", "up", nil, []metric.AggregatePoint{point(1, 10)}, nil, ""},
		{"both absent is no observations", "sum", nil, nil, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := metric.BatchSeriesResult{Values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
				metricstore.MetricTrafficUp:   {metric.AggMax: tc.up},
				metricstore.MetricTrafficDown: {metric.AggMax: tc.down},
			}}
			got, err := cycleSamplesFromSeries(tc.limitType, result)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got samples=%v err=%v, want error containing %q", got, err, tc.wantError)
				}
				if len(got) != 0 {
					t.Fatalf("a partial series must not be projected: %v", got)
				}
				return
			}
			if err != nil || len(got) != len(tc.want) {
				t.Fatalf("got samples=%v err=%v, want totals %v", got, err, tc.want)
			}
			for i, want := range tc.want {
				if got[i].Total != want {
					t.Fatalf("sample %d = %v, want total %d", i, got[i], want)
				}
				if i > 0 && !got[i-1].At.Before(got[i].At) {
					t.Fatalf("samples are not chronological: %v", got)
				}
			}
		})
	}
}

func TestCycleTrafficSamplesUsesActualStoreAndRefusesIncompleteHistory(t *testing.T) {
	ctx := context.Background()
	store, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1), metric.WithRollupPolicy(metric.RollupPolicy{
		RawRetention: time.Hour,
		Tiers:        []metric.RollupTier{{Interval: time.Minute, Retention: 90 * 24 * time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for _, name := range cycleTrafficMetricNames {
		if err := store.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 90}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().UTC().Truncate(24 * time.Hour).Add(-10 * 24 * time.Hour)
	window := cycle.Cycle{Start: start, End: start.Add(30 * 24 * time.Hour)}
	now := start.Add(6 * 24 * time.Hour)
	write := func(node, name string, day int, value float64) {
		t.Helper()
		if err := store.Write(ctx, metric.Point{MetricName: name, EntityID: node, Timestamp: start.Add(time.Duration(day)*24*time.Hour + time.Hour), Value: value}); err != nil {
			t.Fatal(err)
		}
	}
	for day := 0; day < 6; day++ {
		for _, node := range []string{"complete", "incomplete"} {
			write(node, metricstore.MetricTrafficUp, day, float64((day+1)*100))
			if node == "complete" || day != 3 {
				write(node, metricstore.MetricTrafficDown, day, float64((day+1)*50))
			}
		}
	}
	for _, node := range []string{"complete", "incomplete"} {
		samples, err := cycleTrafficSamplesWithStore(now, models.Client{UUID: node, TrafficLimitType: "sum"}, window, store)
		if node == "incomplete" {
			if err == nil || !strings.Contains(err.Error(), "missing") || len(samples) != 0 {
				t.Fatalf("incomplete actual series: samples=%v err=%v", samples, err)
			}
			continue
		}
		if err != nil || len(samples) != 6 {
			t.Fatalf("complete actual series: samples=%v err=%v", samples, err)
		}
		projection := forecast.Compute(now, forecast.Request{CycleStart: window.Start, CycleEnd: window.End, Samples: samples, LimitBytes: 3000})
		if projection.Method != forecast.MethodLinearRate || projection.UsedBytes != 900 || projection.ProjectedBytes < projection.UsedBytes {
			t.Fatalf("complete actual series projection = %+v", projection)
		}
	}
}

func TestCycleSamplesRejectInvalidOrOverflowingCounters(t *testing.T) {
	base := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	point := func(value float64) metric.AggregatePoint { return metric.AggregatePoint{Bucket: base, Value: value} }
	tests := []struct {
		name, limitType string
		up, down        float64
		wantError       string
	}{
		{"negative", "up", -1, 0, "invalid"},
		{"NaN", "up", math.NaN(), 0, "invalid"},
		{"positive infinity", "up", math.Inf(1), 0, "invalid"},
		{"out of range", "up", math.Ldexp(1, 63), 0, "invalid"},
		{"sum overflow", "sum", math.Ldexp(1, 62), math.Ldexp(1, 62), "overflow"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := metric.BatchSeriesResult{Values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
				metricstore.MetricTrafficUp:   {metric.AggMax: {point(tc.up)}},
				metricstore.MetricTrafficDown: {metric.AggMax: {point(tc.down)}},
			}}
			got, err := cycleSamplesFromSeries(tc.limitType, result)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) || len(got) != 0 {
				t.Fatalf("got samples=%v err=%v; want no samples and error containing %q", got, err, tc.wantError)
			}
		})
	}
}
