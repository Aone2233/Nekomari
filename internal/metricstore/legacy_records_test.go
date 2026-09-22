package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/protocol/v2"
)

// GetRecordsByTime now answers with one batched rollup query instead of
// "one Series per metric, per node" (17 queries per node). Batching across nodes
// only stays correct if the batch preserves each entity's own buckets, so this
// pins that: two nodes writing into the same time bucket must come back as two
// records with their own values, never merged into one.
func TestGetRecordsByTimeKeepsEntitiesSeparate(t *testing.T) {
	type nodeWant struct {
		uuid        string
		cpu         float32
		ram         int64
		connections int
	}
	type nodeGot struct {
		cpu         float32
		ram         int64
		connections int
	}

	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatalf("create metric definitions: %v", err)
	}

	storeMu.Lock()
	oldStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		storeMu.Unlock()
		_ = s.Close()
	}()

	now := time.Now().UTC().Truncate(time.Minute)
	ts := now.Add(-time.Hour)
	want := []nodeWant{
		{uuid: "node-a", cpu: 42.5, ram: 123456, connections: 321},
		{uuid: "node-b", cpu: 7.5, ram: 654321, connections: 12},
	}
	for _, node := range want {
		if _, err := WriteReport(ctx, v2.Report{
			UUID:      node.uuid,
			UpdatedAt: ts,
			CPU:       v2.CPUReport{Usage: float64(node.cpu)},
			Ram:       v2.RamReport{Used: node.ram, Total: 999999},
			Connections: v2.ConnectionsReport{
				TCP: node.connections,
				UDP: 1,
			},
		}); err != nil {
			t.Fatalf("write report for %s: %v", node.uuid, err)
		}
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact raw into rollup: %v", err)
	}

	got, err := GetRecordsByTime(ctx, ts.Add(-time.Minute), now)
	if err != nil {
		t.Fatalf("get records: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d records (one per node), got %d: %+v", len(want), len(got), got)
	}

	byClient := make(map[string]nodeGot, len(got))
	for _, rec := range got {
		byClient[rec.Client] = nodeGot{cpu: rec.Cpu, ram: rec.Ram, connections: rec.Connections}
	}
	for _, node := range want {
		rec, ok := byClient[node.uuid]
		if !ok {
			t.Fatalf("no record for %s (entities merged into one record?)", node.uuid)
		}
		if rec.cpu != node.cpu {
			t.Errorf("%s cpu = %v, want %v", node.uuid, rec.cpu, node.cpu)
		}
		if rec.ram != node.ram {
			t.Errorf("%s ram = %v, want %v", node.uuid, rec.ram, node.ram)
		}
		if rec.connections != node.connections {
			t.Errorf("%s connections = %v, want %v", node.uuid, rec.connections, node.connections)
		}
	}
}

// The single-entity path goes through the same batch helper, so it must still
// return that node's record when the batch is asked for exactly one entity.
func TestGetRecordsByClientAndTimeStillReturnsOneEntity(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatalf("create metric definitions: %v", err)
	}

	storeMu.Lock()
	oldStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		storeMu.Unlock()
		_ = s.Close()
	}()

	now := time.Now().UTC().Truncate(time.Minute)
	ts := now.Add(-time.Hour)
	if _, err := WriteReport(ctx, v2.Report{
		UUID:      "node-solo",
		UpdatedAt: ts,
		CPU:       v2.CPUReport{Usage: 11.5},
		Ram:       v2.RamReport{Used: 4242, Total: 999999},
	}); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact raw into rollup: %v", err)
	}

	got, err := GetRecordsByClientAndTime(ctx, "node-solo", ts.Add(-time.Minute), now)
	if err != nil {
		t.Fatalf("get records: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d: %+v", len(got), got)
	}
	if got[0].Client != "node-solo" || got[0].Cpu != 11.5 || got[0].Ram != 4242 {
		t.Fatalf("unexpected record: %+v", got[0])
	}
}
