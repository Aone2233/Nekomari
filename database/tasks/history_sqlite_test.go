package tasks

import (
	"bytes"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Use an affinity-free fixture to exercise malformed legacy values, and a
// separate WAL database so these tests never touch the shared task database.
func historyWALFixture(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	pool, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path)+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := gorm.Open(sqlite.New(sqlite.Config{Conn: pool}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE tasks (task_id TEXT PRIMARY KEY, clients, command, history_order INTEGER)`,
		`CREATE TABLE task_results (task_id TEXT, client TEXT, result, created_at DATETIME, finished_at DATETIME, exit_code INTEGER)`,
		`INSERT INTO tasks VALUES ('fixture', '[]', '', 1)`,
		`INSERT INTO task_results VALUES ('fixture', 'node', '', '2026-11-05 02:00:00+00:00', NULL, NULL)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

func TestHistorySQLiteTypesAndDirectOnly(t *testing.T) {
	db, _ := historyWALFixture(t)
	cursor := encodeResultCursor("node", 1, "2026-11-05 02:00:00+00:00")
	for _, fixture := range []struct {
		name      string
		value     any
		available bool
		want      []byte
	}{
		{"text", "hello", true, []byte("hello")},
		{"blob", []byte{'x', 0, 0xff, 'y'}, true, []byte{'x', 0, 0xff, 'y'}},
		{"empty text", "", true, []byte{}},
		{"empty blob", []byte{}, true, []byte{}},
		{"null", nil, true, []byte{}},
		{"integer", 42, false, []byte{}},
		{"real", 1.5, false, []byte{}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if err := db.Exec(`UPDATE tasks SET command = ? WHERE task_id = 'fixture'`, fixture.value).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`UPDATE task_results SET result = ? WHERE task_id = 'fixture'`, fixture.value).Error; err != nil {
				t.Fatal(err)
			}
			detail, err := getHistoryDetail(db, "fixture", "")
			if err != nil || len(detail.Results) != 1 || detail.Results[0].OutputAvailable != fixture.available {
				t.Fatalf("metadata: %+v %v", detail, err)
			}
			for _, item := range []struct{ kind, cursor string }{{"command", ""}, {"output", cursor}} {
				chunk, err := readHistoryChunk(db, "fixture", item.kind, item.cursor, 0)
				if err != nil || chunk.Available != fixture.available || !bytes.Equal(chunk.Data, fixture.want) || chunk.HasMore || chunk.NextOffset != int64(len(fixture.want)) {
					t.Fatalf("%s: available=%t data=%x more=%t next=%d err=%v", item.kind, chunk.Available, chunk.Data, chunk.HasMore, chunk.NextOffset, err)
				}
			}
		})
	}
	for _, statement := range []string{
		`SELECT nekomari_history_blob(-1, 1, 0)`,
		`SELECT nekomari_history_blob(0, 1, -1)`,
		`SELECT nekomari_history_blob(0, -1, 0)`,
		`SELECT nekomari_history_blob(0, 999999, 0)`,
		`SELECT nekomari_history_blob('0', 1, 0)`,
	} {
		if err := db.Exec(statement).Error; err == nil {
			t.Fatalf("invalid direct call succeeded: %s", statement)
		}
	}
	if err := db.Exec(`CREATE VIEW history_forbidden AS SELECT nekomari_history_blob(0, rowid, 0) FROM tasks`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`SELECT * FROM history_forbidden`).Error; err == nil || !strings.Contains(err.Error(), "unsafe use") {
		t.Fatalf("stored view called DIRECTONLY function: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER history_forbidden_trigger AFTER UPDATE ON tasks BEGIN SELECT nekomari_history_blob(0, new.rowid, 0); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE tasks SET command = 'ignored' WHERE task_id = 'fixture'`).Error; err == nil || !strings.Contains(err.Error(), "unsafe use") {
		t.Fatalf("stored trigger called DIRECTONLY function: %v", err)
	}
}

func TestHistorySQLiteWALSnapshotAndReadOnly(t *testing.T) {
	db, path := historyWALFixture(t)
	if err := db.Exec(`UPDATE tasks SET command = ? WHERE task_id = 'fixture'`, "before").Error; err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	var before string
	if err := tx.Raw(`SELECT command FROM tasks WHERE task_id = 'fixture'`).Scan(&before).Error; err != nil || before != "before" {
		t.Fatalf("start read snapshot: %q %v", before, err)
	}
	if err := db.Exec(`UPDATE tasks SET command = ? WHERE task_id = 'fixture'`, "after").Error; err != nil {
		t.Fatalf("concurrent WAL update: %v", err)
	}
	chunk, err := readHistoryChunk(tx, "fixture", "command", "", 0)
	if err != nil || string(chunk.Data) != "before" || chunk.TotalBytes != 6 {
		t.Fatalf("chunk escaped its SQL snapshot: %q total=%d err=%v", chunk.Data, chunk.TotalBytes, err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	chunk, err = readHistoryChunk(db, "fixture", "command", "", 0)
	if err != nil || string(chunk.Data) != "after" || chunk.TotalBytes != 5 {
		t.Fatalf("later snapshot: %q total=%d err=%v", chunk.Data, chunk.TotalBytes, err)
	}
	reader, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var data []byte
	if err := reader.QueryRow(`SELECT nekomari_history_blob(0, rowid, 0) FROM tasks WHERE task_id = 'fixture'`).Scan(&data); err != nil || string(data) != "after" {
		t.Fatalf("read-only connection: %q %v", data, err)
	}
}
