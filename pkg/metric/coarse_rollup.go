package metric

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

const coarseRollupGrace = 10 * time.Minute

// SQLite's default variable limit is 999; each normalized rollup row binds
// fifteen values, so keep one batched upsert comfortably below that limit.
const coarseRollupWriteBatchSize = 60

// coarseRollupKey identifies one unsealed materialized parent. Its children
// are retained independently so a late minute replacement can overwrite one
// child without attempting to subtract a t-digest contribution.
type coarseRollupKey struct {
	metricName string
	interval   time.Duration
	rollupKey
}

type coarseRollup struct {
	// higher lists the active tiers above interval, nearest first.
	higher   []time.Duration
	children map[rollupKey]*rollupBucket
}

type closedCoarseRollup struct {
	key      coarseRollupKey
	bucket   *rollupBucket
	higher   []time.Duration
	children map[rollupKey]*rollupBucket
}

func (s *Store) addMinuteParents(metricName string, policy RollupPolicy, minute map[rollupKey]*rollupBucket, now time.Time) {
	if len(policy.Tiers) < 2 || len(minute) == 0 {
		return
	}
	interval := policy.Tiers[1].Interval
	eligible := make(map[rollupKey]*rollupBucket, len(minute))
	for key, bucket := range minute {
		parentStart := bucketStartMillis(key.bucket, interval.Milliseconds())
		if parentStart+interval.Milliseconds()+coarseRollupGrace.Milliseconds() <= now.UTC().UnixMilli() {
			// A parent that has already sealed must remain immutable. The minute
			// row still records the late sample, but it is deliberately not fed
			// back into a persisted coarse bucket.
			continue
		}
		eligible[key] = bucket
	}
	if len(eligible) == 0 {
		return
	}
	higher := make([]time.Duration, 0, len(policy.Tiers)-2)
	for _, tier := range policy.Tiers[2:] {
		higher = append(higher, tier.Interval)
	}
	s.addCoarseChildren(metricName, interval, higher, eligible)
}

func (s *Store) addCoarseChildren(metricName string, interval time.Duration, higher []time.Duration, children map[rollupKey]*rollupBucket) {
	if interval <= 0 || len(children) == 0 {
		return
	}
	s.coarseMu.Lock()
	defer s.coarseMu.Unlock()
	for childKey, child := range children {
		parentKey := coarseRollupKey{
			metricName: metricName,
			interval:   interval,
			rollupKey: rollupKey{
				entityID: childKey.entityID, tagsHash: childKey.tagsHash, labelsHash: childKey.labelsHash,
				bucket: bucketStartMillis(childKey.bucket, interval.Milliseconds()),
			},
		}
		if child == nil || child.count == 0 {
			// A late raw replacement can move an observation to a different
			// label set. Remove its stale child before staging the replacement;
			// skipping it would leave the old digest in the eventual parent.
			if parent := s.coarse[parentKey]; parent != nil {
				delete(parent.children, childKey)
				if len(parent.children) == 0 {
					delete(s.coarse, parentKey)
				}
			}
			continue
		}
		parent := s.coarse[parentKey]
		if parent == nil {
			parent = &coarseRollup{
				higher:   append([]time.Duration(nil), higher...),
				children: make(map[rollupKey]*rollupBucket),
			}
			s.coarse[parentKey] = parent
		}
		parent.children[childKey] = child.clone(true)
	}
}

// flushClosedCoarseRollups persists only parents whose end is outside the
// late-arrival window. It flushes one tier at a time so a sealed child can feed
// the next parent without any periodic rewrite of that parent.
func (s *Store) flushClosedCoarseRollups(ctx context.Context, now time.Time) (int, error) {
	s.rollupViewMu.Lock()
	defer s.rollupViewMu.Unlock()
	return s.flushClosedCoarseRollupsUnderView(ctx, now)
}

