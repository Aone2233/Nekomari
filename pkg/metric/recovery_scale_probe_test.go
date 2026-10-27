package metric

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestRecoveryStartupProbe builds a production-shaped metrics database,
// optionally freezes its planner statistics in a stale state, and then measures
// how long Open takes to run the boot-time recovery pass.
//
// The default shape comes from the live OC424 metrics store rather than from
// guesswork: 27 metric definitions, 607 series, one label set, four rollup tiers
// weighted like the real table, ~612k rollup rows in a ~212 MB database, and
// t-digest blobs averaging ~233 bytes (max 1832). Size is what makes the
// recovery query's access path matter, so a shape without digests measures the
// wrong thing.
//
// The fixture self-checks that the pass finds nothing to repair, so the measured
// cost is the scan itself — the steady state a restored archive hits before the
// first new sample arrives — and not a repair of a broken fixture.
//
// It is gated because the fixture is far too large for the ordinary suite: set
// NEKOMARI_RECOVERY_PROBE=1 to run it.
//
//	NEKOMARI_RECOVERY_PROBE=1 go test ./pkg/metric -run TestRecoveryStartupProbe -v -timeout 25m
func TestRecoveryStartupProbe(t *testing.T) {
	if os.Getenv("NEKOMARI_RECOVERY_PROBE") == "" {
		t.Skip("set NEKOMARI_RECOVERY_PROBE=1 to build a production-shaped metrics database")
	}
	shape := probeShape{
		metrics:     envInt(t, "PROBE_METRICS", 27),
		series:      envInt(t, "PROBE_SERIES_PER_METRIC", 22),
		minute:      envInt(t, "PROBE_MINUTE_BUCKETS", 500),
		five:        envInt(t, "PROBE_FIVE_MINUTE_BUCKETS", 200),
		fifteen:     envInt(t, "PROBE_FIFTEEN_MINUTE_BUCKETS", 100),
		hour:        envInt(t, "PROBE_HOUR_BUCKETS", 40),
		digest:      envInt(t, "PROBE_DIGEST_SAMPLES", 2000),
		compression: float64(envInt(t, "PROBE_COMPRESSION", 30)),
	}
	analyze := envInt(t, "PROBE_ANALYZE", 0) == 1
	deadline := time.Duration(envInt(t, "PROBE_DEADLINE_SECONDS", 600)) * time.Second

	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
		{Interval: 15 * time.Minute, Retention: 30 * 24 * time.Hour},
		{Interval: time.Hour, Retention: 90 * 24 * time.Hour},
	}, Compression: shape.compression}

	path := filepath.Join(t.TempDir(), "probe.db")
	ctx := context.Background()

	schema, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	for n := 0; n < shape.metrics; n++ {
		if err := schema.CreateMetric(ctx, Definition{Name: fmt.Sprintf("probe.metric%02d", n), RetentionDays: 90}); err != nil {
			_ = schema.Close()
			t.Fatalf("create metric: %v", err)
		}
	}
	if err := schema.Close(); err != nil {
		t.Fatalf("close schema store: %v", err)
	}

	blob, inserted, err := loadProbeShape(ctx, path, shape, analyze)
	if err != nil {
		t.Fatalf("load rollups: %v", err)
	}
	dbSize := fileSize(t, path)

	// The point of the fixture is an expensive scan over healthy data. If the
	// parents are misaligned the pass becomes a repair, and the timing would
	// measure the wrong thing.
	pending := countPendingRecovery(t, path, policy)
	if pending != 0 {
		t.Fatalf("fixture is inconsistent: %d children have no recorded parent", pending)
	}

	started := time.Now()
	opened, err := Open(withDeadline(t, ctx, deadline), SQLite(path, WithRollupPolicy(policy)))
	elapsed := time.Since(started)
	if err != nil {
		t.Logf("Open failed after %s: %v", elapsed, err)
	}
	if opened != nil {
		_ = opened.Close()
	}

	plan := explainRecovery(t, path, policy)
	fmt.Printf("PROBE metrics=%d series=%d rows=%d db_mb=%.1f blob_bytes=%d pending=%d analyze=%v elapsed_ms=%d err=%v\n",
		shape.metrics, shape.metrics*shape.series, inserted, float64(dbSize)/(1<<20), len(blob), pending, analyze, elapsed.Milliseconds(), err)
	for _, line := range plan {
		fmt.Printf("PROBE_PLAN %s\n", line)
	}

	// The same fixture, the same scan, both statements. Each is run twice so
	// the second pass shows the warm-cache cost rather than a first-touch
	// artefact of whichever ran first.
	scanStore := probeStore(t, path, policy)
	for pass := 1; pass <= 2; pass++ {
		legacyElapsed, legacyRows := timeRecoveryScan(t, scanStore, policy, shape.metrics, true)
		newElapsed, newRows := timeRecoveryScan(t, scanStore, policy, shape.metrics, false)
		fmt.Printf("PROBE_SCAN pass=%d legacy_ms=%d legacy_rows=%d new_ms=%d new_rows=%d\n",
			pass, legacyElapsed.Milliseconds(), legacyRows, newElapsed.Milliseconds(), newRows)
	}
	t.Logf("plan=%v", plan)
}

