package sla

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// These tests pin the maintenance-window half of the report. The definition they
// protect: time inside a window is not evidence, so it leaves the availability
// fraction, the coverage denominator, the latency percentiles and the incident list
// together. Outside the window every number is the one the pre-window code produced.

func TestMaintenanceWindowIsRemovedFromAvailabilityAndCoverage(t *testing.T) {
	// Minutes 3, 4 and 5 are a full outage, and that is exactly the scheduled reboot.
	samples := series(0, 0, 0, 1, 1, 1, 0, 0, 0, 0)
	exclusions := []Interval{{Start: at(3), End: at(6)}}

	legacy := AvailabilityFromLoss(samples, 10*time.Minute)
	if !almostEqual(legacy.Fraction, 0.7) || legacy.Lost != 3 || legacy.Buckets != 10 {
		t.Fatalf("legacy availability = %+v, want 70%% with 3 lost of 10", legacy)
	}

	got := AvailabilityFromLossExcluding(samples, 10*time.Minute, exclusions, time.Minute)
	if !got.HasData {
		t.Fatal("data outside the window must still produce a figure")
	}
	if !almostEqual(got.Fraction, 1) {
		t.Fatalf("available inside the report = %v, want 1: the outage was inside the window", got.Fraction)
	}
	if got.Lost != 0 || got.Buckets != 7 {
		t.Fatalf("buckets = %d lost = %d, want 7 measured and 0 lost", got.Buckets, got.Lost)
	}
	if got.Presence.ExpectedBuckets != 7 || got.Presence.ObservedBuckets != 7 || !almostEqual(got.Presence.Coverage, 1) {
		t.Fatalf("presence = %+v, want 7/7: the window leaves both sides of the ratio", got.Presence)
	}
}