func (s *Store) flushClosedCoarseRollupsUnderView(ctx context.Context, now time.Time) (int, error) {
	if err := s.recoverPendingCoarseRollups(ctx); err != nil {
		return 0, err
	}
	now = now.UTC()
	written := 0
	for {
		closed := s.takeClosedCoarseRollups(now)
		if len(closed) == 0 {
			return written, nil
		}
		sealed, err := s.filterClosedCoarseRollups(ctx, closed)
		if err != nil {
			s.restoreClosedCoarseRollups(closed)
			return written, err
		}
		if len(sealed) == 0 {
			continue
		}
		n, err := s.persistClosedCoarseRollups(ctx, sealed)
		if err != nil {
			s.restoreClosedCoarseRollups(sealed)
			return written, err
		}
		written += n
		for _, item := range sealed {
			if len(item.higher) == 0 {
				continue
			}
			s.addCoarseChildren(item.key.metricName, item.higher[0], item.higher[1:], map[rollupKey]*rollupBucket{
				item.key.rollupKey: item.bucket,
			})
		}
	}
}

// Recover only absent parents, never overwrite a sealed historical bucket.
// Durable children, rather than raw samples, make restart recovery idempotent.
func (s *Store) recoverPendingCoarseRollups(ctx context.Context) error {
	if !s.coarseRecoveryPending {
		return nil
	}
	definitions, err := s.ListMetrics(ctx)
	if err != nil {
		return err
	}
	for _, def := range definitions {
		if def.RetentionDays <= 0 {
			continue
		}
		policy := s.cfg.RollupPolicy.withMetricRetention(time.Duration(def.RetentionDays) * 24 * time.Hour)
		for i := 1; i < len(policy.Tiers); i++ {
			fine, coarse := policy.Tiers[i-1].Interval, policy.Tiers[i].Interval
			// Bucket timestamps are nonnegative Unix milliseconds. Integer division
			// works with SQLite, PostgreSQL and MySQL without dialect-specific dates.
			parentStart := fmt.Sprintf("(r.bucket_milli / %d) * %d", coarse.Milliseconds(), coarse.Milliseconds())
			query := fmt.Sprintf(`SELECT s.entity_id, s.tags_hash, s.tags, l.labels_hash, l.labels,
				r.bucket_milli, r.count, r.sum, r.sum_sq, r.min_val, r.max_val,
				r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli, r.digest
				FROM %s r JOIN %s s ON s.id = r.series_id
				JOIN %s d ON d.id = r.resolution_id JOIN %s l ON l.id = r.label_id
				WHERE s.metric_name = %s AND d.resolution_milli = %s
				AND NOT EXISTS (SELECT 1 FROM %s p JOIN %s pd ON pd.id = p.resolution_id
					WHERE p.series_id = r.series_id AND p.label_id = r.label_id
					AND pd.resolution_milli = %s AND p.bucket_milli = %s)
				ORDER BY r.bucket_milli ASC`, s.tables.rollups, s.tables.series, s.tables.resolutions,
				s.tables.labels, s.dialect.placeholder(1), s.dialect.placeholder(2), s.tables.rollups,
				s.tables.resolutions, s.dialect.placeholder(3), parentStart)
			rows, err := s.reader().QueryContext(ctx, query, def.Name, fine.Milliseconds(), coarse.Milliseconds())
			if err != nil {
				return err
			}
			children, err := scanStoredRollupsForMaintenance(rows, true, policy.compression())
			_ = rows.Close()
			if err != nil {
				return err
			}
			higher := make([]time.Duration, 0, len(policy.Tiers)-i-1)
			for _, tier := range policy.Tiers[i+1:] {
				higher = append(higher, tier.Interval)
			}
			for _, child := range children {
				key := rollupKey{entityID: child.entityID, tagsHash: child.bucketData.tagsHash, labelsHash: child.bucketData.labelsHash, bucket: child.bucket}
				s.addCoarseChildren(def.Name, coarse, higher, map[rollupKey]*rollupBucket{key: child.bucketData})
			}
		}
	}
	s.coarseRecoveryPending = false
	return nil
}