// legacyCoarseRecoveryQuery is the pre-fix recovery statement, kept verbatim
// (including the resolution join that let the planner drive from the resolution
// index) so the probe can time the scan it used to perform on the same fixture.
// It is a measurement artifact, not code that ships.
func legacyCoarseRecoveryQuery(ctx context.Context, s *Store, metricName string, fine, coarse time.Duration) (string, []any) {
	_ = ctx
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
	return query, []any{metricName, fine.Milliseconds(), coarse.Milliseconds()}
}

// timeRecoveryScan runs the recovery scan for every metric and tier the way
// Open does, and returns how long it took and how many rows came back.
func timeRecoveryScan(t *testing.T, store *Store, policy RollupPolicy, metrics int, legacy bool) (time.Duration, int) {
	t.Helper()
	ctx := context.Background()
	started := time.Now()
	rowsSeen := 0
	for m := 0; m < metrics; m++ {
		name := fmt.Sprintf("probe.metric%02d", m)
		for i := 1; i < len(policy.Tiers); i++ {
			fine, coarse := policy.Tiers[i-1].Interval, policy.Tiers[i].Interval
			var query string
			var args []any
			if legacy {
				query, args = legacyCoarseRecoveryQuery(ctx, store, name, fine, coarse)
			} else {
				fineID, err := store.resolutionIDFor(ctx, fine)
				if err != nil {
					t.Fatalf("resolve fine tier: %v", err)
				}
				if fineID == 0 {
					continue
				}
				coarseID, err := store.resolutionIDFor(ctx, coarse)
				if err != nil {
					t.Fatalf("resolve coarse tier: %v", err)
				}
				query, args = store.coarseRecoveryQuery(ctx, name, fineID, coarseID, coarse)
			}
			rows, err := store.db.QueryContext(ctx, query, args...)
			if err != nil {
				t.Fatalf("recovery scan: %v", err)
			}
			for rows.Next() {
				rowsSeen++
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatalf("recovery scan rows: %v", err)
			}
			rows.Close()
		}
	}
	return time.Since(started), rowsSeen
}

type probeShape struct {
	metrics     int
	series      int
	minute      int
	five        int
	fifteen     int
	hour        int
	digest      int
	compression float64
}

func envInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q is not an integer: %v", key, raw, err)
	}
	return value
}

func withDeadline(t *testing.T, ctx context.Context, d time.Duration) context.Context {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, d)
	t.Cleanup(cancel)
	return bounded
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// probeDigestBlob encodes a real t-digest so fixture rows carry the same kind of
// blob a live store holds, including its size.
func probeDigestBlob(compression float64, samples int) []byte {
	digest := NewTDigest(compression)
	for i := 0; i < samples; i++ {
		digest.Add(float64(i), 1)
	}
	return encodeStoredTDigest(digest)
}

