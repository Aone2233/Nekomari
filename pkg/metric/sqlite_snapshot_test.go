package metric

import (
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteSnapshotHotReplacementAndContinuedWrites(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "live.db")
	s, err := Open(ctx, SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "snapshot", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(30 * time.Second).Truncate(time.Millisecond)
	point := Point{MetricName: "snapshot", EntityID: "node", Timestamp: at, Value: 10}
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	// Persist while retaining raw samples, as happens when a late exact upsert
	// rebuilds a bucket that already has a durable version.
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	point.Value = 20
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, snapshot, "snapshot", time.Minute, 1, 20)
	assertSnapshotAggregate(t, snapshot, "snapshot", 5*time.Minute, at, 1, 20)
	assertLiveRaw(t, s, "snapshot", at, []float64{20})

	point.Timestamp = at.Add(time.Second)
	point.Value = 3
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	assertLiveRaw(t, s, "snapshot", at, []float64{20, 3})
	assertSnapshotSummary(t, snapshot, "snapshot", time.Minute, 1, 20)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "snapshot", time.Minute, 2, 23)
}

// A restored snapshot must continue accumulating the same coarse window
// rather than replace the part captured before the snapshot.
func TestSQLiteSnapshotRestoreContinuesOpenCoarseWindow(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
	}, Compression: 30}
	live, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	if err := live.CreateMetric(ctx, Definition{Name: "continue", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	// Future times keep the parent's grace window open during this hermetic test.
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	writeRollupPointsAt(t, ctx, live, base.Add(2*time.Minute), Point{
		MetricName: "continue", EntityID: "node", Timestamp: base.Add(10 * time.Second), Value: 10,
	})
	path := filepath.Join(t.TempDir(), "restore.db")
	if err := live.SnapshotSQLite(ctx, path); err != nil {
		t.Fatal(err)
	}
	var integrity string
	check, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if err := check.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = check.Close()
		t.Fatalf("snapshot integrity = %q, %v", integrity, err)
	}
	if err := check.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	assertStoreSnapshotAggregate(t, restored, "continue", 5*time.Minute, base, 1, 10)
	writeRollupPointsAt(t, ctx, restored, base.Add(4*time.Minute), Point{
		MetricName: "continue", EntityID: "node", Timestamp: base.Add(2*time.Minute + 10*time.Second), Value: 20,
	})
	if _, err := restored.FlushCoarse(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, resolution := range []time.Duration{time.Minute, 5 * time.Minute, time.Hour} {
		rows, err := restored.scanRollupRows(ctx, restored.reader(), "continue", resolution)
		if err != nil {
			t.Fatal(err)
		}
		var got totals
		for _, row := range rows {
			got.count += row.bucketData.count
			got.sum += row.bucketData.sum
		}
		if got != (totals{count: 2, sum: 30}) {
			t.Errorf("%s after restore = %+v, want 2/30", resolution, got)
		}
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err = Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	assertStoreSnapshotAggregate(t, restored, "continue", time.Hour, base, 2, 30)
}

// Persisting a partial minute and then appending another observation must keep
// the parent's child equal to the complete durable minute, not just the delta.
func TestSQLiteSnapshotAfterPartialMinutePersistence(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
	}, Compression: 30}
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "partial-minute", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	if err := s.Write(ctx, Point{MetricName: "partial-minute", EntityID: "node", Timestamp: base.Add(10 * time.Second), Value: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, Point{MetricName: "partial-minute", EntityID: "node", Timestamp: base.Add(20 * time.Second), Value: 20}); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	assertStoreSnapshotAggregate(t, s, "partial-minute", 5*time.Minute, base, 2, 30)
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, path); err != nil {
		t.Fatalf("snapshot after partial-minute persistence: %v", err)
	}
	assertSnapshotSummary(t, path, "partial-minute", time.Minute, 2, 30)
	assertSnapshotAggregate(t, path, "partial-minute", 5*time.Minute, base, 2, 30)
}

