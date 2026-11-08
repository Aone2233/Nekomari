package metric

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// TryAppendCurrentBatch atomically appends a small batch of current samples to
// the raw and hot stores without waiting for a store lock or doing SQL. It
// rejects existing timestamps rather than rebuilding rollups, and leaves all
// state unchanged on any error. Normal compaction persists the hot buckets.
//
// This is intended for result admission while higher-level identity fences are
// held. A successful append linearizes at the last context check: cancellation
// afterwards does not revoke the already-admitted observation.
func (s *Store) TryAppendCurrentBatch(ctx context.Context, points []Point) error {
	return s.tryAppendCurrentBatch(ctx, points, time.Now)
}

func (s *Store) tryAppendCurrentBatch(ctx context.Context, points []Point, clock func() time.Time) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(points) == 0 || len(points) > 3 {
		return fmt.Errorf("%w: append requires one to three points", ErrInvalidArgument)
	}
	for _, point := range points {
		if len(point.MetricName) > 128 || len(point.EntityID) > 128 || len(point.Tags) > 8 || len(point.Labels) > 8 {
			return fmt.Errorf("%w: point exceeds nonblocking append limits", ErrInvalidArgument)
		}
		for key, value := range point.Tags {
			if len(key) > 64 || len(value) > 128 {
				return fmt.Errorf("%w: tag exceeds nonblocking append limits", ErrInvalidArgument)
			}
		}
		for key, value := range point.Labels {
			if len(key) > 64 || len(value) > 128 {
				return fmt.Errorf("%w: label exceeds nonblocking append limits", ErrInvalidArgument)
			}
		}
	}
	prepared, err := prepareMetricPoints(points)
	if err != nil {
		return err
	}
	// TryLock rather than spawning a timed goroutine: no abandoned writer may
	// commit after its caller releases the task/session admission fences.
	if !s.retentionMu.TryRLock() {
		return ErrWriteUnavailable
	}
	defer s.retentionMu.RUnlock()
	if !s.mu.TryRLock() {
		return ErrWriteUnavailable
	}
	if s.closed || s.db == nil {
		s.mu.RUnlock()
		return ErrClosed
	}
	s.mu.RUnlock()
	if !s.definitionsMu.TryRLock() {
		return ErrWriteUnavailable
	}
	snapshot := s.definitions
	if snapshot == nil {
		s.definitionsMu.RUnlock()
		return ErrWriteUnavailable
	}
	for _, point := range prepared {
		def, ok := snapshot.byName[point.metricName]
		if !ok {
			s.definitionsMu.RUnlock()
			return fmt.Errorf("%w: metric %q", ErrNotFound, point.metricName)
		}
		if def.RetentionDays <= 0 {
			s.definitionsMu.RUnlock()
			return ErrWriteUnavailable
		}
	}
	s.definitionsMu.RUnlock()
	if !s.ingestMu.TryLock() {
		return ErrWriteUnavailable
	}
	defer s.ingestMu.Unlock()
	// Query/flush paths take rollupViewMu before hotMu; use that order here.
	if !s.rollupViewMu.TryLock() {
		return ErrWriteUnavailable
	}
	defer s.rollupViewMu.Unlock()
	if s.hotCommitErr != nil {
		return s.hotCommitErr
	}
	if !s.rawMu.TryLock() {
		return ErrWriteUnavailable
	}
	defer s.rawMu.Unlock()
	if !s.hotMu.TryLock() {
		return ErrWriteUnavailable
	}
	defer s.hotMu.Unlock()

	now := clock().UTC()
	directCutoff := now.Add(-directRawRetention).UnixMilli()
	rawCutoff := rawMemoryCutoff(now)
	type sampleKey struct {
		series rawSeriesKey
		at     int64
	}
	seen := make(map[sampleKey]struct{}, len(prepared))
	for _, point := range prepared {
		// No past bucket may be revived after it has already been flushed. The
		// direct-window bound also excludes compressed replacement semantics.
		if point.timestamp < directCutoff || point.timestamp > now.UnixMilli() ||
			bucketStartMillis(point.timestamp, time.Minute.Milliseconds()) != bucketStartMillis(now.UnixMilli(), time.Minute.Milliseconds()) {
			return ErrWriteUnavailable
		}
		seriesKey := rawSeriesKey{metricName: point.metricName, entityID: point.entityID, tagsHash: point.tagsHash}
		if _, incomplete := s.rawIncomplete[minuteKeyForPoint(point)]; incomplete {
			return ErrWriteUnavailable
		}
		// This nonblocking path cannot look up a persisted baseline. An
		// uncached minute is refused rather than assuming its raw history is
		// complete after a reopen or import.
		if len(s.cfg.RollupPolicy.Tiers) > 0 {
			baseline, checked := s.baseline[minuteKeyForPoint(point)]
			if !checked || baseline == nil || baseline.tagsJSON != point.tagsJSON || (baseline.present && point.timestamp <= baseline.last) {
				return ErrWriteUnavailable
			}
		}
		key := sampleKey{series: seriesKey, at: point.timestamp}
		if _, exists := seen[key]; exists {
			return ErrWriteUnavailable
		}
		seen[key] = struct{}{}
		if series := s.raw[seriesKey]; series != nil {
			i := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= point.timestamp })
			if i < len(series.samples) && series.samples[i].timestamp == point.timestamp {
				return ErrWriteUnavailable
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.writeRawPointsLocked(prepared, directCutoff, rawCutoff, make(map[hotRollupKey]struct{}), make(map[*rawSeries]struct{}))
	compression := s.cfg.RollupPolicy.compression()
	rebuild := make(map[hotRollupKey]struct{})
	for _, point := range prepared {
		key := hotRollupKey{metricName: point.metricName, entityID: point.entityID, tagsHash: point.tagsHash,
			labelsHash: point.labelsHash, bucket: bucketStartMillis(point.timestamp, time.Minute.Milliseconds())}
		bucket := s.hot[key]
		if bucket == nil {
			bucket = newRollupBucket(compression)
			bucket.tagsHash, bucket.tagsJSON = point.tagsHash, point.tagsJSON
			bucket.labelsHash, bucket.labelsJSON = point.labelsHash, point.labelsJSON
			s.hot[key] = bucket
		}
		bucket.addPoint(point.value, point.timestamp)
		if baseline := s.baseline[minuteKeyForPoint(point)]; baseline != nil && baseline.present {
			rebuild[key] = struct{}{}
		}
	}
	s.rebuildHotRollupsWithRawLocked(rebuild, compression)
	return nil
}
