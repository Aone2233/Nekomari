package metric

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// rollupPlanFixture is a hermetic in-memory rollups table: entityCount entities
// each carrying one series per metric, with bucketCount minute buckets per
// series.
type rollupPlanFixture struct {
	ctx       context.Context
	store     *Store
	seriesIDs []int64
	resID     int64
	start     int64
	end       int64
}

func newRollupPlanFixture(tb testing.TB, entityCount, metricCount, bucketCount int) *rollupPlanFixture {
	tb.Helper()
	ctx := context.Background()
	store, err := Open(ctx, SQLite("file:test-rollup-read-index?mode=memory&cache=shared"))
	if err != nil {
		tb.Fatalf("open store: %v", err)
	}
	tb.Cleanup(func() { _ = store.Close() })

	if _, err := store.ExecContext(ctx, `INSERT INTO metric_resolutions (resolution_milli) VALUES (?)`, int64(60000)); err != nil {
		tb.Fatalf("insert resolution: %v", err)
	}
	var resID int64
	if err := store.reader().QueryRowContext(ctx, `SELECT id FROM metric_resolutions WHERE resolution_milli = ?`, int64(60000)).Scan(&resID); err != nil {
		tb.Fatalf("read resolution id: %v", err)
	}
	if _, err := store.ExecContext(ctx, `INSERT INTO metric_label_sets (labels_hash, labels) VALUES ('h', '{}')`); err != nil {
		tb.Fatalf("insert label set: %v", err)
	}
	var labelID int64
	if err := store.reader().QueryRowContext(ctx, `SELECT id FROM metric_label_sets WHERE labels_hash = 'h'`).Scan(&labelID); err != nil {
		tb.Fatalf("read label id: %v", err)
	}

	metricNames := make([]string, 0, metricCount)
	for i := 0; i < metricCount; i++ {
		name := fmt.Sprintf("m%02d", i)
		metricNames = append(metricNames, name)
		if _, err := store.ExecContext(ctx,
			`INSERT INTO metric_definitions (name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli) VALUES (?, 'gauge', '', '', 30, '{}', 0, 0)`,
			name); err != nil {
			tb.Fatalf("insert definition %s: %v", name, err)
		}
	}

	start := time.Now().UTC().Add(-time.Duration(bucketCount) * time.Minute).UnixMilli()
	start -= start % 60000
	fixture := &rollupPlanFixture{
		ctx:   ctx,
		store: store,
		resID: resID,
		start: start,
		end:   start + int64(bucketCount)*60000,
	}
	insert, err := store.db.PrepareContext(ctx,
		`INSERT INTO metric_rollups (series_id, resolution_id, label_id, bucket_milli, count, sum, sum_sq, min_val, max_val, first_val, first_ts_milli, last_val, last_ts_milli, digest, created_at_milli)
		 VALUES (?, ?, ?, ?, 1, 1, 1, 1, 1, 1, 0, 1, 0, NULL, 0)`)
	if err != nil {
		tb.Fatalf("prepare rollup insert: %v", err)
	}
	defer insert.Close()

	for entity := 0; entity < entityCount; entity++ {
		entityID := fmt.Sprintf("node-%03d", entity)
		for _, metricName := range metricNames {
			res, err := store.ExecContext(ctx,
				`INSERT INTO metric_series (metric_name, entity_id, tags_hash, tags) VALUES (?, ?, 'h', '{}')`, metricName, entityID)
			if err != nil {
				tb.Fatalf("insert series %s/%s: %v", metricName, entityID, err)
			}
			seriesID, err := res.LastInsertId()
			if err != nil {
				tb.Fatalf("series id: %v", err)
			}
			fixture.seriesIDs = append(fixture.seriesIDs, seriesID)
			for bucket := 0; bucket < bucketCount; bucket++ {
				if _, err := insert.ExecContext(ctx, seriesID, resID, labelID, start+int64(bucket)*60000); err != nil {
					tb.Fatalf("insert rollup: %v", err)
				}
			}
		}
	}
	return fixture
}

func (f *rollupPlanFixture) readPlan(seriesIDs []int64) rollupReadPlan {
	return rollupReadPlan{
		SeriesIDs:    seriesIDs,
		ResolutionID: f.resID,
		StartMilli:   f.start,
		EndMilli:     f.end,
		Fields:       rollupReadSum,
	}
}

