package metric

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func tryAppendFixture(t *testing.T) (*Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "metrics.db"), WithRollupPolicy(RollupPolicy{
		Tiers: []RollupTier{{Interval: time.Minute, Retention: time.Hour}},
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"latency", "loss", "success"} {
		if err := s.CreateMetric(ctx, Definition{Name: name, RetentionDays: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ListMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	return s, time.Now().UTC()
}

func tryAppendPoints(at time.Time) []Point {
	return []Point{
		{MetricName: "latency", EntityID: "node", Timestamp: at, Value: 2.345, Tags: map[string]string{"family": "ipv6"}},
		{MetricName: "loss", EntityID: "node", Timestamp: at, Value: 0, Tags: map[string]string{"family": "ipv6"}},
		{MetricName: "success", EntityID: "node", Timestamp: at, Value: 2.345, Tags: map[string]string{"family": "ipv6"}},
	}
}

func TestTryAppendCurrentBatchCommitsRawAndRollup(t *testing.T) {
	s, at := tryAppendFixture(t)
	ctx := context.Background()
	if err := s.PrepareCurrentBatch(ctx, tryAppendPoints(at)); err != nil {
		t.Fatal(err)
	}
	if err := s.tryAppendCurrentBatch(ctx, tryAppendPoints(at), func() time.Time { return at }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"latency", "loss", "success"} {
		query := Query{MetricName: name, EntityID: "node", Start: at.Add(-time.Second), End: at.Add(time.Second)}
		raw, err := s.Query(ctx, query)
		if err != nil || len(raw) != 1 || raw[0].Tags["family"] != "ipv6" {
			t.Fatalf("%s raw=%+v err=%v", name, raw, err)
		}
		buckets, err := s.Series(ctx, AggregateQuery{Query: query, Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true}, at)
		if err != nil || len(buckets) != 1 || buckets[0].Count != 1 || buckets[0].Value != raw[0].Value {
			t.Fatalf("%s buckets=%+v err=%v", name, buckets, err)
		}
	}
	if n, err := s.Flush(ctx, at.Truncate(time.Minute).Add(time.Minute)); err != nil || n != 3 {
		t.Fatalf("flush = %d, %v", n, err)
	}
	query := AggregateQuery{Query: Query{MetricName: "latency", EntityID: "node", Start: at.Truncate(time.Minute), End: at.Add(time.Minute)}, Aggregation: AggAvg, Interval: time.Minute}
	if buckets, err := s.Series(ctx, query, at.Add(time.Minute)); err != nil || len(buckets) != 1 || buckets[0].Value != 2.345 {
		t.Fatalf("persisted rollup=%+v err=%v", buckets, err)
	}
}

func TestTryAppendCurrentBatchRefusesContendedLocksWithoutWriting(t *testing.T) {
	for _, name := range []string{"retention", "state", "definitions", "ingest", "rollup", "raw", "hot"} {
		t.Run(name, func(t *testing.T) {
			s, at := tryAppendFixture(t)
			unlock := func() {}
			switch name {
			case "retention":
				s.retentionMu.Lock()
				unlock = s.retentionMu.Unlock
			case "state":
				s.mu.Lock()
				unlock = s.mu.Unlock
			case "definitions":
				s.definitionsMu.Lock()
				unlock = s.definitionsMu.Unlock
			case "ingest":
				s.ingestMu.Lock()
				unlock = s.ingestMu.Unlock
			case "rollup":
				s.rollupViewMu.Lock()
				unlock = s.rollupViewMu.Unlock
			case "raw":
				s.rawMu.Lock()
				unlock = s.rawMu.Unlock
			case "hot":
				s.hotMu.Lock()
				unlock = s.hotMu.Unlock
			}
			done := make(chan error, 1)
			go func() { done <- s.TryAppendCurrentBatch(context.Background(), tryAppendPoints(at)) }()
			select {
			case err := <-done:
				unlock()
				if !errors.Is(err, ErrWriteUnavailable) {
					t.Fatalf("contended append = %v", err)
				}
			case <-time.After(time.Second):
				unlock()
				<-done
				t.Fatal("append waited on an ordinary store lock")
			}
			for _, metricName := range []string{"latency", "loss", "success"} {
				points, err := s.Query(context.Background(), Query{MetricName: metricName, EntityID: "node", Start: at.Add(-time.Second), End: at.Add(time.Second)})
				if err != nil || len(points) != 0 {
					t.Fatalf("failed append left %s samples %+v: %v", metricName, points, err)
				}
			}
		})
	}
}

func TestTryAppendCurrentBatchRejectsWithoutPartialWrite(t *testing.T) {
	for _, mode := range []string{"canceled", "missing-snapshot", "duplicate", "existing", "old", "future", "invalid", "disabled", "unknown", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			s, at := tryAppendFixture(t)
			points := tryAppendPoints(at)
			ctx := context.Background()
			switch mode {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-snapshot":
				s.invalidateDefinitions()
			case "duplicate":
				points[2] = points[1]
			case "existing":
				if err := s.Write(ctx, points[1]); err != nil {
					t.Fatal(err)
				}
			case "old":
				points[1].Timestamp = at.Add(-2 * time.Minute)
			case "future":
				points[1].Timestamp = at.Add(time.Hour)
			case "invalid":
				points[1].Value = math.NaN()
			case "disabled":
				if err := s.UpsertMetric(ctx, Definition{Name: "loss", RetentionDays: 0}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ListMetrics(ctx); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				points[1].MetricName = "unknown"
			case "oversized":
				points[1].Tags = make(map[string]string)
				for i := 0; i < 9; i++ {
					points[1].Tags[fmt.Sprint(i)] = "value"
				}
			}
			if err := s.tryAppendCurrentBatch(ctx, points, func() time.Time { return at }); err == nil {
				t.Fatal("unsafe batch accepted")
			}
			for _, name := range []string{"latency", "success"} {
				raw, err := s.Query(context.Background(), Query{MetricName: name, EntityID: "node", Start: at.Add(-3 * time.Minute), End: at.Add(time.Hour)})
				if err != nil || len(raw) != 0 {
					t.Fatalf("partial %s points=%+v err=%v", name, raw, err)
				}
			}
		})
	}
}

func TestTryAppendCancellationDuringContentionCannotCommitLater(t *testing.T) {
	s, at := tryAppendFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.ingestMu.Lock()
	err := s.TryAppendCurrentBatch(ctx, tryAppendPoints(at))
	cancel()
	s.ingestMu.Unlock()
	if !errors.Is(err, ErrWriteUnavailable) {
		t.Fatalf("contended append = %v", err)
	}
	if err := s.TryAppendCurrentBatch(ctx, tryAppendPoints(at)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled append = %v", err)
	}
	for _, name := range []string{"latency", "loss", "success"} {
		points, err := s.Query(context.Background(), Query{MetricName: name, EntityID: "node", Start: at.Add(-time.Second), End: at.Add(time.Second)})
		if err != nil || len(points) != 0 {
			t.Fatalf("canceled %s points=%+v err=%v", name, points, err)
		}
	}
}

func TestTryAppendCurrentBatchConcurrentWithOrdinaryIngest(t *testing.T) {
	s, at := tryAppendFixture(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 80; i++ {
			if err := s.Write(ctx, Point{MetricName: "latency", EntityID: "ordinary", Timestamp: time.Now(), Value: float64(i)}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 80; i++ {
		points := tryAppendPoints(at)
		for j := range points {
			points[j].EntityID = fmt.Sprintf("node-%d", i)
		}
		if err := s.tryAppendCurrentBatch(ctx, points, func() time.Time { return at }); err != nil && !errors.Is(err, ErrWriteUnavailable) {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTryAppendCurrentBatchDropsOnMinuteRollover(t *testing.T) {
	s, _ := tryAppendFixture(t)
	end := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	points := tryAppendPoints(end.Add(-time.Millisecond))
	if err := s.tryAppendCurrentBatch(context.Background(), points, func() time.Time { return end }); !errors.Is(err, ErrWriteUnavailable) {
		t.Fatalf("append across minute boundary = %v", err)
	}
	for _, name := range []string{"latency", "loss", "success"} {
		got, err := s.Query(context.Background(), Query{MetricName: name, EntityID: "node", Start: end.Add(-time.Minute), End: end})
		if err != nil || len(got) != 0 {
			t.Fatalf("rollover left %s samples=%+v err=%v", name, got, err)
		}
	}
}
