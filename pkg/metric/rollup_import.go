package metric

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"
)

// ReplaceRollupPoints idempotently imports pre-aggregated representative
// values into the configured tiers through interval. Keeping the same
// representative in finer retained tiers prevents a newly migrated series
// from appearing blank while queries still prefer those tiers. Imported
// values never enter the exact ten-minute raw window.
func (s *Store) ReplaceRollupPoints(ctx context.Context, interval time.Duration, points []Point) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if !s.hasRollupTier(interval) {
		return fmt.Errorf("%w: rollup interval %s is not configured", ErrInvalidArgument, interval)
	}
	if len(points) == 0 {
		return nil
	}
	// An uncertain import commit must fence every supported writer before it
	// can observe or change the potentially replaced history.
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.pendingCommitError(); err != nil {
		return err
	}
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	points, err := s.filterDisabledMetricPoints(ctx, points)
	if err != nil || len(points) == 0 {
		return err
	}
	prepared, err := prepareMetricPoints(points)
	if err != nil {
		return err
	}
	importWindows := preparedRawWindows(prepared, interval)
	if err := s.checkImportPending(importWindows); err != nil {
		return err
	}

	tiers := make([]time.Duration, 0, len(s.cfg.RollupPolicy.Tiers))
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		if tier.Interval <= interval {
			tiers = append(tiers, tier.Interval)
		}
	}
	type importGroup struct {
		metricName string
		interval   time.Duration
		buckets    map[rollupKey]*rollupBucket
	}
	groups := make(map[string]*importGroup)
	for _, point := range prepared {
		for _, tier := range tiers {
			key := rollupKey{
				entityID: point.entityID, tagsHash: point.tagsHash, labelsHash: point.labelsHash,
				bucket: bucketStartMillis(point.timestamp, tier.Milliseconds()),
			}
			groupKey := point.metricName + "\x00" + tier.String()
			group := groups[groupKey]
			if group == nil {
				group = &importGroup{metricName: point.metricName, interval: tier, buckets: make(map[rollupKey]*rollupBucket)}
				groups[groupKey] = group
			}
			if _, exists := group.buckets[key]; exists {
				return fmt.Errorf("%w: duplicate pre-aggregated point for metric %q at %s", ErrInvalidArgument, point.metricName, fromMillis(key.bucket))
			}
			bucket := newRollupBucket(s.cfg.RollupPolicy.compression())
			bucket.tagsHash, bucket.tagsJSON = point.tagsHash, point.tagsJSON
			bucket.labelsHash, bucket.labelsJSON = point.labelsHash, point.labelsJSON
			bucket.addPoint(point.value, point.timestamp)
			group.buckets[key] = bucket
		}
	}

	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cache := newRollupDictionaryCache()
	for _, groupKey := range groupKeys {
		group := groups[groupKey]
		keys := make([]rollupKey, 0, len(group.buckets))
		for key := range group.buckets {
			keys = append(keys, key)
		}
		sortRollupKeys(keys)
		for _, key := range keys {
			if err := s.upsertRollupWithDictionaryTx(ctx, group.metricName, group.interval, key, group.buckets[key], cache, tx); err != nil {
				return err
			}
		}
	}
	err = tx.Commit()
	// The outcome of COMMIT may be unknown. Do not retain stale raw samples
	// or assume that the former persisted baseline still applies.
	s.invalidateRawWindows(importWindows)
	if err != nil {
		return s.markCommitUncertain(err)
	}
	return nil
}

// RebuildCoarserRollups recomputes every configured tier coarser than source
// from the complete source tier. Existing coarser-only history is preserved.
// A differing durable parent fails the entire transaction: retention may have
// removed some source rows, so replacing it would silently discard history.
func (s *Store) RebuildCoarserRollups(ctx context.Context, source time.Duration) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	sourceIndex := -1
	for i, tier := range s.cfg.RollupPolicy.Tiers {
		if tier.Interval == source {
			sourceIndex = i
			break
		}
	}
	if sourceIndex < 0 {
		return fmt.Errorf("%w: source rollup interval %s is not configured", ErrInvalidArgument, source)
	}
	if sourceIndex == len(s.cfg.RollupPolicy.Tiers)-1 {
		return nil
	}

	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	s.rollupViewMu.Lock()
	defer s.rollupViewMu.Unlock()
	// A pending parent or hot minute can later overwrite the rebuild. Import
	// and reconstruction must be coordinated by the caller instead.
	s.hotMu.RLock()
	hasHot := len(s.hot) != 0
	s.hotMu.RUnlock()
	s.coarseMu.RLock()
	hasCoarse := len(s.coarse) != 0
	s.coarseMu.RUnlock()
	if s.hotCommitErr != nil {
		return s.hotCommitErr
	}
	if hasHot || hasCoarse || s.coarseRecoveryPending {
		return ErrImportPending
	}
	defs, err := s.listMetricsFresh(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cache := newRollupDictionaryCache()
	for _, def := range defs {
		rows, err := s.scanRollupRows(ctx, tx, def.Name, source)
		if err != nil {
			return err
		}
		current := make(map[rollupKey]*rollupBucket, len(rows))
		for _, row := range rows {
			key := rollupKey{
				entityID: row.entityID, tagsHash: row.bucketData.tagsHash,
				labelsHash: row.bucketData.labelsHash, bucket: row.bucket,
			}
			current[key] = row.bucketData
		}
		for _, tier := range s.cfg.RollupPolicy.Tiers[sourceIndex+1:] {
			current = buildCoarserBucketsFromDelta(current, tier.Interval, s.cfg.RollupPolicy.compression())
			keys := make([]rollupKey, 0, len(current))
			for key := range current {
				keys = append(keys, key)
			}
			sortRollupKeys(keys)
			for _, key := range keys {
				stored, err := s.readRollupBucketTx(ctx, def.Name, key, tier.Interval, tx)
				if err != nil {
					return err
				}
				if stored != nil {
					if !equalRebuiltRollup(stored, current[key]) {
						return fmt.Errorf("%w: metric %q entity %q interval %s bucket %s", ErrRebuildConflict,
							def.Name, key.entityID, tier.Interval, fromMillis(key.bucket))
					}
					continue
				}
				if err := s.upsertRollupWithDictionaryTx(ctx, def.Name, tier.Interval, key, current[key], cache, tx); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		// The view lock is already held; do not attempt a blind retry.
		s.hotCommitErr = fmt.Errorf("%w: %v", ErrCommitUncertain, err)
		return s.hotCommitErr
	}
	return nil
}

func equalRebuiltRollup(a, b *rollupBucket) bool {
	if a.count != b.count || a.sum != b.sum || a.sumSq != b.sumSq || a.min != b.min || a.max != b.max ||
		a.firstVal != b.firstVal || a.firstTS != b.firstTS || a.lastVal != b.lastVal || a.lastTS != b.lastTS {
		return false
	}
	// Encoding works on clones because a t-digest can normalize itself while
	// being encoded. Different sketches are not safe to treat as equal.
	return bytes.Equal(a.clone(true).encodedDigest(), b.clone(true).encodedDigest())
}

func (s *Store) hasRollupTier(interval time.Duration) bool {
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		if tier.Interval == interval {
			return true
		}
	}
	return false
}
