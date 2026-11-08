package metric

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The boot-time recovery scan runs once per metric and tier on every startup,
// over the whole retained history. Its access path is therefore part of the
// contract, not an implementation detail: a plan that starts from the
// resolution index re-reads every series' rows of that tier once per metric,
// and on a production-shaped store that is the difference between a bounded
// startup and one that exhausts its deadline.
func TestCoarseRecoveryStatementIsSeriesDriven(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	path := filepath.Join(t.TempDir(), "plan.db")
	store, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	fineID, err := store.resolutionIDFor(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	coarseID, err := store.resolutionIDFor(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	query, args := store.coarseRecoveryQuery(ctx, "plan.metric", fineID, coarseID, 5*time.Minute)
	if len(args) != 3 {
		t.Fatalf("recovery statement binds %d arguments, want 3 (metric, fine id, coarse id): %v", len(args), args)
	}
	// The parent lookup must be constrained by the resolution's id directly. A
	// join on the resolutions table is what previously allowed the planner to
	// drive the scan from the wide resolution index.
	if strings.Contains(query, store.tables.resolutions) {
		t.Fatalf("recovery statement still joins %q:\n%s", store.tables.resolutions, query)
	}
	index := store.rollupSeriesIndexName(ctx)
	if index == "" {
		t.Skip("fixture has no series-leading index to assert")
	}
	if !strings.Contains(query, "INDEXED BY "+index) {
		t.Fatalf("recovery statement does not force %q:\n%s", index, query)
	}

	rows, err := store.reader().QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain recovery statement: %v", err)
	}
	defer rows.Close()
	plan := make([]string, 0, 8)
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, index) {
		t.Fatalf("plan does not use the series-leading index %q: %s", index, joined)
	}
	if strings.Contains(joined, "rollups_resolution_bucket_idx") {
		t.Fatalf("plan fell back to the resolution index: %s", joined)
	}
	if !strings.Contains(joined, "SEARCH s USING INDEX "+store.cfg.TablePrefix+"series_metric_entity_idx") {
		t.Fatalf("plan does not drive from the metric's series: %s", joined)
	}
}

// SQLite tolerates surplus arguments; the other supported drivers reject them.
func TestCoarseRecoveryQueryBindings(t *testing.T) {
	for _, driver := range []Driver{DriverSQLite, DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driver), func(t *testing.T) {
			store := &Store{
				cfg:     Config{Driver: driver},
				dialect: newDialect(driver),
				tables: tables{
					rollups: "metric_rollups", series: "metric_series", labels: "metric_label_sets",
				},
			}
			// The SQLite index hint is verified by the plan test above.
			store.rollupSeriesIndexOnce.Do(func() {})
			query, args := store.coarseRecoveryQuery(context.Background(), "latency", 7, 11, 5*time.Minute)
			if want := []any{"latency", int64(7), int64(11)}; !reflect.DeepEqual(args, want) {
				t.Fatalf("recovery arguments = %v, want %v", args, want)
			}
			placeholder := regexp.MustCompile(`\?`)
			if driver == DriverPostgreSQL {
				placeholder = regexp.MustCompile(`\$[0-9]+`)
			}
			bound := placeholder.FindAllString(query, -1)
			if len(bound) != len(args) {
				t.Fatalf("%d placeholders, %d arguments: %s", len(bound), len(args), query)
			}
			for i, actual := range bound {
				if want := store.dialect.placeholder(i + 1); actual != want {
					t.Fatalf("placeholder %d = %q, want %q", i+1, actual, want)
				}
			}
			if driver != DriverSQLite && strings.Contains(query, "INDEXED BY") {
				t.Fatalf("SQLite-only hint leaked into %s query: %s", driver, query)
			}
			if !strings.Contains(query, "p.bucket_milli % 300000 = 0") || strings.Contains(query, " DIV ") {
				t.Fatalf("missing signed, aligned parent range: %s", query)
			}
		})
	}
}

