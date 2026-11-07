package metric

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in diagnostic for the extra ancestor lookups and view-lock hold time.
// It is intentionally synthetic; it does not replace an OC424 load test.
func TestCoarseQueryCurrentFleetShapeProbe(t *testing.T) {
	if os.Getenv("METRIC_COARSE_SCALE_PROBE") != "1" {
		t.Skip("set METRIC_COARSE_SCALE_PROBE=1 to run the synthetic fleet probe")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 10 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 50 * time.Hour},
		{Interval: time.Hour, Retention: 600 * time.Hour},
		{Interval: 24 * time.Hour, Retention: 90 * 24 * time.Hour},
	}, Compression: 30}
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "scale.db"), WithMaxOpenConns(1), WithSQLiteReadPool(2), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const metricCount, nodes = 34, 14
	for m := 0; m < metricCount; m++ {
		if err := s.CreateMetric(ctx, Definition{Name: fmt.Sprintf("m%02d", m), RetentionDays: 90}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	points := make([]Point, 0, metricCount*nodes)
	// Advance minute by minute so the fixture stages both open five-minute and
	// hourly parents instead of backfilling only the last grace window.
	for minute := 0; minute <= 90; minute++ {
		points = points[:0]
		for m := 0; m < metricCount; m++ {
			for n := 0; n < nodes; n++ {
				points = append(points, Point{MetricName: fmt.Sprintf("m%02d", m), EntityID: fmt.Sprintf("node-%02d", n), Timestamp: base.Add(time.Duration(minute) * time.Minute), Value: float64(m + n + minute + 1)})
			}
		}
		now := base.Add(time.Duration(minute) * time.Minute)
		if err := s.writeBatch(ctx, points, func() time.Time { return now }); err != nil {
			t.Fatal(err)
		}
	}
	shape := snapshotCoarseRollups(s, metricCount*nodes)
	if shape.entries == 0 || shape.children == 0 {
		t.Fatalf("no pending rollup tree staged: %+v", shape)
	}
	t.Logf("synthetic shape: %d metrics x %d nodes, %d series, %d staged parents, %d children by tier=%v", metricCount, nodes, metricCount*nodes, shape.entries, shape.children, shape.childrenByTier)
	for _, scope := range []struct {
		name      string
		metricNum int
		entities  []string
	}{
		{"one-node-14-metrics", 14, []string{"node-00"}},
		{"fleet-one-metric", 1, nil},
		{"fleet-all-metrics", metricCount, nil},
	} {
		t.Run(scope.name, func(t *testing.T) {
			for _, resolution := range []time.Duration{5 * time.Minute, time.Hour, 24 * time.Hour} {
				specs := make([]BatchSeriesSpec, 0, scope.metricNum)
				forced := make(map[string]time.Duration, scope.metricNum)
				for m := 0; m < scope.metricNum; m++ {
					name := fmt.Sprintf("m%02d", m)
					specs = append(specs, BatchSeriesSpec{MetricName: name, Aggregations: []Aggregation{AggAvg}, Interval: resolution, PreserveSeries: true})
					forced[name] = resolution
				}
				query := BatchSeriesQuery{Specs: specs, EntityIDs: scope.entities, Start: base, End: base.Add(90 * time.Minute)}.normalized()
				for i := 0; i < 3; i++ {
					started := time.Now()
					result, err := s.seriesBatchAt(WithReadBudget(ctx, 100000), query, base.Add(90*time.Minute), forced)
					if err != nil {
						t.Fatal(err)
					}
					if len(result.Values) != scope.metricNum {
						t.Fatalf("unexpected result count %d", len(result.Values))
					}
					t.Logf("%s run %d: %s", resolution, i+1, time.Since(started))
				}
			}
		})
	}
}
