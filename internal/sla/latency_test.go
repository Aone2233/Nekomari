package sla

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// The report must read the latency series, not leave the field at its zero value.
//
// `LatencyFromSamples` existed and nothing called it, so every task reported
// `has_data: false` and the page said "no data" for latency while the store held 29 478
// minute rows of `ping.latency_ms` with values up to 513 ms. A field that is always empty
// looks exactly like a target that never answered, which is why the store is consulted here
// rather than the value being defaulted.
func TestBuildReadsLatencyFromItsOwnSeries(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {metric.AggAvg: {
			{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
			{EntityID: "node-a", Bucket: at(1), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
		}},
		MetricLatency: {metric.AggAvg: {
			{EntityID: "node-a", Bucket: at(0), Value: 12.5, Count: 5, Tags: map[string]string{"task_id": "1"}},
			{EntityID: "node-a", Bucket: at(1), Value: 30.0, Count: 5, Tags: map[string]string{"task_id": "1"}},
		}},
	}}

	report, err := Build(context.Background(), ReportRequest{
		Reader: reader, EntityIDs: []string{"node-a"},
		Start: at(0), End: at(2), Interval: time.Minute, Now: base,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	task := report.Nodes[0].Tasks[0]
	if !task.Latency.HasData {
		t.Fatal("latency was not read from its series, so the page would show 'no data'")
	}
	if task.Latency.P50 != 21.25 {
		t.Fatalf("p50 = %v, want 21.25 (the midpoint of 12.5 and 30.0)", task.Latency.P50)
	}
	if task.Latency.Buckets != 2 {
		t.Fatalf("buckets = %d, want 2", task.Latency.Buckets)
	}
}

// A store that fails on the latency read must not cost the availability answer: the report
// exists for coverage, and an instance that never collected ping latency should still get
// one.
func TestAFailedLatencyReadDoesNotFailTheReport(t *testing.T) {
	reader := &partialFailureReader{
		values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
			MetricLoss: {metric.AggAvg: {
				{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
			}},
		},
		failOn: MetricLatency,
	}
	report, err := Build(context.Background(), ReportRequest{
		Reader: reader, EntityIDs: []string{"node-a"},
		Start: at(0), End: at(1), Interval: time.Minute, Now: base,
	})
	if err != nil {
		t.Fatalf("a latency failure must not fail the report: %v", err)
	}
	task := report.Nodes[0].Tasks[0]
	if !task.Loss.HasData {
		t.Fatal("the availability figure was lost with the latency read")
	}
	if task.Latency.HasData {
		t.Fatal("latency claims data after a failed read")
	}
}

// partialFailureReader fails only for one metric, which is what an instance with no ping
// latency history looks like from here.
type partialFailureReader struct {
	values map[string]map[metric.Aggregation][]metric.AggregatePoint
	failOn string
}

func (f *partialFailureReader) SeriesBatch(_ context.Context, query metric.BatchSeriesQuery, _ time.Time) (metric.BatchSeriesResult, error) {
	for _, spec := range query.Specs {
		if spec.MetricName == f.failOn {
			return metric.BatchSeriesResult{}, context.DeadlineExceeded
		}
	}
	return metric.BatchSeriesResult{Values: f.values}, nil
}