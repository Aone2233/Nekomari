package metric

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type seriesDictionaryPlan struct {
	MetricNames []string
	EntityIDs   []string
	Tags        map[string]string
}

type rollupReadPlan struct {
	SeriesIDs    []int64
	ResolutionID int64
	StartMilli   int64
	EndMilli     int64
	Fields       rollupReadFields
}

type rollupReadFields uint16

const (
	rollupReadSum rollupReadFields = 1 << iota
	rollupReadSumSq
	rollupReadMin
	rollupReadMax
	rollupReadFirst
	rollupReadLast
	rollupReadDigest
)

type renderedSQL struct {
	Query string
	Args  []any
}

func (d sqliteDialect) renderSeriesDictionary(tables tables, indexName string, plan seriesDictionaryPlan) renderedSQL {
	return renderExpandedSeriesDictionary(d, tables, " INDEXED BY "+indexName, plan)
}

func (d mysqlDialect) renderSeriesDictionary(tables tables, indexName string, plan seriesDictionaryPlan) renderedSQL {
	return renderExpandedSeriesDictionary(d, tables, " FORCE INDEX ("+indexName+")", plan)
}

func (d postgresDialect) renderSeriesDictionary(tables tables, _ string, plan seriesDictionaryPlan) renderedSQL {
	args := []any{plan.MetricNames}
	parts := []string{"s.metric_name = ANY(" + d.placeholder(1) + "::text[])"}
	if len(plan.EntityIDs) > 0 {
		args = append(args, plan.EntityIDs)
		parts = append(parts, "s.entity_id = ANY("+d.placeholder(len(args))+"::text[])")
	}
	for _, key := range sortedKeys(plan.Tags) {
		args = append(args, plan.Tags[key])
		parts = append(parts, d.jsonExtractEquals("s.tags", key, d.placeholder(len(args))))
	}
	return renderedSQL{
		Query: fmt.Sprintf("SELECT s.id, s.metric_name, s.entity_id, s.tags_hash, s.tags FROM %s s WHERE %s ORDER BY s.id ASC", tables.series, strings.Join(parts, " AND ")),
		Args:  args,
	}
}

func renderExpandedSeriesDictionary(d dialect, tables tables, indexHint string, plan seriesDictionaryPlan) renderedSQL {
	args := make([]any, 0, len(plan.MetricNames)+len(plan.EntityIDs)+len(plan.Tags))
	metricPlaceholders := appendPlaceholders(d, &args, plan.MetricNames)
	parts := []string{"s.metric_name IN (" + strings.Join(metricPlaceholders, ", ") + ")"}
	if len(plan.EntityIDs) > 0 {
		entityPlaceholders := appendPlaceholders(d, &args, plan.EntityIDs)
		parts = append(parts, "s.entity_id IN ("+strings.Join(entityPlaceholders, ", ")+")")
	}
	for _, key := range sortedKeys(plan.Tags) {
		args = append(args, plan.Tags[key])
		parts = append(parts, d.jsonExtractEquals("s.tags", key, d.placeholder(len(args))))
	}
	return renderedSQL{
		Query: fmt.Sprintf("SELECT s.id, s.metric_name, s.entity_id, s.tags_hash, s.tags FROM %s s%s WHERE %s ORDER BY s.id ASC", tables.series, indexHint, strings.Join(parts, " AND ")),
		Args:  args,
	}
}

func appendPlaceholders[T ~string](d dialect, args *[]any, values []T) []string {
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		*args = append(*args, string(value))
		placeholders = append(placeholders, d.placeholder(len(*args)))
	}
	return placeholders
}

// renderRollupRead renders one persisted rollup scan.
//
// indexName, when non-empty, is forced as the access path (SQLite INDEXED BY,
// MySQL FORCE INDEX). An empty indexName means "let the backend's optimizer
// choose"; callers pass it for entity-scoped reads, where forcing the
// (resolution_id, bucket_milli) index would scan every series in the window.
// See Store.rollupReadIndex for how the two cases are told apart.
func (d sqliteDialect) renderRollupRead(tables tables, indexName string, plan rollupReadPlan) renderedSQL {
	seriesJSON, _ := json.Marshal(plan.SeriesIDs)
	args := []any{plan.ResolutionID, plan.StartMilli, plan.EndMilli, string(seriesJSON)}
	hint := ""
	if indexName != "" {
		hint = " INDEXED BY " + indexName
	}
	return renderedSQL{
		Query: fmt.Sprintf("SELECT %s FROM %s r%s WHERE r.resolution_id = %s AND r.bucket_milli >= %s AND r.bucket_milli <= %s AND r.series_id IN (SELECT CAST(value AS INTEGER) FROM json_each(%s)) ORDER BY r.bucket_milli ASC, r.series_id ASC, r.label_id ASC",
			rollupReadColumns(plan.Fields), tables.rollups, hint,
			d.placeholder(1), d.placeholder(2), d.placeholder(3), d.placeholder(4)),
		Args: args,
	}
}

func (d mysqlDialect) renderRollupRead(tables tables, indexName string, plan rollupReadPlan) renderedSQL {
	args := []any{plan.ResolutionID, plan.StartMilli, plan.EndMilli}
	seriesPlaceholders := make([]string, 0, len(plan.SeriesIDs))
	for _, seriesID := range plan.SeriesIDs {
		args = append(args, seriesID)
		seriesPlaceholders = append(seriesPlaceholders, d.placeholder(len(args)))
	}
	hint := ""
	if indexName != "" {
		hint = " FORCE INDEX (" + indexName + ")"
	}
	return renderedSQL{
		Query: fmt.Sprintf("SELECT %s FROM %s r%s WHERE r.resolution_id = %s AND r.bucket_milli >= %s AND r.bucket_milli <= %s AND r.series_id IN (%s) ORDER BY r.bucket_milli ASC, r.series_id ASC, r.label_id ASC",
			rollupReadColumns(plan.Fields), tables.rollups, hint,
			d.placeholder(1), d.placeholder(2), d.placeholder(3), strings.Join(seriesPlaceholders, ", ")),
		Args: args,
	}
}

