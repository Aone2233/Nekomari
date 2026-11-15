package metric

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SnapshotSQLite creates a new SQLite snapshot and overlays the current hot
// minute summaries on the copy. The live store is not flushed or modified.
// Exact raw samples are memory-only and are represented by minute summaries.
// The caller must fence any application-level queues before invoking this.
// The destination's containing directory must be private and non-replaceable
// by other processes: identity checks detect some, but not all, path races.
// This does not coordinate other processes or a separate application database.
func (s *Store) SnapshotSQLite(ctx context.Context, destination string) (err error) {
	if ctx == nil || destination == "" {
		return fmt.Errorf("%w: snapshot requires a context and destination", ErrInvalidArgument)
	}
	if s == nil || s.Driver() != DriverSQLite {
		return fmt.Errorf("%w: snapshot requires a SQLite store", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	// Reserve the destination without following or replacing an existing path.
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create snapshot destination: %w", err)
	}
	reserved, statErr := file.Stat()
	if statErr != nil || !reserved.Mode().IsRegular() {
		_ = file.Close()
		return errors.New("cannot identify snapshot destination")
	}
	verifyDestination := func() error {
		current, err := os.Lstat(destination)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(reserved, current) {
			return errors.New("snapshot destination changed")
		}
		return nil
	}
	complete := false
	defer func() {
		if !complete {
			// Never remove a replacement path that does not belong to this call.
			current, statErr := os.Lstat(destination)
			if statErr == nil && os.SameFile(reserved, current) {
				if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = errors.Join(err, fmt.Errorf("remove incomplete snapshot: %w", removeErr))
				}
			} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("inspect incomplete snapshot: %w", statErr))
			}
		}
	}()
	if err := file.Close(); err != nil {
		return fmt.Errorf("close snapshot destination: %w", err)
	}

	// All regular Store writes, compaction and retention mutations take this
	// lock before touching the hot map or SQLite. Keep them out until VACUUM
	// has captured the database and the matching hot buckets are copied.
	s.retentionMu.Lock()
	if err := s.ensureOpen(); err != nil {
		s.retentionMu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		s.retentionMu.Unlock()
		return err
	}
	s.rollupViewMu.RLock()
	commitErr := s.hotCommitErr
	recoveryPending := s.coarseRecoveryPending
	s.rollupViewMu.RUnlock()
	if commitErr != nil {
		s.retentionMu.Unlock()
		return commitErr
	}
	if recoveryPending {
		s.retentionMu.Unlock()
		return ErrRetentionPending
	}
	if err := verifyDestination(); err != nil {
		s.retentionMu.Unlock()
		return err
	}
	safePath := strings.ReplaceAll(filepath.ToSlash(destination), "'", "''")
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", safePath))
	if err != nil {
		s.retentionMu.Unlock()
		return fmt.Errorf("snapshot SQLite metric store: %w", err)
	}
	if err := verifyDestination(); err != nil {
		s.retentionMu.Unlock()
		return err
	}
	s.rollupViewMu.RLock()
	s.hotMu.RLock()
	hot := make([]hotRollup, 0, len(s.hot))
	for key, bucket := range s.hot {
		_, replace := s.hotReplace[key]
		hot = append(hot, hotRollup{key: key, bucket: bucket.clone(true), replace: replace})
	}
	s.hotMu.RUnlock()
	s.coarseMu.RLock()
	coarse := cloneSnapshotCoarse(s.coarse)
	s.coarseMu.RUnlock()
	snapshotTime := time.Now().UTC()
	s.rollupViewMu.RUnlock()
	s.retentionMu.Unlock()

	if err := verifyDestination(); err != nil {
		return err
	}
	if err := s.overlaySnapshotMemory(ctx, destination, hot, coarse, snapshotTime); err != nil {
		return fmt.Errorf("include in-memory metric summaries: %w", err)
	}
	if err := verifyDestination(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	complete = true
	return nil
}

func cloneSnapshotCoarse(source map[coarseRollupKey]*coarseRollup) map[coarseRollupKey]*coarseRollup {
	cloned := make(map[coarseRollupKey]*coarseRollup, len(source))
	for key, parent := range source {
		copyParent := &coarseRollup{higher: append([]time.Duration(nil), parent.higher...), children: make(map[rollupKey]*rollupBucket, len(parent.children))}
		for child, bucket := range parent.children {
			copyParent.children[child] = bucket.clone(true)
		}
		cloned[key] = copyParent
	}
	return cloned
}

