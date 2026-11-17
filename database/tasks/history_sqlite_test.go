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
	"time"

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
		`{"c":"bm9kZQ","i":"00000000000000000000000000000000"}`,
		`{"c":"bm9kZQ","r":null,"i":"00000000000000000000000000000000"}`,
		`{"c":"bm9kZQ","r":9223372036854775808,"i":"00000000000000000000000000000000"}`,
		`{"c":"bm9kZQ","r":0,"i":"bad"}`,
		`{"c":"!!!","r":0,"i":"00000000000000000000000000000000"}`,
	} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, err := decodeResultCursor(cursor); !errors.Is(err, ErrInvalidHistoryParams) {
			t.Errorf("malformed cursor accepted: %s: %v", raw, err)
		}
	}
	// An empty identity marks a boundary-only cursor: valid to page from, never
	// valid to read a row with.
	boundary, err := decodeResultCursor(encodeResultCursor("node", 0, ""))
	if err != nil || boundary.Identity != "" || string(boundary.Client) != "node" {
		t.Fatalf("boundary cursor rejected: %+v %v", boundary, err)
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

func TestHistoryMissingOrMalformedIdentityIsSkippedButCursorsFailClosed(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`DROP TRIGGER task_results_assign_history_identity`).Error; err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{nil, "", "UPPERCASE", strings.Repeat("x", 1<<20)} {
		if err := db.Exec(`UPDATE task_results SET history_identity = ? WHERE rowid = 1`, bad).Error; err != nil {
			t.Fatal(err)
		}
		detail, err := getHistoryDetail(db, "fixture", "")
		if err != nil {
			t.Fatalf("malformed identity failed the page: %v", err)
		}
		if detail.SkippedMalformed != 1 || len(detail.Results) != 0 {
			t.Fatalf("detail = %+v, want one skipped row", detail)
		}
		if _, err := decodeResultCursor(encodeResultCursor("node", 1, "UPPERCASE")); !errors.Is(err, ErrInvalidHistoryParams) {
			t.Fatalf("malformed identity accepted in a cursor")
		}
	}
}

