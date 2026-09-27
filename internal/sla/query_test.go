package sla

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// fakeReader returns canned buckets and records the query it was asked for, so the
// query layer can be tested without a database. The decisions worth testing are what
// the points *mean*, not how they are fetched.
type fakeReader struct {
	points []metric.AggregatePoint
	err    error
	got    metric.AggregateQuery
	calls  int
}

func (f *fakeReader) Series(_ context.Context, query metric.AggregateQuery, _ time.Time) ([]metric.AggregatePoint, error) {
	f.calls++
	f.got = query
	return f.points, f.err
}

// A store's fill_empty produces zero-valued points with no evidence behind them, and
// those must arrive as gaps rather than as measurements of zero.
func TestEmptyBucketsBecomeGapsNotMeasurements(t *testing.T) {
	reader := &fakeReader{points: []metric.AggregatePoint{
		{Bucket: at(0), Value: 0, Count: 3},
		{Bucket: at(1), Value: 0, Count: 0}, // filled empty: no evidence
		{Bucket: at(2), Value: 0, Count: 3},
	}}

	samples, err := ReadSeries(context.Background(), reader, SeriesQuery{
		MetricName: MetricLoss, EntityID: "node", Start: at(0), End: at(3), Interval: time.Minute,
	}, base)
	if err != nil {
		t.Fatalf("ReadSeries: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want 3", len(samples))
	}
	if samples[1].Count != 0 {
		t.Fatalf("the empty bucket lost its count: %+v", samples[1])
	}

	result := AvailabilityFromLoss(samples, 3*time.Minute)
	if result.Buckets != 2 {
		t.Fatalf("Buckets = %d, want 2: the empty bucket is not a measurement", result.Buckets)
	}
	if !almostEqual(result.Presence.Coverage, 2.0/3.0) {
		t.Fatalf("Coverage = %v, want 2/3", result.Presence.Coverage)
	}
}

// The default aggregation is the mean, and an explicit one is passed through. This is
// what keeps a loss series readable: the mean of a bucket's probes is the fraction lost.
func TestAggregationDefaultsToAvg(t *testing.T) {
	reader := &fakeReader{}
	if _, err := ReadSeries(context.Background(), reader, SeriesQuery{
		MetricName: MetricLoss, Start: at(0), End: at(1), Interval: time.Minute,
	}, base); err != nil {
		t.Fatalf("ReadSeries: %v", err)
	}
	if reader.got.Aggregation != metric.AggAvg {
		t.Fatalf("aggregation = %q, want %q", reader.got.Aggregation, metric.AggAvg)
	}

	explicit := &fakeReader{}
	if _, err := ReadSeries(context.Background(), explicit, SeriesQuery{
		MetricName: MetricLoss, Start: at(0), End: at(1), Interval: time.Minute,
		Aggregation: metric.AggMax,
	}, base); err != nil {
		t.Fatalf("ReadSeries: %v", err)
	}
	if explicit.got.Aggregation != metric.AggMax {
		t.Fatalf("aggregation = %q, want %q", explicit.got.Aggregation, metric.AggMax)
	}
}

// A missing store or a nonsensical interval is an error rather than an empty report
// that reads as "everything is fine".
func TestBadQueriesAreErrors(t *testing.T) {
	if _, err := ReadSeries(context.Background(), nil, SeriesQuery{MetricName: MetricLoss}, base); err == nil {
		t.Fatal("a nil reader must be an error")
	}
	reader := &fakeReader{}
	if _, err := ReadSeries(context.Background(), reader, SeriesQuery{
		MetricName: MetricLoss, Interval: 0,
	}, base); err == nil {
		t.Fatal("a zero interval must be an error")
	}
	if reader.calls != 0 {
		t.Fatalf("the reader was called %d times for an invalid query", reader.calls)
	}
}

// Buckets arrive sorted even when the reader returns them out of order, because every
// downstream definition (cadence, incidents, coverage) assumes order.
func TestBucketsAreSorted(t *testing.T) {
	reader := &fakeReader{points: []metric.AggregatePoint{
		{Bucket: at(2), Value: 0, Count: 1},
		{Bucket: at(0), Value: 0, Count: 1},
		{Bucket: at(1), Value: 0, Count: 1},
	}}
	samples, err := ReadSeries(context.Background(), reader, SeriesQuery{
		MetricName: MetricLoss, Interval: time.Minute, Start: at(0), End: at(3),
	}, base)
	if err != nil {
		t.Fatalf("ReadSeries: %v", err)
	}
	for i := 1; i < len(samples); i++ {
		if samples[i].Bucket.Before(samples[i-1].Bucket) {
			t.Fatalf("samples are not sorted: %+v", samples)
		}
	}
}