// Recovery must not expose an incomplete higher tier before the reconstructed
// finer parent has reached its normal sealing time.
func TestSQLiteSnapshotRestoreExposesOpenHigherTier(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
	}, Compression: 30}
	live, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	if err := live.CreateMetric(ctx, Definition{Name: "higher", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	writeRollupPointsAt(t, ctx, live, base.Add(2*time.Minute), Point{
		MetricName: "higher", EntityID: "node", Timestamp: base.Add(10 * time.Second), Value: 10,
	})
	path := filepath.Join(t.TempDir(), "restore.db")
	if err := live.SnapshotSQLite(ctx, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	assertStoreSnapshotAggregate(t, restored, "higher", time.Hour, base, 1, 10)
}

func TestSQLiteSnapshotRestoreCombinesSealedAndOpenChildren(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
	}, Compression: 30}
	live, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	if err := live.CreateMetric(ctx, Definition{Name: "mixed", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	writeRollupPointsAt(t, ctx, live, base.Add(21*time.Minute),
		Point{MetricName: "mixed", EntityID: "node", Timestamp: base.Add(10 * time.Second), Value: 10},
		Point{MetricName: "mixed", EntityID: "node", Timestamp: base.Add(6*time.Minute + 10*time.Second), Value: 20},
		Point{MetricName: "mixed", EntityID: "node", Timestamp: base.Add(16*time.Minute + 10*time.Second), Value: 30},
	)
	path := filepath.Join(t.TempDir(), "restore.db")
	if err := live.SnapshotSQLite(ctx, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	assertStoreSnapshotAggregate(t, restored, "mixed", time.Hour, base, 3, 60)
	if _, err := restored.FlushCoarse(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := restored.scanRollupRows(ctx, restored.reader(), "mixed", time.Hour)
	if err != nil || len(rows) != 1 || rows[0].bucketData.count != 3 || rows[0].bucketData.sum != 60 {
		t.Fatalf("sealed restored parent = %+v, %v; want 3/60", rows, err)
	}
}

func TestOpenHigherTierCombinesDisjointPendingLevels(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
		{Interval: 24 * time.Hour, Retention: 90 * 24 * time.Hour},
	}, Compression: 30}
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "levels", RetentionDays: 90}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	point := func(at time.Duration, value float64, label string) Point {
		return Point{MetricName: "levels", EntityID: "node", Timestamp: base.Add(at), Value: value,
			Tags: map[string]string{"path": "ipv6"}, Labels: map[string]string{"source": label}}
	}
	writeRollupPointsAt(t, ctx, s, base.Add(2*time.Minute), point(10*time.Second, 10, "a"))
	writeRollupPointsAt(t, ctx, s, base.Add(8*time.Minute), point(6*time.Minute+10*time.Second, 20, "b"))
	if _, err := s.FlushCoarse(ctx, base.Add(time.Hour+11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	writeRollupPointsAt(t, ctx, s, base.Add(time.Hour+13*time.Minute),
		point(time.Hour+2*time.Minute, 30, "a"), point(time.Hour+12*time.Minute, 40, "b"))
	query := AggregateQuery{Query: Query{MetricName: "levels", EntityID: "node", Tags: map[string]string{"path": "ipv6"},
		Start: base, End: base.Add(24*time.Hour - time.Millisecond)}, Interval: 24 * time.Hour}
	check := func() {
		t.Helper()
		for aggregation, want := range map[Aggregation]float64{AggCount: 4, AggSum: 100, AggMin: 10, AggMax: 40, AggFirst: 10, AggLast: 40} {
			query.Aggregation = aggregation
			rows, err := s.AggregateRollup(WithReadBudget(ctx, 1000), query, 24*time.Hour)
			if err != nil || len(rows) != 1 || rows[0].Count != 4 || rows[0].Value != want {
				t.Fatalf("%s = %+v, %v; want count 4, value %v", aggregation, rows, err, want)
			}
		}
		query.Aggregation = AggRate
		rows, err := s.AggregateRollup(WithReadBudget(ctx, 1000), query, 24*time.Hour)
		// The existing rate convention uses each label's last source value.
		// Four disjoint pending parents must reduce to those two source rows.
		if err != nil || len(rows) != 1 || rows[0].Count != 2 || math.Abs(rows[0].Value-1.0/60) > 1e-12 {
			t.Fatalf("rate = %+v, %v; want two label rows and 1/60", rows, err)
		}
	}
	check()
	query.Aggregation = AggSum
	if _, err := s.AggregateRollup(WithReadBudget(ctx, 1), query, 24*time.Hour); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("open descendant scan ignored read budget: %v", err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	check()
}

// A pre-existing higher-tier row can have been imported while its time window
// is still open. Later fine-tier work must not be added to that durable row or
// replace it when the staged parent seals.
func TestOpenDescendantConflictsWithImportedDurableAncestor(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
		{Interval: 24 * time.Hour, Retention: 90 * 24 * time.Hour},
	}, Compression: 30}
	path := filepath.Join(t.TempDir(), "imported.db")
	s, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMetric(ctx, Definition{Name: "imported", RetentionDays: 90}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	var imported []PersistedRollup
	for _, interval := range []time.Duration{time.Minute, 5 * time.Minute, time.Hour} {
		imported = append(imported, constantTransferRollup("imported", "node", base, interval, 10, base))
	}
	if err := s.ImportRollups(ctx, imported); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	writeRollupPointsAt(t, ctx, s, base.Add(8*time.Minute), Point{
		MetricName: "imported", EntityID: "node", Timestamp: base.Add(6*time.Minute + 10*time.Second), Value: 20,
		Tags: map[string]string{"region": "eu"}, Labels: map[string]string{"rack": "r2"},
	})
	for _, interval := range []time.Duration{time.Hour, 24 * time.Hour} {
		_, err := s.AggregateRollup(ctx, AggregateQuery{
			Query:       Query{MetricName: "imported", EntityID: "node", Start: base, End: base.Add(24*time.Hour - time.Millisecond)},
			Aggregation: AggSum, Interval: interval,
		}, interval)
		if !errors.Is(err, ErrRebuildConflict) {
			t.Errorf("query %s over durable ancestor = %v, want conflict", interval, err)
		}
	}
	if _, err := s.FlushCoarse(ctx, base.Add(2*time.Hour)); !errors.Is(err, ErrRebuildConflict) {
		t.Errorf("seal over durable ancestor = %v, want conflict", err)
	}
	rows, err := s.scanRollupRows(ctx, s.reader(), "imported", time.Hour)
	if err != nil || len(rows) != 1 || rows[0].bucketData.count != 4 || rows[0].bucketData.sum != 40 {
		t.Errorf("imported hour changed: %+v, %v", rows, err)
	}
}

func TestRecoveredCoarseFiltersAndAlignedBounds(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
	}, Compression: 30}
	live, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	if err := live.CreateMetric(ctx, Definition{Name: "filtered", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	point := func(entity, path, label string, offset time.Duration, value float64) Point {
		return Point{MetricName: "filtered", EntityID: entity, Timestamp: base.Add(offset), Value: value,
			Tags: map[string]string{"path": path}, Labels: map[string]string{"source": label}}
	}
	writeRollupPointsAt(t, ctx, live, base.Add(time.Hour+3*time.Minute),
		point("node-a", "ipv6", "one", time.Minute, 10),
		point("node-a", "ipv6", "two", 6*time.Minute, 30),
		point("node-a", "ipv4", "other", 2*time.Minute, 100),
		point("node-b", "ipv6", "other", 3*time.Minute, 200),
		point("node-a", "ipv6", "three", time.Hour+time.Minute, 50),
	)
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := live.SnapshotSQLite(ctx, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	query := BatchSeriesQuery{
		Specs:     []BatchSeriesSpec{{MetricName: "filtered", Aggregations: []Aggregation{AggSum, AggP50}, Interval: time.Hour, PreserveSeries: true}},
		EntityIDs: []string{"node-a"}, Tags: map[string]string{"path": "ipv6"}, Start: base, End: base.Add(2*time.Hour - time.Millisecond),
	}
	check := func(start time.Time, aligned bool, sums []float64, counts []int) {
		t.Helper()
		query.Start = start
		query.Specs[0].AlignedStart = aligned
		result, err := restored.seriesBatchAt(ctx, query.normalized(), base, map[string]time.Duration{"filtered": time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		points := result.Values["filtered"][AggSum]
		if len(points) != len(sums) {
			t.Fatalf("filtered sums = %+v, want %v", points, sums)
		}
		for i, point := range points {
			if point.EntityID != "node-a" || point.Tags["path"] != "ipv6" || point.Count != counts[i] || point.Value != sums[i] {
				t.Fatalf("filtered sum %d = %+v, want count %d sum %v", i, point, counts[i], sums[i])
			}
		}
		percentiles := result.Values["filtered"][AggP50]
		if len(percentiles) != len(sums) || percentiles[len(percentiles)-1].Value != 50 {
			t.Fatalf("filtered percentiles = %+v", percentiles)
		}
	}
	check(base, false, []float64{40, 50}, []int{2, 1})
	check(base.Add(30*time.Minute), true, []float64{50}, []int{1})
	if _, err := restored.FlushCoarse(ctx, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	check(base, false, []float64{40, 50}, []int{2, 1})
	check(base.Add(30*time.Minute), true, []float64{50}, []int{1})
}

func TestSQLiteSnapshotRefusesUnrecoverableOpenParent(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "missing-child", RetentionDays: 7}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	writeRollupPointsAt(t, ctx, s, base.Add(2*time.Minute), Point{
		MetricName: "missing-child", EntityID: "node", Timestamp: base.Add(10 * time.Second), Value: 7,
	})
	if _, err := s.ExecContext(ctx, `DELETE FROM metric_rollups WHERE resolution_id IN (SELECT id FROM metric_resolutions WHERE resolution_milli = ?)`, time.Minute.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, destination); !errors.Is(err, ErrRetentionPending) {
		t.Fatalf("snapshot with missing open child = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete snapshot remains: %v", err)
	}
	if len(s.coarse) == 0 {
		t.Fatal("failed snapshot discarded the live parent")
	}
}

func TestSQLiteSnapshotRefusesExistingParentConflict(t *testing.T) {
	for _, resolution := range []time.Duration{5 * time.Minute, time.Hour} {
		t.Run(resolution.String(), func(t *testing.T) {
			testSQLiteSnapshotRefusesExistingParentConflict(t, resolution)
		})
	}
}

func testSQLiteSnapshotRefusesExistingParentConflict(t *testing.T, resolution time.Duration) {
	t.Helper()
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
	}, Compression: 30}
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "parent-conflict", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	// Model an earlier incomplete restore which left a parent in SQLite while
	// this process has accepted another child for the same window.
	old := constantTransferRollup("parent-conflict", "node", base, resolution, 10, base.Add(10*time.Second))
	old.Tags, old.Labels = nil, nil
	if err := s.ImportRollups(ctx, []PersistedRollup{old}); err != nil {
		t.Fatal(err)
	}
	writeRollupPointsAt(t, ctx, s, base.Add(4*time.Minute), Point{
		MetricName: "parent-conflict", EntityID: "node", Timestamp: base.Add(2*time.Minute + 10*time.Second), Value: 20,
	})
	destination := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, destination); !errors.Is(err, ErrRebuildConflict) {
		t.Fatalf("snapshot with existing parent = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conflicting snapshot remains: %v", err)
	}
	rows, err := s.scanRollupRows(ctx, s.reader(), "parent-conflict", resolution)
	if err != nil || len(rows) != 1 || rows[0].bucketData.sum != 40 {
		t.Fatalf("snapshot changed live parent: %+v, %v", rows, err)
	}
}

// A handoff can reopen a database whose old minute rollup is durable but whose
// exact raw cache starts empty. A later exact replacement must not overwrite
// older observations missing from that new cache.
func TestReopenedStoreLateReplacementPreservesEarlierSamples(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "handoff.db")
	open := func() *Store {
		s, err := Open(ctx, SQLite(path))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	if err := s.CreateMetric(ctx, Definition{Name: "handoff", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(30 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "handoff", EntityID: "node", Timestamp: at, Value: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	t.Cleanup(func() { _ = s.Close() })
	at = at.Add(10 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "handoff", EntityID: "node", Timestamp: at, Value: 20}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, Point{MetricName: "handoff", EntityID: "node", Timestamp: at, Value: 30}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "handoff", time.Minute, 2, 40)
}

func TestSQLiteSnapshotRetainsCoarseOnlyChildren(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
	}, Compression: 30}
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "coarse-only", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	// Keep the hourly parent closed beyond the ten-minute late-arrival grace,
	// even when this test runs just after the top of an hour.
	base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	prepared, err := prepareMetricPoints([]Point{{MetricName: "coarse-only", EntityID: "node", Timestamp: base.Add(10 * time.Second), Value: 7}})
	if err != nil {
		t.Fatal(err)
	}
	rebuild := s.writeRawPointsAt(prepared, base.Add(2*time.Minute))
	if err := s.writePreparedHotRollups(ctx, prepared, base.Add(2*time.Minute), rebuild); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Retention can remove a fine child while its hourly parent is still in
	// memory. Startup reconstruction from the remaining SQLite rows alone is
	// then insufficient.
	if _, err := s.ExecContext(ctx, `DELETE FROM metric_rollups WHERE resolution_id IN (SELECT id FROM metric_resolutions WHERE resolution_milli = ?)`, (5 * time.Minute).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, snapshot, "coarse-only", time.Hour, 1, 7)
	if len(s.coarse) == 0 {
		t.Fatal("snapshot changed the live pending parents")
	}
}

func TestSQLiteSnapshotFailureDoesNotChangeLiveStore(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "failure", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(30 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "failure", EntityID: "node", Timestamp: at, Value: 4}); err != nil {
		t.Fatal(err)
	}
	// The snapshot has copied successfully when the detached overlay detects
	// the inconsistent schema; the partial output must be removed.
	if _, err := s.ExecContext(ctx, `DROP TABLE metric_rollups`); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, destination); err == nil {
		t.Fatal("snapshot with missing rollup table succeeded")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial snapshot remains: %v", err)
	}
	if len(s.hot) != 1 {
		t.Fatalf("snapshot failure changed hot data: %d buckets", len(s.hot))
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotSQLite(ctx, destination); err != nil {
		t.Fatalf("retry snapshot: %v", err)
	}
	assertSnapshotSummary(t, destination, "failure", time.Minute, 1, 4)
}

func TestSQLiteSnapshotRefusesIncompleteCoarseRecovery(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "live.db")
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}}
	s, err := Open(ctx, SQLite(path, WithRollupPolicy(policy), WithAutoMigrate(false)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMetric(ctx, Definition{Name: "recovery", RetentionDays: 7}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(5 * time.Minute)
	row := constantTransferRollup("recovery", "node", base, time.Minute, 7, base)
	if err := s.ImportRollups(ctx, []PersistedRollup{row}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, destination); !errors.Is(err, ErrRetentionPending) {
		t.Fatalf("snapshot before recovery = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete snapshot remains: %v", err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("recover coarse history: %v", err)
	}
	if err := s.SnapshotSQLite(ctx, destination); err != nil {
		t.Fatalf("snapshot after recovery: %v", err)
	}
	assertSnapshotSummary(t, destination, "recovery", 5*time.Minute, 4, 28)
}

func TestSQLiteSnapshotConcurrentWritesAreConsistent(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "live.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, name := range []string{"left", "right"} {
		if err := s.CreateMetric(ctx, Definition{Name: name, RetentionDays: 1}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UTC().Add(30 * time.Second).Truncate(time.Millisecond)
	var wg sync.WaitGroup
	writeErr := make(chan error, 1)
	wg.Go(func() {
		for i := 0; i < 100; i++ {
			at := base.Add(time.Duration(i) * time.Millisecond)
			if err := s.WriteBatch(ctx, []Point{{MetricName: "left", EntityID: "node", Timestamp: at, Value: 1}, {MetricName: "right", EntityID: "node", Timestamp: at, Value: 1}}); err != nil {
				writeErr <- err
				return
			}
		}
	})
	for i := 0; i < 5; i++ {
		path := filepath.Join(t.TempDir(), "snapshot.db")
		if err := s.SnapshotSQLite(ctx, path); err != nil {
			t.Fatal(err)
		}
		left, right := snapshotTotals(t, path, "left", time.Minute), snapshotTotals(t, path, "right", time.Minute)
		if left != right {
			t.Fatalf("snapshot crossed an atomic batch: left=%v right=%v", left, right)
		}
	}
	wg.Wait()
	select {
	case err := <-writeErr:
		t.Fatal(err)
	default:
	}
}

type totals struct {
	count int64
	sum   float64
}

func snapshotTotals(t *testing.T, path, name string, resolution time.Duration) totals {
	t.Helper()
	s, err := Open(t.Context(), SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.scanRollupRows(t.Context(), s.reader(), name, resolution)
	if err != nil {
		t.Fatal(err)
	}
	var result totals
	for _, row := range rows {
		result.count += row.bucketData.count
		result.sum += row.bucketData.sum
	}
	return result
}

func assertSnapshotSummary(t *testing.T, path, name string, resolution time.Duration, count int64, sum float64) {
	t.Helper()
	if got := snapshotTotals(t, path, name, resolution); got != (totals{count: count, sum: sum}) {
		t.Fatalf("%s %s snapshot totals = %+v, want %d/%v", name, resolution, got, count, sum)
	}
}

func assertSnapshotAggregate(t *testing.T, path, name string, resolution time.Duration, at time.Time, count int, sum float64) {
	t.Helper()
	s, err := Open(t.Context(), SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertStoreSnapshotAggregate(t, s, name, resolution, at, count, sum)
}

func assertStoreSnapshotAggregate(t *testing.T, s *Store, name string, resolution time.Duration, at time.Time, count int, sum float64) {
	t.Helper()
	start := at.Truncate(resolution)
	points, err := s.AggregateRollup(t.Context(), AggregateQuery{
		Query:       Query{MetricName: name, EntityID: "node", Start: start, End: start.Add(resolution - time.Millisecond)},
		Aggregation: AggSum, Interval: resolution,
	}, resolution)
	if err != nil || len(points) != 1 || points[0].Count != count || points[0].Value != sum {
		t.Fatalf("%s snapshot aggregate = %+v, %v; want %d/%v", resolution, points, err, count, sum)
	}
}

func assertLiveRaw(t *testing.T, s *Store, name string, at time.Time, want []float64) {
	t.Helper()
	points, err := s.Query(t.Context(), Query{MetricName: name, EntityID: "node", Start: at.Add(-time.Minute), End: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != len(want) {
		t.Fatalf("live raw = %v, want values %v", points, want)
	}
	for i, value := range want {
		if points[i].Value != value {
			t.Fatalf("live raw = %v, want values %v", points, want)
		}
	}
}
