package pingrollback

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func scratchRollbackMetricStore(t *testing.T, name string) *metric.Store {
	t.Helper()
	policy := metric.RollupPolicy{Tiers: []metric.RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	s, err := metric.Open(t.Context(), metric.SQLite(filepath.Join(t.TempDir(), name+".db"), metric.WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSplitMetricHistorySeparatesOnlyParkedPingSeries(t *testing.T) {
	ctx := t.Context()
	src, live, archive := scratchRollbackMetricStore(t, "source"), scratchRollbackMetricStore(t, "old"), scratchRollbackMetricStore(t, "archive")
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	names := []string{"ping.latency_ms", "ping.success_latency_ms", "ping.loss", "cpu.usage"}
	for _, name := range names {
		if err := src.UpsertMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatal(err)
		}
	}
	rows := []metric.PersistedRollup{}
	for _, name := range names {
		for i, taskID := range []string{"12", "13"} {
			bucket := base.Add(time.Duration(i) * time.Minute)
			rows = append(rows, metric.PersistedRollup{MetricName: name, EntityID: "node", Tags: map[string]string{"task_id": taskID, "family": "ipv6"}, Labels: map[string]string{"fixture": "one"}, Resolution: time.Minute, Bucket: bucket, Count: 1, Sum: 7, SumSq: 49, Min: 7, Max: 7, FirstValue: 7, LastValue: 7, FirstTime: bucket, LastTime: bucket, CreatedAt: bucket})
		}
	}
	if err := src.ImportRollups(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := splitMetricHistory(ctx, src, live, archive, []uint{12}); err != nil {
		t.Fatal(err)
	}
	read := func(store *metric.Store, name string) []metric.PersistedRollup {
		t.Helper()
		var got []metric.PersistedRollup
		_, err := store.ExportRollups(ctx, name, 1, func(rows []metric.PersistedRollup) error {
			got = append(got, rows...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, name := range names {
		original, oldRows, parkedRows := read(src, name), read(live, name), read(archive, name)
		if len(original) != 2 {
			t.Fatalf("source %s changed: %+v", name, original)
		}
		if name == "cpu.usage" {
			if !reflect.DeepEqual(original, oldRows) || len(parkedRows) != 0 {
				t.Fatalf("non-ping history was split: old=%+v archive=%+v", oldRows, parkedRows)
			}
		} else {
			if len(oldRows) != 1 || len(parkedRows) != 1 || oldRows[0].Tags["task_id"] != "13" || parkedRows[0].Tags["task_id"] != "12" {
				t.Fatalf("%s incorrectly split: old=%+v archive=%+v", name, oldRows, parkedRows)
			}
			byID := map[string]metric.PersistedRollup{}
			for _, row := range original {
				byID[row.Tags["task_id"]] = row
			}
			if !reflect.DeepEqual(byID["13"], oldRows[0]) || !reflect.DeepEqual(byID["12"], parkedRows[0]) {
				t.Fatalf("%s rollup values changed", name)
			}
		}
	}
	// Simulate the retired panel writing and later purging the parked task in
	// its own scratch metrics DB. The separate archive must be unchanged.
	for _, name := range names[:3] {
		before := read(archive, name)
		if err := live.ImportRollups(ctx, before); err != nil {
			t.Fatal(err)
		}
		if _, err := live.DeleteSeries(ctx, metric.Query{MetricName: name, Tags: map[string]string{"task_id": "12"}}); err != nil {
			t.Fatal(err)
		}
		if after := read(archive, name); !reflect.DeepEqual(before, after) {
			t.Fatalf("old store operation changed %s archive", name)
		}
		if got := read(live, name); len(got) != 1 || got[0].Tags["task_id"] != "13" {
			t.Fatalf("old store purge damaged unrelated %s history: %+v", name, got)
		}
	}
}

func TestSplitMetricHistoryRejectsInvalidScratchInputs(t *testing.T) {
	src, live, archive := scratchRollbackMetricStore(t, "source"), scratchRollbackMetricStore(t, "old"), scratchRollbackMetricStore(t, "archive")
	for _, call := range []func() error{
		func() error { return splitMetricHistory(nil, src, live, archive, []uint{12}) },
		func() error { return splitMetricHistory(t.Context(), src, src, archive, []uint{12}) },
		func() error { return splitMetricHistory(t.Context(), src, live, archive, nil) },
		func() error { return splitMetricHistory(t.Context(), src, live, archive, []uint{0}) },
		func() error { return splitMetricHistory(t.Context(), src, live, archive, []uint{12, 12}) },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid quarantine input accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := splitMetricHistory(ctx, src, live, archive, []uint{12}); err == nil {
		t.Fatal("canceled quarantine accepted")
	}
}

func TestMetricQuarantineRejectsPhysicalDatabaseAliasesBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	open := func(path string) *metric.Store {
		t.Helper()
		s, err := metric.Open(t.Context(), metric.SQLite(path))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	path := filepath.Join(dir, "shared.db")
	first, alias, other := open(path), open(path), open(filepath.Join(dir, "other.db"))
	if err := first.UpsertMetric(t.Context(), metric.Definition{Name: "ping.loss", Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	if err := splitMetricHistory(t.Context(), first, other, alias, []uint{12}); err == nil {
		t.Fatal("split accepted separate handles to the same database")
	}
	if err := mergeMetricHistory(t.Context(), first, other, alias, []uint{12}); err == nil {
		t.Fatal("merge accepted separate handles to the same database")
	}
	defs, err := other.ListMetrics(t.Context())
	if err != nil || len(defs) != 0 {
		t.Fatalf("alias rejection occurred after writing: definitions=%v err=%v", defs, err)
	}
}

func TestSplitMetricHistorySeesHotBucketsOnlyAfterCleanClose(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "source.db")
	source, err := metric.Open(ctx, metric.SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.UpsertMetric(ctx, metric.Definition{Name: "ping.loss", Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	if err := source.Write(ctx, metric.Point{MetricName: "ping.loss", EntityID: "node", Timestamp: time.Now().UTC(), Value: 0,
		Tags: map[string]string{"task_id": "12", "family": "ipv6"}}); err != nil {
		t.Fatal(err)
	}
	read := func(store *metric.Store) []metric.PersistedRollup {
		t.Helper()
		var rows []metric.PersistedRollup
		if _, err := store.ExportRollups(ctx, "ping.loss", 100, func(batch []metric.PersistedRollup) error {
			rows = append(rows, batch...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if got := read(source); len(got) != 0 {
		t.Fatalf("fixture did not retain its current-minute bucket in memory: %+v", got)
	}
	firstLive, firstArchive := scratchRollbackMetricStore(t, "first-live"), scratchRollbackMetricStore(t, "first-archive")
	if err := splitMetricHistory(ctx, source, firstLive, firstArchive, []uint{12}); err != nil {
		t.Fatal(err)
	}
	if got := read(firstArchive); len(got) != 0 {
		t.Fatalf("scratch split unexpectedly included the in-memory bucket: %+v", got)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := metric.Open(ctx, metric.SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	secondLive, secondArchive := scratchRollbackMetricStore(t, "second-live"), scratchRollbackMetricStore(t, "second-archive")
	if err := splitMetricHistory(ctx, reopened, secondLive, secondArchive, []uint{12}); err != nil {
		t.Fatal(err)
	}
	if got := read(secondArchive); len(got) != 1 || got[0].Tags["task_id"] != "12" || got[0].Count != 1 {
		t.Fatalf("cleanly closed bucket missing from archive: %+v", got)
	}
}

func TestSplitMetricHistoryAfterCleanClosePreservesOpenCoarseParents(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	policy := metric.RollupPolicy{Tiers: []metric.RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	open := func(name string) *metric.Store {
		t.Helper()
		s, err := metric.Open(ctx, metric.SQLite(filepath.Join(dir, name+".db"), metric.WithRollupPolicy(policy)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	source := open("source")
	if err := source.CreateMetric(ctx, metric.Definition{Name: "ping.loss", Type: metric.TypeGauge, Unit: "ratio", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(10 * time.Second)
	for _, task := range []string{"12", "13"} {
		if err := source.Write(ctx, metric.Point{MetricName: "ping.loss", EntityID: "node", Timestamp: at, Value: 1,
			Tags: map[string]string{"task_id": task}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	source = open("source")
	old, archive := open("old"), open("archive")
	if err := splitMetricHistory(ctx, source, old, archive, []uint{12}); err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*metric.Store{"old": old, "archive": archive} {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		reopened := open(name)
		if written, err := reopened.FlushCoarse(ctx, at.Add(time.Hour)); err != nil || written != 1 {
			t.Fatalf("%s lost its reconstructible coarse parent: written=%d err=%v", name, written, err)
		}
		var parents []metric.PersistedRollup
		if _, err := reopened.ExportRollups(ctx, "ping.loss", 10, func(rows []metric.PersistedRollup) error {
			for _, row := range rows {
				if row.Resolution == 5*time.Minute {
					parents = append(parents, row)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		want := "13"
		if name == "archive" {
			want = "12"
		}
		if len(parents) != 1 || parents[0].Tags["task_id"] != want || parents[0].Count != 1 || parents[0].Sum != 1 {
			t.Fatalf("%s coarse history = %+v", name, parents)
		}
	}
}

func TestScratchRollbackMergeCanReviveExpiredArchivedHistory(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	policy := metric.RollupPolicy{Tiers: []metric.RollupTier{
		{Interval: time.Minute, Retention: time.Hour},
		{Interval: 5 * time.Minute, Retention: 24 * time.Hour},
	}, Compression: 30}
	open := func(name string) *metric.Store {
		t.Helper()
		s, err := metric.Open(ctx, metric.SQLite(filepath.Join(dir, name+".db"), metric.WithRollupPolicy(policy)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	source, old, archive := open("source"), open("old"), open("archive")
	if err := source.CreateMetric(ctx, metric.Definition{Name: "ping.loss", Type: metric.TypeGauge, Unit: "ratio", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(5 * time.Minute).Add(-2 * time.Hour)
	row := metric.PersistedRollup{MetricName: "ping.loss", EntityID: "node", Tags: map[string]string{"task_id": "12"},
		Resolution: 5 * time.Minute, Bucket: at, Count: 1, Sum: 1, SumSq: 1, Min: 1, Max: 1,
		FirstValue: 1, FirstTime: at, LastValue: 1, LastTime: at, CreatedAt: at}
	if err := source.ImportRollups(ctx, []metric.PersistedRollup{row}); err != nil {
		t.Fatal(err)
	}
	if err := splitMetricHistory(ctx, source, old, archive, []uint{12}); err != nil {
		t.Fatal(err)
	}
	merged := open("merged")
	if err := mergeMetricHistory(ctx, old, archive, merged, []uint{12}); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(48 * time.Hour)
	if _, err := merged.CleanupExpired(ctx, future); err != nil {
		t.Fatal(err)
	}
	count := 0
	if _, err := merged.ExportRollups(ctx, "ping.loss", 10, func(rows []metric.PersistedRollup) error {
		count += len(rows)
		return nil
	}); err != nil || count != 0 {
		t.Fatalf("uninterrupted retention fixture did not expire: count=%d err=%v", count, err)
	}
	// The experimental merge is intentionally not a release tool: it has no
	// cutover time or retention policy and therefore revives this old bucket.
	again := open("merge-after-downtime")
	if err := mergeMetricHistory(ctx, old, archive, again, []uint{12}); err != nil {
		t.Fatal(err)
	}
	count = 0
	if _, err := again.ExportRollups(ctx, "ping.loss", 10, func(rows []metric.PersistedRollup) error {
		count += len(rows)
		return nil
	}); err != nil || count != 1 {
		t.Fatalf("fixture did not expose expired-history revival: count=%d err=%v", count, err)
	}
}

func TestSplitMetricHistoryRejectsUnknownPingSchemaAndAmbiguousIDs(t *testing.T) {
	for _, taskID := range []string{"", "012", "12x", "0"} {
		t.Run("id="+taskID, func(t *testing.T) {
			src, live, archive := scratchRollbackMetricStore(t, "source"), scratchRollbackMetricStore(t, "old"), scratchRollbackMetricStore(t, "archive")
			if err := src.UpsertMetric(t.Context(), metric.Definition{Name: "ping.loss", Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
				t.Fatal(err)
			}
			bucket := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
			row := metric.PersistedRollup{MetricName: "ping.loss", EntityID: "node", Tags: map[string]string{"task_id": taskID}, Resolution: time.Minute, Bucket: bucket, Count: 1, Sum: 1, SumSq: 1, Min: 1, Max: 1, FirstValue: 1, LastValue: 1, FirstTime: bucket, LastTime: bucket, CreatedAt: bucket}
			if err := src.ImportRollups(t.Context(), []metric.PersistedRollup{row}); err != nil {
				t.Fatal(err)
			}
			if err := splitMetricHistory(t.Context(), src, live, archive, []uint{12}); err == nil {
				t.Fatal("ambiguous ping task identity was accepted")
			}
		})
	}
	t.Run("unknown metric", func(t *testing.T) {
		src, live, archive := scratchRollbackMetricStore(t, "source"), scratchRollbackMetricStore(t, "old"), scratchRollbackMetricStore(t, "archive")
		if err := src.UpsertMetric(t.Context(), metric.Definition{Name: "ping.future", Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatal(err)
		}
		if err := splitMetricHistory(t.Context(), src, live, archive, []uint{12}); err == nil {
			t.Fatal("unknown ping metric was accepted")
		}
	})
}

func TestMergeMetricHistoryKeepsCurrentDataAndDiscardsParkedPollution(t *testing.T) {
	ctx := t.Context()
	src, old, archive, merged := scratchRollbackMetricStore(t, "source"), scratchRollbackMetricStore(t, "old"), scratchRollbackMetricStore(t, "archive"), scratchRollbackMetricStore(t, "merged")
	for _, name := range []string{"ping.loss", "ping.latency_ms", "cpu.usage"} {
		if err := src.UpsertMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatal(err)
		}
	}
	bucket := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	row := func(name, id string, at time.Time, value float64) metric.PersistedRollup {
		return metric.PersistedRollup{MetricName: name, EntityID: "node", Tags: map[string]string{"task_id": id}, Resolution: time.Minute, Bucket: at, Count: 1, Sum: value, SumSq: value * value, Min: value, Max: value, FirstValue: value, LastValue: value, FirstTime: at, LastTime: at, CreatedAt: at}
	}
	initial := []metric.PersistedRollup{row("ping.loss", "12", bucket, 0), row("ping.latency_ms", "12", bucket, 5), row("ping.loss", "13", bucket, 1), row("cpu.usage", "12", bucket, 42)}
	if err := src.ImportRollups(ctx, initial); err != nil {
		t.Fatal(err)
	}
	if err := splitMetricHistory(ctx, src, old, archive, []uint{12}); err != nil {
		t.Fatal(err)
	}
	// The old panel can overwrite a same-key parked rollup and create another
	// parked bucket. Neither may replace the protected archive when merging.
	pollution := []metric.PersistedRollup{row("ping.loss", "12", bucket, 1), row("ping.loss", "12", bucket.Add(time.Minute), 1), row("ping.loss", "13", bucket.Add(time.Minute), 0), row("ping.loss", "14", bucket, 0)}
	if err := old.ImportRollups(ctx, pollution); err != nil {
		t.Fatal(err)
	}
	if _, err := old.UpdateMetricRetention(ctx, "ping.latency_ms", 0); err != nil {
		t.Fatal(err)
	}
	if err := mergeMetricHistory(ctx, old, archive, merged, []uint{12}); err != nil {
		t.Fatal(err)
	}
	var rows []metric.PersistedRollup
	if _, err := merged.ExportRollups(ctx, "ping.loss", 2, func(batch []metric.PersistedRollup) error { rows = append(rows, batch...); return nil }); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, got := range rows {
		counts[got.Tags["task_id"]]++
		if got.Tags["task_id"] == "12" && (got.Sum != 0 || !got.Bucket.Equal(bucket)) {
			t.Fatalf("parked pollution was merged: %+v", got)
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"12": 1, "13": 2, "14": 1}) {
		t.Fatalf("merged wrong ping history: %+v", rows)
	}
	var latency []metric.PersistedRollup
	if _, err := merged.ExportRollups(ctx, "ping.latency_ms", 10, func(batch []metric.PersistedRollup) error { latency = append(latency, batch...); return nil }); err != nil || len(latency) != 0 {
		t.Fatalf("disabled metric restored: %+v err=%v", latency, err)
	}
	def, err := merged.GetMetric(ctx, "ping.latency_ms")
	if err != nil || def.RetentionDays != 0 {
		t.Fatalf("current retention not preserved: %+v err=%v", def, err)
	}
	var cpu []metric.PersistedRollup
	if _, err := merged.ExportRollups(ctx, "cpu.usage", 10, func(batch []metric.PersistedRollup) error { cpu = append(cpu, batch...); return nil }); err != nil || len(cpu) != 1 || cpu[0].Sum != 42 {
		t.Fatalf("non-ping current data was lost: %+v err=%v", cpu, err)
	}
}

func TestMergeMetricHistoryRejectsUnrelatedArchiveAndDefinitionDrift(t *testing.T) {
	for _, mode := range []string{"unparked", "nonping", "missing_definition", "type_changed", "unit_changed", "retention_changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			old, archive, merged := scratchRollbackMetricStore(t, "old"), scratchRollbackMetricStore(t, "archive"), scratchRollbackMetricStore(t, "merged")
			name := "ping.loss"
			if mode == "nonping" {
				name = "cpu.usage"
			}
			def := metric.Definition{Name: name, Type: metric.TypeGauge, Unit: "unit", RetentionDays: 30}
			if err := archive.UpsertMetric(ctx, def); err != nil {
				t.Fatal(err)
			}
			current := def
			if mode == "type_changed" {
				current.Type = metric.TypeCounter
			}
			if mode == "unit_changed" {
				current.Unit = "different"
			}
			if mode == "retention_changed" {
				current.RetentionDays = 10
			}
			if mode != "missing_definition" {
				if err := old.UpsertMetric(ctx, current); err != nil {
					t.Fatal(err)
				}
			}
			id := "12"
			if mode == "unparked" {
				id = "13"
			}
			bucket := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
			row := metric.PersistedRollup{MetricName: name, EntityID: "node", Tags: map[string]string{"task_id": id}, Resolution: time.Minute, Bucket: bucket, Count: 1, Sum: 1, SumSq: 1, Min: 1, Max: 1, FirstValue: 1, LastValue: 1, FirstTime: bucket, LastTime: bucket, CreatedAt: bucket}
			if err := archive.ImportRollups(ctx, []metric.PersistedRollup{row}); err != nil {
				t.Fatal(err)
			}
			if err := mergeMetricHistory(ctx, old, archive, merged, []uint{12}); err == nil {
				t.Fatalf("accepted %s", mode)
			}
		})
	}
}
