package metricstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func TestPanelHistoryRetentionPersistenceAndUnknownRSS(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "metrics.db")
	open := func() *metric.Store {
		s, err := openStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: file})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	base := time.Now().UTC().Truncate(5 * time.Minute).Add(-10 * time.Minute)
	if err := writePanelSample(ctx, s, PanelSample{At: base.Add(time.Minute), HeapBytes: 120, Goroutines: 7}); err != nil {
		t.Fatal(err)
	}
	if err := writePanelSample(ctx, s, PanelSample{At: base.Add(6 * time.Minute), RSSBytes: 450, HeapBytes: 180, Goroutines: 9}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer s.Close()
	history, err := readPanelHistory(ctx, s, 24, base.Add(8*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if history.IntervalSeconds != 300 || history.RetentionDays[MetricPanelRSS] != 1 || history.RetentionDays[MetricPanelHeap] != 1 {
		t.Fatalf("unexpected policy: %+v", history)
	}
	var first, second *PanelHistoryPoint
	for i := range history.Points {
		p := &history.Points[i]
		if p.Time.Equal(base) {
			first = p
		}
		if p.Time.Equal(base.Add(5 * time.Minute)) {
			second = p
		}
	}
	if first == nil || first.RSSBytes != nil || first.HeapBytes == nil || *first.HeapBytes != 120 || first.Goroutines == nil || *first.Goroutines != 7 {
		t.Fatalf("first bucket: %+v", first)
	}
	if second == nil || second.RSSBytes == nil || *second.RSSBytes != 450 || second.HeapBytes == nil || *second.HeapBytes != 180 {
		t.Fatalf("second bucket: %+v", second)
	}
	if _, err := s.UpdateMetricRetention(ctx, MetricPanelHeap, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMetricDataIfDisabled(ctx, MetricPanelHeap); err != nil {
		t.Fatal(err)
	}
	if err := writePanelSample(ctx, s, PanelSample{At: base.Add(7 * time.Minute), RSSBytes: 500, HeapBytes: 999, Goroutines: 10}); err != nil {
		t.Fatal(err)
	}
	history, err = readPanelHistory(ctx, s, 24, base.Add(9*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if history.RetentionDays[MetricPanelHeap] != 0 {
		t.Fatalf("disabled retention = %d", history.RetentionDays[MetricPanelHeap])
	}
	for _, p := range history.Points {
		if p.HeapBytes != nil {
			t.Fatalf("disabled history returned a heap value: %+v", p)
		}
	}
}

func TestPanelHistoryIncludesNewestLiveSample(t *testing.T) {
	ctx := context.Background()
	s, err := openStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	if err := writePanelSample(ctx, s, PanelSample{At: now.Add(-time.Second), RSSBytes: 234, HeapBytes: 123, Goroutines: 4}); err != nil {
		t.Fatal(err)
	}
	for _, hours := range []int{1, 24} {
		history, err := readPanelHistory(ctx, s, hours, now)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, point := range history.Points {
			if point.HeapBytes != nil && *point.HeapBytes == 123 {
				found = true
			}
		}
		if !found {
			t.Errorf("%dh history omitted the newest live bucket", hours)
		}
	}
}

func TestPanelHistoryDefinitionsSurviveReclaimAndExpire(t *testing.T) {
	ctx := context.Background()
	s, err := openStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	old := now.Add(-48 * time.Hour)
	if err := writePanelSample(ctx, s, PanelSample{At: old, HeapBytes: 111, Goroutines: 5}); err != nil {
		t.Fatal(err)
	}
	if err := writePanelSample(ctx, s, PanelSample{At: now.Add(-time.Hour), HeapBytes: 222, Goroutines: 6}); err != nil {
		t.Fatal(err)
	}
	if err := deleteUndefinedMetrics(ctx, s); err != nil {
		t.Fatal(err)
	}
	for _, name := range panelMetricNames {
		if _, err := s.GetMetric(ctx, name); err != nil {
			t.Fatalf("reclaim removed %s: %v", name, err)
		}
	}
	if _, err := s.CleanupExpired(ctx, now); err != nil {
		t.Fatal(err)
	}
	points, err := s.Series(ctx, metric.AggregateQuery{Query: metric.Query{
		MetricName: MetricPanelHeap, EntityID: panelHistoryEntity, Start: old.Add(-time.Hour), End: now,
	}, Aggregation: metric.AggLast, Interval: time.Hour}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range points {
		if point.Value == 111 {
			t.Fatalf("expired observation survived cleanup: %+v", point)
		}
	}
	current, err := readPanelHistory(ctx, s, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	var retained bool
	for _, point := range current.Points {
		retained = retained || point.HeapBytes != nil && *point.HeapBytes == 222
	}
	if !retained {
		t.Fatal("cleanup deleted a recent observation")
	}
}

func TestPanelHistoryDefinitionsDoNotChangeNodeRetentionSummary(t *testing.T) {
	got := summarizeRetentionDefinitions([]metric.Definition{
		{Name: MetricCPU, RetentionDays: 3},
		{Name: MetricPanelHeap, RetentionDays: 0},
		{Name: MetricPanelRSS, RetentionDays: 90},
	})
	if !got.AllPositive || got.MaxDays != 3 {
		t.Fatalf("panel retention changed node summary: %+v", got)
	}
	if got := summarizeRetentionDefinitions([]metric.Definition{{Name: MetricPanelHeap, RetentionDays: 1}}); got.AllPositive || got.MaxDays != 0 {
		t.Fatalf("panel-only definitions counted as node records: %+v", got)
	}
}

func TestPanelHistoryBoundsAndStoreLease(t *testing.T) {
	ctx := context.Background()
	if _, err := ReadPanelHistory(ctx, 2, time.Now()); err == nil {
		t.Fatal("unsupported window accepted")
	}
	if err := storeOperations.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	defer storeOperations.Release()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := WritePanelSample(cancelled, PanelSample{At: time.Now(), Goroutines: 1}); err == nil {
		t.Fatal("sample did not respect a cancelled lease")
	}
	if _, err := ReadPanelHistory(cancelled, 1, time.Now()); err == nil {
		t.Fatal("query did not respect a cancelled lease")
	}
}

func TestPanelHistoryWritesDuringStoreReload(t *testing.T) {
	ctx := context.Background()
	config := &MetricStoreConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "metrics.db")}
	active, err := openStore(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	storeMu.Lock()
	previous := store
	store = active
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		current := store
		store = previous
		storeMu.Unlock()
		_ = current.Close()
	}()
	results := make(chan error, 1)
	go func() {
		for i := 0; i < 40; i++ {
			if err := WritePanelSample(ctx, PanelSample{At: time.Now(), HeapBytes: uint64(i), Goroutines: 3}); err != nil {
				results <- err
				return
			}
			if _, err := ReadPanelHistory(ctx, 1, time.Now()); err != nil {
				results <- err
				return
			}
		}
		results <- nil
	}()
	for i := 0; i < 4; i++ {
		if err := storeOperations.Acquire(ctx); err != nil {
			t.Fatal(err)
		}
		next, err := openStore(ctx, config)
		if err != nil {
			storeOperations.Release()
			t.Fatal(err)
		}
		storeMu.Lock()
		old := store
		store = next
		storeMu.Unlock()
		if err := old.Close(); err != nil {
			storeOperations.Release()
			t.Fatal(err)
		}
		storeOperations.Release()
	}
	if err := <-results; err != nil {
		t.Fatalf("history operation raced store replacement: %v", err)
	}
}