// persistedReadSQL renders the read through the production path — the same
// function scanPersistedRollupGroup calls — so the access-path selection is
// covered, not just the renderer it feeds.
func (f *rollupPlanFixture) persistedReadSQL(tb testing.TB, seriesIDs []int64) renderedSQL {
	tb.Helper()
	group := &batchSeriesGroup{
		key:         batchSeriesGroupKey{resolution: time.Minute},
		metricNames: map[string]struct{}{"m00": {}},
		fields:      rollupReadSum,
	}
	query := BatchSeriesQuery{Start: fromMillis(f.start), End: fromMillis(f.end)}
	rendered, found, err := f.store.renderPersistedRollupRead(f.ctx, query, group, seriesIDs)
	if err != nil {
		tb.Fatalf("render persisted rollup read: %v", err)
	}
	if !found {
		tb.Fatal("persisted rollup read found no resolution row")
	}
	return rendered
}

func (f *rollupPlanFixture) plan(t *testing.T, rendered renderedSQL) string {
	t.Helper()
	rows, err := f.store.reader().QueryContext(f.ctx, "EXPLAIN QUERY PLAN "+rendered.Query, rendered.Args...)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return strings.Join(details, " | ")
}

func (f *rollupPlanFixture) rows(t *testing.T, seriesIDs []int64, indexName string) []string {
	t.Helper()
	rendered := f.store.dialect.renderRollupRead(f.store.tables, indexName, f.readPlan(seriesIDs))
	rows, err := f.store.reader().QueryContext(f.ctx, rendered.Query, rendered.Args...)
	if err != nil {
		t.Fatalf("run rollup read: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seriesID, bucket, count int64
		var sum float64
		if err := rows.Scan(&seriesID, &bucket, &count, &sum); err != nil {
			t.Fatalf("scan rollup row: %v", err)
		}
		out = append(out, fmt.Sprintf("%d/%d/%d/%v", seriesID, bucket, count, sum))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rollup rows: %v", err)
	}
	return out
}

// TestRollupReadIndexSelection pins which access path each rollup read is
// forced onto. An entity-scoped read must be driven by the series index; a
// fleet-wide read keeps the resolution index.
func TestRollupReadIndexSelection(t *testing.T) {
	const entities = 40
	const metrics = 17
	fixture := newRollupPlanFixture(t, entities, metrics, 4)
	ctx := context.Background()
	store := fixture.store

	narrow := fixture.seriesIDs[:metrics]
	wide := fixture.seriesIDs

	seriesIndex := store.rollupReadIndex(ctx, len(narrow))
	if seriesIndex == "" {
		t.Fatal("expected an index to force for an entity-scoped read")
	}
	if leading, err := store.indexLeadingColumn(ctx, seriesIndex); err != nil || leading != "series_id" {
		t.Fatalf("entity-scoped index %q leads with %q (err %v), want series_id", seriesIndex, leading, err)
	}
	if got := store.rollupReadIndex(ctx, len(wide)); got != "metric_rollups_resolution_bucket_idx" {
		t.Fatalf("fleet-wide index = %q, want the resolution index", got)
	}
	if got := store.rollupReadIndex(ctx, narrowSeriesReadLimit+1); got != "metric_rollups_resolution_bucket_idx" {
		t.Fatalf("index just above the narrow limit = %q, want the resolution index", got)
	}
	if got := store.rollupReadIndex(ctx, narrowSeriesReadLimit); got != seriesIndex {
		t.Fatalf("index at the narrow limit = %q, want %q", got, seriesIndex)
	}

	narrowPlan := fixture.plan(t, fixture.persistedReadSQL(t, narrow))
	if !strings.Contains(narrowPlan, seriesIndex) || !strings.Contains(narrowPlan, "series_id=?") {
		t.Fatalf("entity-scoped plan does not drive from the series index: %s", narrowPlan)
	}
	if strings.Contains(narrowPlan, "metric_rollups_resolution_bucket_idx") {
		t.Fatalf("entity-scoped plan still uses the resolution index: %s", narrowPlan)
	}

	widePlan := fixture.plan(t, fixture.persistedReadSQL(t, wide))
	if !strings.Contains(widePlan, "metric_rollups_resolution_bucket_idx") {
		t.Fatalf("fleet-wide plan does not use the resolution index: %s", widePlan)
	}
	if strings.Contains(widePlan, seriesIndex) {
		t.Fatalf("fleet-wide plan switched to the series index: %s", widePlan)
	}

	// The access path is the only thing that may change: both must return the
	// same rows for the same series set.
	narrowRows := fixture.rows(t, narrow, seriesIndex)
	referenceRows := fixture.rows(t, narrow, "metric_rollups_resolution_bucket_idx")
	if len(narrowRows) != len(referenceRows) {
		t.Fatalf("row count changed with the access path: %d vs %d", len(narrowRows), len(referenceRows))
	}
	sort.Strings(narrowRows)
	sort.Strings(referenceRows)
	for i := range narrowRows {
		if narrowRows[i] != referenceRows[i] {
			t.Fatalf("row %d differs: %s vs %s", i, narrowRows[i], referenceRows[i])
		}
	}
}

// TestRollupReadSQLShape pins the rendered SQL: the fleet-wide read keeps the
// json_each form and the resolution index it has always used, and only the
// entity-scoped read drops the hint.
func TestRollupReadSQLShape(t *testing.T) {
	fixture := newRollupPlanFixture(t, 1, 1, 1)
	plan := rollupReadPlan{SeriesIDs: []int64{1, 2}, ResolutionID: 3, StartMilli: 4, EndMilli: 5, Fields: rollupReadSum}

	wideSQL := fixture.store.dialect.renderRollupRead(fixture.store.tables, "metric_rollups_resolution_bucket_idx", plan).Query
	if !strings.Contains(wideSQL, "INDEXED BY metric_rollups_resolution_bucket_idx") {
		t.Fatalf("fleet-wide SQL lost its index hint: %s", wideSQL)
	}
	if !strings.Contains(wideSQL, "json_each(") {
		t.Fatalf("fleet-wide SQL changed shape: %s", wideSQL)
	}

	narrowSQL := fixture.store.dialect.renderRollupRead(fixture.store.tables, "", plan).Query
	if strings.Contains(narrowSQL, "INDEXED BY") {
		t.Fatalf("empty index name still rendered a hint: %s", narrowSQL)
	}

	mysql := mysqlDialect{}
	mysqlWide := mysql.renderRollupRead(fixture.store.tables, "metric_rollups_resolution_bucket_idx", plan).Query
	if !strings.Contains(mysqlWide, "FORCE INDEX (metric_rollups_resolution_bucket_idx)") {
		t.Fatalf("MySQL fleet-wide SQL lost FORCE INDEX: %s", mysqlWide)
	}
	mysqlNarrow := mysql.renderRollupRead(fixture.store.tables, "", plan).Query
	if strings.Contains(mysqlNarrow, "FORCE INDEX") {
		t.Fatalf("MySQL entity-scoped SQL still forces an index: %s", mysqlNarrow)
	}
	if len(mysqlNarrow) == 0 {
		t.Fatal("empty MySQL SQL")
	}
}

// BenchmarkRollupReadAccessPath measures the two access paths over the same
// entity-scoped series set. Run with:
//
//	go test ./pkg/metric/ -run '^$' -bench RollupReadAccessPath -benchtime 3x
func BenchmarkRollupReadAccessPath(b *testing.B) {
	const entities = 200
	const metrics = 17
	const buckets = 240
	fixture := newRollupPlanFixture(b, entities, metrics, buckets)
	narrow := fixture.seriesIDs[:metrics]

	seriesIndex := fixture.store.rollupReadIndex(fixture.ctx, len(narrow))
	if seriesIndex == "" {
		b.Fatal("no series index resolved")
	}
	plan := fixture.readPlan(narrow)

	for _, accessPath := range []struct {
		name  string
		index string
	}{
		{"resolution_index", "metric_rollups_resolution_bucket_idx"},
		{"series_index", seriesIndex},
	} {
		rendered := fixture.store.dialect.renderRollupRead(fixture.store.tables, accessPath.index, plan)
		b.Run(accessPath.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				rows, err := fixture.store.reader().QueryContext(fixture.ctx, rendered.Query, rendered.Args...)
				if err != nil {
					b.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var seriesID, bucket, rowCount int64
					var sum float64
					if err := rows.Scan(&seriesID, &bucket, &rowCount, &sum); err != nil {
						b.Fatal(err)
					}
					count++
				}
				if err := rows.Close(); err != nil {
					b.Fatal(err)
				}
				if count != metrics*buckets {
					b.Fatalf("rows = %d, want %d", count, metrics*buckets)
				}
			}
		})
	}
}