// Before 1970, SQL integer division truncates toward zero while rollup
// buckets are aligned toward negative infinity. Reopening must recognize an
// already sealed parent and recover a genuinely missing one independently.
func TestCoarseRecoveryBeforeUnixEpoch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	path := filepath.Join(t.TempDir(), "pre-epoch.db")
	open := func() *Store {
		s, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	defer func() { _ = s.Close() }()
	if err := s.CreateMetric(ctx, Definition{Name: "before-epoch", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	points := []Point{
		{MetricName: "before-epoch", EntityID: "sealed", Timestamp: time.Unix(-30, 0), Value: 7},
		{MetricName: "before-epoch", EntityID: "missing", Timestamp: time.Unix(-90, 0), Value: 9},
		{MetricName: "before-epoch", EntityID: "boundary", Timestamp: time.Unix(-300, 0), Value: 11},
		{MetricName: "before-epoch", EntityID: "positive", Timestamp: time.Unix(60, 0), Value: 13},
	}
	if err := s.ReplaceRollupPoints(ctx, 5*time.Minute, points); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s
		WHERE resolution_id = (SELECT id FROM %s WHERE resolution_milli = ?)
		AND series_id = (SELECT id FROM %s WHERE metric_name = ? AND entity_id = ?)`,
		s.tables.rollups, s.tables.resolutions, s.tables.series), (5 * time.Minute).Milliseconds(), "before-epoch", "missing"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	if len(s.coarse) != 1 {
		t.Fatalf("recovered %d parents, want only the missing one", len(s.coarse))
	}
	for key, bucket := range s.coarse {
		if key.entityID != "missing" || key.bucket != -300000 || len(bucket.children) != 1 {
			t.Fatalf("unexpected recovered parent: %+v, %d children", key, len(bucket.children))
		}
	}
	if written, err := s.FlushCoarse(ctx, time.Unix(3600, 0)); err != nil || written != 1 {
		t.Fatalf("flush recovered parent = %d, %v; want 1, nil", written, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	if len(s.coarse) != 0 {
		t.Fatalf("sealed parents were recovered again: %+v", s.coarse)
	}
}

func TestCoarseRecoverySQLIntegration(t *testing.T) {
	for _, backend := range []struct {
		name string
		env  string
		cfg  func(string, ...Option) Config
	}{
		{"mysql", "METRIC_MYSQL_DSN", MySQL},
		{"postgres", "METRIC_POSTGRES_DSN", PostgreSQL},
	} {
		t.Run(backend.name, func(t *testing.T) {
			dsn := os.Getenv(backend.env)
			if dsn == "" {
				t.Skip(backend.env + " is not set")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			policy := RollupPolicy{Tiers: []RollupTier{
				{Interval: time.Minute, Retention: 24 * time.Hour},
				{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
			}, Compression: 30}
			cfg := backend.cfg(dsn, WithRollupPolicy(policy))
			cfg.TablePrefix = fmt.Sprintf("recovery_%d_", time.Now().UnixNano())
			store, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			defer dropIntegrationTables(t, store, cfg.TablePrefix)
			if err := store.CreateMetric(ctx, Definition{Name: "recovery.metric", RetentionDays: 30}); err != nil {
				t.Fatal(err)
			}
			base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
			prepared, err := prepareMetricPoints([]Point{{MetricName: "recovery.metric", EntityID: "n1", Timestamp: base.Add(2*time.Minute + 10*time.Second), Value: 7}})
			if err != nil {
				t.Fatal(err)
			}
			rebuild := store.writeRawPointsAt(prepared, base.Add(4*time.Minute))
			if err := store.writePreparedHotRollups(ctx, prepared, base.Add(4*time.Minute), rebuild); err != nil {
				t.Fatal(err)
			}
			store.coarse = make(map[coarseRollupKey]*coarseRollup)
			store.coarseRecoveryPending = true
			if err := store.recoverPendingCoarseRollups(ctx); err != nil {
				t.Fatalf("recover durable child on %s: %v", backend.name, err)
			}
			if written, err := store.FlushCoarse(ctx, base.Add(time.Hour)); err != nil || written != 1 {
				t.Fatalf("recovered flush = %d, %v; want 1, nil", written, err)
			}
			rows, err := store.scanRollupRows(ctx, store.reader(), "recovery.metric", 5*time.Minute)
			if err != nil || len(rows) != 1 || rows[0].bucketData.count != 1 || rows[0].bucketData.sum != 7 {
				t.Fatalf("recovered parent = %#v, %v", rows, err)
			}
			store.coarseRecoveryPending = true
			if err := store.recoverPendingCoarseRollups(ctx); err != nil {
				t.Fatalf("recheck sealed parent: %v", err)
			}
			if len(store.coarse) != 0 {
				t.Fatalf("sealed parent restaged: %d buckets", len(store.coarse))
			}
		})
	}
}

// The same signed range lookup must work with the real MySQL and PostgreSQL
// drivers, not just their generated placeholder strings.
func TestCoarseRecoverySignedSQLIntegration(t *testing.T) {
	for _, backend := range []struct {
		name string
		env  string
		cfg  func(string, ...Option) Config
	}{
		{"mysql", "METRIC_MYSQL_DSN", MySQL},
		{"postgres", "METRIC_POSTGRES_DSN", PostgreSQL},
	} {
		t.Run(backend.name, func(t *testing.T) {
			dsn := os.Getenv(backend.env)
			if dsn == "" {
				t.Skip(backend.env + " is not set")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			policy := RollupPolicy{Tiers: []RollupTier{
				{Interval: time.Minute, Retention: 24 * time.Hour},
				{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
			}, Compression: 30}
			cfg := backend.cfg(dsn, WithRollupPolicy(policy), WithMaxOpenConns(1), WithMaxIdleConns(1))
			cfg.TablePrefix = fmt.Sprintf("signed_%d_", time.Now().UnixNano())
			s, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			defer dropIntegrationTables(t, s, cfg.TablePrefix)
			if err := s.CreateMetric(ctx, Definition{Name: "before-epoch", RetentionDays: 30}); err != nil {
				t.Fatal(err)
			}
			points := []Point{
				{MetricName: "before-epoch", EntityID: "sealed", Timestamp: time.Unix(-30, 0), Value: 7},
				{MetricName: "before-epoch", EntityID: "missing", Timestamp: time.Unix(-90, 0), Value: 9},
				{MetricName: "before-epoch", EntityID: "boundary", Timestamp: time.Unix(-300, 0), Value: 11},
				{MetricName: "before-epoch", EntityID: "positive", Timestamp: time.Unix(60, 0), Value: 13},
			}
			if err := s.ReplaceRollupPoints(ctx, 5*time.Minute, points); err != nil {
				t.Fatal(err)
			}
			query := fmt.Sprintf(`DELETE FROM %s WHERE resolution_id =
				(SELECT id FROM %s WHERE resolution_milli = %s) AND series_id =
				(SELECT id FROM %s WHERE metric_name = %s AND entity_id = %s)`,
				s.tables.rollups, s.tables.resolutions, s.dialect.placeholder(1), s.tables.series,
				s.dialect.placeholder(2), s.dialect.placeholder(3))
			if _, err := s.db.ExecContext(ctx, query, (5 * time.Minute).Milliseconds(), "before-epoch", "missing"); err != nil {
				t.Fatal(err)
			}
			s.coarseRecoveryPending = true
			if err := s.recoverPendingCoarseRollups(ctx); err != nil {
				t.Fatalf("signed recovery on %s: %v", backend.name, err)
			}
			if len(s.coarse) != 1 {
				t.Fatalf("recovered %d parents, want only the missing one", len(s.coarse))
			}
			for key, parent := range s.coarse {
				if key.entityID != "missing" || key.bucket != -300000 || len(parent.children) != 1 {
					t.Fatalf("unexpected recovered parent: %+v, %d children", key, len(parent.children))
				}
			}
			if written, err := s.FlushCoarse(ctx, time.Unix(3600, 0)); err != nil || written != 1 {
				t.Fatalf("flush recovered parent = %d, %v; want 1, nil", written, err)
			}
			s.coarseRecoveryPending = true
			if err := s.recoverPendingCoarseRollups(ctx); err != nil || len(s.coarse) != 0 {
				t.Fatalf("sealed parents recovered again: %d, %v", len(s.coarse), err)
			}
		})
	}
}

// Exercise the range predicate itself at both integer boundaries. This does
// not claim that the rest of the rollup engine accepts extreme timestamps.
func TestCoarseRecoveryQueryIntegerBoundaries(t *testing.T) {
	for _, backend := range []struct {
		name string
		env  string
		cfg  func(string, ...Option) Config
	}{
		{"sqlite", "", SQLite},
		{"mysql", "METRIC_MYSQL_DSN", MySQL},
		{"postgres", "METRIC_POSTGRES_DSN", PostgreSQL},
	} {
		t.Run(backend.name, func(t *testing.T) {
			dsn := filepath.Join(t.TempDir(), "integer-boundaries.db")
			if backend.env != "" {
				dsn = os.Getenv(backend.env)
				if dsn == "" {
					t.Skip(backend.env + " is not set")
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			policy := RollupPolicy{Tiers: []RollupTier{
				{Interval: time.Minute, Retention: 24 * time.Hour},
				{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
			}, Compression: 30}
			cfg := backend.cfg(dsn, WithRollupPolicy(policy), WithMaxOpenConns(1), WithMaxIdleConns(1))
			if backend.env != "" {
				cfg.TablePrefix = fmt.Sprintf("bounds_%d_", time.Now().UnixNano())
			}
			s, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if backend.env != "" {
				defer dropIntegrationTables(t, s, cfg.TablePrefix)
			}
			if err := s.CreateMetric(ctx, Definition{Name: "bounds", RetentionDays: 30}); err != nil {
				t.Fatal(err)
			}
			base := time.Unix(300, 0).UTC()
			for _, entity := range []string{"min-missing", "min-sealed", "min-unaligned", "max-missing", "max-sealed", "max-unaligned"} {
				if err := s.ReplaceRollupPoints(ctx, 5*time.Minute, []Point{{MetricName: "bounds", EntityID: entity, Timestamp: base, Value: 1}}); err != nil {
					t.Fatal(err)
				}
			}
			minAligned := int64(math.MinInt64) - int64(math.MinInt64)%300000
			maxAligned := int64(math.MaxInt64) - int64(math.MaxInt64)%300000
			cases := []struct {
				entity        string
				child, parent int64
				missing       bool
			}{
				{"min-missing", math.MinInt64, 0, true},
				{"min-sealed", minAligned + 60000, minAligned, false},
				{"min-unaligned", math.MinInt64 + 100, math.MinInt64, true},
				{"max-missing", math.MaxInt64, 0, true},
				{"max-sealed", math.MaxInt64, maxAligned, false},
				{"max-unaligned", math.MaxInt64, math.MaxInt64 - 1, true},
			}
			update := fmt.Sprintf(`UPDATE %s SET bucket_milli = %s WHERE resolution_id =
				(SELECT id FROM %s WHERE resolution_milli = %s) AND series_id =
				(SELECT id FROM %s WHERE metric_name = %s AND entity_id = %s)`,
				s.tables.rollups, s.dialect.placeholder(1), s.tables.resolutions, s.dialect.placeholder(2),
				s.tables.series, s.dialect.placeholder(3), s.dialect.placeholder(4))
			deleteParent := fmt.Sprintf(`DELETE FROM %s WHERE resolution_id =
				(SELECT id FROM %s WHERE resolution_milli = %s) AND series_id =
				(SELECT id FROM %s WHERE metric_name = %s AND entity_id = %s)`,
				s.tables.rollups, s.tables.resolutions, s.dialect.placeholder(1), s.tables.series,
				s.dialect.placeholder(2), s.dialect.placeholder(3))
			for _, item := range cases {
				if _, err := s.db.ExecContext(ctx, update, item.child, time.Minute.Milliseconds(), "bounds", item.entity); err != nil {
					t.Fatal(err)
				}
				if item.entity == "min-missing" || item.entity == "max-missing" {
					_, err = s.db.ExecContext(ctx, deleteParent, (5 * time.Minute).Milliseconds(), "bounds", item.entity)
				} else {
					_, err = s.db.ExecContext(ctx, update, item.parent, (5 * time.Minute).Milliseconds(), "bounds", item.entity)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			fineID, err := s.resolutionIDFor(ctx, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			coarseID, err := s.resolutionIDFor(ctx, 5*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			query, args := s.coarseRecoveryQuery(ctx, "bounds", fineID, coarseID, 5*time.Minute)
			rows, err := s.reader().QueryContext(ctx, query, args...)
			if err != nil {
				t.Fatal(err)
			}
			children, err := scanStoredRollupsForMaintenance(rows, true, policy.compression())
			_ = rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]int64, len(children))
			for _, child := range children {
				got[child.entityID] = child.bucket
			}
			want := make(map[string]int64)
			for _, item := range cases {
				if item.missing {
					want[item.entity] = item.child
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("missing boundary children = %v, want %v", got, want)
			}
		})
	}
}

// The conflict probe participates in both reads and sealing transactions and
// must use each backend's real placeholders, joins and read-only behavior.
func TestCoarseRecoveryAncestorConflictSQLIntegration(t *testing.T) {
	for _, backend := range []struct {
		name string
		env  string
		cfg  func(string, ...Option) Config
	}{
		{"mysql", "METRIC_MYSQL_DSN", MySQL},
		{"postgres", "METRIC_POSTGRES_DSN", PostgreSQL},
	} {
		t.Run(backend.name, func(t *testing.T) {
			dsn := os.Getenv(backend.env)
			if dsn == "" {
				t.Skip(backend.env + " is not set")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			policy := RollupPolicy{Tiers: []RollupTier{
				{Interval: time.Minute, Retention: 24 * time.Hour},
				{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
				{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
				{Interval: 24 * time.Hour, Retention: 90 * 24 * time.Hour},
			}, Compression: 30}
			cfg := backend.cfg(dsn, WithRollupPolicy(policy), WithMaxOpenConns(1), WithMaxIdleConns(1))
			cfg.TablePrefix = fmt.Sprintf("ac_%d_", time.Now().UnixNano())
			store, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			defer dropIntegrationTables(t, store, cfg.TablePrefix)
			if err := store.CreateMetric(ctx, Definition{Name: "ancestor", RetentionDays: 90}); err != nil {
				t.Fatal(err)
			}
			base := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
			var imported []PersistedRollup
			for _, interval := range []time.Duration{time.Minute, 5 * time.Minute, time.Hour} {
				imported = append(imported, constantTransferRollup("ancestor", "node", base, interval, 10, base))
			}
			if err := store.ImportRollups(ctx, imported); err != nil {
				t.Fatal(err)
			}
			writeRollupPointsAt(t, ctx, store, base.Add(8*time.Minute), Point{
				MetricName: "ancestor", EntityID: "node", Timestamp: base.Add(6*time.Minute + time.Second), Value: 20,
				Tags: map[string]string{"region": "eu"}, Labels: map[string]string{"rack": "r2"},
			})
			query := AggregateQuery{Query: Query{MetricName: "ancestor", EntityID: "node", Start: base, End: base.Add(24*time.Hour - time.Millisecond)}, Aggregation: AggSum, Interval: time.Hour}
			if _, err := store.AggregateRollup(ctx, query, time.Hour); !errors.Is(err, ErrRebuildConflict) {
				t.Fatalf("conflicting read = %v", err)
			}
			if _, err := store.FlushCoarse(ctx, base.Add(2*time.Hour)); !errors.Is(err, ErrRebuildConflict) {
				t.Fatalf("conflicting flush = %v", err)
			}
			rows, err := store.scanRollupRows(ctx, store.reader(), "ancestor", time.Hour)
			if err != nil || len(rows) != 1 || rows[0].bucketData.sum != 40 {
				t.Fatalf("durable ancestor changed: %+v, %v", rows, err)
			}
		})
	}
}

// A tier that has never been written must read as id zero rather than as an
// error, because the recovery pass binds it as the parent id and "no parent
// exists" is exactly what zero matches.
func TestResolutionIDForReportsUnwrittenTierAsZero(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	store, err := Open(ctx, SQLite(filepath.Join(t.TempDir(), "tiers.db"), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.CreateMetric(ctx, Definition{Name: "tiers.metric", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	prepared, err := prepareMetricPoints([]Point{{MetricName: "tiers.metric", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 1}})
	if err != nil {
		t.Fatal(err)
	}
	rebuild := store.writeRawPointsAt(prepared, base.Add(2*time.Minute))
	if err := store.writePreparedHotRollups(ctx, prepared, base.Add(2*time.Minute), rebuild); err != nil {
		t.Fatal(err)
	}

	// The minute tier is durable now; the five-minute parent has only ever
	// existed in memory, so its resolution row has not been written yet.
	minuteID, err := store.resolutionIDFor(ctx, time.Minute)
	if err != nil {
		t.Fatalf("resolve written tier: %v", err)
	}
	if minuteID == 0 {
		t.Fatal("the minute tier was written and must have an id")
	}
	coarseID, err := store.resolutionIDFor(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("resolve unwritten tier: %v", err)
	}
	if coarseID != 0 {
		t.Fatalf("unwritten five-minute tier reported id %d, want 0", coarseID)
	}
}
