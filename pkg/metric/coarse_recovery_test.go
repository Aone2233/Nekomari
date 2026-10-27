package metric

import (
	"context"
	"path/filepath"
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
	if len(args) != 5 {
		t.Fatalf("recovery statement binds %d arguments, want 5 (metric, fine id, coarse id, coarse ms twice): %v", len(args), args)
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
