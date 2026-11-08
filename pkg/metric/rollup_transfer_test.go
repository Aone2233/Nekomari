package metric

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestImportRollupsRejectsInvalidBucketBoundariesAtomically(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: time.Hour}}}
	store := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "boundaries.db"), policy)
	if err := store.CreateMetric(ctx, Definition{Name: "boundaries", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	valid := constantTransferRollup("boundaries", "node", base, time.Minute, 7, base)
	cases := map[string]func(*PersistedRollup){
		"misaligned bucket":     func(r *PersistedRollup) { r.Bucket = base.Add(time.Second) },
		"submillisecond bucket": func(r *PersistedRollup) { r.Bucket = base.Add(time.Nanosecond) },
		"first before bucket":   func(r *PersistedRollup) { r.FirstTime = base.Add(-time.Second) },
		"last at bucket end":    func(r *PersistedRollup) { r.LastTime = base.Add(time.Minute) },
		"invalid metric UTF-8":  func(r *PersistedRollup) { r.MetricName = string([]byte{0xff}) },
		"invalid entity UTF-8":  func(r *PersistedRollup) { r.EntityID = string([]byte{0xff}) },
		"time range overflow": func(r *PersistedRollup) {
			r.Bucket = time.UnixMilli(math.MaxInt64 - math.MaxInt64%time.Minute.Milliseconds()).UTC()
			r.FirstTime, r.LastTime = r.Bucket, r.Bucket
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			invalid := valid
			invalid.EntityID = "invalid"
			mutate(&invalid)
			if err := store.ImportRollups(ctx, []PersistedRollup{valid, invalid}); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid import = %v", err)
			}
			rows, _ := exportAllTestRollups(t, store, "boundaries", 10)
			if len(rows) != 0 {
				t.Fatalf("invalid batch partially imported: %+v", rows)
			}
		})
	}
	if err := store.ImportRollups(ctx, []PersistedRollup{valid}); err != nil {
		t.Fatalf("valid import = %v", err)
	}
}

func TestImportRollupsRoundTripsBeforeUnixEpoch(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	source := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "source.db"), policy)
	target := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "target.db"), policy)
	for _, store := range []*Store{source, target} {
		if err := store.CreateMetric(ctx, Definition{Name: "pre-epoch", RetentionDays: 30}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Unix(-30, 0).UTC()
	if err := source.ReplaceRollupPoints(ctx, 5*time.Minute, []Point{{MetricName: "pre-epoch", EntityID: "node", Timestamp: at, Value: 7}}); err != nil {
		t.Fatal(err)
	}
	rows, _ := exportAllTestRollups(t, source, "pre-epoch", 10)
	if len(rows) != 2 {
		t.Fatalf("exported %d rows, want minute and five-minute buckets", len(rows))
	}
	if err := target.ImportRollups(ctx, rows); err != nil {
		t.Fatalf("import pre-epoch rollups: %v", err)
	}
	got, _ := exportAllTestRollups(t, target, "pre-epoch", 10)
	if !reflect.DeepEqual(got, rows) {
		t.Fatalf("pre-epoch import changed aggregates: got %+v, want %+v", got, rows)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestImportRollupsPreservingExistingRefusesConflictAtomically(t *testing.T) {
	ctx := t.Context()
	policy := RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	s := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "preserve.db"), policy)
	if err := s.CreateMetric(ctx, Definition{Name: "sample", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	original := constantTransferRollup("sample", "node", base, time.Minute, 7, base)
	if err := s.ImportRollups(ctx, []PersistedRollup{original}); err != nil {
		t.Fatal(err)
	}
	before, _ := exportAllTestRollups(t, s, "sample", 10)
	same := original
	same.CreatedAt = base.Add(time.Hour)
	if err := s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{same}); err != nil {
		t.Fatalf("same aggregate should be idempotent: %v", err)
	}
	after, _ := exportAllTestRollups(t, s, "sample", 10)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("idempotent import rewrote target: before=%+v after=%+v", before, after)
	}
	newRow := constantTransferRollup("sample", "another", base, time.Minute, 5, base)
	conflict := constantTransferRollup("sample", "node", base, time.Minute, 9, base)
	if err := s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{newRow, conflict}); !errors.Is(err, ErrImportConflict) {
		t.Fatalf("conflicting import = %v", err)
	}
	after, _ = exportAllTestRollups(t, s, "sample", 10)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("conflicting batch changed target: before=%+v after=%+v", before, after)
	}
	if err := s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{newRow, newRow}); !errors.Is(err, ErrImportConflict) {
		t.Fatalf("duplicate import = %v", err)
	}
	if err := s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{newRow}); err != nil {
		t.Fatalf("new rollup import = %v", err)
	}
	variable := constantTransferRollup("sample", "variable", base, time.Minute, 1, base)
	variable.Count, variable.Sum, variable.SumSq, variable.Min, variable.Max = 2, 4, 10, 1, 3
	variable.FirstValue, variable.LastValue = 1, 3
	variable.FirstTime, variable.LastTime = base.Add(time.Second), base.Add(2*time.Second)
	digest := NewTDigest(30)
	digest.Add(1, 1)
	digest.Add(3, 1)
	variable.Digest = digest.Encode()
	if err := s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{variable}); err != nil {
		t.Fatalf("variable rollup import = %v", err)
	}
	if err := s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{variable}); err != nil {
		t.Fatalf("variable rollup retry = %v", err)
	}
	changedDigest := NewTDigest(30)
	changedDigest.Add(1, 1)
	changedDigest.Add(2, 1)
	changedDigest.Add(3, 1)
	variable.Digest = changedDigest.Encode()
	if err := s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{variable}); !errors.Is(err, ErrImportConflict) {
		t.Fatalf("changed percentile sketch = %v", err)
	}
}