// overlaySnapshotMemory uses the same minute merge/replace rules as a normal
// flush, but only on the detached snapshot. Open parents are reconstructed
// from durable children after restore; they are not prematurely sealed.
func (s *Store) overlaySnapshotMemory(ctx context.Context, destination string, hot []hotRollup, coarse map[coarseRollupKey]*coarseRollup, snapshotTime time.Time) (err error) {
	cfg := s.cfg
	cfg.DB = nil
	cfg.DSN = destination
	cfg.AutoMigrate = false
	cfg.MaxOpenConns = 1
	cfg.MaxIdleConns = 1
	cfg.SQLite.ReadPoolSize = 0
	// Keep the private copy self-contained; failure removes this file and does
	// not leave committed changes in an ignored WAL sidecar.
	cfg.SQLite.PerformanceProfile = SQLiteProfileDurable
	copyStore, err := Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := copyStore.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	var journal string
	if err := copyStore.db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&journal); err != nil {
		return err
	}
	if !strings.EqualFold(journal, "delete") {
		return errors.New("could not make snapshot self-contained")
	}
	type metricBuckets struct {
		merge   map[rollupKey]*rollupBucket
		replace map[rollupKey]*rollupBucket
	}
	byMetric := make(map[string]*metricBuckets)
	for _, item := range hot {
		buckets := byMetric[item.key.metricName]
		if buckets == nil {
			buckets = &metricBuckets{merge: make(map[rollupKey]*rollupBucket), replace: make(map[rollupKey]*rollupBucket)}
			byMetric[item.key.metricName] = buckets
		}
		key := rollupKey{entityID: item.key.entityID, tagsHash: item.key.tagsHash, labelsHash: item.key.labelsHash, bucket: item.key.bucket}
		if item.replace {
			buckets.replace[key] = item.bucket
		} else {
			buckets.merge[key] = item.bucket
		}
	}
	names := make([]string, 0, len(byMetric))
	for name := range byMetric {
		names = append(names, name)
	}
	sort.Strings(names)
	tx, err := copyStore.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	policies := make(map[string]RollupPolicy, len(names))
	for _, name := range names {
		policy, err := copyStore.metricRollupPolicyTx(ctx, name, tx)
		if err != nil {
			return err
		}
		if _, err := copyStore.writeTierCascadeTx(ctx, name, policy, byMetric[name].merge, tx); err != nil {
			return err
		}
		if err := copyStore.replaceMinuteRollupsTx(ctx, name, policy, byMetric[name].replace, tx); err != nil {
			return err
		}
		policies[name] = policy
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	copyStore.coarse = coarse
	for _, name := range names {
		minute := make(map[rollupKey]*rollupBucket, len(byMetric[name].merge)+len(byMetric[name].replace))
		for key, bucket := range byMetric[name].merge {
			minute[key] = bucket
		}
		for key, bucket := range byMetric[name].replace {
			minute[key] = bucket
		}
		copyStore.addMinuteParents(name, policies[name], minute, snapshotTime)
	}
	// A staged parent must never overwrite an independently durable parent.
	// This can happen when an earlier restore sealed an incomplete window.
	if err := copyStore.verifySnapshotParentsAbsent(ctx); err != nil {
		return err
	}
	// Persist only parents whose grace window has actually closed. An open
	// parent must remain absent from SQLite so a restored Store can rebuild its
	// complete child set and keep accumulating that same window. Closing an open
	// parent in the copy would make recovery treat the partial row as sealed;
	// the next child could then overwrite the earlier observations.
	//
	// Before discarding the detached in-memory tree, prove every remaining
	// child is present in SQLite. Retention may already have removed a child;
	// in that case there is no safe restart representation, so fail the whole
	// snapshot rather than emit an apparently complete archive.
	copyStore.coarseRecoveryPending = false
	if _, err := copyStore.FlushCoarse(ctx, snapshotTime); err != nil {
		return err
	}
	return copyStore.verifySnapshotPendingChildren(ctx)
}

func (s *Store) verifySnapshotParentsAbsent(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	checked := make(map[coarseRollupKey]struct{})
	for key, parent := range s.coarse {
		if err := s.rejectPersistedCoarseAncestorsTx(ctx, tx, key, parent.higher, 0, checked, false); err != nil {
			return err
		}
	}
	return tx.Rollback()
}

func (s *Store) verifySnapshotPendingChildren(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	policies := make(map[string]RollupPolicy)
	for parentKey, parent := range s.coarse {
		policy, ok := policies[parentKey.metricName]
		if !ok {
			policy, err = s.metricRollupPolicyTx(ctx, parentKey.metricName, tx)
			if err != nil {
				return err
			}
			policies[parentKey.metricName] = policy
		}
		var childInterval time.Duration
		for i := 1; i < len(policy.Tiers); i++ {
			if policy.Tiers[i].Interval == parentKey.interval {
				childInterval = policy.Tiers[i-1].Interval
				break
			}
		}
		if childInterval == 0 {
			return ErrRetentionPending
		}
		if len(parent.children) == 0 {
			return ErrRetentionPending
		}
		for childKey, child := range parent.children {
			if child == nil || child.count <= 0 {
				return ErrRetentionPending
			}
			stored, readErr := s.readRollupBucketTx(ctx, parentKey.metricName, childKey, childInterval, tx)
			if readErr != nil || stored == nil || !equalRebuiltRollup(stored, child) || stored.tagsJSON != child.tagsJSON || stored.labelsJSON != child.labelsJSON {
				if readErr != nil {
					return readErr
				}
				return ErrRetentionPending
			}
		}
	}
	return tx.Rollback()
}
