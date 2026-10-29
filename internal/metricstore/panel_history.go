package metricstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

const panelHistoryEntity = "_nekomari_panel"

// PanelSample contains only instantaneous process readings. RSS is unknown
// when it cannot be read, rather than an observed zero.
type PanelSample struct {
	At         time.Time
	RSSBytes   uint64
	HeapBytes  uint64
	Goroutines int
	ThemeCache *PanelThemeCacheSample
}

// Theme cache values are sampled process observations, not disk health probes.
type PanelThemeCacheSample struct {
	Disk         string
	MemoryLoaded bool
}

type PanelHistoryPoint struct {
	Time                   time.Time `json:"time"`
	RSSBytes               *float64  `json:"rss_bytes"`
	HeapBytes              *float64  `json:"heap_bytes"`
	Goroutines             *float64  `json:"goroutines"`
	MainWaitCount          *float64  `json:"main_wait_count"`
	MainWaitMS             *float64  `json:"main_wait_ms"`
	WriteWaitCount         *float64  `json:"write_wait_count"`
	WriteWaitMS            *float64  `json:"write_wait_ms"`
	ReadWaitCount          *float64  `json:"read_wait_count"`
	ReadWaitMS             *float64  `json:"read_wait_ms"`
	ThemeCacheDisk         *string   `json:"theme_cache_disk"`
	ThemeCacheMemoryLoaded *bool     `json:"theme_cache_memory_loaded"`
}

type PanelHistory struct {
	IntervalSeconds int                 `json:"interval_seconds"`
	RetentionDays   map[string]int      `json:"retention_days"`
	Points          []PanelHistoryPoint `json:"points"`
}

func clearPanelHistoryValue(point *PanelHistoryPoint, name string) {
	switch name {
	case MetricPanelRSS:
		point.RSSBytes = nil
	case MetricPanelHeap:
		point.HeapBytes = nil
	case MetricPanelGoroutines:
		point.Goroutines = nil
	case MetricPanelMainWaitCount:
		point.MainWaitCount = nil
	case MetricPanelMainWaitMS:
		point.MainWaitMS = nil
	case MetricPanelWriteWaitCount:
		point.WriteWaitCount = nil
	case MetricPanelWriteWaitMS:
		point.WriteWaitMS = nil
	case MetricPanelReadWaitCount:
		point.ReadWaitCount = nil
	case MetricPanelReadWaitMS:
		point.ReadWaitMS = nil
	case MetricPanelThemeDisk:
		point.ThemeCacheDisk = nil
	case MetricPanelThemeMemory:
		point.ThemeCacheMemoryLoaded = nil
	}
}

// These indexes are persisted as metric values. Never reorder or reuse them;
// append new states and keep old codes readable across releases.
var panelThemeDiskStates = [...]string{"disabled", "uninitialized", "initializing", "ready", "failed", "degraded"}

func panelThemeDiskCode(state string) float64 {
	for code, known := range panelThemeDiskStates {
		if state == known {
			return float64(code)
		}
	}
	return -1
}

// PanelPoolSampler remembers only the preceding successful sample. A new
// process therefore never compares its counters with a previous process.
// Collect is serialized by panelhistory.Sampler.
type PanelPoolSampler struct {
	previous poolSnapshot
}

type poolSnapshot struct {
	at                time.Time
	store             *metric.Store
	mainDB            *sql.DB
	main, write, read sql.DBStats
	hasRead           bool
}

const panelPoolUnknown = -1.0

// Reset prevents a cancelled or skipped collection from bridging its gap.
// The caller must serialize Reset and Write.
func (p *PanelPoolSampler) Reset() {
	p.previous = poolSnapshot{}
}

// Write acquires one store lease for the pool snapshot and the write. Reload
// cannot switch or close the store between those two operations.
func (p *PanelPoolSampler) Write(ctx context.Context, sample PanelSample, mainDB *sql.DB) error {
	if err := storeOperations.AcquireShared(ctx); err != nil {
		p.previous = poolSnapshot{}
		return err
	}
	defer storeOperations.ReleaseShared()
	s := GetStore()
	if s == nil {
		p.previous = poolSnapshot{}
		return ErrStoreNotInitialized
	}
	current := poolSnapshot{at: sample.At, store: s, mainDB: mainDB}
	if current.mainDB != nil {
		current.main = current.mainDB.Stats()
	}
	pools := s.PoolStats()
	current.write, current.read, current.hasRead = pools.Write, pools.Read, pools.HasRead
	points := p.points(current)
	if err := writePanelSampleWithPools(ctx, s, sample, points); err != nil {
		p.previous = poolSnapshot{}
		return err
	}
	p.previous = current
	return nil
}

