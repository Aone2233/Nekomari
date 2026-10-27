package sla

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func generationPoints(name string) []metric.AggregatePoint {
	var points []metric.AggregatePoint
	for generation, tags := range []map[string]string{
		{"task_id": "7"},
		{"task_id": "7", "protocol": "icmp", "family": "ipv4"},
	} {
		for i := 5; i >= 0; i-- {
			value, count := 0.0, 1
			if i == 2 {
				count = 0
			}
			if name == MetricLoss {
				if i == 1 || i == 3 || (i == 4 && generation == 1) {
					value = 1
				}
			} else {
				value = []float64{10, -1, 0, 20, 30, 40}[i]
				if generation == 1 {
					value = []float64{15, 0, 0, 25, -1, 0}[i]
					if i == 1 || i == 5 {
						count = 0
					}
				}
			}
			points = append(points, metric.AggregatePoint{
				EntityID: "node-a", Bucket: at(i), Value: value, Count: count, Tags: tags,
			})
		}
	}
	return points
}

func TestBuildMergesTaskGenerationsBeforeAllStatistics(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "maintenance"}[maintenance], func(t *testing.T) {
			reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
				MetricLoss:    {metric.AggAvg: generationPoints(MetricLoss)},
				MetricLatency: {metric.AggAvg: generationPoints(MetricLatency)},
			}}
			request := ReportRequest{Reader: reader, EntityIDs: []string{"node-a"},
				Start: at(0), End: at(6), Interval: time.Minute, Now: base}
			if maintenance {
				request.Exclusions = map[string][]Interval{"node-a": {{Start: at(3), End: at(4)}}}
			}
			report, err := Build(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if reader.calls != 2 || len(report.Nodes) != 1 || len(report.Nodes[0].Tasks) != 1 {
				t.Fatalf("query/grouping contract changed: calls=%d report=%+v", reader.calls, report)
			}
			node, task := report.Nodes[0], report.Nodes[0].Tasks[0]
			buckets, lost, fraction := 5, 3, 0.4
			latencyBuckets, p50, p95, p99 := 4, 27.5, 38.5, 39.7
			expected, observed, coverage := 6, 5, 5.0/6
			wantOutages := []Incident{
				{Start: at(1), End: at(1), Buckets: 1, PeakLoss: 1},
				{Start: at(3), End: at(4), Buckets: 2, Duration: time.Minute, PeakLoss: 1},
			}
			if maintenance {
				buckets, lost, fraction = 4, 2, 0.5
				latencyBuckets, p50, p95, p99 = 3, 30, 39, 39.8
				expected, observed, coverage = 5, 4, 0.8
				wantOutages[1] = Incident{Start: at(4), End: at(4), Buckets: 1, PeakLoss: 1}
				if node.MaintenanceExcludedSeconds != 60 {
					t.Fatalf("maintenance seconds=%d", node.MaintenanceExcludedSeconds)
				}
			}
			if !task.Loss.HasData || task.Loss.Buckets != buckets || task.Loss.Lost != lost || !almostEqual(task.Loss.Fraction, fraction) {
				t.Errorf("loss=%+v, want %d buckets/%d lost/%v fraction", task.Loss, buckets, lost, fraction)
			}
			if !task.Latency.HasData || task.Latency.Buckets != latencyBuckets ||
				!almostEqual(task.Latency.P50, p50) || !almostEqual(task.Latency.P95, p95) || !almostEqual(task.Latency.P99, p99) {
				t.Errorf("latency=%+v, want %d buckets/%v/%v/%v", task.Latency, latencyBuckets, p50, p95, p99)
			}
			if !reflect.DeepEqual(task.Outages, wantOutages) {
				t.Errorf("outages=%+v, want %+v", task.Outages, wantOutages)
			}
			for _, presence := range []Presence{node.Presence, task.Loss.Presence} {
				if presence.ExpectedBuckets != expected || presence.ObservedBuckets != observed || !almostEqual(presence.Coverage, coverage) {
					t.Errorf("presence=%+v, want %d/%d=%v", presence, observed, expected, coverage)
				}
			}
			if len(node.Gaps) != 1 || !node.Gaps[0].Start.Equal(at(2)) || node.Gaps[0].Buckets != 1 {
				t.Errorf("reporting gaps=%+v, want only minute 2", node.Gaps)
			}
		})
	}
}

