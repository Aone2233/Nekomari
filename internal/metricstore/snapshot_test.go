package metricstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestSnapshotStoreDrainsAcceptedReportsAndFencesAdmissions(t *testing.T) {
	prepareRecoveryTest(t)
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: filepath.Join(t.TempDir(), "live.db")}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(context.Background()); err != nil {
			t.Errorf("stop batcher: %v", err)
		}
	})
	at := time.Now().UTC().Add(30 * time.Second)
	if _, err := WriteReport(t.Context(), v2.Report{UUID: "node", UpdatedAt: at, CPU: v2.CPUReport{Usage: 42}}); err != nil {
		t.Fatal(err)
	}
	if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 12, Client: "node", Time: at, Value: 7}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.db")
	err := WithSnapshotStore(t.Context(), func(store *metric.Store) error {
		if _, err := WriteReport(t.Context(), v2.Report{UUID: "later", UpdatedAt: at, CPU: v2.CPUReport{Usage: 1}}); !errors.Is(err, metric.ErrWriteUnavailable) {
			t.Errorf("report admitted during snapshot: %v", err)
		}
		if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 12, Client: "later", Time: at, Value: 1}); !errors.Is(err, metric.ErrWriteUnavailable) {
			t.Errorf("ping admitted during snapshot: %v", err)
		}
		return store.SnapshotSQLite(t.Context(), path)
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := metric.Open(t.Context(), metric.SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for name, want := range map[string]float64{MetricCPU: 42, MetricPingLoss: 0} {
		var rows []metric.PersistedRollup
		if _, err := snapshot.ExportRollups(t.Context(), name, 100, func(batch []metric.PersistedRollup) error {
			rows = append(rows, batch...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.EntityID == "node" && row.Resolution == time.Minute && row.Count == 1 && row.Sum == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("queued %s not present in snapshot: %+v", name, rows)
		}
	}
	if _, err := WriteReport(t.Context(), v2.Report{UUID: "later", UpdatedAt: at, CPU: v2.CPUReport{Usage: 1}}); err != nil {
		t.Fatalf("admission did not resume after snapshot: %v", err)
	}
}

func TestSnapshotStorePropagatesDrainFailureAndReleasesFence(t *testing.T) {
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(context.Background()); err != nil {
			t.Errorf("stop batcher: %v", err)
		}
	})
	at := time.Now().UTC().Add(-2 * time.Minute)
	if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 12, Client: "node", Time: at, Value: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(t.Context(), `DROP TABLE metric_rollups`); err != nil {
		t.Fatal(err)
	}
	called := false
	err := WithSnapshotStore(t.Context(), func(*metric.Store) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("failed drain proceeded to snapshot: called=%v err=%v", called, err)
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := WithSnapshotStore(t.Context(), func(*metric.Store) error { return nil }); err != nil {
		t.Fatalf("retry drain: %v", err)
	}
	points, err := s.Query(t.Context(), metric.Query{MetricName: MetricPingLoss, EntityID: "node", Start: at.Add(-time.Minute), End: at.Add(time.Minute)})
	if err != nil || len(points) != 1 {
		t.Fatalf("accepted record lost during failed drain: %v, %v", points, err)
	}
	var rows []metric.PersistedRollup
	if _, err := s.ExportRollups(t.Context(), MetricPingLoss, 100, func(batch []metric.PersistedRollup) error {
		rows = append(rows, batch...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Count != 1 {
		t.Fatalf("retry duplicated an accepted record: %+v", rows)
	}
}

func TestStoreReplacementRetainsActiveStoreWhenHotPersistenceFails(t *testing.T) {
	prepareRecoveryTest(t)
	path := filepath.Join(t.TempDir(), "active.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	active := GetStore()
	at := time.Now().UTC().Add(30 * time.Second)
	if err := active.Write(t.Context(), metric.Point{MetricName: MetricCPU, EntityID: "node", Timestamp: at, Value: 42}); err != nil {
		t.Fatal(err)
	}
	if _, err := active.ExecContext(t.Context(), `DROP TABLE metric_rollups`); err != nil {
		t.Fatal(err)
	}
	if err := Reload(t.Context()); err == nil {
		t.Fatal("reload replaced a store whose hot persistence failed")
	}
	if GetStore() != active {
		t.Fatal("failed reload replaced the active store")
	}
	if err := CloseStoreContext(t.Context()); err == nil {
		t.Fatal("close unexpectedly succeeded with broken metric schema")
	}
	if GetStore() != active {
		t.Fatal("failed close discarded the active store")
	}
	if err := active.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := Reload(t.Context()); err != nil {
		t.Fatalf("retry reload: %v", err)
	}
	var rows []metric.PersistedRollup
	if _, err := GetStore().ExportRollups(t.Context(), MetricCPU, 100, func(batch []metric.PersistedRollup) error {
		rows = append(rows, batch...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Count != 1 || rows[0].Sum != 42 {
		t.Fatalf("reload retry lost or duplicated hot data: %+v", rows)
	}
}