func (s *Store) filterClosedCoarseRollups(ctx context.Context, closed []closedCoarseRollup) ([]closedCoarseRollup, error) {
	policies := make(map[string]RollupPolicy)
	filtered := make([]closedCoarseRollup, 0, len(closed))
	for _, item := range closed {
		policy, ok := policies[item.key.metricName]
		if !ok {
			var err error
			policy, err = s.metricRollupPolicy(ctx, item.key.metricName)
			if err != nil {
				return nil, err
			}
			policies[item.key.metricName] = policy
		}
		for _, tier := range policy.Tiers {
			if tier.Interval == item.key.interval {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered, nil
}

func (s *Store) takeClosedCoarseRollups(now time.Time) []closedCoarseRollup {
	s.coarseMu.Lock()
	defer s.coarseMu.Unlock()
	var interval time.Duration
	for key := range s.coarse {
		if key.bucket+key.interval.Milliseconds()+coarseRollupGrace.Milliseconds() > now.UnixMilli() {
			continue
		}
		if interval == 0 || key.interval < interval {
			interval = key.interval
		}
	}
	if interval == 0 {
		return nil
	}
	closed := make([]closedCoarseRollup, 0)
	for key, parent := range s.coarse {
		if key.interval != interval || key.bucket+key.interval.Milliseconds()+coarseRollupGrace.Milliseconds() > now.UnixMilli() {
			continue
		}
		bucket := newRollupBucket(s.cfg.RollupPolicy.compression())
		childKeys := make([]rollupKey, 0, len(parent.children))
		for childKey := range parent.children {
			childKeys = append(childKeys, childKey)
		}
		sortRollupKeys(childKeys)
		for _, childKey := range childKeys {
			bucket.mergeStored(parent.children[childKey])
		}
		if bucket.count != 0 {
			bucket.tagsHash, bucket.tagsJSON = key.tagsHash, parent.children[childKeys[0]].tagsJSON
			bucket.labelsHash, bucket.labelsJSON = key.labelsHash, parent.children[childKeys[0]].labelsJSON
			closed = append(closed, closedCoarseRollup{
				key: key, bucket: bucket, higher: append([]time.Duration(nil), parent.higher...), children: parent.children,
			})
		}
		delete(s.coarse, key)
	}
	return closed
}

func (s *Store) restoreClosedCoarseRollups(closed []closedCoarseRollup) {
	s.coarseMu.Lock()
	defer s.coarseMu.Unlock()
	for _, item := range closed {
		if _, exists := s.coarse[item.key]; exists {
			continue
		}
		s.coarse[item.key] = &coarseRollup{
			higher:   append([]time.Duration(nil), item.higher...),
			children: item.children,
		}
	}
}

func (s *Store) persistClosedCoarseRollups(ctx context.Context, closed []closedCoarseRollup) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	byMetric := make(map[string]map[time.Duration]map[rollupKey]*rollupBucket)
	for _, item := range closed {
		byInterval := byMetric[item.key.metricName]
		if byInterval == nil {
			byInterval = make(map[time.Duration]map[rollupKey]*rollupBucket)
			byMetric[item.key.metricName] = byInterval
		}
		buckets := byInterval[item.key.interval]
		if buckets == nil {
			buckets = make(map[rollupKey]*rollupBucket)
			byInterval[item.key.interval] = buckets
		}
		buckets[item.key.rollupKey] = item.bucket
	}

	names := make([]string, 0, len(byMetric))
	for name := range byMetric {
		names = append(names, name)
	}
	sort.Strings(names)
	written := 0
	for _, name := range names {
		cache := newRollupDictionaryCache()
		intervals := make([]time.Duration, 0, len(byMetric[name]))
		for interval := range byMetric[name] {
			intervals = append(intervals, interval)
		}
		sort.Slice(intervals, func(i, j int) bool { return intervals[i] < intervals[j] })
		for _, interval := range intervals {
			n, err := s.upsertClosedCoarseBucketsTx(ctx, name, interval, byMetric[name][interval], cache, tx)
			if err != nil {
				return written, err
			}
			written += n
		}
	}
	if err := tx.Commit(); err != nil {
		return written, err
	}
	return written, nil
}

// upsertClosedCoarseBucketsTx writes a fully sealed parent exactly once. It
// intentionally bypasses the minute-tier read/merge path: a parent cannot be
// incrementally updated after this point, and re-reading it would reintroduce
// the avoidable I/O this accumulator exists to remove.
func (s *Store) upsertClosedCoarseBucketsTx(ctx context.Context, metricName string, interval time.Duration, buckets map[rollupKey]*rollupBucket, cache *rollupDictionaryCache, tx *sql.Tx) (int, error) {
	if len(buckets) == 0 {
		return 0, nil
	}
	keys := make([]rollupKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sortRollupKeys(keys)
	rows := make([]normalizedRollupRow, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		key.bucket = normalizeBucketMillis(key.bucket)
		seriesID, err := cache.seriesID(ctx, s, tx, metricName, key, bucket.tagsJSON)
		if err != nil {
			return 0, err
		}
		labelID, err := cache.labelID(ctx, s, tx, key.labelsHash, bucket.labelsJSON)
		if err != nil {
			return 0, err
		}
		resolutionID, err := cache.resolutionID(ctx, s, tx, interval)
		if err != nil {
			return 0, err
		}
		rows = append(rows, normalizedRollupRow{
			seriesID: seriesID, resolutionID: resolutionID, labelID: labelID,
			bucketMilli: key.bucket, count: bucket.count, sum: bucket.sum, sumSq: bucket.sumSq,
			min: bucket.min, max: bucket.max, firstVal: bucket.firstVal, firstTSMilli: bucket.firstTS,
			lastVal: bucket.lastVal, lastTSMilli: bucket.lastTS, digest: bucket.encodedDigest(),
			createdAtMilli: timeMillis(time.Now()),
		})
	}
	for start := 0; start < len(rows); start += coarseRollupWriteBatchSize {
		end := start + coarseRollupWriteBatchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := s.upsertNormalizedRollupRowsTx(ctx, rows[start:end], tx); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

// FlushCoarse seals due in-memory parent buckets. It is primarily called by
// the scheduled compactor so a series that stops reporting still closes.
func (s *Store) FlushCoarse(ctx context.Context, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	return s.flushClosedCoarseRollups(ctx, now)
}

func (s *Store) metricRollupPolicyTx(ctx context.Context, metricName string, tx *sql.Tx) (RollupPolicy, error) {
	var retentionDays int
	err := tx.QueryRowContext(ctx, "SELECT retention_days FROM "+s.tables.definitions+" WHERE name = "+s.dialect.placeholder(1), metricName).Scan(&retentionDays)
	if err != nil {
		return RollupPolicy{}, err
	}
	if retentionDays <= 0 {
		return RollupPolicy{}, nil
	}
	return s.cfg.RollupPolicy.withMetricRetention(time.Duration(retentionDays) * 24 * time.Hour), nil
}

func (s *Store) metricRollupPolicy(ctx context.Context, metricName string) (RollupPolicy, error) {
	var retentionDays int
	err := s.reader().QueryRowContext(ctx, "SELECT retention_days FROM "+s.tables.definitions+" WHERE name = "+s.dialect.placeholder(1), metricName).Scan(&retentionDays)
	if err == sql.ErrNoRows {
		// Preserve AggregateRollup's historical empty-result behavior for an
		// unknown metric rather than turning a read into an ErrNotFound.
		return s.cfg.RollupPolicy, nil
	}
	if err != nil {
		return RollupPolicy{}, err
	}
	if retentionDays <= 0 {
		return RollupPolicy{}, nil
	}
	return s.cfg.RollupPolicy.withMetricRetention(time.Duration(retentionDays) * 24 * time.Hour), nil
}

func (s *Store) deleteCoarseRollups(metricName string) {
	_, _ = s.deleteCoarseRollupsMatching(metricName, "", nil)
}

func (s *Store) deleteCoarseRollupsMatching(metricName, entityID string, tags map[string]string) (int64, error) {
	s.coarseMu.Lock()
	defer s.coarseMu.Unlock()
	var deleted int64
	for key := range s.coarse {
		if metricName != "" && key.metricName != metricName {
			continue
		}
		if entityID != "" && key.entityID != entityID {
			continue
		}
		parent := s.coarse[key]
		if len(tags) == 0 {
			deleted += int64(len(parent.children))
			delete(s.coarse, key)
			continue
		}
		for childKey, child := range parent.children {
			_, matched, err := matchRawTags(child.tagsJSON, tags)
			if err != nil {
				return deleted, err
			}
			if matched {
				delete(parent.children, childKey)
				deleted++
			}
		}
		if len(parent.children) == 0 {
			delete(s.coarse, key)
		}
	}
	return deleted, nil
}