func TestHistoryMalformedParentIsSkippedAndOrderKept(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`INSERT INTO tasks VALUES (?, '[]', '', 2)`, strings.Repeat("y", 40)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tasks VALUES ('older', '[]', '', 3)`).Error; err != nil {
		t.Fatal(err)
	}
	page, err := listHistory(db, "")
	if err != nil {
		t.Fatalf("malformed parent failed the page: %v", err)
	}
	if page.SkippedMalformed != 1 || len(page.Tasks) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Tasks[0].TaskID != "older" || page.Tasks[1].TaskID != "fixture" {
		t.Fatalf("unexpected order: %+v", page.Tasks)
	}
}

func TestHistoryAllSkippedWindowStillPages(t *testing.T) {
	db, _ := historyWALFixture(t)
	// Longer than one page of unaddressable parents in front of the good rows:
	// the window fills with rows nobody can address, and paging must still move.
	for i := 0; i < 25; i++ {
		if err := db.Exec(`INSERT INTO tasks VALUES (?, '[]', '', ?)`, strings.Repeat("y", 40)+fmt.Sprint(i), 100-i).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := listHistory(db, "")
	if err != nil {
		t.Fatalf("all-skipped window failed the request: %v", err)
	}
	if len(page.Tasks) != 0 || page.SkippedMalformed != 20 || page.NextCursor == "" {
		t.Fatalf("page = %+v, want no tasks, 20 skipped and a cursor", page)
	}
	second, err := listHistory(db, page.NextCursor)
	if err != nil {
		t.Fatalf("boundary cursor unusable: %v", err)
	}
	if second.SkippedMalformed != 5 || len(second.Tasks) != 1 || second.Tasks[0].TaskID != "fixture" {
		t.Fatalf("second page = %+v, want the 5 skipped rows and the fixture", second)
	}
}

func TestHistoryAllSkippedResultWindowStillPages(t *testing.T) {
	db, _ := historyWALFixture(t)
	// Clients over the addressable bound sort before "node", so the first
	// window is entirely unaddressable.
	for i := 0; i < 25; i++ {
		if err := db.Exec(`INSERT INTO task_results(task_id,client,result,created_at) VALUES('fixture',?,'bad','2026-11-05 02:10:00+00:00')`, strings.Repeat("a", 129)+fmt.Sprint(i)).Error; err != nil {
			t.Fatal(err)
		}
	}
	detail, err := getHistoryDetail(db, "fixture", "")
	if err != nil {
		t.Fatalf("all-skipped result window failed the request: %v", err)
	}
	if len(detail.Results) != 0 || detail.SkippedMalformed != 20 || detail.NextCursor == "" {
		t.Fatalf("detail = %+v, want no results, 20 skipped and a cursor", detail)
	}
	if _, err := readHistoryChunk(db, "fixture", "output", detail.NextCursor, 0); !errors.Is(err, ErrInvalidHistoryParams) {
		t.Fatalf("cursor minted from a skipped row read its output: %v", err)
	}
	second, err := getHistoryDetail(db, "fixture", detail.NextCursor)
	if err != nil {
		t.Fatalf("boundary cursor unusable: %v", err)
	}
	if second.SkippedMalformed != 5 || len(second.Results) != 1 || second.Results[0].Client != "node" {
		t.Fatalf("second page = %+v, want the 5 skipped rows and the node result", second)
	}
}

func TestHistoryBoundaryCursorSurvivesLongAndBinaryValues(t *testing.T) {
	db, _ := historyWALFixture(t)
	// Control bytes expand sixfold under JSON escaping if they are carried raw;
	// the cursor must stay decodable and must still name the same boundary.
	for i := 0; i < 25; i++ {
		client := strings.Repeat("\x01", 128) + fmt.Sprint(i)
		if i%2 == 1 {
			client = strings.Repeat("b", 128) + fmt.Sprint(i)
		}
		if err := db.Exec(`INSERT INTO task_results(task_id,client,result,created_at) VALUES('fixture',?,'boundary','2026-11-05 02:20:00+00:00')`, client).Error; err != nil {
			t.Fatal(err)
		}
	}
	detail, err := getHistoryDetail(db, "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.NextCursor == "" {
		t.Fatal("no cursor minted for a page with more rows")
	}
	if _, err := decodeResultCursor(detail.NextCursor); err != nil {
		t.Fatalf("server minted a cursor it refuses: %v", err)
	}
	if _, err := getHistoryDetail(db, "fixture", detail.NextCursor); err != nil {
		t.Fatalf("server cursor rejected on reuse: %v", err)
	}
	// A boundary-only cursor may not read anyone's output.
	boundary := encodeResultCursor("node", 1, "")
	if _, err := readHistoryChunk(db, "fixture", "output", boundary, 0); !errors.Is(err, ErrInvalidHistoryParams) {
		t.Fatalf("boundary cursor read output: %v", err)
	}
}

func TestHistoryUntravelableBoundaryIsBlocked(t *testing.T) {
	db, _ := historyWALFixture(t)
	// Keys over the 512-octet carry bound cannot be named by any cursor, so the
	// response must report blocked rather than stopping as if the table ended.
	for i := 0; i < 21; i++ {
		if err := db.Exec(`INSERT INTO task_results(task_id,client,result,created_at) VALUES('fixture',?,'bad','2026-11-05 02:30:00+00:00')`, strings.Repeat("c", 600)+fmt.Sprint(i)).Error; err != nil {
			t.Fatal(err)
		}
	}
	detail, err := getHistoryDetail(db, "fixture", "")
	if err != nil {
		t.Fatalf("untravelable boundary failed the request: %v", err)
	}
	if !detail.Blocked || detail.NextCursor != "" || detail.SkippedMalformed != 20 {
		t.Fatalf("detail = %+v, want blocked with no cursor", detail)
	}
	for i := 0; i < 21; i++ {
		if err := db.Exec(`INSERT INTO tasks VALUES (?, '[]', '', ?)`, strings.Repeat("t", 600)+fmt.Sprint(i), 200-i).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := listHistory(db, "")
	if err != nil {
		t.Fatalf("untravelable parent boundary failed the request: %v", err)
	}
	if !page.Blocked || page.NextCursor != "" || page.SkippedMalformed != 20 {
		t.Fatalf("page = %+v, want blocked with no cursor", page)
	}
}

func TestHistoryBoundaryPrefersTheLastTravelableRow(t *testing.T) {
	db, _ := historyWALFixture(t)
	// One untravelable key at the window edge must not block the rows behind it:
	// the cursor comes from the last row that can travel.
	for i := 0; i < 19; i++ {
		if err := db.Exec(`INSERT INTO tasks VALUES (?, '[]', '', ?)`, strings.Repeat("t", 40)+fmt.Sprint(i), 50-i).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO tasks VALUES (?, '[]', '', 30)`, strings.Repeat("u", 600)).Error; err != nil {
		t.Fatal(err)
	}
	page, err := listHistory(db, "")
	if err != nil {
		t.Fatalf("mixed window failed: %v", err)
	}
	if page.Blocked || page.NextCursor == "" {
		t.Fatalf("page = %+v, want a cursor from the travelable rows", page)
	}
	second, err := listHistory(db, page.NextCursor)
	if err != nil {
		t.Fatalf("mixed-window cursor unusable: %v", err)
	}
	if second.SkippedMalformed == 0 {
		t.Fatalf("second page = %+v, want the untravelable row counted again", second)
	}
}