func TestOutsideWindowTheSameLossStillCounts(t *testing.T) {
	samples := series(1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
	exclusions := []Interval{{Start: at(3), End: at(6)}}

	got := AvailabilityFromLossExcluding(samples, 10*time.Minute, exclusions, time.Minute)
	if !got.HasData || got.Buckets != 7 || got.Lost != 7 {
		t.Fatalf("availability = %+v, want all 7 remaining buckets failed", got)
	}
	if got.Fraction != 0 {
		t.Fatalf("fraction = %v, want 0: loss outside a window is still downtime", got.Fraction)
	}
}

func TestFullyCoveredWindowHasNoDataRatherThanPerfectAvailability(t *testing.T) {
	samples := series(0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	exclusions := []Interval{{Start: at(0), End: at(10)}}

	availability := AvailabilityFromLossExcluding(samples, 10*time.Minute, exclusions, time.Minute)
	if availability.HasData {
		t.Fatalf("a fully excluded window has no evidence, got %+v", availability)
	}
	if availability.Presence.ExpectedBuckets != 0 || availability.Presence.ObservedBuckets != 0 || availability.Presence.Coverage != 0 {
		t.Fatalf("presence = %+v, want all zero", availability.Presence)
	}
	if incidents := IncidentsFromLossExcluding(samples, 0.5, exclusions, time.Minute); len(incidents) != 0 {
		t.Fatalf("incidents = %+v, want none", incidents)
	}
	if latency := LatencyFromSamplesExcluding([]Sample{{Bucket: at(1), Value: 10, Count: 1}}, exclusions, time.Minute); latency.HasData {
		t.Fatalf("latency = %+v, want no data", latency)
	}

	node := BuildNodeReportExcluding("node-a", samples, map[string][]Sample{"1": samples},
		map[string][]Sample{"1": {{Bucket: at(1), Value: 10, Count: 1}}}, 10*time.Minute, time.Minute, exclusions)
	if node.HasReport {
		t.Fatalf("node report = %+v, want no report", node)
	}
	if node.MaintenanceExcludedSeconds != 600 {
		t.Fatalf("maintenance_excluded_seconds = %d, want 600", node.MaintenanceExcludedSeconds)
	}
}

func TestIncidentsDoNotMergeAcrossAMaintenanceWindow(t *testing.T) {
	// Failures from minute 1 through 8; the reboot is minutes 3, 4 and 5.
	samples := series(0, 1, 1, 1, 1, 1, 1, 1, 1, 0)
	exclusions := []Interval{{Start: at(3), End: at(6)}}

	legacy := IncidentsFromLoss(samples, 0.5)
	if len(legacy) != 1 || legacy[0].Buckets != 8 {
		t.Fatalf("legacy incidents = %+v, want one 8-bucket run", legacy)
	}

	got := IncidentsFromLossExcluding(samples, 0.5, exclusions, time.Minute)
	if len(got) != 2 {
		t.Fatalf("incidents = %+v, want two: the window ends a run rather than merging across it", got)
	}
	if got[0].Start != at(1) || got[0].End != at(2) || got[0].Buckets != 2 {
		t.Fatalf("first incident = %+v, want minutes 1-2", got[0])
	}
	if got[1].Start != at(6) || got[1].End != at(8) || got[1].Buckets != 3 {
		t.Fatalf("second incident = %+v, want minutes 6-8", got[1])
	}
	for _, incident := range got {
		if incident.Start.Before(exclusions[0].Start) && incident.End.After(exclusions[0].Start) {
			t.Fatalf("incident %+v spans the window", incident)
		}
	}
}

func TestBucketBoundariesAreHalfOpen(t *testing.T) {
	window := []Interval{{Start: at(3), End: at(5)}}
	cases := []struct {
		name   string
		bucket time.Time
		width  time.Duration
		want   bool
	}{
		{"first bucket of the window", at(3), time.Minute, true},
		{"bucket ending at the window start", at(2), time.Minute, false},
		{"bucket starting at the window end", at(5), time.Minute, false},
		{"last bucket of the window", at(4), time.Minute, true},
		{"partial overlap inside", at(3).Add(30 * time.Second), time.Minute, true},
		{"partial overlap before", at(2).Add(30 * time.Second), time.Minute, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := bucketIntersects(test.bucket, test.width, window); got != test.want {
				t.Fatalf("bucketIntersects(%s, %s) = %v, want %v", test.bucket, test.width, got, test.want)
			}
		})
	}
}

func TestExcludedLatencyBucketsDoNotDragThePercentiles(t *testing.T) {
	// A slow upgrade inside the window must not become the report's p95.
	samples := series(10, 10, 10, 500, 500, 500, 10, 10, 10)
	exclusions := []Interval{{Start: at(3), End: at(6)}}

	legacy := LatencyFromSamples(samples)
	if legacy.P95 <= 10 {
		t.Fatalf("legacy p95 = %v, want the slow buckets included", legacy.P95)
	}
	got := LatencyFromSamplesExcluding(samples, exclusions, time.Minute)
	if !got.HasData || got.Buckets != 6 {
		t.Fatalf("latency = %+v, want the 6 measured buckets", got)
	}
	if !almostEqual(got.P50, 10) || !almostEqual(got.P95, 10) || !almostEqual(got.P99, 10) {
		t.Fatalf("latency = %+v, want 10ms percentiles from the clean buckets", got)
	}
}

func TestNoExclusionsProducesTheLegacyReport(t *testing.T) {
	samples := series(0, 1, gap, 0, 1, 0)
	const window = 6 * time.Minute

	legacyAvailability := AvailabilityFromLoss(samples, window)
	excludedAvailability := AvailabilityFromLossExcluding(samples, window, nil, time.Minute)
	if !reflect.DeepEqual(legacyAvailability, excludedAvailability) {
		t.Fatalf("availability changed with no exclusions: legacy=%+v got=%+v", legacyAvailability, excludedAvailability)
	}

	legacyPresence := PresenceFromSamples(samples, window)
	excludedPresence := PresenceFromSamplesExcluding(samples, window, nil, time.Minute)
	if !reflect.DeepEqual(legacyPresence, excludedPresence) {
		t.Fatalf("presence changed with no exclusions: legacy=%+v got=%+v", legacyPresence, excludedPresence)
	}

	legacyIncidents := IncidentsFromLoss(samples, 0.5)
	excludedIncidents := IncidentsFromLossExcluding(samples, 0.5, nil, time.Minute)
	if !reflect.DeepEqual(legacyIncidents, excludedIncidents) {
		t.Fatalf("incidents changed with no exclusions: legacy=%+v got=%+v", legacyIncidents, excludedIncidents)
	}

	legacyLatency := LatencyFromSamples(samples)
	excludedLatency := LatencyFromSamplesExcluding(samples, nil, time.Minute)
	if !reflect.DeepEqual(legacyLatency, excludedLatency) {
		t.Fatalf("latency changed with no exclusions: legacy=%+v got=%+v", legacyLatency, excludedLatency)
	}

	legacyNode := BuildNodeReport("node-a", samples, map[string][]Sample{"1": samples}, nil, window)
	excludedNode := BuildNodeReportExcluding("node-a", samples, map[string][]Sample{"1": samples}, nil, window, time.Minute, nil)
	if !reflect.DeepEqual(legacyNode, excludedNode) {
		t.Fatalf("node report changed with no exclusions: legacy=%+v got=%+v", legacyNode, excludedNode)
	}
}

func TestNormalizeIntervalsClipsAndMerges(t *testing.T) {
	start, end := at(0), at(10)
	intervals := []Interval{
		{Start: at(-5), End: at(2)},  // clipped at the report start
		{Start: at(1), End: at(4)},   // overlaps the first
		{Start: at(6), End: at(15)},  // clipped at the report end
		{Start: at(8), End: at(8)},   // empty, dropped
		{Start: at(12), End: at(13)}, // entirely outside, dropped
	}

	got := normalizeIntervals(start, end, intervals)
	want := []Interval{
		{Start: at(0), End: at(4)},
		{Start: at(6), End: at(10)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeIntervals = %+v, want %+v", got, want)
	}
	if seconds := excludedSeconds(got); seconds != int64(8*time.Minute/time.Second) {
		t.Fatalf("excludedSeconds = %d, want %d", seconds, int64(8*time.Minute/time.Second))
	}
}

func TestBuildAppliesPerEntityExclusions(t *testing.T) {
	lossPoints := func() []metric.AggregatePoint {
		points := make([]metric.AggregatePoint, 0, 6)
		for i := 0; i < 6; i++ {
			value := 0.0
			if i >= 2 && i <= 4 {
				value = 1
			}
			points = append(points, metric.AggregatePoint{
				EntityID: "node-a", Bucket: at(i), Value: value, Count: 5,
				Tags: map[string]string{"task_id": "1"},
			})
		}
		return points
	}
	nodeBPoints := lossPoints()
	for i := range nodeBPoints {
		nodeBPoints[i].EntityID = "node-b"
	}
	reader := &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
		MetricLoss: {metric.AggAvg: append(lossPoints(), nodeBPoints...)},
	}}

	report, err := Build(context.Background(), ReportRequest{
		Reader:     reader,
		EntityIDs:  []string{"node-a", "node-b"},
		Start:      at(0),
		End:        at(6),
		Interval:   time.Minute,
		Exclusions: map[string][]Interval{"node-a": {{Start: at(2), End: at(5)}}},
		Now:        base,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(report.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want two", report.Nodes)
	}

	nodeA := report.Nodes[0]
	if nodeA.EntityID != "node-a" || len(nodeA.Tasks) != 1 {
		t.Fatalf("node-a = %+v", nodeA)
	}
	task := nodeA.Tasks[0]
	if !task.Loss.HasData || !almostEqual(task.Loss.Fraction, 1) || task.Loss.Buckets != 3 {
		t.Fatalf("node-a loss = %+v, want 100%% over the 3 non-maintenance buckets", task.Loss)
	}
	if nodeA.MaintenanceExcludedSeconds != 180 {
		t.Fatalf("node-a excluded = %d, want 180", nodeA.MaintenanceExcludedSeconds)
	}
	if nodeA.Presence.ExpectedBuckets != 3 || nodeA.Presence.ObservedBuckets != 3 {
		t.Fatalf("node-a presence = %+v, want 3/3", nodeA.Presence)
	}

	nodeB := report.Nodes[1]
	if nodeB.EntityID != "node-b" || len(nodeB.Tasks) != 1 {
		t.Fatalf("node-b = %+v", nodeB)
	}
	if !almostEqual(nodeB.Tasks[0].Loss.Fraction, 0.5) || nodeB.Tasks[0].Loss.Lost != 3 {
		t.Fatalf("node-b loss = %+v, want the unexcluded 50%%", nodeB.Tasks[0].Loss)
	}
	if nodeB.MaintenanceExcludedSeconds != 0 {
		t.Fatalf("node-b excluded = %d, want 0", nodeB.MaintenanceExcludedSeconds)
	}

	encoded, err := json.Marshal(nodeB)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "maintenance_excluded_seconds") {
		t.Fatalf("node-b must omit the excluded field: %s", encoded)
	}
	encoded, err = json.Marshal(nodeA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"maintenance_excluded_seconds":180`) {
		t.Fatalf("node-a must report the excluded seconds: %s", encoded)
	}
}

// A window that does not touch the report changes nothing: the numbers, and the wire
// shape, are the ones the report produced before maintenance windows existed. This is
// the "outside a window the behaviour is unchanged" half of the contract.
func TestWindowsOutsideTheReportDoNotChangeTheReport(t *testing.T) {
	samples := series(0, 1, gap, 0, 1, 0)
	later := []Interval{{Start: at(20), End: at(30)}}
	const window = 6 * time.Minute

	if legacy, got := AvailabilityFromLoss(samples, window), AvailabilityFromLossExcluding(samples, window, later, time.Minute); !reflect.DeepEqual(legacy, got) {
		t.Fatalf("availability changed by a window after the data: legacy=%+v got=%+v", legacy, got)
	}
	if legacy, got := PresenceFromSamples(samples, window), PresenceFromSamplesExcluding(samples, window, later, time.Minute); !reflect.DeepEqual(legacy, got) {
		t.Fatalf("presence changed by a window after the data: legacy=%+v got=%+v", legacy, got)
	}
	if legacy, got := IncidentsFromLoss(samples, 0.5), IncidentsFromLossExcluding(samples, 0.5, later, time.Minute); !reflect.DeepEqual(legacy, got) {
		t.Fatalf("incidents changed by a window after the data: legacy=%+v got=%+v", legacy, got)
	}
	if legacy, got := LatencyFromSamples(samples), LatencyFromSamplesExcluding(samples, later, time.Minute); !reflect.DeepEqual(legacy, got) {
		t.Fatalf("latency changed by a window after the data: legacy=%+v got=%+v", legacy, got)
	}

	points := make([]metric.AggregatePoint, 0, 6)
	for i := 0; i < 6; i++ {
		value := 0.0
		if i == 1 || i == 4 {
			value = 1
		}
		points = append(points, metric.AggregatePoint{
			EntityID: "node-a", Bucket: at(i), Value: value, Count: 5,
			Tags: map[string]string{"task_id": "1"},
		})
	}
	request := func(exclusions map[string][]Interval) Report {
		report, err := Build(context.Background(), ReportRequest{
			Reader:     &fakeBatchReader{values: map[string]map[metric.Aggregation][]metric.AggregatePoint{MetricLoss: {metric.AggAvg: points}}},
			EntityIDs:  []string{"node-a"},
			Start:      at(0),
			End:        at(6),
			Interval:   time.Minute,
			Exclusions: exclusions,
			Now:        base,
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return report
	}
	without := request(nil)
	with := request(map[string][]Interval{"node-a": later})
	if !reflect.DeepEqual(without, with) {
		t.Fatalf("Build changed by an out-of-window exclusion: without=%+v with=%+v", without, with)
	}
	if with.Nodes[0].MaintenanceExcludedSeconds != 0 {
		t.Fatalf("excluded seconds = %d, want 0: the window was outside the report", with.Nodes[0].MaintenanceExcludedSeconds)
	}
}
