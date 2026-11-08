package metric

import (
	"context"
	"fmt"
	"time"
)

// rawMinuteKey deliberately excludes labels: labels can change when a raw
// point is replaced, but tags and the timestamp define its identity.
type rawMinuteKey struct {
	series rawSeriesKey
	bucket int64
}

type rawMinuteBaseline struct {
	// A negative lookup still carries the tags needed to invalidate only the
	// series selected by a later filtered deletion.
	tagsJSON string
	present  bool
	last     int64
	labels   map[string]*rollupBucket
}

type rawImportWindow struct {
	series     rawSeriesKey
	start, end int64 // half-open, at the imported resolution
}

// PrepareCurrentBatch performs the potentially blocking baseline lookup before
// a caller enters an identity/session fence. A later TryAppendCurrentBatch
// still rechecks the cached bound, minute, context, definitions, and locks.
func (s *Store) PrepareCurrentBatch(ctx context.Context, points []Point) error {
	if ctx == nil || len(points) == 0 || len(points) > 3 {
		return fmt.Errorf("%w: baseline preparation requires one to three points", ErrInvalidArgument)
	}
	prepared, err := prepareMetricPoints(points)
	if err != nil {
		return err
	}
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	filtered, err := s.filterDisabledMetricPoints(ctx, points)
	if err != nil {
		return err
	}
	if len(filtered) != len(points) {
		return ErrWriteUnavailable
	}
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.checkRawBaselines(ctx, prepared)
}

func minuteKeyForPoint(point preparedMetricPoint) rawMinuteKey {
	return rawMinuteKey{
		series: rawSeriesKey{metricName: point.metricName, entityID: point.entityID, tagsHash: point.tagsHash},
		bucket: bucketStartMillis(point.timestamp, time.Minute.Milliseconds()),
	}
}

// checkRawBaselines runs under ingestMu before any raw/hot mutation. The last
// persisted timestamp is an upper bound for all missing raw identities, across
// every label set. Only points strictly later than it can be admitted safely.
func (s *Store) checkRawBaselines(ctx context.Context, points []preparedMetricPoint) error {
	if len(s.cfg.RollupPolicy.Tiers) == 0 {
		return nil
	}
	// Stage lookups until the whole batch succeeds. In particular, a failed
	// lookup cannot make TryAppendCurrentBatch mistake unknown data for empty.
	lookups := make(map[rawMinuteKey]*rawMinuteBaseline)
	// Check all split-minute markers before doing any SQL or state mutation.
	s.rawMu.RLock()
	for _, point := range points {
		if err := ctx.Err(); err != nil {
			s.rawMu.RUnlock()
			return err
		}
		if _, incomplete := s.rawIncomplete[minuteKeyForPoint(point)]; incomplete {
			s.rawMu.RUnlock()
			return fmt.Errorf("%w: minute was partially deleted", ErrRawHistoryUnavailable)
		}
	}
	s.rawMu.RUnlock()
	for _, point := range points {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := minuteKeyForPoint(point)
		baseline, found := s.baseline[key]
		if !found {
			baseline, found = lookups[key]
			if !found {
				var err error
				baseline, err = s.loadRawBaseline(ctx, key, point.tagsJSON)
				if err != nil {
					return err
				}
				lookups[key] = baseline
			}
		}
		if baseline == nil || baseline.tagsJSON != point.tagsJSON {
			return fmt.Errorf("%w: invalid cached series identity", ErrRawHistoryUnavailable)
		}
		if baseline.present && point.timestamp <= baseline.last {
			return fmt.Errorf("%w: metric %q entity %q minute %s", ErrRawHistoryUnavailable,
				key.series.metricName, key.series.entityID, fromMillis(key.bucket).Format(time.RFC3339))
		}
	}
	for key, baseline := range lookups {
		s.baseline[key] = baseline
	}
	return nil
}

