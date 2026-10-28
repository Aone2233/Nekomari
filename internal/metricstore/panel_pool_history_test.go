package metricstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func poolValues(points []metric.Point) map[string]float64 {
	result := make(map[string]float64, len(points))
	for _, point := range points {
		result[point.MetricName] = point.Value
	}
	return result
}

func TestPanelPoolContinuityAndIdentity(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 15, 0, time.UTC)
	mainDB, replacementDB := &sql.DB{}, &sql.DB{}
	store, replacementStore := &metric.Store{}, &metric.Store{}
	previous := poolSnapshot{at: base, store: store, mainDB: mainDB, hasRead: true,
		main:  sql.DBStats{WaitCount: 10, WaitDuration: time.Second},
		write: sql.DBStats{WaitCount: 20, WaitDuration: 2 * time.Second},
		read:  sql.DBStats{WaitCount: 30, WaitDuration: 3 * time.Second}}
	current := poolSnapshot{at: base.Add(time.Minute), store: store, mainDB: mainDB, hasRead: true,
		main:  sql.DBStats{WaitCount: 12, WaitDuration: 1500 * time.Millisecond},
		write: sql.DBStats{WaitCount: 20, WaitDuration: 2 * time.Second},
		read:  sql.DBStats{WaitCount: 33, WaitDuration: 3001250 * time.Microsecond}}
	check := func(t *testing.T, sampler PanelPoolSampler, next poolSnapshot, want map[string]float64) {
		t.Helper()
		got := poolValues(sampler.points(next))
		for _, name := range panelPoolMetricNames {
			value, ok := want[name]
			if !ok {
				value = panelPoolUnknown
			}
			if got[name] != value {
				t.Errorf("%s = %v, want %v", name, got[name], value)
			}
		}
	}
	t.Run("continuous including real zero and fractional milliseconds", func(t *testing.T) {
		check(t, PanelPoolSampler{previous}, current, map[string]float64{
			MetricPanelMainWaitCount: 2, MetricPanelMainWaitMS: 500,
			MetricPanelWriteWaitCount: 0, MetricPanelWriteWaitMS: 0,
			MetricPanelReadWaitCount: 3, MetricPanelReadWaitMS: 1.25,
		})
	})
	t.Run("new process with larger counters", func(t *testing.T) { check(t, PanelPoolSampler{}, current, nil) })
	t.Run("explicit missed collection", func(t *testing.T) {
		sampler := PanelPoolSampler{previous}
		sampler.Reset()
		check(t, sampler, current, nil)
	})
	t.Run("store replaced with larger counters", func(t *testing.T) {
		next := current
		next.store = replacementStore
		check(t, PanelPoolSampler{previous}, next, map[string]float64{MetricPanelMainWaitCount: 2, MetricPanelMainWaitMS: 500})
	})
	t.Run("main pool replaced", func(t *testing.T) {
		next := current
		next.mainDB = replacementDB
		check(t, PanelPoolSampler{previous}, next, map[string]float64{
			MetricPanelWriteWaitCount: 0, MetricPanelWriteWaitMS: 0, MetricPanelReadWaitCount: 3, MetricPanelReadWaitMS: 1.25})
	})
	t.Run("no separate read pool", func(t *testing.T) {
		next := current
		next.hasRead = false
		check(t, PanelPoolSampler{previous}, next, map[string]float64{
			MetricPanelMainWaitCount: 2, MetricPanelMainWaitMS: 500, MetricPanelWriteWaitCount: 0, MetricPanelWriteWaitMS: 0})
	})
	for _, gap := range []time.Duration{0, 20 * time.Second, 91 * time.Second, 2 * time.Minute, -time.Minute} {
		t.Run("gap "+gap.String(), func(t *testing.T) {
			next := current
			next.at = base.Add(gap)
			check(t, PanelPoolSampler{previous}, next, nil)
		})
	}
	t.Run("thirty seconds in the same minute is not continuous", func(t *testing.T) {
		next := current
		next.at = base.Add(30 * time.Second)
		check(t, PanelPoolSampler{previous}, next, nil)
	})
	t.Run("ninety seconds skipping a minute is not continuous", func(t *testing.T) {
		prev := previous
		prev.at = base.Add(40 * time.Second)
		next := current
		next.at = prev.at.Add(90 * time.Second)
		check(t, PanelPoolSampler{prev}, next, nil)
	})
	t.Run("counter decrease invalidates both readings", func(t *testing.T) {
		next := current
		next.main.WaitCount = 9
		next.read.WaitDuration = 2 * time.Second
		check(t, PanelPoolSampler{previous}, next, map[string]float64{MetricPanelWriteWaitCount: 0, MetricPanelWriteWaitMS: 0})
	})
}