// A node's report carries coverage and gaps, and no invented availability figure —
// the panel does not persist an online/offline history, so claiming one would be
// fiction. See PresenceFromSamples.
func TestNodeReportHasCoverageAndGapsButNoInventedUptime(t *testing.T) {
	presence := slots([]float64{0, 0, gap, gap, 0, 0})

	report := BuildNodeReport("node-uuid", presence, nil, nil, 6*time.Minute)

	if report.Presence.ObservedBuckets != 4 {
		t.Fatalf("ObservedBuckets = %d, want 4", report.Presence.ObservedBuckets)
	}
	if len(report.Gaps) != 1 {
		t.Fatalf("got %d reporting gaps, want 1: %+v", len(report.Gaps), report.Gaps)
	}
	gap := report.Gaps[0]
	if !gap.Start.Equal(at(2)) || !gap.End.Equal(at(3)) {
		t.Fatalf("gap = %s..%s, want %s..%s", gap.Start, gap.End, at(2), at(3))
	}
	if !report.HasReport {
		t.Fatal("a node that reported four buckets has a report")
	}
}

// One task's outage must not become the node's, and two tasks are reported separately
// with deterministic ordering so a report can be diffed between requests.
func TestPerTaskReportsAreSeparateAndOrdered(t *testing.T) {
	presence := series(0, 0, 0, 0)
	taskSamples := map[string][]Sample{
		"7": series(0, 1, 1, 0),
		"3": series(0, 0, 0, 0),
	}

	report := BuildNodeReport("node-uuid", presence, taskSamples, nil, 4*time.Minute)
	if len(report.Tasks) != 2 {
		t.Fatalf("got %d task reports, want 2", len(report.Tasks))
	}
	if report.Tasks[0].TaskID != "3" || report.Tasks[1].TaskID != "7" {
		t.Fatalf("tasks are not sorted: %s, %s", report.Tasks[0].TaskID, report.Tasks[1].TaskID)
	}
	// Task 3 was clean; task 7 had a two-bucket outage.
	if !almostEqual(report.Tasks[0].Loss.Fraction, 1) {
		t.Fatalf("task 3 availability = %v, want 1", report.Tasks[0].Loss.Fraction)
	}
	if len(report.Tasks[1].Outages) != 1 || report.Tasks[1].Outages[0].Buckets != 2 {
		t.Fatalf("task 7 outages = %+v, want one two-bucket run", report.Tasks[1].Outages)
	}
	// The node's own presence is unaffected by the task's failure: this is the
	// distinction the report exists to keep.
	if report.Presence.ObservedBuckets != 4 {
		t.Fatalf("node presence changed because a task failed: %+v", report.Presence)
	}
}

// The agent writes -1 for a bucket in which every probe failed. Averaging that in
// would report an outage as an extremely fast ping.
func TestFailedLatencyBucketsAreExcluded(t *testing.T) {
	latency := LatencyFromSamples(series(10, -1, 20))
	if latency.Buckets != 2 {
		t.Fatalf("Buckets = %d, want 2: the -1 bucket is not a latency", latency.Buckets)
	}
	if !almostEqual(latency.P50, 15) {
		t.Fatalf("p50 = %v, want 15", latency.P50)
	}

	empty := LatencyFromSamples(nil)
	if empty.HasData {
		t.Fatal("no latency data must not produce percentiles")
	}
	if empty.P50 != 0 {
		t.Fatalf("p50 = %v, want 0 for an unset figure", empty.P50)
	}
}

// A node with nothing at all is a report with no data, not a failure.
func TestAnEmptyNodeIsReportedAsNoData(t *testing.T) {
	report := BuildNodeReport("node-uuid", nil, map[string][]Sample{"1": nil}, nil, time.Hour)
	if report.HasReport {
		t.Fatal("a node with no samples has no report")
	}
	if report.Presence.Coverage != 0 {
		t.Fatalf("Coverage = %v, want 0", report.Presence.Coverage)
	}
	if len(report.Tasks) != 1 {
		t.Fatalf("got %d task reports, want 1", len(report.Tasks))
	}
	if report.Tasks[0].Loss.HasData {
		t.Fatal("a task with no samples must not report an availability figure")
	}
}