func TestRollupTransferRoundTrip(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 10 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 50 * time.Hour},
			{Interval: time.Hour, Retention: 600 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	source := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "source.db"), policy)
	target := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "target.db"), policy)

	definition := Definition{
		Name: "load.average", Description: "system load", Type: TypeGauge,
		Unit: "load", RetentionDays: 365, Metadata: map[string]string{"scope": "system"},
	}
	for _, store := range []*Store{source, target} {
		if err := store.UpsertMetric(ctx, definition); err != nil {
			t.Fatalf("upsert metric: %v", err)
		}
	}

	base := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	variableDigest := NewTDigest(policy.Compression)
	for _, value := range []float64{1, 4, 9} {
		variableDigest.Add(value, 1)
	}
	constantDigest := NewTDigest(policy.Compression)
	constantDigest.Add(7, 2)
	createdAt := base.Add(48 * time.Hour)
	input := []PersistedRollup{
		{
			MetricName: definition.Name, EntityID: "node-a",
			Tags: map[string]string{"core": "0", "region": "ap"}, Labels: map[string]string{"rack": "r1"},
			Resolution: 30 * time.Second, Bucket: base, Count: 2, Sum: 14, SumSq: 98,
			Min: 7, Max: 7, FirstValue: 7, FirstTime: base.Add(time.Second),
			LastValue: 7, LastTime: base.Add(20 * time.Second), Digest: constantDigest.Encode(), CreatedAt: createdAt,
		},
		{
			MetricName: definition.Name, EntityID: "node-a",
			Tags: map[string]string{"core": "0", "region": "ap"}, Labels: map[string]string{"rack": "r1"},
			Resolution: time.Minute, Bucket: base, Count: 3, Sum: 14, SumSq: 98,
			Min: 1, Max: 9, FirstValue: 1, FirstTime: base.Add(time.Second),
			LastValue: 9, LastTime: base.Add(40 * time.Second), Digest: variableDigest.Encode(), CreatedAt: createdAt,
		},
		constantTransferRollup(definition.Name, "node-a", base, 5*time.Minute, 11, createdAt),
		constantTransferRollup(definition.Name, "node-b", base, time.Hour, 12, createdAt),
		constantTransferRollup(definition.Name, "node-b", base, 24*time.Hour, 13, createdAt),
	}
	if err := source.ImportRollups(ctx, input); err != nil {
		t.Fatalf("import source rollups: %v", err)
	}

	exported, batches := exportAllTestRollups(t, source, definition.Name, 2)
	if len(exported) != len(input) {
		t.Fatalf("exported rollups = %d, want %d", len(exported), len(input))
	}
	if !reflect.DeepEqual(batches, []int{2, 2, 1}) {
		t.Fatalf("export batch sizes = %v, want [2 2 1]", batches)
	}
	for _, rollup := range exported {
		if rollup.Min == rollup.Max && len(rollup.Digest) != 0 {
			t.Fatalf("constant rollup at %s retained a digest", rollup.Resolution)
		}
	}
	if len(exported[1].Digest) == 0 {
		t.Fatal("non-constant minute rollup did not preserve its digest")
	}
	exportedDigest, err := DecodeTDigest(exported[1].Digest)
	if err != nil || math.Abs(exportedDigest.Quantile(0.95)-variableDigest.Quantile(0.95)) > 1e-12 {
		t.Fatalf("non-constant minute rollup changed digest semantics: err=%v", err)
	}

	if err := target.ImportRollups(ctx, exported); err != nil {
		t.Fatalf("import target rollups: %v", err)
	}
	if err := target.ImportRollups(ctx, exported); err != nil {
		t.Fatalf("repeat target import: %v", err)
	}
	roundTrip, _ := exportAllTestRollups(t, target, definition.Name, 20)
	if !reflect.DeepEqual(roundTrip, exported) {
		t.Fatalf("round-trip rollups differ:\n got: %#v\nwant: %#v", roundTrip, exported)
	}

	digest, err := DecodeTDigest(variableDigest.Encode())
	if err != nil {
		t.Fatalf("decode expected digest: %v", err)
	}
	points, err := target.AggregateRollup(ctx, AggregateQuery{
		Query: Query{
			MetricName: definition.Name, EntityID: "node-a", Tags: map[string]string{"core": "0", "region": "ap"},
			Start: base, End: base.Add(time.Minute - time.Millisecond),
		},
		Aggregation: AggP95, Interval: time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("query transferred percentile: %v", err)
	}
	if len(points) != 1 || points[0].Count != 3 || math.Abs(points[0].Value-digest.Quantile(0.95)) > 1e-12 {
		t.Fatalf("transferred percentile = %#v, want count 3 value %v", points, digest.Quantile(0.95))
	}
}