func TestPanelPoolReadsRealSQLWaitCounters(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	base := time.Now().UTC().Truncate(time.Minute)
	previous := poolSnapshot{at: base, mainDB: db, main: db.Stats()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		other, err := db.Conn(ctx)
		if err == nil {
			_ = other.Close()
		}
		finished <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for db.Stats().WaitCount == previous.main.WaitCount && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if db.Stats().WaitCount != previous.main.WaitCount+1 {
		t.Fatal("second SQL connection did not wait")
	}
	cancel()
	if err := <-finished; err != context.Canceled {
		t.Fatalf("waiting for SQL connection: %v", err)
	}
	current := poolSnapshot{at: base.Add(time.Minute), mainDB: db, main: db.Stats()}
	values := poolValues((&PanelPoolSampler{previous: previous}).points(current))
	if values[MetricPanelMainWaitCount] != 1 || values[MetricPanelMainWaitMS] <= 0 {
		t.Fatalf("real SQL wait interval = count %v, duration %v ms", values[MetricPanelMainWaitCount], values[MetricPanelMainWaitMS])
	}
}

func TestPanelPoolLiveTailPrefersUnknownOverStaleCoarse(t *testing.T) {
	ctx := context.Background()
	s, err := openStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().UTC().Truncate(5 * time.Minute)
	for _, reading := range []struct {
		at    time.Time
		value float64
	}{{base.Add(time.Second), 7}, {base.Add(time.Minute), panelPoolUnknown}} {
		point := metric.Point{MetricName: MetricPanelWriteWaitCount, EntityID: panelHistoryEntity, Timestamp: reading.at, Value: reading.value}
		if err := writePanelSampleWithPools(ctx, s, PanelSample{At: reading.at, Goroutines: 1}, []metric.Point{point}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := readPanelHistory(ctx, s, 24, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if last := history.Points[len(history.Points)-1]; last.WriteWaitCount != nil {
		t.Fatalf("current coarse bucket concealed unknown latest sample: %+v", last)
	}
}

func TestPanelPoolUnknownSurvivesRollupAndRestart(t *testing.T) {
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
	write := func(at time.Time, value float64) {
		point := metric.Point{MetricName: MetricPanelWriteWaitCount, EntityID: panelHistoryEntity, Timestamp: at, Value: value}
		if err := writePanelSampleWithPools(ctx, s, PanelSample{At: at, Goroutines: 1}, []metric.Point{point}); err != nil {
			t.Fatal(err)
		}
	}
	write(base.Add(time.Minute), 5)
	write(base.Add(2*time.Minute), panelPoolUnknown)
	write(base.Add(6*time.Minute), 0)
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
		var unknown, zero *PanelHistoryPoint
		for i := range history.Points {
			point := &history.Points[i]
			if (hours == 24 && point.Time.Equal(base)) || (hours == 1 && point.Time.Equal(base.Add(2*time.Minute))) {
				unknown = point
			}
			if (hours == 24 && point.Time.Equal(base.Add(5*time.Minute))) || (hours == 1 && point.Time.Equal(base.Add(6*time.Minute))) {
				zero = point
			}
		}
		if unknown == nil || unknown.WriteWaitCount != nil {
			t.Fatalf("%dh unknown bucket: %+v", hours, unknown)
		}
		if zero == nil || zero.WriteWaitCount == nil || *zero.WriteWaitCount != 0 {
			t.Fatalf("%dh zero bucket: %+v", hours, zero)
		}
	}
	if _, err := s.UpdateMetricRetention(ctx, MetricPanelWriteWaitCount, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMetricDataIfDisabled(ctx, MetricPanelWriteWaitCount); err != nil {
		t.Fatal(err)
	}
	history, err := readPanelHistory(ctx, s, 24, base.Add(8*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if history.RetentionDays[MetricPanelWriteWaitCount] != 0 {
		t.Fatal("disabled policy not returned")
	}
	for _, point := range history.Points {
		if point.WriteWaitCount != nil {
			t.Fatal("disabled pool metric returned history")
		}
	}
}

func TestPanelPoolFailedWriteClearsBaseline(t *testing.T) {
	ctx := context.Background()
	s, err := openStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	storeMu.Lock()
	previousStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = previousStore
		storeMu.Unlock()
		_ = s.Close()
	}()
	sampler := PanelPoolSampler{previous: poolSnapshot{at: time.Now(), store: s}}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := sampler.Write(cancelled, PanelSample{At: time.Now(), Goroutines: 1}, nil); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	if !sampler.previous.at.IsZero() {
		t.Fatal("failed write kept baseline")
	}
	if err := sampler.Write(ctx, PanelSample{At: time.Now(), Goroutines: 0}, nil); err == nil {
		t.Fatal("invalid write succeeded")
	}
	if !sampler.previous.at.IsZero() {
		t.Fatal("invalid write kept baseline")
	}
	if err := sampler.Write(ctx, PanelSample{At: time.Now(), Goroutines: 1}, nil); err != nil {
		t.Fatal(err)
	}
	history, err := ReadPanelHistory(ctx, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if last := history.Points[len(history.Points)-1]; last.WriteWaitCount != nil {
		t.Fatal("first sample fabricated a delta")
	}
}
