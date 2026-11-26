package metricstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestClearRecordsDrainsQueueAndFencesTransitions(t *testing.T) {
	for _, kind := range []string{"ping", "system"} {
		t.Run(kind, func(t *testing.T) {
			prepareRecoveryTest(t)
			path := recoveryStorePath(t, "active.db")
			if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
				t.Fatal(err)
			}
			if err := InitializeStore(); err != nil {
				t.Fatal(err)
			}
			StartReportBatcher()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := StopReportBatcher(ctx); err != nil {
					t.Errorf("stop batcher: %v", err)
				}
			})
			now := time.Now().UTC()
			if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 12, Client: "node", Time: now, Value: 7}); err != nil {
				t.Fatal(err)
			}
			if _, err := WriteReport(t.Context(), v2.Report{UUID: "node", UpdatedAt: now, CPU: v2.CPUReport{Usage: 42}}); err != nil {
				t.Fatal(err)
			}
			clear := DeleteAllRecords
			name, kept := MetricCPU, MetricPingLoss
			if kind == "ping" {
				clear, name, kept = DeleteAllPingRecords, MetricPingLoss, MetricCPU
			}
			if err := WithPingIdentityOperation(t.Context(), func() error {
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
				if err := clear(ctx); !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("clear did not wait for task deletion: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := clear(t.Context()); err != nil {
				t.Fatal(err)
			}
			query := func(name string) []metric.Point {
				t.Helper()
				points, err := GetStore().Query(t.Context(), metric.Query{MetricName: name, EntityID: "node", Start: now.Add(-time.Minute), End: now.Add(time.Minute)})
				if err != nil {
					t.Fatal(err)
				}
				return points
			}
			if got, remaining := query(name), query(kept); len(got) != 0 || len(remaining) != 1 {
				t.Fatalf("clear lost queued or unrelated data: removed=%v kept=%v", got, remaining)
			}
			if err := Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := countReservationRollups(t, GetStore(), name); len(got) != 0 {
				t.Fatalf("reload restored cleared rollups: %v", got)
			}
			if keptRows := countReservationRollups(t, GetStore(), kept); len(keptRows) != 1 {
				t.Fatalf("reload lost unrelated rollups: %v", keptRows)
			}
		})
	}
}

func TestClearPingRecordsDropsPendingCoarseBuckets(t *testing.T) {
	prepareRecoveryTest(t)
	path := recoveryStorePath(t, "active.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := now.Truncate(5 * time.Minute).Add(time.Minute)
	if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 12, Client: "node", Time: base, Value: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetStore().Flush(t.Context(), base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := countReservationRollups(t, GetStore(), MetricPingLoss); got["12"] == 0 {
		t.Fatal("fixture did not produce a durable fine bucket")
	}
	if err := DeleteAllPingRecords(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := GetStore().FlushCoarse(t.Context(), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := countReservationRollups(t, GetStore(), MetricPingLoss); len(got) != 0 {
		t.Fatalf("coarse flush resurrected cleared observations: %v", got)
	}
}

func TestDeleteEntityDrainsQueueAndFencesStoreTransition(t *testing.T) {
	prepareRecoveryTest(t)
	path := recoveryStorePath(t, "active.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	StartReportBatcher()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop batcher: %v", err)
		}
	})
	now := time.Now().UTC()
	for _, id := range []string{"deleted-node", "kept-node"} {
		if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 12, Client: id, Time: now, Value: 7}); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteReport(t.Context(), v2.Report{UUID: id, UpdatedAt: now, CPU: v2.CPUReport{Usage: 42}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := WithPingIdentityOperation(t.Context(), func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if err := DeleteEntity(ctx, "deleted-node"); !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("node cleanup crossed transition: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteEntity(t.Context(), "deleted-node"); err != nil {
		t.Fatal(err)
	}
	query := func(name, id string) []metric.Point {
		t.Helper()
		points, err := GetStore().Query(t.Context(), metric.Query{MetricName: name, EntityID: id, Start: now.Add(-time.Minute), End: now.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return points
	}
	for _, name := range []string{MetricPingLoss, MetricCPU} {
		if got, kept := query(name, "deleted-node"), query(name, "kept-node"); len(got) != 0 || len(kept) != 1 {
			t.Fatalf("%s cleanup removed=%v kept=%v", name, got, kept)
		}
	}
	if err := Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{MetricPingLoss, MetricCPU} {
		var rows []metric.PersistedRollup
		if _, err := GetStore().ExportRollups(t.Context(), name, 10, func(batch []metric.PersistedRollup) error {
			rows = append(rows, batch...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].EntityID != "kept-node" {
			t.Fatalf("%s reload resurrected or lost rollups: %+v", name, rows)
		}
	}
}

func TestReportAdmissionsFenceTransitionDrain(t *testing.T) {
	prepareRecoveryTest(t)
	path := recoveryStorePath(t, "active.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	StartReportBatcher()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop batcher: %v", err)
		}
	})
	now := time.Now().UTC()
	ping := models.PingRecord{TaskId: 42, Client: "node", Time: now, Value: 7}
	if err := WritePingRecord(t.Context(), ping); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReport(t.Context(), v2.Report{UUID: "node", UpdatedAt: now, CPU: v2.CPUReport{Usage: 42}}); err != nil {
		t.Fatal(err)
	}
	// The acquired transition has already drained accepted work but has not
	// cleared it. New admissions cannot slip into the queue in this interval.
	if err := acquirePingHistoryTransition(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	defer releasePingHistoryTransition()
	for _, write := range []func(context.Context) error{
		func(ctx context.Context) error { return WritePingRecord(ctx, ping) },
		func(ctx context.Context) error {
			_, err := WriteReport(ctx, v2.Report{UUID: "node", UpdatedAt: now, CPU: v2.CPUReport{Usage: 42}})
			return err
		},
	} {
		if err := write(t.Context()); !errors.Is(err, metric.ErrWriteUnavailable) {
			t.Fatalf("write crossed transition admission fence: %v", err)
		}
	}
	if err := WritePingRecordSync(t.Context(), ping); !errors.Is(err, metric.ErrWriteUnavailable) {
		t.Fatalf("pinned result crossed transition admission fence: %v", err)
	}
	if err := deleteMetricRecords(t.Context(), GetStore(), append(append([]string{}, pingMetricNames...), recordMetricNames...)); err != nil {
		t.Fatal(err)
	}
}