// loadProbeShape writes every tier a healthy production store holds: minute rows
// for the recent window and the sealed parents those minutes already produced,
// aligned to the tier boundaries the recovery pass compares against.
func loadProbeShape(ctx context.Context, path string, shape probeShape, analyze bool) ([]byte, int, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=OFF", "PRAGMA cache_size=-65536"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return nil, 0, err
		}
	}

	blob := probeDigestBlob(shape.compression, shape.digest)
	tiers := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
	resolutions := make(map[int64]int64, len(tiers))
	for _, tier := range tiers {
		if _, err := db.ExecContext(ctx, `INSERT INTO metric_resolutions (resolution_milli) VALUES (?) ON CONFLICT(resolution_milli) DO NOTHING`, tier.Milliseconds()); err != nil {
			return nil, 0, err
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT id, resolution_milli FROM metric_resolutions`)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var id, milli int64
		if err := rows.Scan(&id, &milli); err != nil {
			rows.Close()
			return nil, 0, err
		}
		resolutions[milli] = id
	}
	rows.Close()

	if _, err := db.ExecContext(ctx, `INSERT INTO metric_label_sets (labels_hash, labels) VALUES ('h_label', '{}') ON CONFLICT(labels_hash) DO NOTHING`); err != nil {
		return nil, 0, err
	}
	var labelID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM metric_label_sets WHERE labels_hash='h_label'`).Scan(&labelID); err != nil {
		return nil, 0, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	insertSeries, err := tx.PrepareContext(ctx, `INSERT INTO metric_series (metric_name, entity_id, tags_hash, tags) VALUES (?, ?, ?, '{}')`)
	if err != nil {
		return nil, 0, err
	}
	defer insertSeries.Close()
	insertRollup, err := tx.PrepareContext(ctx, `INSERT INTO metric_rollups
		(series_id, resolution_id, label_id, bucket_milli, count, sum, sum_sq, min_val, max_val,
		 first_val, first_ts_milli, last_val, last_ts_milli, digest, created_at_milli)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, 0, err
	}
	defer insertRollup.Close()

	now := time.Now().UTC().Truncate(time.Hour)
	created := now.UnixMilli()
	inserted := 0
	emit := func(seriesID, resolutionID, bucket, value int64) error {
		// min != max, so the row carries the encoded sketch rather than the
		// constant-bucket shorthand.
		_, err := insertRollup.ExecContext(ctx, seriesID, resolutionID, labelID, bucket,
			float64(value), float64(value*value), float64(value), float64(value+1),
			float64(value), bucket, float64(value), bucket, blob, created)
		inserted++
		return err
	}
	// Parents cover more history than their children so every child bucket has
	// its parent present, which is what a healthy store looks like.
	windows := []struct {
		tier  time.Duration
		count int
	}{
		{time.Minute, shape.minute},
		{5 * time.Minute, shape.five},
		{15 * time.Minute, shape.fifteen},
		{time.Hour, shape.hour},
	}

	for m := 0; m < shape.metrics; m++ {
		metricName := fmt.Sprintf("probe.metric%02d", m)
		for n := 0; n < shape.series; n++ {
			res, err := insertSeries.ExecContext(ctx, metricName, fmt.Sprintf("node-%04d", n), fmt.Sprintf("h_tags_%04d_%02d", n, m))
			if err != nil {
				return nil, inserted, err
			}
			seriesID, err := res.LastInsertId()
			if err != nil {
				return nil, inserted, err
			}
			for _, window := range windows {
				step := window.tier.Milliseconds()
				base := (now.UnixMilli() / step) * step
				for k := 0; k < window.count; k++ {
					bucket := base - int64(k)*step
					if err := emit(seriesID, resolutions[window.tier.Milliseconds()], bucket, int64(k)); err != nil {
						return nil, inserted, err
					}
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, inserted, err
	}
	if analyze {
		if _, err := db.ExecContext(ctx, "ANALYZE"); err != nil {
			return nil, inserted, err
		}
	}
	return blob, inserted, nil
}

// countPendingRecovery reports how many durable children the boot-time pass
// would have to repair, using the shipped statement itself.
func countPendingRecovery(t *testing.T, path string, policy RollupPolicy) int {
	t.Helper()
	store := probeStore(t, path, policy)
	ctx := context.Background()
	total := 0
	for _, name := range []string{"probe.metric00", "probe.metric13", "probe.metric26"} {
		for i := 1; i < len(policy.Tiers); i++ {
			fineID, err := store.resolutionIDFor(ctx, policy.Tiers[i-1].Interval)
			if err != nil {
				t.Fatalf("resolve fine tier: %v", err)
			}
			coarseID, err := store.resolutionIDFor(ctx, policy.Tiers[i].Interval)
			if err != nil {
				t.Fatalf("resolve coarse tier: %v", err)
			}
			if fineID == 0 {
				continue
			}
			query, args := store.coarseRecoveryQuery(ctx, name, fineID, coarseID, policy.Tiers[i].Interval)
			rows, err := store.db.QueryContext(ctx, query, args...)
			if err != nil {
				t.Fatalf("count pending: %v", err)
			}
			for rows.Next() {
				total++
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatalf("count pending rows: %v", err)
			}
			rows.Close()
		}
	}
	return total
}

// explainRecovery prints the plan the backend chose for the recovery statement
// that Open runs, so a slow startup can be attributed to a plan rather than
// guessed at.
func explainRecovery(t *testing.T, path string, policy RollupPolicy) []string {
	t.Helper()
	store := probeStore(t, path, policy)
	ctx := context.Background()
	out := make([]string, 0, 12)
	for i := 1; i < len(policy.Tiers); i++ {
		fineID, err := store.resolutionIDFor(ctx, policy.Tiers[i-1].Interval)
		if err != nil {
			t.Fatalf("resolve fine tier: %v", err)
		}
		coarseID, err := store.resolutionIDFor(ctx, policy.Tiers[i].Interval)
		if err != nil {
			t.Fatalf("resolve coarse tier: %v", err)
		}
		query, args := store.coarseRecoveryQuery(ctx, "probe.metric00", fineID, coarseID, policy.Tiers[i].Interval)
		rows, err := store.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			out = append(out, fmt.Sprintf("tier%d explain error: %v", i, err))
			continue
		}
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				rows.Close()
				t.Fatalf("scan plan: %v", err)
			}
			out = append(out, fmt.Sprintf("tier%d %s->%s | %s", i,
				policy.Tiers[i-1].Interval, policy.Tiers[i].Interval, detail))
		}
		rows.Close()
	}
	if encoded, err := json.Marshal(out); err == nil {
		fmt.Printf("PROBE_PLAN_JSON %s\n", encoded)
	}
	return out
}

// probeStore assembles a read-only Store over the fixture, without running any of
// the startup work that is being measured.
func probeStore(t *testing.T, path string, policy RollupPolicy) *Store {
	t.Helper()
	cfg := SQLite(path, WithRollupPolicy(policy))
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Store{
		cfg:     cfg,
		dialect: newDialect(DriverSQLite),
		tables: tables{
			definitions: tableName(cfg.TablePrefix, "definitions"),
			points:      tableName(cfg.TablePrefix, "points"),
			series:      tableName(cfg.TablePrefix, "series"),
			resolutions: tableName(cfg.TablePrefix, "resolutions"),
			labels:      tableName(cfg.TablePrefix, "label_sets"),
			rollups:     tableName(cfg.TablePrefix, "rollups"),
			watermarks:  tableName(cfg.TablePrefix, "compaction_watermarks"),
			state:       tableName(cfg.TablePrefix, "store_state"),
		},
		db: db,
	}
}
