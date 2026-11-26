package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// Expired data must be absent, not a stale value and not a fabricated zero.
func TestExpiredMetricDataIsAbsentNotZero(t *testing.T) {
	ctx := context.Background()
	policy := metric.RollupPolicy{
		Tiers: []metric.RollupTier{{Interval: time.Minute, Retention: time.Hour}},
	}
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1), metric.WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, metric.Definition{Name: "probe.expiry", Type: metric.TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	old := now.Add(-48 * time.Hour)
	if err := s.Write(ctx, metric.Point{MetricName: "probe.expiry", EntityID: "node-a", Timestamp: old, Value: 42}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatal(err)
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: "probe.expiry", EntityID: "node-a",
		Start: now.Add(-72 * time.Hour), End: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 0 {
		t.Fatalf("expired data still returned: %#v", points)
	}
}
