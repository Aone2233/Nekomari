package pingrollback

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type rollbackFixture struct {
	db                   *gorm.DB
	first, empty, legacy models.PingTask
}

func newRollbackFixture(t *testing.T) rollbackFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_foreign_keys=1&_busy_timeout=5000", filepath.Join(t.TempDir(), "main.db"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.Client{}, &models.PingTask{}, &models.PingRecord{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"node-a", "node-b"} {
		if err := db.Create(&models.Client{UUID: id, Token: "token-" + id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	first := models.PingTask{Weight: 7, Name: "v6", Type: "tcp", Target: "example.test:80", Reference: "gateway", Interval: 60,
		Clients: models.StringArray{"node-a", "node-b"}, TargetFamily: "ipv6", PinnedGeneration: "0123456789abcdef0123456789abcdef"}
	empty := models.PingTask{Weight: 8, Name: "future", Type: "tcp", Target: "example.test:80", Interval: 120,
		DefaultOn: true, Clients: models.StringArray{}, TargetFamily: "ipv4", PinnedGeneration: "fedcba9876543210fedcba9876543210"}
	legacy := models.PingTask{Weight: 9, Name: "legacy", Type: "tcp", Target: "192.0.2.1:80", Interval: 60,
		Clients: models.StringArray{"node-a"}}
	for _, task := range []*models.PingTask{&first, &empty, &legacy} {
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.PingRecord{Client: "node-a", TaskId: first.Id, Value: 42, Time: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	var fks []struct {
		Table    string
		OnDelete string
	}
	if err := db.Raw(`PRAGMA foreign_key_list('ping_records')`).Scan(&fks).Error; err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fk := range fks {
		if fk.Table == "ping_tasks" && fk.OnDelete == "CASCADE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture lacks cascading task FK: %+v", fks)
	}
	return rollbackFixture{db: db, first: first, empty: empty, legacy: legacy}
}

func (f rollbackFixture) task(t *testing.T, id uint) models.PingTask {
	t.Helper()
	var task models.PingTask
	if err := f.db.Take(&task, id).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func (f rollbackFixture) assertHistory(t *testing.T) {
	t.Helper()
	var n int64
	if err := f.db.Model(&models.PingRecord{}).Where("task_id = ?", f.first.Id).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("legacy history: %d, %v", n, err)
	}
	var violations []struct{ Table string }
	if err := f.db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil || len(violations) != 0 {
		t.Fatalf("foreign key violations: %+v, %v", violations, err)
	}
}

func TestQuarantinePreservesForeignKeysIDsAndRestores(t *testing.T) {
	f := newRollbackFixture(t)
	beforeFirst, beforeEmpty, beforeLegacy := f.task(t, f.first.Id), f.task(t, f.empty.Id), f.task(t, f.legacy.Id)
	beforeRaw, err := readClients(f.db, f.first.Id)
	if err != nil {
		t.Fatal(err)
	}
	count, err := Quarantine(f.db)
	if err != nil || count != 2 {
		t.Fatalf("quarantine: %d, %v", count, err)
	}
	for _, id := range []uint{f.first.Id, f.empty.Id} {
		task := f.task(t, id)
		if task.Interval != 0 || task.DefaultOn || !reflect.DeepEqual(task.Clients, models.StringArray{parkedClient}) {
			t.Fatalf("not parked: %+v", task)
		}
	}
	if got := f.task(t, f.legacy.Id); !reflect.DeepEqual(got, beforeLegacy) {
		t.Fatalf("legacy changed: %+v", got)
	}
	f.assertHistory(t)
	if err := f.db.Exec(`UPDATE ping_tasks SET interval=10 WHERE id=?`, f.first.Id).Error; err == nil {
		t.Fatal("parked task update was not blocked")
	}
	if err := f.db.Exec(`DELETE FROM ping_tasks WHERE id=?`, f.first.Id).Error; err == nil {
		t.Fatal("parked task delete was not blocked")
	}
	if err := f.db.Exec(`INSERT OR REPLACE INTO ping_tasks (id, name, type, target, interval) VALUES (?, 'replacement', 'tcp', '192.0.2.1:80', 60)`, f.first.Id).Error; err == nil {
		t.Fatal("parked task replacement was not blocked")
	}
	if err := f.db.Exec(`DELETE FROM nekomari_ping_rollback_rows`).Error; err == nil {
		t.Fatal("quarantine rows were not sealed")
	}
	f.assertHistory(t)
	// The old startup normalizer and all-clients expansion must not touch
	// parked rows, including one originally default-on with no clients.
	if err := f.db.Exec(`UPDATE ping_tasks SET clients='[]' WHERE clients IS NULL OR clients='' OR clients='[]' OR clients='null'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE ping_tasks SET clients='["node-a","node-b"]' WHERE all_clients=1 AND (clients IS NULL OR clients='' OR clients='[]' OR clients='null')`).Error; err != nil {
		t.Fatal(err)
	}
	newTask := models.PingTask{Name: "old-panel-created", Type: "tcp", Target: "192.0.2.2:80", Interval: 60}
	if err := f.db.Create(&newTask).Error; err != nil || newTask.Id <= f.legacy.Id {
		t.Fatalf("new task ID reused: %+v %v", newTask, err)
	}
	count, err = Restore(f.db)
	if err != nil || count != 2 {
		t.Fatalf("restore: %d, %v", count, err)
	}
	for _, original := range []models.PingTask{beforeFirst, beforeEmpty} {
		got := f.task(t, original.Id)
		if got.PinnedGeneration == original.PinnedGeneration || len(got.PinnedGeneration) != 32 {
			t.Fatalf("generation not renewed: %+v", got)
		}
		got.PinnedGeneration = original.PinnedGeneration
		if !reflect.DeepEqual(got, original) {
			t.Fatalf("restored %+v, want %+v", got, original)
		}
	}
	afterRaw, err := readClients(f.db, f.first.Id)
	if err != nil || !reflect.DeepEqual(afterRaw, beforeRaw) {
		t.Fatalf("raw clients changed: %+v -> %+v (%v)", beforeRaw, afterRaw, err)
	}
	if got := f.task(t, newTask.Id); got.Name != newTask.Name {
		t.Fatalf("new task changed: %+v", got)
	}
	if f.db.Migrator().HasTable(&snapshot{}) || f.db.Migrator().HasTable(&row{}) {
		t.Fatal("quarantine tables remain")
	}
	f.assertHistory(t)
}

func TestQuarantineRollsBackOnMidwayFailure(t *testing.T) {
	f := newRollbackFixture(t)
	before := f.task(t, f.first.Id)
	if err := f.db.Exec(fmt.Sprintf(`CREATE TRIGGER fixture_fail BEFORE UPDATE ON ping_tasks WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'injected'); END`, f.empty.Id)).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := Quarantine(f.db); n != 0 || err == nil {
		t.Fatalf("quarantine = %d, %v", n, err)
	}
	if got := f.task(t, f.first.Id); !reflect.DeepEqual(got, before) {
		t.Fatalf("partial quarantine: %+v", got)
	}
	if f.db.Migrator().HasTable(&snapshot{}) || f.db.Migrator().HasTable(&row{}) {
		t.Fatal("failed quarantine left tables")
	}
	f.assertHistory(t)
}

func TestRestoreRejectsChangedRosterAndDamagedSealAtomically(t *testing.T) {
	for _, change := range []string{"new-node", "missing-node", "missing-trigger", "bad-row", "extra-column"} {
		t.Run(change, func(t *testing.T) {
			f := newRollbackFixture(t)
			if _, err := Quarantine(f.db); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "new-node":
				if err := f.db.Create(&models.Client{UUID: "new", Token: "new-token"}).Error; err != nil {
					t.Fatal(err)
				}
			case "missing-node":
				if err := f.db.Where("uuid = ?", "node-b").Delete(&models.Client{}).Error; err != nil {
					t.Fatal(err)
				}
			case "missing-trigger":
				if err := f.db.Exec(`DROP TRIGGER nekomari_ping_rollback_task_update`).Error; err != nil {
					t.Fatal(err)
				}
			case "bad-row":
				if err := f.db.Exec(`DROP TRIGGER nekomari_ping_rollback_rows_update`).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.db.Exec(`UPDATE nekomari_ping_rollback_rows SET data = ? WHERE task_id = ?`, []byte("bad"), f.first.Id).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.db.Exec(triggerSQL[4]).Error; err != nil {
					t.Fatal(err)
				}
			case "extra-column":
				if err := f.db.Exec(`ALTER TABLE ping_tasks ADD COLUMN future_data TEXT`).Error; err != nil {
					t.Fatal(err)
				}
			}
			if n, err := Restore(f.db); n != 0 || !errors.Is(err, ErrConflict) {
				t.Fatalf("restore = %d, %v", n, err)
			}
			if got := f.task(t, f.first.Id); got.Interval != 0 || got.PinnedGeneration != f.first.PinnedGeneration {
				t.Fatalf("partial restore: %+v", got)
			}
			if !f.db.Migrator().HasTable(&snapshot{}) || !f.db.Migrator().HasTable(&row{}) {
				t.Fatal("failed restore removed state")
			}
			f.assertHistory(t)
		})
	}
}

func TestQuarantineRejectsUnknownSchemaAndPartialState(t *testing.T) {
	for _, issue := range []string{"extra-column", "existing-table", "bad-generation"} {
		t.Run(issue, func(t *testing.T) {
			f := newRollbackFixture(t)
			switch issue {
			case "extra-column":
				if err := f.db.Exec(`ALTER TABLE ping_tasks ADD COLUMN future_data TEXT`).Error; err != nil {
					t.Fatal(err)
				}
			case "existing-table":
				if err := f.db.Exec(`CREATE TABLE nekomari_ping_rollback_rows (task_id INTEGER PRIMARY KEY)`).Error; err != nil {
					t.Fatal(err)
				}
			case "bad-generation":
				if err := f.db.Model(&models.PingTask{}).Where("id = ?", f.empty.Id).Update("pinned_generation", "bad").Error; err != nil {
					t.Fatal(err)
				}
			}
			if n, err := Quarantine(f.db); n != 0 || err == nil {
				t.Fatalf("quarantine = %d, %v", n, err)
			}
			if got := f.task(t, f.first.Id); got.Interval != f.first.Interval {
				t.Fatalf("partial quarantine: %+v", got)
			}
			f.assertHistory(t)
		})
	}
}