func openRollupTransferTestStore(t *testing.T, path string, policy RollupPolicy) *Store {
	t.Helper()
	store, err := Open(context.Background(), SQLite(path,
		WithMaxOpenConns(1),
		WithMaxIdleConns(1),
		WithRollupPolicy(policy),
	))
	if err != nil {
		t.Fatalf("open rollup transfer store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func constantTransferRollup(metricName, entityID string, bucket time.Time, resolution time.Duration, value float64, createdAt time.Time) PersistedRollup {
	return PersistedRollup{
		MetricName: metricName, EntityID: entityID,
		Tags: map[string]string{"region": "eu"}, Labels: map[string]string{"rack": "r2"},
		Resolution: resolution, Bucket: bucket, Count: 4, Sum: value * 4, SumSq: value * value * 4,
		Min: value, Max: value, FirstValue: value, FirstTime: bucket.Add(time.Second),
		LastValue: value, LastTime: bucket.Add(2 * time.Second), CreatedAt: createdAt,
	}
}

func exportAllTestRollups(t *testing.T, store *Store, metricName string, batchSize int) ([]PersistedRollup, []int) {
	t.Helper()
	var (
		rollups []PersistedRollup
		batches []int
	)
	total, err := store.ExportRollups(context.Background(), metricName, batchSize, func(batch []PersistedRollup) error {
		batches = append(batches, len(batch))
		rollups = append(rollups, batch...)
		return nil
	})
	if err != nil {
		t.Fatalf("export rollups: %v", err)
	}
	if total != int64(len(rollups)) {
		t.Fatalf("export total = %d, want %d", total, len(rollups))
	}
	return rollups, batches
}
