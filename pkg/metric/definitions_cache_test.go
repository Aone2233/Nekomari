package metric

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// writeCacheTestPoint writes one point and fails the test on error.
func writeCacheTestPoint(t *testing.T, s *Store, name, entity string, at time.Time) {
	t.Helper()
	if err := s.Write(context.Background(), Point{MetricName: name, EntityID: entity, Timestamp: at, Value: 1}); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// countCacheTestPoints returns how many raw points the store still holds.
func countCacheTestPoints(t *testing.T, s *Store, name string) int {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	points, err := s.Query(ctx, Query{MetricName: name, Start: now.Add(-time.Hour), End: now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("query %s: %v", name, err)
	}
	return len(points)
}

// TestDefinitionCacheInvalidation verifies that every definition mutation path
// invalidates the ingest snapshot, so a retention change is honoured by the
// very next write instead of being filtered by a stale definition.
func TestDefinitionCacheInvalidation(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	now := time.Now().UTC()

	if err := s.CreateMetric(ctx, Definition{Name: "cache.a", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatalf("create cache.a: %v", err)
	}
	writeCacheTestPoint(t, s, "cache.a", "node-1", now)
	if got := countCacheTestPoints(t, s, "cache.a"); got != 1 {
		t.Fatalf("points after first write = %d, want 1", got)
	}

	// CreateMetric must be visible to ListMetrics and to the ingest filter even
	// though the snapshot was already loaded by the previous write.
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		t.Fatalf("list metrics: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("definitions = %d, want 1", len(defs))
	}
	if err := s.CreateMetric(ctx, Definition{Name: "cache.b", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatalf("create cache.b: %v", err)
	}
	defs, err = s.ListMetrics(ctx)
	if err != nil {
		t.Fatalf("list metrics after create: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("definitions after create = %d, want 2", len(defs))
	}
	writeCacheTestPoint(t, s, "cache.b", "node-1", now)
	if got := countCacheTestPoints(t, s, "cache.b"); got != 1 {
		t.Fatalf("points for the newly created metric = %d, want 1", got)
	}

	// Disabling retention must drop the next write; a stale snapshot would keep
	// persisting points and reintroduce the "retention changed but writes still
	// use the old definition" bug.
	if _, err := s.UpdateMetricRetention(ctx, "cache.a", 0); err != nil {
		t.Fatalf("update retention: %v", err)
	}
	if err := s.Write(ctx, Point{MetricName: "cache.a", EntityID: "node-1", Timestamp: now.Add(time.Second), Value: 2}); err != nil {
		t.Fatalf("write to disabled metric: %v", err)
	}
	if got := countCacheTestPoints(t, s, "cache.a"); got != 1 {
		t.Fatalf("disabled metric persisted a point: %d, want the original 1", got)
	}

	// Re-enabling retention through SetMetricRetention must be honoured too.
	if _, err := s.SetMetricRetention(ctx, "cache.a", 30); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	writeCacheTestPoint(t, s, "cache.a", "node-1", now.Add(2*time.Second))
	if got := countCacheTestPoints(t, s, "cache.a"); got != 2 {
		t.Fatalf("re-enabled metric points = %d, want 2", got)
	}

	// Disabling through SetMetricRetention must drop the next write as well.
	if _, err := s.SetMetricRetention(ctx, "cache.a", 0); err != nil {
		t.Fatalf("disable retention: %v", err)
	}
	if err := s.Write(ctx, Point{MetricName: "cache.a", EntityID: "node-1", Timestamp: now.Add(3 * time.Second), Value: 3}); err != nil {
		t.Fatalf("write to disabled metric: %v", err)
	}
	if got := countCacheTestPoints(t, s, "cache.a"); got != 0 {
		t.Fatalf("disabled metric points = %d, want 0", got)
	}

	// UpsertMetric must publish the new definition to both readers and writers.
	if err := s.UpsertMetric(ctx, Definition{Name: "cache.c", Type: TypeCounter, RetentionDays: 30}); err != nil {
		t.Fatalf("upsert cache.c: %v", err)
	}
	writeCacheTestPoint(t, s, "cache.c", "node-1", now)
	defs, err = s.ListMetrics(ctx)
	if err != nil {
		t.Fatalf("list metrics after upsert: %v", err)
	}
	found := false
	for _, def := range defs {
		if def.Name == "cache.c" {
			found = def.Type == TypeCounter
		}
	}
	if !found {
		t.Fatalf("upserted definition missing or stale: %#v", defs)
	}

	// DeleteMetric must remove the definition from the snapshot, so the next
	// write is rejected instead of creating orphaned data.
	if err := s.DeleteMetric(ctx, "cache.b"); err != nil {
		t.Fatalf("delete cache.b: %v", err)
	}
	err = s.Write(ctx, Point{MetricName: "cache.b", EntityID: "node-1", Timestamp: now, Value: 4})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("write after delete = %v, want ErrNotFound", err)
	}
}

// TestDefinitionCacheReturnsCopy verifies that callers cannot corrupt the
// cached snapshot through the slice ListMetrics hands them.
func TestDefinitionCacheReturnsCopy(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)

	if err := s.CreateMetric(ctx, Definition{Name: "copy.a", Type: TypeGauge, RetentionDays: 30, Metadata: map[string]string{"owner": "team"}}); err != nil {
		t.Fatalf("create copy.a: %v", err)
	}
	if err := s.CreateMetric(ctx, Definition{Name: "copy.b", Type: TypeGauge, RetentionDays: 7}); err != nil {
		t.Fatalf("create copy.b: %v", err)
	}
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		t.Fatalf("list metrics: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("definitions = %d, want 2", len(defs))
	}
	defs[0], defs[1] = defs[1], defs[0]
	defs[0].Name = "mutated"
	defs[0].RetentionDays = 0
	defs[0].Metadata["owner"] = "mutated"

	again, err := s.ListMetrics(ctx)
	if err != nil {
		t.Fatalf("list metrics again: %v", err)
	}
	if len(again) != 2 {
		t.Fatalf("definitions = %d, want 2", len(again))
	}
	if again[0].Name != "copy.a" || again[1].Name != "copy.b" {
		t.Fatalf("cached definitions were reordered: %#v", again)
	}
	if again[0].RetentionDays != 30 {
		t.Fatalf("cached retention = %d, want 30", again[0].RetentionDays)
	}
	if again[0].Metadata["owner"] != "team" {
		t.Fatalf("cached metadata was mutated: %#v", again[0].Metadata)
	}
}

// TestDefinitionCacheBypassedByCleanupExpired verifies that maintenance paths
// read the committed definitions table rather than the ingest snapshot: a
// retention written straight to the database is still enforced by
// CleanupExpired, which must not act on a cached copy.
func TestDefinitionCacheBypassedByCleanupExpired(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	now := time.Now().UTC()

	if err := s.CreateMetric(ctx, Definition{Name: "cache.cleanup", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	writeCacheTestPoint(t, s, "cache.cleanup", "node-1", now)
	if got := countCacheTestPoints(t, s, "cache.cleanup"); got != 1 {
		t.Fatalf("points = %d, want 1", got)
	}

	// Bypass the API so only the committed definition changes; this is the
	// out-of-band edit the ingest snapshot could not observe.
	if _, err := s.db.ExecContext(ctx, "UPDATE "+s.tables.definitions+" SET retention_days = ? WHERE name = ?", 0, "cache.cleanup"); err != nil {
		t.Fatalf("seed disabled definition: %v", err)
	}
	if _, err := s.CleanupExpired(ctx, now); err != nil {
		t.Fatalf("cleanup expired: %v", err)
	}
	if got := countCacheTestPoints(t, s, "cache.cleanup"); got != 0 {
		t.Fatalf("cleanup left %d points for a disabled metric, want 0", got)
	}
}

// TestDefinitionCacheConcurrentAccess exercises the snapshot under concurrent
// readers, writers, and definition mutations. Run with -race.
func TestDefinitionCacheConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	now := time.Now().UTC()

	if err := s.CreateMetric(ctx, Definition{Name: "cache.shared", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	const writers = 4
	const readers = 2
	const iterations = 40
	var wg sync.WaitGroup
	errs := make(chan error, writers+readers+1)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			entity := "node-" + string(rune('a'+w))
			for i := 0; i < iterations; i++ {
				err := s.WriteBatch(ctx, []Point{{
					MetricName: "cache.shared",
					EntityID:   entity,
					Timestamp:  now.Add(time.Duration(i) * time.Millisecond),
					Value:      float64(i),
				}})
				if err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if _, err := s.ListMetrics(ctx); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			name := "cache.dynamic." + string(rune('a'+i%26))
			if err := s.UpsertMetric(ctx, Definition{Name: name, Type: TypeGauge, RetentionDays: 30}); err != nil {
				errs <- err
				return
			}
			if _, err := s.UpdateMetricRetention(ctx, name, 1); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent access: %v", err)
	}

	// The snapshot must still be usable and consistent after the mutations.
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		t.Fatalf("list metrics: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("definitions became empty after concurrent mutations")
	}
	if err := s.Write(ctx, Point{MetricName: "cache.shared", EntityID: "node-a", Timestamp: now, Value: 1}); err != nil {
		t.Fatalf("write after concurrent mutations: %v", err)
	}
}