func (s *Store) loadRawBaseline(ctx context.Context, key rawMinuteKey, tagsJSON string) (*rawMinuteBaseline, error) {
	query := fmt.Sprintf(`SELECT s.entity_id, s.tags_hash, s.tags, l.labels_hash, l.labels,
		r.bucket_milli, r.count, r.sum, r.sum_sq, r.min_val, r.max_val,
		r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli, r.digest
		FROM %s s JOIN %s r ON r.series_id = s.id
		JOIN %s d ON d.id = r.resolution_id JOIN %s l ON l.id = r.label_id
		WHERE s.metric_name = %s AND s.entity_id = %s AND s.tags_hash = %s
		AND d.resolution_milli = %s AND r.bucket_milli = %s`,
		s.tables.series, s.tables.rollups, s.tables.resolutions, s.tables.labels,
		s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
		s.dialect.placeholder(4), s.dialect.placeholder(5))
	rows, err := s.db.QueryContext(ctx, query, key.series.metricName, key.series.entityID, key.series.tagsHash,
		time.Minute.Milliseconds(), key.bucket)
	if err != nil {
		return nil, fmt.Errorf("read exact-history baseline: %w", err)
	}
	stored, err := scanStoredRollupsForMaintenance(rows, true, s.cfg.RollupPolicy.compression())
	closeErr := rows.Close()
	if err != nil {
		return nil, fmt.Errorf("read exact-history baseline: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close exact-history baseline: %w", closeErr)
	}
	if len(stored) == 0 {
		// Fine-tier retention can remove this minute while an ancestor still
		// represents it. With no exact identities, a purported new point may
		// already be in that ancestor and must not revive the minute.
		for _, tier := range s.cfg.RollupPolicy.Tiers {
			if tier.Interval <= time.Minute {
				continue
			}
			var count int
			query := fmt.Sprintf(`SELECT COUNT(*) FROM %s s JOIN %s r ON r.series_id = s.id
				JOIN %s d ON d.id = r.resolution_id WHERE s.metric_name = %s AND s.entity_id = %s
				AND s.tags_hash = %s AND d.resolution_milli = %s AND r.bucket_milli = %s
				AND r.first_ts_milli < %s AND r.last_ts_milli >= %s`,
				s.tables.series, s.tables.rollups, s.tables.resolutions,
				s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
				s.dialect.placeholder(4), s.dialect.placeholder(5), s.dialect.placeholder(6), s.dialect.placeholder(7))
			if err := s.db.QueryRowContext(ctx, query, key.series.metricName, key.series.entityID, key.series.tagsHash,
				tier.Interval.Milliseconds(), bucketStartMillis(key.bucket, tier.Interval.Milliseconds()), key.bucket+time.Minute.Milliseconds(), key.bucket).Scan(&count); err != nil {
				return nil, fmt.Errorf("read coarse-history baseline: %w", err)
			}
			if count > 0 {
				return nil, fmt.Errorf("%w: only coarse history remains", ErrRawHistoryUnavailable)
			}
		}
		return &rawMinuteBaseline{tagsJSON: tagsJSON}, nil
	}
	baseline := &rawMinuteBaseline{tagsJSON: tagsJSON, present: true, last: stored[0].bucketData.lastTS, labels: make(map[string]*rollupBucket, len(stored))}
	for _, row := range stored {
		tags, err := decodeMapString(row.bucketData.tagsJSON)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid persisted series tags", ErrRawHistoryUnavailable)
		}
		hash, canonical, err := tagsFingerprint(tags)
		if err != nil || hash != key.series.tagsHash || canonical != tagsJSON || row.bucketData.count <= 0 || row.bucketData.firstTS > row.bucketData.lastTS ||
			bucketStartMillis(row.bucketData.firstTS, time.Minute.Milliseconds()) != key.bucket ||
			bucketStartMillis(row.bucketData.lastTS, time.Minute.Milliseconds()) != key.bucket {
			return nil, fmt.Errorf("%w: invalid persisted minute baseline", ErrRawHistoryUnavailable)
		}
		if row.bucketData.lastTS > baseline.last {
			baseline.last = row.bucketData.lastTS
		}
		baseline.labels[row.bucketData.labelsHash] = row.bucketData
	}
	return baseline, nil
}

// Caller holds ingestMu. Buckets outside the exact window cannot be rebuilt
// from raw samples; require a fresh persisted baseline if such a minute is
// ever written again. Future timestamps are kept until time catches up.
func (s *Store) pruneRawBaselines(now time.Time) {
	cutoff := rawMemoryCutoff(now)
	for key := range s.baseline {
		if key.bucket < cutoff {
			delete(s.baseline, key)
		}
	}
	s.rawMu.Lock()
	for key := range s.rawIncomplete {
		if key.bucket < cutoff {
			delete(s.rawIncomplete, key)
		}
	}
	s.rawMu.Unlock()
}

// A retention or explicit cutoff can remove some durable minute rows while
// leaving a coarse ancestor intact. A formerly empty cache entry no longer
// proves that a later point is new. Preserve live raw identities and fail
// closed for those affected minutes instead of reconstructing from partial data.
func (s *Store) invalidateRetainedMinuteBaselines(metricName string, before int64) {
	cutoff := bucketStartMillis(before, time.Minute.Milliseconds())
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	for key := range s.baseline {
		if key.series.metricName != metricName || key.bucket >= cutoff {
			continue
		}
		delete(s.baseline, key)
		if series := s.raw[key.series]; series != nil {
			s.rawIncomplete[key] = series.tagsJSON
		}
	}
}

