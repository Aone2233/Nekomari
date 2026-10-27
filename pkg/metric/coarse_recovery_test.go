package metric

import (
	"context"
	"fmt"
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
			if (driver == DriverMySQL) != strings.Contains(query, " DIV ") {
				t.Fatalf("incorrect integer-division dialect: %s", query)
			}
		})
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
