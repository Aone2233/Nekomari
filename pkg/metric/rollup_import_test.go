package metric

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestReplaceRollupPointsIsIdempotentAndSkipsRawWindow(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	store := newRollupStore(t, policy)
	if err := store.UpsertMetric(ctx, Definition{Name: "legacy.p95", RetentionDays: 365}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	points := []Point{
		{MetricName: "legacy.p95", EntityID: "node-a", Timestamp: base, Value: 95},
		{MetricName: "legacy.p95", EntityID: "node-a", Timestamp: base.Add(time.Hour), Value: 195},
	}
	for i := 0; i < 2; i++ {
		if err := store.ReplaceRollupPoints(ctx, time.Hour, points); err != nil {
			t.Fatalf("replace hourly points pass %d: %v", i+1, err)
		}
	}
	if err := store.RebuildCoarserRollups(ctx, time.Hour); err != nil {
		t.Fatalf("rebuild daily rollups: %v", err)
	}

	raw, err := store.Query(ctx, Query{MetricName: "legacy.p95", EntityID: "node-a", Start: base.Add(-time.Minute), End: base.Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("query raw window: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("pre-aggregated import entered raw window: %#v", raw)
	}
	minute, err := store.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "legacy.p95", EntityID: "node-a", Start: base.Add(-time.Minute), End: base.Add(time.Minute)},
		Aggregation: AggLast, Interval: time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("query minute compatibility import: %v", err)
	}
	if len(minute) != 1 || minute[0].Count != 1 || minute[0].Value != 95 {
		t.Fatalf("minute compatibility import = %#v", minute)
	}
	hourly, err := store.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "legacy.p95", EntityID: "node-a", Start: base, End: base.Add(2 * time.Hour), Order: OrderAsc},
		Aggregation: AggLast, Interval: time.Hour,
	}, time.Hour)
	if err != nil {
		t.Fatalf("query hourly import: %v", err)
	}
	if len(hourly) != 2 || hourly[0].Count != 1 || hourly[0].Value != 95 || hourly[1].Count != 1 || hourly[1].Value != 195 {
		t.Fatalf("idempotent hourly import = %#v", hourly)
	}
	daily, err := store.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "legacy.p95", EntityID: "node-a", Start: base.Truncate(24 * time.Hour), End: base.Add(24 * time.Hour)},
		Aggregation: AggAvg, Interval: 24 * time.Hour,
	}, 24*time.Hour)
	if err != nil {
		t.Fatalf("query rebuilt daily import: %v", err)
	}
	if len(daily) != 1 || daily[0].Count != 2 || daily[0].Value != 145 {
		t.Fatalf("rebuilt daily import = %#v", daily)
	}
}

func TestRebuildCoarserRollupsDoesNotOverwriteRetainedHistory(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: time.Hour},
		{Interval: time.Hour, Retention: 48 * time.Hour},
		{Interval: 24 * time.Hour, Retention: 365 * 24 * time.Hour},
	}}
	store := newRollupStore(t, policy)
	for _, name := range []string{"a-first", "z-conflict"} {
		if err := store.CreateMetric(ctx, Definition{Name: name, RetentionDays: 365}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	created := base.Add(48 * time.Hour)
	rows := []PersistedRollup{
		constantTransferRollup("a-first", "node", base, time.Hour, 5, created),
		constantTransferRollup("z-conflict", "node", base.Add(time.Hour), time.Hour, 10, created),
		constantTransferRollup("z-conflict", "node", base, 24*time.Hour, 30, created),
	}
	if err := store.ImportRollups(ctx, rows); err != nil {
		t.Fatal(err)
	}
	before, _ := exportAllTestRollups(t, store, "z-conflict", 10)
	if err := store.RebuildCoarserRollups(ctx, time.Hour); !errors.Is(err, ErrRebuildConflict) {
		t.Fatalf("rebuild with retained parent = %v", err)
	}
	after, _ := exportAllTestRollups(t, store, "z-conflict", 10)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rebuild changed retained history: before=%+v after=%+v", before, after)
	}
	first, _ := exportAllTestRollups(t, store, "a-first", 10)
	if len(first) != 1 || first[0].Resolution != time.Hour {
		t.Fatalf("conflicting rebuild committed an earlier metric: %+v", first)
	}
}
