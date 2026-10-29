package metric

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestSeriesBatchAlignedStartExcludesCrossingBackingBuckets(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: time.Hour},
		{Interval: 5 * time.Minute, Retention: 72 * time.Hour},
	}}
	s := newRollupStore(t, policy)
	for _, name := range []string{"aligned", "legacy"} {
		if err := s.CreateMetric(ctx, Definition{Name: name, Type: TypeGauge, RetentionDays: 90}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2026, 10, 2, 0, 2, 0, 0, time.UTC)
	now := start.Add(25 * time.Hour)
	var rows []PersistedRollup
	for _, name := range []string{"aligned", "legacy"} {
		rows = append(rows,
			constantTransferRollup(name, "node", start.Truncate(5*time.Minute), 5*time.Minute, 1000, now),
			constantTransferRollup(name, "node", start.Truncate(5*time.Minute).Add(5*time.Minute), 5*time.Minute, 100, now),
		)
	}
	if err := s.ImportRollups(ctx, rows); err != nil {
		t.Fatal(err)
	}
	query := BatchSeriesQuery{Specs: []BatchSeriesSpec{
		{MetricName: "legacy", Aggregations: []Aggregation{AggMax}, Interval: 7 * time.Hour},
		{MetricName: "aligned", Aggregations: []Aggregation{AggMax}, Interval: 7 * time.Hour, AlignedStart: true},
	}, EntityIDs: []string{"node"}, Start: start, End: now}
	result, err := s.SeriesBatch(ctx, query, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolutions["aligned"] != 5*time.Minute || result.Resolutions["legacy"] != 5*time.Minute {
		t.Fatalf("wrong backing resolutions: %v", result.Resolutions)
	}
	legacy := result.Values["legacy"][AggMax]
	if len(legacy) != 1 || legacy[0].Value != 1000 || !legacy[0].Bucket.Before(start) {
		t.Fatalf("legacy overlap semantics changed: %+v", legacy)
	}
	aligned := result.Values["aligned"][AggMax]
	if len(aligned) != 1 || aligned[0].Value != 100 || !aligned[0].Bucket.Equal(start) {
		t.Fatalf("aligned output = %+v; want only the wholly in-cycle backing bucket at %s", aligned, start)
	}
	// Two dictionary rows and three persisted rows must fit; including the
	// aligned crossing row would require a sixth budget item.
	if _, err := s.SeriesBatch(WithReadBudget(ctx, 5), query, now); err != nil {
		t.Fatalf("two series and three persisted rows should fit: %v", err)
	}
	if _, err := s.SeriesBatch(WithReadBudget(ctx, 4), query, now); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("four items should exceed the read budget: %v", err)
	}
	rateQuery := query
	rateQuery.Specs = []BatchSeriesSpec{{MetricName: "aligned", Aggregations: []Aggregation{AggRate}, Interval: 7 * time.Hour, AlignedStart: true}}
	rateResult, err := s.SeriesBatch(ctx, rateQuery, now)
	if err != nil {
		t.Fatal(err)
	}
	rate := rateResult.Values["aligned"][AggRate]
	if len(rate) != 1 || !rate[0].Bucket.Equal(start) {
		t.Fatalf("aligned rate bucket = %+v; want %s", rate, start)
	}
	// No alignment is lost when the cycle starts exactly on the backing tier.
	exactQuery := query
	exactQuery.Start = start.Truncate(5 * time.Minute)
	exact, err := s.SeriesBatch(ctx, exactQuery, now)
	if err != nil {
		t.Fatal(err)
	}
	if points := exact.Values["aligned"][AggMax]; len(points) != 1 || points[0].Value != 1000 || !points[0].Bucket.Equal(exactQuery.Start) {
		t.Fatalf("exact backing boundary = %+v", points)
	}
}

func TestSeriesBatchAlignedStartSelectsHotAndCoarseBuckets(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: time.Hour},
		{Interval: 5 * time.Minute, Retention: 72 * time.Hour},
	}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "hot", Type: TypeGauge, RetentionDays: 90}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(5 * time.Minute).Add(-10 * time.Minute)
	start, now := base.Add(2*time.Minute+30*time.Second), base.Add(9*time.Minute)
	writeRollupPointsAt(t, ctx, s, now,
		Point{MetricName: "hot", EntityID: "node", Timestamp: start.Add(-20 * time.Second), Value: 1000},
		Point{MetricName: "hot", EntityID: "node", Timestamp: start.Add(10 * time.Second), Value: 2000},
		Point{MetricName: "hot", EntityID: "node", Timestamp: base.Add(3*time.Minute + 10*time.Second), Value: 100},
		Point{MetricName: "hot", EntityID: "node", Timestamp: base.Add(5*time.Minute + 10*time.Second), Value: 200},
	)
	query := BatchSeriesQuery{Specs: []BatchSeriesSpec{{MetricName: "hot", Aggregations: []Aggregation{AggMax}, Interval: 7 * time.Hour, AlignedStart: true}}, EntityIDs: []string{"node"}, Start: start, End: now}
	minute, err := s.seriesBatchAt(ctx, query.normalized(), now, map[string]time.Duration{"hot": time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if minute.Resolutions["hot"] != time.Minute {
		t.Fatalf("minute backing not selected: %v", minute.Resolutions)
	}
	points := minute.Values["hot"][AggMax]
	if len(points) != 1 || points[0].Value != 200 || !points[0].Bucket.Equal(start) {
		t.Fatalf("hot minute output = %+v; crossing minute must be excluded", points)
	}
	// Exercise the same boundary using a live five-minute parent.
	coarse, err := s.seriesBatchAt(ctx, query.normalized(), now, map[string]time.Duration{"hot": 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if coarse.Resolutions["hot"] != 5*time.Minute {
		t.Fatalf("coarse backing not selected: %v", coarse.Resolutions)
	}
	points = coarse.Values["hot"][AggMax]
	if len(points) != 1 || points[0].Value != 200 || !points[0].Bucket.Equal(start) {
		t.Fatalf("coarse output = %+v; crossing parent must be excluded", points)
	}
}

func TestAlignedRollupStartRounding(t *testing.T) {
	base := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		start      time.Time
		aligned    bool
		resolution time.Duration
		want       time.Time
	}{
		{"exact boundary", base, true, 5 * time.Minute, base},
		{"interior", base.Add(2 * time.Minute), true, 5 * time.Minute, base.Add(5 * time.Minute)},
		{"sub-millisecond", base.Add(time.Nanosecond), true, 5 * time.Minute, base.Add(5 * time.Minute)},
		{"legacy overlap", base.Add(2 * time.Minute), false, 5 * time.Minute, base},
		{"negative epoch", time.Unix(-1, 999999999), true, time.Minute, time.Unix(0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rollupStartMillis(tc.start, tc.resolution.Milliseconds(), tc.aligned)
			if got != tc.want.UnixMilli() {
				t.Fatalf("got %s, want %s", time.UnixMilli(got), tc.want)
			}
		})
	}
	// Guard arithmetic even for artificial dates beyond ordinary application use.
	if got := rollupStartMillis(time.UnixMilli(math.MaxInt64), 60000, true); got != math.MaxInt64 {
		t.Fatalf("overflowing next boundary = %d", got)
	}
}
