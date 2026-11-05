package dbcore

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTaskHistoryOrderMigrationSurvivesVacuumAndRestart(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	createUpgradeFixtureDatabase(t, filepath.Join(dir, "data", "komari.db"),
		`CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO configs (key, value) VALUES ('system_version', '"v-old"')`,
		`CREATE TABLE tasks (task_id TEXT PRIMARY KEY, clients TEXT, command TEXT)`,
		`INSERT INTO tasks(task_id,clients,command) VALUES ('first','["node"]','secret-1')`,
		`INSERT INTO tasks(task_id,clients,command) VALUES ('gap','["node"]','secret-gap')`,
		`INSERT INTO tasks(task_id,clients,command) VALUES ('third','["node"]','secret-3')`,
		`DELETE FROM tasks WHERE task_id='gap'`,
		`CREATE TABLE task_results (task_id TEXT, client TEXT, result TEXT, exit_code INTEGER, finished_at TIMESTAMP, created_at TIMESTAMP)`,
		`INSERT INTO task_results(task_id,client,result,exit_code,created_at) VALUES ('third','node','secret-output',NULL,'2026-11-05 01:00:00+00:00')`,
	)
	SetVersionID("v-new")
	if err := Initialize(); err != nil {
		t.Fatalf("initialize old database: %v", err)
	}
	var before []models.Task
	if err := instance.Order("history_order").Find(&before).Error; err != nil || len(before) != 2 {
		t.Fatalf("migrated tasks: %+v %v", before, err)
	}
	if before[0].TaskId != "first" || before[1].TaskId != "third" || before[0].HistoryOrder == nil || before[1].HistoryOrder == nil || *before[1].HistoryOrder <= *before[0].HistoryOrder {
		t.Fatalf("lost legacy insertion order: %+v", before)
	}
	firstOrder, thirdOrder := *before[0].HistoryOrder, *before[1].HistoryOrder
	if !instance.Migrator().HasIndex(&models.TaskResult{}, "idx_task_results_history") || !instance.Migrator().HasIndex(&models.TaskResult{}, "idx_task_results_history_identity") || !instance.Migrator().HasIndex(&models.Task{}, "idx_tasks_history_order") {
		t.Fatal("history indexes were not created")
	}
	var beforeID string
	if err := instance.Raw(`SELECT history_identity FROM task_results WHERE task_id = 'third'`).Scan(&beforeID).Error; err != nil || len(beforeID) != 32 {
		t.Fatalf("missing migrated result identity: %q %v", beforeID, err)
	}
	if err := ReclaimSpace(t.Context()); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	if err := backfillTaskHistoryOrder(instance); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	resetDatabaseState()
	SetVersionID("v-new")
	if err := Initialize(); err != nil {
		t.Fatalf("restart after vacuum: %v", err)
	}
	var after []models.Task
	if err := instance.Order("history_order").Find(&after).Error; err != nil || len(after) != 2 {
		t.Fatalf("tasks after restart: %+v %v", after, err)
	}
	if after[0].HistoryOrder == nil || after[1].HistoryOrder == nil || *after[0].HistoryOrder != firstOrder || *after[1].HistoryOrder != thirdOrder || after[0].Command != "secret-1" || after[1].Command != "secret-3" {
		t.Fatalf("vacuum or restart changed task data/order: %+v", after)
	}
	var result models.TaskResult
	if err := instance.Where("task_id = ?", "third").First(&result).Error; err != nil || result.Result != "secret-output" || result.HistoryIdentity != beforeID {
		t.Fatalf("result changed during migration: %+v %v", result, err)
	}
	// Simulate a task written by the previous version during an image rollback.
	if err := instance.Exec(`INSERT INTO tasks(task_id,clients,command) VALUES ('rollback-task','["node"]','during-rollback')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := instance.Exec(`INSERT INTO task_results(task_id,client,result,created_at) VALUES ('rollback-task','node','rollback-result','2026-11-05 02:00:00+00:00')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillTaskHistoryOrder(instance); err != nil {
		t.Fatalf("reconcile rollback task: %v", err)
	}
	var reconciled models.Task
	if err := instance.Where("task_id = ?", "rollback-task").First(&reconciled).Error; err != nil || reconciled.HistoryOrder == nil || *reconciled.HistoryOrder <= thirdOrder {
		t.Fatalf("rollback task is not ordered after existing tasks: %+v %v", reconciled, err)
	}
	var inserted models.TaskResult
	if err := instance.Where("task_id = ?", "rollback-task").First(&inserted).Error; err != nil || len(inserted.HistoryIdentity) != 32 || inserted.HistoryIdentity == beforeID {
		t.Fatalf("rollback insert identity: %+v %v", inserted, err)
	}
}

func TestTaskResultHistoryIdentityReconcilesOldRowsAndPreservesBytes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "results.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, query := range []string{
		`CREATE TABLE task_results(task_id TEXT, client TEXT, result BLOB, history_identity TEXT)`,
		`CREATE UNIQUE INDEX idx_task_results_history_identity ON task_results(history_identity)`,
		`INSERT INTO task_results(rowid,task_id,client,result) VALUES (-2,'task','node',x'00ff11'),(0,'task','node',x'01fe22')`,
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	var plan []struct{ Detail string }
	if err := db.Raw(`EXPLAIN QUERY PLAN UPDATE task_results SET history_identity = lower(hex(randomblob(16))) WHERE history_identity IS NULL`).Scan(&plan).Error; err != nil || len(plan) == 0 || !strings.Contains(plan[0].Detail, "idx_task_results_history_identity") {
		t.Fatalf("unindexed identity reconciliation: %+v %v", plan, err)
	}
	if err := backfillTaskResultHistoryIdentity(db); err != nil {
		t.Fatal(err)
	}
	var first []struct {
		RowID int64
		ID    string
		Bytes string
	}
	if err := db.Raw(`SELECT rowid AS row_id, history_identity AS id, hex(result) AS bytes FROM task_results ORDER BY rowid`).Scan(&first).Error; err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(first[0].ID) != 32 || len(first[1].ID) != 32 || first[0].ID == first[1].ID || first[0].Bytes != "00FF11" || first[1].Bytes != "01FE22" {
		t.Fatalf("backfill changed contents or failed identity: %+v", first)
	}
	if err := backfillTaskResultHistoryIdentity(db); err != nil {
		t.Fatal(err)
	}
	var again []struct {
		RowID int64
		ID    string
		Bytes string
	}
	if err := db.Raw(`SELECT rowid AS row_id, history_identity AS id, hex(result) AS bytes FROM task_results ORDER BY rowid`).Scan(&again).Error; err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if again[i] != first[i] {
			t.Fatalf("repeat backfill changed result: %+v -> %+v", first[i], again[i])
		}
	}
	if err := db.Exec(`INSERT INTO task_results(task_id,client,result) VALUES ('old-binary','node',x'00')`).Error; err != nil {
		t.Fatal(err)
	}
	var inserted string
	if err := db.Raw(`SELECT history_identity FROM task_results WHERE task_id = 'old-binary'`).Scan(&inserted).Error; err != nil || len(inserted) != 32 || inserted == first[0].ID || inserted == first[1].ID {
		t.Fatalf("legacy-style insert identity: %q %v", inserted, err)
	}
	if err := db.Exec(`INSERT INTO task_results(task_id,client,result,history_identity) VALUES ('duplicate','node',x'00',?)`, inserted).Error; err == nil {
		t.Fatal("duplicate identity accepted")
	}
	// Simulate a legacy restore or rollback that removed the trigger entirely.
	if err := db.Exec(`DROP TRIGGER task_results_assign_history_identity`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO task_results(task_id,client,result) VALUES ('unmarked','node',x'10')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillTaskResultHistoryIdentity(db); err != nil {
		t.Fatal(err)
	}
	var reconciled string
	if err := db.Raw(`SELECT history_identity FROM task_results WHERE task_id = 'unmarked'`).Scan(&reconciled).Error; err != nil || len(reconciled) != 32 || reconciled == inserted {
		t.Fatalf("unmarked row not reconciled: %q %v", reconciled, err)
	}
	var still string
	if err := db.Raw(`SELECT history_identity FROM task_results WHERE task_id = 'old-binary'`).Scan(&still).Error; err != nil || still != inserted {
		t.Fatalf("existing identity changed during reconcile: %q %v", still, err)
	}
}

