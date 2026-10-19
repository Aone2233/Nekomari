package metric

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// benchmarkIngestStore opens an isolated in-memory Store holding one enabled
// definition per benchmarked metric.
func benchmarkIngestStore(b *testing.B, metricNames []string) *Store {
	b.Helper()
	ctx := context.Background()
	store, err := Open(ctx, SQLite("file:bench-write-batch?mode=memory&cache=shared"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	for _, name := range metricNames {
		if err := store.CreateMetric(ctx, Definition{Name: name, Type: TypeGauge, RetentionDays: 30}); err != nil {
			b.Fatalf("create metric %q: %v", name, err)
		}
	}
	return store
}

// benchmarkWriteBatchIngest measures the steady-state ingest path for one
// report flush: 20 metrics x 9 entities = 180 points, the shape a 3 s server
// flush produces. Each iteration advances the batch by 3 s so the raw window
// and the hot rollup tier see the same turnover they do in production.
//
// The tagged variant adds the tags and labels the GPU report series carry, so
// the canonical fingerprint cache is exercised instead of taking the empty-tag
// fast path.
func benchmarkWriteBatchIngest(b *testing.B, tagged bool) {
	const metrics = 20
	const entities = 9
	const stride = 3 * time.Second

	names := make([]string, metrics)
	for i := range names {
		names[i] = fmt.Sprintf("agent.report.bench.%02d", i)
	}
	store := benchmarkIngestStore(b, names)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(stride).Add(-time.Minute)
	points := make([]Point, 0, metrics*entities)
	for i, name := range names {
		for e := 0; e < entities; e++ {
			point := Point{
				MetricName: name,
				EntityID:   fmt.Sprintf("node-%02d", e),
				Timestamp:  base.Add(time.Duration(i*entities+e) * time.Millisecond),
				Value:      float64(i*entities + e),
			}
			if tagged {
				point.Tags = map[string]string{
					"device_index": fmt.Sprintf("%d", e%2),
					"device_name":  "NVIDIA GeForce RTX 4090",
				}
				point.Labels = map[string]string{"metric_kind": "gpu"}
			}
			points = append(points, point)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reuse one batch slice: WriteBatch does not retain it, and copying it
		// inside the timed loop would pollute the allocation count.
		for j := range points {
			points[j].Timestamp = points[j].Timestamp.Add(stride)
		}
		if err := store.WriteBatch(ctx, points); err != nil {
			b.Fatalf("write batch: %v", err)
		}
	}
}

// BenchmarkWriteBatchIngest is the untagged report-flush benchmark that matches
// the 180-point workload from the 2026-10-18 memory audit.
func BenchmarkWriteBatchIngest(b *testing.B) {
	benchmarkWriteBatchIngest(b, false)
}

// BenchmarkWriteBatchIngestTagged is the same flush with GPU-style tags and
// labels on every point, so repeated canonical fingerprints are served from the
// fingerprint cache.
func BenchmarkWriteBatchIngestTagged(b *testing.B) {
	benchmarkWriteBatchIngest(b, true)
}

// BenchmarkTagsFingerprint measures one tag-set fingerprint: the empty fast
// path, one repeated canonical set, and a wider GPU tag set.
func BenchmarkTagsFingerprint(b *testing.B) {
	sets := []struct {
		name string
		tags map[string]string
	}{
		{"empty", nil},
		{"single", map[string]string{"task_id": "7"}},
		{"gpu", map[string]string{"device_index": "0", "device_name": "NVIDIA GeForce RTX 4090"}},
	}
	for _, set := range sets {
		b.Run(set.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, err := tagsFingerprint(set.tags); err != nil {
					b.Fatalf("fingerprint: %v", err)
				}
			}
		})
	}
}
