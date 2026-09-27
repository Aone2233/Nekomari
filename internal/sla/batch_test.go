package sla

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// fakeBatchReader serves the batch query shape and records what was asked for.
type fakeBatchReader struct {
	values map[string]map[metric.Aggregation][]metric.AggregatePoint
	err    error
	got    metric.BatchSeriesQuery
	calls  int
}

func (f *fakeBatchReader) SeriesBatch(_ context.Context, query metric.BatchSeriesQuery, _ time.Time) (metric.BatchSeriesResult, error) {
	f.calls++
	f.got = query
	if f.err != nil {
		return metric.BatchSeriesResult{}, f.err
	}
	return metric.BatchSeriesResult{Values: f.values}, nil
}

// One query covers every entity, and the result is split back into per-entity series
// with their tags intact — that is what makes a per-task report possible instead of one
// number per node.
func TestBatchReadSplitsByEntityAndTags(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {
			metric.AggAvg: {
				{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
				{EntityID: "node-a", Bucket: at(1), Value: 1, Count: 5, Tags: map[string]string{"task_id": "1"}},
				{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "2"}},
				{EntityID: "node-b", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
			},
		},
	}}

	buckets, err := ReadSeriesPerEntity(context.Background(), reader, SeriesQuery{
		MetricName: MetricLoss, Start: at(0), End: at(2), Interval: time.Minute,
	}, []string{"node-a", "node-b"}, base)
	if err != nil {
		t.Fatalf("ReadSeriesPerEntity: %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("expected one query for both entities, got %d", reader.calls)
	}
	if len(reader.got.Specs) != 1 || reader.got.Specs[0].MetricName != MetricLoss {
		t.Fatalf("unexpected spec: %+v", reader.got.Specs)
	}
	if !reader.got.Specs[0].PreserveSeries {
		t.Fatal("series must be preserved, or two tasks merge into one")
	}
	// node-a task 1, node-a task 2, node-b task 1.
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets, want 3: %+v", len(buckets), buckets)
	}
	if buckets[0].EntityID != "node-a" || buckets[0].Tags["task_id"] != "1" {
		t.Fatalf("first bucket = %s/%v", buckets[0].EntityID, buckets[0].Tags)
	}
	if len(buckets[0].Samples) != 2 {
		t.Fatalf("first bucket has %d samples, want 2", len(buckets[0].Samples))
	}
}

// An entity with no data simply does not appear; the caller reports it as no data
// rather than the reader inventing an empty series for it.
func TestBatchReadOmitsEntitiesWithNoData(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {metric.AggAvg: {
			{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 1},
		}},
	}}
	buckets, err := ReadSeriesPerEntity(context.Background(), reader, SeriesQuery{
		MetricName: MetricLoss, Interval: time.Minute, Start: at(0), End: at(1),
	}, []string{"node-a", "node-b"}, base)
	if err != nil {
		t.Fatalf("ReadSeriesPerEntity: %v", err)
	}
	if len(buckets) != 1 || buckets[0].EntityID != "node-a" {
		t.Fatalf("buckets = %+v, want only node-a", buckets)
	}
}

// Missing values for the metric are not an error: a metric nobody has reported yet is
// an empty report, not a failure.
func TestBatchReadHandlesAnUnknownMetric(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{}}
	buckets, err := ReadSeriesPerEntity(context.Background(), reader, SeriesQuery{
		MetricName: MetricLoss, Interval: time.Minute, Start: at(0), End: at(1),
	}, []string{"node-a"}, base)
	if err != nil {
		t.Fatalf("an unknown metric must not error: %v", err)
	}
	if len(buckets) != 0 {
		t.Fatalf("buckets = %+v, want none", buckets)
	}
}

// Absent buckets and zero-count buckets are the same thing to a reader, and both must
// come back as gaps. This is the failure mode where a dead node reads as healthy.
func TestCompleteGridTreatsAbsentAndEmptyAlike(t *testing.T) {
	buckets := []Bucket{{
		EntityID: "node-a",
		Samples: []Sample{
			{Bucket: at(0), Value: 0, Count: 3},
			// minute 1 is simply absent from the store's answer
			{Bucket: at(2), Value: 0, Count: 0}, // and minute 2 came back empty
			{Bucket: at(3), Value: 0, Count: 3},
		},
	}}

	filled := CompleteGrid(buckets, at(0), at(4), time.Minute)
	if len(filled) != 1 {
		t.Fatalf("got %d buckets, want 1", len(filled))
	}
	samples := filled[0].Samples
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 4: %+v", len(samples), samples)
	}
	if samples[1].Count != 0 {
		t.Fatalf("the absent minute was not filled as a gap: %+v", samples[1])
	}
	if samples[2].Count != 0 {
		t.Fatalf("the empty minute lost its gap status: %+v", samples[2])
	}
	if samples[0].Count == 0 || samples[3].Count == 0 {
		t.Fatalf("measured minutes were turned into gaps: %+v", samples)
	}

	result := AvailabilityFromLoss(samples, 4*time.Minute)
	if result.Buckets != 2 {
		t.Fatalf("Buckets = %d, want 2 measured minutes", result.Buckets)
	}
	if !almostEqual(result.Presence.Coverage, 0.5) {
		t.Fatalf("Coverage = %v, want 0.5", result.Presence.Coverage)
	}
}

// The grid stops where the data stops: inventing trailing empty buckets would report a
// node that stopped reporting an hour ago as 50% covered rather than 100% of what it
// sent.
func TestCompleteGridDoesNotInventTrailingBuckets(t *testing.T) {
	buckets := []Bucket{{
		EntityID: "node-a",
		Samples:  []Sample{{Bucket: at(1), Value: 0, Count: 1}},
	}}
	filled := CompleteGrid(buckets, at(0), at(10), time.Minute)
	samples := filled[0].Samples
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2 (the grid up to the last reported minute): %+v", len(samples), samples)
	}
	if !samples[len(samples)-1].Bucket.Equal(at(1)) {
		t.Fatalf("grid ends at %s, want %s", samples[len(samples)-1].Bucket, at(1))
	}
}

// A series with nothing in it stays empty rather than being given a grid to be empty in.
func TestCompleteGridOnNoSamplesIsANoOp(t *testing.T) {
	buckets := []Bucket{{EntityID: "node-a"}}
	filled := CompleteGrid(buckets, at(0), at(5), time.Minute)
	if len(filled) != 1 || len(filled[0].Samples) != 0 {
		t.Fatalf("filled = %+v, want the bucket unchanged", filled)
	}
}