func (d postgresDialect) renderRollupRead(tables tables, _ string, plan rollupReadPlan) renderedSQL {
	args := []any{plan.ResolutionID, plan.StartMilli, plan.EndMilli, plan.SeriesIDs}
	return renderedSQL{
		Query: fmt.Sprintf("SELECT %s FROM %s r WHERE r.resolution_id = %s AND r.bucket_milli >= %s AND r.bucket_milli <= %s AND r.series_id = ANY(%s::bigint[]) ORDER BY r.bucket_milli ASC, r.series_id ASC, r.label_id ASC",
			rollupReadColumns(plan.Fields), tables.rollups,
			d.placeholder(1), d.placeholder(2), d.placeholder(3), d.placeholder(4)),
		Args: args,
	}
}

func rollupReadColumns(fields rollupReadFields) string {
	columns := []string{"r.series_id", "r.bucket_milli", "r.count"}
	if fields&rollupReadSum != 0 {
		columns = append(columns, "r.sum")
	}
	if fields&rollupReadSumSq != 0 {
		columns = append(columns, "r.sum_sq")
	}
	if fields&rollupReadMin != 0 {
		columns = append(columns, "r.min_val")
	}
	if fields&rollupReadMax != 0 {
		columns = append(columns, "r.max_val")
	}
	if fields&rollupReadFirst != 0 {
		columns = append(columns, "r.first_val", "r.first_ts_milli")
	}
	if fields&rollupReadLast != 0 {
		columns = append(columns, "r.last_val", "r.last_ts_milli")
	}
	if fields&rollupReadDigest != 0 {
		columns = append(columns, "r.digest")
	}
	return strings.Join(columns, ", ")
}

// narrowSeriesReadLimit is the largest series set that a persisted rollup read
// still treats as entity-scoped.
//
// A panel request for one node's history resolves to roughly one series per
// metric (about twenty), and a handful of nodes stays well inside this bound,
// while a fleet-wide read resolves to thousands of series. The value is a
// policy boundary, not a planner constant: at or below it the read is driven
// from the requested series, above it the resolution index is kept.
const narrowSeriesReadLimit = 64

// rollupReadIndex returns the index to force for one persisted rollup scan, or
// "" to leave the access path to the backend's optimizer.
//
// The normalized schema has two usable indexes on the rollups table:
// UNIQUE(series_id, resolution_id, label_id, bucket_milli) and
// (resolution_id, bucket_milli). The second one is only selective on time, so
// forcing it for a read that names a handful of series makes the scan walk
// every series in the window and discard all but the requested ones — cost that
// grows with the number of monitored entities. Entity-scoped reads are
// therefore driven from the series index instead.
//
// The wide case deliberately keeps the previous behaviour: a fleet-wide scan
// covers most of the rows in the window anyway, and the resolution index
// returns them partially ordered by bucket, which the series index cannot.
func (s *Store) rollupReadIndex(ctx context.Context, seriesCount int) string {
	if seriesCount > narrowSeriesReadLimit {
		return s.cfg.TablePrefix + "rollups_resolution_bucket_idx"
	}
	return s.rollupSeriesIndexName(ctx)
}

// rollupSeriesIndexName returns the name of the index whose leading column is
// series_id on the rollups table, or "" when there is none to force.
//
// SQLite has no way to name an index in the query other than by its name, and
// the index that leads with series_id is the one the UNIQUE constraint creates
// implicitly — a name this package does not choose. It is therefore looked up
// in the catalog once per Store instead of being hardcoded: a database created
// by an older build, or by a rebuild path that laid the table out differently,
// would otherwise make every entity-scoped read fail at prepare time with
// "no such index". MySQL and PostgreSQL return "" and fall back to their own
// optimizer, which has cardinality statistics for the IN list that SQLite's
// planner does not.
func (s *Store) rollupSeriesIndexName(ctx context.Context) string {
	if s.cfg.Driver != DriverSQLite {
		return ""
	}
	s.rollupSeriesIndexOnce.Do(func() {
		// The lookup is a property of the schema, not of the caller's request:
		// it must not be abandoned halfway by a cancelled query and then cached
		// as "no index", which would silently disable the narrow read path for
		// the lifetime of the Store.
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		s.rollupSeriesIndex = s.resolveRollupSeriesIndexName(resolveCtx)
	})
	return s.rollupSeriesIndex
}

func (s *Store) resolveRollupSeriesIndexName(ctx context.Context) string {
	rows, err := s.reader().QueryContext(ctx,
		`SELECT name FROM pragma_index_list(?) WHERE "unique" = 1 ORDER BY name ASC`, s.tables.rollups)
	if err != nil {
		return ""
	}
	names := make([]string, 0, 2)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return ""
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ""
	}
	rows.Close()

	for _, name := range names {
		leading, err := s.indexLeadingColumn(ctx, name)
		if err != nil {
			return ""
		}
		if leading == "series_id" {
			return name
		}
	}
	return ""
}

// indexLeadingColumn returns the first column of one index, in index order.
func (s *Store) indexLeadingColumn(ctx context.Context, index string) (string, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT name FROM pragma_index_info(?) WHERE seqno = 0`, index)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", rows.Err()
	}
	var column string
	if err := rows.Scan(&column); err != nil {
		return "", err
	}
	return column, rows.Err()
}
