package sla

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// Build must cover the whole fleet in one query, group per node and per task, and
// report a node that sent nothing as having no data rather than as failing.
func TestBuildAssemblesTheWholeReport(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {
			metric.AggAvg: {
				// node-a, task 1: one lost bucket out of four.
				{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
				{EntityID: "node-a", Bucket: at(1), Value: 1, Count: 5, Tags: map[string]string{"task_id": "1"}},
				{EntityID: "node-a", Bucket: at(2), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
				{EntityID: "node-a", Bucket: at(3), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
				// node-a, task 2: clean.
				{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "2"}},
				{EntityID: "node-a", Bucket: at(1), Value: 0, Count: 5, Tags: map[string]string{"task_id": "2"}},
				{EntityID: "node-a", Bucket: at(2), Value: 0, Count: 5, Tags: map[string]string{"task_id": "2"}},
				{EntityID: "node-a", Bucket: at(3), Value: 0, Count: 5, Tags: map[string]string{"task_id": "2"}},
				// node-b, task 1: a gap in the middle.
				{EntityID: "node-b", Bucket: at(0), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
				{EntityID: "node-b", Bucket: at(3), Value: 0, Count: 5, Tags: map[string]string{"task_id": "1"}},
			},
		},
	}}

	report, err := Build(context.Background(), ReportRequest{
		Reader:      reader,
		EntityIDs:   []string{"node-a", "node-b", "node-c"},
		Start:       at(0),
		End:         at(4),
		Interval:    time.Minute,
		WindowLabel: "4m",
		Now:         base,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("expected one query for three nodes, got %d", reader.calls)
	}
	if len(report.Nodes) != 3 {
		t.Fatalf("got %d nodes, want 3: %+v", len(report.Nodes), report.Nodes)
	}

	// node-a: two tasks, the first with a one-bucket outage.
	nodeA := report.Nodes[0]
	if nodeA.EntityID != "node-a" || len(nodeA.Tasks) != 2 {
		t.Fatalf("node-a = %+v, want two tasks", nodeA)
	}
	if !almostEqual(nodeA.Tasks[0].Loss.Fraction, 0.75) {
		t.Fatalf("node-a task 1 availability = %v, want 0.75", nodeA.Tasks[0].Loss.Fraction)
	}
	if len(nodeA.Tasks[0].Outages) != 1 {
		t.Fatalf("node-a task 1 outages = %+v, want one", nodeA.Tasks[0].Outages)
	}
	if !almostEqual(nodeA.Tasks[1].Loss.Fraction, 1) {
		t.Fatalf("node-a task 2 availability = %v, want 1", nodeA.Tasks[1].Loss.Fraction)
	}
	if !almostEqual(nodeA.Presence.Coverage, 1) {
		t.Fatalf("node-a coverage = %v, want 1", nodeA.Presence.Coverage)
	}
	if !nodeA.HasReport {
		t.Fatal("node-a has a report")
	}

	// node-b: the absent minutes are gaps, so coverage is 2/4 and the two measured
	// buckets are both clean.
	nodeB := report.Nodes[1]
	if nodeB.EntityID != "node-b" {
		t.Fatalf("second node = %s, want node-b", nodeB.EntityID)
	}
	if len(nodeB.Tasks) != 1 {
		t.Fatalf("node-b tasks = %d, want 1", len(nodeB.Tasks))
	}
	if !almostEqual(nodeB.Tasks[0].Loss.Fraction, 1) {
		t.Fatalf("node-b availability = %v, want 1: both measured buckets were clean",
			nodeB.Tasks[0].Loss.Fraction)
	}
	if !almostEqual(nodeB.Tasks[0].Loss.Presence.Coverage, 0.5) {
		t.Fatalf("node-b coverage = %v, want 0.5", nodeB.Tasks[0].Loss.Presence.Coverage)
	}

	// node-c sent nothing at all.
	nodeC := report.Nodes[2]
	if nodeC.EntityID != "node-c" {
		t.Fatalf("third node = %s, want node-c", nodeC.EntityID)
	}
	if nodeC.HasReport {
		t.Fatal("node-c sent nothing, so it has no report")
	}
	if nodeC.Presence.Coverage != 0 {
		t.Fatalf("node-c coverage = %v, want 0", nodeC.Presence.Coverage)
	}
}

// A series with no task_id tag is reported under an empty id rather than dropped:
// discarding data that exists would make the report disagree with the raw series for
// no stated reason.
func TestBuildKeepsASeriesWithoutATaskTag(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {metric.AggAvg: {
			{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 1},
		}},
	}}
	report, err := Build(context.Background(), ReportRequest{
		Reader: reader, EntityIDs: []string{"node-a"},
		Start: at(0), End: at(1), Interval: time.Minute, Now: base,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(report.Nodes) != 1 || len(report.Nodes[0].Tasks) != 1 {
		t.Fatalf("nodes = %+v, want one node with one untagged series", report.Nodes)
	}
	if report.Nodes[0].Tasks[0].TaskID != "" {
		t.Fatalf("task id = %q, want empty", report.Nodes[0].Tasks[0].TaskID)
	}
}

// Entities are reported in a stable order, including any that appeared in the data but
// were not asked for.
func TestBuildOrderIsStableAndIncludesUnexpectedEntities(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {metric.AggAvg: {
			{EntityID: "node-z", Bucket: at(0), Value: 0, Count: 1},
			{EntityID: "node-a", Bucket: at(0), Value: 0, Count: 1},
		}},
	}}
	report, err := Build(context.Background(), ReportRequest{
		Reader: reader, EntityIDs: []string{"node-a"},
		Start: at(0), End: at(1), Interval: time.Minute, Now: base,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(report.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2 (the requested one and the one in the data): %+v",
			len(report.Nodes), report.Nodes)
	}
	if report.Nodes[0].EntityID != "node-a" || report.Nodes[1].EntityID != "node-z" {
		t.Fatalf("order = %s, %s; want node-a then node-z",
			report.Nodes[0].EntityID, report.Nodes[1].EntityID)
	}
}

// A store error is returned rather than being turned into an empty report, which would
// read as "everything is fine".
func TestBuildPropagatesStoreErrors(t *testing.T) {
	reader := &fakeBatchReader{err: context.DeadlineExceeded}
	if _, err := Build(context.Background(), ReportRequest{
		Reader: reader, EntityIDs: []string{"node-a"},
		Start: at(0), End: at(1), Interval: time.Minute, Now: base,
	}); err == nil {
		t.Fatal("a store error must not produce a report")
	}
	if _, err := Build(context.Background(), ReportRequest{
		Start: at(0), End: at(1), Interval: time.Minute,
	}); err == nil {
		t.Fatal("a missing reader must be an error")
	}
}
