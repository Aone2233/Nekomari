package tasks

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/Aone2233/nekomari/pkg/metric"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPingDeletionMainDBFailurePreservesMetrics(t *testing.T) {
	db := pinnedTaskDB(t)
	task := models.PingTask{Name: "blocked", Type: "tcp", Target: "example.test:80", Interval: 60}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_ping_delete BEFORE DELETE ON ping_tasks BEGIN SELECT RAISE(ABORT, 'blocked'); END`).Error; err != nil {
		t.Fatal(err)
	}
	called := false
	err := deletePingTasksWith(t.Context(), db, []uint{task.Id}, func(context.Context) error { called = true; return nil },
		func(context.Context, []uint) error { called = true; return nil }, func() error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("failed SQL delete ran cleanup: called=%v err=%v", called, err)
	}
	var got models.PingTask
	var count int64
	if err := db.Take(&got, task.Id).Error; err != nil || got.Name != task.Name {
		t.Fatalf("failed SQL delete changed task: %+v err=%v", got, err)
	}
	if err := db.Model(&models.PingTaskDeletion{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed SQL delete committed tombstone: count=%d err=%v", count, err)
	}
}

func TestPingDeletionPendingSurvivesAndRetries(t *testing.T) {
	for _, failure := range []string{"flush", "metrics", "reload"} {
		t.Run(failure, func(t *testing.T) {
			db := pinnedTaskDB(t)
			task := models.PingTask{Name: "pending", Type: "tcp", Target: "example.test:80", Interval: 60}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			cause := errors.New("injected failure")
			var flushCalls, deleteCalls, reloadCalls int
			flush := func(context.Context) error {
				flushCalls++
				if failure == "flush" {
					return cause
				}
				return nil
			}
			remove := func(_ context.Context, ids []uint) error {
				deleteCalls++
				if len(ids) != 1 || ids[0] != task.Id {
					t.Fatalf("cleanup IDs=%v", ids)
				}
				if failure == "metrics" {
					return cause
				}
				return nil
			}
			reload := func() error {
				reloadCalls++
				if failure == "reload" {
					return cause
				}
				return nil
			}
			err := deletePingTasksWith(t.Context(), db, []uint{task.Id}, flush, remove, reload)
			var pending *PingDeletionPendingError
			if !errors.As(err, &pending) || !errors.Is(err, cause) || reloadCalls != 1 || flushCalls != 1 {
				t.Fatalf("delete=%v flush=%d remove=%d reload=%d", err, flushCalls, deleteCalls, reloadCalls)
			}
			if err := db.Take(&models.PingTask{}, task.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("committed delete left task: %v", err)
			}
			var tombstone models.PingTaskDeletion
			if err := db.Take(&tombstone, "task_id = ?", task.Id).Error; err != nil {
				t.Fatal(err)
			}
			if failure == "reload" {
				if tombstone.CleanedAt == nil {
					t.Fatal("successful cleanup did not finish the tombstone")
				}
				return
			}
			if tombstone.CleanedAt != nil || (failure == "flush" && deleteCalls != 0) {
				t.Fatalf("pending cleanup was finished or removed metrics: tombstone=%+v calls=%d", tombstone, deleteCalls)
			}
			if err := assertPingTaskIDAvailable(db, task.Id); err == nil {
				t.Fatal("task ID with pending cleanup was not reserved")
			}
			cleaned, err := reconcilePingDeletionsWith(t.Context(), db, func(context.Context) error { return nil }, func(_ context.Context, ids []uint) error {
				if len(ids) != 1 || ids[0] != task.Id {
					t.Fatalf("retry IDs=%v", ids)
				}
				return nil
			})
			if err != nil || cleaned != 1 {
				t.Fatalf("retry cleaned=%d err=%v", cleaned, err)
			}
			if err := db.Take(&tombstone, "task_id = ?", task.Id).Error; err != nil || tombstone.CleanedAt == nil {
				t.Fatalf("retry did not finish tombstone: %+v err=%v", tombstone, err)
			}
			if err := assertPingTaskIDAvailable(db, task.Id); err == nil {
				t.Fatal("completed cleanup allowed the deleted task ID to be reused")
			}
		})
	}
}

func TestPingDeletionRetryDoesNotStarveLaterTasks(t *testing.T) {
	db := pinnedTaskDB(t)
	const blocked = uint(1)
	if err := db.Create(&models.PingTask{Id: blocked, Name: "reused", Type: "tcp", Target: "example.test:80", Interval: 60}).Error; err != nil {
		t.Fatal(err)
	}
	for id := uint(1); id <= pingDeletionBatchSize+2; id++ {
		if err := db.Create(&models.PingTaskDeletion{TaskID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var removed []uint
	flush := func(context.Context) error { return nil }
	remove := func(_ context.Context, ids []uint) error {
		if len(ids) != 1 || ids[0] == blocked {
			return fmt.Errorf("unsafe cleanup: %v", ids)
		}
		removed = append(removed, ids[0])
		return nil
	}
	if cleaned, err := reconcilePingDeletionsWith(t.Context(), db, flush, remove); cleaned != pingDeletionBatchSize-1 || err == nil {
		t.Fatalf("first batch cleaned=%d err=%v", cleaned, err)
	}
	if cleaned, err := reconcilePingDeletionsWith(t.Context(), db, flush, remove); cleaned != 2 || err == nil {
		t.Fatalf("second batch cleaned=%d err=%v", cleaned, err)
	}
	if len(removed) != pingDeletionBatchSize+1 {
		t.Fatalf("later tasks were starved: removed=%v", removed)
	}
	var tombstone models.PingTaskDeletion
	if err := db.Take(&tombstone, "task_id = ?", blocked).Error; err != nil || tombstone.CleanedAt != nil || tombstone.LastAttemptAt == nil {
		t.Fatalf("blocked tombstone is not observable: %+v err=%v", tombstone, err)
	}
}

func TestPingDeletionCrashRecoveryAcrossSQLiteStores(t *testing.T) {
	for _, stopAt := range []string{"main-commit", "flush", "partial-metrics", "metrics", "complete"} {
		t.Run(stopAt, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			mainPath := filepath.Join(dir, "main.db")
			metricsPath := filepath.Join(dir, "metrics.db")
			openMain := func() *gorm.DB {
				t.Helper()
				db, err := gorm.Open(sqlite.Open(mainPath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.AutoMigrate(&models.PingTask{}, &models.PingTaskDeletion{}); err != nil {
					t.Fatal(err)
				}
				return db
			}
			openMetrics := func() *metric.Store {
				t.Helper()
				store, err := metric.Open(ctx, metric.SQLite(metricsPath))
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{metricstore.MetricPingLatency, metricstore.MetricPingLoss, metricstore.MetricPingSuccessLatency} {
					if err := store.UpsertMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
						t.Fatal(err)
					}
				}
				return store
			}
			closeMain := func(db *gorm.DB) {
				t.Helper()
				sqlDB, err := db.DB()
				if err != nil {
					t.Fatal(err)
				}
				if err := sqlDB.Close(); err != nil {
					t.Fatal(err)
				}
			}
			db, store := openMain(), openMetrics()
			task := models.PingTask{Name: "deleted", Type: "tcp", Target: "example.test:80", Interval: 60}
			other := models.PingTask{Name: "keep", Type: "tcp", Target: "example.test:80", Interval: 60}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&other).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			names := []string{metricstore.MetricPingLatency, metricstore.MetricPingLoss, metricstore.MetricPingSuccessLatency}
			for _, name := range names {
				for _, id := range []uint{task.Id, other.Id} {
					if err := store.Write(ctx, metric.Point{MetricName: name, EntityID: "node", Timestamp: now,
						Value: 1, Tags: map[string]string{"task_id": fmt.Sprint(id)}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			removeNames := func(ids []uint, metrics []string) error {
				for _, id := range ids {
					for _, name := range metrics {
						if _, err := store.DeleteSeries(ctx, metric.Query{MetricName: name, Tags: map[string]string{"task_id": fmt.Sprint(id)}}); err != nil {
							return err
						}
					}
				}
				return nil
			}
			interrupted := errors.New("simulated interruption")
			flush := func(context.Context) error {
				if stopAt == "main-commit" {
					return interrupted
				}
				return nil
			}
			remove := func(_ context.Context, ids []uint) error {
				if stopAt == "flush" {
					return interrupted
				}
				toRemove := names
				if stopAt == "partial-metrics" {
					toRemove = names[:1]
				}
				if err := removeNames(ids, toRemove); err != nil {
					return err
				}
				if stopAt != "complete" {
					return interrupted
				}
				return nil
			}
			err := deletePingTasksWith(ctx, db, []uint{task.Id}, flush, remove, func() error { return nil })
			if stopAt == "complete" && err != nil || stopAt != "complete" && !errors.Is(err, interrupted) {
				t.Fatalf("unexpected deletion result: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			closeMain(db)

			db, store = openMain(), openMetrics()
			defer closeMain(db)
			defer func() { _ = store.Close() }()
			cleaned, err := reconcilePingDeletionsWith(ctx, db, func(context.Context) error { return nil },
				func(_ context.Context, ids []uint) error { return removeNames(ids, names) })
			wantCleaned := 1
			if stopAt == "complete" {
				wantCleaned = 0
			}
			if err != nil || cleaned != wantCleaned {
				t.Fatalf("reopened cleanup=%d want=%d err=%v", cleaned, wantCleaned, err)
			}
			if err := db.Take(&models.PingTask{}, task.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("deleted task restored: %v", err)
			}
			if err := assertPingTaskIDAvailable(db, task.Id); err == nil {
				t.Fatal("reopen released deleted task ID")
			}
			for _, name := range names {
				for _, id := range []uint{task.Id, other.Id} {
					points, err := store.Series(ctx, metric.AggregateQuery{Query: metric.Query{MetricName: name, EntityID: "node",
						Start: now.Add(-time.Minute), End: now.Add(time.Minute), Tags: map[string]string{"task_id": fmt.Sprint(id)}},
						Aggregation: metric.AggLast, Interval: time.Minute, PreserveSeries: true}, now.Add(time.Minute))
					want := 1
					if id == task.Id {
						want = 0
					}
					if err != nil || len(points) != want {
						t.Fatalf("%s task %d points=%d want=%d err=%v", name, id, len(points), want, err)
					}
				}
			}
		})
	}
}

func TestPingDeletionRetryNeverCleansReusedLiveID(t *testing.T) {
	db := pinnedTaskDB(t)
	task := models.PingTask{Name: "reused", Type: "tcp", Target: "example.test:80", Interval: 60}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PingTaskDeletion{TaskID: task.Id}).Error; err != nil {
		t.Fatal(err)
	}
	called := false
	cleaned, err := reconcilePingDeletionsWith(t.Context(), db, func(context.Context) error { called = true; return nil },
		func(context.Context, []uint) error { called = true; return nil })
	if err == nil || cleaned != 0 || called {
		t.Fatalf("cleanup touched reused ID: cleaned=%d called=%v err=%v", cleaned, called, err)
	}
}

func TestPingDeletionWaitsForResultGuard(t *testing.T) {
	db := pinnedTaskDB(t)
	task := models.PingTask{Name: "guarded", Type: "tcp", Target: "example.test:80", Interval: 60}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	if err := pingguard.With(t.Context(), task.Id, func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		return deletePingTasksWith(ctx, db, []uint{task.Id}, func(context.Context) error { called.Store(true); return nil },
			func(context.Context, []uint) error { called.Store(true); return nil }, func() error { called.Store(true); return nil })
	}); !errors.Is(err, context.DeadlineExceeded) || called.Load() {
		t.Fatalf("deletion crossed result guard: called=%v err=%v", called.Load(), err)
	}
	if err := db.Take(&models.PingTask{}, task.Id).Error; err != nil {
		t.Fatalf("guarded task was deleted: %v", err)
	}
}
