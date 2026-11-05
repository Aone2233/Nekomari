package tasks

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
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
		`CREATE TABLE task_results (task_id TEXT, client TEXT, result, created_at DATETIME, finished_at DATETIME, exit_code INTEGER, history_identity TEXT)`,
		`CREATE UNIQUE INDEX idx_task_results_history_identity ON task_results(history_identity)`,
		`CREATE TRIGGER task_results_assign_history_identity AFTER INSERT ON task_results WHEN NEW.history_identity IS NULL BEGIN UPDATE task_results SET history_identity = lower(hex(randomblob(16))) WHERE rowid = NEW.rowid; END`,
		`INSERT INTO tasks VALUES ('fixture', '[]', '', 1)`,
		`INSERT INTO task_results(task_id,client,result,created_at) VALUES ('fixture', 'node', '', '2026-11-05 02:00:00+00:00')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

func TestHistorySQLiteTypesAndDirectOnly(t *testing.T) {
	db, _ := historyWALFixture(t)
	var identity string
	if err := db.Raw(`SELECT history_identity FROM task_results WHERE rowid = 1`).Scan(&identity).Error; err != nil {
		t.Fatal(err)
	}
	cursor := encodeResultCursor("node", 1, identity)
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

func TestHistoryResultCursorsAcceptSQLiteRowIDZeroAndNegative(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`DELETE FROM task_results`).Error; err != nil {
		t.Fatal(err)
	}
	ids := []int64{math.MinInt64, -19, -18, -17, -16, -15, -14, -13, -12, -11, -10, -9, -8, -7, -6, -5, -4, -3, -2, -1, 0, 1, math.MaxInt64}
	for _, id := range ids {
		if err := db.Exec(`INSERT INTO task_results(rowid,task_id,client,result,created_at) VALUES(?, 'fixture', 'node', ?, '2026-11-05 02:00:00+00:00')`, id, fmt.Sprintf("result-%d", id)).Error; err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[int64]bool)
	for cursor := ""; ; {
		detail, err := getHistoryDetail(db, "fixture", cursor)
		if err != nil {
			t.Fatalf("detail page: %v", err)
		}
		for _, result := range detail.Results {
			decoded, err := decodeResultCursor(result.Cursor)
			if err != nil || seen[decoded.RowID] {
				t.Fatalf("invalid or duplicate cursor: %+v %v", decoded, err)
			}
			seen[decoded.RowID] = true
			chunk, err := readHistoryChunk(db, "fixture", "output", result.Cursor, 0)
			if err != nil || string(chunk.Data) != fmt.Sprintf("result-%d", decoded.RowID) {
				t.Errorf("rowid %d: data=%q err=%v", decoded.RowID, chunk.Data, err)
			}
		}
		if detail.NextCursor == "" {
			break
		}
		cursor = detail.NextCursor
	}
	if len(seen) != len(ids) {
		t.Fatalf("visited %d of %d rows", len(seen), len(ids))
	}
	for _, raw := range []string{
		`{"c":"node","i":"00000000000000000000000000000000"}`,
		`{"c":"node","r":null,"i":"00000000000000000000000000000000"}`,
		`{"c":"node","r":9223372036854775808,"i":"00000000000000000000000000000000"}`,
		`{"c":"node","r":0}`,
		`{"c":"node","r":0,"i":"bad"}`,
	} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, err := decodeResultCursor(cursor); !errors.Is(err, ErrInvalidHistoryParams) {
			t.Errorf("malformed cursor accepted: %s: %v", raw, err)
		}
	}
}

func TestHistoryVacuumCannotRetargetDuplicateResultCursor(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`DELETE FROM task_results`).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id     int
		client string
		result string
	}{{1, "node", "result-1"}, {2, "other", "removed"}, {3, "node", "result-3"}, {4, "node", "result-4"}, {5, "node", "result-5"}} {
		if err := db.Exec(`INSERT INTO task_results(rowid,task_id,client,result,created_at) VALUES(?, 'fixture', ?, ?, '2026-11-05 02:00:00+00:00')`, row.id, row.client, row.result).Error; err != nil {
			t.Fatal(err)
		}
	}
	detail, err := getHistoryDetail(db, "fixture", "")
	if err != nil || len(detail.Results) != 5 {
		t.Fatalf("initial detail: %+v %v", detail, err)
	}
	var cursor, untouched string
	for _, row := range detail.Results {
		parsed, err := decodeResultCursor(row.Cursor)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.RowID == 3 {
			cursor = row.Cursor
		}
		if parsed.RowID == 1 {
			untouched = row.Cursor
		}
	}
	if cursor == "" || untouched == "" {
		t.Fatal("missing original cursor")
	}
	if err := db.Exec(`DELETE FROM task_results WHERE rowid = 2`).Error; err != nil {
		t.Fatal(err)
	}
	// Model a maintenance rebuild that changes schema objects before VACUUM;
	// current SQLite may keep rowids when an index or trigger is present.
	if err := db.Exec(`DROP TRIGGER task_results_assign_history_identity`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DROP INDEX idx_task_results_history_identity`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`VACUUM`).Error; err != nil {
		t.Fatal(err)
	}
	chunk, err := readHistoryChunk(db, "fixture", "output", cursor, 0)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale cursor retargeted different output: %q %v", chunk.Data, err)
	}
	if _, err := getHistoryDetail(db, "fixture", cursor); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale pagination cursor accepted: %v", err)
	}
	chunk, err = readHistoryChunk(db, "fixture", "output", untouched, 0)
	if err != nil || string(chunk.Data) != "result-1" {
		t.Fatalf("unchanged cursor failed after vacuum: %q %v", chunk.Data, err)
	}
	if err := db.Exec(`DELETE FROM task_results WHERE rowid = 3`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO task_results(rowid,task_id,client,result,created_at,history_identity) VALUES(3,'fixture','node','replacement','2026-11-05 02:00:00+00:00',lower(hex(randomblob(16))))`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := readHistoryChunk(db, "fixture", "output", cursor, 0); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("reused rowid and timestamp retargeted old cursor: %v", err)
	}
}

func TestHistoryMissingOrMalformedIdentityFailsClosed(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`DROP TRIGGER task_results_assign_history_identity`).Error; err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{nil, "", "UPPERCASE", strings.Repeat("x", 1<<20)} {
		if err := db.Exec(`UPDATE task_results SET history_identity = ? WHERE rowid = 1`, bad).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := getHistoryDetail(db, "fixture", ""); !errors.Is(err, ErrInvalidHistoryParams) {
			t.Fatalf("malformed identity did not fail closed: %v", err)
		}
	}
}