func TestBuildGenerationMergeDoesNotMixTasksOrNodes(t *testing.T) {
	points := generationPoints(MetricLoss)
	for _, pair := range [][2]string{{"node-a", "8"}, {"node-b", "7"}, {"node-b", ""}} {
		for i := 0; i < 6; i++ {
			points = append(points, metric.AggregatePoint{EntityID: pair[0], Bucket: at(i),
				Value: 0, Count: 1, Tags: map[string]string{"task_id": pair[1]}})
		}
	}
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{MetricLoss: {metric.AggAvg: points}}}
	report, err := Build(context.Background(), ReportRequest{Reader: reader, EntityIDs: []string{"node-a", "node-b", "empty"},
		Start: at(0), End: at(6), Interval: time.Minute, Now: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Nodes) != 3 || len(report.Nodes[0].Tasks) != 2 || len(report.Nodes[1].Tasks) != 2 || report.Nodes[2].HasReport {
		t.Fatalf("entity/task isolation changed: %+v", report.Nodes)
	}
	for i, node := range report.Nodes[:2] {
		for _, task := range node.Tasks {
			if i == 0 && task.TaskID == "7" {
				continue
			}
			if task.Loss.Buckets != 6 || task.Loss.Lost != 0 || task.Loss.Fraction != 1 {
				t.Errorf("unrelated task changed: %+v", task)
			}
		}
	}
}

func TestDedupeIgnoresEmptyValuesRegardlessOfGenerationOrder(t *testing.T) {
	for _, samples := range [][]Sample{
		{{Bucket: at(0), Value: -1, Count: 1}, {Bucket: at(0), Value: 0, Count: 0}},
		{{Bucket: at(0), Value: 0, Count: 0}, {Bucket: at(0), Value: -1, Count: 1}},
		{{Bucket: at(0), Value: 999, Count: 0}, {Bucket: at(0), Value: 25, Count: 2}},
		{{Bucket: at(0), Value: 25, Count: 2}, {Bucket: at(0), Value: 999, Count: 0}},
	} {
		merged := dedupe(samples)
		want := samples[0]
		if want.Count == 0 {
			want = samples[1]
		}
		if len(merged) != 1 || merged[0].Value != want.Value || merged[0].Count != want.Count {
			t.Errorf("dedupe(%+v)=%+v, want measured %+v", samples, merged, want)
		}
	}
}

func TestBuildGenerationMergeIsOrderIndependent(t *testing.T) {
	points := generationPoints(MetricLoss)
	read := func(points []metric.AggregatePoint) Report {
		reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{MetricLoss: {metric.AggAvg: points}}}
		report, err := Build(context.Background(), ReportRequest{Reader: reader, EntityIDs: []string{"node-a"},
			Start: at(0), End: at(6), Interval: time.Minute, Now: base})
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	want := read(points)
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	if got := read(points); !reflect.DeepEqual(got, want) {
		t.Fatalf("generation order changes the report: got %+v, want %+v", got, want)
	}
}

func TestBuildSortsNonoverlappingTaskGenerations(t *testing.T) {
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {metric.AggAvg: {
			{EntityID: "node-a", Bucket: at(2), Value: 1, Count: 1, Tags: map[string]string{"task_id": "7"}},
			{EntityID: "node-a", Bucket: at(0), Value: 1, Count: 1, Tags: map[string]string{"task_id": "7", "protocol": "icmp"}},
			{EntityID: "node-a", Bucket: at(1), Value: 1, Count: 1, Tags: map[string]string{"task_id": "7", "protocol": "icmp"}},
		}},
	}}
	report, err := Build(context.Background(), ReportRequest{Reader: reader, EntityIDs: []string{"node-a"},
		Start: at(0), End: at(3), Interval: time.Minute, Now: base})
	if err != nil {
		t.Fatal(err)
	}
	task := report.Nodes[0].Tasks[0]
	if len(task.Outages) != 1 || !task.Outages[0].Start.Equal(at(0)) || !task.Outages[0].End.Equal(at(2)) ||
		task.Outages[0].Buckets != 3 || task.Outages[0].Duration != 2*time.Minute {
		t.Fatalf("nonoverlapping generations not chronological: %+v", task.Outages)
	}
}