func importedRawWindows(rollups []PersistedRollup) ([]rawImportWindow, error) {
	windows := make([]rawImportWindow, 0, len(rollups))
	for _, rollup := range rollups {
		if err := validatePersistedRollup(rollup); err != nil {
			return nil, err
		}
		hash, _, err := tagsFingerprint(rollup.Tags)
		if err != nil {
			return nil, err
		}
		start := rollup.Bucket.UTC().UnixMilli()
		end := start + rollup.Resolution.Milliseconds()
		if end <= start {
			return nil, fmt.Errorf("%w: rollup interval overflow", ErrInvalidArgument)
		}
		windows = append(windows, rawImportWindow{series: rawSeriesKey{metricName: rollup.MetricName, entityID: rollup.EntityID, tagsHash: hash}, start: start, end: end})
	}
	return windows, nil
}

func preparedRawWindows(points []preparedMetricPoint, interval time.Duration) []rawImportWindow {
	windows := make([]rawImportWindow, 0, len(points))
	for _, point := range points {
		start := bucketStartMillis(point.timestamp, interval.Milliseconds())
		windows = append(windows, rawImportWindow{series: minuteKeyForPoint(point).series, start: start, end: start + interval.Milliseconds()})
	}
	return windows
}

// Imports run under ingestMu, preventing a new hot bucket from appearing
// between this check and COMMIT. A matching buffered observation is not safe
// to discard or merge with a replacing import.
func (s *Store) checkImportPending(windows []rawImportWindow) error {
	overlaps := func(series rawSeriesKey, start, end int64) bool {
		for _, window := range windows {
			if window.series == series && start < window.end && window.start < end {
				return true
			}
		}
		return false
	}
	s.hotMu.RLock()
	for key := range s.hot {
		series := rawSeriesKey{metricName: key.metricName, entityID: key.entityID, tagsHash: key.tagsHash}
		if overlaps(series, key.bucket, key.bucket+time.Minute.Milliseconds()) {
			s.hotMu.RUnlock()
			return ErrImportPending
		}
	}
	s.hotMu.RUnlock()
	// Pending parents may contain descendants of the imported interval or
	// overwrite a parent the import replaces. Refuse either overlap.
	s.coarseMu.RLock()
	defer s.coarseMu.RUnlock()
	for key := range s.coarse {
		series := rawSeriesKey{metricName: key.metricName, entityID: key.entityID, tagsHash: key.tagsHash}
		if overlaps(series, key.bucket, key.bucket+key.interval.Milliseconds()) {
			return ErrImportPending
		}
	}
	return nil
}

// Import paths hold ingestMu. After an import, the durable minute supersedes
// the old raw identities; a future write must load the new baseline.
func (s *Store) invalidateRawWindows(windows []rawImportWindow) {
	contains := func(key rawMinuteKey) bool {
		for _, window := range windows {
			if window.series == key.series && key.bucket < window.end && window.start < key.bucket+time.Minute.Milliseconds() {
				return true
			}
		}
		return false
	}
	keys := make(map[rawMinuteKey]struct{})
	for key := range s.baseline {
		if contains(key) {
			keys[key] = struct{}{}
		}
	}
	for key := range keys {
		delete(s.baseline, key)
	}
	// Raw samples are shared by all labels. Remove only the imported minute,
	// preserving the rest of each compressed/direct series.
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	// A partial deletion marker must survive an import. Replacing one label
	// or a coarse tier does not prove all missing exact identities are gone.
	for seriesKey, series := range s.raw {
		decoder := newRawSampleDecoder(series.compressed)
		for sample, more := decoder.next(); more; sample, more = decoder.next() {
			key := rawMinuteKey{series: seriesKey, bucket: bucketStartMillis(sample.timestamp, time.Minute.Milliseconds())}
			if contains(key) {
				keys[key] = struct{}{}
			}
		}
		for _, sample := range series.samples {
			key := rawMinuteKey{series: seriesKey, bucket: bucketStartMillis(sample.timestamp, time.Minute.Milliseconds())}
			if contains(key) {
				keys[key] = struct{}{}
			}
		}
	}
	for key := range keys {
		series := s.raw[key.series]
		if series == nil {
			continue
		}
		decoder := newRawSampleDecoder(series.compressed)
		encoded := newCompressedRawSamples(len(series.compressed.data))
		for sample, more := decoder.next(); more; sample, more = decoder.next() {
			if bucketStartMillis(sample.timestamp, time.Minute.Milliseconds()) != key.bucket {
				encoded.appendSample(sample)
			}
		}
		series.compressed = encoded
		kept := series.samples[:0]
		for _, sample := range series.samples {
			if bucketStartMillis(sample.timestamp, time.Minute.Milliseconds()) != key.bucket {
				kept = append(kept, sample)
			}
		}
		series.samples = kept
		if len(series.samples) == 0 && series.compressed.count == 0 {
			delete(s.raw, key.series)
		} else {
			compactRawLabels(series)
		}
	}
}
