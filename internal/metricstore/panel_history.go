package metricstore

import (
	"context"
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
}

type PanelHistoryPoint struct {
	Time       time.Time `json:"time"`
	RSSBytes   *float64  `json:"rss_bytes"`
	HeapBytes  *float64  `json:"heap_bytes"`
	Goroutines *float64  `json:"goroutines"`
}

type PanelHistory struct {
	IntervalSeconds int                 `json:"interval_seconds"`
	RetentionDays   map[string]int      `json:"retention_days"`
	Points          []PanelHistoryPoint `json:"points"`
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
			value := point.Value
			switch name {
			case MetricPanelRSS:
				result.Points[index].RSSBytes = &value
			case MetricPanelHeap:
				result.Points[index].HeapBytes = &value
			case MetricPanelGoroutines:
				result.Points[index].Goroutines = &value
			}
		}
		if interval > time.Minute {
			index := positions[now.Truncate(interval).UnixMilli()]
			replace := func(value *float64) {
				switch name {
				case MetricPanelRSS:
					result.Points[index].RSSBytes = value
				case MetricPanelHeap:
					result.Points[index].HeapBytes = value
				case MetricPanelGoroutines:
					result.Points[index].Goroutines = value
				}
			}
			// A fine observation supersedes a possibly stale coarse partial;
			// otherwise a real coarse observation is still valid historical data.
			for _, point := range recent.Values[name][metric.AggLast] {
				if point.Count <= 0 {
					continue
				}
				value := point.Value
				replace(&value)
			}
		}
	}
	return result, nil
}