func TestTaskResultHistoryIdentityRollbackOnUniqueFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "results.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, query := range []string{
		`CREATE TABLE task_results(history_identity TEXT CHECK(history_identity IS NULL))`,
		`INSERT INTO task_results(history_identity) VALUES(NULL)`,
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := backfillTaskResultHistoryIdentity(db); err == nil {
		t.Fatal("failed backfill unexpectedly succeeded")
	}
	var missing, triggers int64
	if err := db.Raw(`SELECT count(*) FROM task_results WHERE history_identity IS NULL`).Scan(&missing).Error; err != nil || missing != 1 {
		t.Fatalf("failed backfill changed existing row: %d %v", missing, err)
	}
	if err := db.Raw(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'task_results_assign_history_identity'`).Scan(&triggers).Error; err != nil || triggers != 0 {
		t.Fatalf("failed backfill created trigger: %d %v", triggers, err)
	}
}

func TestTaskHistoryBackfillMultipleRollbackRowsAndExhaustion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tasks.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, query := range []string{
		`CREATE TABLE tasks(task_id TEXT PRIMARY KEY, history_order INTEGER)`,
		`CREATE INDEX idx_tasks_history_order ON tasks(history_order,task_id)`,
		`INSERT INTO tasks(rowid, task_id, history_order) VALUES (2, 'first', 5), (4, 'tie-a', 9), (6, 'tie-b', 9)`,
		`INSERT INTO tasks(rowid, task_id) VALUES (-7, 'rollback-a'), (3, 'rollback-b'), (9223372036854775807, 'rollback-c')`,
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	var plan []struct{ Detail string }
	if err := db.Raw(`EXPLAIN QUERY PLAN SELECT count(*) FROM tasks WHERE history_order IS NULL`).Scan(&plan).Error; err != nil || len(plan) == 0 || !strings.Contains(plan[0].Detail, "idx_tasks_history_order") {
		t.Fatalf("unindexed reconciliation discovery: %+v %v", plan, err)
	}
	if err := backfillTaskHistoryOrder(db); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		TaskID       string
		HistoryOrder int64
	}
	if err := db.Raw(`SELECT task_id, history_order FROM tasks ORDER BY history_order, task_id`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		id    string
		order int64
	}{{"first", 5}, {"tie-a", 9}, {"tie-b", 9}, {"rollback-a", 10}, {"rollback-b", 11}, {"rollback-c", 12}} {
		if rows[i].TaskID != want.id || rows[i].HistoryOrder != want.order {
			t.Fatalf("reconciled row %d: %+v", i, rows[i])
		}
	}
	if err := backfillTaskHistoryOrder(db); err != nil {
		t.Fatalf("idempotent backfill: %v", err)
	}
	if err := db.Exec(`UPDATE tasks SET history_order = ? WHERE task_id = 'first'`, int64(math.MaxInt64)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tasks(rowid,task_id) VALUES (8,'overflow-a'), (10,'overflow-b')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillTaskHistoryOrder(db); err == nil {
		t.Fatal("history order overflow accepted")
	}
	var unnumbered int64
	if err := db.Raw(`SELECT count(*) FROM tasks WHERE history_order IS NULL`).Scan(&unnumbered).Error; err != nil || unnumbered != 2 {
		t.Fatalf("failed backfill partially wrote task order: %d %v", unnumbered, err)
	}
}
