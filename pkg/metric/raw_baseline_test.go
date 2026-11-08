package metric

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func baselineFixture(t *testing.T) (*Store, string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.db")
	s, err := Open(t.Context(), SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"history", "other"} {
		if err := s.CreateMetric(t.Context(), Definition{Name: name, RetentionDays: 30}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path, time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
}

func reopenBaseline(t *testing.T, s *Store, path string) *Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened
}

func historyPoint(at time.Time, value float64, label string) Point {
	return Point{MetricName: "history", EntityID: "node", Timestamp: at, Value: value,
		Tags: map[string]string{"task": "7"}, Labels: map[string]string{"source": label}}
}

func TestRawBaselineCompressedReplacementPreservesLabelsAndDigest(t *testing.T) {
	s, path, base := baselineFixture(t)
	ctx := t.Context()
	if err := s.WriteBatch(ctx, []Point{historyPoint(base.Add(5*time.Second), 10, "a"), historyPoint(base.Add(15*time.Second), 20, "b")}); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	point := historyPoint(base.Add(40*time.Second), 30, "a")
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	point.Value, point.Labels["source"] = 100, "b"
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "current.db")
	if err := s.SnapshotSQLite(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	assertSnapshotAggregate(t, snapshot, "history", 5*time.Minute, base, 3, 130)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	check, err := Open(ctx, SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	rows, err := check.scanRollupRows(ctx, check.reader(), "history", time.Minute)
	if err != nil || len(rows) != 2 {
		t.Fatalf("minute rows=%v err=%v", rows, err)
	}
	for _, row := range rows {
		labels, err := decodeMapString(row.bucketData.labelsJSON)
		if err != nil {
			t.Fatal(err)
		}
		switch labels["source"] {
		case "a":
			if row.bucketData.count != 1 || row.bucketData.sum != 10 {
				t.Fatalf("old label=%+v", row.bucketData)
			}
		case "b":
			if row.bucketData.count != 2 || row.bucketData.sum != 120 || row.bucketData.digest.Count() != 2 || row.bucketData.digest.Quantile(.95) < 80 {
				t.Fatalf("new label/digest=%+v", row.bucketData)
			}
		default:
			t.Fatalf("unexpected labels: %v", labels)
		}
	}
}

func TestRawBaselineRejectsUnknownOldIdentityWithoutPartialBatch(t *testing.T) {
	s, path, base := baselineFixture(t)
	ctx := t.Context()
	if err := s.WriteBatch(ctx, []Point{historyPoint(base.Add(5*time.Second), 10, "a"), historyPoint(base.Add(15*time.Second), 20, "b")}); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	for _, at := range []time.Time{base.Add(5 * time.Second), base.Add(10 * time.Second), base.Add(15 * time.Second)} {
		batch := []Point{
			{MetricName: "other", EntityID: "node", Timestamp: at, Value: 99},
			historyPoint(at, 30, "changed"),
		}
		if err := s.WriteBatch(ctx, batch); !errors.Is(err, ErrRawHistoryUnavailable) {
			t.Fatalf("ambiguous timestamp %s = %v", at, err)
		}
	}
	if len(s.raw) != 0 || len(s.hot) != 0 || len(s.baseline) != 0 {
		t.Fatalf("failed batches changed state: raw=%d hot=%d baseline=%d", len(s.raw), len(s.hot), len(s.baseline))
	}
	if err := s.Write(ctx, historyPoint(base.Add(25*time.Second), 7, "a")); err != nil {
		t.Fatalf("strictly newer point: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 3, 37)
	assertSnapshotSummary(t, path, "other", time.Minute, 0, 0)
}

func TestRawBaselineBatchDuplicateAndIndependentTag(t *testing.T) {
	s, path, base := baselineFixture(t)
	ctx := t.Context()
	if err := s.Write(ctx, historyPoint(base.Add(5*time.Second), 10, "a")); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	p := historyPoint(base.Add(35*time.Second), 20, "a")
	q := historyPoint(p.Timestamp, 30, "b")
	different := historyPoint(base.Add(5*time.Second), 4, "a")
	different.Tags["task"] = "8"
	if err := s.WriteBatch(ctx, []Point{p, q, different}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 3, 44)
}

func TestRawBaselineOldBatchDuplicateIsLastWrite(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(-20 * time.Minute)
	at := base.Add(10 * time.Second)
	if err := s.WriteBatch(ctx, []Point{historyPoint(at, 10, "a"), historyPoint(at, 20, "b")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, historyPoint(at, 30, "b")); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("ambiguous old replay=%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 1, 20)
}

func TestRawBaselineLiveReadsAndSnapshotUseReplacementOnce(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	if err := s.Write(ctx, historyPoint(base.Add(5*time.Second), 10, "a")); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	if err := s.Write(ctx, historyPoint(base.Add(25*time.Second), 30, "a")); err != nil {
		t.Fatal(err)
	}
	query := Query{MetricName: "history", EntityID: "node", Start: base, End: base.Add(time.Minute)}
	stats, err := s.Stats(ctx, query)
	if err != nil || stats.Count != 2 || stats.Sum != 40 || math.Abs(stats.Avg-20) > .001 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	points, err := s.Series(ctx, AggregateQuery{Query: query, Aggregation: AggSum, Interval: time.Minute}, base)
	if err != nil || len(points) != 1 || points[0].Count != 2 || points[0].Value != 40 {
		t.Fatalf("series=%+v err=%v", points, err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, snapshot, "history", time.Minute, 2, 40)
	assertSnapshotAggregate(t, snapshot, "history", 5*time.Minute, base, 2, 40)
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	points, err = s.Series(ctx, AggregateQuery{Query: query, Aggregation: AggSum, Interval: time.Minute}, base)
	if err != nil || len(points) != 1 || points[0].Count != 2 || points[0].Value != 40 {
		t.Fatalf("persisted series=%+v err=%v", points, err)
	}
}

func TestRawBaselineSnapshotIncludesRecoveredOlderCoarseChildren(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(5 * time.Minute)
	if err := s.WriteBatch(ctx, []Point{historyPoint(base.Add(10*time.Second), 10, "a"), historyPoint(base.Add(time.Minute+10*time.Second), 20, "a")}); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	assertSnapshotAggregate(t, snapshot, "history", 5*time.Minute, base, 2, 30)
}

func TestRawBaselineTryAppendRequiresPreparedMinute(t *testing.T) {
	s, at := tryAppendFixture(t)
	ctx := t.Context()
	points := tryAppendPoints(at)
	if err := s.tryAppendCurrentBatch(ctx, points, func() time.Time { return at }); !errors.Is(err, ErrWriteUnavailable) {
		t.Fatalf("unprepared append=%v", err)
	}
	if err := s.PrepareCurrentBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	if err := s.tryAppendCurrentBatch(ctx, points, func() time.Time { return at }); err != nil {
		t.Fatal(err)
	}
}

func TestRawBaselineImportInvalidatesIdentityAndRefusesPending(t *testing.T) {
	s, _, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	at := base.Add(10 * time.Second)
	p := historyPoint(at, 10, "a")
	if err := s.Write(ctx, p); err != nil {
		t.Fatal(err)
	}
	imported := historyPoint(base.Add(30*time.Second), 50, "a")
	if err := s.ReplaceRollupPoints(ctx, time.Minute, []Point{imported}); !errors.Is(err, ErrImportPending) {
		t.Fatalf("pending import=%v", err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRollupPoints(ctx, time.Minute, []Point{imported}); !errors.Is(err, ErrImportPending) {
		t.Fatalf("pending coarse import=%v", err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRollupPoints(ctx, time.Minute, []Point{imported}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, historyPoint(at, 11, "b")); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("old cached sample survived import: %v", err)
	}
	if err := s.Write(ctx, historyPoint(base.Add(40*time.Second), 2, "a")); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.scanRollupRows(ctx, s.reader(), "history", time.Minute)
	if err != nil || len(rows) != 1 || rows[0].bucketData.count != 2 || rows[0].bucketData.sum != 52 {
		t.Fatalf("post import rows=%v err=%v", rows, err)
	}
}

func TestRawBaselineImportRejectsPendingCoarseChild(t *testing.T) {
	s, _, base := baselineFixture(t)
	ctx := t.Context()
	p := historyPoint(base.Add(10*time.Second), 10, "a")
	if err := s.Write(ctx, p); err != nil {
		t.Fatal(err)
	}
	imported := historyPoint(base.Add(30*time.Second), 50, "a")
	if err := s.ReplaceRollupPoints(ctx, time.Minute, []Point{imported}); !errors.Is(err, ErrImportPending) {
		t.Fatalf("pending parent import=%v", err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRollupPoints(ctx, time.Minute, []Point{imported}); err != nil {
		t.Fatalf("import after parent sealed: %v", err)
	}
}

func TestRawBaselinePartialDeleteRejectsReplacement(t *testing.T) {
	s, _, base := baselineFixture(t)
	ctx := t.Context()
	if err := s.WriteBatch(ctx, []Point{historyPoint(base.Add(5*time.Second), 10, "a"), historyPoint(base.Add(30*time.Second), 20, "a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteBefore(ctx, "history", base.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, historyPoint(base.Add(30*time.Second), 30, "a")); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("partial deletion replacement=%v", err)
	}
}

func TestRawBaselineDeletionDoesNotResurrectHistory(t *testing.T) {
	s, path, base := baselineFixture(t)
	ctx := t.Context()
	at := base.Add(10 * time.Second)
	if err := s.Write(ctx, historyPoint(at, 10, "a")); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	if err := s.Write(ctx, historyPoint(base.Add(20*time.Second), 20, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteSeries(ctx, Query{MetricName: "history", EntityID: "node", Tags: map[string]string{"task": "7"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, historyPoint(at, 3, "a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 1, 3)
}

func TestRawBaselineFilteredDeletePreservesOtherSeries(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted_baseline_%t", persisted), func(t *testing.T) {
			s, path, _ := baselineFixture(t)
			ctx := t.Context()
			base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
			other := func(at time.Time, value float64) Point {
				point := historyPoint(at, value, "a")
				point.Tags["task"] = "8"
				return point
			}
			if persisted {
				if err := s.Write(ctx, other(base.Add(5*time.Second), 10)); err != nil {
					t.Fatal(err)
				}
				s = reopenBaseline(t, s, path)
			}
			at := base.Add(20 * time.Second)
			if err := s.Write(ctx, other(at, 20)); err != nil {
				t.Fatal(err)
			}
			if err := s.PersistHotRollups(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteSeries(ctx, Query{MetricName: "history", EntityID: "node", Tags: map[string]string{"task": "7"}}); err != nil {
				t.Fatal(err)
			}
			if err := s.Write(ctx, other(at, 30)); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if persisted {
				assertSnapshotSummary(t, path, "history", time.Minute, 2, 40)
			} else {
				assertSnapshotSummary(t, path, "history", time.Minute, 1, 30)
			}
		})
	}
}

func TestRawBaselineFilteredDeletePreservesOtherPartialMinute(t *testing.T) {
	s, _, base := baselineFixture(t)
	ctx := t.Context()
	first := historyPoint(base.Add(5*time.Second), 10, "a")
	first.Tags["task"] = "8"
	second := historyPoint(base.Add(30*time.Second), 20, "a")
	second.Tags["task"] = "8"
	if err := s.WriteBatch(ctx, []Point{first, second}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteBefore(ctx, "history", base.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteSeries(ctx, Query{MetricName: "history", EntityID: "node", Tags: map[string]string{"task": "7"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, second); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("unrelated delete forgot the partial-minute marker: %v", err)
	}
}

func TestRawBaselineFilteredDeleteMatchesEmptyTagOnlyWhenPresent(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	at := base.Add(10 * time.Second)
	untagged := historyPoint(at, 10, "a")
	untagged.Tags = nil
	tagged := historyPoint(at, 20, "a")
	tagged.Tags = map[string]string{"task": ""}
	if err := s.WriteBatch(ctx, []Point{untagged, tagged}); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteSeries(ctx, Query{MetricName: "history", EntityID: "node", Tags: map[string]string{"task": ""}}); err != nil {
		t.Fatal(err)
	}
	untagged.Value = 30
	if err := s.Write(ctx, untagged); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 1, 30)
}

func TestRawBaselineFilteredDeleteKeepsUntaggedHotPoint(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	untagged := historyPoint(base.Add(10*time.Second), 10, "a")
	untagged.Tags = nil
	tagged := historyPoint(base.Add(10*time.Second), 20, "a")
	tagged.Tags = map[string]string{"task": ""}
	if err := s.WriteBatch(ctx, []Point{untagged, tagged}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteSeries(ctx, Query{MetricName: "history", EntityID: "node", Tags: map[string]string{"task": ""}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 1, 10)
}

func TestRawBaselineWriteUsesOneClockAtCutoff(t *testing.T) {
	s, path, base := baselineFixture(t)
	ctx := t.Context()
	if err := s.Write(ctx, historyPoint(base.Add(5*time.Second), 10, "a")); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	now := base.Add(10*time.Minute + 59*time.Second)
	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return now
		}
		return now.Add(2 * time.Second)
	}
	if err := s.writeBatch(ctx, []Point{historyPoint(base.Add(20*time.Second), 30, "a")}, clock); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("write read clock %d times", calls)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 2, 40)
}

func TestRawBaselineOldAcceptedFlushFailureDoesNotDuplicate(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(-20 * time.Minute)
	if _, err := s.ExecContext(ctx, `CREATE TRIGGER reject_old_minute BEFORE INSERT ON metric_rollups BEGIN SELECT RAISE(ABORT, 'blocked minute'); END`); err != nil {
		t.Fatal(err)
	}
	old := historyPoint(base.Add(10*time.Second), 10, "a")
	err := s.Write(ctx, old)
	var accepted *WriteAcceptedError
	if !errors.As(err, &accepted) || !strings.Contains(err.Error(), "blocked minute") {
		t.Fatalf("flush failure did not report admitted batch: %v", err)
	}
	blocked := historyPoint(base.Add(20*time.Second), 20, "a")
	err = s.Write(ctx, blocked)
	if err == nil || errors.As(err, &accepted) {
		t.Fatalf("next old batch was admitted while previous flush failed: %v", err)
	}
	if _, err := s.ExecContext(ctx, `DROP TRIGGER reject_old_minute`); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, old); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("old replay was not rejected after pending flush: %v", err)
	}
	if err := s.Write(ctx, blocked); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 2, 30)
}

func TestRawBaselineCoarseFailureReportsAcceptedMinute(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(5 * time.Minute)
	if err := s.Write(ctx, historyPoint(base.Add(10*time.Second), 10, "a")); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(ctx, `CREATE TRIGGER reject_coarse BEFORE INSERT ON metric_rollups WHEN NEW.resolution_id != (SELECT id FROM metric_resolutions WHERE resolution_milli = 60000) BEGIN SELECT RAISE(ABORT, 'blocked coarse'); END`); err != nil {
		t.Fatal(err)
	}
	now := base.Add(20 * time.Minute)
	point := historyPoint(base.Add(time.Minute+10*time.Second), 20, "a")
	err := s.writeBatch(ctx, []Point{point}, func() time.Time { return now })
	var accepted *WriteAcceptedError
	if !errors.As(err, &accepted) || !strings.Contains(err.Error(), "blocked coarse") {
		t.Fatalf("coarse failure did not report admitted batch: %v", err)
	}
	if _, err := s.ExecContext(ctx, `DROP TRIGGER reject_coarse`); err != nil {
		t.Fatal(err)
	}
	if err := s.writeBatch(ctx, []Point{point}, func() time.Time { return now }); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("already persisted minute was replayed: %v", err)
	}
	if _, err := s.FlushCoarse(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 2, 30)
}

func TestRawBaselineRefusesCoarseOnlyHistory(t *testing.T) {
	s, _, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(5 * time.Minute)
	point := historyPoint(base.Add(10*time.Second), 10, "a")
	rollup := PersistedRollup{MetricName: point.MetricName, EntityID: point.EntityID, Tags: point.Tags, Labels: point.Labels,
		Resolution: 5 * time.Minute, Bucket: base, Count: 1, Sum: 10, SumSq: 100, Min: 10, Max: 10,
		FirstValue: 10, FirstTime: point.Timestamp, LastValue: 10, LastTime: point.Timestamp, CreatedAt: time.Now().UTC()}
	if err := s.ImportRollups(ctx, []PersistedRollup{rollup}); err != nil {
		t.Fatal(err)
	}
	point.Timestamp = base.Add(20 * time.Second)
	if err := s.Write(ctx, point); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("coarse-only history admitted ambiguous point: %v", err)
	}
	rows, err := s.scanRollupRows(ctx, s.reader(), "history", time.Minute)
	if err != nil || len(rows) != 0 {
		t.Fatalf("refused point created minute history: %v, %v", rows, err)
	}
}

func TestRawBaselineAllowsNewMinuteOutsideCoarseObservationSpan(t *testing.T) {
	s, _, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(5 * time.Minute).Add(-time.Hour)
	point := historyPoint(base.Add(10*time.Second), 10, "a")
	rollup := PersistedRollup{MetricName: point.MetricName, EntityID: point.EntityID, Tags: point.Tags, Labels: point.Labels,
		Resolution: 24 * time.Hour, Bucket: base.Truncate(24 * time.Hour), Count: 1, Sum: 10, SumSq: 100, Min: 10, Max: 10,
		FirstValue: 10, FirstTime: point.Timestamp, LastValue: 10, LastTime: point.Timestamp, CreatedAt: time.Now().UTC()}
	if err := s.ImportRollups(ctx, []PersistedRollup{rollup}); err != nil {
		t.Fatal(err)
	}
	point.Timestamp = base.Add(time.Minute + 10*time.Second)
	if err := s.Write(ctx, point); err != nil {
		t.Fatalf("new minute outside ancestor observation span was refused: %v", err)
	}
}

func TestRawBaselineCoarseImportRefusesPendingMinute(t *testing.T) {
	s, _, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(5 * time.Minute).Add(5 * time.Minute)
	point := historyPoint(base.Add(time.Minute+10*time.Second), 10, "a")
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	rollup := PersistedRollup{MetricName: point.MetricName, EntityID: point.EntityID, Tags: point.Tags, Labels: point.Labels,
		Resolution: 5 * time.Minute, Bucket: base, Count: 1, Sum: 20, SumSq: 400, Min: 20, Max: 20,
		FirstValue: 20, FirstTime: point.Timestamp, LastValue: 20, LastTime: point.Timestamp, CreatedAt: time.Now().UTC()}
	if err := s.ImportRollups(ctx, []PersistedRollup{rollup}); !errors.Is(err, ErrImportPending) {
		t.Fatalf("coarse import overlapped hot minute: %v", err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportRollups(ctx, []PersistedRollup{rollup}); !errors.Is(err, ErrImportPending) {
		t.Fatalf("coarse import overlapped pending parent: %v", err)
	}
	if err := s.RebuildCoarserRollups(ctx, time.Minute); !errors.Is(err, ErrImportPending) {
		t.Fatalf("coarse rebuild overlapped pending parent: %v", err)
	}
}

func TestRawBaselineImportPreservesPartialDeletionMarker(t *testing.T) {
	s, _, base := baselineFixture(t)
	ctx := t.Context()
	first := historyPoint(base.Add(5*time.Second), 10, "a")
	second := historyPoint(base.Add(30*time.Second), 20, "a")
	if err := s.WriteBatch(ctx, []Point{first, second}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteBefore(ctx, "history", base.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRollupPoints(ctx, time.Minute, []Point{historyPoint(base.Add(40*time.Second), 50, "a")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, second); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("import forgot partial deletion marker: %v", err)
	}
}

func TestRawBaselineDeleteBeforeDoesNotResurrectPendingParent(t *testing.T) {
	s, path, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(5 * time.Minute).Add(-5 * time.Minute)
	if err := s.Write(ctx, historyPoint(base.Add(10*time.Second), 10, "a")); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.coarse) == 0 {
		t.Fatal("fixture did not retain a pending parent")
	}
	if _, err := s.DeleteBefore(ctx, "history", base.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 0, 0)
	assertSnapshotSummary(t, path, "history", 5*time.Minute, 0, 0)
}

func TestRawBaselineRetentionInvalidatesCachedEmptyMinute(t *testing.T) {
	s, _, _ := baselineFixture(t)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(5 * time.Minute)
	point := historyPoint(base.Add(10*time.Second), 10, "a")
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	// Materialize a coarse parent, then move retention beyond its minute but
	// not beyond the coarser tier. The writer's negative baseline is stale.
	// Flush against a synthetic clock while the live clock is still inside the
	// parent grace period, then advance through each ancestor's close time.
	if _, err := s.Flush(ctx, base.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupExpired(ctx, base.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.scanRollupRows(ctx, s.reader(), "history", 24*time.Hour)
	if err != nil || len(rows) != 1 {
		t.Fatalf("fixture did not retain daily ancestor: rows=%v err=%v coarse=%d", rows, err, len(s.coarse))
	}
	point.Timestamp = base.Add(20 * time.Second)
	if err := s.Write(ctx, point); !errors.Is(err, ErrRawHistoryUnavailable) {
		t.Fatalf("stale empty baseline revived expired minute: %v", err)
	}
}

func TestRetentionCleanupRemovesExpiredPendingBuckets(t *testing.T) {
	for _, compactMetric := range []bool{false, true} {
		t.Run(fmt.Sprintf("compactMetric=%t", compactMetric), func(t *testing.T) {
			ctx := t.Context()
			policy := RollupPolicy{Tiers: []RollupTier{
				{Interval: time.Minute, Retention: time.Hour},
				{Interval: 5 * time.Minute, Retention: 2 * time.Hour},
				{Interval: time.Hour, Retention: 72 * time.Hour},
			}}
			s := newRollupStore(t, policy)
			if err := s.CreateMetric(ctx, Definition{Name: "retention", RetentionDays: 2}); err != nil {
				t.Fatal(err)
			}
			base := time.Now().UTC().Truncate(5 * time.Minute)
			first := Point{MetricName: "retention", EntityID: "node", Timestamp: base.Add(time.Second), Value: 10}
			if err := s.writeBatch(ctx, []Point{first}, func() time.Time { return base.Add(3 * time.Second) }); err != nil {
				t.Fatal(err)
			}
			if err := s.PersistHotRollups(ctx); err != nil {
				t.Fatal(err)
			}
			first.Timestamp = base.Add(2 * time.Second)
			if err := s.writeBatch(ctx, []Point{first}, func() time.Time { return base.Add(3 * time.Second) }); err != nil {
				t.Fatal(err)
			}
			if len(s.hot) == 0 || len(s.coarse) == 0 {
				t.Fatal("fixture must have hot and coarse pending data")
			}
			future := base.Add(72 * time.Hour)
			var err error
			if compactMetric {
				_, err = s.CompactMetric(ctx, "retention", future)
			} else {
				_, err = s.CleanupExpired(ctx, future)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(s.hot) != 0 || len(s.coarse) != 0 {
				t.Fatalf("expired buckets remain in memory: hot=%d coarse=%d", len(s.hot), len(s.coarse))
			}
			if _, err := s.Flush(ctx, future); err != nil {
				t.Fatal(err)
			}
			if _, err := s.FlushCoarse(ctx, future); err != nil {
				t.Fatal(err)
			}
			rows, _ := exportAllTestRollups(t, s, "retention", 10)
			if len(rows) != 0 {
				t.Fatalf("expired data resurrected: %+v", rows)
			}
		})
	}
}

func TestCompactDisabledMetricClearsPendingDataBeforeReenable(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "disabled-pending", RetentionDays: 7}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(5 * time.Minute)
	point := Point{MetricName: "disabled-pending", EntityID: "node", Timestamp: base.Add(time.Second), Value: 10}
	if err := s.writeBatch(ctx, []Point{point}, func() time.Time { return base.Add(3 * time.Second) }); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	point.Timestamp = base.Add(2 * time.Second)
	if err := s.writeBatch(ctx, []Point{point}, func() time.Time { return base.Add(3 * time.Second) }); err != nil {
		t.Fatal(err)
	}
	if len(s.hot) == 0 || len(s.coarse) == 0 {
		t.Fatal("fixture requires both a hot minute and a pending parent")
	}
	if _, err := s.UpdateMetricRetention(ctx, point.MetricName, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompactMetric(ctx, point.MetricName, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(s.hot) != 0 || len(s.coarse) != 0 || len(s.raw) != 0 || len(s.baseline) != 0 {
		t.Fatalf("disabled cleanup retained memory: hot=%d coarse=%d raw=%d baseline=%d", len(s.hot), len(s.coarse), len(s.raw), len(s.baseline))
	}
	if _, err := s.UpdateMetricRetention(ctx, point.MetricName, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(ctx, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushCoarse(ctx, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, _ := exportAllTestRollups(t, s, point.MetricName, 10)
	if len(rows) != 0 {
		t.Fatalf("disabled history returned after re-enable: %+v", rows)
	}
}

func TestRetentionDoesNotDropPendingDataNeededByCoarserTiers(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for _, coarse := range []bool{false, true} {
			t.Run(fmt.Sprintf("compact=%t/coarse=%t", compact, coarse), func(t *testing.T) {
				ctx := t.Context()
				policy := RollupPolicy{Tiers: []RollupTier{
					{Interval: time.Minute, Retention: time.Hour},
					{Interval: 5 * time.Minute, Retention: 2 * time.Hour},
					{Interval: time.Hour, Retention: 72 * time.Hour},
				}}
				s := newRollupStore(t, policy)
				if err := s.CreateMetric(ctx, Definition{Name: "retention", RetentionDays: 2}); err != nil {
					t.Fatal(err)
				}
				base := time.Now().UTC().Truncate(5 * time.Minute)
				point := Point{MetricName: "retention", EntityID: "node", Timestamp: base.Add(time.Second), Value: 10}
				if err := s.writeBatch(ctx, []Point{point}, func() time.Time { return base.Add(2 * time.Second) }); err != nil {
					t.Fatal(err)
				}
				if coarse {
					if err := s.PersistHotRollups(ctx); err != nil {
						t.Fatal(err)
					}
					if len(s.coarse) == 0 {
						t.Fatal("fixture did not stage a coarse parent")
					}
				}
				before, _ := exportAllTestRollups(t, s, "retention", 10)
				var err error
				if compact {
					_, err = s.CompactMetric(ctx, "retention", base.Add(3*time.Hour))
				} else {
					_, err = s.CleanupExpired(ctx, base.Add(3*time.Hour))
				}
				if !errors.Is(err, ErrRetentionPending) {
					t.Fatalf("retention dropped source needed by a retained tier: %v", err)
				}
				after, _ := exportAllTestRollups(t, s, "retention", 10)
				if !reflect.DeepEqual(before, after) || (coarse && len(s.coarse) == 0) || (!coarse && len(s.hot) == 0) {
					t.Fatalf("refused retention changed data: before=%+v after=%+v hot=%d coarse=%d", before, after, len(s.hot), len(s.coarse))
				}
			})
		}
	}
}

func TestRetentionDoesNotDeleteDurableChildrenBeforeRecovery(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: time.Hour},
		{Interval: 5 * time.Minute, Retention: 2 * time.Hour},
		{Interval: time.Hour, Retention: 72 * time.Hour},
	}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "retention", RetentionDays: 2}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(5 * time.Minute)
	row := constantTransferRollup("retention", "node", base, time.Minute, 10, base)
	if err := s.ImportRollups(ctx, []PersistedRollup{row}); err != nil {
		t.Fatal(err)
	}
	s.coarseRecoveryPending = true
	if _, err := s.CleanupExpired(ctx, base.Add(3*time.Hour)); !errors.Is(err, ErrRetentionPending) {
		t.Fatalf("retention ran before recovery: %v", err)
	}
	rows, _ := exportAllTestRollups(t, s, "retention", 10)
	if len(rows) != 1 || rows[0].Resolution != time.Minute {
		t.Fatalf("retention deleted unrecovered source: %+v", rows)
	}
}

func TestRetentionFlushesRecoveredCoarseChainBeforePruning(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: time.Hour},
		{Interval: 5 * time.Minute, Retention: 2 * time.Hour},
		{Interval: time.Hour, Retention: 72 * time.Hour},
	}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "retention", RetentionDays: 2}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour)
	row := constantTransferRollup("retention", "node", base, time.Minute, 10, base)
	if err := s.ImportRollups(ctx, []PersistedRollup{row}); err != nil {
		t.Fatal(err)
	}
	s.rollupViewMu.Lock()
	s.coarseRecoveryPending = true
	if err := s.recoverPendingCoarseRollups(ctx); err != nil {
		s.rollupViewMu.Unlock()
		t.Fatal(err)
	}
	s.rollupViewMu.Unlock()
	if len(s.coarse) == 0 {
		t.Fatal("fixture did not recover a pending parent")
	}
	if _, err := s.FlushCoarse(ctx, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupExpired(ctx, base.Add(3*time.Hour)); err != nil {
		t.Fatalf("retention did not make progress after parent persisted: %v", err)
	}
	rows, _ := exportAllTestRollups(t, s, "retention", 10)
	if len(rows) != 1 || rows[0].Resolution != time.Hour || rows[0].Count != 4 || rows[0].Sum != 40 {
		t.Fatalf("recovered coarser data lost: %+v", rows)
	}
}

func TestRawBaselineConcurrentReplacements(t *testing.T) {
	s, path, base := baselineFixture(t)
	ctx := t.Context()
	if err := s.Write(ctx, historyPoint(base.Add(5*time.Second), 10, "a")); err != nil {
		t.Fatal(err)
	}
	s = reopenBaseline(t, s, path)
	at := base.Add(30 * time.Second)
	var wg sync.WaitGroup
	errCh := make(chan error, 16)
	for i := 0; i < cap(errCh); i++ {
		wg.Go(func() {
			if err := s.Write(ctx, historyPoint(at, 30, "b")); err != nil {
				errCh <- err
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(fmt.Errorf("concurrent write: %w", err))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotSummary(t, path, "history", time.Minute, 2, 40)
}