func (p *PanelPoolSampler) points(current poolSnapshot) []metric.Point {
	previous := p.previous
	elapsed := current.at.Sub(previous.at)
	// Monotonic time detects wall-clock changes; distinct consecutive minute
	// buckets ensure no missing or duplicated minute is silently bridged.
	continuous := !previous.at.IsZero() && elapsed >= 30*time.Second && elapsed <= 90*time.Second &&
		current.at.UTC().Truncate(time.Minute).Sub(previous.at.UTC().Truncate(time.Minute)) == time.Minute
	points := make([]metric.Point, 0, len(panelPoolMetricNames))
	appendPool := func(countName, durationName string, same bool, before, after sql.DBStats) {
		count, duration := panelPoolUnknown, panelPoolUnknown
		if continuous && same && before.WaitCount >= 0 && before.WaitDuration >= 0 &&
			after.WaitCount >= before.WaitCount && after.WaitDuration >= before.WaitDuration {
			count = float64(after.WaitCount - before.WaitCount)
			duration = float64(after.WaitDuration-before.WaitDuration) / float64(time.Millisecond)
		}
		points = append(points,
			metric.Point{MetricName: countName, EntityID: panelHistoryEntity, Timestamp: current.at, Value: count},
			metric.Point{MetricName: durationName, EntityID: panelHistoryEntity, Timestamp: current.at, Value: duration})
	}
	appendPool(MetricPanelMainWaitCount, MetricPanelMainWaitMS, current.mainDB != nil && current.mainDB == previous.mainDB, previous.main, current.main)
	appendPool(MetricPanelWriteWaitCount, MetricPanelWriteWaitMS, current.store != nil && current.store == previous.store, previous.write, current.write)
	appendPool(MetricPanelReadWaitCount, MetricPanelReadWaitMS, current.store != nil && current.store == previous.store && current.hasRead && previous.hasRead, previous.read, current.read)
	return points
}

// WritePanelSample holds a shared store lease across the entire write, so a
// reload, reclaim or shutdown cannot replace the store during sampling.
func WritePanelSample(ctx context.Context, sample PanelSample) error {
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return err
	}
	defer storeOperations.ReleaseShared()
	s := GetStore()
	if s == nil {
		return ErrStoreNotInitialized
	}
	return writePanelSample(ctx, s, sample)
}

func writePanelSample(ctx context.Context, s *metric.Store, sample PanelSample) error {
	return writePanelSampleWithPools(ctx, s, sample, nil)
}

func writePanelSampleWithPools(ctx context.Context, s *metric.Store, sample PanelSample, poolPoints []metric.Point) error {
	if sample.At.IsZero() || sample.Goroutines < 1 {
		return fmt.Errorf("invalid panel sample")
	}
	points := []metric.Point{
		{MetricName: MetricPanelHeap, EntityID: panelHistoryEntity, Timestamp: sample.At, Value: float64(sample.HeapBytes)},
		{MetricName: MetricPanelGoroutines, EntityID: panelHistoryEntity, Timestamp: sample.At, Value: float64(sample.Goroutines)},
	}
	if sample.RSSBytes > 0 {
		points = append(points, metric.Point{MetricName: MetricPanelRSS, EntityID: panelHistoryEntity, Timestamp: sample.At, Value: float64(sample.RSSBytes)})
	}
	if sample.ThemeCache != nil {
		memory := 0.0
		if sample.ThemeCache.MemoryLoaded {
			memory = 1
		}
		points = append(points,
			metric.Point{MetricName: MetricPanelThemeDisk, EntityID: panelHistoryEntity, Timestamp: sample.At, Value: panelThemeDiskCode(sample.ThemeCache.Disk)},
			metric.Point{MetricName: MetricPanelThemeMemory, EntityID: panelHistoryEntity, Timestamp: sample.At, Value: memory})
	}
	points = append(points, poolPoints...)
	return s.WriteBatch(ctx, points)
}

// ReadPanelHistory accepts only the two fixed windows exposed by the admin
// view. Both the row budget and the store lease cover the complete batch.
func ReadPanelHistory(ctx context.Context, hours int, now time.Time) (PanelHistory, error) {
	if hours != 1 && hours != 24 {
		return PanelHistory{}, fmt.Errorf("hours must be 1 or 24")
	}
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return PanelHistory{}, err
	}
	defer storeOperations.ReleaseShared()
	s := GetStore()
	if s == nil {
		return PanelHistory{}, ErrStoreNotInitialized
	}
	return readPanelHistory(ctx, s, hours, now)
}

