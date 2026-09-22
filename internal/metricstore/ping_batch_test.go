package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// The live-status poll used to run one Series scan per node. It now asks for every
// node in one SeriesBatch, so the batched answer has to be identical to the
// per-node one: same records, same per-node grouping, same newest-first order.
// If the batch ever merged entities or dropped the tags, the ping statistics in
// the panel would silently change meaning.
func TestGetPingRecordsBatchMatchesPerNodeQueries(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer s.Close()

	for _, name := range []string{MetricPingLatency, MetricPingLoss} {
		if err := s.UpsertMetric(ctx, metric.Definition{
			Name: name, Type: metric.TypeGauge, RetentionDays: 30,
		}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	storeMu.Lock()
	oldStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		storeMu.Unlock()
	}()

	// Two nodes, two tasks, both protocols, several buckets: enough to catch
	// entity merging, tag loss and ordering differences at once.
	records := []models.PingRecord{
		{Client: "node-a", TaskId: 9, PingType: "icmp", Value: 12, Time: now.Add(-3 * time.Minute)},
		{Client: "node-a", TaskId: 9, PingType: "tcp", Value: 15, Time: now.Add(-3 * time.Minute)},
		{Client: "node-a", TaskId: 9, PingType: "icmp", Value: 20, Time: now.Add(-time.Minute)},
		{Client: "node-a", TaskId: 11, PingType: "icmp", Role: "reference", Value: 3, Time: now.Add(-time.Minute)},
		{Client: "node-b", TaskId: 9, PingType: "icmp", Value: 7, Time: now.Add(-2 * time.Minute)},
		{Client: "node-b", TaskId: 11, PingType: "icmp", Value: 9, Time: now.Add(-2 * time.Minute)},
	}
	if err := writePingRecords(ctx, records); err != nil {
		t.Fatalf("write ping records: %v", err)
	}
	if _, err := s.Compact(ctx, now.Add(time.Minute)); err != nil {
		t.Fatalf("compact: %v", err)
	}

	start := now.Add(-time.Hour)
	end := now.Add(time.Minute)
	batch, err := GetPingRecordsBatch(ctx, []string{"node-a", "node-b"}, -1, start, end)
	if err != nil {
		t.Fatalf("batch query: %v", err)
	}

	for _, uuid := range []string{"node-a", "node-b"} {
		want, err := GetPingRecords(ctx, uuid, -1, start, end)
		if err != nil {
			t.Fatalf("single query for %s: %v", uuid, err)
		}
		got := batch[uuid]
		if len(got) != len(want) {
			t.Fatalf("%s: batch returned %d records, per-node query returned %d\n batch=%+v\n want=%+v",
				uuid, len(got), len(want), got, want)
		}
		for i := range want {
			// PingRecord embeds a PingTask, so it is not comparable as a struct.
			if got[i].Client != want[i].Client || got[i].TaskId != want[i].TaskId ||
				got[i].PingType != want[i].PingType || got[i].Role != want[i].Role ||
				got[i].Value != want[i].Value || !got[i].Time.Equal(want[i].Time) {
				t.Errorf("%s record %d differs:\n batch=%+v\n want =%+v", uuid, i, got[i], want[i])
			}
		}
		// Newest first, like the per-node path.
		for i := 1; i < len(got); i++ {
			if got[i].Time.After(got[i-1].Time) {
				t.Errorf("%s: records are not newest-first: %v then %v", uuid, got[i-1].Time, got[i].Time)
			}
		}
	}

	// A node with no data is simply absent, not an error.
	empty, err := GetPingRecordsBatch(ctx, []string{"node-absent"}, -1, start, end)
	if err != nil {
		t.Fatalf("batch query for absent node: %v", err)
	}
	if len(empty["node-absent"]) != 0 {
		t.Fatalf("expected no records for node-absent, got %+v", empty["node-absent"])
	}
}
