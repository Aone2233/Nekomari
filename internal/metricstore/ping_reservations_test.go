package metricstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func reservationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.PingTask{}, &models.PingTaskDeletion{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func reservationStore(t *testing.T, path string) *metric.Store {
	t.Helper()
	s, err := metric.Open(t.Context(), metric.SQLite(path, metric.WithRollupPolicy(defaultRollupPolicy())))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, name := range append(append([]string{}, pingMetricNames...), MetricCPU) {
		if err := s.UpsertMetric(t.Context(), metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func reservationRollup(name, task string) metric.PersistedRollup {
	bucket := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	return metric.PersistedRollup{
		MetricName: name, EntityID: "node", Tags: map[string]string{"task_id": task},
		Resolution: time.Minute, Bucket: bucket, Count: 1, Sum: 1, SumSq: 1, Min: 1, Max: 1,
		FirstValue: 1, FirstTime: bucket, LastValue: 1, LastTime: bucket, CreatedAt: bucket,
	}
}

func countReservationRollups(t *testing.T, s *metric.Store, name string) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	_, err := s.ExportRollups(t.Context(), name, 1, func(batch []metric.PersistedRollup) error {
		for _, row := range batch {
			counts[row.Tags["task_id"]]++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestReservedPingHistoryCleanupAndMigration(t *testing.T) {
	ctx := t.Context()
	db := reservationDB(t)
	if err := db.Create(&models.PingTaskDeletion{TaskID: 12}).Error; err != nil {
		t.Fatal(err)
	}
	src := reservationStore(t, filepath.Join(t.TempDir(), "source.db"))
	dst := reservationStore(t, filepath.Join(t.TempDir(), "destination.db"))
	var rows []metric.PersistedRollup
	for _, name := range pingMetricNames {
		rows = append(rows, reservationRollup(name, "12"), reservationRollup(name, "13"))
	}
	rows = append(rows, reservationRollup(MetricCPU, "12"))
	for _, s := range []*metric.Store{src, dst} {
		if err := s.ImportRollups(ctx, rows); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := reservedPingTaskIDs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanReservedPingHistory(ctx, dst, ids); err != nil {
		t.Fatal(err)
	}
	if count, err := migrateBetweenStores(ctx, src, dst, ids, nil); err != nil || count != 4 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	for _, name := range pingMetricNames {
		if got := countReservationRollups(t, dst, name); got["12"] != 0 || got["13"] != 1 {
			t.Fatalf("%s resurrected deleted history or lost live history: %v", name, got)
		}
	}
	if got := countReservationRollups(t, dst, MetricCPU); got["12"] != 1 {
		t.Fatalf("unrelated metric was removed: %v", got)
	}
	if got := countReservationRollups(t, src, MetricPingLoss); got["12"] != 1 {
		t.Fatalf("source was modified: %v", got)
	}
}

func TestReservedPingHistoryConflictFailsClosed(t *testing.T) {
	db := reservationDB(t)
	s := reservationStore(t, filepath.Join(t.TempDir(), "metrics.db"))
	if err := db.Create(&models.PingTaskDeletion{TaskID: 12}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PingTask{Id: 12, Name: "conflict", Target: "example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ImportRollups(t.Context(), []metric.PersistedRollup{reservationRollup(MetricPingLoss, "12")}); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileReservedPingHistory(t.Context(), s, db); err == nil {
		t.Fatal("a live reserved task was accepted")
	}
	if got := countReservationRollups(t, s, MetricPingLoss); got["12"] != 1 {
		t.Fatalf("conflict deleted potentially live history: %v", got)
	}
}

func TestRecoverStoreCleansReservedHistoryBeforeActivation(t *testing.T) {
	prepareRecoveryTest(t)
	db := config.Database()
	if err := db.Create(&models.PingTaskDeletion{TaskID: 12}).Error; err != nil {
		t.Fatal(err)
	}
	path := recoveryStorePath(t, "old.db")
	old := reservationStore(t, path)
	if err := old.ImportRollups(t.Context(), []metric.PersistedRollup{
		reservationRollup(MetricPingLoss, "12"), reservationRollup(MetricPingLoss, "13"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverStore(t.Context(), &MetricStoreConfig{Driver: "sqlite", DSN: path}); err != nil {
		t.Fatal(err)
	}
	if got := countReservationRollups(t, GetStore(), MetricPingLoss); got["12"] != 0 || got["13"] != 1 {
		t.Fatalf("activated stale history: %v", got)
	}
}

func TestReloadSameStoreDoesNotResurrectReservedHotBucket(t *testing.T) {
	prepareRecoveryTest(t)
	path := recoveryStorePath(t, "active.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, task := range []string{"12", "13"} {
		if err := GetStore().Write(t.Context(), metric.Point{MetricName: MetricPingLoss, EntityID: "node", Timestamp: now, Value: 1, Tags: map[string]string{"task_id": task}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Database().Create(&models.PingTaskDeletion{TaskID: 12}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := countReservationRollups(t, GetStore(), MetricPingLoss); got["12"] != 0 || got["13"] != 1 {
		t.Fatalf("reload resurrected deleted history or lost unrelated history: %v", got)
	}
}

func TestPingWriterWaitsForStoreReplacement(t *testing.T) {
	s := openPingTestStore(t)
	if err := storeOperations.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	err := writePingRecords(ctx, []models.PingRecord{{TaskId: 12, Client: "node", Time: time.Now(), Value: 1}})
	storeOperations.Release()
	if err == nil || ctx.Err() == nil {
		t.Fatalf("writer crossed replacement lease: %v", err)
	}
	points, err := s.Query(context.Background(), metric.Query{MetricName: MetricPingLoss, EntityID: "node", Tags: map[string]string{"task_id": fmt.Sprint(12)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
	if err != nil || len(points) != 0 {
		t.Fatalf("writer left points behind: %v %v", points, err)
	}
}

func TestStoreMigrationFencesDeletionAndDrainsQueuedPing(t *testing.T) {
	prepareRecoveryTest(t)
	path := recoveryStorePath(t, "active.db")
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := metric.Open(t.Context(), metric.SQLite(sourcePath, metric.WithRollupPolicy(defaultRollupPolicy())))
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureBuiltinMetricDefinitions(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
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
	if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 42, Client: "node", Time: time.Now().UTC(), Value: 7}); err != nil {
		t.Fatal(err)
	}
	// A failed attempt must not take the store lease or drain the queue while a
	// task deletion is in its identity-critical section.
	if err := WithPingIdentityOperation(t.Context(), func() error {
		if err := StartStoreMigration("sqlite", sourcePath); !errors.Is(err, ErrStoreBusy) {
			return fmt.Errorf("migration crossed deletion fence: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := StartStoreMigration("sqlite", sourcePath); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for IsStoreMigrationRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if IsStoreMigrationRunning() || GetStoreMigrationProgress().Status != "completed" {
		t.Fatalf("migration did not complete: %+v", GetStoreMigrationProgress())
	}
	points, err := GetStore().Query(t.Context(), metric.Query{MetricName: MetricPingLoss, EntityID: "node", Tags: map[string]string{"task_id": "42"}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
	if err != nil || len(points) != 1 {
		t.Fatalf("accepted queued ping was not drained: %+v err=%v", points, err)
	}
}

func TestStoreTransitionsFenceDeletionAndDrainReservedQueue(t *testing.T) {
	for _, operation := range []string{"reload", "recover", "close"} {
		t.Run(operation, func(t *testing.T) {
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
			for _, id := range []uint{12, 13} {
				if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: id, Client: "node", Time: time.Now().UTC(), Value: 7}); err != nil {
					t.Fatal(err)
				}
			}

			done := make(chan error, 1)
			err := WithPingIdentityOperation(t.Context(), func() error {
				// Simulate an interrupted deletion with accepted records still queued.
				if err := config.Database().Create(&models.PingTaskDeletion{TaskID: 12}).Error; err != nil {
					return err
				}
				go func() {
					switch operation {
					case "reload":
						done <- Reload(t.Context())
					case "recover":
						done <- RecoverStore(t.Context(), &MetricStoreConfig{Driver: "sqlite", DSN: path})
					case "close":
						done <- CloseStoreContext(t.Context())
					}
				}()
				deadline := time.Now().Add(5 * time.Second)
				for pingIdentityOperations.TryAcquireShared() {
					pingIdentityOperations.ReleaseShared()
					if time.Now().After(deadline) {
						return errors.New("transition did not wait for deletion fence")
					}
					time.Sleep(time.Millisecond)
				}
				select {
				case err := <-done:
					return fmt.Errorf("transition crossed deletion fence: %v", err)
				default:
					return nil
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}

			// Closing persists the active hot buckets; reopening also checks
			// that no old hot bucket resurrects on an aliased destination.
			if operation != "close" {
				if err := CloseStoreContext(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			reopened := reservationStore(t, path)
			for _, name := range pingMetricNames {
				if got := countReservationRollups(t, reopened, name); got["12"] != 0 || got["13"] != 1 {
					t.Fatalf("%s resurrected reserved history or lost unrelated history: %v", name, got)
				}
			}
		})
	}
}

func TestStoppedBatcherCanStillCloseStoreAndPersistAcceptedPing(t *testing.T) {
	prepareRecoveryTest(t)
	path := filepath.Join(t.TempDir(), "shutdown.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	StartReportBatcher()
	t.Cleanup(func() { _ = StopReportBatcher(context.Background()) })
	if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 42, Client: "node", Time: time.Now().UTC(), Value: 7}); err != nil {
		t.Fatal(err)
	}
	if err := StopReportBatcher(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := CloseStoreContext(t.Context()); err != nil {
		t.Fatalf("store close after a successful batcher stop: %v", err)
	}
	reopened := reservationStore(t, path)
	if got := countReservationRollups(t, reopened, MetricPingLoss); got["42"] != 1 {
		t.Fatalf("accepted ping was not persisted on shutdown: %v", got)
	}
}

func TestQuiescedBatcherFencesAllReportWritesUntilStoreClose(t *testing.T) {
	prepareRecoveryTest(t)
	path := filepath.Join(t.TempDir(), "quiesced.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	if err := StartReportBatcher(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reportClosing.Store(false)
		_ = StopReportBatcher(context.Background())
	})
	at := time.Now().UTC()
	ping := models.PingRecord{TaskId: 42, Client: "node", Time: at, Value: 7}
	report := v2.Report{UUID: "node", UpdatedAt: at, CPU: v2.CPUReport{Usage: 42}}
	if err := WritePingRecord(t.Context(), ping); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}

	// An admission already underway must leave the gate before the worker can
	// stop; a queued exclusive waiter also rejects later admissions.
	if err := reportAdmissions.AcquireShared(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- QuiesceReportBatcher(t.Context()) }()
	deadline := time.Now().Add(3 * time.Second)
	for reportAdmissions.TryAcquireShared() {
		reportAdmissions.ReleaseShared()
		if time.Now().After(deadline) {
			reportAdmissions.ReleaseShared()
			t.Fatal("quiesce did not wait for the active admission")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-result:
		reportAdmissions.ReleaseShared()
		t.Fatalf("quiesce returned while an admission remained active: %v", err)
	default:
	}
	reportAdmissions.ReleaseShared()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := StartReportBatcher(); !errors.Is(err, ErrReportBatchStopped) {
		t.Fatalf("quiesced batcher restarted: %v", err)
	}
	if _, err := WriteReport(t.Context(), report); !errors.Is(err, ErrReportBatchStopped) {
		t.Fatalf("report admitted after quiescence: %v", err)
	}
	if err := WritePingRecord(t.Context(), ping); !errors.Is(err, ErrReportBatchStopped) {
		t.Fatalf("queued ping admitted after quiescence: %v", err)
	}
	if err := WritePingRecordSync(t.Context(), ping); !errors.Is(err, ErrReportBatchStopped) {
		t.Fatalf("synchronous ping admitted after quiescence: %v", err)
	}
	if err := QuiesceReportBatcher(t.Context()); err != nil {
		t.Fatalf("repeated quiescence: %v", err)
	}
	if err := CloseStoreContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened := reservationStore(t, path)
	if got := countReservationRollups(t, reopened, MetricPingLoss); got["42"] != 1 {
		t.Fatalf("accepted ping was not persisted on quiescent close: %v", got)
	}
	if got := countReservationRollups(t, reopened, MetricCPU); got[""] != 1 {
		t.Fatalf("accepted report was not persisted on quiescent close: %v", got)
	}
}

func TestQuiesceFailureKeepsAcceptedQueueAndRejectsNewWrites(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	if err := StartReportBatcher(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reportClosing.Store(false)
		_ = StopReportBatcher(ctx)
	})
	at := time.Now().UTC()
	ping := models.PingRecord{TaskId: 44, Client: "node", Time: at, Value: 11}
	if err := WritePingRecord(ctx, ping); err != nil {
		t.Fatal(err)
	}
	if err := storeOperations.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	err := QuiesceReportBatcher(stopCtx)
	cancel()
	storeOperations.Release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked quiescence = %v, want deadline", err)
	}
	if err := WritePingRecord(ctx, ping); !errors.Is(err, ErrReportBatchStopped) {
		t.Fatalf("failed quiescence admitted another record: %v", err)
	}
	if err := QuiesceReportBatcher(ctx); err != nil {
		t.Fatalf("retry quiescence: %v", err)
	}
	assertMetricValues(t, s, MetricPingLatency, ping.Client, at.Add(-time.Second), at.Add(time.Minute), []float64{11})
}

func TestStoreTransitionsAbortIfQueueCannotDrain(t *testing.T) {
	for _, operation := range []string{"reload", "recover", "close"} {
		t.Run(operation, func(t *testing.T) {
			prepareRecoveryTest(t)
			path := recoveryStorePath(t, "active.db")
			if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: path}); err != nil {
				t.Fatal(err)
			}
			if err := InitializeStore(); err != nil {
				t.Fatal(err)
			}
			active := GetStore()
			StartReportBatcher()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := StopReportBatcher(ctx); err != nil {
					t.Errorf("stop batcher: %v", err)
				}
			})
			if err := WritePingRecord(t.Context(), models.PingRecord{TaskId: 42, Client: "node", Time: time.Now().UTC(), Value: 7}); err != nil {
				t.Fatal(err)
			}
			if err := storeOperations.Acquire(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			var err error
			switch operation {
			case "reload":
				err = Reload(ctx)
			case "recover":
				err = RecoverStore(ctx, &MetricStoreConfig{Driver: "sqlite", DSN: path})
			case "close":
				err = CloseStoreContext(ctx)
			}
			cancel()
			storeOperations.Release()
			if !errors.Is(err, context.DeadlineExceeded) || GetStore() != active {
				t.Fatalf("failed drain replaced or closed active store: err=%v", err)
			}
			if err := FlushReportBatch(t.Context()); err != nil {
				t.Fatal(err)
			}
			points, err := active.Query(t.Context(), metric.Query{MetricName: MetricPingLoss, EntityID: "node", Tags: map[string]string{"task_id": "42"}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
			if err != nil || len(points) != 1 {
				t.Fatalf("failed drain lost queued observation: points=%v err=%v", points, err)
			}
		})
	}
}