func TestHistoryAggregateOrdersInstantsNotText(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`DELETE FROM task_results`).Error; err != nil {
		t.Fatal(err)
	}
	// The same day written with two offsets: 01:00-05:00 is 06:00Z, so a text
	// minimum would report 06:00Z instead of the true earliest, 02:00Z.
	for _, row := range []struct{ client, at string }{
		{"b", "2026-11-05 01:00:00-05:00"},
		{"a", "2026-11-05 02:00:00+00:00"},
	} {
		if err := db.Exec(`INSERT INTO task_results(task_id,client,result,created_at) VALUES('fixture',?,'r',?)`, row.client, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := listHistory(db, "")
	if err != nil {
		t.Fatal(err)
	}
	got := page.Tasks[0].EarliestResultTime
	if got == nil || !got.Equal(time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("earliest = %v, want 02:00Z", got)
	}
}

func TestHistoryAggregateFlagsUnorderableTimestamps(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`DELETE FROM task_results`).Error; err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"garbage", ""} {
		if err := db.Exec(`INSERT INTO task_results(task_id,client,result,created_at) VALUES('fixture',?,?,?)`, at, "r", at).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := listHistory(db, "")
	if err != nil {
		t.Fatal(err)
	}
	summary := page.Tasks[0]
	if summary.ResultCount != 2 || summary.EarliestResultTime != nil || !summary.TimeIncomplete {
		t.Fatalf("summary = %+v, want two results, no earliest and an incomplete time", summary)
	}
}

func TestHistoryAggregateCountsAreExhaustive(t *testing.T) {
	db, _ := historyWALFixture(t)
	if err := db.Exec(`DELETE FROM task_results`).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		finished any
		exit     any
	}{
		{nil, nil},                         // pending
		{"2026-11-05 02:00:05+00:00", nil}, // unknown
		{"2026-11-05 02:00:05+00:00", 0},   // success
		{"2026-11-05 02:00:05+00:00", 7},   // failed
	} {
		if err := db.Exec(`INSERT INTO task_results(task_id,client,result,created_at,finished_at,exit_code) VALUES('fixture',?,'r','2026-11-05 02:00:00+00:00',?,?)`, fmt.Sprint(row.finished), row.finished, row.exit).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := listHistory(db, "")
	if err != nil {
		t.Fatal(err)
	}
	s := page.Tasks[0]
	if s.ResultCount != 4 || s.PendingCount != 1 || s.UnknownCount != 1 || s.SuccessCount != 1 || s.FailedCount != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if s.PendingCount+s.UnknownCount+s.SuccessCount+s.FailedCount != s.ResultCount {
		t.Fatalf("counts do not add up: %+v", s)
	}
}
