package metricstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func TestThemeCacheHistoryStatesRetentionAndRestart(t *testing.T) {
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
	write := func(at time.Time, disk string, loaded bool) {
		t.Helper()
		err := writePanelSample(ctx, s, PanelSample{At: at, Goroutines: 1,
			ThemeCache: &PanelThemeCacheSample{Disk: disk, MemoryLoaded: loaded}})
		if err != nil {
			t.Fatal(err)
		}
	}
	write(base.Add(time.Minute), "ready", false)
	write(base.Add(2*time.Minute), "new-unrecognized-state", true)
	write(base.Add(6*time.Minute), "degraded", true)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer s.Close()
	for _, hours := range []int{1, 24} {
		history, err := readPanelHistory(ctx, s, hours, base.Add(8*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if history.RetentionDays[MetricPanelThemeDisk] != 1 || history.RetentionDays[MetricPanelThemeMemory] != 1 {
			t.Fatalf("%dh theme retention: %+v", hours, history.RetentionDays)
		}
		var unknown, degraded *PanelHistoryPoint
		for i := range history.Points {
			p := &history.Points[i]
			if (hours == 1 && p.Time.Equal(base.Add(2*time.Minute))) || (hours == 24 && p.Time.Equal(base)) {
				unknown = p
			}
			if (hours == 1 && p.Time.Equal(base.Add(6*time.Minute))) || (hours == 24 && p.Time.Equal(base.Add(5*time.Minute))) {
				degraded = p
			}
		}
		if unknown == nil || unknown.ThemeCacheDisk != nil || unknown.ThemeCacheMemoryLoaded == nil || !*unknown.ThemeCacheMemoryLoaded {
			t.Fatalf("%dh unknown disk/loaded memory: %+v", hours, unknown)
		}
		if degraded == nil || degraded.ThemeCacheDisk == nil || *degraded.ThemeCacheDisk != "degraded" || degraded.ThemeCacheMemoryLoaded == nil || !*degraded.ThemeCacheMemoryLoaded {
			t.Fatalf("%dh degraded bucket: %+v", hours, degraded)
		}
	}
	if _, err := s.UpdateMetricRetention(ctx, MetricPanelThemeDisk, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMetricDataIfDisabled(ctx, MetricPanelThemeDisk); err != nil {
		t.Fatal(err)
	}
	write(base.Add(7*time.Minute), "ready", false)
	history, err := readPanelHistory(ctx, s, 24, base.Add(9*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if history.RetentionDays[MetricPanelThemeDisk] != 0 || history.RetentionDays[MetricPanelThemeMemory] != 1 {
		t.Fatalf("independent retention: %+v", history.RetentionDays)
	}
	var observedFalse bool
	for _, p := range history.Points {
		if p.ThemeCacheDisk != nil {
			t.Fatalf("disabled disk history returned a value: %+v", p)
		}
		observedFalse = observedFalse || p.ThemeCacheMemoryLoaded != nil && !*p.ThemeCacheMemoryLoaded
	}
	if !observedFalse {
		t.Fatal("memory false was lost or confused with unknown")
	}
	if _, err := s.UpdateMetricRetention(ctx, MetricPanelThemeDisk, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMetricRetention(ctx, MetricPanelThemeMemory, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMetricDataIfDisabled(ctx, MetricPanelThemeMemory); err != nil {
		t.Fatal(err)
	}
	write(base.Add(8*time.Minute), "ready", true)
	history, err = readPanelHistory(ctx, s, 24, base.Add(9*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var observedDisk bool
	for _, p := range history.Points {
		if p.ThemeCacheMemoryLoaded != nil {
			t.Fatalf("disabled memory history returned a value: %+v", p)
		}
		observedDisk = observedDisk || p.ThemeCacheDisk != nil && *p.ThemeCacheDisk == "ready"
	}
	if !observedDisk || history.RetentionDays[MetricPanelThemeMemory] != 0 {
		t.Fatalf("disk history did not survive independent memory disable: %+v", history.RetentionDays)
	}
}

func TestThemeCacheAllStatesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := openStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	states := []string{"disabled", "uninitialized", "initializing", "ready", "failed", "degraded"}
	for i, state := range states {
		if err := writePanelSample(ctx, s, PanelSample{At: base.Add(time.Duration(i) * time.Minute), Goroutines: 1,
			ThemeCache: &PanelThemeCacheSample{Disk: state, MemoryLoaded: i%2 == 1}}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := readPanelHistory(ctx, s, 1, base.Add(8*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for i, state := range states {
		at := base.Add(time.Duration(i) * time.Minute)
		var got *PanelHistoryPoint
		for j := range history.Points {
			if history.Points[j].Time.Equal(at) {
				got = &history.Points[j]
				break
			}
		}
		if got == nil || got.ThemeCacheDisk == nil || *got.ThemeCacheDisk != state ||
			got.ThemeCacheMemoryLoaded == nil || *got.ThemeCacheMemoryLoaded != (i%2 == 1) {
			t.Fatalf("state %s: %+v", state, got)
		}
	}
}

func TestThemeCacheHistoryLiveTailAndPrivateDefinitions(t *testing.T) {
	ctx := context.Background()
	s, err := openStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().UTC().Truncate(5 * time.Minute)
	for _, sample := range []struct {
		at   time.Time
		disk string
	}{{base.Add(time.Second), "ready"}, {base.Add(time.Minute), "future-state"}} {
		err := writePanelSample(ctx, s, PanelSample{At: sample.at, Goroutines: 1,
			ThemeCache: &PanelThemeCacheSample{Disk: sample.disk}})
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := readPanelHistory(ctx, s, 24, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	last := history.Points[len(history.Points)-1]
	if last.ThemeCacheDisk != nil || last.ThemeCacheMemoryLoaded == nil || *last.ThemeCacheMemoryLoaded {
		t.Fatalf("latest unknown disk/false memory: %+v", last)
	}
	for _, name := range []string{MetricPanelThemeDisk, MetricPanelThemeMemory} {
		if !IsPanelMetric(name) {
			t.Fatalf("%s is not private", name)
		}
	}
	if err := deleteUndefinedMetrics(ctx, s); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{MetricPanelThemeDisk, MetricPanelThemeMemory} {
		if _, err := s.GetMetric(ctx, name); err != nil {
			t.Fatalf("reclaim removed %s: %v", name, err)
		}
	}
}

func TestThemeCacheDiskCodeRoundTripsKnownStates(t *testing.T) {
	// These numbers are persisted; changing one would reinterpret old history.
	for _, test := range []struct {
		name string
		code float64
	}{
		{"disabled", 0}, {"uninitialized", 1}, {"initializing", 2},
		{"ready", 3}, {"failed", 4}, {"degraded", 5},
	} {
		if got := panelThemeDiskCode(test.name); got != test.code {
			t.Fatalf("state %s = %v, want %v", test.name, got, test.code)
		}
		if got := panelThemeDiskStates[int(test.code)]; got != test.name {
			t.Fatalf("persisted code %v = %s, want %s", test.code, got, test.name)
		}
	}
	if got := panelThemeDiskCode("future"); got != -1 {
		t.Fatalf("unknown state = %v", got)
	}
}

func TestPanelHistoryDenseDefaultDayFitsReadBudget(t *testing.T) {
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
	now := time.Now().UTC().Truncate(5 * time.Minute).Add(4 * time.Minute)
	start := now.Add(-24 * time.Hour)
	for i := 0; i <= 24*60; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		pools := make([]metric.Point, 0, len(panelPoolMetricNames))
		for _, name := range panelPoolMetricNames {
			pools = append(pools, metric.Point{MetricName: name, EntityID: panelHistoryEntity, Timestamp: at, Value: float64(i)})
		}
		if err := writePanelSampleWithPools(ctx, s, PanelSample{At: at, RSSBytes: 200, HeapBytes: 100,
			Goroutines: 4, ThemeCache: &PanelThemeCacheSample{Disk: "ready", MemoryLoaded: true}}, pools); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Historical writes arrive too late to build sealed parents. Seed the
	// missing 5-minute tier in the disposable database as an already-running
	// sampler would have produced it, while retaining the real minute rows.
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO metric_resolutions (resolution_milli) VALUES (?) ON CONFLICT(resolution_milli) DO NOTHING`, []any{(5 * time.Minute).Milliseconds()}},
		{`INSERT INTO metric_rollups (series_id, resolution_id, label_id, bucket_milli, count, sum, sum_sq, min_val, max_val, first_val, first_ts_milli, last_val, last_ts_milli, digest, created_at_milli)
		 SELECT r.series_id, coarse.id, r.label_id, r.bucket_milli - r.bucket_milli % ?, r.count, r.sum, r.sum_sq, r.min_val, r.max_val,
		 r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli, r.digest, r.created_at_milli
		 FROM metric_rollups r JOIN metric_resolutions fine ON fine.id = r.resolution_id
		 JOIN metric_resolutions coarse ON coarse.resolution_milli = ?
		 WHERE fine.resolution_milli = ? AND r.bucket_milli % ? = 0
		 ON CONFLICT(series_id, resolution_id, label_id, bucket_milli) DO NOTHING`,
			[]any{(5 * time.Minute).Milliseconds(), (5 * time.Minute).Milliseconds(), time.Minute.Milliseconds(), (5 * time.Minute).Milliseconds()}},
	} {
		db, err := sql.Open("sqlite3", file)
		if err != nil {
			t.Fatal(err)
		}
		_, execErr := db.ExecContext(ctx, statement.query, statement.args...)
		closeErr := db.Close()
		if execErr != nil {
			t.Fatal(execErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	s = open()
	defer s.Close()
	history, err := readPanelHistory(ctx, s, 24, now)
	if err != nil {
		t.Fatalf("dense 11-metric default-day read: %v", err)
	}
	var populated int
	for _, p := range history.Points {
		if p.RSSBytes != nil && p.HeapBytes != nil && p.Goroutines != nil &&
			p.MainWaitCount != nil && p.MainWaitMS != nil && p.WriteWaitCount != nil &&
			p.WriteWaitMS != nil && p.ReadWaitCount != nil && p.ReadWaitMS != nil &&
			p.ThemeCacheDisk != nil && *p.ThemeCacheDisk == "ready" &&
			p.ThemeCacheMemoryLoaded != nil && *p.ThemeCacheMemoryLoaded {
			populated++
		}
	}
	if populated < 280 {
		t.Fatalf("dense history contained only %d fully populated buckets", populated)
	}
}