func readPanelHistory(ctx context.Context, s *metric.Store, hours int, now time.Time) (PanelHistory, error) {
	if hours != 1 && hours != 24 {
		return PanelHistory{}, fmt.Errorf("hours must be 1 or 24")
	}
	now = now.UTC()
	interval := time.Minute
	if hours == 24 {
		interval = 5 * time.Minute
	}
	start := now.Add(-time.Duration(hours) * time.Hour).Truncate(interval)
	interval = s.CompatibleSeriesInterval(start, now, interval)
	start = now.Add(-time.Duration(hours) * time.Hour).Truncate(interval)
	specs := make([]metric.BatchSeriesSpec, 0, len(panelMetricNames))
	setValue := func(point *PanelHistoryPoint, name string, value float64) {
		if IsPanelPoolMetric(name) && value < 0 {
			return
		}
		switch name {
		case MetricPanelRSS:
			point.RSSBytes = &value
		case MetricPanelHeap:
			point.HeapBytes = &value
		case MetricPanelGoroutines:
			point.Goroutines = &value
		case MetricPanelMainWaitCount:
			point.MainWaitCount = &value
		case MetricPanelMainWaitMS:
			point.MainWaitMS = &value
		case MetricPanelWriteWaitCount:
			point.WriteWaitCount = &value
		case MetricPanelWriteWaitMS:
			point.WriteWaitMS = &value
		case MetricPanelReadWaitCount:
			point.ReadWaitCount = &value
		case MetricPanelReadWaitMS:
			point.ReadWaitMS = &value
		case MetricPanelThemeDisk:
			for code, state := range panelThemeDiskStates {
				if value == float64(code) {
					point.ThemeCacheDisk = &state
					break
				}
			}
		case MetricPanelThemeMemory:
			if value == 0 || value == 1 {
				loaded := value == 1
				point.ThemeCacheMemoryLoaded = &loaded
			}
		}
	}
	for _, name := range panelMetricNames {
		specs = append(specs, metric.BatchSeriesSpec{MetricName: name, Aggregations: []metric.Aggregation{metric.AggLast}, Interval: interval})
	}
	ctx = metric.WithReadBudgetBytes(ctx, 10000, 8<<20)
	batch, err := s.SeriesBatch(ctx, metric.BatchSeriesQuery{
		Specs: specs, EntityIDs: []string{panelHistoryEntity}, Start: start, End: now, Order: metric.OrderAsc,
	}, now)
	if err != nil {
		return PanelHistory{}, err
	}
	// Coarse parents lag the in-memory minute tier. Replace only the current
	// coarse bucket with a bounded finest-tier read so the view is fresh without
	// summing or duplicating an overlapping partial parent.
	var recent metric.BatchSeriesResult
	if interval > time.Minute {
		freshSpecs := make([]metric.BatchSeriesSpec, 0, len(panelMetricNames))
		for _, name := range panelMetricNames {
			freshSpecs = append(freshSpecs, metric.BatchSeriesSpec{MetricName: name, Aggregations: []metric.Aggregation{metric.AggLast}, Interval: time.Minute})
		}
		// The fine tier normally retains at least the recent ten minutes. Never
		// scan an unexpectedly large custom coarse bucket to repair its tail.
		freshStart := now.Truncate(interval)
		if limit := now.Add(-10 * time.Minute).Truncate(time.Minute); freshStart.Before(limit) {
			freshStart = limit
		}
		recent, err = s.SeriesBatch(ctx, metric.BatchSeriesQuery{
			Specs: freshSpecs, EntityIDs: []string{panelHistoryEntity}, Start: freshStart, End: now, Order: metric.OrderAsc,
		}, now)
		if err != nil {
			return PanelHistory{}, err
		}
	}
	result := PanelHistory{IntervalSeconds: int(interval.Seconds()), RetentionDays: make(map[string]int, len(panelMetricNames))}
	positions := make(map[int64]int)
	for at := start; !at.After(now); at = at.Add(interval) {
		positions[at.UnixMilli()] = len(result.Points)
		result.Points = append(result.Points, PanelHistoryPoint{Time: at})
	}
	for _, name := range panelMetricNames {
		def, ok := batch.Definitions[name]
		if !ok {
			return PanelHistory{}, fmt.Errorf("panel metric definition missing: %s", name)
		}
		result.RetentionDays[name] = def.RetentionDays
		if def.RetentionDays == 0 {
			continue
		}
		for _, point := range batch.Values[name][metric.AggLast] {
			index, ok := positions[point.Bucket.UnixMilli()]
			if !ok || point.Count <= 0 {
				continue
			}
			setValue(&result.Points[index], name, point.Value)
		}
		if interval > time.Minute {
			index := positions[now.Truncate(interval).UnixMilli()]
			// A fine observation supersedes a possibly stale coarse partial;
			// otherwise a real coarse observation is still valid historical data.
			for _, point := range recent.Values[name][metric.AggLast] {
				if point.Count <= 0 {
					continue
				}
				// Clear a stale coarse value when the latest fine sample is unknown.
				clearPanelHistoryValue(&result.Points[index], name)
				setValue(&result.Points[index], name, point.Value)
			}
		}
	}
	return result, nil
}
